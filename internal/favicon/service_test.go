package favicon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homepage/internal/db"
	"homepage/internal/store"
)

// syncBuf is a mutex-guarded buffer so a slog handler on the worker
// goroutine can be read safely from the test goroutine.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// testEnv bundles a Service wired to a real SQLite store and an icon
// source server, all living in t.TempDir().
type testEnv struct {
	svc     *Service
	st      *store.Store
	srv     *httptest.Server
	iconDir string
}

// newTestEnv spins up: an httptest server whose "/" is an HTML page
// linking to "/i.png" (a PNG); the favicon worker pointed at it with the
// SSRF guard off.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(context.Background(), sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	st := store.NewSQLite(sqlDB)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><link rel="icon" href="/i.png"></head>`))
	})
	serveIcon(t, mux, "/i.png", "image/png", png1x1)
	srv := httptest.NewServer(mux)

	fetcher := newFetcher(Options{})
	svc, err := NewService(st.BookmarkStore, fetcher, dir,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		svc.Close()
		srv.Close()
		sqlDB.Close()
	})
	return &testEnv{svc: svc, st: st, srv: srv, iconDir: filepath.Join(dir, "icons")}
}

// waitStatus polls until the bookmark's icon status equals want or the
// deadline expires.
func waitStatus(t *testing.T, st store.BookmarkStore, id int64, want string) store.Bookmark {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var b store.Bookmark
	for time.Now().Before(deadline) {
		var err error
		b, err = st.Get(context.Background(), id)
		if err == nil && b.IconStatus == want {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("bookmark %d icon status = %q, want %q", id, b.IconStatus, want)
	return b
}

func TestServiceEnqueueFetchesIcon(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	b, err := env.st.Create(ctx, "t", env.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b.ID, b.URL)
	env.svc.Enqueue(b.ID, b.URL) // dedupe: a second enqueue must not break

	b = waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)
	if b.IconPath == "" {
		t.Fatal("IconPath empty after ok")
	}
	raw, err := os.ReadFile(filepath.Join(env.iconDir, b.IconPath))
	if err != nil {
		t.Fatalf("cached icon missing: %v", err)
	}
	if len(raw) != len(png1x1) {
		t.Fatalf("cached icon = %d bytes", len(raw))
	}

	// IconFile resolves the same file and derives its type.
	path, ct, err := env.svc.IconFile(ctx, b)
	if err != nil {
		t.Fatalf("IconFile: %v", err)
	}
	if ct != "image/png" {
		t.Fatalf("content type = %q", ct)
	}
	if filepath.Dir(path) != env.iconDir || filepath.Base(path) != b.IconPath {
		t.Fatalf("IconFile path = %q", path)
	}
}

func TestServiceNoIcon(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// A page with no icon anywhere (favicon.ico 404s through "/").
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><title>none</title></head>`))
	})
	bare := httptest.NewServer(mux)
	defer bare.Close()

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b, err := env.st.Create(ctx, "t", bare.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b.ID, b.URL)
	waitStatus(t, env.st.BookmarkStore, b.ID, store.IconNone)
}

func TestServiceStartRecoversPending(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Pending rows that exist before Start are picked up automatically —
	// this is the restart-recovery path.
	b, err := env.st.Create(ctx, "t", env.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)
}

func TestServiceBlockedPrivateRecordsError(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Swap in a guarding fetcher (AllowPrivate off) so the loopback
	// httptest target is refused by the SSRF guard, and capture the
	// worker's logs to check the outcome is explained.
	env.svc.fetch = New(Options{Timeout: 5 * time.Second})
	logs := &syncBuf{}
	env.svc.logger = slog.New(slog.NewTextHandler(logs, nil))

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b, err := env.st.Create(ctx, "t", env.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b.ID, b.URL)
	waitStatus(t, env.st.BookmarkStore, b.ID, store.IconError)

	// The block is logged at Info with an actionable reason so users
	// know HOMEPAGE_FAVICON_ALLOW_PRIVATE exists for LAN sites.
	out := logs.String()
	if !strings.Contains(out, "level=INFO") ||
		!strings.Contains(out, "blocked private address") {
		t.Fatalf("expected Info log \"blocked private address\", got: %s", out)
	}
}

func TestServiceSkipsStaleJob(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b, err := env.st.Create(ctx, "t", env.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Enqueued URL doesn't match the bookmark's current URL: the worker
	// must drop the job instead of saving a mismatched icon.
	env.svc.Enqueue(b.ID, "https://unrelated.example/")
	time.Sleep(200 * time.Millisecond)
	got, err := env.st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IconStatus != store.IconPending {
		t.Fatalf("status = %q, want still pending", got.IconStatus)
	}
}

// countingIconServer serves the usual "HTML page linking /i.png" icon
// setup while counting page and icon requests separately, so tests can
// assert how many real fetches ran.
type countingIconServer struct {
	srv      *httptest.Server
	pageHits *atomic.Int32
	iconHits *atomic.Int32
}

func newCountingIconServer(t *testing.T) *countingIconServer {
	t.Helper()
	c := &countingIconServer{pageHits: &atomic.Int32{}, iconHits: &atomic.Int32{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		c.pageHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><link rel="icon" href="/i.png"></head>`))
	})
	mux.HandleFunc("/i.png", func(w http.ResponseWriter, _ *http.Request) {
		c.iconHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	})
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

