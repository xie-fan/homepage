// app.js — homepage UI: state, paged grid rendering, navigation, modals.
// All DOM is built with createElement/textContent (never innerHTML) so
// bookmark titles and URLs cannot inject markup.

import { api } from './api.js';

// ---------- state ----------

const state = {
  bookmarks: [],
  grid: { rows: 4, cols: 6 },
  page: 0,
  editing: null, // bookmark in the modal; null while adding
  loadError: null,
};

const PAGE_KEY = 'homepage.page'; // sessionStorage key for the current page

// Wheel: accumulate deltas so both wheel mice and trackpads work; a
// threshold + cooldown makes one gesture flip exactly one page.
const WHEEL_THRESHOLD = 40; // accumulated delta units to trigger a flip
const WHEEL_COOLDOWN = 500; // ms between page flips
const WHEEL_RESET_MS = 200; // idle gap that ends a gesture

// Swipe (pointer events).
const SWIPE_SLOP = 8; // px before a press becomes a drag
const SWIPE_FLIP = 0.15; // fraction of viewport width that flips a page
const FLICK_VELOCITY = 0.4; // px/ms that counts as a fast flick

// Tile drag thresholds (the drag itself lives in dnd.js; onPointerDown
// arbitrates between tile drag and page swipe).
const TILE_DRAG_SLOP = 6; // px of movement (mouse on tile) that starts a drag
const TILE_HOLD_MS = 350; // touch/pen hold on a tile that starts a drag
const HOLD_SLOP = 10; // px of wandering that forfeits a pending touch hold

const POLL_INTERVAL = 1500; // ms between bookmark re-fetches
const POLL_TRIES = 8; // give up after ~12s of pending icons

// Mid-tone palette for letter avatars; readable white text on both themes.
const AVATAR_COLORS = [
  '#ef5350', '#ec407a', '#ab47bc', '#7e57c2', '#5c6bc0', '#42a5f5',
  '#29b6f6', '#26a69a', '#66bb6a', '#9ccc65', '#ffa726', '#8d6e63',
];

const $ = (sel) => document.querySelector(sel);
const els = {};

// el creates an element; text always goes through textContent.
function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// ---------- geometry ----------

const pageSize = () => state.grid.rows * state.grid.cols;
const pageCount = () => Math.max(1, Math.ceil(state.bookmarks.length / pageSize()));
// While a tile drag runs on a full last page, dnd.js asks for one extra
// empty page so an item can be carried "past the end" — a drop there lands
// at the tail of the flat position list.
const displayPageCount = () =>
  pageCount() + (tileDnd && tileDnd.wantsExtraPage() ? 1 : 0);
const clampPage = () => {
  state.page = Math.min(Math.max(state.page, 0), displayPageCount() - 1);
};

// ---------- layout ----------
//
// The grid keeps the configured rows/cols on every viewport: the tile is
// shrunk until the whole grid fits, never grown past the 96×106 desktop
// tile. layout() measures the real .viewport box and writes the resolved
// metrics (--tile-w/--tile-h/--grid-gap/--page-pad-*) as px on :root —
// style.css derives icon/label/edit sizes from --tile-w via clamp(), and
// dnd.js parses --tile-* back into numbers. The formula is documented
// next to the fallbacks in style.css (:root).
const TILE_DESKTOP_W = 96;
const TILE_DESKTOP_H = 106;
function layout() {
  const { rows, cols } = state.grid;
  const vw = els.viewport.clientWidth;
  const vh = els.viewport.clientHeight;
  // Side arrows overlay the page gutters at ≥768px (the .nav-arrow media
  // query); below that — or on a single page where they are disabled —
  // they sit in the pager row and a slim edge inset is enough.
  const overlay =
    getComputedStyle(els.navPrev).position === 'absolute' &&
    displayPageCount() > 1;
  const padX = overlay ? 64 : 12;
  const padY = 12;
  // Gap scales with the tile (8–14px). Bound the tile with the max gap
  // first, pick the gap, then re-fit — one pass converges.
  const fit = (gap) => Math.min(
    TILE_DESKTOP_W,
    (vw - 2 * padX - (cols - 1) * gap) / cols,
    ((vh - 2 * padY - (rows - 1) * gap) / rows) * (TILE_DESKTOP_W / TILE_DESKTOP_H),
  );
  const gap = Math.round(Math.min(14, Math.max(8, fit(14) * 0.15)));
  const tile = Math.max(1, Math.floor(fit(gap)));
  // Tile height follows the desktop 96:106 aspect, grown a little when
  // the floor-clamped icon (36px) plus label would not fit the aspect,
  // and always capped at the per-row allotment so the grid can never
  // overflow vertically. The +22 approximates padding + link gap + the
  // 11px label line (mirrors --icon: clamp(36, .7·w, 64) in style.css).
  const rowFit = Math.floor((vh - 2 * padY - (rows - 1) * gap) / rows);
  const icon = Math.min(64, Math.max(36, tile * 0.7));
  const tileH = Math.max(1, Math.min(rowFit,
    Math.max(Math.floor(tile * (TILE_DESKTOP_H / TILE_DESKTOP_W)), Math.ceil(icon) + 22)));
  const root = document.documentElement.style;
  root.setProperty('--tile-w', `${tile}px`);
  root.setProperty('--tile-h', `${tileH}px`);
  root.setProperty('--grid-gap', `${gap}px`);
  root.setProperty('--page-pad-x', `${padX}px`);
  root.setProperty('--page-pad-y', `${padY}px`);
}

