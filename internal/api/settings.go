package api

import (
	"net/http"

	"homepage/internal/store"
)

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.SettingsStore.Get(r.Context())
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putSettings validates the whole settings document, stores it, and
// answers with it as persisted.
func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if aerr := decodeJSON(w, r, &in, maxBody); aerr != nil {
		writeErr(w, aerr)
		return
	}
	if err := in.Grid.Validate(); err != nil {
		writeErr(w, badInput(codeInvalidGrid, "grid", err.Error()))
		return
	}
	if err := s.store.SettingsStore.Put(r.Context(), in); err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}
