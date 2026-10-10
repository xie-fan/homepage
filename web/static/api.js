// api.js — thin fetch wrapper over the homepage REST API.
// Every call returns parsed JSON (or null for 204) and throws an Error
// carrying the server's {"error": "..."} message when one is present.
// Thrown errors also carry the structured fields the API returns:
// err.code (e.g. "duplicate_url"), err.field, err.status and err.data
// (the whole parsed body, e.g. err.data.existing_id). A request that
// never reached the server throws with err.code === 'network'.

async function request(path, { method = 'GET', body } = {}) {
  let resp;
  try {
    resp = await fetch(path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    const err = new Error('network error');
    err.code = 'network';
    throw err;
  }
  if (!resp.ok) {
    let data = null;
    let message = `HTTP ${resp.status}`;
    try {
      data = await resp.json();
      if (data && data.error) message = data.error;
    } catch {
      // Non-JSON error body: keep the HTTP status message.
    }
    const err = new Error(message);
    err.status = resp.status;
    err.data = data;
    if (data) {
      if (data.code) err.code = data.code;
      if (data.field) err.field = data.field;
    }
    throw err;
  }
  if (resp.status === 204) return null;
  return resp.json();
}

export const api = {
  listBookmarks: () => request('/api/bookmarks'),
  // page is the layout page the user is viewing (Round 8): the server
  // uses it as the Add() hint — the bookmark lands at the end of the
  // first page with a free slot at or after it.
  createBookmark: (title, url, allowDuplicate = false, page = 0) =>
    request('/api/bookmarks', {
      method: 'POST',
      body: { title, url, allow_duplicate: allowDuplicate, page },
    }),
  updateBookmark: (id, title, url, allowDuplicate = false) =>
    request(`/api/bookmarks/${id}`, {
      method: 'PUT',
      body: { title, url, allow_duplicate: allowDuplicate },
    }),
  deleteBookmark: (id) => request(`/api/bookmarks/${id}`, { method: 'DELETE' }),
  // Round 8: a drag commits as one server-side move (layout Insert in a
  // transaction); page 0..len(pages) — len = a new page — and index >= 0
  // is clamped to the end of the target page. The response is the full
  // bookmark list, same shape as GET /api/bookmarks.
  moveBookmark: (id, page, index) =>
    request('/api/bookmarks/move', { method: 'POST', body: { id, page, index } }),
  refreshIcon: (id) => request(`/api/bookmarks/${id}/icon/refresh`, { method: 'POST' }),
  getSettings: () => request('/api/settings'),
  // patch is a partial settings document, e.g. {grid:{rows,cols}} or
  // {spacing:'relaxed'} — the server merges it into the stored settings.
  putSettings: (patch) => request('/api/settings', { method: 'PUT', body: patch }),
  // doc is the export-shaped object (or bare array); mode is 'append' | 'replace'.
  importBookmarks: (doc, mode) =>
    request(`/api/import?mode=${encodeURIComponent(mode)}`, { method: 'POST', body: doc }),
};
