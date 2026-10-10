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
  createBookmark: (title, url, allowDuplicate = false) =>
    request('/api/bookmarks', {
      method: 'POST',
      body: { title, url, allow_duplicate: allowDuplicate },
    }),
  updateBookmark: (id, title, url, allowDuplicate = false) =>
    request(`/api/bookmarks/${id}`, {
      method: 'PUT',
      body: { title, url, allow_duplicate: allowDuplicate },
    }),
  deleteBookmark: (id) => request(`/api/bookmarks/${id}`, { method: 'DELETE' }),
  reorder: (ids) => request('/api/bookmarks/order', { method: 'PUT', body: { ids } }),
  refreshIcon: (id) => request(`/api/bookmarks/${id}/icon/refresh`, { method: 'POST' }),
  getSettings: () => request('/api/settings'),
  putSettings: (grid) => request('/api/settings', { method: 'PUT', body: { grid } }),
  // doc is the export-shaped object (or bare array); mode is 'append' | 'replace'.
  importBookmarks: (doc, mode) =>
    request(`/api/import?mode=${encodeURIComponent(mode)}`, { method: 'POST', body: doc }),
};
