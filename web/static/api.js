// api.js — thin fetch wrapper over the homepage REST API.
// Every call returns parsed JSON (or null for 204) and throws an Error
// carrying the server's {"error": "..."} message when one is present.

async function request(path, { method = 'GET', body } = {}) {
  const resp = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!resp.ok) {
    let message = `HTTP ${resp.status}`;
    try {
      const data = await resp.json();
      if (data && data.error) message = data.error;
    } catch {
      // Non-JSON error body: keep the HTTP status message.
    }
    throw new Error(message);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

export const api = {
  listBookmarks: () => request('/api/bookmarks'),
  createBookmark: (title, url) =>
    request('/api/bookmarks', { method: 'POST', body: { title, url } }),
  updateBookmark: (id, title, url) =>
    request(`/api/bookmarks/${id}`, { method: 'PUT', body: { title, url } }),
  deleteBookmark: (id) => request(`/api/bookmarks/${id}`, { method: 'DELETE' }),
  reorder: (ids) => request('/api/bookmarks/order', { method: 'PUT', body: { ids } }),
  refreshIcon: (id) => request(`/api/bookmarks/${id}/icon/refresh`, { method: 'POST' }),
  getSettings: () => request('/api/settings'),
  putSettings: (grid) => request('/api/settings', { method: 'PUT', body: { grid } }),
  // doc is the export-shaped object (or bare array); mode is 'append' | 'replace'.
  importBookmarks: (doc, mode) =>
    request(`/api/import?mode=${encodeURIComponent(mode)}`, { method: 'POST', body: doc }),
};
