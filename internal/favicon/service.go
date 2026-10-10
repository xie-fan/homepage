package favicon

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"homepage/internal/store"
)

// Worker pool sizing: a handful of concurrent fetches keeps icon
// resolution quick after bulk imports without hammering the network;
// the queue holds a backlog larger than a reasonable import burst.
const (
	numWorkers = 4
	queueSize  = 256
)

// iconReuseMaxAge bounds how old a sibling's cached icon file may be to
// be adopted instead of refetching. The API has a single Enqueue entry
// point for both ordinary fetches and explicit "refresh icon" requests
// (the refresh handler resets the icon to empty/pending first, so
// a refresh looks exactly like a normal pending job by the time it is
// dequeued — no force flag can reach the worker). Bounding reuse by
// file age is what keeps refresh meaningful: a fresh sibling icon is
// adopted, anything older is refetched. And because reuse needs a
// different bookmark with status ok, refreshing the only bookmark on a
// host always hits the network.
const iconReuseMaxAge = 7 * 24 * time.Hour

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
	// flights holds one in-flight fetch per host (hostKey): jobs for
	// the same host join the running flight and share its outcome
	// instead of fetching in parallel.
	flights map[string]*hostFlight
}

// fetchResult is the outcome of one icon fetch, shared with every
// bookmark that joined the same per-host flight.
type fetchResult struct {
	name   string // cached icon file name; "" unless status is ok
	status string // store.IconOK / IconNone / IconError
}

// hostFlight coordinates the fetch for one host: followers wait on
// done and read res once the leader publishes it via finishFlight.
type hostFlight struct {
	done chan struct{}
	res  fetchResult
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
		flights:  make(map[string]*hostFlight),
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

// process resolves the icon for one bookmark: it first tries to adopt
// a sibling bookmark's fresh cached icon for the same host (icon files
// are host-keyed, so the sibling's file is the exact name a fetch here
// would write anyway), otherwise it joins — or leads — the per-host
// fetch flight.
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

	host := hostKey(b.URL)
	if host == "" {
		// Shouldn't happen (the API validates absolute http/https
		// URLs); without a host key there is nothing to dedupe on.
		s.commitIfCurrent(ctx, id, pageURL, s.fetchAndStore(ctx, id, b.URL))
		return
	}
	if name, ok := s.reusableIcon(ctx, id, host); ok {
		s.setIcon(id, name, store.IconOK)
		return
	}

	fl, leader := s.flightFor(host)
	if !leader {
		select {
		case <-fl.done:
		case <-s.ctx.Done():
			return
		}
		s.commitIfCurrent(ctx, id, pageURL, fl.res)
		return
	}
	// The reuse check runs again inside the flight: a previous flight
	// for this host may have committed between our first check and
	// flight registration.
	var res fetchResult
	if name, ok := s.reusableIcon(ctx, id, host); ok {
		res = fetchResult{name: name, status: store.IconOK}
	} else {
		res = s.fetchAndStore(ctx, id, b.URL)
	}
	// Commit before publishing: a job that misses the flight and leads
	// the next one must already find this icon in its reuse check.
	s.commitIfCurrent(ctx, id, pageURL, res)
	s.finishFlight(host, fl, res)
}

// reusableIcon returns the icon file name a different bookmark on the
// same host already fetched — status ok, the file on disk, written less
// than iconReuseMaxAge ago. Reporting false sends the caller to the
// network. A stale sibling file (older than the cap) is not adopted, so
// an aged icon — or an explicit refresh — still refetches.
func (s *Service) reusableIcon(ctx context.Context, id int64, host string) (string, bool) {
	bs, err := s.store.List(ctx)
	if err != nil {
		return "", false
	}
	for _, b := range bs {
		if b.ID == id || b.IconStatus != store.IconOK || b.IconPath == "" {
			continue
		}
		if hostKey(b.URL) != host {
			continue
		}
		name := filepath.Base(b.IconPath)
		st, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil || time.Since(st.ModTime()) > iconReuseMaxAge {
			continue
		}
		return name, true
	}
	return "", false
}

// flightFor registers on the flight for host, reporting whether the
// caller leads (runs the fetch). Followers wait on fl.done and read
// fl.res once the leader publishes it via finishFlight.
func (s *Service) flightFor(host string) (fl *hostFlight, leader bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fl, ok := s.flights[host]; ok {
		return fl, false
	}
	fl = &hostFlight{done: make(chan struct{})}
	s.flights[host] = fl
	return fl, true
}

// finishFlight publishes res to waiting followers and frees the host
// for a future flight.
func (s *Service) finishFlight(host string, fl *hostFlight, res fetchResult) {
	if res.status == "" {
		res.status = store.IconError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fl.res = res
	delete(s.flights, host)
	close(fl.done)
}

// fetchAndStore downloads the icon for pageURL and writes the
// host-keyed cache file, reporting the outcome for the caller — and
// any flight followers — to record on their own bookmarks.
func (s *Service) fetchAndStore(ctx context.Context, id int64, pageURL string) fetchResult {
	data, ct, err := s.fetch.Fetch(ctx, pageURL)
	switch {
	case errors.Is(err, ErrNoIcon):
		return fetchResult{status: store.IconNone}
	case err != nil:
		if errors.Is(err, ErrBlockedIP) {
			s.logger.Info("icon fetch failed",
				"id", id, "url", pageURL,
				"reason", "blocked private address",
				"hint", "set HOMEPAGE_FAVICON_ALLOW_PRIVATE=true for LAN sites",
				"err", err)
		} else {
			s.logger.Warn("icon fetch failed", "id", id, "url", pageURL, "err", err)
		}
		return fetchResult{status: store.IconError}
	}

	name := iconFileName(pageURL, extForContentType(ct))
	if err := writeFileAtomic(s.dir, name, data); err != nil {
		s.logger.Warn("icon write failed", "id", id, "err", err)
		return fetchResult{status: store.IconError}
	}
	return fetchResult{name: name, status: store.IconOK}
}

// commitIfCurrent records res on the bookmark, but only when it still
// points at the URL the job ran for — an edit or delete mid-flight
// must not be overwritten.
func (s *Service) commitIfCurrent(ctx context.Context, id int64, pageURL string, res fetchResult) {
	b, err := s.store.Get(ctx, id)
	if err != nil || b.URL != pageURL {
		return
	}
	s.setIcon(id, res.name, res.status)
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
