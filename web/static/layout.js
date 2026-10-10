// layout.js — independent-page layout algorithm (Round 8).
// Pure functions over a plain data structure, no DOM: this module is the
// client-side 1:1 mirror of internal/layout on the server and is
// unit-testable in node (it's an ES module with no browser imports).
//
// A layout is an array of pages; a page is an array of bookmark ids in
// order. Invariants: a page holds at most `cap` ids (cap = rows*cols),
// free slots appear only at a page's end, pages are numbered 0..P-1 with
// no empty page — except a single empty page 0 when nothing exists.
// Every op returns a NEW layout; the input is never mutated.

const copy = (L) => (L || []).map((page) => page.slice());

// normCap guards against a non-positive capacity, which would make
// every page overflow forever.
const normCap = (cap) => (cap < 1 ? 1 : cap);

// fromFlat fills pages sequentially — the shape used by migration and by
// imports that carry no page info. Always returns at least one page.
export function fromFlat(ids, cap) {
  cap = normCap(cap);
  const L = [[]];
  for (const id of ids) {
    if (L[L.length - 1].length >= cap) L.push([]);
    L[L.length - 1].push(id);
  }
  return L;
}

// cascade pushes overflow forward starting at page `from`: while a page
// holds more than cap ids its LAST item moves to the FRONT of the next
// page, creating a new last page when the overflow runs off the end.
function cascade(L, from, cap) {
  cap = normCap(cap);
  for (let p = Math.max(0, from); p < L.length; p++) {
    while (L[p].length > cap) {
      if (p + 1 === L.length) L.push([]);
      L[p + 1].unshift(L[p].pop());
    }
  }
  return L;
}

// insert moves `id` to (page, index): the id is first removed from its
// current page — that page closes up and NOTHING is pulled from the next
// page — then inserted into the target page (page == L.length means a new
// page at the end) at min(index, len(page)). Overflow cascades forward
// from the target page; pages left empty are dropped at the end.
// A thumbnail drop is insert with index = len(page), so any index past
// the end (e.g. the drag code's 1e9) clamps there.
export function insert(L, id, page, index, cap) {
  cap = normCap(cap);
  const out = copy(L);
  for (const p of out) {
    const i = p.indexOf(id);
    if (i >= 0) {
      p.splice(i, 1);
      break;
    }
  }
  const t = Math.min(Math.max(0, page), out.length);
  if (t === out.length) out.push([]);
  const at = Math.min(Math.max(0, index), out[t].length);
  out[t].splice(at, 0, id);
  cascade(out, t, cap);
  return dropEmpty(out);
}

// add appends `id` at the end of the first page p >= fromPage that still
// has a free slot; when none qualifies a new page is appended at the end.
// Nothing else moves.
export function add(L, id, fromPage, cap) {
  cap = normCap(cap);
  const out = copy(L);
  for (let p = Math.max(0, fromPage); p < out.length; p++) {
    if (out[p].length < cap) {
      out[p].push(id);
      return out;
    }
  }
  out.push([id]);
  return out;
}

// remove takes `id` out of its page (the page closes up); a page left
// empty is dropped — unless it is the only page.
export function remove(L, id) {
  const out = copy(L);
  for (const p of out) {
    const i = p.indexOf(id);
    if (i >= 0) {
      p.splice(i, 1);
      break;
    }
  }
  return dropEmpty(out);
}

// reflow re-cascades every page after a grid change: shrinking pushes
// overflow forward, growing is a no-op.
export function reflow(L, cap) {
  return cascade(copy(L), 0, cap);
}

// dropEmpty removes empty pages but always keeps at least one.
export function dropEmpty(L) {
  const out = (L || []).filter((p) => p.length > 0);
  return out.length > 0 ? out : [[]];
}

// toLayout groups bookmark objects ({id, page, position}) into a layout:
// ids grouped by `page` and ordered by `position` inside each page.
// Always returns at least one page.
export function toLayout(bookmarks) {
  const groups = new Map();
  let max = -1;
  for (const b of bookmarks) {
    const p = Number.isInteger(b.page) && b.page > 0 ? b.page : 0;
    if (!groups.has(p)) groups.set(p, []);
    groups.get(p).push(b);
    if (p > max) max = p;
  }
  const L = [];
  for (let p = 0; p <= max; p++) {
    const items = groups.get(p) || [];
    items.sort((a, b) => (a.position - b.position) || (a.id - b.id));
    L.push(items.map((b) => b.id));
  }
  return L.length > 0 ? L : [[]];
}
