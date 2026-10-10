package api

import (
	"net/http"

	"homepage/internal/store"
)

// bookmarkIn is the JSON body for create and update:
// {title, url, page?, allow_duplicate?}. page (create only) is the page
// the user is viewing — the Add hint; it is ignored on update.
type bookmarkIn struct {
	Title          string `json:"title"`
	URL            string `json:"url"`
	Page           *int   `json:"page"`
	AllowDuplicate bool   `json:"allow_duplicate"`
}

func (s *server) listBookmarks(w http.ResponseWriter, r *http.Request) {
	bs, err := s.store.List(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

func (s *server) createBookmark(w http.ResponseWriter, r *http.Request) {
	var in bookmarkIn
	if aerr := decodeJSON(w, r, &in, maxBody); aerr != nil {
		writeErr(w, aerr)
		return
	}
	title, rawURL, aerr := validateBookmarkInput(in.Title, in.URL)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	if s.rejectDuplicate(w, r, rawURL, 0, in.AllowDuplicate) {
		return
	}
	fromPage := 0
	if in.Page != nil {
		fromPage = *in.Page
	}
	b, err := s.store.Create(r.Context(), title, rawURL, fromPage)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.icons.Enqueue(b.ID, b.URL)
	writeJSON(w, http.StatusCreated, b)
}

func (s *server) updateBookmark(w http.ResponseWriter, r *http.Request) {
	id, aerr := pathID(r)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	var in bookmarkIn
	if aerr := decodeJSON(w, r, &in, maxBody); aerr != nil {
		writeErr(w, aerr)
		return
	}
	title, rawURL, aerr := validateBookmarkInput(in.Title, in.URL)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	// Fetched up front for the 404 and to detect a URL change.
	old, err := s.store.BookmarkStore.Get(r.Context(), id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if s.rejectDuplicate(w, r, rawURL, id, in.AllowDuplicate) {
		return
	}
	b, err := s.store.Update(r.Context(), id, title, rawURL)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	// A new URL invalidates the cached icon (the store reset it to
	// pending); a same-URL update keeps it.
	if b.URL != old.URL {
		s.icons.Enqueue(b.ID, b.URL)
	}
	writeJSON(w, http.StatusOK, b)
}

// rejectDuplicate writes a 409 naming the colliding bookmark and reports
// true when the normalized rawURL already belongs to a stored bookmark
// other than excludeID (0 on create) — unless the caller passed
// allow_duplicate. Personal scale: the check scans the full list.
func (s *server) rejectDuplicate(w http.ResponseWriter, r *http.Request, rawURL string, excludeID int64, allow bool) bool {
	if allow {
		return false
	}
	want := NormalizeURL(rawURL)
	bs, err := s.store.List(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return true
	}
	for _, b := range bs {
		if b.ID != excludeID && NormalizeURL(b.URL) == want {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":       "bookmark with this URL already exists",
				"code":        codeDuplicateURL,
				"existing_id": b.ID,
			})
			return true
		}
	}
	return false
}

func (s *server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, aerr := pathID(r)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		s.handleErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveIn is the POST /api/bookmarks/move body: relocate bookmark id to
// slot index of page (page may name a new last page).
type moveIn struct {
	ID    int64 `json:"id"`
	Page  int   `json:"page"`
	Index int   `json:"index"`
}

// moveBookmark applies a layout.Insert inside one transaction and
// answers 200 with the full bookmark list, same as GET /api/bookmarks —
// the frontend adopts the server's result verbatim.
func (s *server) moveBookmark(w http.ResponseWriter, r *http.Request) {
	var in moveIn
	if aerr := decodeJSON(w, r, &in, maxBody); aerr != nil {
		writeErr(w, badInput(codeInvalidMove, "",
			`expected a JSON body {"id":N,"page":P,"index":I}`))
		return
	}
	switch {
	case in.ID < 0:
		writeErr(w, badInput(codeInvalidMove, "id", "id must not be negative"))
		return
	case in.Page < 0:
		writeErr(w, badInput(codeInvalidMove, "page", "page must not be negative"))
		return
	case in.Index < 0:
		writeErr(w, badInput(codeInvalidMove, "index", "index must not be negative"))
		return
	}
	bs, err := s.store.Move(r.Context(), in.ID, in.Page, in.Index)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

// refreshIcon marks the bookmark's icon pending, queues a forced
// refetch (an explicit refresh bypasses the icon cache), and answers
// 202 with the updated bookmark.
func (s *server) refreshIcon(w http.ResponseWriter, r *http.Request) {
	id, aerr := pathID(r)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	ctx := r.Context()
	b, err := s.store.BookmarkStore.Get(ctx, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if err := s.store.SetIcon(ctx, id, "", store.IconPending); err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.icons.EnqueueForce(b.ID, b.URL)
	// Re-read so the response reflects the persisted icon state.
	b, err = s.store.BookmarkStore.Get(ctx, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}
