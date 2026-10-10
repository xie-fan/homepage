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
const LONG_PRESS_MS = 550; // touch hold that opens the edit modal

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
const clampPage = () => {
  state.page = Math.min(Math.max(state.page, 0), pageCount() - 1);
};

// ---------- rendering ----------

function render() {
  clampPage();
  sessionStorage.setItem(PAGE_KEY, String(state.page));
  renderTrack();
  renderDots();
  updateArrows();
  // Do not yank the track mid-swipe; the drag handler owns it then.
  if (!drag || !drag.active) offsetTrack(0, false);
}

function renderTrack() {
  els.track.replaceChildren();
  const size = pageSize();
  for (let p = 0; p < pageCount(); p++) {
    const page = el('div', 'page');
    // Fixed-px tracks keep tiles a constant size whatever the grid is.
    page.style.gridTemplateColumns = `repeat(${state.grid.cols}, var(--tile-w))`;
    page.style.gridTemplateRows = `repeat(${state.grid.rows}, var(--tile-h))`;
    const items = state.bookmarks.slice(p * size, (p + 1) * size);
    if (items.length === 0) {
      page.append(el('p', 'empty',
        state.loadError ? `加载失败：${state.loadError}` : '还没有书签，点击 + 添加'));
    } else {
      for (const b of items) page.append(makeTile(b));
    }
    els.track.append(page);
  }
}

function makeTile(b) {
  const tile = el('div', 'tile');
  tile.dataset.id = String(b.id); // Task 7 (drag & drop) hooks onto data-id

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
  for (let i = 0; i < pageCount(); i++) {
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
  els.navNext.disabled = state.page >= pageCount() - 1;
}

// ---------- navigation ----------

function goToPage(i) {
  state.page = Math.min(Math.max(i, 0), pageCount() - 1);
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
  if (modalOpen() || (drag && drag.active)) return;
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
  if (e.key === 'Escape' && modalOpen()) {
    closeModal();
    return;
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

// ---------- pointer swipe ----------

let drag = null; // {pointerId, startX, startT, dx, active}
let suppressClick = false; // swallow the click that follows a swipe
let pressTimer = 0; // long-press timer (touch → edit modal)

function onPointerDown(e) {
  cancelPress();
  suppressClick = false; // a new gesture clears any stale suppression
  if (modalOpen()) return;
  if (e.pointerType === 'mouse' && e.button !== 0) return;
  drag = { pointerId: e.pointerId, startX: e.clientX, startT: performance.now(), dx: 0, active: false };
  // Long-press on a tile opens its edit modal (touch only; mouse uses ✎/right-click).
  const tile = e.target.closest('.tile');
  if (tile && e.pointerType === 'touch') {
    pressTimer = setTimeout(() => {
      suppressClick = true; // eat the click after the finger lifts
      const b = state.bookmarks.find((x) => x.id === Number(tile.dataset.id));
      if (b) openBookmarkModal(b);
    }, LONG_PRESS_MS);
  }
}

function cancelPress() {
  clearTimeout(pressTimer);
}

function onPointerMove(e) {
  if (!drag || e.pointerId !== drag.pointerId) return;
  const dx = e.clientX - drag.startX;
  if (!drag.active) {
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
  const { dx, startT, active } = drag;
  drag = null;
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

function onViewportClick(e) {
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

// Task 7 hook: persist a new order as the full id list in display order
// (tiles carry data-id; collect them left-to-right across pages).
export async function persistOrder(ids) {
  state.bookmarks = await api.reorder(ids);
  render();
}

// ---------- events & boot ----------

function bindEvents() {
  els.navPrev.addEventListener('click', () => goToPage(state.page - 1));
  els.navNext.addEventListener('click', () => goToPage(state.page + 1));
  els.btnAdd.addEventListener('click', () => openBookmarkModal(null));
  els.btnSettings.addEventListener('click', openSettingsModal);

  document.addEventListener('keydown', onKeyDown);
  window.addEventListener('resize', () => offsetTrack(0, false));
  els.viewport.addEventListener('wheel', onWheel, { passive: false });
  els.viewport.addEventListener('pointerdown', onPointerDown);
  els.viewport.addEventListener('pointermove', onPointerMove);
  els.viewport.addEventListener('pointerup', onPointerEnd);
  els.viewport.addEventListener('pointercancel', onPointerEnd);
  els.viewport.addEventListener('click', onViewportClick, true);

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
    'btn-export', 'btn-import', 'import-file', 'set-close',
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
