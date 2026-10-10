// dnd.js — pointer-based drag & drop across independent pages (Round 8).
//
// app.js owns gesture arbitration: a press on a tile becomes a tile drag
// after >6px of pointer movement (mouse) or a 350ms hold (touch/pen) —
// see onPointerDown there for the full swipe-vs-drag rule. Once
// arbitration calls begin() this module owns the gesture: a floating
// clone follows the pointer and the dragged item's grid slot renders as
// a dashed placeholder.
//
// Pages are independent: every bookmark carries page + position, and the
// committed order is a LAYOUT (ids per page — layout.js, the client
// mirror of the server's algorithm). At drag start we snapshot the
// committed layout L0 and never touch state.bookmarks afterwards: every
// hover recomputes preview = insert(L0, id, page, index, cap) FROM THE
// SNAPSHOT (never cumulative) and app.js renders that preview in place
// of the real pages. Esc, or a release outside every valid target, just
// drops the preview — the committed layout was never modified.
//
// Hover → index rules (an index counts slots AFTER removing the dragged
// id, exactly like insert() applies it):
//   • over a tile at slot i        → index i
//   • over an empty slot on page p → index len(L0[p] without the dragged
//     id); unified with the tile rule as index = min(cell, that length)
//   • the dashed "+ 新页" page      → a new last page: page = len(L0)
//   • a page thumbnail             → index 1e9 (the server clamps it to
//     the end of the page; the preview does the same)
//   • anywhere outside the current page's padded grid and the thumbnails
//     → preview = L0, so what you see is what a cancel restores
//
// A DISPLAYED page index can differ from its L0 index in exactly one
// case: the dragged item was the only bookmark on its L0 page and the
// preview moved it off — the emptied page no longer exists and later
// displayed pages sit one lower. mapPage() translates displayed → L0.
//
// A drop POSTs /api/bookmarks/move {id,page,index} with the same values
// the current preview used and adopts the returned list — the server is
// the source of truth. While a drag runs, thumbs.js mounts a strip of
// page thumbnails above the pager; edge-dwell page flips and thumbnail
// dwell switching work like before.
//
// Imports are one-way — app.js never imports this file; it calls the
// handlers registered at the bottom. index.html loads both modules.

import {
  state, pageSize, render, goToPage, showToast, friendlyError,
  commitMove, registerTileDnd,
} from './app.js';
import { insert, toLayout } from './layout.js';
import * as thumbs from './thumbs.js';

const EDGE_DWELL_MS = 700; // dwell time per edge flip (repeats while held)
const CLONE_SCALE = 1.05; // slight zoom on the floating clone
const DROP_END = 1e9;     // index meaning "the end of the page" (clamped)

// Edge zone width: the wider of 56px and 8% of the viewport element's
// width, so the flip target stays reachable on wide screens.
const edgeZonePx = () => Math.max(56, viewport.clientWidth * 0.08);

const viewport = document.getElementById('viewport');
const track = document.getElementById('track');

// Active drag, or null. `L0` is the committed layout snapshot taken at
// drag start, `preview` the layout the grid currently renders, and
// `target` the {page,index} — in L0 coordinates — that produced it, i.e.
// exactly what a drop POSTs. `page` is the view at drag start (cancel
// restores it), `srcPage` the dragged id's L0 page. offX/offY is the
// grab point inside the tile so the clone keeps that offset while
// following the pointer.
let dnd = null; // {id, L0, preview, target, page, srcPage, clone, offX, offY, x, y, edgeDir, edgeTimer}

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

// ---------- helpers ----------

function sameLayout(a, b) {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const pa = a[i];
    const pb = b[i];
    if (pa.length !== pb.length) return false;
    for (let j = 0; j < pa.length; j++) if (pa[j] !== pb[j]) return false;
  }
  return true;
}

// mapPage translates a DISPLAYED page index (an index into the live
// preview — the page the pointer is over) into the L0 page index that
// insert() and the move endpoint expect. They differ only when the
// dragged bookmark was the sole item of its L0 page and the preview
// moved it off: that page no longer exists and displayed pages after it
// sit one lower. The "+ 新页" targets — index len(preview) — land on
// len(L0), the "new page" the server understands; anything past the end
// clamps there too.
function mapPage(i) {
  const shifted = dnd.preview.length < dnd.L0.length;
  const p = shifted && i >= dnd.srcPage ? i + 1 : i;
  return Math.min(Math.max(p, 0), dnd.L0.length);
}

// setPreview swaps in a freshly computed preview layout and the target
// that produced it, re-rendering only when the layout actually changed —
// the grid, dots and thumbnails all follow it.
function setPreview(L, target) {
  dnd.target = target;
  if (sameLayout(L, dnd.preview)) return;
  dnd.preview = L;
  render();        // pages render from the preview via displayPages()
  thumbs.update(); // the mini pages mirror the preview too
}

// ---------- handlers called by app.js ----------

