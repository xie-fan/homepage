package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"homepage/internal/db"
	"homepage/internal/store"
)

// fakeIcons records Enqueue calls and returns a canned IconFile result.
type fakeIcons struct {
	mu       sync.Mutex
	enqueued []enqueuedIcon
	path     string
	ct       string
	err      error
}

type enqueuedIcon struct {
	id  int64
	url string
}

func (f *fakeIcons) Enqueue(id int64, pageURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, enqueuedIcon{id, pageURL})
}

func (f *fakeIcons) IconFile(context.Context, store.Bookmark) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path, f.ct, f.err
}

// calls returns a copy of the recorded Enqueue calls.
func (f *fakeIcons) calls() []enqueuedIcon {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.enqueued)
}

// setResult changes what IconFile returns.
func (f *fakeIcons) setResult(path, ct string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path, f.ct, f.err = path, ct, err
}

// newTestServer builds the API over a real migrated SQLite store in a
// temp dir, behind an httptest server.
func newTestServer(t *testing.T) (*httptest.Server, *store.Store, *fakeIcons) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(context.Background(), sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	st := store.NewSQLite(sqlDB)
	icons := &fakeIcons{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(NewServer(Deps{Store: st, Icons: icons, Logger: logger}))
	t.Cleanup(func() {
		srv.Close()
		sqlDB.Close()
	})
	return srv, st, icons
}

// do sends one request; body (when non-nil) is JSON-encoded.
func do(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	return send(t, srv, method, path, rdr)
}

// doRaw sends one request with a raw string body.
func doRaw(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()
	return send(t, srv, method, path, strings.NewReader(body))
}

func send(t *testing.T, srv *httptest.Server, method, path string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// reqJSON performs a JSON request and decodes the response into out
// (skipped when out is nil). Returns the status code.
func reqJSON(t *testing.T, srv *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	resp := do(t, srv, method, path, body)
	defer resp.Body.Close()
	return decodeResp(t, resp, out)
}

// reqRaw is reqJSON with a raw (non-encoded) request body.
func reqRaw(t *testing.T, srv *httptest.Server, method, path, body string, out any) int {
	t.Helper()
	resp := doRaw(t, srv, method, path, body)
	defer resp.Body.Close()
	return decodeResp(t, resp, out)
}

func decodeResp(t *testing.T, resp *http.Response, out any) int {
	t.Helper()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("%s %s: decode response: %v", resp.Request.Method, resp.Request.URL, err)
	}
	return resp.StatusCode
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return raw
}

// postBookmark creates one bookmark through the API and requires a 201.
func postBookmark(t *testing.T, srv *httptest.Server, title, url string) store.Bookmark {
	t.Helper()
	var b store.Bookmark
	code := reqJSON(t, srv, http.MethodPost, "/api/bookmarks",
		map[string]string{"title": title, "url": url}, &b)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/bookmarks %q: status %d, want 201", url, code)
	}
	return b
}

// listTitles returns the titles of all bookmarks in display order.
func listTitles(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	var list []store.Bookmark
	reqJSON(t, srv, http.MethodGet, "/api/bookmarks", nil, &list)
	titles := make([]string, len(list))
	for i, b := range list {
		titles[i] = b.Title
	}
	return titles
}