func TestServiceSameHostSingleFetch(t *testing.T) {
	// Three bookmarks on one host enqueued at once coalesce into a
	// single fetch and land on the same host-keyed file.
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var ids []int64
	for i := 0; i < 3; i++ {
		b, err := env.st.Create(ctx, "t", fmt.Sprintf("%s/p%d", src.srv.URL, i))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, b.ID)
		env.svc.Enqueue(b.ID, b.URL)
	}
	path := ""
	for _, id := range ids {
		b := waitStatus(t, env.st.BookmarkStore, id, store.IconOK)
		if path == "" {
			path = b.IconPath
		} else if b.IconPath != path {
			t.Fatalf("bookmark %d icon = %q, want shared file %q", id, b.IconPath, path)
		}
	}
	if path == "" {
		t.Fatal("no icon path recorded")
	}
	if got := src.pageHits.Load(); got != 1 {
		t.Fatalf("page requests = %d, want 1 (fetches not coalesced)", got)
	}
	if got := src.iconHits.Load(); got != 1 {
		t.Fatalf("icon requests = %d, want 1", got)
	}
}

func TestServiceReusesCachedIcon(t *testing.T) {
	// A second bookmark on the same host, added after the first one's
	// fetch completed, adopts the cached file without any network.
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b1, err := env.st.Create(ctx, "t", src.srv.URL+"/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b1.ID, b1.URL)
	b1 = waitStatus(t, env.st.BookmarkStore, b1.ID, store.IconOK)

	b2, err := env.st.Create(ctx, "t", src.srv.URL+"/b")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b2.ID, b2.URL)
	b2 = waitStatus(t, env.st.BookmarkStore, b2.ID, store.IconOK)

	if b2.IconPath != b1.IconPath {
		t.Fatalf("b2 icon = %q, want reused %q", b2.IconPath, b1.IconPath)
	}
	if got := src.pageHits.Load(); got != 1 {
		t.Fatalf("page requests = %d, want 1 (sibling icon not reused)", got)
	}
}

