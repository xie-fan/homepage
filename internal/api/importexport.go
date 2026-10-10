package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"homepage/internal/store"
)

const (
	exportVersion  = 1
	exportFilename = "homepage-bookmarks.json"
	// maxImportItems bounds a single import.
	maxImportItems = 5000
)

// exportDoc is the on-disk file format for import/export (version 1).
type exportDoc struct {
	Version    int                 `json:"version"`
	ExportedAt time.Time           `json:"exported_at"`
	Settings   store.Settings      `json:"settings"`
	Bookmarks  []store.NewBookmark `json:"bookmarks"`
}

// importDoc is the object form accepted by POST /api/import; version and
// exported_at are ignored on the way in.
type importDoc struct {
	Settings  *store.Settings     `json:"settings"`
	Bookmarks []store.NewBookmark `json:"bookmarks"`
}

// exportBookmarks downloads all bookmarks plus settings as an attachment.
func (s *server) exportBookmarks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bs, err := s.store.List(ctx)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	st, err := s.store.SettingsStore.Get(ctx)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	items := make([]store.NewBookmark, len(bs))
	for i, b := range bs {
		items[i] = store.NewBookmark{Title: b.Title, URL: b.URL}
	}
	doc := exportDoc{
		Version:    exportVersion,
		ExportedAt: time.Now().UTC(),
		Settings:   st,
		Bookmarks:  items,
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", exportFilename))
	writeJSON(w, http.StatusOK, doc)
}

// importBookmarks imports the export shape — or a bare [{title,url}]
// array — either appending (default) or replacing all bookmarks. The
// whole import is rejected on the first invalid item. Embedded settings
// apply only in replace mode. Append mode skips items whose normalized
// URL is already bookmarked or repeats an earlier item; replace mode
// imports the file verbatim. Imported bookmarks get icon fetches queued.
func (s *server) importBookmarks(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "replace" {
		writeErr(w, badInput(codeInvalidMode, "mode",
			fmt.Sprintf("invalid mode %q: want append or replace", mode)))
		return
	}
	items, settings, aerr := decodeImportBody(w, r)
	if aerr != nil {
		writeErr(w, aerr)
		return
	}
	if len(items) > maxImportItems {
		writeErr(w, badInput(codeImportTooLarge, "",
			fmt.Sprintf("too many bookmarks: %d (max %d)", len(items), maxImportItems)))
		return
	}
	// Validate everything up front so a bad item rejects the whole import.
	for i, nb := range items {
		title, rawURL, verr := validateBookmarkInput(nb.Title, nb.URL)
		if verr != nil {
			writeErr(w, badInput(codeImportInvalid, verr.Field,
				fmt.Sprintf("bookmarks[%d]: %s", i, verr.Msg)))
			return
		}
		items[i] = store.NewBookmark{Title: title, URL: rawURL}
	}

	ctx := r.Context()
	skipped := 0
	if mode == "append" {
		var err error
		items, skipped, err = s.dropDuplicateItems(ctx, items)
		if err != nil {
			s.handleErr(w, r, err)
			return
		}
	}
	if mode == "replace" && settings != nil {
		if err := settings.Grid.Validate(); err != nil {
			writeErr(w, badInput(codeInvalidGrid, "grid", fmt.Sprintf("settings: %s", err)))
			return
		}
		if err := s.store.SettingsStore.Put(ctx, *settings); err != nil {
			s.handleErr(w, r, err)
			return
		}
	}

	var created []store.Bookmark
	var err error
	if mode == "replace" {
		created, err = s.store.ReplaceAll(ctx, items)
	} else {
		created, err = s.store.AppendMany(ctx, items)
	}
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	for _, b := range created {
		s.icons.Enqueue(b.ID, b.URL)
	}
	writeJSON(w, http.StatusOK, map[string]int{"imported": len(created), "skipped": skipped})
}

// dropDuplicateItems filters items down to those whose normalized URL
// is not already bookmarked and did not appear earlier in items,
// reporting how many were dropped. Append-mode imports only: replace
// mode must reproduce the file exactly.
func (s *server) dropDuplicateItems(ctx context.Context, items []store.NewBookmark) ([]store.NewBookmark, int, error) {
	bs, err := s.store.List(ctx)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]bool, len(bs)+len(items))
	for _, b := range bs {
		seen[NormalizeURL(b.URL)] = true
	}
	var kept []store.NewBookmark
	for _, nb := range items {
		n := NormalizeURL(nb.URL)
		if seen[n] {
			continue
		}
		seen[n] = true
		kept = append(kept, nb)
	}
	return kept, len(items) - len(kept), nil
}

// decodeImportBody reads the import payload: either the export object or
// a bare [ {title,url}, ... ] array. Returns the bookmark items and the
// optional embedded settings (nil when absent or when importing an
// array), or an *apiError describing the bad payload.
func decodeImportBody(w http.ResponseWriter, r *http.Request) ([]store.NewBookmark, *store.Settings, *apiError) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, nil, badInput(codeImportTooLarge, "",
				fmt.Sprintf("import body too large: max %d bytes", maxImportBody))
		}
		return nil, nil, badInput(codeImportInvalid, "", "import body unreadable")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil, badInput(codeImportInvalid, "", "empty import body")
	}
	if raw[0] == '[' {
		var items []store.NewBookmark
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, nil, badInput(codeImportInvalid, "", "invalid JSON array")
		}
		return items, nil, nil
	}
	var doc importDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, badInput(codeImportInvalid, "", "invalid JSON object")
	}
	return doc.Bookmarks, doc.Settings, nil
}