function begin(x, y, bm) {
  if (dnd) return;
  const src = track.querySelector(`.tile[data-id="${bm.id}"]`);
  if (!src) return; // tile vanished between pointerdown and drag start
  const L0 = toLayout(state.bookmarks);
  const srcPage = Math.max(0, L0.findIndex((p) => p.includes(bm.id)));
  dnd = {
    id: bm.id,
    L0,
    preview: L0,
    target: null,
    page: state.page,
    srcPage,
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
  // A page thumbnail under the pointer owns it: the preview shows the
  // item appended to that page (index 1e9 — the very value a drop
  // sends; insert() clamps it to the end just like the server does)
  // and the edge zones are disarmed until the pointer moves off.
  const tp = thumbs.move(x, y);
  if (tp !== null) {
    setEdge(0);
    const p = mapPage(tp);
    setPreview(insert(dnd.L0, dnd.id, p, DROP_END, pageSize()),
      { page: p, index: DROP_END });
    return;
  }
  updateEdge(x, y);
  updateTarget(x, y);
}

async function drop() {
  if (!dnd) return;
  // Commit at the real release point (see lastUp above); fall back to
  // the last processed move only if no fresh pointerup was seen.
  const up = lastUp && performance.now() - lastUp.t < 250 ? lastUp : dnd;
  const x = up.x;
  const y = up.y;
  // A release on a page thumbnail moves the item to the END of that
  // page (the strip is still mounted here — cleanup() hides it below).
  // A release over the current page's grid commits the same {page,index}
  // the live preview just used, so what the placeholder showed is what
  // persists. A release anywhere else cancels the drag like Esc.
  let target = null;
  const tp = thumbs.dropPage(x, y);
  if (tp !== null) {
    const p = mapPage(tp);
    target = { page: p, index: DROP_END };
    setPreview(insert(dnd.L0, dnd.id, p, DROP_END, pageSize()), target);
    goToPage(tp); // land the view where the drop went (clamps itself)
  } else if (overGrid(x, y)) {
    updateTarget(x, y); // recompute at the real release point
    target = dnd.target;
  } else {
    cancel(); // outside every valid target: revert, re-render, no save
    return;
  }
  const unchanged = target === null || sameLayout(dnd.preview, dnd.L0);
  const id = dnd.id;
  cleanup();
  if (unchanged) return; // landed back where it started: nothing to save
  try {
    await commitMove(id, target.page, target.index);
  } catch (err) {
    // state.bookmarks still holds the committed layout (it was never
    // mutated) — re-render shows L0 again, i.e. the revert.
    render();
    showToast(friendlyError(err));
  }
}

function cancel() {
  if (!dnd) return;
  state.page = dnd.page; // go back to the page the drag started on
  cleanup();             // drops the preview — render() shows L0 again
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

// ---------- live preview ----------

// updateTarget resolves the drop index under the pointer on the CURRENT
// page's grid and previews the move. A point outside the valid drop area
// previews the committed layout instead — matching what a release there
// (a cancel) restores.
function updateTarget(x, y) {
  const pageEl = track.children[state.page];
  if (!pageEl || !overGrid(x, y)) {
    setPreview(dnd.L0, null);
    return;
  }
  let page;
  let index;
  if (state.page >= dnd.preview.length) {
    // The dashed "+ 新页" page at the end: a drop starts a new last page.
    page = dnd.L0.length;
    index = 0;
  } else {
    page = mapPage(state.page);
    // Slots past the page's items are empty: the item lands at the end
    // of the page — index = len(L0 page without the dragged id).
    const len = page < dnd.L0.length
      ? dnd.L0[page].length - (page === dnd.srcPage ? 1 : 0)
      : 0;
    index = Math.min(cellIndexAt(pageEl, x, y), len);
  }
  setPreview(insert(dnd.L0, dnd.id, page, index, pageSize()), { page, index });
}

// gridMetrics returns the cell-grid geometry of the CURRENT .page: the
// first cell's origin (ox, oy), the full grid extent and the resolved
// tile/gap metrics. The page centers its grid, so the origin is derived
// from the element box, the grid padding, and the fixed tile metrics
// layout() wrote on :root. Shared by cellIndexAt (point → cell) and
// overGrid (point → valid drop area?).
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
  // Use the viewport's box, not pageEl.getBoundingClientRect(): right
  // after an edge-dwell/thumbnail page flip the track's transform is
  // still animating, so the current page's live rect is mid-slide. Every
  // page is flex:0 0 100% of a viewport-sized track, so the CURRENT
  // page's settled box is exactly the viewport's (callers only ever pass
  // track.children[state.page]).
  const r = viewport.getBoundingClientRect();
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
// outside the grid snaps to the nearest border cell.
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
  goToPage(before + dir); // clamps at both ends, incl. the "+ 新页" page
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
  render(); // the committed layout replaces the preview
}

registerTileDnd({
  begin, move, drop, cancel,
  active: () => dnd !== null,
  draggingId: () => (dnd ? dnd.id : null),
  // The layout app.js renders in place of the committed pages while a
  // drag runs (arrays of ids per page), or null when idle.
  previewPages: () => (dnd ? dnd.preview : null),
  // The dashed "+ 新页" page (a "start a new page" drop target) stays
  // available while the preview hasn't already grown past the committed
  // layout — once it has, the new page is real and needs no twin.
  wantsExtraPage: () =>
    dnd !== null && dnd.preview.length <= dnd.L0.length,
});