func TestHealth(t *testing.T) {
	srv, _, _ := newTestServer(t)
	var out map[string]bool
	if code := reqJSON(t, srv, http.MethodGet, "/api/health", nil, &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if !out["ok"] {
		t.Errorf("body = %v, want {\"ok\":true}", out)
	}
}

func TestBookmarkCRUD(t *testing.T) {
	srv, _, icons := newTestServer(t)

	b := postBookmark(t, srv, "Example", "https://example.com")
	if b.ID == 0 || b.Position != 0 || b.IconStatus != store.IconPending {
		t.Fatalf("created = %+v", b)
	}
	// Creating enqueues an icon fetch for the new bookmark.
	if got := icons.calls(); len(got) != 1 || got[0].id != b.ID || got[0].url != b.URL {
		t.Fatalf("enqueued = %v, want one call for %+v", got, b)
	}

	if got := listTitles(t, srv); !slices.Equal(got, []string{"Example"}) {
		t.Fatalf("list = %v", got)
	}

	// A title-only update keeps the icon; no extra enqueue.
	var up store.Bookmark
	code := reqJSON(t, srv, http.MethodPut, fmt.Sprintf("/api/bookmarks/%d", b.ID),
		map[string]string{"title": "Renamed", "url": b.URL}, &up)
	if code != http.StatusOK || up.Title != "Renamed" || up.URL != b.URL {
		t.Fatalf("update = %d %+v", code, up)
	}
	if got := icons.calls(); len(got) != 1 {
		t.Fatalf("enqueued after same-URL update = %v, want still 1 call", got)
	}

	// A URL change resets the icon and queues a refetch.
	code = reqJSON(t, srv, http.MethodPut, fmt.Sprintf("/api/bookmarks/%d", b.ID),
		map[string]string{"title": "Renamed", "url": "https://new.example.com"}, &up)
	if code != http.StatusOK || up.URL != "https://new.example.com" ||
		up.IconStatus != store.IconPending || up.IconPath != "" {
		t.Fatalf("url-change update = %d %+v", code, up)
	}
	if got := icons.calls(); len(got) != 2 || got[1].url != "https://new.example.com" {
		t.Fatalf("enqueued = %v, want refetch of new URL", got)
	}

	if code := reqJSON(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/bookmarks/%d", b.ID), nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
	resp := do(t, srv, http.MethodGet, "/api/bookmarks", nil)
	if raw := readAll(t, resp); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("list after delete = %s, want []", raw)
	}
	if code := reqJSON(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/bookmarks/%d", b.ID), nil, nil); code != http.StatusNotFound {
		t.Fatalf("re-delete = %d, want 404", code)
	}
}

func TestCreateValidation(t *testing.T) {
	srv, _, _ := newTestServer(t)
	longURL := "https://example.com/" + strings.Repeat("x", maxURLLen)
	cases := map[string]map[string]string{
		"ftp scheme":        {"url": "ftp://example.com"},
		"javascript scheme": {"url": "javascript:alert(1)"},
		"no scheme":         {"url": "example.com"},
		"missing host":      {"url": "https:///path"},
		"empty host":        {"url": "https://"},
		"userinfo":          {"url": "https://user:pw@example.com"},
		"url too long":      {"url": longURL},
		"missing url":       {"title": "x"},
		"title too long":    {"title": strings.Repeat("t", maxTitleRunes+1), "url": "https://example.com"},
	}
	for name, body := range cases {
		var out map[string]string
		code := reqJSON(t, srv, http.MethodPost, "/api/bookmarks", body, &out)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
		if out["error"] == "" {
			t.Errorf("%s: response has no error message: %v", name, out)
		}
	}
	// Rejected creates persisted nothing.
	if got := listTitles(t, srv); len(got) != 0 {
		t.Errorf("list = %v, want empty", got)
	}
}

func TestCreateNormalizes(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Empty title defaults to the URL host.
	b := postBookmark(t, srv, "", "https://sub.example.com/page")
	if b.Title != "sub.example.com" {
		t.Errorf("title = %q, want host default", b.Title)
	}
	// Title and URL are trimmed; unknown fields are ignored.
	var b2 store.Bookmark
	code := reqJSON(t, srv, http.MethodPost, "/api/bookmarks",
		map[string]string{"title": "  padded  ", "url": "  https://two.example.com  ", "extra": "ignored"}, &b2)
	if code != http.StatusCreated || b2.Title != "padded" || b2.URL != "https://two.example.com" {
		t.Errorf("not normalized: %d %+v", code, b2)
	}
}

func TestNotFound(t *testing.T) {
	srv, _, _ := newTestServer(t)
	valid := map[string]string{"title": "x", "url": "https://x.example.com"}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/bookmarks/999", valid},
		{http.MethodDelete, "/api/bookmarks/999", nil},
		{http.MethodPost, "/api/bookmarks/999/icon/refresh", nil},
		{http.MethodGet, "/api/icons/999", nil},
	} {
		if code := reqJSON(t, srv, tc.method, tc.path, tc.body, nil); code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", tc.method, tc.path, code)
		}
	}
	// A non-numeric {id} is a 400, not a 404.
	if code := reqJSON(t, srv, http.MethodDelete, "/api/bookmarks/abc", nil, nil); code != http.StatusBadRequest {
		t.Errorf("bad id: %d, want 400", code)
	}
}

