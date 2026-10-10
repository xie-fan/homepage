// dnd.js — pointer-based drag & drop reordering of tiles (Task 7).
//
// app.js owns gesture arbitration: a press on a tile becomes a tile drag
// after >6px of pointer movement (mouse) or a 350ms hold (touch/pen) — see
// onPointerDown there for the full swipe-vs-drag rule. Once arbitration
// calls begin() this module owns the gesture: a floating clone follows the
// pointer, the dragged item's grid slot renders as a dashed placeholder,
// the bookmark list is reordered live, and hovering a viewport edge flips
// pages. drop() persists the order; cancel() reverts it.
//
// While a drag runs, thumbs.js mounts a strip of page thumbnails above
// the pager: parking on one dwells toward a page flip (a thumb under the
// pointer disarms both the in-grid reorder and the edge zones), and
// releasing on one lands the item at the end of that page.
//
// Valid drop targets — releasing anywhere else cancels the move like Esc:
//   1. the cell grid of the CURRENT page, padded by half the inter-cell
//      gap on each side (min 12px) — see overGrid();
//   2. the page thumbnails — see thumbs.dropPage().
// A release on the page gutters, the edge-flip zones, the header, the
// pager, below the grid or outside the window restores the pre-drag
// order and persists nothing. The live reorder preview keeps tracking
// the pointer wherever it roams; the revert happens at drop time.
//
// Imports are one-way — app.js never imports this file; it calls the
// handlers registered at the bottom. index.html loads both modules.

import {
  state, pageSize, render, goToPage, refresh, showToast, friendlyError,
  persistOrder, registerTileDnd,
} from './app.js';
import * as thumbs from './thumbs.js';

const EDGE_DWELL_MS = 700; // dwell time per edge flip (repeats while held)
const CLONE_SCALE = 1.05; // slight zoom on the floating clone

// Edge zone width: the wider of 56px and 8% of the viewport element's
// width, so the flip target stays reachable on wide screens.
const edgeZonePx = () => Math.max(56, viewport.clientWidth * 0.08);

const viewport = document.getElementById('viewport');
const track = document.getElementById('track');

// Active drag, or null. `original` is a snapshot of the bookmark array taken
// at drag start (order is what matters — items are shared, never mutated) and
// `page` the page it started on — both restored by cancel(). offX/offY is the
// grab point inside the tile so the clone keeps that offset while following
// the pointer.
let dnd = null; // {id, original, page, clone, offX, offY, x, y, edgeDir, edgeTimer}

// Viewport point of the latest primary pointerup. pointermove events
// coalesce, so on a fast release the last move dnd.js processed can lag
// the real pointerup by a cell — and the drop would commit a stale index
// (QA's "dropped on a slot, landed next to it"). The window capture-phase
// listener runs before app.js's bubble-phase pointerup handler calls
// drop(), so drop() can score the actual release point.
let lastUp = null; // {x, y, t}
window.addEventListener('pointerup', (e) => {
  if (e.isPrimary) lastUp = { x: e.clientX, y: e.clientY, t: performance.now() };
}, true);

// ---------- handlers called by app.js ----------

function begin(x, y, bm) {
  if (dnd) return;
  const src = track.querySelector(`.tile[data-id="${bm.id}"]`);
  if (!src) return; // tile vanished between pointerdown and drag start
  dnd = {
    id: bm.id,
    original: state.bookmarks.slice(),
    page: state.page,
    x, y,
    offX: 0, offY: 0,
    edgeDir: 0, edgeTimer: 0,
    clone: null,
  };
  makeClone(src, x, y);
  document.body.classList.add('dnd-active');
  render();   // source slot re-renders as the dashed placeholder
  thumbs.show(dnd.id); // page-thumbnail strip above the pager
  move(x, y); // evaluate the slot already under the pointer
}

function move(x, y) {
  if (!dnd) return;
  dnd.x = x;
  dnd.y = y;
  positionClone(x, y);
  // A page thumbnail under the pointer owns it: the grid reorder stays
  // put (hovering must not scramble the order) and the edge zones are
  // disarmed — the strip wins over both until the pointer moves off it.
  if (thumbs.move(x, y) !== null) {
    setEdge(0);
    return;
  }
  updateEdge(x, y);
  updateTarget(x, y);
}

