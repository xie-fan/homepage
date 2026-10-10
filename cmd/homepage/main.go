// Command homepage runs the self-hosted personal homepage server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"homepage/internal/api"
	"homepage/internal/config"
	"homepage/internal/db"
	"homepage/internal/favicon"
	"homepage/internal/store"
)

// shutdownTimeout bounds graceful shutdown on SIGINT/SIGTERM.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %q: %w", cfg.DataDir, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := filepath.Join(cfg.DataDir, "homepage.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", dbPath, err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("close database", "err", err)
		}
	}()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	st := store.NewSQLite(sqlDB)
	fetcher := favicon.New(favicon.Options{
		Timeout:      cfg.FaviconTimeout,
		AllowPrivate: cfg.FaviconAllowPrivate,
		Fallback:     cfg.FaviconFallback,
	})
	icons, err := favicon.NewService(st.BookmarkStore, fetcher, cfg.DataDir, slog.Default())
	if err != nil {
		return fmt.Errorf("init icon service: %w", err)
	}
	if err := icons.Start(ctx); err != nil {
		return fmt.Errorf("start icon service: %w", err)
	}
	// Closed before the DB (defer LIFO) so workers can save results.
	defer func() {
		if err := icons.Close(); err != nil {
			slog.Warn("close icon service", "err", err)
		}
	}()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: api.NewServer(api.Deps{
			Store:  st,
			Icons:  icons,
			Logger: slog.Default(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "dataDir", cfg.DataDir)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		// ListenAndServe failed on its own (e.g. address in use).
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		// Drain the listener error; ErrServerClosed is the expected result.
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
