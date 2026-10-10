// search.js — header search: type-to-search, IME handoff, filtered grid,
// keyboard selection.
//
// app.js owns the filter state (state.query, visibleBookmarks(), setQuery):
// while a query is non-empty the whole paging machinery — grid size, dots,
// arrows, wheel, swipe, ←/→ — operates on the matching subset. This module
// owns the input element, the flat selection index and the aria-live count
// announcement. The selection marker reaches tiles through the registered
// hooks (imports stay one-way: search.js → app.js, same pattern as dnd.js).

import {
  state, pageSize, goToPage, modalOpen, isEditable, isFiltering,
  visibleBookmarks, setQuery, isTileDragActive, registerSearch,
} from './app.js';

const input = document.getElementById('search-input');
const status = document.getElementById('search-status');
const track = document.getElementById('track');

// Flat index into visibleBookmarks(); -1 = no selection (empty query or
// no results).
let selIndex = -1;

const selectedId = () => {
  const list = visibleBookmarks();
  return selIndex >= 0 && selIndex < list.length ? list[selIndex].id : null;
};

// makeTile marks the selected tile as it is (re)created; afterRender keeps
// the selection valid when the list changes under it (icon poll, edit,
// import) without waiting for a keypress.
registerSearch({ selectedId, afterRender });

function afterRender() {
  syncSelection();
  applySelectionDom();
}

// Clamp selIndex into the current result list and flip to the page that
// holds it (selection is flat, so moving past a page edge lands on the
// neighbouring page).
function syncSelection() {
  const n = visibleBookmarks().length;
  if (!isFiltering() || n === 0) {
    selIndex = -1;
    return;
  }
  selIndex = Math.min(Math.max(selIndex, 0), n - 1);
  const page = Math.floor(selIndex / pageSize());
  if (page !== state.page) goToPage(page);
}

// Re-apply .tile-selected/aria markers to the current page's DOM. Tiles
// are recreated on every render, so marking has to be re-done each time.
function applySelectionDom() {
  for (const t of track.querySelectorAll('.tile-selected')) {
    t.classList.remove('tile-selected');
    t.removeAttribute('aria-selected');
    t.removeAttribute('aria-current');
  }
  const id = selectedId();
  if (id == null) {
    input.removeAttribute('aria-activedescendant');
    return;
  }
  input.setAttribute('aria-activedescendant', `tile-${id}`);
  const page = track.children[state.page];
  const tile = page && page.querySelector(`.tile[data-id="${id}"]`);
  if (tile) {
    tile.classList.add('tile-selected');
    tile.setAttribute('aria-selected', 'true');
    tile.setAttribute('aria-current', 'true');
  }
}

function move(d) {
  const n = visibleBookmarks().length;
  if (!n) return;
  const next = Math.min(Math.max(selIndex + d, 0), n - 1);
  if (next === selIndex) return;
  selIndex = next;
  syncSelection();
  applySelectionDom();
}

function openSelected(e) {
  const b = visibleBookmarks()[selIndex < 0 ? 0 : selIndex];
  if (!b) return;
  e.preventDefault();
  if (e.ctrlKey || e.metaKey) window.open(b.url, '_blank', 'noopener');
  else location.assign(b.url);
}

function announce() {
  const n = isFiltering() ? visibleBookmarks().length : -1;
  status.textContent = n < 0 ? '' : n > 0 ? `找到 ${n} 个书签` : '没有匹配的书签';
}

// applyQuery pushes the input's text into app.js state (which re-renders
// the grid), then resets the selection to the first result.
function applyQuery() {
  setQuery(input.value);
  const n = visibleBookmarks().length;
  selIndex = isFiltering() && n > 0 ? 0 : -1;
  announce();
  applySelectionDom();
}

// Esc (or an explicit clear): empty query = normal mode — setQuery brings
// back the page the user was on before searching.
function clearSearch() {
  input.value = '';
  selIndex = -1;
  setQuery('');
  announce();
  input.blur();
}

// Manual text insertion for the type-to-search handoff: the keydown's own
// default action is prevented (focusing alone is not reliable across
// browsers), so the character is spliced in at the caret like a keystroke.
function insertAtCaret(ch) {
  const s = input.selectionStart ?? input.value.length;
  const end = input.selectionEnd ?? s;
  input.value = input.value.slice(0, s) + ch + input.value.slice(end);
  input.setSelectionRange(s + ch.length, s + ch.length);
  applyQuery();
}

// Document keydown (capture phase, so it runs before app.js's handler):
// type-to-search and the filtering Escape live here. Skips whenever a
// modal is open, a tile drag is active, or focus already sits in an
// editable element.
function onTypeToSearch(e) {
  if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if (t === input) return; // the box's own keydown handler manages it
  if (modalOpen() || isTileDragActive() || isEditable(t)) return;
  if (e.key === 'Escape') {
    if (isFiltering()) {
      e.preventDefault();
      clearSearch();
    }
    return;
  }
  // IME composition or a dead-key sequence wants a text field — hand it
  // the search box without preventDefault so the sequence continues there.
  if (e.key === 'Process' || e.keyCode === 229 || e.key === 'Dead') {
    input.focus();
    return;
  }
  if (e.key.length !== 1) return; // not a printable character
  e.preventDefault();
  input.focus();
  insertAtCaret(e.key);
}

// Keys while the box itself is focused: selection navigation and open.
function onInputKeydown(e) {
  if (e.isComposing) return; // the IME owns keys mid-composition
  if (e.key === 'Escape') {
    e.preventDefault();
    clearSearch();
    return;
  }
  const n = visibleBookmarks().length;
  if (!isFiltering() || n === 0) return; // plain caret/Tab/Enter behaviour
  switch (e.key) {
    case 'ArrowRight': e.preventDefault(); move(1); break;
    case 'ArrowLeft': e.preventDefault(); move(-1); break;
    case 'ArrowDown': e.preventDefault(); move(state.grid.cols); break;
    case 'ArrowUp': e.preventDefault(); move(-state.grid.cols); break;
    case 'Tab': e.preventDefault(); move(e.shiftKey ? -1 : 1); break;
    case 'Enter': openSelected(e); break;
  }
}

input.addEventListener('input', (e) => {
  if (e.isComposing) return; // pinyin in flight — filter on compositionend
  applyQuery();
});
input.addEventListener('compositionend', () => applyQuery());
input.addEventListener('keydown', onInputKeydown);
document.addEventListener('keydown', onTypeToSearch, true);
// A composition starting while nothing editable is focused (IMEs that
// don't go through the Process keydown): aim it at the search box.
document.addEventListener('compositionstart', (e) => {
  if (e.target === input || modalOpen() || isTileDragActive() || isEditable(e.target)) return;
  input.focus();
});
