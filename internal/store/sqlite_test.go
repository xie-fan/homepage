package store

import (
	"context"
	"errors"
	"fmt"
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

// mustCreate inserts one bookmark from page 0 or fails the test.
func mustCreate(t *testing.T, ctx context.Context, bs BookmarkStore, title string) Bookmark {
	t.Helper()
	b, err := bs.Create(ctx, title, "https://"+title+".example.com", 0)
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return b
}

// mustCreateN inserts n bookmarks named b00.. via Create from page 0.
func mustCreateN(t *testing.T, ctx context.Context, bs BookmarkStore, n int) []Bookmark {
	t.Helper()
	out := make([]Bookmark, n)
	for i := range out {
		name := fmt.Sprintf("b%02d", i)
		b, err := bs.Create(ctx, name, "https://"+name+".example.com", 0)
		if err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
		out[i] = b
	}
	return out
}

// mustPutGrid stores the grid, failing the test on error.
func mustPutGrid(t *testing.T, ctx context.Context, ss SettingsStore, rows, cols int) {
	t.Helper()
	err := ss.Put(ctx, Settings{Grid: Grid{Rows: rows, Cols: cols}, Spacing: SpacingNormal})
	if err != nil {
		t.Fatalf("Put grid %dx%d: %v", rows, cols, err)
	}
}

// wantLayout asserts the bookmarks appear in exactly the given pages of
// titles — each at slot (page index, position inside the page) — and
// returns the listed bookmarks.
func wantLayout(t *testing.T, ctx context.Context, bs BookmarkStore, pages ...[]string) []Bookmark {
	t.Helper()
	list, err := bs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var want []string
	for _, pg := range pages {
		want = append(want, pg...)
	}
	got := make([]string, len(list))
	for i, b := range list {
		got[i] = b.Title
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want pages %v", got, pages)
	}
	i := 0
	for p, pg := range pages {
		for pos := range pg {
			if list[i].Page != p || list[i].Position != pos {
				t.Errorf("bookmark %q slot = (%d,%d), want (%d,%d)",
					list[i].Title, list[i].Page, list[i].Position, p, pos)
			}
			i++
		}
	}
	return list
}

// titles collects the titles of bs in order.
func titles(bs []Bookmark) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Title
	}
	return out
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
	if a.Page != 0 || a.Position != 0 || b.Page != 0 || b.Position != 1 {
		t.Errorf("slots = (%d,%d), (%d,%d); want (0,0), (0,1)",
			a.Page, a.Position, b.Page, b.Position)
	}
	if a.IconStatus != IconPending || a.IconPath != "" {
		t.Errorf("new bookmark icon = %q/%q, want pending/''", a.IconStatus, a.IconPath)
	}
	if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Error("timestamps not populated")
	}
	wantLayout(t, ctx, st.BookmarkStore, []string{"a", "b"})

	got, err := st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "b" || got.URL != "https://b.example.com" ||
		got.Page != 0 || got.Position != 1 {
		t.Errorf("Get = %+v", got)
	}
}

// TestCreateFromPage: the fromPage hint picks the first page at or
// after it with a free slot — earlier free slots stay untouched, and a
// new page appears only when every page from fromPage on is full.
func TestCreateFromPage(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 12) // fills page 0

	// Page 0 is full: create from 0 lands on a new page 1.
	b, err := bs.Create(ctx, "x", "https://x.example.com", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.Page != 1 || b.Position != 0 {
		t.Fatalf("x slot = (%d,%d), want (1,0)", b.Page, b.Position)
	}

	// Move a seed off page 0 so it has a free slot again.
	if _, err := bs.Move(ctx, seed[0].ID, 1, 1); err != nil {
		t.Fatalf("Move: %v", err)
	}
	wantLayout(t, ctx, bs,
		titles(seed[1:]),
		[]string{"x", seed[0].Title})

	// Create from page 1: page 0's free slot is skipped, x's page wins.
	y, err := bs.Create(ctx, "y", "https://y.example.com", 1)
	if err != nil {
		t.Fatalf("Create y: %v", err)
	}
	if y.Page != 1 || y.Position != 2 {
		t.Fatalf("y slot = (%d,%d), want (1,2)", y.Page, y.Position)
	}
	// Create from page 0 fills page 0's free slot at its end.
	z, err := bs.Create(ctx, "z", "https://z.example.com", 0)
	if err != nil {
		t.Fatalf("Create z: %v", err)
	}
	if z.Page != 0 || z.Position != 11 {
		t.Fatalf("z slot = (%d,%d), want (0,11)", z.Page, z.Position)
	}
}

