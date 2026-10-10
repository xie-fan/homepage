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
// Imports are one-way — app.js never imports this file; it calls the
// handlers registered at the bottom. index.html loads both modules.

import {
  state, pageSize, render, goToPage, refresh, showToast,
  persistOrder, registerTileDnd,
} from './app.js';

const EDGE_PX = 40; // pointer this close to a viewport side flips pages
const EDGE_DELAY_MS = 600; // hover time per edge flip (repeats while held)
const CLONE_SCALE = 1.05; // slight zoom on the floating clone

const viewport = document.getElementById('viewport');
const track = document.getElementById('track');

// Active drag, or null. `original` is a snapshot of the bookmark array taken
// at drag start (order is what matters — items are shared, never mutated) and
// `page` the page it started on — both restored by cancel(). offX/offY is the
// grab point inside the tile so the clone keeps that offset while following
// the pointer.
let dnd = null; // {id, original, page, clone, offX, offY, x, y, edgeDir, edgeTimer}

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
  move(x, y); // evaluate the slot already under the pointer
}

function move(x, y) {
  if (!dnd) return;
  dnd.x = x;
  dnd.y = y;
  positionClone(x, y);
  updateEdge(x, y);
  updateTarget(x, y);
}

async function drop() {
  if (!dnd) return;
  const { original } = dnd;
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
    showToast(`顺序保存失败：${err.message}`);
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
    pointerEvents: 'none', // elementFromPoint must see the tiles below
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

// updateTarget maps the pointer to a grid cell on the current page and
// moves the dragged bookmark to that position in the flat list.
function updateTarget(x, y) {
  const pageEl = track.children[state.page];
  if (!pageEl) return;
  const size = pageSize();
  const cell = cellIndexAt(pageEl, x, y);
  const to = Math.min(
    Math.max(state.page * size + cell, 0),
    state.bookmarks.length - 1,
  );
  const from = state.bookmarks.findIndex((b) => b.id === dnd.id);
  if (from < 0 || from === to) return;
  const [bm] = state.bookmarks.splice(from, 1);
  state.bookmarks.splice(to, 0, bm);
  render(); // the placeholder rides along to the item's new slot
}

// cellIndexAt finds which grid cell of a .page contains (x, y). The page
// centers its grid, so the first cell's origin is derived from the element
// box, the grid padding, and the fixed tile metrics from style.css.
function cellIndexAt(pageEl, x, y) {
  const { rows, cols } = state.grid;
  const root = getComputedStyle(document.documentElement);
  const tileW = parseFloat(root.getPropertyValue('--tile-w')) || 96;
  const tileH = parseFloat(root.getPropertyValue('--tile-h')) || 106;
  const ps = getComputedStyle(pageEl);
  const gap = parseFloat(ps.columnGap) || 0;
  const padL = parseFloat(ps.paddingLeft) || 0;
  const padR = parseFloat(ps.paddingRight) || 0;
  const padT = parseFloat(ps.paddingTop) || 0;
  const padB = parseFloat(ps.paddingBottom) || 0;
  const r = pageEl.getBoundingClientRect();
  const gridW = cols * tileW + (cols - 1) * gap;
  const gridH = rows * tileH + (rows - 1) * gap;
  const ox = r.left + padL + (r.width - padL - padR - gridW) / 2;
  const oy = r.top + padT + (r.height - padT - padB - gridH) / 2;
  return cellCoord(y, oy, tileH, gap, rows) * cols +
    cellCoord(x, ox, tileW, gap, cols);
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

function edgeDirAt(x, y) {
  const r = viewport.getBoundingClientRect();
  if (x - r.left < EDGE_PX) return -1;
  if (r.right - x < EDGE_PX) return 1;
  // The arrow buttons sit inside the edge zone on wide layouts but extend
  // past it; hovering one counts as hovering the edge.
  const hit = document.elementFromPoint(x, y);
  if (hit?.closest('.nav-prev')) return -1;
  if (hit?.closest('.nav-next')) return 1;
  return 0;
}

function updateEdge(x, y) {
  const dir = edgeDirAt(x, y);
  if (dir === dnd.edgeDir) return;
  dnd.edgeDir = dir;
  clearTimeout(dnd.edgeTimer);
  if (dir !== 0) dnd.edgeTimer = setTimeout(() => edgeFlip(dir), EDGE_DELAY_MS);
}

function edgeFlip(dir) {
  if (!dnd || dnd.edgeDir !== dir) return;
  const before = state.page;
  goToPage(before + dir); // clamps at both ends, incl. the extra drag page
  if (state.page === before) {
    dnd.edgeDir = 0; // at the bound — only a fresh hover re-arms the flip
    return;
  }
  updateTarget(dnd.x, dnd.y); // the page under the pointer just changed
  // Keep flipping while the pointer stays parked in the edge zone.
  dnd.edgeTimer = setTimeout(() => edgeFlip(dir), EDGE_DELAY_MS);
}

// ---------- cleanup & registration ----------

function cleanup() {
  clearTimeout(dnd.edgeTimer);
  dnd.clone?.remove();
  dnd = null;
  document.body.classList.remove('dnd-active');
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