// ---------- rendering ----------

function render() {
  clampPage();
  sessionStorage.setItem(PAGE_KEY, String(state.page));
  layout();
  renderTrack();
  renderDots();
  updateArrows();
  // Do not yank the track mid-gesture: during a swipe the finger owns it,
  // during a tile drag goToPage (edge flips) owns it.
  if ((!drag || !drag.active) && !(tileDnd && tileDnd.active())) {
    offsetTrack(0, false);
  }
}

function renderTrack() {
  els.track.replaceChildren();
  const size = pageSize();
  for (let p = 0; p < displayPageCount(); p++) {
    const page = el('div', 'page');
    // --cols/--rows feed the repeat()s in style.css; the track sizes
    // (--tile-w/--tile-h) come from layout() so the grid always fits.
    page.style.setProperty('--cols', state.grid.cols);
    page.style.setProperty('--rows', state.grid.rows);
    const items = state.bookmarks.slice(p * size, (p + 1) * size);
    if (items.length === 0) {
      if (p === 0) {
        page.append(el('p', 'empty',
          state.loadError ? `加载失败：${state.loadError}` : '还没有书签，点击 + 添加'));
      } else {
        // The extra last page shown during a drag on a full grid: a dashed
        // slot marks it as a valid "new page" drop target.
        page.append(el('div', 'tile drop-slot'));
      }
    } else {
      for (const b of items) page.append(makeTile(b));
    }
    els.track.append(page);
  }
}

function makeTile(b) {
  const tile = el('div', 'tile');
  tile.dataset.id = String(b.id); // Task 7 (drag & drop) hooks onto data-id
  // The dragged item stays in the list; its slot renders as the placeholder.
  if (tileDnd && tileDnd.draggingId() === b.id) tile.classList.add('tile-source');

  const link = el('a', 'tile-link');
  link.href = b.url; // real anchor: Ctrl/Cmd/middle click opens a new tab natively
  link.title = b.title;
  link.draggable = false;
  link.append(makeIcon(b), el('span', 'tile-title', b.title));

  const edit = el('button', 'tile-edit', '✎');
  edit.type = 'button';
  edit.setAttribute('aria-label', '编辑');
  edit.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    openBookmarkModal(b);
  });
  tile.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    // While a tile drag is armed or running (touch hold), the context menu
    // — and our edit shortcut — must not pop over the drag.
    if ((drag && drag.mode) || (tileDnd && tileDnd.active())) return;
    openBookmarkModal(b);
  });

  tile.append(link, edit);
  return tile;
}

function makeIcon(b) {
  const box = el('div', 'tile-icon');
  if (b.icon_status === 'ok') {
    const img = new Image();
    img.src = `/api/icons/${b.id}?v=${encodeURIComponent(b.updated_at)}`;
    img.alt = '';
    img.draggable = false;
    img.addEventListener('error', () => box.replaceChildren(avatar(b)), { once: true });
    box.append(img);
  } else {
    box.append(avatar(b));
  }
  return box;
}

