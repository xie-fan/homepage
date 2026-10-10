package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// bookmarkCols is the shared SELECT column list; keep it in sync with
// scanBookmark.
const bookmarkCols = `id, title, url, position, icon_path, icon_status, created_at, updated_at`

// NewSQLite returns a Store whose interfaces are implemented on db
// (opened and migrated via internal/db).
func NewSQLite(db *sql.DB) *Store {
	return &Store{
		BookmarkStore: &sqliteBookmarks{db: db},
		SettingsStore: &sqliteSettings{db: db},
	}
}

var (
	_ BookmarkStore = (*sqliteBookmarks)(nil)
	_ SettingsStore = (*sqliteSettings)(nil)
)

// sqliteBookmarks implements BookmarkStore and sqliteSettings implements
// SettingsStore, both over the same *sql.DB. They are separate types
// because the interfaces' Get methods have different signatures.
type sqliteBookmarks struct{ db *sql.DB }
type sqliteSettings struct{ db *sql.DB }

func (s *sqliteBookmarks) List(ctx context.Context) ([]Bookmark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookmarkCols+` FROM bookmarks ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer rows.Close()

	bs := []Bookmark{}
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			return nil, fmt.Errorf("list bookmarks: %w", err)
		}
		bs = append(bs, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	return bs, nil
}

func (s *sqliteBookmarks) Get(ctx context.Context, id int64) (Bookmark, error) {
	return getBookmark(ctx, s.db, id)
}

func (s *sqliteBookmarks) Create(ctx context.Context, title, url string) (Bookmark, error) {
	bs, err := s.AppendMany(ctx, []NewBookmark{{Title: title, URL: url}})
	if err != nil {
		return Bookmark{}, err
	}
	return bs[0], nil
}

func (s *sqliteBookmarks) Update(ctx context.Context, id int64, title, url string) (Bookmark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, fmt.Errorf("update bookmark %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once committed

	b, err := getBookmark(ctx, tx, id)
	if err != nil {
		return Bookmark{}, err
	}
	// A new URL invalidates the cached icon; it will be refetched.
	if b.URL != url {
		b.IconPath, b.IconStatus = "", IconPending
	}
	b.Title, b.URL = title, url
	b.UpdatedAt = time.Now().UTC()

	if _, err := tx.ExecContext(ctx,
		`UPDATE bookmarks
		 SET title = ?, url = ?, icon_path = ?, icon_status = ?, updated_at = ?
		 WHERE id = ?`,
		b.Title, b.URL, b.IconPath, b.IconStatus, formatTime(b.UpdatedAt), id); err != nil {
		return Bookmark{}, fmt.Errorf("update bookmark %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Bookmark{}, fmt.Errorf("update bookmark %d: %w", id, err)
	}
	return b, nil
}

func (s *sqliteBookmarks) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM bookmarks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}
	// Compact the remaining positions to 0..n-1 in the same transaction.
	ids, err := listIDs(ctx, tx)
	if err != nil {
		return err
	}
	if err := setPositions(ctx, tx, ids); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}
	return nil
}

func (s *sqliteBookmarks) Reorder(ctx context.Context, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reorder bookmarks: %w", err)
	}
	defer tx.Rollback()

	existing, err := listIDs(ctx, tx)
	if err != nil {
		return err
	}
	if err := checkPermutation(existing, ids); err != nil {
		return err
	}
	if err := setPositions(ctx, tx, ids); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reorder bookmarks: %w", err)
	}
	return nil
}

func (s *sqliteBookmarks) SetIcon(ctx context.Context, id int64, path, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET icon_path = ?, icon_status = ?, updated_at = ? WHERE id = ?`,
		path, status, formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("set icon on bookmark %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}
	return nil
}

func (s *sqliteBookmarks) ReplaceAll(ctx context.Context, items []NewBookmark) ([]Bookmark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("replace bookmarks: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmarks`); err != nil {
		return nil, fmt.Errorf("replace bookmarks: %w", err)
	}
	bs, err := insertAll(ctx, tx, items, 0)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("replace bookmarks: %w", err)
	}
	return bs, nil
}

