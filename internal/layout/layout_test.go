package layout

import (
	"slices"
	"testing"
)

// same reports whether two layouts hold the same ids on the same pages.
func same(a, b [][]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// check fails unless got holds exactly want.
func check(t *testing.T, got, want [][]int64) {
	t.Helper()
	if !same(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFromFlat(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []int64
		cap  int
		want [][]int64
	}{
		{"empty", nil, 2, [][]int64{{}}},
		{"exact fill", []int64{1, 2, 3, 4}, 2, [][]int64{{1, 2}, {3, 4}}},
		{"partial last page", []int64{1, 2, 3, 4, 5}, 2, [][]int64{{1, 2}, {3, 4}, {5}}},
		{"one page", []int64{1, 2}, 5, [][]int64{{1, 2}}},
		{"cap one", []int64{1, 2, 3}, 1, [][]int64{{1}, {2}, {3}}},
		{"cap clamped", []int64{1, 2}, 0, [][]int64{{1}, {2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, FromFlat(tc.ids, tc.cap), tc.want)
		})
	}
}

func TestInsert(t *testing.T) {
	for _, tc := range []struct {
		name        string
		L           [][]int64
		id          int64
		page, index int
		cap         int
		want        [][]int64
	}{
		{"same page to front",
			[][]int64{{1, 2, 3}}, 3, 0, 0, 3,
			[][]int64{{3, 1, 2}}},
		{"same page to hovered slot",
			[][]int64{{1, 2, 3, 4}}, 1, 0, 2, 4,
			[][]int64{{2, 3, 1, 4}}},
		{"same page past end is the tail",
			[][]int64{{1, 2, 3, 4}}, 1, 0, 4, 4,
			[][]int64{{2, 3, 4, 1}}},
		{"cross page into full pushes tail to next front",
			[][]int64{{1, 2}, {3, 4}}, 1, 1, 0, 2,
			[][]int64{{2}, {1, 3}, {4}}},
		{"cascade across multiple full pages",
			[][]int64{{1, 2}, {3, 4}, {5, 6}}, 1, 1, 0, 2,
			[][]int64{{2}, {1, 3}, {4, 5}, {6}}},
		{"insert into page len creates a page",
			[][]int64{{1, 2}}, 1, 1, 0, 2,
			[][]int64{{2}, {1}}},
		{"source page emptied is removed, later pages shift",
			[][]int64{{1}, {2, 3}}, 1, 1, 1, 2,
			[][]int64{{2, 1}, {3}}},
		{"move sole item to a new last page is a no-op",
			[][]int64{{1, 2}, {3}}, 3, 2, 0, 2,
			[][]int64{{1, 2}, {3}}},
		{"move last page item past the end",
			[][]int64{{1}, {2}}, 2, 2, 0, 2,
			[][]int64{{1}, {2}}},
		{"inserting an absent id",
			[][]int64{{1, 2}}, 9, 0, 0, 2,
			[][]int64{{9, 1}, {2}}},
		{"index beyond the page appends at its end",
			[][]int64{{1}, {2, 3}}, 1, 1, 9, 3,
			[][]int64{{2, 3, 1}}},
		{"negative index clamps to the page front",
			[][]int64{{1, 2, 3}}, 3, 0, -5, 3,
			[][]int64{{3, 1, 2}}},
		{"negative page clamps to page 0",
			[][]int64{{1, 2}, {3, 4}}, 4, -1, 0, 2,
			[][]int64{{4, 1}, {2, 3}}},
		{"page beyond len lands on a new last page",
			[][]int64{{1, 2}}, 1, 9, 0, 2,
			[][]int64{{2}, {1}}},
		{"empty board",
			[][]int64{{}}, 9, 0, 0, 2,
			[][]int64{{9}}},
		{"nil board",
			nil, 9, 0, 0, 2,
			[][]int64{{9}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, Insert(tc.L, tc.id, tc.page, tc.index, tc.cap), tc.want)
		})
	}
}

func TestAdd(t *testing.T) {
	for _, tc := range []struct {
		name     string
		L        [][]int64
		id       int64
		fromPage int
		cap      int
		want     [][]int64
	}{
		{"first free slot from current page",
			[][]int64{{1}, {3, 4}}, 9, 0, 2,
			[][]int64{{1, 9}, {3, 4}}},
		{"full current page skips ahead",
			[][]int64{{1, 2}, {3}}, 9, 0, 2,
			[][]int64{{1, 2}, {3, 9}}},
		{"free slots on earlier pages are skipped",
			[][]int64{{1}, {3, 4}}, 9, 1, 2,
			[][]int64{{1}, {3, 4}, {9}}},
		{"all pages from current on full creates a page",
			[][]int64{{1, 2}, {3, 4}}, 9, 0, 2,
			[][]int64{{1, 2}, {3, 4}, {9}}},
		{"same for a later fromPage",
			[][]int64{{1, 2}, {3, 4}}, 9, 1, 2,
			[][]int64{{1, 2}, {3, 4}, {9}}},
		{"fromPage beyond the last appends a page",
			[][]int64{{1}}, 9, 5, 2,
			[][]int64{{1}, {9}}},
		{"negative fromPage starts at 0",
			[][]int64{{1}}, 9, -2, 2,
			[][]int64{{1, 9}}},
		{"empty board",
			[][]int64{{}}, 9, 0, 2,
			[][]int64{{9}}},
		{"nil board",
			nil, 9, 0, 2,
			[][]int64{{9}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, Add(tc.L, tc.id, tc.fromPage, tc.cap), tc.want)
		})
	}
}

