package config

import (
	"strings"
	"testing"
	"time"
)

var envKeys = []string{
	"HOMEPAGE_ADDR",
	"HOMEPAGE_DATA_DIR",
	"HOMEPAGE_FAVICON_ALLOW_PRIVATE",
	"HOMEPAGE_FAVICON_FALLBACK",
	"HOMEPAGE_FAVICON_TIMEOUT",
}

// clearEnv neutralizes all HOMEPAGE_* variables so tests are hermetic
// (Load treats empty as unset).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := Config{
		Addr:           ":8080",
		DataDir:        "./data",
		FaviconTimeout: 8 * time.Second,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOMEPAGE_ADDR", "127.0.0.1:9090")
	t.Setenv("HOMEPAGE_DATA_DIR", "/tmp/hp")
	t.Setenv("HOMEPAGE_FAVICON_ALLOW_PRIVATE", "true")
	t.Setenv("HOMEPAGE_FAVICON_FALLBACK", "duckduckgo")
	t.Setenv("HOMEPAGE_FAVICON_TIMEOUT", "2500ms")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := Config{
		Addr:                "127.0.0.1:9090",
		DataDir:             "/tmp/hp",
		FaviconAllowPrivate: true,
		FaviconFallback:     "duckduckgo",
		FaviconTimeout:      2500 * time.Millisecond,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOMEPAGE_FAVICON_ALLOW_PRIVATE", "yes-please")
	t.Setenv("HOMEPAGE_FAVICON_FALLBACK", "bing")
	t.Setenv("HOMEPAGE_FAVICON_TIMEOUT", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
	for _, k := range []string{
		"HOMEPAGE_FAVICON_ALLOW_PRIVATE",
		"HOMEPAGE_FAVICON_FALLBACK",
		"HOMEPAGE_FAVICON_TIMEOUT",
	} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error %q does not mention %s", err, k)
		}
	}
}

func TestLoadNonPositiveTimeout(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOMEPAGE_FAVICON_TIMEOUT", "0s")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want non-nil for non-positive timeout")
	}
}