// TestCreateFromPageBeyondLast: a fromPage past the last page means
// "the last page" — its free slot wins, a full one yields a new page.
func TestCreateFromPageBeyondLast(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4)
	bs := st.BookmarkStore
	mustCreateN(t, ctx, bs, 13) // page 0 (12), page 1 (1)

	// Page 1 has room; fromPage 99 is treated as page 1.
	b, err := bs.Create(ctx, "x", "https://x.example.com", 99)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.Page != 1 || b.Position != 1 {
		t.Fatalf("x slot = (%d,%d), want (1,1)", b.Page, b.Position)
	}
	// A negative fromPage is page 0: y lands at the first free slot
	// from there — the not-full page 1.
	c, err := bs.Create(ctx, "y", "https://y.example.com", -3)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Page != 1 || c.Position != 2 {
		t.Fatalf("y slot = (%d,%d), want (1,2)", c.Page, c.Position)
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
	if err := st.Delete(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
	if _, err := st.Move(ctx, 1, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Move: err = %v, want ErrNotFound", err)
	}
	if err := st.SetIcon(ctx, 1, "x.png", IconOK); !errors.Is(err, ErrNotFound) {
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
	if up.Title != "a2" || up.URL != b.URL || up.Page != 0 || up.Position != 0 {
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
	wantLayout(t, ctx, st.BookmarkStore, []string{"a", "c", "d"})
	if _, err := st.BookmarkStore.Get(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted bookmark still readable: %v", err)
	}

	// Deleting the last row still leaves compact positions.
	if err := st.Delete(ctx, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantLayout(t, ctx, st.BookmarkStore, []string{"a", "c"})
}

// TestDeleteDropsEmptyPage: a page left empty by a delete drops out and
// later pages renumber down; deleting the last bookmark keeps a single
// (empty) page in the model — nothing is left in the table either way.
func TestDeleteDropsEmptyPage(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 13) // page 0 (12), page 1 (1)

	if err := bs.Delete(ctx, seed[12].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantLayout(t, ctx, bs, titles(seed[:12]))
}

func TestMoveSamePage(t *testing.T) {
	ctx, st := newTestStore(t)
	a := mustCreate(t, ctx, st.BookmarkStore, "a")
	mustCreate(t, ctx, st.BookmarkStore, "b")
	mustCreate(t, ctx, st.BookmarkStore, "c")

	list, err := st.Move(ctx, a.ID, 0, 2)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got := titles(list); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("Move returned %v", got)
	}
	wantLayout(t, ctx, st.BookmarkStore, []string{"b", "c", "a"})
}

// TestMoveCascade: moving into a full page pushes its tail to the front
// of the next page, cascading through as many full pages as it takes —
// and onto a new last page when the last one overflows.
func TestMoveCascade(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 25) // pages 0,1 full, page 2 has one

	// Move b00 into page 1's front: b12..b22 shifts over, b23 spills to
	// page 2's front ahead of b24.
	list, err := bs.Move(ctx, seed[0].ID, 1, 0)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	want := [][]string{
		titles(seed[1:12]),
		append([]string{seed[0].Title}, titles(seed[12:23])...),
		append([]string{seed[23].Title}, titles(seed[24:25])...),
	}
	if got := titles(list); !slices.Equal(got, flatten(want)) {
		t.Fatalf("Move returned %v, want %v", got, want)
	}
	wantLayout(t, ctx, bs, want...)
}

// TestMoveNewPage: targeting page == len(pages) puts the bookmark on a
// new last page.
func TestMoveNewPage(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4)
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 3)

	if _, err := bs.Move(ctx, seed[0].ID, 1, 0); err != nil {
		t.Fatalf("Move: %v", err)
	}
	wantLayout(t, ctx, bs,
		[]string{seed[1].Title, seed[2].Title},
		[]string{seed[0].Title})
}

