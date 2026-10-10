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

// settingsIn is the PUT /api/settings body. Pointer fields make an
// omitted key (keep the current value) distinct from a present one:
// each key is a partial update.
type settingsIn struct {
	Grid    *store.Grid `json:"grid"`
	Spacing *string     `json:"spacing"`
}

// putSettings applies a partial settings update — omitted keys keep
// their current values — validates the result, stores it, and answers
// with it as persisted.
func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsIn
	if aerr := decodeJSON(w, r, &in, maxBody); aerr != nil {
		writeErr(w, aerr)
		return
	}
	ctx := r.Context()
	st, err := s.store.SettingsStore.Get(ctx)
	if err != nil {
		s.handleErr(w, r, err)
		return
	}
	if in.Grid != nil {
		st.Grid = *in.Grid
	}
	if in.Spacing != nil {
		st.Spacing = *in.Spacing
	}
	if err := st.Grid.Validate(); err != nil {
		writeErr(w, badInput(codeInvalidGrid, "grid", err.Error()))
		return
	}
	if err := store.ValidateSpacing(st.Spacing); err != nil {
		writeErr(w, badInput(codeInvalidSpacing, "spacing", err.Error()))
		return
	}
	if err := s.store.SettingsStore.Put(ctx, st); err != nil {
		s.handleErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
