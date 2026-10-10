// Package config loads environment-based configuration for the homepage
// server. See docs/PLAN.md for the authoritative list of variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
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
	// FaviconProxy is an optional proxy URL for icon fetches; nil means
	// connect directly (HOMEPAGE_FAVICON_PROXY). With a proxy the SSRF
	// guard can only vet the proxy's own address — the target host is
	// resolved and reached by the proxy.
	FaviconProxy *url.URL
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
	if v := os.Getenv("HOMEPAGE_FAVICON_PROXY"); v != "" {
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" || !supportedProxyScheme(u.Scheme) {
			errs = append(errs, fmt.Errorf("HOMEPAGE_FAVICON_PROXY: invalid proxy URL %q (want http, https, socks5 or socks5h)", v))
		} else {
			cfg.FaviconProxy = u
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

// supportedProxyScheme limits proxy URLs to the schemes net/http's
// Transport can actually speak.
func supportedProxyScheme(s string) bool {
	switch s {
	case "http", "https", "socks5", "socks5h":
		return true
	}
	return false
}
