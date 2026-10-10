// Package api wires the HTTP API: routing, handlers, JSON in/out.
package api

import (
	"encoding/json"
	"net/http"
)

// NewServer builds the API handler. Later tasks add bookmark, settings and
// icon routes plus their dependencies (store, favicon fetcher, config).
func NewServer() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health)
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encode errors are not actionable here: the header is already sent and
	// the handler types we encode cannot fail marshaling.
	_ = json.NewEncoder(w).Encode(v)
}