func TestServiceDifferentHostsSeparateIcons(t *testing.T) {
	// "localhost" and "127.0.0.1" reach the same server but are
	// distinct host keys: each gets its own fetch and icon file.
	ips, err := net.LookupIP("localhost")
	v4 := false
	for _, ip := range ips {
		if ip.To4() != nil && ip.IsLoopback() {
			v4 = true
		}
	}
	if err != nil || !v4 {
		t.Skipf("localhost does not resolve to IPv4 loopback: %v", ips)
	}

	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	u, err := url.Parse(src.srv.URL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	localURL := "http://localhost:" + u.Port()

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b1, err := env.st.Create(ctx, "t", src.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b2, err := env.st.Create(ctx, "t", localURL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b1.ID, b1.URL)
	env.svc.Enqueue(b2.ID, b2.URL)

	b1 = waitStatus(t, env.st.BookmarkStore, b1.ID, store.IconOK)
	b2 = waitStatus(t, env.st.BookmarkStore, b2.ID, store.IconOK)
	if b1.IconPath == b2.IconPath {
		t.Fatalf("different hosts share icon file %q", b1.IconPath)
	}
	if got := src.pageHits.Load(); got != 2 {
		t.Fatalf("page requests = %d, want 2", got)
	}
}

func TestServiceStaleHostFileRefetches(t *testing.T) {
	// A cached host file older than iconReuseMaxAge is not adopted:
	// the second bookmark refetches (and rewrites the same host-keyed
	// file) — this is also the path for aged icons.
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b1, err := env.st.Create(ctx, "t", src.srv.URL+"/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b1.ID, b1.URL)
	b1 = waitStatus(t, env.st.BookmarkStore, b1.ID, store.IconOK)

	old := time.Now().Add(-iconReuseMaxAge - time.Hour)
	iconFile := filepath.Join(env.iconDir, b1.IconPath)
	if err := os.Chtimes(iconFile, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	b2, err := env.st.Create(ctx, "t", src.srv.URL+"/b")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b2.ID, b2.URL)
	b2 = waitStatus(t, env.st.BookmarkStore, b2.ID, store.IconOK)

	if got := src.pageHits.Load(); got != 2 {
		t.Fatalf("page requests = %d, want 2 (stale icon wrongly reused)", got)
	}
	if b2.IconPath != b1.IconPath {
		t.Fatalf("b2 icon = %q, want host-keyed %q", b2.IconPath, b1.IconPath)
	}
}

// TestServiceInvalidCachedIconNotReused: a host-keyed cache file whose
// bytes are not an icon (an HTML page once accepted under an image
// label) is deleted instead of adopted, and the icon is refetched.
func TestServiceInvalidCachedIconNotReused(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Plant the poisoned file before the first fetch for the host.
	badName := iconFileName(src.srv.URL, "ico")
	badPath := filepath.Join(env.iconDir, badName)
	if err := os.WriteFile(badPath,
		[]byte("<!DOCTYPE html><html>not an icon"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	b, err := env.st.Create(ctx, "t", src.srv.URL+"/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b.ID, b.URL)
	b = waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)

	// The cache entry was rejected: a real fetch ran and stored the PNG
	// under its own extension.
	if got := src.pageHits.Load(); got != 1 {
		t.Fatalf("page requests = %d, want 1 (invalid icon wrongly reused)", got)
	}
	if want := iconFileName(src.srv.URL, "png"); b.IconPath != want {
		t.Fatalf("icon = %q, want refetched %q", b.IconPath, want)
	}
	if _, err := os.Stat(badPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("poisoned file still present: %v", err)
	}
}

// TestServiceIconFileDropsCorrupt: serving a cache file that fails byte
// validation reports it missing, deletes the file, flips the bookmark
// back to pending and queues a refetch that heals it.
func TestServiceIconFileDropsCorrupt(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	b, err := env.st.Create(ctx, "t", env.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Plant a poisoned host-keyed file and mark the bookmark ok, as if
	// an older build had cached it.
	name := iconFileName(env.srv.URL, "ico")
	path := filepath.Join(env.iconDir, name)
	if err := os.WriteFile(path, []byte("<!DOCTYPE html>"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := env.st.SetIcon(ctx, b.ID, name, store.IconOK); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	b, err = env.st.BookmarkStore.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, _, err := env.svc.IconFile(ctx, b); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("IconFile err = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("corrupt file still present: %v", err)
	}

	// The refetch queued by IconFile heals the bookmark with the real
	// icon (a PNG under its own extension).
	b = waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)
	if want := iconFileName(env.srv.URL, "png"); b.IconPath != want {
		t.Fatalf("icon = %q, want refetched %q", b.IconPath, want)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("poisoned file restored: %v", err)
	}
}

func TestServiceMissingHostFileRefetches(t *testing.T) {
	// Reuse requires the host's file on disk; a status-ok row whose
	// file vanished does not count.
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b1, err := env.st.Create(ctx, "t", src.srv.URL+"/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b1.ID, b1.URL)
	b1 = waitStatus(t, env.st.BookmarkStore, b1.ID, store.IconOK)
	if err := os.Remove(filepath.Join(env.iconDir, b1.IconPath)); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	b2, err := env.st.Create(ctx, "t", src.srv.URL+"/b")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b2.ID, b2.URL)
	waitStatus(t, env.st.BookmarkStore, b2.ID, store.IconOK)

	if got := src.pageHits.Load(); got != 2 {
		t.Fatalf("page requests = %d, want 2 (missing file wrongly reused)", got)
	}
}

// TestServiceAdoptsHostFileAfterReplace: a replace-import wipes every
// bookmark row but leaves the host-keyed icon files, so a re-imported
// bookmark on the same host adopts the cached file — no sibling with
// status ok needed and no network request.
func TestServiceAdoptsHostFileAfterReplace(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b1, err := env.st.Create(ctx, "t", src.srv.URL+"/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b1.ID, b1.URL)
	b1 = waitStatus(t, env.st.BookmarkStore, b1.ID, store.IconOK)

	// ReplaceAll wipes the rows (the import replace path); the icon
	// file itself must survive.
	created, err := env.st.ReplaceAll(ctx,
		[]store.NewBookmark{{Title: "t", URL: src.srv.URL + "/b"}})
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.iconDir, b1.IconPath)); err != nil {
		t.Fatalf("icon file lost across replace: %v", err)
	}

	b2 := created[0]
	env.svc.Enqueue(b2.ID, b2.URL)
	b2 = waitStatus(t, env.st.BookmarkStore, b2.ID, store.IconOK)

	if b2.IconPath != b1.IconPath {
		t.Fatalf("b2 icon = %q, want adopted %q", b2.IconPath, b1.IconPath)
	}
	if got := src.pageHits.Load(); got != 1 {
		t.Fatalf("page requests = %d, want 1 (cached host file not reused)", got)
	}
	if got := src.iconHits.Load(); got != 1 {
		t.Fatalf("icon requests = %d, want 1", got)
	}
}

func TestServiceRefreshSoleBookmarkRefetches(t *testing.T) {
	// POST .../icon/refresh resets icon_path/status then calls
	// EnqueueForce. Even though a fresh host-keyed file is cached, the
	// forced job bypasses it and hits the network again.
	env := newTestEnv(t)
	ctx := context.Background()
	src := newCountingIconServer(t)

	if err := env.svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	b, err := env.st.Create(ctx, "t", src.srv.URL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.svc.Enqueue(b.ID, b.URL)
	b = waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)
	if got := src.pageHits.Load(); got != 1 {
		t.Fatalf("page requests = %d, want 1", got)
	}

	// Corrupt the cached file: a forced refetch must overwrite it with
	// the real icon again rather than adopt it.
	iconFile := filepath.Join(env.iconDir, b.IconPath)
	if err := os.WriteFile(iconFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Mirror internal/api's refreshIcon handler: reset, then
	// EnqueueForce.
	if err := env.st.SetIcon(ctx, b.ID, "", store.IconPending); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	env.svc.EnqueueForce(b.ID, b.URL)
	b = waitStatus(t, env.st.BookmarkStore, b.ID, store.IconOK)
	if got := src.pageHits.Load(); got != 2 {
		t.Fatalf("page requests = %d, want 2 (forced refresh did not refetch)", got)
	}
	raw, err := os.ReadFile(iconFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(raw, png1x1) {
		t.Fatal("cached host file not overwritten by forced refetch")
	}
}