// avatar is the fallback icon: first letter/CJK char of the title over a
// color derived from the URL host (stable across reloads).
function avatar(b) {
  const node = el('div', 'avatar');
  node.textContent = ([...b.title][0] ?? '?').toUpperCase();
  node.style.background = AVATAR_COLORS[hashStr(hostOf(b.url)) % AVATAR_COLORS.length];
  return node;
}

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function hashStr(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h;
}

function renderDots() {
  els.dots.replaceChildren();
  for (let i = 0; i < displayPageCount(); i++) {
    const dot = el('button', 'dot');
    dot.type = 'button';
    dot.setAttribute('aria-label', `第 ${i + 1} 页`);
    if (i === state.page) dot.classList.add('active');
    dot.addEventListener('click', () => goToPage(i));
    els.dots.append(dot);
  }
}

function updateArrows() {
  els.navPrev.disabled = state.page <= 0;
  els.navNext.disabled = state.page >= displayPageCount() - 1;
}

// ---------- navigation ----------

function goToPage(i) {
  state.page = Math.min(Math.max(i, 0), displayPageCount() - 1);
  sessionStorage.setItem(PAGE_KEY, String(state.page));
  offsetTrack(0, true);
  renderDots();
  updateArrows();
}

// offsetTrack positions the track: current page plus an optional drag dx.
function offsetTrack(dx, animate) {
  const w = els.viewport.clientWidth || 1;
  els.track.classList.toggle('no-anim', !animate);
  els.track.style.transform = `translateX(${-state.page * w + dx}px)`;
}

function restorePage() {
  const saved = Number.parseInt(sessionStorage.getItem(PAGE_KEY) ?? '', 10);
  if (Number.isFinite(saved)) state.page = saved; // render() clamps it
}

// ---------- wheel & keys ----------

let wheelAcc = 0;
let wheelLastFire = -Infinity;
let wheelResetTimer = 0;

function onWheel(e) {
  if (modalOpen() || (drag && drag.active) || (tileDnd && tileDnd.active())) return;
  e.preventDefault(); // block scroll-chaining and browser swipe-back
  const d = Math.abs(e.deltaX) >= Math.abs(e.deltaY) ? e.deltaX : e.deltaY;
  wheelAcc += d;
  clearTimeout(wheelResetTimer);
  wheelResetTimer = setTimeout(() => { wheelAcc = 0; }, WHEEL_RESET_MS);
  const now = performance.now();
  if (Math.abs(wheelAcc) < WHEEL_THRESHOLD || now - wheelLastFire < WHEEL_COOLDOWN) return;
  wheelLastFire = now;
  const dir = Math.sign(wheelAcc);
  wheelAcc = 0;
  goToPage(state.page + dir);
}

const isEditable = (t) =>
  t instanceof HTMLElement &&
  (t.isContentEditable || /^(INPUT|SELECT|TEXTAREA)$/.test(t.tagName));

function onKeyDown(e) {
  if (e.key === 'Escape') {
    if (tileDnd && tileDnd.active()) {
      cancelTileDrag();
      return;
    }
    if (modalOpen()) {
      closeModal();
      return;
    }
  }
  if (modalOpen() || isEditable(e.target)) return;
  if (e.key === 'ArrowLeft') {
    e.preventDefault();
    goToPage(state.page - 1);
  } else if (e.key === 'ArrowRight') {
    e.preventDefault();
    goToPage(state.page + 1);
  }
}

// ---------- pointer gestures (swipe & tile drag) ----------

// One gesture at a time. mode: null = undecided, 'tile-pending' = press on
// a tile not yet qualified as a drag, 'tile' = running tile drag (dnd.js),
// active = running page swipe.
let drag = null;
let suppressClick = false; // swallow the click that follows a swipe or drag
let pressTimer = 0; // touch/pen hold timer on a tile