async function drop() {
  if (!dnd) return;
  const { original } = dnd;
  // Commit at the real release point (see lastUp above); fall back to the
  // last processed move only if no fresh pointerup was seen.
  const up = lastUp && performance.now() - lastUp.t < 250 ? lastUp : dnd;
  const x = up.x;
  const y = up.y;
  // A release on a page thumbnail moves the item to the END of that page
  // (the strip is still mounted here — cleanup() hides it below). A
  // release over the current page's grid commits through updateTarget —
  // the same code path as the live preview, so the persisted index is
  // exactly the one the placeholder showed at this point. A release
  // anywhere else cancels the drag like Esc.
  const page = thumbs.dropPage(x, y);
  if (page !== null) {
    moveToPageEnd(page);
    goToPage(page); // land the view where the drop went (clamps itself)
  } else if (overGrid(x, y)) {
    updateTarget(x, y);
  } else {
    cancel(); // outside every valid target: revert, re-render, no save
    return;
  }
  const changed = state.bookmarks.some((b, i) => b.id !== original[i].id);
  cleanup();
  if (!changed) return; // landed back where it started: nothing to save
  try {
    await persistOrder(state.bookmarks.map((b) => b.id));
  } catch (err) {
    // The server is the source of truth: refetch the stored order. If even
    // that fails, put the pre-drag order back so the UI is not left lying.
    try {
      await refresh();
    } catch {
      state.bookmarks = original;
      render();
    }
    showToast(friendlyError(err));
  }
}

function cancel() {
  if (!dnd) return;
  state.bookmarks = dnd.original; // drop the live-reordered array entirely
  state.page = dnd.page;          // and go back to the page it started on
  cleanup();
}

// ---------- clone ----------

function makeClone(src, x, y) {
  const r = src.getBoundingClientRect();
  dnd.offX = x - r.left;
  dnd.offY = y - r.top;
  const clone = src.cloneNode(true);
  clone.classList.remove('tile-source');
  clone.classList.add('drag-clone');
  clone.querySelector('.tile-edit')?.remove();
  Object.assign(clone.style, {
    position: 'fixed',
    left: '0',
    top: '0',
    margin: '0',
    width: `${r.width}px`,
    height: `${r.height}px`,
    pointerEvents: 'none', // hit-testing must see the tiles/strip below
    zIndex: '100',
  });
  // Scale around the grab point so the pointer keeps its spot in the tile.
  clone.style.transformOrigin = `${dnd.offX}px ${dnd.offY}px`;
  document.body.append(clone);
  dnd.clone = clone;
  positionClone(x, y);
}

function positionClone(x, y) {
  if (!dnd.clone) return;
  dnd.clone.style.transform =
    `translate(${x - dnd.offX}px, ${y - dnd.offY}px) scale(${CLONE_SCALE})`;
}

// ---------- live reorder ----------

// landingIndex maps a hovered slot to the flat list index the dragged
// item A takes on the current page — ONE rule shared by the live preview
// (updateTarget) and the drop commit in drop(), so what the placeholder
// previews is exactly what persists. The order is a flat list and pages
// are fixed-size slices of it, so when A moves forward across a page
// boundary something must flow back the other way — that is inherent,
// and the move stays a plain one: A is the only bookmark ever moved.
// L' below means the bookmark list without A (entries past A's vacated
// slot sit one step lower); `i` is the hovered cell's flat index in the
// live list:
//   • over a tile T → A lands immediately BEFORE T: to = indexOf(T in
//     L'), clamped up to pageStart. The clamp only bites on the FIRST
//     tile of a later page: A still sits upstream of it, so T's index
//     in L' is pageStart − 1 and an unclamped insert would park A at
//     the previous page's tail — "the drop didn't go onto this page".
//     With to = pageStart, A becomes the hovered page's first tile and
//     T keeps its index — i.e. flows back to the previous page's last
//     slot (acceptable, inherent to fixed-size slices);
//   • over an empty cell past the page's last tile (i ≥ length) → A
//     goes to the end of that page's run: min(pageStart + perPage,
//     length − 1) — the list end on the last page.
// Examples (perPage = 24):
//   [A,B,C,D,E,F], A over E → [B,C,D,A,E,F]:  k = 3, to = 3 (A at 3).
//   [A,B,C,D,E,F], F over B → [A,F,B,C,D,E]:  k = 1, to = 1 (F at 1).
//   n = 27, A at index 2, over page 2's first tile (i = 24):
//     k = 23 < pageStart → to = 24 → A at 24, T stays at 23 (page 1's
//     last slot). Over page 2's second tile (i = 25): k = 24 → to = 24
//     → A at 24, that tile at 25.
// Returns {to}: A's landing index in the reordered list.
function landingIndex(list, dragId, pageStart, perPage, i) {
  const from = list.findIndex((b) => b.id === dragId);
  if (i === from) return { to: from };
  if (i < list.length) {
    const k = from >= 0 && i > from ? i - 1 : i; // hovered tile's index in L'
    return { to: Math.max(pageStart, k) };
  }
  return { to: Math.min(pageStart + perPage, list.length - 1) };
}