// TestReorder exercises PUT /api/bookmarks/order. Because the {id}
// handler would 400 on the non-numeric id "order", a 200 here proves the
// literal route wins over PUT /api/bookmarks/{id}.
func TestReorder(t *testing.T) {
	srv, _, _ := newTestServer(t)
	a := postBookmark(t, srv, "a", "https://a.example.com")
	b := postBookmark(t, srv, "b", "https://b.example.com")
	c := postBookmark(t, srv, "c", "https://c.example.com")

	var list []store.Bookmark
	code := reqJSON(t, srv, http.MethodPut, "/api/bookmarks/order",
		map[string][]int64{"ids": {c.ID, a.ID, b.ID}}, &list)
	if code != http.StatusOK {
		t.Fatalf("reorder = %d, want 200", code)
	}
	var got []string
	for _, bm := range list {
		got = append(got, bm.Title)
	}
	if !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("order = %v", got)
	}
	if got := listTitles(t, srv); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("persisted order = %v", got)
	}

	for name, ids := range map[string][]int64{
		"missing id": {a.ID, b.ID},
		"unknown id": {a.ID, b.ID, c.ID, 9999},
		"duplicate":  {a.ID, a.ID, b.ID},
	} {
		var out map[string]string
		code := reqJSON(t, srv, http.MethodPut, "/api/bookmarks/order",
			map[string][]int64{"ids": ids}, &out)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
		if out["error"] == "" {
			t.Errorf("%s: no error message", name)
		}
	}
	// Rejected reorders leave the stored order untouched.
	if got := listTitles(t, srv); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("order after rejects = %v", got)
	}
}

