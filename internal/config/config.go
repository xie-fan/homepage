// Package config loads environment-based configuration for the homepage
// server. See docs/PLAN.md for the authoritative list of variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the service configuration.
type Config struct {
	// Addr is the HTTP listen address (HOMEPAGE_ADDR).
	Addr string
	// DataDir holds the SQLite DB and the icon cache (HOMEPAGE_DATA_DIR).
	DataDir string
	// FaviconAllowPrivate permits fetching icons from private/loopback IPs
	// (HOMEPAGE_FAVICON_ALLOW_PRIVATE).
	FaviconAllowPrivate bool
	// FaviconFallback selects an optional third-party icon fallback:
	// "" (off), "google" or "duckduckgo" (HOMEPAGE_FAVICON_FALLBACK).
	FaviconFallback string
	// FaviconTimeout is the per-request timeout for icon fetching
	// (HOMEPAGE_FAVICON_TIMEOUT).
	FaviconTimeout time.Duration
}

// Load returns a Config populated from HOMEPAGE_* environment variables,
// falling back to documented defaults. Empty values are treated as unset.
// Invalid values are reported together as a single joined error naming the
// offending variables.
func Load() (Config, error) {
	cfg := Config{
		Addr:           ":8080",
		DataDir:        "./data",
		FaviconTimeout: 8 * time.Second,
	}
	var errs []error

	if v := os.Getenv("HOMEPAGE_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("HOMEPAGE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("HOMEPAGE_FAVICON_ALLOW_PRIVATE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("HOMEPAGE_FAVICON_ALLOW_PRIVATE: %w", err))
		} else {
			cfg.FaviconAllowPrivate = b
		}
	}
	if v := os.Getenv("HOMEPAGE_FAVICON_FALLBACK"); v != "" {
		switch v {
		case "google", "duckduckgo":
			cfg.FaviconFallback = v
		default:
			errs = append(errs, fmt.Errorf("HOMEPAGE_FAVICON_FALLBACK: unsupported value %q (want \"google\" or \"duckduckgo\")", v))
		}
	}
	if v := os.Getenv("HOMEPAGE_FAVICON_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("HOMEPAGE_FAVICON_TIMEOUT: invalid duration %q", v))
		} else {
			cfg.FaviconTimeout = d
		}
	}
	return cfg, errors.Join(errs...)
}