// applyLanding performs the computed move as a plain splice — take A
// out, insert it at t.to, and never move any other bookmark. When the
// pageStart clamp put `to` past the hovered tile's index in L', that
// tile keeps its slot and so lands on the previous page's tail: the
// flat list flows back across the page boundary by itself. Returns
// false when no move is needed.
function applyLanding(list, dragId, t) {
  const from = list.findIndex((b) => b.id === dragId);
  if (from < 0 || from === t.to) return false;
  const [bm] = list.splice(from, 1);
  list.splice(t.to, 0, bm);
  return true;
}

// insertIndexAt resolves the DOM side of the rule — which grid cell of
// the current page the point is over — and defers the index math to
// landingIndex. Returns null when the current page isn't mounted.
function insertIndexAt(x, y) {
  const pageEl = track.children[state.page];
  if (!pageEl) return null;
  const pageStart = state.page * pageSize();
  const i = pageStart + cellIndexAt(pageEl, x, y);
  return landingIndex(state.bookmarks, dnd.id, pageStart, pageSize(), i);
}

// updateTarget evaluates the insertion index under the pointer and moves
// the dragged bookmark there in the flat list — the live reorder preview.
function updateTarget(x, y) {
  const t = insertIndexAt(x, y);
  if (!t || !applyLanding(state.bookmarks, dnd.id, t)) return;
  render(); // the placeholder rides along to the item's new slot
  thumbs.update(); // the mini pages mirror the live order too
}

// moveToPageEnd splices the dragged bookmark to the last slot of page p —
// index min((p+1)·size − 1, n−1) in the FINAL array, so the "+ 新页"
// thumbnail (one past the last real page) clamps to the very end. `to` is
// already adjusted for the removed item: after splice(from, 1) an insert
// at `to` puts the item exactly on that index whichever side it came
// from.
function moveToPageEnd(p) {
  const list = state.bookmarks;
  const from = list.findIndex((b) => b.id === dnd.id);
  if (from < 0) return;
  const to = Math.min((p + 1) * pageSize() - 1, list.length - 1);
  if (from === to) return;
  const [bm] = list.splice(from, 1);
  list.splice(to, 0, bm);
}

// gridMetrics returns the cell-grid geometry of a .page: the first
// cell's origin (ox, oy), the full grid extent and the resolved tile/gap
// metrics. The page centers its grid, so the origin is derived from the
// element box, the grid padding, and the fixed tile metrics layout()
// wrote on :root. Shared by cellIndexAt (point → cell) and overGrid
// (point → valid drop area?).
function gridMetrics(pageEl) {
  const { rows, cols } = state.grid;
  const root = getComputedStyle(document.documentElement);
  const tileW = parseFloat(root.getPropertyValue('--tile-w')) || 106;
  const tileH = parseFloat(root.getPropertyValue('--tile-h')) || 98;
  const ps = getComputedStyle(pageEl);
  // Column/row gaps differ when the spacing preset adds row air.
  const gapX = parseFloat(ps.columnGap) || 0;
  const gapY = parseFloat(ps.rowGap) || 0;
  const padL = parseFloat(ps.paddingLeft) || 0;
  const padR = parseFloat(ps.paddingRight) || 0;
  const padT = parseFloat(ps.paddingTop) || 0;
  const padB = parseFloat(ps.paddingBottom) || 0;
  const r = pageEl.getBoundingClientRect();
  const gridW = cols * tileW + (cols - 1) * gapX;
  const gridH = rows * tileH + (rows - 1) * gapY;
  const ox = r.left + padL + (r.width - padL - padR - gridW) / 2;
  const oy = r.top + padT + (r.height - padT - padB - gridH) / 2;
  return { ox, oy, gridW, gridH, tileW, tileH, gapX, gapY };
}

