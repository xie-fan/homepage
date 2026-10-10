// Package api wires the HTTP API: routing, handlers, JSON in/out.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"homepage/internal/store"
	"homepage/web"
)

// Request body limits: bookmark/settings payloads are tiny, imports may
// carry thousands of entries.
const (
	maxBody       = 1 << 20  // 1 MiB
	maxImportBody = 10 << 20 // 10 MiB
)

// Deps collects the dependencies NewServer needs.
type Deps struct {
	Store  *store.Store
	Icons  IconService
	Logger *slog.Logger
	// Static is the UI file system served for non-/api GET paths; nil
	// falls back to the embedded assets in homepage/web.
	Static fs.FS
}

// server is the unexported handler implementation behind NewServer.
type server struct {
	store    *store.Store
	icons    IconService
	logger   *slog.Logger
	static   http.Handler // file server for the embedded UI
	staticFS fs.FS        // the same files, for direct reads (favicon)
	// apiPaths mirrors the API routes without methods so unknownAPI can
	// tell a wrong-method call (405) from an unknown path (404);
	// apiMethods lists the registered methods per path pattern.
	apiPaths   *http.ServeMux
	apiMethods map[string][]string
}

// NewServer builds the API handler: all routes plus logging and recover
// middleware.
func NewServer(deps Deps) http.Handler {
	s := &server{store: deps.Store, icons: deps.Icons, logger: deps.Logger}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.icons == nil {
		s.icons = NopIcons{}
	}

	mux := http.NewServeMux()
	s.apiPaths = http.NewServeMux()
	s.apiMethods = map[string][]string{}
	nop := func(http.ResponseWriter, *http.Request) {}
	route := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, h)
		method, path, _ := strings.Cut(pattern, " ")
		if _, ok := s.apiMethods[path]; !ok {
			s.apiPaths.HandleFunc(path, nop)
		}
		s.apiMethods[path] = append(s.apiMethods[path], method)
	}
	route("GET /api/health", s.health)
	route("GET /api/bookmarks", s.listBookmarks)
	route("POST /api/bookmarks", s.createBookmark)
	route("PUT /api/bookmarks/{id}", s.updateBookmark)
	route("DELETE /api/bookmarks/{id}", s.deleteBookmark)
	route("POST /api/bookmarks/move", s.moveBookmark)
	route("POST /api/bookmarks/{id}/icon/refresh", s.refreshIcon)
	route("GET /api/icons/{id}", s.getIcon)
	route("GET /api/settings", s.getSettings)
	route("PUT /api/settings", s.putSettings)
	route("GET /api/export", s.exportBookmarks)
	route("POST /api/import", s.importBookmarks)

	// Browsers request /favicon.ico unconditionally; answer with the app
	// icon SVG (also reachable as /favicon.svg via the file server).
	mux.HandleFunc("GET /favicon.ico", s.serveFavicon)

	// Unmatched /api paths (any method) get a JSON 404 instead of the file
	// server's plain-text one. More specific API patterns win over these.
	mux.HandleFunc("/api", s.unknownAPI)
	mux.HandleFunc("/api/", s.unknownAPI)

	static := deps.Static
	if static == nil {
		static = web.Static()
	}
	s.static = http.FileServer(http.FS(static))
	s.staticFS = static
	// The bare "/" pattern also covers /api/* paths, but the more specific
	// API and /api catch-all patterns win for those.
	mux.HandleFunc("/", s.serveStatic)

	// recover sits inside the logger so a panicking handler still produces
	// a logged 500 response.
	return s.logRequests(s.recover(mux))
}

// unknownAPI is the catch-all for /api requests no route claimed: a
// known path with the wrong method is a 405 (with Allow), anything else
// a 404.
func (s *server) unknownAPI(w http.ResponseWriter, r *http.Request) {
	if _, pattern := s.apiPaths.Handler(r); pattern != "" {
		w.Header().Set("Allow", strings.Join(s.apiMethods[pattern], ", "))
		writeErr(w, errBadMethod)
		return
	}
	writeErr(w, errNotFound)
}

// serveStatic serves the embedded UI for every non-/api GET path — the
// file server maps "/" to index.html itself. Other methods get a 405
// rather than a file response.
func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, errBadMethod)
		return
	}
	s.static.ServeHTTP(w, r)
}

// serveFavicon answers GET /favicon.ico with the app icon SVG, day-cached.
func (s *server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.staticFS, "favicon.svg")
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", iconCacheControl)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// logRequests logs one line per request: method, path, status, duration.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start))
	})
}

// statusWriter records the response status code for request logging.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// recover converts a handler panic into a logged 500.
func (s *server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.logger.Error("handler panic",
					"method", r.Method, "path", r.URL.Path, "err", p)
				writeErr(w, errInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// decodeJSON decodes a JSON request body of at most limit bytes into v.
// Unknown fields are ignored — decoding is deliberately lenient. The
// decoder's detail is dropped: callers get a fixed "invalid JSON".
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) *apiError {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return badInput(codeInvalidJSON, "", "request body too large")
		}
		return errBadJSON
	}
	return nil
}

// pathID parses the {id} path value as a positive int64.
func pathID(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, badInput(codeInvalidID, "id",
			fmt.Sprintf("invalid bookmark id %q", r.PathValue("id")))
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encode errors are not actionable here: the header is already sent and
	// the handler types we encode cannot fail marshaling.
	_ = json.NewEncoder(w).Encode(v)
}

// handleErr maps domain errors to responses: ErrNotFound → 404,
// ErrInvalidMove → 400 (the wrapped detail is safe to show), anything
// else → 500 with details logged server-side only.
func (s *server) handleErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, errNotFound)
	case errors.Is(err, store.ErrInvalidMove):
		writeErr(w, badInput(codeInvalidMove, "", err.Error()))
	default:
		s.logger.Error("request failed",
			"method", r.Method, "path", r.URL.Path, "err", err)
		writeErr(w, errInternal)
	}
}
