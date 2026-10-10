package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"homepage/internal/layout"
)

// bookmarkCols is the shared SELECT column list; keep it in sync with
// scanBookmark.
const bookmarkCols = `id, title, url, page, position, icon_path, icon_status, created_at, updated_at`

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
	return listBookmarks(ctx, s.db)
}

func (s *sqliteBookmarks) Get(ctx context.Context, id int64) (Bookmark, error) {
	return getBookmark(ctx, s.db, id)
}

func (s *sqliteBookmarks) Create(ctx context.Context, title, url string, fromPage int) (Bookmark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, fmt.Errorf("create bookmark: %w", err)
	}
	defer tx.Rollback()

	L, prev, err := loadLayout(ctx, tx)
	if err != nil {
		return Bookmark{}, err
	}
	cap, err := gridCap(ctx, tx)
	if err != nil {
		return Bookmark{}, err
	}
	// fromPage is the page the user is viewing: a page past the end
	// means "the last page" (and Add creates a new one only when even
	// that is full).
	fromPage = max(0, min(fromPage, len(L)-1))

	b, err := insertBookmark(ctx, tx, NewBookmark{Title: title, URL: url})
	if err != nil {
		return Bookmark{}, err
	}
	L = layout.Add(L, b.ID, fromPage, cap)
	if err := writeLayout(ctx, tx, prev, L); err != nil {
		return Bookmark{}, err
	}
	b.Page, b.Position = slotOf(L, b.ID)
	if err := tx.Commit(); err != nil {
		return Bookmark{}, fmt.Errorf("create bookmark: %w", err)
	}
	return b, nil
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

	L, prev, err := loadLayout(ctx, tx)
	if err != nil {
		return err
	}
	if _, ok := prev[id]; !ok {
		return fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM bookmarks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}
	// Close up the page, dropping it when left empty, in the same
	// transaction as the DELETE.
	if err := writeLayout(ctx, tx, prev, layout.Remove(L, id)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}
	return nil
}

func (s *sqliteBookmarks) Move(ctx context.Context, id int64, page, index int) ([]Bookmark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("move bookmark %d: %w", id, err)
	}
	defer tx.Rollback()

	L, prev, err := loadLayout(ctx, tx)
	if err != nil {
		return nil, err
	}
	if page < 0 || index < 0 {
		return nil, fmt.Errorf("%w: negative page %d or index %d", ErrInvalidMove, page, index)
	}
	if page > len(L) {
		return nil, fmt.Errorf("%w: page %d beyond last page %d", ErrInvalidMove, page, len(L))
	}
	if _, ok := prev[id]; !ok {
		return nil, fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}
	cap, err := gridCap(ctx, tx)
	if err != nil {
		return nil, err
	}
	L = layout.Insert(L, id, page, index, cap)
	if err := writeLayout(ctx, tx, prev, L); err != nil {
		return nil, err
	}
	bs, err := listBookmarks(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("move bookmark %d: %w", id, err)
	}
	return bs, nil
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
	if len(items) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("replace bookmarks: %w", err)
		}
		return []Bookmark{}, nil
	}
	cap, err := gridCap(ctx, tx)
	if err != nil {
		return nil, err
	}

	// Until rows exist there are no ids: the layout is expressed in
	// item indices and each item lands at the slot its index gets.
	L := groupByPage(items)
	if L == nil {
		ids := make([]int64, len(items))
		for i := range items {
			ids[i] = int64(i)
		}
		L = layout.FromFlat(ids, cap)
	} else {
		L = layout.Reflow(L, cap)
	}

	created := make([]Bookmark, len(items))
	now := time.Now().UTC()
	for p, pg := range L {
		for pos, k := range pg {
			b, err := insertBookmarkAt(ctx, tx, items[k], p, pos, now)
			if err != nil {
				return nil, err
			}
			created[k] = b
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("replace bookmarks: %w", err)
	}
	return created, nil
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

	L, prev, err := loadLayout(ctx, tx)
	if err != nil {
		return nil, err
	}
	cap, err := gridCap(ctx, tx)
	if err != nil {
		return nil, err
	}
	created := make([]Bookmark, len(items))
	now := time.Now().UTC()
	for i, nb := range items {
		b, err := insertBookmarkAt(ctx, tx, nb, 0, 0, now)
		if err != nil {
			return nil, err
		}
		created[i] = b
		L = layout.Add(L, b.ID, 0, cap)
	}
	if err := writeLayout(ctx, tx, prev, L); err != nil {
		return nil, err
	}
	for i := range created {
		created[i].Page, created[i].Position = slotOf(L, created[i].ID)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("append bookmarks: %w", err)
	}
	return created, nil
}

func (s *sqliteSettings) Get(ctx context.Context) (Settings, error) {
	out := Settings{Grid: DefaultGrid, Spacing: DefaultSpacing}
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
		case "spacing":
			// A malformed or out-of-range stored value falls back to the
			// default rather than failing the whole read.
			var spacing string
			if err := json.Unmarshal([]byte(val), &spacing); err == nil &&
				ValidateSpacing(spacing) == nil {
				out.Spacing = spacing
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
	if err := st.Validate(); err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	grid, err := json.Marshal(st.Grid)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	// Spacing is stored as a JSON string, like the grid document.
	spacing, err := json.Marshal(st.Spacing)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put settings: %w", err)
	}
	defer tx.Rollback()
	old, err := storedGrid(ctx, tx)
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{{"grid", string(grid)}, {"spacing", string(spacing)}} {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
			kv[0], kv[1]); err != nil {
			return fmt.Errorf("put settings: %w", err)
		}
	}
	// A changed grid reflows every page in the same transaction.
	if st.Grid != old {
		L, prev, err := loadLayout(ctx, tx)
		if err != nil {
			return err
		}
		L = layout.Reflow(L, st.Grid.Rows*st.Grid.Cols)
		if err := writeLayout(ctx, tx, prev, L); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
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

// listBookmarks returns all bookmarks ordered by page, then position.
func listBookmarks(ctx context.Context, q querier) ([]Bookmark, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+bookmarkCols+` FROM bookmarks ORDER BY page, position, id`)
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

// insertBookmark inserts one bookmark row at placeholder slot (0,0)
// with a pending icon; a layout op decides the real slot right after.
func insertBookmark(ctx context.Context, q querier, nb NewBookmark) (Bookmark, error) {
	return insertBookmarkAt(ctx, q, nb, 0, 0, time.Now().UTC())
}

// insertBookmarkAt inserts one bookmark row at page/position with a
// pending icon and the shared timestamp now.
func insertBookmarkAt(ctx context.Context, q querier, nb NewBookmark, page, pos int, now time.Time) (Bookmark, error) {
	ts := formatTime(now)
	res, err := q.ExecContext(ctx,
		`INSERT INTO bookmarks (title, url, page, position, icon_status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nb.Title, nb.URL, page, pos, IconPending, ts, ts)
	if err != nil {
		return Bookmark{}, fmt.Errorf("insert bookmark %q: %w", nb.Title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Bookmark{}, fmt.Errorf("insert bookmark %q: %w", nb.Title, err)
	}
	return Bookmark{
		ID: id, Title: nb.Title, URL: nb.URL, Page: page, Position: pos,
		IconStatus: IconPending, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// scanBookmark reads a row produced by a SELECT of bookmarkCols. Times
// are stored as RFC3339Nano UTC text.
func scanBookmark(row scanner) (Bookmark, error) {
	var b Bookmark
	var created, updated string
	if err := row.Scan(&b.ID, &b.Title, &b.URL, &b.Page, &b.Position,
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
