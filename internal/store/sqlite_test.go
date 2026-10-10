package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"homepage/internal/db"
)

// newTestStore opens and migrates a fresh database in a temp dir.
func newTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return ctx, NewSQLite(sqlDB)
}

// mustCreate inserts one bookmark or fails the test.
func mustCreate(t *testing.T, ctx context.Context, bs BookmarkStore, title string) Bookmark {
	t.Helper()
	b, err := bs.Create(ctx, title, "https://"+title+".example.com")
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return b
}

// wantOrder asserts the bookmark titles appear in exactly the given
// order at positions 0..n-1, and returns the listed bookmarks.
func wantOrder(t *testing.T, ctx context.Context, bs BookmarkStore, titles ...string) []Bookmark {
	t.Helper()
	list, err := bs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(list))
	for i, b := range list {
		got[i] = b.Title
		if b.Position != i {
			t.Errorf("bookmark %q position = %d, want %d", b.Title, b.Position, i)
		}
	}
	if !slices.Equal(got, titles) {
		t.Errorf("order = %v, want %v", got, titles)
	}
	return list
}

func TestListEmpty(t *testing.T) {
	ctx, st := newTestStore(t)
	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List = %v, want empty", list)
	}
}

func TestCreateAppendsInOrder(t *testing.T) {
	ctx, st := newTestStore(t)
	a := mustCreate(t, ctx, st.BookmarkStore, "a")
	b := mustCreate(t, ctx, st.BookmarkStore, "b")

	if a.ID == 0 || b.ID <= a.ID {
		t.Errorf("ids not increasing: %d, %d", a.ID, b.ID)
	}
	if a.Position != 0 || b.Position != 1 {
		t.Errorf("positions = %d, %d; want 0, 1", a.Position, b.Position)
	}
	if a.IconStatus != IconPending || a.IconPath != "" {
		t.Errorf("new bookmark icon = %q/%q, want pending/''", a.IconStatus, a.IconPath)
	}
	if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Error("timestamps not populated")
	}
	wantOrder(t, ctx, st.BookmarkStore, "a", "b")

	got, err := st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "b" || got.URL != "https://b.example.com" || got.Position != 1 {
		t.Errorf("Get = %+v", got)
	}
}

func TestNotFound(t *testing.T) {
	ctx, st := newTestStore(t)
	if _, err := st.BookmarkStore.Get(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := st.BookmarkStore.Update(ctx, 1, "t", "https://x.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: err = %v, want ErrNotFound", err)
	}
	if err := st.BookmarkStore.Delete(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
	if err := st.BookmarkStore.SetIcon(ctx, 1, "x.png", IconOK); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetIcon: err = %v, want ErrNotFound", err)
	}
}

func TestUpdateSameURLKeepsIcon(t *testing.T) {
	ctx, st := newTestStore(t)
	b := mustCreate(t, ctx, st.BookmarkStore, "a")
	if err := st.SetIcon(ctx, b.ID, "deadbeef.png", IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}

	up, err := st.Update(ctx, b.ID, "a2", b.URL)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Title != "a2" || up.URL != b.URL || up.Position != 0 {
		t.Errorf("Update = %+v", up)
	}
	if up.IconPath != "deadbeef.png" || up.IconStatus != IconOK {
		t.Errorf("icon = %q/%q, want deadbeef.png/ok", up.IconPath, up.IconStatus)
	}
	if up.UpdatedAt.Before(b.UpdatedAt) {
		t.Error("updated_at went backwards")
	}
	// Returned value matches what was persisted.
	got, err := st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "a2" || got.IconStatus != IconOK || got.IconPath != "deadbeef.png" {
		t.Errorf("stored = %+v", got)
	}
}

func TestUpdateNewURLResetsIcon(t *testing.T) {
	ctx, st := newTestStore(t)
	b := mustCreate(t, ctx, st.BookmarkStore, "a")
	if err := st.SetIcon(ctx, b.ID, "deadbeef.png", IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}

	up, err := st.Update(ctx, b.ID, "a", "https://new.example.com")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.URL != "https://new.example.com" {
		t.Errorf("url = %q", up.URL)
	}
	if up.IconPath != "" || up.IconStatus != IconPending {
		t.Errorf("icon = %q/%q, want ''/pending", up.IconPath, up.IconStatus)
	}
}

