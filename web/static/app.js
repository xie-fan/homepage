// app.js — homepage UI: state, paged grid rendering, navigation, modals.
// All DOM is built with createElement/textContent (never innerHTML) so
// bookmark titles and URLs cannot inject markup.

import { api } from './api.js';

// ---------- state ----------

const state = {
  bookmarks: [],
  grid: { rows: 4, cols: 6 },
  spacing: 'normal', // one of SPACING_VALUES; drives the layout preset
  page: 0,
  editing: null, // bookmark in the modal; null while adding
  loadError: null,
};

// spacingOf validates a settings.spacing value from the server (or an
// import file); anything unknown or absent falls back to "normal".
const SPACING_VALUES = ['compact', 'normal', 'relaxed'];
const spacingOf = (v) => (SPACING_VALUES.includes(v) ? v : 'normal');

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

// Icon polling: a favicon fetch can take ~24s, so re-fetch bookmarks at
// 1.5s for a while, back off to 3s, and give up after ~40s total.
const POLL_INTERVAL = 1500; // initial ms between bookmark re-fetches
const POLL_INTERVAL_SLOW = 3000; // backed-off interval
const POLL_SLOW_AFTER = 15000; // switch to the slow interval past this
const POLL_BUDGET = 40000; // stop polling pending icons after ~40s

// API error codes → Chinese messages shown in dialogs and toasts.
const ERROR_MESSAGES = {
  url_required: '请输入网址',
  url_invalid: '网址格式不正确',
  url_scheme: '只支持 http/https 网址',
  url_too_long: '网址太长（最多 2048 个字符）',
  url_userinfo: '网址中不能包含用户名或密码',
  title_too_long: '名称太长（最多 200 个字符）',
  duplicate_url: '该网址已存在',
  invalid_grid: '网格设置无效',
  invalid_spacing: '间距设置无效',
  import_invalid: '导入文件格式不正确',
  import_too_large: '导入的书签太多（最多 5000 个）',
  not_found: '书签不存在，可能已被删除',
  invalid_order: '排序失败，请刷新后重试',
  internal: '服务器出错了，请稍后重试',
  network: '网络错误，请检查连接',
};

// friendlyError maps an api.js error (err.code) to a Chinese message.
function friendlyError(err) {
  return ERROR_MESSAGES[err && err.code] || '操作失败';
}

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
// The grid keeps the configured rows/cols on every viewport. Each
// spacing preset (state.spacing) contributes: frac — the share of the
// viewport WIDTH the grid aims for on large screens (below 1024px it
// eases toward a near-full-width grid so phones still use their
// width); gapShare — the share of that target spent on the (cols−1)
// column gaps; gapMin/gapMax — the gap range; tileMax — the tile width
// cap; rowMul — row-gap multiplier (relaxed adds extra air between
// rows). layout() measures the real .viewport box and writes resolved
// metrics (--tile-w/--tile-h/--grid-gap-x/--grid-gap-y/--page-pad-*) as
// px on :root — style.css derives icon/label/edit sizes from --tile-w
// via clamp() (--icon: 36px…0.62·w…72px, label ~13–14px on desktop) and
// dnd.js parses --tile-* back into numbers. The formula (also in the
// :root comment in style.css):
//
//   availW = vw − 2·padX      availH = vh − 2·padY
//   F      = min(availW, lerp(0.94 → preset.frac) · vw)  target width
//   gapX   = clamp(gapMin, F·gapShare/(cols−1), gapMax)
//   gapY   = gapX · rowMul
//   tileW  = min(tileMax, (F − (cols−1)·gapX)/cols,
//                        (availW − (cols−1)·gapX)/cols, height bound)
//   tileH  = clamp(aspect·tileW, icon + label block, rowFit)
//
// Gaps shrink to gapMin before tiles give way ("gaps first, icons
// second"), so every grid 3×4…5×6 fits without clipping or scrolling.
// padX reserves 64px a side while the ≥768px arrows overlay the grid so
// a tile can never slide under an arrow; padY = 12px.
const TILE_ASPECT = 0.92; // tileH/tileW while icon + label fit inside
const TILE_LABEL = 31;    // link pad-top + gap + one label line (~2+10+19)
const ICON_MIN = 36;      // mirrors the --icon clamp in style.css
const ICON_MAX = 72;
const ICON_RATIO = 0.62;
// Gaps are ~10% airier than the original presets at every viewport
// (gapMin/gapMax ×1.1 too). frac/gapShare are calibrated so the
// reference layout — 1280×800, 4×6, overlay gutters (padX 64) — keeps
// the tile widths it had before the change while both gaps land ×1.1:
// F = cols·tileW + (cols−1)·gapX, gapShare chosen so F·gapShare/(cols−1)
// is exactly 1.1× the old gap.
//             compact  normal  relaxed
//   gridW       557px   805px   990px
//   gapX/gapY   19/19   35/35   48/62px
//   tileW        77px   105px   125px
const SPACING = {
  //            frac  gapShare  gapMin  gapMax  tileMax  rowMul
  compact:  { frac: 0.4366, gapShare: 0.1679, gapMin: 11,   gapMax: 26.4, tileMax: 96,  rowMul: 1.0 },
  normal:   { frac: 0.6325, gapShare: 0.2157, gapMin: 13.2, gapMax: 44,   tileMax: 106, rowMul: 1.0 },
  relaxed:  { frac: 0.7772, gapShare: 0.2431, gapMin: 15.4, gapMax: 57.2, tileMax: 126, rowMul: 1.3 },
};
const iconFor = (t) => Math.min(ICON_MAX, Math.max(ICON_MIN, t * ICON_RATIO));
// Least tileH for a tile of width t: the aspect box, or the icon with
// one label line under it — whichever is taller.
const minTileH = (t) => Math.max(t * TILE_ASPECT, iconFor(t) + TILE_LABEL);

