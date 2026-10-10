package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// loadLayout reads every bookmark id ordered by page, position into the
// [][]int64 shape internal/layout works on, plus each id's stored
// (page, position) slot for the writeLayout diff. An empty table
// yields a single empty page — the no-bookmarks board.
func loadLayout(ctx context.Context, q querier) ([][]int64, map[int64][2]int, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, page, position FROM bookmarks ORDER BY page, position, id`)
	if err != nil {
		return nil, nil, fmt.Errorf("load layout: %w", err)
	}
	defer rows.Close()

	L := [][]int64{{}}
	slots := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var page, pos int
		if err := rows.Scan(&id, &page, &pos); err != nil {
			return nil, nil, fmt.Errorf("load layout: %w", err)
		}
		if page < 0 {
			page = 0
		}
		for len(L) <= page {
			L = append(L, []int64{})
		}
		L[page] = append(L[page], id)
		slots[id] = [2]int{page, pos}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("load layout: %w", err)
	}
	return L, slots, nil
}

// writeLayout persists the slot next assigns to each id, updating only
// rows whose stored slot (in prev) differs — or that prev does not know
// (freshly inserted rows still on their placeholder slot).
func writeLayout(ctx context.Context, tx *sql.Tx, prev map[int64][2]int, next [][]int64) error {
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE bookmarks SET page = ?, position = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	defer stmt.Close()

	for p, pg := range next {
		for pos, id := range pg {
			if old, ok := prev[id]; ok && old[0] == p && old[1] == pos {
				continue
			}
			if _, err := stmt.ExecContext(ctx, p, pos, id); err != nil {
				return fmt.Errorf("write layout: bookmark %d: %w", id, err)
			}
		}
	}
	return nil
}

// slotOf reports the (page, position) a layout assigns to id.
func slotOf(L [][]int64, id int64) (int, int) {
	for p, pg := range L {
		if i := slices.Index(pg, id); i >= 0 {
			return p, i
		}
	}
	return 0, 0
}

// storedGrid reads the persisted grid value inside a transaction,
// merged over the default like SettingsStore.Get does.
func storedGrid(ctx context.Context, q querier) (Grid, error) {
	var raw string
	err := q.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'grid'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultGrid, nil
	}
	if err != nil {
		return Grid{}, fmt.Errorf("read grid setting: %w", err)
	}
	g := DefaultGrid
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return Grid{}, fmt.Errorf("settings key %q: %w", "grid", err)
	}
	return g, nil
}

// gridCap is the page capacity — grid rows*cols — inside a transaction.
func gridCap(ctx context.Context, q querier) (int, error) {
	g, err := storedGrid(ctx, q)
	if err != nil {
		return 0, err
	}
	if cap := g.Rows * g.Cols; cap > 0 {
		return cap, nil
	}
	return DefaultGrid.Rows * DefaultGrid.Cols, nil
}

// groupByPage packs item indices into pages following the page info
// each item carries: pages in ascending recorded-page order (renumbered
// 0.. by their rank), items within a page ordered by recorded position
// and then file order. Returns nil unless every item has a Page.
func groupByPage(items []NewBookmark) [][]int64 {
	byPage := map[int][]int64{}
	for i, nb := range items {
		if nb.Page == nil {
			return nil
		}
		byPage[*nb.Page] = append(byPage[*nb.Page], int64(i))
	}
	if len(items) == 0 {
		return nil
	}
	L := make([][]int64, 0, len(byPage))
	for _, p := range slices.Sorted(maps.Keys(byPage)) {
		idxs := byPage[p]
		slices.SortStableFunc(idxs, func(a, b int64) int {
			return cmp.Compare(items[a].Position, items[b].Position)
		})
		L = append(L, idxs)
	}
	return L
}