func TestSettings(t *testing.T) {
	srv, _, _ := newTestServer(t)

	var st store.Settings
	reqJSON(t, srv, http.MethodGet, "/api/settings", nil, &st)
	if st.Grid != store.DefaultGrid {
		t.Fatalf("default grid = %+v, want %+v", st.Grid, store.DefaultGrid)
	}

	want := store.Settings{Grid: store.Grid{Rows: 3, Cols: 5}}
	if code := reqJSON(t, srv, http.MethodPut, "/api/settings", want, &st); code != http.StatusOK {
		t.Fatalf("put = %d, want 200", code)
	}
	reqJSON(t, srv, http.MethodGet, "/api/settings", nil, &st)
	if st != want {
		t.Fatalf("settings = %+v, want %+v", st, want)
	}

	for name, body := range map[string]any{
		"rows too small": store.Settings{Grid: store.Grid{Rows: 2, Cols: 5}},
		"rows too big":   store.Settings{Grid: store.Grid{Rows: 6, Cols: 5}},
		"cols too big":   store.Settings{Grid: store.Grid{Rows: 4, Cols: 7}},
		"zero grid":      store.Settings{},
	} {
		if code := reqJSON(t, srv, http.MethodPut, "/api/settings", body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	// Malformed JSON is also a 400; stored settings survive all of it.
	if code := reqRaw(t, srv, http.MethodPut, "/api/settings", "{not json", nil); code != http.StatusBadRequest {
		t.Errorf("bad json: %d, want 400", code)
	}
	reqJSON(t, srv, http.MethodGet, "/api/settings", nil, &st)
	if st != want {
		t.Fatalf("settings after rejects = %+v, want %+v", st, want)
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	srv1, _, _ := newTestServer(t)
	postBookmark(t, srv1, "a", "https://a.example.com")
	postBookmark(t, srv1, "b", "https://b.example.com")
	grid := store.Settings{Grid: store.Grid{Rows: 3, Cols: 5}}
	reqJSON(t, srv1, http.MethodPut, "/api/settings", grid, nil)

	resp := do(t, srv1, http.MethodGet, "/api/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, exportFilename) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	raw := readAll(t, resp)

	var doc struct {
		Version    int                 `json:"version"`
		ExportedAt string              `json:"exported_at"`
		Settings   store.Settings      `json:"settings"`
		Bookmarks  []store.NewBookmark `json:"bookmarks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("export json: %v", err)
	}
	if doc.Version != exportVersion || doc.ExportedAt == "" ||
		len(doc.Bookmarks) != 2 || doc.Settings != grid {
		t.Fatalf("export doc = %s", raw)
	}

	// Replace-import into a fresh instance applies bookmarks and settings.
	srv2, _, icons2 := newTestServer(t)
	var res map[string]int
	code := reqRaw(t, srv2, http.MethodPost, "/api/import?mode=replace", string(raw), &res)
	if code != http.StatusOK || res["imported"] != 2 {
		t.Fatalf("replace import = %d %v", code, res)
	}
	if got := listTitles(t, srv2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("imported list = %v", got)
	}
	var st store.Settings
	reqJSON(t, srv2, http.MethodGet, "/api/settings", nil, &st)
	if st != grid {
		t.Fatalf("imported settings = %+v, want %+v", st, grid)
	}
	// Each imported bookmark got an icon fetch queued.
	if got := icons2.calls(); len(got) != 2 {
		t.Fatalf("enqueued = %v, want 2 calls", got)
	}

	// Append (the default mode) adds without wiping, and ignores settings
	// embedded in the payload.
	doc.Settings = store.Settings{Grid: store.Grid{Rows: 5, Cols: 6}}
	raw2, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	code = reqRaw(t, srv2, http.MethodPost, "/api/import", string(raw2), &res)
	if code != http.StatusOK || res["imported"] != 2 {
		t.Fatalf("append import = %d %v", code, res)
	}
	if got := listTitles(t, srv2); !slices.Equal(got, []string{"a", "b", "a", "b"}) {
		t.Fatalf("after append = %v", got)
	}
	reqJSON(t, srv2, http.MethodGet, "/api/settings", nil, &st)
	if st != grid {
		t.Fatalf("append changed settings = %+v, want %+v", st, grid)
	}
}

func TestImportBareArray(t *testing.T) {
	srv, _, icons := newTestServer(t)
	var res map[string]int
	code := reqRaw(t, srv, http.MethodPost, "/api/import",
		`[{"title":"x","url":"https://x.example.com"},{"url":"https://y.example.com"}]`, &res)
	if code != http.StatusOK || res["imported"] != 2 {
		t.Fatalf("import = %d %v", code, res)
	}
	// The item without a title got the host default.
	if got := listTitles(t, srv); !slices.Equal(got, []string{"x", "y.example.com"}) {
		t.Fatalf("list = %v", got)
	}
	if got := icons.calls(); len(got) != 2 {
		t.Fatalf("enqueued = %v, want 2 calls", got)
	}
}

func TestImportValidation(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// One bad item rejects the whole import and names the item index.
	resp := doRaw(t, srv, http.MethodPost, "/api/import?mode=replace",
		`[{"title":"ok","url":"https://ok.example.com"},{"title":"bad","url":"ftp://x"}]`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if raw := readAll(t, resp); !strings.Contains(string(raw), "bookmarks[1]") {
		t.Fatalf("error does not name the bad index: %s", raw)
	}
	if got := listTitles(t, srv); len(got) != 0 {
		t.Fatalf("partial import went through: %v", got)
	}

	// Over the item cap → 400.
	big := make([]store.NewBookmark, maxImportItems+1)
	for i := range big {
		big[i] = store.NewBookmark{Title: "x", URL: "https://x.example.com"}
	}
	if code := reqJSON(t, srv, http.MethodPost, "/api/import", big, nil); code != http.StatusBadRequest {
		t.Fatalf("oversize import = %d, want 400", code)
	}

	// Unknown mode and empty body are 400s too.
	if code := reqRaw(t, srv, http.MethodPost, "/api/import?mode=sideways", `[]`, nil); code != http.StatusBadRequest {
		t.Errorf("bad mode = %d, want 400", code)
	}
	if code := reqRaw(t, srv, http.MethodPost, "/api/import", ``, nil); code != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", code)
	}
}

// TestImportReplaceInvalidSettings: present-but-invalid settings reject
// the import in replace mode.
func TestImportReplaceInvalidSettings(t *testing.T) {
	srv, _, _ := newTestServer(t)
	postBookmark(t, srv, "keep", "https://keep.example.com")
	body := `{"settings":{"grid":{"rows":99,"cols":5}},"bookmarks":[{"url":"https://x.example.com"}]}`
	if code := reqRaw(t, srv, http.MethodPost, "/api/import?mode=replace", body, nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	// Nothing was replaced.
	if got := listTitles(t, srv); !slices.Equal(got, []string{"keep"}) {
		t.Fatalf("list = %v", got)
	}
}

func TestGetIcon(t *testing.T) {
	srv, st, icons := newTestServer(t)
	ctx := context.Background()
	b, err := st.Create(ctx, "a", "https://a.example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	iconURL := fmt.Sprintf("/api/icons/%d", b.ID)

	// Icon not fetched yet (status pending) → 404.
	if code := reqJSON(t, srv, http.MethodGet, iconURL, nil, nil); code != http.StatusNotFound {
		t.Fatalf("pending icon = %d, want 404", code)
	}

	// Status ok → serve the file the IconService reports, with headers.
	path := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(path, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	icons.setResult(path, "image/png", nil)
	if err := st.SetIcon(ctx, b.ID, "icon.png", store.IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	resp := do(t, srv, http.MethodGet, iconURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("icon = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != iconCacheControl {
		t.Errorf("Cache-Control = %q, want %q", cc, iconCacheControl)
	}
	if raw := readAll(t, resp); string(raw) != "PNGDATA" {
		t.Errorf("body = %q, want PNGDATA", raw)
	}

	// The IconService reports the cached file gone → 404.
	icons.setResult("", "", fs.ErrNotExist)
	if code := reqJSON(t, srv, http.MethodGet, iconURL, nil, nil); code != http.StatusNotFound {
		t.Fatalf("missing file = %d, want 404", code)
	}
}

func TestIconRefresh(t *testing.T) {
	srv, st, icons := newTestServer(t)
	ctx := context.Background()
	b, err := st.Create(ctx, "a", "https://a.example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Direct store writes bypass the API, so nothing was enqueued yet.
	if err := st.SetIcon(ctx, b.ID, "x.png", store.IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}

	var out store.Bookmark
	code := reqJSON(t, srv, http.MethodPost,
		fmt.Sprintf("/api/bookmarks/%d/icon/refresh", b.ID), nil, &out)
	if code != http.StatusAccepted || out.IconStatus != store.IconPending || out.IconPath != "" {
		t.Fatalf("refresh = %d %+v, want 202 pending", code, out)
	}
	if got := icons.calls(); len(got) != 1 || got[0].id != b.ID || got[0].url != b.URL {
		t.Fatalf("enqueued = %v", got)
	}
	// The pending state was persisted, not just reported.
	got, err := st.BookmarkStore.Get(ctx, b.ID)
	if err != nil || got.IconStatus != store.IconPending || got.IconPath != "" {
		t.Fatalf("stored = %+v, %v", got, err)
	}
}

func TestStaticUI(t *testing.T) {
	srv, _, _ := newTestServer(t)

	resp := do(t, srv, http.MethodGet, "/", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(string(body), `id="app"`) {
		t.Error(`GET /: body missing id="app" app root`)
	}
}

func TestStaticAssets(t *testing.T) {
	srv, _, _ := newTestServer(t)

	for _, path := range []string{"/app.js", "/api.js", "/style.css"} {
		resp := do(t, srv, http.MethodGet, path, nil)
		_ = readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestUnknownAPIPath(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// Unknown /api paths get a JSON 404 for every method, never the file
	// server's plain-text response.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var out map[string]string
		code := reqJSON(t, srv, method, "/api/no-such-route", nil, &out)
		if code != http.StatusNotFound {
			t.Errorf("%s /api/no-such-route: status %d, want 404", method, code)
		}
		if out["error"] == "" {
			t.Errorf("%s /api/no-such-route: body %v has no error", method, out)
		}
	}

	var out map[string]string
	if code := reqJSON(t, srv, http.MethodGet, "/api", nil, &out); code != http.StatusNotFound {
		t.Errorf("GET /api: status %d, want 404", code)
	}

	// A non-GET method on a static path is a 405, not a file response.
	resp := do(t, srv, http.MethodPost, "/", nil)
	_ = readAll(t, resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /: status %d, want 405", resp.StatusCode)
	}
}