function onPointerDown(e) {
  // A second finger arriving mid-gesture is ignored. Pending/inactive
  // presses are simply replaced, which also heals a drag object orphaned
  // by a pointerup the page never saw.
  if (drag && (drag.active || drag.mode === 'tile')) return;
  if (e.pointerType === 'mouse' && e.button !== 0) return;
  cancelPress();
  suppressClick = false; // a new gesture clears any stale suppression
  if (modalOpen()) return;
  const tileEl = e.target.closest('.tile');
  drag = {
    pointerId: e.pointerId,
    startX: e.clientX, startY: e.clientY,
    lastX: e.clientX, lastY: e.clientY,
    startT: performance.now(),
    dx: 0, active: false, mode: null, tileId: 0,
  };
  // Swipe vs tile-drag rule: a press landing on a real tile (not on its ✎
  // button, not the dashed drop slot) is a tile-drag CANDIDATE — it becomes
  // a drag once the pointer moves >6px while held (mouse) or survives a
  // 350ms hold (touch/pen). Any other press stays a page-swipe candidate,
  // and a tile candidate that strays too far before its hold fires also
  // converts to a swipe — so a flick on a tile still flips the page and a
  // tap stays a link click. (A touch hold now drags instead of opening the
  // editor; the always-visible ✎ button covers editing.)
  if (tileEl && tileEl.dataset.id && !e.target.closest('.tile-edit')) {
    drag.mode = 'tile-pending';
    drag.tileId = Number(tileEl.dataset.id);
    if (e.pointerType !== 'mouse') pressTimer = setTimeout(startTileDrag, TILE_HOLD_MS);
  }
}

// startTileDrag promotes a tile-pending press into a real drag. Called by
// the hold timer (touch/pen) or by onPointerMove past the slop (mouse).
function startTileDrag() {
  if (!drag || drag.mode !== 'tile-pending') return;
  const bm = tileDnd && state.bookmarks.find((b) => b.id === drag.tileId);
  if (!bm) {
    drag.mode = null; // can't drag — let the gesture fall back to a swipe
    return;
  }
  cancelPress();
  drag.mode = 'tile';
  suppressClick = true; // the release click must not open the link
  try {
    els.viewport.setPointerCapture(drag.pointerId);
  } catch {
    // Pointer was already released in a lost-up race; keep going — the
    // first move/end event will settle the gesture.
  }
  tileDnd.begin(drag.lastX, drag.lastY, bm);
}

// Esc during a tile drag: end the gesture and revert the list order.
function cancelTileDrag() {
  cancelPress();
  if (drag) {
    try {
      els.viewport.releasePointerCapture(drag.pointerId);
    } catch { /* pointer was never captured */ }
    drag = null;
  }
  suppressClick = true; // eat the click when the held pointer releases
  tileDnd.cancel();
}

function cancelPress() {
  clearTimeout(pressTimer);
}

function onPointerMove(e) {
  if (!drag || e.pointerId !== drag.pointerId) return;
  drag.lastX = e.clientX;
  drag.lastY = e.clientY;
  if (drag.mode === 'tile') {
    tileDnd.move(e.clientX, e.clientY);
    return;
  }
  const dx = e.clientX - drag.startX;
  if (!drag.active) {
    if (drag.mode === 'tile-pending') {
      const dist = Math.hypot(dx, e.clientY - drag.startY);
      if (e.pointerType === 'mouse') {
        if (dist <= TILE_DRAG_SLOP) return;
        startTileDrag();
        return;
      }
      // Touch/pen: wandering too far before the hold fires forfeits the
      // drag — the gesture can still become a page swipe below.
      if (dist > HOLD_SLOP) {
        cancelPress();
        drag.mode = null;
        suppressClick = true; // moved too far to count as a tap
      }
    }
    if (Math.abs(dx) < SWIPE_SLOP) return;
    drag.active = true;
    cancelPress();
    // Capture only past the slop so a simple tap stays a normal click.
    els.viewport.setPointerCapture(drag.pointerId);
  }
  drag.dx = dx;
  offsetTrack(dx, false); // follow the finger
}

function onPointerEnd(e) {
  cancelPress();
  if (!drag || e.pointerId !== drag.pointerId) return;
  const { dx, startT, active, mode } = drag;
  drag = null;
  if (mode === 'tile') {
    suppressClick = true; // a drag is never a link click
    if (tileDnd) {
      if (e.type === 'pointercancel') tileDnd.cancel();
      else tileDnd.drop();
    }
    return;
  }
  if (!active) return;
  suppressClick = true;
  const w = els.viewport.clientWidth || 1;
  const velocity = Math.abs(dx) / Math.max(performance.now() - startT, 1);
  let next = state.page;
  if (Math.abs(dx) > w * SWIPE_FLIP || (velocity > FLICK_VELOCITY && Math.abs(dx) > SWIPE_SLOP)) {
    next += dx < 0 ? 1 : -1;
  }
  goToPage(next); // snaps to next/prev or animates back to the current page
}

