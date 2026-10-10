package api

import (
	"net/http"

	"homepage/internal/store"
)

// bookmarkIn is the JSON body for create and update: {title, url}.
type bookmarkIn struct {
	Title string `json:"title"`
	URL   string `json:"url"`
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
	if err := decodeJSON(w, r, &in, maxBody); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title, rawURL, err := validateBookmarkInput(in.Title, in.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := s.store.Create(r.Context(), title, rawURL)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	s.icons.Enqueue(b.ID, b.URL)
	writeJSON(w, http.StatusCreated, b)
}

func (s *server) updateBookmark(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var in bookmarkIn
	if err := decodeJSON(w, r, &in, maxBody); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title, rawURL, err := validateBookmarkInput(in.Title, in.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Fetched up front for the 404 and to detect a URL change.
	old, err := s.store.BookmarkStore.Get(r.Context(), id)
	if err != nil {
		s.handleErr(w, r, err)
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

func (s *server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		s.handleErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reorderBookmarks applies a full new order: ids must be a permutation of
// every existing bookmark id (enforced by the store). The response is the
// reordered list.
func (s *server) reorderBookmarks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(w, r, &in, maxBody); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Reorder(r.Context(), in.IDs); err != nil {
		s.handleErr(w, r, err)
		return
	}
	bs, err := s.store.List(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

// refreshIcon marks the bookmark's icon pending, queues a refetch, and
// answers 202 with the updated bookmark.
func (s *server) refreshIcon(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	s.icons.Enqueue(b.ID, b.URL)
	// Re-read so the response reflects the persisted icon state.
	b, err = s.store.BookmarkStore.Get(ctx, id)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}