// TestMoveDropsSourcePage: a source page left empty by the move drops
// out and the pages after it shift down — here page 2 becomes page 1.
func TestMoveDropsSourcePage(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4)
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 14) // page 0 (12), page 1 (2)

	// Park b12 on a new page 2.
	if _, err := bs.Move(ctx, seed[12].ID, 2, 0); err != nil {
		t.Fatalf("Move b12: %v", err)
	}
	wantLayout(t, ctx, bs,
		titles(seed[:12]),
		[]string{seed[13].Title},
		[]string{seed[12].Title})

	// Move the remaining page-1 item onto page 2: page 1 drops out.
	if _, err := bs.Move(ctx, seed[13].ID, 2, 0); err != nil {
		t.Fatalf("Move b13: %v", err)
	}
	wantLayout(t, ctx, bs,
		titles(seed[:12]),
		[]string{seed[13].Title, seed[12].Title})
}

func TestMoveInvalid(t *testing.T) {
	ctx, st := newTestStore(t)
	a := mustCreate(t, ctx, st.BookmarkStore, "a")

	for name, mv := range map[string][2]int{
		"negative page":  {-1, 0},
		"negative index": {0, -1},
		"page past end":  {2, 0}, // len(pages)=1 → valid range is 0..1
	} {
		if _, err := st.Move(ctx, a.ID, mv[0], mv[1]); !errors.Is(err, ErrInvalidMove) {
			t.Errorf("%s: err = %v, want ErrInvalidMove", name, err)
		}
	}
	// Failed moves leave the board untouched.
	wantLayout(t, ctx, st.BookmarkStore, []string{"a"})
}

// TestMoveIndexClamps: an index past the page's end lands at its tail.
func TestMoveIndexClamps(t *testing.T) {
	ctx, st := newTestStore(t)
	mustCreate(t, ctx, st.BookmarkStore, "a")
	mustCreate(t, ctx, st.BookmarkStore, "b")
	c := mustCreate(t, ctx, st.BookmarkStore, "c")

	if _, err := st.Move(ctx, c.ID, 0, 99); err != nil {
		t.Fatalf("Move: %v", err)
	}
	wantLayout(t, ctx, st.BookmarkStore, []string{"a", "b", "c"})
}

// TestSettingsPutReflows: a grid shrink pushes each page's overflow
// forward inside the same transaction; a grow is a no-op.
func TestSettingsPutReflows(t *testing.T) {
	ctx, st := newTestStore(t)
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 25) // default 4x6 cap 24 → [24],[1]

	// Shrink to 3x5 cap 15: page 0 overflows 9 → page 1 front, b24 follows.
	mustPutGrid(t, ctx, st.SettingsStore, 3, 5)
	wantLayout(t, ctx, bs,
		titles(seed[:15]),
		append(titles(seed[15:24]), seed[24].Title))

	// Grow to 5x6 cap 30: no-op, free slots are not back-filled.
	mustPutGrid(t, ctx, st.SettingsStore, 5, 6)
	wantLayout(t, ctx, bs,
		titles(seed[:15]),
		append(titles(seed[15:24]), seed[24].Title))
}

// TestSettingsPutReflowCascade: a shrink can cascade over several pages.
func TestSettingsPutReflowCascade(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 5, 6) // cap 30
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 31) // [30],[1]

	// Shrink to 3x4 cap 12: 18 spill from page 0 to page 1, which
	// overflows 7 to page 2.
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4)
	wantLayout(t, ctx, bs,
		titles(seed[:12]),
		titles(seed[12:24]),
		append(titles(seed[24:30]), seed[30].Title))
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
	if len(bs) != 2 || bs[0].Page != 0 || bs[0].Position != 0 ||
		bs[1].Page != 0 || bs[1].Position != 1 {
		t.Fatalf("ReplaceAll = %+v", bs)
	}
	if bs[0].ID == 0 || bs[0].IconStatus != IconPending {
		t.Errorf("new bookmark = %+v", bs[0])
	}
	wantLayout(t, ctx, st.BookmarkStore, []string{"x", "y"})

	// Replace with nothing clears the table.
	if _, err := st.ReplaceAll(ctx, nil); err != nil {
		t.Fatalf("ReplaceAll(nil): %v", err)
	}
	wantLayout(t, ctx, st.BookmarkStore)
}

// TestReplaceAllFlat: without page info items fill pages in file order.
func TestReplaceAllFlat(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	items := make([]NewBookmark, 14)
	for i := range items {
		items[i] = NewBookmark{Title: fmt.Sprintf("b%02d", i), URL: fmt.Sprintf("https://b%02d.example.com", i)}
	}
	if _, err := st.ReplaceAll(ctx, items); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	wantLayout(t, ctx, st.BookmarkStore, titles2(items[:12]), titles2(items[12:]))
}