function layout() {
  const { rows, cols } = state.grid;
  const p = SPACING[state.spacing] || SPACING.normal;
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
  const availW = vw - 2 * padX;
  const availH = vh - 2 * padY;
  // Target grid width: the preset share of the viewport on large
  // screens, easing to near-full width below 1024px (t: 0 at ≤480px).
  const t = Math.min(1, Math.max(0, (vw - 480) / (1024 - 480)));
  const targetW = Math.min(availW, (p.frac * t + 0.94 * (1 - t)) * vw);
  const spans = Math.max(cols - 1, 1);
  const gapX = Math.round(Math.min(p.gapMax,
    Math.max(p.gapMin, (targetW * p.gapShare) / spans)));
  const gapY = Math.round(gapX * p.rowMul);
  const rowFit = Math.floor((availH - (rows - 1) * gapY) / rows);
  // Height-bound tile width: the aspect bound, tightened until the
  // icon+label block also fits the row allotment (once the icon reaches
  // its 72px cap the aspect bound alone is enough; a row too small for
  // even the 36px icon floor is pathological — the tileH cap below
  // keeps the grid inside the viewport regardless).
  let tileFitH = Math.floor(rowFit / TILE_ASPECT);
  const iconRoom = rowFit - TILE_LABEL;
  if (iconRoom >= ICON_MIN && iconRoom < ICON_MAX) {
    tileFitH = Math.min(tileFitH, Math.floor(iconRoom / ICON_RATIO));
  }
  const tile = Math.max(1, Math.floor(Math.min(
    p.tileMax,
    (targetW - (cols - 1) * gapX) / cols,
    (availW - (cols - 1) * gapX) / cols,
    tileFitH,
  )));
  const tileH = Math.max(1, Math.min(rowFit, Math.ceil(minTileH(tile))));
  const root = document.documentElement.style;
  root.setProperty('--tile-w', `${tile}px`);
  root.setProperty('--tile-h', `${tileH}px`);
  root.setProperty('--grid-gap-x', `${gapX}px`);
  root.setProperty('--grid-gap-y', `${gapY}px`);
  root.setProperty('--page-pad-x', `${padX}px`);
  root.setProperty('--page-pad-y', `${padY}px`);
}

// ---------- rendering ----------

