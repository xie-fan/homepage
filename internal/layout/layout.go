// Package layout implements the independent-pages model of docs/PLAN.md
// ("Round 8 — independent pages"): a layout is a list of pages, each
// page the bookmark ids it holds in slot order. Pages hold at most cap
// ids, may have free slots at the end, and never have holes in the
// middle. All ops are pure — they never mutate the input — and return
// the new layout. Mirrored 1:1 by web/static/layout.js.
package layout

import "slices"

// FromFlat packs ids into pages of at most cap slots, in order — the
// shape migrations and imports without page info produce. An empty
// input yields a single empty page (the no-bookmarks board).
func FromFlat(ids []int64, cap int) [][]int64 {
	cap = normCap(cap)
	L := [][]int64{{}}
	for _, id := range ids {
		last := len(L) - 1
		if len(L[last]) == cap {
			L = append(L, []int64{})
			last++
		}
		L[last] = append(L[last], id)
	}
	return L
}

// Insert moves id to slot index of page — page == len(L) means a new
// last page — then lets the target page's overflow cascade forward and
// drops empty pages. The index clamps to min(len(page), cap-1): an
// "append" to a full page lands at cap-1, so the dropped tile takes the
// page's LAST slot and its previous tail is pushed to the front of the
// next page; on a page with room len(page) ≤ cap-1 already, so it still
// appends at the end. Removing id closes up its page (nothing is pulled
// from the next page), but the emptied page keeps its number until
// DropEmpty runs at the end, so the target page/index still refer to
// the input's numbering. A thumbnail drop is Insert with a huge index
// (e.g. 1e9) that clamps into the page's last slot.
func Insert(L [][]int64, id int64, page, index, cap int) [][]int64 {
	cap = normCap(cap)
	L = removeID(clone(L), id)
	if page < 0 {
		page = 0
	}
	if page > len(L) {
		page = len(L)
	}
	if page == len(L) {
		L = append(L, []int64{})
	}
	if index < 0 {
		index = 0
	}
	if max := min(len(L[page]), cap-1); index > max {
		index = max
	}
	L[page] = slices.Insert(L[page], index, id)
	return DropEmpty(cascade(L, page, cap))
}

// Add puts id at the end of the first page at or after fromPage with a
// free slot; when every page from fromPage to the last is full, id
// lands on a new last page. Add never pushes anything forward.
func Add(L [][]int64, id int64, fromPage, cap int) [][]int64 {
	cap = normCap(cap)
	L = clone(L)
	if fromPage < 0 {
		fromPage = 0
	}
	for p := fromPage; p < len(L); p++ {
		if len(L[p]) < cap {
			L[p] = append(L[p], id)
			return L
		}
	}
	return append(L, []int64{id})
}

// Remove drops id and closes up its page (nothing is pulled from the
// next page); a page left empty is dropped unless it is the only one.
func Remove(L [][]int64, id int64) [][]int64 {
	return DropEmpty(removeID(clone(L), id))
}

// Reflow re-cascades the whole board after a capacity change: a smaller
// cap pushes each page's overflow forward; a larger one is a no-op —
// free slots at page ends are never back-filled.
func Reflow(L [][]int64, cap int) [][]int64 {
	return cascade(clone(L), 0, cap)
}

// DropEmpty removes empty pages; the result always keeps at least one
// page — a single empty page 0 when nothing remains.
func DropEmpty(L [][]int64) [][]int64 {
	out := make([][]int64, 0, len(L))
	for _, pg := range L {
		if len(pg) > 0 {
			out = append(out, pg)
		}
	}
	if len(out) == 0 {
		out = append(out, []int64{})
	}
	return out
}

// cascade pushes each overflowing page's tail to the FRONT of the next
// page, starting at from and creating a new last page when the last one
// overflows. Pages before from are left alone — even over-cap ones.
func cascade(L [][]int64, from, cap int) [][]int64 {
	cap = normCap(cap)
	if from < 0 {
		from = 0
	}
	for p := from; p < len(L); p++ {
		for len(L[p]) > cap {
			tail := L[p][len(L[p])-1]
			L[p] = L[p][:len(L[p])-1]
			if p+1 == len(L) {
				L = append(L, []int64{})
			}
			L[p+1] = append([]int64{tail}, L[p+1]...)
		}
	}
	return L
}

// removeID deletes the first occurrence of id from its page, which
// keeps its place (possibly left empty). Callers must own L — the
// splice writes into the page's backing array.
func removeID(L [][]int64, id int64) [][]int64 {
	for p := range L {
		if i := slices.Index(L[p], id); i >= 0 {
			L[p] = slices.Delete(L[p], i, i+1)
			return L
		}
	}
	return L
}

// clone deep-copies a layout so an op can mutate pages in place without
// ever touching the caller's input.
func clone(L [][]int64) [][]int64 {
	out := make([][]int64, len(L))
	for i, pg := range L {
		out[i] = slices.Clone(pg)
	}
	return out
}

// normCap guards against a non-positive capacity, which would make
// every page overflow forever.
func normCap(cap int) int {
	if cap < 1 {
		return 1
	}
	return cap
}
