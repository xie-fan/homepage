package favicon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// png1x1 is the smallest useful PNG (1x1, transparent).
var png1x1 = mustB64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

var icoBytes = []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}

func mustB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// newFetcher builds a Fetcher that can reach httptest's loopback.
func newFetcher(opts Options) *Fetcher {
	opts.AllowPrivate = true
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	return New(opts)
}

// serveIcon replies with body and content type on every request.
func serveIcon(t *testing.T, mux *http.ServeMux, path string, ct string, body []byte) {
	t.Helper()
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(body)
	})
}

func TestFetchLinkIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><link rel="icon" href="/i.png"></head><body></body></html>`))
	})
	serveIcon(t, mux, "/i.png", "image/png", png1x1)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/png" || !bytes.Equal(data, png1x1) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

func TestFetchFallbackICO(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>x</title></head><body></body></html>`))
	})
	serveIcon(t, mux, "/favicon.ico", "image/x-icon", icoBytes)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL+"/some/page")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/x-icon" || !bytes.Equal(data, icoBytes) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

func TestFetchOversizedIconRejected(t *testing.T) {
	big := make([]byte, maxIconBytes+1)
	copy(big, png1x1) // still sniffs as PNG; size is what disqualifies it
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<link rel="icon" href="/big.png">`))
	})
	serveIcon(t, mux, "/big.png", "image/png", big)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoIcon) {
		t.Fatalf("err = %v, want ErrNoIcon", err)
	}
}

func TestFetchNonImageRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<link rel="icon" href="/not.png">`))
	})
	serveIcon(t, mux, "/not.png", "text/plain", []byte("this is not an image"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoIcon) {
		t.Fatalf("err = %v, want ErrNoIcon", err)
	}
}

func TestFetchErrNoIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>nothing</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, _, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrNoIcon) {
		t.Fatalf("err = %v, want ErrNoIcon", err)
	}
}

func TestFetchRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/real", http.StatusFound)
	})
	mux.HandleFunc("/real", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<head><link rel="icon" href="icon.png"></head>`))
	})
	serveIcon(t, mux, "/icon.png", "image/png", png1x1) // resolved vs /real
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/png" || !bytes.Equal(data, png1x1) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

func TestFetchSVGSniffed(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<link rel="icon" href="/icon.svg" type="image/svg+xml">`))
	})
	// Served as text/xml: sniffs as text, accepted via the "<svg" probe.
	serveIcon(t, mux, "/icon.svg", "text/xml", svg)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/svg+xml" || !bytes.Equal(data, svg) {
		t.Fatalf("got ct=%q body=%q", ct, data)
	}
}

func TestFetchDataURI(t *testing.T) {
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1x1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<link rel="icon" href="` + uri + `">`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/png" || !bytes.Equal(data, png1x1) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

// nonLoopbackListener returns a TCP listener bound to a non-loopback
// IPv4 interface. ProxyFromEnvironment exempts loopback targets, so a
// test needs a LAN-ish address to observe whether *_PROXY env vars are
// honored at all.
func nonLoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("InterfaceAddrs: %v", err)
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipn.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || !ip4.IsGlobalUnicast() {
			continue
		}
		if ln, err := net.Listen("tcp4", net.JoinHostPort(ip4.String(), "0")); err == nil {
			return ln
		}
	}
	t.Skip("no usable non-loopback IPv4 interface")
	return nil
}

func TestFetchIgnoresProxyEnv(t *testing.T) {
	// A dead proxy for both schemes: if the fetcher honored *_PROXY
	// env vars, the request below would be routed through it and fail.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	f := newFetcher(Options{})
	defer f.Close()
	if f.transport.Proxy != nil {
		t.Fatal("default fetcher is not direct: transport.Proxy set")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<link rel="icon" href="/i.png">`))
	})
	serveIcon(t, mux, "/i.png", "image/png", png1x1)
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = nonLoopbackListener(t)
	srv.URL = "" // let Start derive it from the new listener
	srv.Start()
	defer srv.Close()

	data, ct, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/png" || !bytes.Equal(data, png1x1) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

func TestFetchViaConfiguredProxy(t *testing.T) {
	var hits atomic.Int32
	// A fake forward proxy: every proxied plain-http GET lands here.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !r.URL.IsAbs() {
			t.Errorf("proxied request not in absolute-URI form: %q", r.URL)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	}))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	f := newFetcher(Options{Proxy: proxyURL})
	defer f.Close()

	// The page fetch returns image/png (no links), so the fetcher falls
	// through to /favicon.ico — also via the proxy.
	data, ct, err := f.Fetch(context.Background(), "http://icon.example/")
	if err != nil {
		t.Fatalf("Fetch via proxy: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("no request reached the proxy")
	}
	if ct != "image/png" || !bytes.Equal(data, png1x1) {
		t.Fatalf("got ct=%q %d bytes", ct, len(data))
	}
}

func TestFetchBlockedIP(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	// The same httptest URL that works with AllowPrivate is refused by
	// the SSRF guard without it: 127.0.0.1 is blocked.
	f := New(Options{Timeout: 5 * time.Second})
	_, _, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrBlockedIP) {
		t.Fatalf("err = %v, want ErrBlockedIP", err)
	}
}

func TestFetchPageNotHTML(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1}`))
	})
	serveIcon(t, mux, "/favicon.ico", "image/x-icon", icoBytes)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No link parsing on non-HTML, but /favicon.ico still resolves.
	data, ct, err := newFetcher(Options{}).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ct != "image/x-icon" || !bytes.Equal(data, icoBytes) {
		t.Fatalf("got ct=%q", ct)
	}
}

func TestFallbackURL(t *testing.T) {
	f := New(Options{Fallback: "google"})
	if got := f.fallbackURL("ex ample.com"); !strings.Contains(got, "domain=ex+ample.com") ||
		!strings.Contains(got, "sz=64") {
		t.Errorf("google fallback = %q", got)
	}
	f = New(Options{Fallback: "duckduckgo"})
	if got := f.fallbackURL("example.com"); got != "https://icons.duckduckgo.com/ip3/example.com.ico" {
		t.Errorf("duckduckgo fallback = %q", got)
	}
	f = New(Options{})
	if got := f.fallbackURL("example.com"); got != "" {
		t.Errorf("empty fallback = %q", got)
	}
}