func TestDeleteCompactsPositions(t *testing.T) {
	ctx, st := newTestStore(t)
	mustCreate(t, ctx, st.BookmarkStore, "a")
	b := mustCreate(t, ctx, st.BookmarkStore, "b")
	mustCreate(t, ctx, st.BookmarkStore, "c")
	d := mustCreate(t, ctx, st.BookmarkStore, "d")

	if err := st.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantOrder(t, ctx, st.BookmarkStore, "a", "c", "d")
	if _, err := st.BookmarkStore.Get(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted bookmark still readable: %v", err)
	}

	// Deleting the last row still leaves compact positions.
	if err := st.Delete(ctx, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantOrder(t, ctx, st.BookmarkStore, "a", "c")
}

func TestReorder(t *testing.T) {
	ctx, st := newTestStore(t)
	a := mustCreate(t, ctx, st.BookmarkStore, "a")
	b := mustCreate(t, ctx, st.BookmarkStore, "b")
	c := mustCreate(t, ctx, st.BookmarkStore, "c")

	if err := st.Reorder(ctx, []int64{c.ID, a.ID, b.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	wantOrder(t, ctx, st.BookmarkStore, "c", "a", "b")
}

func TestReorderRejectsNonPermutation(t *testing.T) {
	ctx, st := newTestStore(t)
	a := mustCreate(t, ctx, st.BookmarkStore, "a")
	b := mustCreate(t, ctx, st.BookmarkStore, "b")
	c := mustCreate(t, ctx, st.BookmarkStore, "c")

	for name, ids := range map[string][]int64{
		"missing id": {a.ID, b.ID},
		"extra id":   {a.ID, b.ID, c.ID, 9999},
		"duplicate":  {a.ID, a.ID, b.ID},
		"unknown id": {a.ID, b.ID, 9999},
	} {
		if err := st.Reorder(ctx, ids); !errors.Is(err, ErrInvalidOrder) {
			t.Errorf("%s: err = %v, want ErrInvalidOrder", name, err)
		}
	}
	// Failed reorders leave the stored order untouched.
	wantOrder(t, ctx, st.BookmarkStore, "a", "b", "c")
}

func TestSetIcon(t *testing.T) {
	ctx, st := newTestStore(t)
	b := mustCreate(t, ctx, st.BookmarkStore, "a")

	if err := st.SetIcon(ctx, b.ID, "abc.png", IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	got, err := st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IconPath != "abc.png" || got.IconStatus != IconOK {
		t.Errorf("icon = %q/%q, want abc.png/ok", got.IconPath, got.IconStatus)
	}
}

func TestReplaceAll(t *testing.T) {
	ctx, st := newTestStore(t)
	mustCreate(t, ctx, st.BookmarkStore, "old1")
	mustCreate(t, ctx, st.BookmarkStore, "old2")

	bs, err := st.ReplaceAll(ctx, []NewBookmark{
		{Title: "x", URL: "https://x.example.com"},
		{Title: "y", URL: "https://y.example.com"},
	})
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if len(bs) != 2 || bs[0].Position != 0 || bs[1].Position != 1 {
		t.Fatalf("ReplaceAll = %+v", bs)
	}
	if bs[0].ID == 0 || bs[0].IconStatus != IconPending {
		t.Errorf("new bookmark = %+v", bs[0])
	}
	wantOrder(t, ctx, st.BookmarkStore, "x", "y")

	// Replace with nothing clears the table.
	if _, err := st.ReplaceAll(ctx, nil); err != nil {
		t.Fatalf("ReplaceAll(nil): %v", err)
	}
	wantOrder(t, ctx, st.BookmarkStore)
}

func TestAppendMany(t *testing.T) {
	ctx, st := newTestStore(t)
	mustCreate(t, ctx, st.BookmarkStore, "a")

	bs, err := st.AppendMany(ctx, []NewBookmark{
		{Title: "b", URL: "https://b.example.com"},
		{Title: "c", URL: "https://c.example.com"},
	})
	if err != nil {
		t.Fatalf("AppendMany: %v", err)
	}
	if len(bs) != 2 || bs[0].Position != 1 || bs[1].Position != 2 {
		t.Fatalf("AppendMany = %+v", bs)
	}
	wantOrder(t, ctx, st.BookmarkStore, "a", "b", "c")

	// Empty input is a no-op.
	empty, err := st.AppendMany(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("AppendMany(nil) = %v, %v", empty, err)
	}
	wantOrder(t, ctx, st.BookmarkStore, "a", "b", "c")
}

func TestSettingsDefaults(t *testing.T) {
	ctx, st := newTestStore(t)
	s, err := st.SettingsStore.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Grid != DefaultGrid {
		t.Errorf("grid = %+v, want default %+v", s.Grid, DefaultGrid)
	}
	if s.Spacing != DefaultSpacing {
		t.Errorf("spacing = %q, want default %q", s.Spacing, DefaultSpacing)
	}
}

func TestSettingsPutGet(t *testing.T) {
	ctx, st := newTestStore(t)
	want := Settings{Grid: Grid{Rows: 3, Cols: 5}, Spacing: SpacingCompact}
	if err := st.Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	s, err := st.SettingsStore.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s != want {
		t.Errorf("settings = %+v, want %+v", s, want)
	}

	// Overwrite an existing value.
	want = Settings{Grid: Grid{Rows: 5, Cols: 4}, Spacing: SpacingRelaxed}
	if err := st.Put(ctx, want); err != nil {
		t.Fatalf("Put overwrite: %v", err)
	}
	s, err = st.SettingsStore.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s != want {
		t.Errorf("settings = %+v, want %+v", s, want)
	}
}

func TestSettingsPutInvalidGrid(t *testing.T) {
	ctx, st := newTestStore(t)
	for _, g := range []Grid{
		{Rows: 2, Cols: 6}, // rows too small
		{Rows: 6, Cols: 6}, // rows too big
		{Rows: 4, Cols: 3}, // cols too small
		{Rows: 4, Cols: 7}, // cols too big
	} {
		if err := st.Put(ctx, Settings{Grid: g, Spacing: SpacingNormal}); err == nil {
			t.Errorf("Put(%+v): want error", g)
		}
	}
	// Rejected Puts store nothing: still defaults.
	s, err := st.SettingsStore.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Grid != DefaultGrid {
		t.Errorf("grid = %+v after rejected Puts", s.Grid)
	}
}

func TestSettingsPutInvalidSpacing(t *testing.T) {
	ctx, st := newTestStore(t)
	for _, sp := range []string{"", "wide", "NORMAL"} {
		if err := st.Put(ctx, Settings{Grid: DefaultGrid, Spacing: sp}); err == nil {
			t.Errorf("Put(spacing %q): want error", sp)
		}
	}
	// Rejected Puts store nothing: still defaults.
	s, err := st.SettingsStore.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Spacing != DefaultSpacing {
		t.Errorf("spacing = %q after rejected Puts, want %q", s.Spacing, DefaultSpacing)
	}
}

// TestSettingsInvalidStoredSpacing: a stored spacing that is not valid
// JSON or not a known value yields the default instead of an error.
func TestSettingsInvalidStoredSpacing(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	st := NewSQLite(sqlDB)

	for _, raw := range []string{`"bogus"`, `123`, `{oops`} {
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT OR REPLACE INTO settings (key, value) VALUES ('spacing', ?)`, raw); err != nil {
			t.Fatalf("seed spacing %q: %v", raw, err)
		}
		s, err := st.SettingsStore.Get(ctx)
		if err != nil {
			t.Fatalf("Get with stored %q: %v", raw, err)
		}
		if s.Spacing != DefaultSpacing {
			t.Errorf("stored %q → spacing %q, want %q", raw, s.Spacing, DefaultSpacing)
		}
	}
}

func TestGridValidate(t *testing.T) {
	for _, g := range []Grid{
		{Rows: 3, Cols: 4}, {Rows: 4, Cols: 6}, {Rows: 5, Cols: 6}, {Rows: 3, Cols: 6},
	} {
		if err := g.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", g, err)
		}
	}
}