// overGrid decides whether a release point lands in the valid grid drop
// area: the CURRENT page's cell box padded by half the inter-cell gap on
// each side (min 12px). Everything outside it — gutters, edge-flip
// zones, header, pager, below the grid, outside the window — cancels the
// drag (see the file header for the full rule).
function overGrid(x, y) {
  const pageEl = track.children[state.page];
  if (!pageEl) return false;
  const g = gridMetrics(pageEl);
  const padX = Math.max(12, g.gapX / 2);
  const padY = Math.max(12, g.gapY / 2);
  return x >= g.ox - padX && x <= g.ox + g.gridW + padX &&
    y >= g.oy - padY && y <= g.oy + g.gridH + padY;
}

// cellIndexAt finds which grid cell of a .page contains (x, y). A point
// outside the grid snaps to the nearest border cell: the live preview
// may track the pointer anywhere — only the drop is area-restricted.
function cellIndexAt(pageEl, x, y) {
  const { rows, cols } = state.grid;
  const g = gridMetrics(pageEl);
  return cellCoord(y, g.oy, g.tileH, g.gapY, rows) * cols +
    cellCoord(x, g.ox, g.tileW, g.gapX, cols);
}

// cellCoord converts one axis into a cell number. Each repeat unit is
// tile+gap; a point inside the gap snaps to the nearer cell.
function cellCoord(p, origin, cell, gap, n) {
  const u = (p - origin) / (cell + gap);
  let i = Math.floor(u);
  if (u - i > cell / (cell + gap)) i += 1;
  return Math.min(Math.max(i, 0), n - 1);
}

// ---------- edge page flips ----------
//
// Parking the pointer inside an edge zone flips pages on a dwell timer:
// entering the zone arms it, each flip re-arms it, and leaving the zone
// cancels — no pointer movement is needed once it's parked.

function edgeDirAt(x, y) {
  const r = viewport.getBoundingClientRect();
  const zone = edgeZonePx();
  if (x - r.left < zone) return -1;
  if (r.right - x < zone) return 1;
  // The arrow buttons sit inside the edge zone on wide layouts but extend
  // past it; hovering one counts as hovering the edge.
  const hit = document.elementFromPoint(x, y);
  if (hit?.closest('.nav-prev')) return -1;
  if (hit?.closest('.nav-next')) return 1;
  return 0;
}

// setEdgeHint marks the occupied edge zone so style.css can highlight it.
function setEdgeHint(dir) {
  document.body.classList.toggle('dnd-edge-left', dir === -1);
  document.body.classList.toggle('dnd-edge-right', dir === 1);
}

function updateEdge(x, y) {
  setEdge(edgeDirAt(x, y));
}

// setEdge applies a resolved edge direction: entering a zone arms the
// dwell timer, leaving it disarms it. move() also calls this with 0 while
// a page thumbnail owns the pointer so the strip beats the edge zones.
function setEdge(dir) {
  if (dir === dnd.edgeDir) return;
  dnd.edgeDir = dir;
  setEdgeHint(dir);
  clearTimeout(dnd.edgeTimer);
  if (dir !== 0) dnd.edgeTimer = setTimeout(() => edgeFlip(dir), EDGE_DWELL_MS);
}

function edgeFlip(dir) {
  if (!dnd || dnd.edgeDir !== dir) return;
  const before = state.page;
  goToPage(before + dir); // clamps at both ends, incl. the extra drag page
  // At the bound nothing flips; the timer stays disarmed until the
  // pointer leaves and re-enters the zone (edgeDir keeps the direction).
  if (state.page === before) return;
  thumbs.update(); // move the strip's current-page marker to the new page
  updateTarget(dnd.x, dnd.y); // the page under the pointer just changed
  // Keep flipping while the pointer stays parked in the edge zone.
  dnd.edgeTimer = setTimeout(() => edgeFlip(dir), EDGE_DWELL_MS);
}

// ---------- cleanup & registration ----------

function cleanup() {
  clearTimeout(dnd.edgeTimer);
  dnd.clone?.remove();
  dnd = null;
  lastUp = null; // next drag must not score a stale release point
  document.body.classList.remove('dnd-active', 'dnd-edge-left', 'dnd-edge-right');
  thumbs.hide(); // fades the strip; clears its dwell timer + resize hook
  render(); // clears the placeholder marking and the extra drag page
}

registerTileDnd({
  begin, move, drop, cancel,
  active: () => dnd !== null,
  draggingId: () => (dnd ? dnd.id : null),
  // While dragging on a full last page, app.js appends one empty page so an
  // item can be carried "past the end" (it lands at the tail of the list).
  wantsExtraPage: () =>
    dnd !== null &&
    state.bookmarks.length > 0 &&
    state.bookmarks.length % pageSize() === 0,
});