function render() {
  clampPage();
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

// iconURL builds the icon URL with a cache-buster that changes whenever
// the icon file or the bookmark changes, so a re-render after an icon
// fetch never serves the stale (or previously failed) response.
function iconURL(b) {
  const v = `${b.icon_path}|${b.updated_at}`;
  return `/api/icons/${b.id}?v=${encodeURIComponent(v)}`;
}

// Icons are never painted until known-good: the tile renders the letter
// avatar first and the <img> replaces it only after its load event fired
// with a decoded (naturalWidth > 0) image — a 404 or broken file can
// therefore never flash the browser's broken-image placeholder. A failed
// load retries once after ~1.5s with a fresh cache-buster (the fetch
// worker may still be writing the file or the cache may hold a stale
// error), then is remembered in iconFailures keyed by bookmark
// id + icon_path so re-renders and icon polling neither restart the
// retry loop nor reflash a broken tile. The stored value is the
// bookmark's updated_at: an edit or icon refresh bumps it and earns the
// icon a fresh attempt.
const iconFailures = new Map(); // `${id}|${icon_path}` → updated_at that failed
const ICON_RETRY_MS = 1500;

function makeIcon(b) {
  const box = el('div', 'tile-icon');
  box.append(avatar(b)); // fallback first: a broken img is never visible
  if (b.icon_status !== 'ok') return box;
  const key = `${b.id}|${b.icon_path}`;
  if (iconFailures.get(key) === b.updated_at) return box;
  iconFailures.delete(key); // new icon data — allow another attempt
  const img = new Image();
  img.alt = '';
  img.draggable = false;
  let retried = false;
  const fail = () => {
    if (!retried) {
      retried = true;
      setTimeout(() => { img.src = `${iconURL(b)}&r=1`; }, ICON_RETRY_MS);
    } else {
      iconFailures.set(key, b.updated_at);
    }
  };
  const show = () => {
    // A "loaded" response that decoded to nothing counts as broken.
    if (img.naturalWidth <= 0) {
      fail();
      return;
    }
    const swap = () => box.replaceChildren(img);
    // decode() waits out a partially decoded frame; where it is missing
    // or rejects (e.g. some SVGs) the load event is proof enough.
    if (img.decode) img.decode().then(swap, swap); else swap();
  };
  img.addEventListener('load', show);
  img.addEventListener('error', fail);
  img.src = iconURL(b);
  // Memory-cache hit: the image can be complete the moment src is set —
  // swap in sync and skip the avatar frame entirely.
  if (img.complete && img.naturalWidth > 0) show();
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

// Resize storms (window drags, mobile URL-bar churn) are coalesced to one
// layout pass per frame.
let resizeRaf = 0;
function onResize() {
  if (resizeRaf) return;
  resizeRaf = requestAnimationFrame(() => {
    resizeRaf = 0;
    layout(); // tiles/gutters track the new viewport box
    // Re-snap right away: the page width changed with the viewport, so
    // re-apply the transform for the current page with the transition
    // OFF and force a reflow — the grid recenters without animating —
    // then restore the transition for later page moves.
    offsetTrack(0, false);
    void els.track.offsetWidth; // commit the snap before re-enabling
    els.track.classList.remove('no-anim');
  });
}

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
let pressTimer = 0; // touch/pen hold timer on a tile

// Post-gesture click suppression. A swipe or tile drag is followed by the
// gesture's own release click, which must not activate whatever lies under
// the pointer. This used to be a plain flag that only the next click
// cleared — so when the browser never delivered that release click
// (pointer released off-window, capture retargeting, a second finger), it
// went stale and ate an unrelated later click: the first ⚙ press after a
// drag could silently do nothing. Now a click is eaten only when it lands
// inside the viewport within SUPPRESS_CLICK_MS of the gesture's pointerup
// — i.e. only the release click of that same gesture; header buttons and
// modal dialogs can never be swallowed.
const SUPPRESS_CLICK_MS = 350;
let suppressClickUntil = 0; // performance.now() deadline; 0 = disarmed
// Pointer id of a tile drag cancelled while still held (Esc): its eventual
// release click still belongs to that gesture, so its pointerup arms
// suppression too (see onPointerEnd).
let suppressReleaseId = -1;

function armClickSuppress() {
  suppressClickUntil = performance.now() + SUPPRESS_CLICK_MS;
}

function onPointerDown(e) {
  // A second finger arriving mid-gesture is ignored. Pending/inactive
  // presses are simply replaced, which also heals a drag object orphaned
  // by a pointerup the page never saw.
  if (drag && (drag.active || drag.mode === 'tile')) return;
  if (e.pointerType === 'mouse' && e.button !== 0) return;
  cancelPress();
  // A new press retires whatever the last gesture left armed: the click
  // that follows belongs to THIS gesture, never to the old one.
  suppressClickUntil = 0;
  suppressReleaseId = -1;
  if (modalOpen()) return;
  const tileEl = e.target.closest('.tile');
  drag = {
    pointerId: e.pointerId,
    startX: e.clientX, startY: e.clientY,
    lastX: e.clientX, lastY: e.clientY,
    startT: performance.now(),
    dx: 0, active: false, mode: null, tileId: 0, eatClick: false,
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
    const pointerId = drag.pointerId;
    // The pointer is still held: remember it so the release click it
    // eventually produces is eaten as this gesture's own (onPointerEnd).
    // drag is nulled BEFORE releasing capture because lostpointercapture
    // may fire synchronously and must not see a live gesture.
    suppressReleaseId = pointerId;
    drag = null;
    releaseCapture(pointerId);
  }
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
        drag.eatClick = true; // moved too far to count as a tap
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
  if (e.pointerId === suppressReleaseId) {
    suppressReleaseId = -1;
    // The held pointer of an Esc-cancelled drag just released: the click
    // it produces is still that gesture's — arm suppression now.
    armClickSuppress();
  }
  if (!drag || e.pointerId !== drag.pointerId) return;
  const { dx, startT, active, mode, eatClick } = drag;
  drag = null;
  releaseCapture(e.pointerId);
  if (mode === 'tile') {
    armClickSuppress(); // a drag is never a link click
    if (tileDnd) {
      if (e.type === 'pointercancel') tileDnd.cancel();
      else tileDnd.drop();
    }
    return;
  }
  if (!active) {
    if (eatClick) armClickSuppress();
    return;
  }
  armClickSuppress();
  const w = els.viewport.clientWidth || 1;
  const velocity = Math.abs(dx) / Math.max(performance.now() - startT, 1);
  let next = state.page;
  if (Math.abs(dx) > w * SWIPE_FLIP || (velocity > FLICK_VELOCITY && Math.abs(dx) > SWIPE_SLOP)) {
    next += dx < 0 ? 1 : -1;
  }
  goToPage(next); // snaps to next/prev or animates back to the current page
}

// releaseCapture frees the viewport's pointer capture if it still holds
// one — capture must never outlive the gesture that set it.
function releaseCapture(pointerId) {
  try {
    if (els.viewport.hasPointerCapture(pointerId)) {
      els.viewport.releasePointerCapture(pointerId);
    }
  } catch {
    // The pointer is already gone (lost-up race); nothing to release.
  }
}

function onLostPointerCapture(e) {
  // The implicit release right after every pointerup also arrives here —
  // drag is already null then, so this is a no-op in the normal flow. It
  // only bites when the browser takes the capture back mid-gesture.
  if (drag && e.pointerId === drag.pointerId) abortGesture();
}

// abortGesture tears down a gesture that can no longer complete: the
// capture was lost without a pointerup, or the window blurred mid-press.
// The half-dragged track snaps back to the current page (never a flip
// without a real release), and a release click — if one still arrives —
// is eaten like any drag end.
function abortGesture() {
  cancelPress();
  if (!drag) return;
  const { pointerId, mode, active, eatClick } = drag;
  drag = null;
  releaseCapture(pointerId);
  if (mode === 'tile' && tileDnd) tileDnd.cancel();
  else if (active) goToPage(state.page); // re-center the dragged track
  if (mode === 'tile' || active || eatClick) armClickSuppress();
}

function onClickCapture(e) {
  if (performance.now() > suppressClickUntil) return; // disarmed or expired
  suppressClickUntil = 0;
  // Only the gesture's own release click is eaten: it lands inside the
  // viewport (pointer capture retargets it there) right after pointerup.
  // Clicks on header buttons, modal dialogs or the pager pass through.
  if (!els.viewport.contains(e.target)) return;
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

// Set when the server answered 409 duplicate_url: the next submit resends
// the same values with allow_duplicate, and the primary button says so.
let allowDuplicate = false;

// resetDuplicate clears the "add anyway" state — on open and whenever a
// field is edited, the dialog goes back to a normal save.
function resetDuplicate() {
  allowDuplicate = false;
  els.bmSubmit.textContent = '保存';
}

function openBookmarkModal(b) {
  state.editing = b;
  els.modalBookmarkTitle.textContent = b ? '编辑书签' : '添加书签';
  els.bmTitle.value = b ? b.title : '';
  els.bmUrl.value = b ? b.url : '';
  hideMsg(els.bmError);
  resetDuplicate();
  els.bmDelete.hidden = !b;
  els.bmRefresh.hidden = !b;
  showModal(els.modalBookmark);
}

async function onBookmarkSubmit(e) {
  e.preventDefault();
  const title = els.bmTitle.value.trim();
  let url = els.bmUrl.value.trim();
  if (!url) {
    // The form uses novalidate: requiredness is enforced here so the hint
    // shows inline instead of the browser's native tooltip.
    showMsg(els.bmError, '请输入网址', true);
    els.bmUrl.focus();
    return;
  }
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(url)) url = `https://${url}`;
  let created = null;
  try {
    if (state.editing) {
      await api.updateBookmark(state.editing.id, title, url, allowDuplicate);
    } else {
      created = await api.createBookmark(title, url, allowDuplicate);
    }
    closeModal();
    await refresh();
    pollIcons();
    if (created) revealNewTile(created.id);
  } catch (err) {
    if (err.code === 'duplicate_url') {
      // Offer the explicit override: relabel the primary button and let
      // the next submit resend with allow_duplicate. Editing a field
      // resets this (see the input listeners in bindEvents).
      allowDuplicate = true;
      els.bmSubmit.textContent = state.editing ? '仍然保存' : '仍然添加';
      const existing = state.bookmarks.find(
        (x) => x.id === (err.data && err.data.existing_id));
      showMsg(els.bmError,
        existing ? `该网址已存在（${existing.title}）` : '该网址已存在', true);
      return;
    }
    showMsg(els.bmError, friendlyError(err), true);
  }
}

// revealNewTile flips to the page a just-added bookmark landed on (the
// server appends it at the tail of the list) and pulses the tile briefly
// so the eye finds it. The .tile-new ring is CSS (~1.5s); under
// prefers-reduced-motion it shows as a static outline instead.
function revealNewTile(id) {
  const index = state.bookmarks.findIndex((b) => b.id === id);
  if (index < 0) return;
  goToPage(Math.floor(index / pageSize()));
  const tile = els.track.querySelector(`.tile[data-id="${id}"]`);
  if (!tile) return;
  tile.classList.add('tile-new');
  // Timed removal, not animationend: with animation:none the event never
  // fires and the class would stay stuck on.
  setTimeout(() => tile.classList.remove('tile-new'), 1600);
}

async function onDeleteClick() {
  const b = state.editing;
  if (!b || !confirm(`删除书签“${b.title}”？`)) return;
  try {
    await api.deleteBookmark(b.id);
    closeModal();
    await refresh();
  } catch (err) {
    showMsg(els.bmError, friendlyError(err), true);
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
    showMsg(els.bmError, friendlyError(err), true);
  }
}

// ---------- settings modal ----------

// applySettings stores a settings document echoed by the server (PUT,
// import, or the GET in refresh); spacing is validated defensively so a
// server without the field — or an odd value — falls back to "normal".
function applySettings(saved) {
  if (saved && saved.grid) state.grid = saved.grid;
  state.spacing = spacingOf(saved && saved.spacing);
}

function openSettingsModal() {
  els.setRows.value = String(state.grid.rows);
  els.setCols.value = String(state.grid.cols);
  els.setSpacing.value = state.spacing;
  els.importFile.value = '';
  hideMsg(els.setMsg);
  showModal(els.modalSettings);
}

async function onGridChange() {
  const grid = { rows: Number(els.setRows.value), cols: Number(els.setCols.value) };
  try {
    applySettings(await api.putSettings({ grid })); // server echoes stored settings
    render(); // re-chunk pages under the new grid
  } catch (err) {
    showMsg(els.setMsg, friendlyError(err), true);
  }
}

// onSpacingChange saves just the spacing preset; the re-layout is
// immediate since spacing only changes tile/gap metrics.
async function onSpacingChange() {
  try {
    applySettings(await api.putSettings({ spacing: els.setSpacing.value }));
    render();
  } catch (err) {
    els.setSpacing.value = state.spacing; // restore the stored value
    showMsg(els.setMsg, friendlyError(err), true);
  }
}

function onExportClick() {
  // The endpoint answers with a Content-Disposition attachment.
  const a = document.createElement('a');
  a.href = '/api/export';
  a.click();
}

// importDocError validates the import file BEFORE the replace confirm and
// before any request is sent. Accepted shapes: the export object
// ({"bookmarks": [...], "settings"?: {...}}) or a bare array. Every item
// needs a non-empty string url; an optional settings.grid must hold
// integer rows 3–5 and cols 4–6. Returns a short reason, or null.
function importDocError(doc) {
  let items = doc;
  if (!Array.isArray(items)) {
    if (!doc || typeof doc !== 'object') return '不是书签导出文件';
    if (!Array.isArray(doc.bookmarks)) return '缺少 bookmarks 数组';
    const grid = doc.settings ? doc.settings.grid : undefined;
    if (grid !== undefined && grid !== null) {
      if (typeof grid !== 'object' ||
          !Number.isInteger(grid.rows) || grid.rows < 3 || grid.rows > 5 ||
          !Number.isInteger(grid.cols) || grid.cols < 4 || grid.cols > 6) {
        return 'settings.grid 无效（行须为 3–5，列须为 4–6）';
      }
    }
    const spacing = doc.settings ? doc.settings.spacing : undefined;
    if (spacing !== undefined && spacing !== null &&
        !SPACING_VALUES.includes(spacing)) {
      return 'settings.spacing 无效（须为 compact / normal / relaxed）';
    }
    items = doc.bookmarks;
  }
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (!it || typeof it !== 'object' || typeof it.url !== 'string' || !it.url.trim()) {
      return `第 ${i + 1} 个书签缺少有效网址`;
    }
  }
  return null;
}

async function onImportClick() {
  const file = els.importFile.files[0];
  if (!file) {
    showMsg(els.setMsg, '请选择要导入的 JSON 文件', true);
    return;
  }
  const mode = $('input[name="import-mode"]:checked')?.value || 'append';
  let doc;
  try {
    doc = JSON.parse(await file.text());
  } catch {
    showMsg(els.setMsg, '导入文件格式不正确：不是有效的 JSON', true);
    return;
  }
  const invalid = importDocError(doc);
  if (invalid) {
    // Rejected before the replace confirm and before any request.
    showMsg(els.setMsg, `导入文件格式不正确：${invalid}`, true);
    return;
  }
  if (mode === 'replace' && !confirm('替换模式将删除所有现有书签，确定继续？')) return;
  try {
    const res = await api.importBookmarks(doc, mode);
    hideMsg(els.setMsg);
    showToast(mode === 'replace'
      ? `已替换为 ${res.imported} 个书签`
      : `已导入 ${res.imported} 个，跳过 ${res.skipped ?? 0} 个重复`);
    await refresh(); // reloads settings too: replace may import grid+spacing
    // Reflect freshly applied settings in the still-open dialog.
    els.setRows.value = String(state.grid.rows);
    els.setCols.value = String(state.grid.cols);
    els.setSpacing.value = state.spacing;
    pollIcons();
  } catch (err) {
    showMsg(els.setMsg, friendlyError(err), true);
  }
}

// ---------- data ----------

async function refresh() {
  const [settings, bookmarks] = await Promise.all([api.getSettings(), api.listBookmarks()]);
  applySettings(settings);
  state.bookmarks = bookmarks;
  state.loadError = null;
  render();
}

// pollIcons re-fetches bookmarks while icons are still pending so tiles
// pick up fetched favicons without a manual reload. Restarted after
// add/edit/refresh/import; a fetch can take ~24s, so the budget is ~40s:
// 1.5s ticks early, backing off to 3s once the wait gets long.
let pollTimer = 0;
let pollStart = 0;
function pollIcons() {
  clearTimeout(pollTimer);
  pollStart = Date.now();
  pollTimer = setTimeout(pollTick, POLL_INTERVAL);
}

function schedulePoll() {
  const elapsed = Date.now() - pollStart;
  if (elapsed >= POLL_BUDGET) return;
  const delay = elapsed < POLL_SLOW_AFTER ? POLL_INTERVAL : POLL_INTERVAL_SLOW;
  pollTimer = setTimeout(pollTick, Math.min(delay, pollStart + POLL_BUDGET - Date.now()));
}

async function pollTick() {
  // A running tile drag owns the list order; don't clobber it mid-gesture.
  if (tileDnd && tileDnd.active()) {
    schedulePoll();
    return;
  }
  try {
    state.bookmarks = await api.listBookmarks();
    render();
  } catch {
    // Transient poll failure: the next scheduled tick retries.
  }
  if (state.bookmarks.some((b) => b.icon_status === 'pending')) schedulePoll();
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

export { state, pageSize, render, goToPage, refresh, showToast, friendlyError };

// ---------- events & boot ----------

function bindEvents() {
  els.navPrev.addEventListener('click', () => goToPage(state.page - 1));
  els.navNext.addEventListener('click', () => goToPage(state.page + 1));
  els.btnAdd.addEventListener('click', () => openBookmarkModal(null));
  els.btnSettings.addEventListener('click', openSettingsModal);

  document.addEventListener('keydown', onKeyDown);
  window.addEventListener('resize', onResize);
  els.viewport.addEventListener('wheel', onWheel, { passive: false });
  els.viewport.addEventListener('pointerdown', onPointerDown);
  els.viewport.addEventListener('pointermove', onPointerMove);
  els.viewport.addEventListener('pointerup', onPointerEnd);
  els.viewport.addEventListener('pointercancel', onPointerEnd);
  // Gestures that lose their capture (or the window's focus) mid-press
  // never see a pointerup — these keep drag state and pointer capture
  // from outliving the gesture.
  els.viewport.addEventListener('lostpointercapture', onLostPointerCapture);
  window.addEventListener('blur', abortGesture);
  // Release clicks are retargeted to the viewport while it holds pointer
  // capture; document-level capture catches them wherever they dispatch.
  document.addEventListener('click', onClickCapture, true);

  els.formBookmark.addEventListener('submit', onBookmarkSubmit);
  // Editing either field drops the "add anyway" state (and its button
  // label) so a stale duplicate answer can't be submitted by accident.
  els.bmTitle.addEventListener('input', resetDuplicate);
  els.bmUrl.addEventListener('input', resetDuplicate);
  els.bmCancel.addEventListener('click', closeModal);
  els.bmDelete.addEventListener('click', onDeleteClick);
  els.bmRefresh.addEventListener('click', onRefreshIconClick);

  els.setRows.addEventListener('change', onGridChange);
  els.setCols.addEventListener('change', onGridChange);
  els.setSpacing.addEventListener('change', onSpacingChange);
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
    'bm-error', 'bm-delete', 'bm-refresh', 'bm-cancel', 'bm-submit',
    'modal-settings', 'set-rows', 'set-cols', 'set-spacing', 'set-msg',
    'btn-export', 'btn-import', 'import-file', 'set-close', 'toast',
  ]) {
    els[id.replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = document.getElementById(id);
  }
  bindEvents();
  try {
    await refresh();
    // Icons can already be pending at load (fresh import, previous
    // session closed mid-fetch): start polling right away.
    if (state.bookmarks.some((b) => b.icon_status === 'pending')) pollIcons();
  } catch (err) {
    state.loadError = friendlyError(err);
    render();
  }
}

init();
