package favicon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
