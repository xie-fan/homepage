package favicon

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"homepage/internal/store"
)

// Worker pool sizing: a handful of concurrent fetches keeps icon
// resolution quick after bulk imports without hammering the network;
// the queue holds a backlog larger than a reasonable import burst.
const (
	numWorkers = 4
	queueSize  = 256
)

// Service is the async icon-fetching worker behind api.IconService.
// Enqueue is non-blocking; a small pool of workers resolves queued
// bookmarks, writes icon files under <DataDir>/icons, and records the
// outcome on the bookmark.
type Service struct {
	store  store.BookmarkStore
	fetch  *Fetcher
	dir    string
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	done   sync.Once

	queue chan int64
	// inflight maps a queued/running bookmark id to the newest URL it
	// was enqueued with, deduplicating repeat enqueues.
	mu       sync.Mutex
	inflight map[int64]string
}

// NewService creates the icon cache directory under dataDir and returns
// a Service ready to Start. A nil logger falls back to slog.Default.
func NewService(bs store.BookmarkStore, f *Fetcher, dataDir string, logger *slog.Logger) (*Service, error) {
	dir := filepath.Join(dataDir, "icons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store:    bs,
		fetch:    f,
		dir:      dir,
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		queue:    make(chan int64, queueSize),
		inflight: make(map[int64]string),
	}, nil
}

// Start launches the worker pool, then re-enqueues every bookmark still
// marked pending so icon fetches interrupted by a restart resume.
func (s *Service) Start(ctx context.Context) error {
	for i := 0; i < numWorkers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	bs, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, b := range bs {
		if b.IconStatus == store.IconPending {
			s.Enqueue(b.ID, b.URL)
			n++
		}
	}
	if n > 0 {
		s.logger.Info("re-enqueued pending icon fetches", "count", n)
	}
	return nil
}

// Enqueue queues an icon fetch for the bookmark without blocking.
// Re-enqueues of an in-flight id coalesce to the newest URL; when the
// queue is full the fetch is dropped and logged.
func (s *Service) Enqueue(id int64, pageURL string) {
	s.mu.Lock()
	if _, dup := s.inflight[id]; dup {
		s.inflight[id] = pageURL
		s.mu.Unlock()
		return
	}
	s.inflight[id] = pageURL
	s.mu.Unlock()

	select {
	case <-s.ctx.Done():
		s.clearInflight(id)
		return
	default:
	}
	select {
	case s.queue <- id:
	case <-s.ctx.Done():
		s.clearInflight(id)
	default:
		s.clearInflight(id)
		s.logger.Warn("icon queue full; dropping fetch", "id", id)
	}
}

// IconFile returns the cached icon file for b plus its content type, or
// fs.ErrNotExist when nothing is cached. The stored name is reduced to
// its basename before joining so a corrupt icon_path cannot escape the
// icons dir.
func (s *Service) IconFile(_ context.Context, b store.Bookmark) (string, string, error) {
	if b.IconPath == "" {
		return "", "", fs.ErrNotExist
	}
	path := filepath.Join(s.dir, filepath.Base(b.IconPath))
	if _, err := os.Stat(path); err != nil {
		return "", "", err
	}
	return path, contentTypeForExt(filepath.Ext(b.IconPath)), nil
}

// Close stops accepting work, cancels in-flight fetches and waits for
// the workers to drain. It is idempotent.
func (s *Service) Close() error {
	s.done.Do(func() {
		s.cancel()
		s.fetch.Close()
		s.wg.Wait()
	})
	return nil
}

// worker pops bookmark ids until shutdown.
func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.mu.Lock()
			url := s.inflight[id]
			delete(s.inflight, id)
			s.mu.Unlock()
			s.process(id, url)
		}
	}
}

// process fetches and stores the icon for one bookmark.
func (s *Service) process(id int64, pageURL string) {
	ctx := s.ctx
	b, err := s.store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Warn("icon job: load bookmark", "id", id, "err", err)
		}
		return // deleted or unreadable: nothing to do
	}
	if b.URL != pageURL {
		return // URL changed since enqueue; a newer job owns the fetch
	}

	data, ct, err := s.fetch.Fetch(ctx, b.URL)
	switch {
	case errors.Is(err, ErrNoIcon):
		s.setIcon(id, "", store.IconNone)
		return
	case err != nil:
		if errors.Is(err, ErrBlockedIP) {
			s.logger.Info("icon fetch failed",
				"id", id, "url", b.URL,
				"reason", "blocked private address",
				"hint", "set HOMEPAGE_FAVICON_ALLOW_PRIVATE=true for LAN sites",
				"err", err)
		} else {
			s.logger.Warn("icon fetch failed", "id", id, "url", b.URL, "err", err)
		}
		s.setIcon(id, "", store.IconError)
		return
	}

	// The bookmark may have been edited or deleted mid-fetch; only
	// commit when it still points at the URL we fetched.
	b, err = s.store.Get(ctx, id)
	if err != nil || b.URL != pageURL {
		return
	}
	name := iconFileName(b.URL, extForContentType(ct))
	if err := writeFileAtomic(s.dir, name, data); err != nil {
		s.logger.Warn("icon write failed", "id", id, "err", err)
		s.setIcon(id, "", store.IconError)
		return
	}
	s.setIcon(id, name, store.IconOK)
}

// setIcon records the fetch outcome, swallowing the store error (a
// deleted bookmark is the common cause and is harmless).
func (s *Service) setIcon(id int64, name, status string) {
	if err := s.store.SetIcon(s.ctx, id, name, status); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		s.logger.Warn("icon job: save result", "id", id, "err", err)
	}
}

// clearInflight drops id from the dedupe map.
func (s *Service) clearInflight(id int64) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}
