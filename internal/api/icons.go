package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"

	"homepage/internal/store"
)

// IconService abstracts icon fetching and caching. The real
// implementation is the async favicon worker (internal/favicon, Task 5);
// tests substitute a fake.
type IconService interface {
	// Enqueue queues an icon fetch for the bookmark; it must not block.
	// A fresh cached file for the bookmark's host may be adopted instead
	// of hitting the network.
	Enqueue(id int64, pageURL string)
	// EnqueueForce queues a fetch that always hits the network,
	// bypassing and overwriting the host's cached icon file.
	EnqueueForce(id int64, pageURL string)
	// IconFile returns the cached icon file for b and its content type.
	IconFile(ctx context.Context, b store.Bookmark) (path, contentType string, err error)
}

// NopIcons is the placeholder IconService used until the favicon worker
// exists: enqueues do nothing and IconFile reports a missing file.
type NopIcons struct{}

func (NopIcons) Enqueue(int64, string)      {}
func (NopIcons) EnqueueForce(int64, string) {}

func (NopIcons) IconFile(context.Context, store.Bookmark) (string, string, error) {
	return "", "", fs.ErrNotExist
}

// iconCacheControl lets clients cache a served icon for a day.
const iconCacheControl = "public, max-age=86400"

// getIcon serves the cached icon file for a bookmark. Until a fetch
// succeeded (icon_status = ok) — or if the cached file is gone — it 404s
// and the frontend falls back to a letter avatar.
func (s *server) getIcon(w http.ResponseWriter, r *http.Request) {
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
	if b.IconStatus != store.IconOK {
		writeErr(w, errNoIcon)
		return
	}
	path, contentType, err := s.icons.IconFile(ctx, b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, store.ErrNotFound) {
			writeErr(w, errNoIcon)
			return
		}
		s.handleErr(w, r, err)
		return
	}
	// An empty content type leaves ServeFile free to sniff one.
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", iconCacheControl)
	http.ServeFile(w, r, path)
}