function onClickCapture(e) {
  if (!suppressClick) return;
  suppressClick = false;
  e.preventDefault();
  e.stopPropagation();
}

// ---------- modals ----------

let activeModal = null; // the visible .modal-backdrop element
const modalOpen = () => activeModal !== null;

function showModal(backdrop) {
  activeModal = backdrop;
  backdrop.hidden = false;
  backdrop.querySelector('input, select, button')?.focus();
}

function closeModal() {
  if (!activeModal) return;
  activeModal.hidden = true;
  activeModal = null;
}

function showMsg(node, text, isError) {
  node.textContent = text;
  node.classList.toggle('error', Boolean(isError));
  node.hidden = false;
}

const hideMsg = (node) => { node.hidden = true; };

// ---------- toast ----------

let toastTimer = 0;
function showToast(text) {
  els.toast.textContent = text;
  els.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { els.toast.hidden = true; }, 3500);
}

// ---------- bookmark modal ----------

function openBookmarkModal(b) {
  state.editing = b;
  els.modalBookmarkTitle.textContent = b ? '编辑书签' : '添加书签';
  els.bmTitle.value = b ? b.title : '';
  els.bmUrl.value = b ? b.url : '';
  hideMsg(els.bmError);
  els.bmDelete.hidden = !b;
  els.bmRefresh.hidden = !b;
  showModal(els.modalBookmark);
}

async function onBookmarkSubmit(e) {
  e.preventDefault();
  const title = els.bmTitle.value.trim();
  let url = els.bmUrl.value.trim();
  if (url && !/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url)) url = `https://${url}`;
  try {
    if (state.editing) {
      await api.updateBookmark(state.editing.id, title, url);
    } else {
      await api.createBookmark(title, url);
    }
    closeModal();
    await refresh();
    pollIcons();
  } catch (err) {
    showMsg(els.bmError, err.message, true);
  }
}

async function onDeleteClick() {
  const b = state.editing;
  if (!b || !confirm(`删除书签“${b.title}”？`)) return;
  try {
    await api.deleteBookmark(b.id);
    closeModal();
    await refresh();
  } catch (err) {
    showMsg(els.bmError, err.message, true);
  }
}

async function onRefreshIconClick() {
  const b = state.editing;
  if (!b) return;
  try {
    const updated = await api.refreshIcon(b.id);
    const i = state.bookmarks.findIndex((x) => x.id === b.id);
    if (i >= 0) state.bookmarks[i] = updated;
    render();
    pollIcons();
  } catch (err) {
    showMsg(els.bmError, err.message, true);
  }
}

// ---------- settings modal ----------

function openSettingsModal() {
  els.setRows.value = String(state.grid.rows);
  els.setCols.value = String(state.grid.cols);
  els.importFile.value = '';
  hideMsg(els.setMsg);
  showModal(els.modalSettings);
}

async function onGridChange() {
  const grid = { rows: Number(els.setRows.value), cols: Number(els.setCols.value) };
  try {
    const saved = await api.putSettings(grid); // server echoes the stored settings
    state.grid = saved.grid;
    render(); // re-chunk pages under the new grid
  } catch (err) {
    showMsg(els.setMsg, err.message, true);
  }
}

function onExportClick() {
  // The endpoint answers with a Content-Disposition attachment.
  const a = document.createElement('a');
  a.href = '/api/export';
  a.click();
}

async function onImportClick() {
  const file = els.importFile.files[0];
  if (!file) {
    showMsg(els.setMsg, '请选择要导入的 JSON 文件', true);
    return;
  }
  const mode = $('input[name="import-mode"]:checked')?.value || 'append';
  if (mode === 'replace' && !confirm('替换模式将删除所有现有书签，确定继续？')) return;
  let doc;
  try {
    doc = JSON.parse(await file.text());
  } catch {
    showMsg(els.setMsg, '文件不是有效的 JSON', true);
    return;
  }
  try {
    const res = await api.importBookmarks(doc, mode);
    showMsg(els.setMsg, `成功导入 ${res.imported} 个书签`, false);
    await refresh();
    pollIcons();
  } catch (err) {
    showMsg(els.setMsg, err.message, true);
  }
}

// ---------- data ----------

async function refresh() {
  const [settings, bookmarks] = await Promise.all([api.getSettings(), api.listBookmarks()]);
  state.grid = settings.grid;
  state.bookmarks = bookmarks;
  state.loadError = null;
  render();
}

