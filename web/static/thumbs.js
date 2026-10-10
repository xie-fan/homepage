// thumbs.js — page-thumbnail strip shown during a tile drag (Task F8).
//
// While dnd.js runs a tile drag this module mounts a fixed strip of mini
// page previews centered just above the pager dots. Each thumbnail is a
// shrunken rows×cols grid built from the page LAYOUT currently rendered
// (displayPages() — the live drag preview while dnd.js is active): a
// bookmark with a fetched icon (icon_status 'ok') renders it as a tiny
// background image, others get a colored dot on the same host-hash
// palette the tile letter avatars use. The dragged bookmark's live slot
// keeps a dashed accent mark, and update() re-renders on every preview
// change, so the strip always tracks it.
//
// Page count mirrors the displayed pages: one thumbnail per preview page
// plus — under the same condition dnd.js wantsExtraPage() appends its
// dashed drop page (the preview hasn't grown past the committed layout)
// — a final "+ 新页" thumbnail. With independent pages a new page is
// always a meaningful destination: dwelling on the thumb flips the view
// to the grid's dashed drop page, where a release creates a fresh last
// page rather than landing on an existing one.
//
// Behaviour: parking the pointer ~400ms on a thumbnail flips the main
// view to that page (one shot per entry) so the drag can continue into
// that page's grid and drop at a precise slot. Page switching is ALL a
// thumbnail does — it is never a drop target: while a thumb is under
// the pointer dnd.js disarms the edge-flip zones and shows the
// committed layout (the same thing a cancel restores), and a release
// over a thumbnail — resolved by dnd.js through dropPage() — CANCELS
// the drag exactly like a release outside the grid: nothing moves and
// nothing is sent to the server. The "+ 新页" thumb works the same way:
// dwell flips to the new-page view, release on it cancels. Thumbs
// report DISPLAYED page indices; dnd.js translates them into layout
// indices.
// hide() fades the strip out and clears every timer and the resize hook
// — the strip itself is hit-tested geometrically through
// document.elementsFromPoint and has no DOM listeners of its own, so
// nothing else needs unbinding. Imports stay one-way:
// dnd.js → thumbs.js → app.js.
//
// Hit-testability is instant: show() mounts the strip and turns its
// pointer-events on in the same synchronous pass (.live), so the very
// first drag move can already resolve a thumbnail — only the opacity
// fade animates (.show, next frame). The drag clone never shadows the
// strip: it is pointer-events:none and sits BELOW it in z-order
// (thumbs.css), and thumbAt() skips any stack entry that isn't a thumb
// even if that ever regresses.

import { state, pageSize, goToPage, displayPages } from './app.js';
import { toLayout } from './layout.js';

const HOVER_FLIP_MS = 400; // dwell on a thumbnail before goToPage
const REMOVE_MS = 240;     // detach after the ~180ms fade (+slack)

// Same mid-tone palette app.js uses for letter avatars so a mini dot
// reads in the same color as its tile (app.js doesn't export it —
// duplicating keeps the module imports one-way).
const AVATAR_COLORS = [
  '#ef5350', '#ec407a', '#ab47bc', '#7e57c2', '#5c6bc0', '#42a5f5',
  '#29b6f6', '#26a69a', '#66bb6a', '#9ccc65', '#ffa726', '#8d6e63',
];

let strip = null;     // .thumb-strip element (created once, remounted per drag)
let shown = false;    // set between show() and hide(); guards the fade-in frame
let dragId = null;    // id of the dragged bookmark (its slot stays marked)
let hoverPage = null; // thumb index under the pointer, or null
let flipTimer = 0;    // pending hover → goToPage dwell
let removeTimer = 0;  // pending post-fade detach

// ---------- mount & lifecycle ----------

function makeStrip() {
  const s = document.createElement('div');
  s.className = 'thumb-strip';
  s.setAttribute('role', 'listbox');
  s.setAttribute('aria-label', '拖到页面');
  return s;
}

