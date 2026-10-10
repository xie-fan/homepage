package api

import "net/http"

// Machine-readable codes carried by every API error body:
// {"error": <message>, "code": <code>} plus an optional "field" naming
// the offending input field. "error" stays a plain string so existing
// clients keep working.
const (
	codeInvalidJSON      = "invalid_json"
	codeURLRequired      = "url_required"
	codeURLInvalid       = "url_invalid"
	codeURLScheme        = "url_scheme"
	codeURLTooLong       = "url_too_long"
	codeURLUserinfo      = "url_userinfo"
	codeTitleTooLong     = "title_too_long"
	codeNotFound         = "not_found"
	codeInvalidID        = "invalid_id"
	codeInvalidOrder     = "invalid_order"
	codeInvalidGrid      = "invalid_grid"
	codeImportInvalid    = "import_invalid"
	codeImportTooLarge   = "import_too_large"
	codeInvalidMode      = "invalid_mode"
	codeMethodNotAllowed = "method_not_allowed"
	codeInternal         = "internal"
)

// apiError is a typed handler error: an HTTP status plus the fields of
// the error body. Validation helpers return it so handlers can respond
// with writeErr and nothing else.
type apiError struct {
	Status int
	Code   string
	Field  string // optional: names the offending input field
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

// Shared fixed errors; writeErr only reads them.
var (
	errNotFound  = &apiError{http.StatusNotFound, codeNotFound, "", "not found"}
	errNoIcon    = &apiError{http.StatusNotFound, codeNotFound, "", "no icon"}
	errInternal  = &apiError{http.StatusInternalServerError, codeInternal, "", "internal error"}
	errBadMethod = &apiError{http.StatusMethodNotAllowed, codeMethodNotAllowed, "", "method not allowed"}
	errBadJSON   = &apiError{http.StatusBadRequest, codeInvalidJSON, "", "invalid JSON"}
)

// badInput builds a 400 apiError blaming a request field ("" when the
// error is not field-specific).
func badInput(code, field, msg string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: code, Field: field, Msg: msg}
}

// writeErr renders e as {"error","code","field"?} JSON — the single
// helper every handler error path goes through.
func writeErr(w http.ResponseWriter, e *apiError) {
	body := map[string]string{"error": e.Msg, "code": e.Code}
	if e.Field != "" {
		body["field"] = e.Field
	}
	writeJSON(w, e.Status, body)
}