func (s *sqliteBookmarks) AppendMany(ctx context.Context, items []NewBookmark) ([]Bookmark, error) {
	if len(items) == 0 {
		return []Bookmark{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("append bookmarks: %w", err)
	}
	defer tx.Rollback()

	var pos int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM bookmarks`).Scan(&pos); err != nil {
		return nil, fmt.Errorf("append bookmarks: %w", err)
	}
	bs, err := insertAll(ctx, tx, items, pos)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("append bookmarks: %w", err)
	}
	return bs, nil
}

func (s *sqliteSettings) Get(ctx context.Context) (Settings, error) {
	out := Settings{Grid: DefaultGrid}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("get settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return Settings{}, fmt.Errorf("get settings: %w", err)
		}
		switch key {
		case "grid":
			// Unmarshal over the defaults so a partially stored document
			// still yields a complete grid.
			if err := json.Unmarshal([]byte(val), &out.Grid); err != nil {
				return Settings{}, fmt.Errorf("settings key %q: %w", key, err)
			}
		}
		// Unknown keys are ignored for forward compatibility.
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("get settings: %w", err)
	}
	return out, nil
}

func (s *sqliteSettings) Put(ctx context.Context, st Settings) error {
	if err := st.Grid.Validate(); err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	raw, err := json.Marshal(st.Grid)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO settings (key, value) VALUES ('grid', ?)`,
		string(raw)); err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	return nil
}

// getBookmark loads one bookmark by id via q, mapping a missing row to
// ErrNotFound.
func getBookmark(ctx context.Context, q querier, id int64) (Bookmark, error) {
	b, err := scanBookmark(q.QueryRowContext(ctx,
		`SELECT `+bookmarkCols+` FROM bookmarks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return Bookmark{}, fmt.Errorf("get bookmark %d: %w", id, err)
	}
	return b, nil
}

// listIDs returns every bookmark id in display order.
func listIDs(ctx context.Context, q querier) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM bookmarks ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list bookmark ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list bookmark ids: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bookmark ids: %w", err)
	}
	return ids, nil
}

// setPositions assigns each id its slice index as its new position.
func setPositions(ctx context.Context, tx *sql.Tx, ids []int64) error {
	stmt, err := tx.PrepareContext(ctx, `UPDATE bookmarks SET position = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("reorder bookmarks: %w", err)
	}
	defer stmt.Close()
	for pos, id := range ids {
		if _, err := stmt.ExecContext(ctx, pos, id); err != nil {
			return fmt.Errorf("reorder bookmark %d: %w", id, err)
		}
	}
	return nil
}

// checkPermutation verifies ids lists each existing id exactly once.
func checkPermutation(existing, ids []int64) error {
	if len(ids) != len(existing) {
		return fmt.Errorf("%w: got %d ids, have %d bookmarks", ErrInvalidOrder, len(ids), len(existing))
	}
	set := make(map[int64]bool, len(existing))
	for _, id := range existing {
		set[id] = false
	}
	for _, id := range ids {
		seen, ok := set[id]
		if !ok {
			return fmt.Errorf("%w: unknown id %d", ErrInvalidOrder, id)
		}
		if seen {
			return fmt.Errorf("%w: duplicate id %d", ErrInvalidOrder, id)
		}
		set[id] = true
	}
	return nil
}

// insertAll inserts items at consecutive positions starting at pos,
// inside q. All new rows share one timestamp and pending icon state.
func insertAll(ctx context.Context, q querier, items []NewBookmark, pos int) ([]Bookmark, error) {
	now := time.Now().UTC()
	ts := formatTime(now)
	bs := make([]Bookmark, 0, len(items))
	for _, nb := range items {
		res, err := q.ExecContext(ctx,
			`INSERT INTO bookmarks (title, url, position, icon_status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			nb.Title, nb.URL, pos, IconPending, ts, ts)
		if err != nil {
			return nil, fmt.Errorf("insert bookmark %q: %w", nb.Title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("insert bookmark %q: %w", nb.Title, err)
		}
		bs = append(bs, Bookmark{
			ID: id, Title: nb.Title, URL: nb.URL, Position: pos,
			IconStatus: IconPending, CreatedAt: now, UpdatedAt: now,
		})
		pos++
	}
	return bs, nil
}

// scanBookmark reads a row produced by a SELECT of bookmarkCols. Times
// are stored as RFC3339Nano UTC text.
func scanBookmark(row scanner) (Bookmark, error) {
	var b Bookmark
	var created, updated string
	if err := row.Scan(&b.ID, &b.Title, &b.URL, &b.Position,
		&b.IconPath, &b.IconStatus, &created, &updated); err != nil {
		return Bookmark{}, err
	}
	var err error
	if b.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Bookmark{}, fmt.Errorf("created_at %q: %w", created, err)
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return Bookmark{}, fmt.Errorf("updated_at %q: %w", updated, err)
	}
	return b, nil
}

// formatTime renders t in the storage format: RFC3339Nano UTC text.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// querier is the query surface shared by *sql.DB and *sql.Tx, so helpers
// can run inside or outside a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}