// position anchors the strip just above the pager row (the dots plus, on
// narrow screens, the in-flow arrows): bottom = viewport height minus the
// pager's top edge, plus a small gap. Re-run on resize while shown.
function position() {
  if (!strip) return;
  const pager = document.querySelector('.pager');
  const top = pager ? pager.getBoundingClientRect().top : window.innerHeight;
  strip.style.bottom = `${Math.max(8, Math.round(window.innerHeight - top + 8))}px`;
}

const onResize = () => position();

export function show(id) {
  dragId = id;
  hoverPage = null;
  shown = true;
  clearTimeout(removeTimer); // re-show during a fade-out keeps the element
  if (!strip) strip = makeStrip();
  if (!strip.isConnected) document.body.append(strip);
  // Hit-testable from this same frame: .live turns pointer-events on now
  // while .show (next frame) only animates the opacity fade.
  strip.classList.add('live');
  position();
  update();
  // .show goes on next frame so the fade-in transition always runs (also
  // when re-shown mid-fade-out); `shown` covers a hide() that beat it.
  requestAnimationFrame(() => {
    if (shown && strip) strip.classList.add('show');
  });
  window.addEventListener('resize', onResize);
}

export function hide() {
  shown = false;
  hoverPage = null;
  dragId = null;
  clearTimeout(flipTimer);
  window.removeEventListener('resize', onResize);
  if (!strip) return;
  // Removing .live/.show drops pointer-events, so the fading card can
  // neither eat a post-drop click nor answer a late hit-test.
  strip.classList.remove('live', 'show');
  clearTimeout(removeTimer);
  removeTimer = setTimeout(() => { if (strip) strip.remove(); }, REMOVE_MS);
}

// ---------- content ----------

// update rebuilds every thumbnail from the page layout currently
// rendered (displayPages() — the live drag preview) and re-marks the
// current and hovered pages. dnd.js calls it after each preview change
// and each page flip.
export function update() {
  if (!shown || !strip || !strip.isConnected) return;
  const pages = displayPages();
  // Same condition as dnd.js wantsExtraPage(): the "+ 新页" drop target
  // exists while the preview hasn't already grown past the committed
  // layout — once it has, that new page is real and needs no twin.
  const wantNew = pages.length <= toLayout(state.bookmarks).length;
  const total = pages.length + (wantNew ? 1 : 0);
  const nodes = [];
  for (let p = 0; p < total; p++) nodes.push(makeThumb(p, p >= pages.length, pages));
  // Rebuilding the children resets scrollLeft — keep it so a live preview
  // doesn't snap a scrolled strip back to the start.
  const sl = strip.scrollLeft;
  strip.replaceChildren(...nodes);
  strip.scrollLeft = sl;
}

function makeThumb(p, isNew, pages) {
  const size = pageSize();
  const t = document.createElement('div');
  t.className = 'thumb';
  // The thumb's DISPLAYED page index — for "+ 新页" that's one past the
  // last preview page. dnd.js maps it to a layout page for the move.
  t.dataset.page = String(p);
  t.setAttribute('role', 'option');
  t.setAttribute('aria-label', isNew ? '新页' : `第 ${p + 1} 页`);
  if (isNew) t.classList.add('thumb--new');
  if (p === state.page) {
    t.classList.add('thumb--current');
    t.setAttribute('aria-selected', 'true');
  }
  if (p === hoverPage) t.classList.add('thumb--hover');
  const g = document.createElement('div');
  g.className = 'thumb-grid';
  // --t-rows/--t-cols feed the repeat()s in thumbs.css, like .page does.
  g.style.setProperty('--t-rows', state.grid.rows);
  g.style.setProperty('--t-cols', state.grid.cols);
  if (isNew) {
    // Empty dashed cells under a centered "+" — a visual echo of the
    // dashed drop-slot the extra grid page itself renders.
    for (let i = 0; i < size; i++) g.append(makeCell(null));
    const plus = document.createElement('span');
    plus.className = 'thumb-plus';
    plus.textContent = '+';
    g.append(plus);
  } else {
    const items = pages[p] || [];
    for (let i = 0; i < size; i++) g.append(makeCell(items[i]));
  }
  t.append(g);
  return t;
}