func TestRemove(t *testing.T) {
	for _, tc := range []struct {
		name string
		L    [][]int64
		id   int64
		want [][]int64
	}{
		{"closes up within the page only",
			[][]int64{{1, 2, 3}, {4}}, 1,
			[][]int64{{2, 3}, {4}}},
		{"emptied page drops and later pages shift down",
			[][]int64{{1}, {2, 3}}, 1,
			[][]int64{{2, 3}}},
		{"the only page is kept",
			[][]int64{{1}}, 1,
			[][]int64{{}}},
		{"absent id is a no-op",
			[][]int64{{1, 2}}, 9,
			[][]int64{{1, 2}}},
		{"empty board stays one empty page",
			[][]int64{{}}, 9,
			[][]int64{{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, Remove(tc.L, tc.id), tc.want)
		})
	}
}

func TestReflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		L    [][]int64
		cap  int
		want [][]int64
	}{
		{"shrink cascades overflow forward",
			[][]int64{{1, 2, 3}, {4, 5, 6}}, 2,
			[][]int64{{1, 2}, {3, 4}, {5, 6}}},
		{"shrink spills onto a new last page",
			[][]int64{{1, 2, 3, 4}}, 2,
			[][]int64{{1, 2}, {3, 4}}},
		{"shrink cascades repeatedly",
			[][]int64{{1, 2, 3, 4, 5}}, 2,
			[][]int64{{1, 2}, {3, 4}, {5}}},
		{"grow is a no-op: free slots are not back-filled",
			[][]int64{{1}, {2}}, 4,
			[][]int64{{1}, {2}}},
		{"same cap is a no-op",
			[][]int64{{1, 2}, {3}}, 2,
			[][]int64{{1, 2}, {3}}},
		{"cap clamps to 1",
			[][]int64{{1, 2}}, 0,
			[][]int64{{1}, {2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, Reflow(tc.L, tc.cap), tc.want)
		})
	}
}

func TestDropEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		L    [][]int64
		want [][]int64
	}{
		{"empties between kept pages",
			[][]int64{{}, {1}, {}, {2, 3}, {}},
			[][]int64{{1}, {2, 3}}},
		{"all empty keeps one",
			[][]int64{{}, {}},
			[][]int64{{}}},
		{"nil keeps one",
			nil,
			[][]int64{{}}},
		{"non-empty unchanged",
			[][]int64{{1}, {2}},
			[][]int64{{1}, {2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check(t, DropEmpty(tc.L), tc.want)
		})
	}
}

// TestCascade: pages before from are never touched, even over-cap ones;
// overflow lands at the front of the next page in order.
func TestCascade(t *testing.T) {
	check(t, cascade([][]int64{{1, 2, 3}, {4}}, 1, 2),
		[][]int64{{1, 2, 3}, {4}}) // over-cap page 0 left alone: from=1
	check(t, cascade([][]int64{{1, 2, 3}, {9}}, 0, 2),
		[][]int64{{1, 2}, {3, 9}}) // tail lands at the front in order
	check(t, cascade([][]int64{{1, 2, 3, 4}}, 0, 2),
		[][]int64{{1, 2}, {3, 4}}) // one page cascades onto a new page
	check(t, cascade([][]int64{{1, 2, 3, 4, 5}}, 0, 2),
		[][]int64{{1, 2}, {3, 4}, {5}})
}

// TestOpsDoNotMutateInput: every op must leave the input layout — and
// its page slices — untouched, since callers keep snapshots around.
func TestOpsDoNotMutateInput(t *testing.T) {
	orig := [][]int64{{1, 2}, {3, 4}, {5}}
	frozen := clone(orig)

	_ = Insert(orig, 1, 1, 0, 2)
	_ = Add(orig, 9, 0, 2)
	_ = Remove(orig, 2)
	_ = Reflow(orig, 1)
	_ = DropEmpty(orig)
	// cascade/removeID are the internal helpers that mutate in place —
	// callers must own the layout (Insert/Reflow clone first).
	_ = cascade(clone(orig), 0, 1)
	_ = removeID(clone(orig), 1)

	if !same(orig, frozen) {
		t.Fatalf("input mutated: %v, want %v", orig, frozen)
	}
	// The returned layouts share nothing with the input either.
	next := Insert(orig, 1, 0, 0, 2)
	next[0][0] = 99
	if orig[0][0] != 1 {
		t.Fatalf("result aliased the input: %v", orig)
	}
}