// TestReplaceAllWithPages: when every item carries a page, grouping
// follows (page, position) in file order, pages renumbered 0.. — here
// file pages 0 and 7 become board pages 0 and 1, and a sparse group
// with no position info falls back to file order.
func TestReplaceAllWithPages(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4)
	p := func(v int) *int { return &v }
	items := []NewBookmark{
		{Title: "a", URL: "https://a.example.com", Page: p(0), Position: 1},
		{Title: "b", URL: "https://b.example.com", Page: p(0), Position: 0},
		{Title: "c", URL: "https://c.example.com", Page: p(7), Position: 0},
		{Title: "d", URL: "https://d.example.com", Page: p(7)},
	}
	if _, err := st.ReplaceAll(ctx, items); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	// Page 0 ordered by position → b then a; file page 7 renumbered to
	// board page 1, c (pos 0) before d (no pos → 0, file order).
	wantLayout(t, ctx, st.BookmarkStore,
		[]string{"b", "a"},
		[]string{"c", "d"})
}

// TestReplaceAllWithPagesReflow: a file page bigger than the current
// cap reflows its tail onto the next page's front.
func TestReplaceAllWithPagesReflow(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	p := func(v int) *int { return &v }
	items := make([]NewBookmark, 0, 15)
	for i := 0; i < 14; i++ {
		items = append(items, NewBookmark{
			Title: fmt.Sprintf("b%02d", i), URL: fmt.Sprintf("https://b%02d.example.com", i),
			Page: p(0), Position: i,
		})
	}
	items = append(items, NewBookmark{
		Title: "z", URL: "https://z.example.com", Page: p(1), Position: 0,
	})
	if _, err := st.ReplaceAll(ctx, items); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	// File page 0 (14 items) overflows 2 → front of page 1, before z.
	want := [][]string{
		titles2(items[:12]),
		append(titles2(items[12:14]), "z"),
	}
	wantLayout(t, ctx, st.BookmarkStore, want...)
}

// TestAppendMany: items are inserted one by one via Add from page 0 —
// they fill the earliest free slot, not the board's tail.
func TestAppendMany(t *testing.T) {
	ctx, st := newTestStore(t)
	mustPutGrid(t, ctx, st.SettingsStore, 3, 4) // cap 12
	bs := st.BookmarkStore
	seed := mustCreateN(t, ctx, bs, 12) // page 0 full
	mustCreate(t, ctx, bs, "m")         // page 1

	// Free a slot on page 0: move its first item to page 1's end.
	if _, err := bs.Move(ctx, seed[0].ID, 1, 1); err != nil {
		t.Fatalf("Move: %v", err)
	}
	wantLayout(t, ctx, bs,
		titles(seed[1:]),
		[]string{"m", seed[0].Title})

	out, err := bs.AppendMany(ctx, []NewBookmark{
		{Title: "x", URL: "https://x.example.com"},
		{Title: "y", URL: "https://y.example.com"},
	})
	if err != nil {
		t.Fatalf("AppendMany: %v", err)
	}
	// x fills page 0's free slot at its end; y follows onto page 1's
	// free end (page 0 is full again).
	if out[0].Page != 0 || out[0].Position != 11 {
		t.Fatalf("x slot = (%d,%d), want (0,11)", out[0].Page, out[0].Position)
	}
	if out[1].Page != 1 || out[1].Position != 2 {
		t.Fatalf("y slot = (%d,%d), want (1,2)", out[1].Page, out[1].Position)
	}
	wantLayout(t, ctx, bs,
		append(titles(seed[1:]), "x"),
		[]string{"m", seed[0].Title, "y"})

	// Empty input is a no-op.
	empty, err := bs.AppendMany(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("AppendMany(nil) = %v, %v", empty, err)
	}
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

// flatten joins pages of titles into one ordered list.
func flatten(pages [][]string) []string {
	var out []string
	for _, pg := range pages {
		out = append(out, pg...)
	}
	return out
}

// titles2 maps NewBookmark items to their titles.
func titles2(items []NewBookmark) []string {
	out := make([]string, len(items))
	for i, nb := range items {
		out[i] = nb.Title
	}
	return out
}