function makeCell(b) {
  const c = document.createElement('div');
  c.className = 'thumb-cell';
  if (!b) return c; // empty slot: faint square
  if (b.id === dragId) c.classList.add('thumb-cell--src');
  if (b.icon_status === 'ok') {
    c.classList.add('thumb-cell--icon');
    c.style.backgroundImage = `url("${iconURL(b)}")`;
    return c;
  }
  const dot = document.createElement('span');
  dot.className = 'thumb-dot';
  dot.style.background = colorFor(b);
  c.append(dot);
  return c;
}

// iconURL mirrors the cache-busted icon URL app.js builds for tiles —
// the mini icon must not serve a stale response either.
function iconURL(b) {
  const v = `${b.icon_path}|${b.updated_at}`;
  return `/api/icons/${b.id}?v=${encodeURIComponent(v)}`;
}

// colorFor reimplements the avatar host-hash so a bookmark without a
// fetched icon wears the same color it has at tile size.
function colorFor(b) {
  let host = b.url;
  try {
    host = new URL(b.url).host;
  } catch {
    // Keep the raw url — matches app.js hostOf()'s fallback.
  }
  let h = 0;
  for (let i = 0; i < host.length; i++) h = (h * 31 + host.charCodeAt(i)) >>> 0;
  return AVATAR_COLORS[h % AVATAR_COLORS.length];
}

// ---------- hit-testing & hover ----------

// thumbAt returns the page index of the thumbnail at viewport point
// (x, y), or null. The strip box itself is a cheap pre-filter; points on
// the card but between thumbs return null so the grid below stays the
// drop target. elementsFromPoint walks the whole stack at the point:
// the drag clone (pointer-events:none) or any other layer above the
// strip can't shadow a thumbnail — entries that don't resolve to a
// .thumb are simply skipped.
function thumbAt(x, y) {
  if (!shown || !strip || !strip.isConnected) return null;
  const r = strip.getBoundingClientRect();
  if (x < r.left || x > r.right || y < r.top || y > r.bottom) return null;
  for (const el of document.elementsFromPoint(x, y)) {
    const t = el.closest ? el.closest('.thumb') : null;
    if (t && strip.contains(t)) return Number(t.dataset.page);
  }
  return null;
}

// setHover tracks which thumb the pointer parks on: entering arms a
// one-shot dwell that flips the main view to that page (leaving or
// switching thumbs re-arms). The already-current page never needs a
// flip; the hover ring is re-applied after each update() rebuild.
function setHover(p) {
  if (p === hoverPage) return;
  hoverPage = p;
  clearTimeout(flipTimer);
  if (strip) {
    for (const t of strip.querySelectorAll('.thumb')) {
      t.classList.toggle('thumb--hover', Number(t.dataset.page) === p);
    }
  }
  if (p === null || p === state.page) return;
  flipTimer = setTimeout(() => {
    if (hoverPage !== null && hoverPage !== state.page) {
      goToPage(hoverPage);
      update(); // move the current-page marker to the flipped page
    }
  }, HOVER_FLIP_MS);
}

// move is called on every drag pointermove; it returns the hovered page
// index (or null) so dnd.js can keep the cancel preview and disarm edge
// flips while a thumbnail owns the pointer.
export function move(x, y) {
  const p = thumbAt(x, y);
  setHover(p);
  return p;
}

// dropPage resolves a release point to a thumbnail page index (or null)
// without touching hover state — used by dnd.js on pointerup, where any
// non-null answer cancels the drag (a release on a thumb never commits).
export function dropPage(x, y) {
  return thumbAt(x, y);
}