// pollIcons re-fetches bookmarks while icons are still pending so tiles
// pick up fetched favicons without a manual reload.
let pollTimer = 0;
function pollIcons() {
  clearTimeout(pollTimer);
  let tries = 0;
  const tick = async () => {
    tries++;
    // A running tile drag owns the list order; don't clobber it mid-gesture.
    if (tileDnd && tileDnd.active()) {
      if (tries < POLL_TRIES) pollTimer = setTimeout(tick, POLL_INTERVAL);
      return;
    }
    try {
      state.bookmarks = await api.listBookmarks();
      render();
    } catch {
      // Transient poll failure: the next scheduled tick retries.
    }
    if (tries < POLL_TRIES && state.bookmarks.some((b) => b.icon_status === 'pending')) {
      pollTimer = setTimeout(tick, POLL_INTERVAL);
    }
  };
  pollTimer = setTimeout(tick, POLL_INTERVAL);
}

// ---------- tile drag & drop (dnd.js) ----------
//
// dnd.js registers its handlers through this hook so module imports stay
// one-way (dnd.js → app.js); index.html loads both files. Every tileDnd
// call site guards on it being non-null: if dnd.js fails to load, presses
// on tiles just degrade to plain clicks.
let tileDnd = null;
export function registerTileDnd(handlers) { tileDnd = handlers; }

// Task 7 hook: persist a new order as the full id list in display order
// (tiles carry data-id; collect them left-to-right across pages).
export async function persistOrder(ids) {
  state.bookmarks = await api.reorder(ids);
  render();
}

export { state, pageSize, render, goToPage, refresh, showToast };

// ---------- events & boot ----------

function bindEvents() {
  els.navPrev.addEventListener('click', () => goToPage(state.page - 1));
  els.navNext.addEventListener('click', () => goToPage(state.page + 1));
  els.btnAdd.addEventListener('click', () => openBookmarkModal(null));
  els.btnSettings.addEventListener('click', openSettingsModal);

  document.addEventListener('keydown', onKeyDown);
  window.addEventListener('resize', () => {
    layout();          // tiles/gutters track the new viewport box
    offsetTrack(0, false); // re-snap: page width = viewport width changed
  });
  els.viewport.addEventListener('wheel', onWheel, { passive: false });
  els.viewport.addEventListener('pointerdown', onPointerDown);
  els.viewport.addEventListener('pointermove', onPointerMove);
  els.viewport.addEventListener('pointerup', onPointerEnd);
  els.viewport.addEventListener('pointercancel', onPointerEnd);
  // Clicks are captured at document level: a drop can end over the arrows
  // or dots, which live outside the viewport element.
  document.addEventListener('click', onClickCapture, true);

  els.formBookmark.addEventListener('submit', onBookmarkSubmit);
  els.bmCancel.addEventListener('click', closeModal);
  els.bmDelete.addEventListener('click', onDeleteClick);
  els.bmRefresh.addEventListener('click', onRefreshIconClick);

  els.setRows.addEventListener('change', onGridChange);
  els.setCols.addEventListener('change', onGridChange);
  els.btnExport.addEventListener('click', onExportClick);
  els.btnImport.addEventListener('click', onImportClick);
  els.setClose.addEventListener('click', closeModal);

  for (const bd of document.querySelectorAll('.modal-backdrop')) {
    bd.addEventListener('click', (e) => {
      if (e.target === bd) closeModal(); // backdrop click, not dialog content
    });
  }
}

async function init() {
  // Cache elements: id "nav-prev" → els.navPrev.
  for (const id of [
    'track', 'viewport', 'dots', 'nav-prev', 'nav-next', 'btn-add', 'btn-settings',
    'modal-bookmark', 'modal-bookmark-title', 'form-bookmark', 'bm-title', 'bm-url',
    'bm-error', 'bm-delete', 'bm-refresh', 'bm-cancel',
    'modal-settings', 'set-rows', 'set-cols', 'set-msg',
    'btn-export', 'btn-import', 'import-file', 'set-close', 'toast',
  ]) {
    els[id.replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = document.getElementById(id);
  }
  bindEvents();
  restorePage();
  try {
    await refresh();
  } catch (err) {
    state.loadError = err.message;
    render();
  }
}

init();
