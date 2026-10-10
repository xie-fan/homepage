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
	"HOMEPAGE_FAVICON_PROXY",
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

func TestLoadFaviconProxy(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.FaviconProxy != nil {
		t.Fatalf("default FaviconProxy = %v, want nil (direct)", cfg.FaviconProxy)
	}

	for _, v := range []string{
		"http://proxy.local:3128",
		"https://user:pw@proxy.local:3128",
		"socks5://127.0.0.1:1080",
		"socks5h://127.0.0.1:1080",
	} {
		t.Setenv("HOMEPAGE_FAVICON_PROXY", v)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() with proxy %q error: %v", v, err)
		}
		if cfg.FaviconProxy == nil || cfg.FaviconProxy.String() != v {
			t.Fatalf("proxy %q: FaviconProxy = %v", v, cfg.FaviconProxy)
		}
	}
}

func TestLoadFaviconProxyInvalid(t *testing.T) {
	clearEnv(t)
	for _, v := range []string{
		"not a url",
		"proxy.local:3128",     // missing scheme
		"http://",              // missing host
		"ftp://proxy.local:21", // unsupported scheme
		"http://[::1",          // malformed
	} {
		t.Setenv("HOMEPAGE_FAVICON_PROXY", v)
		_, err := Load()
		if err == nil {
			t.Errorf("Load() with proxy %q error = nil, want non-nil", v)
		} else if !strings.Contains(err.Error(), "HOMEPAGE_FAVICON_PROXY") {
			t.Errorf("error %q does not mention HOMEPAGE_FAVICON_PROXY", err)
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
