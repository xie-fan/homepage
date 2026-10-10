package favicon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Fetch limits (docs/PLAN.md): pages are parsed up to 1 MiB, icons are
// accepted up to 512 KiB, and at most 5 redirects are followed.
const (
	maxPageBytes = 1 << 20
	maxIconBytes = 512 << 10
	maxRedirects = 5
)

// ErrNoIcon is returned by Fetch when the page, its declared icons, the
// conventional /favicon.ico and the configured third-party fallback all
// yielded nothing usable.
var ErrNoIcon = errors.New("favicon: no icon found")

// defaultUserAgent mimics a browser so icon-hostile sites still serve
// real pages/icons instead of bot walls.
const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const (
	acceptPage  = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"
	acceptImage = "image/avif,image/webp,image/png,image/svg+xml,image/x-icon,image/*,*/*;q=0.8"
)

// Options configures a Fetcher.
type Options struct {
	// Timeout bounds a single HTTP request (including redirects).
	Timeout time.Duration
	// AllowPrivate disables the SSRF guard (for tests / trusted LANs).
	AllowPrivate bool
	// Fallback selects a third-party icon service tried last:
	// "" (off), "google" or "duckduckgo".
	Fallback string
	// Proxy is an optional explicit proxy URL for icon requests; nil
	// means connect directly. Environment proxy variables are never
	// honored: a proxy resolves the target hostname itself, so the SSRF
	// guard's per-dial IP check could no longer see the real
	// destination — with Proxy set, only the proxy's own address is
	// vetted.
	Proxy *url.URL
	// UserAgent overrides the default browser-like UA.
	UserAgent string
}

// Fetcher downloads icons over HTTP(S) through the guarded dialer.
type Fetcher struct {
	client    *http.Client
	transport *http.Transport
	opts      Options
}

// New builds a Fetcher. The underlying http.Client dials through the
// SSRF guard, follows at most 5 redirects, and refuses redirect targets
// that are not http/https. Requests go direct by default — never via
// HTTP(S)_PROXY env vars — unless Options.Proxy is set.
func New(opts Options) *Fetcher {
	if opts.Timeout <= 0 {
		opts.Timeout = 8 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	transport := &http.Transport{
		DialContext:           newDialer(opts.AllowPrivate, opts.Timeout).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if opts.Proxy != nil {
		// Opt-in only: the proxy does DNS + connect, so the guard then
		// sees just the proxy's address, not the target's.
		transport.Proxy = http.ProxyURL(opts.Proxy)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("favicon: too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("favicon: refusing redirect to scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
	return &Fetcher{client: client, transport: transport, opts: opts}
}

// Close releases idle connections held by the fetcher.
func (f *Fetcher) Close() {
	f.transport.CloseIdleConnections()
}

// Fetch finds and downloads the icon for pageURL, returning the icon
// bytes and a normalized content type. Order: <link> candidates parsed
// from the page, then <scheme>://<host>/apple-touch-icon.png and
// /favicon.ico, then the configured third-party fallback. ErrNoIcon
// means the site simply has no usable icon; any other error is a
// transport/guard failure worth reporting.
func (f *Fetcher) Fetch(ctx context.Context, pageURL string) (data []byte, contentType string, err error) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("favicon: invalid page URL %q", pageURL)
	}

	// One Fetch runs several requests (page, candidates, path
	// fallbacks, a third-party fallback), each bounded by the client
	// timeout. A total budget of a few per-request timeouts keeps a
	// worst-case site from stalling the worker beyond what the UI's
	// post-add polling can pick up.
	ctx, cancel := context.WithTimeout(ctx, 3*f.opts.Timeout)
	defer cancel()

	// The page fetch doubles as the candidate source and as the origin
	// for the path fallbacks (post-redirect, so the host is the one
	// that actually served the page). A non-2xx or non-HTML page yields
	// no candidates but never aborts the fetch — the fallbacks below
	// still run (e.g. stackoverflow.com 403s the page yet serves a real
	// /favicon.ico). A transport error is kept: when everything else
	// fails too it is a better answer than ErrNoIcon (e.g. the SSRF
	// guard's ErrBlockedIP).
	cands, effective, fetchErr := f.pageCandidates(ctx, u)
	if effective != nil {
		u = effective
	}
	for _, c := range cands {
		if data, ct, ok := f.tryCandidate(ctx, c); ok {
			return data, ct, nil
		}
	}

	for _, path := range []string{"/apple-touch-icon.png", "/favicon.ico"} {
		iconURL := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: path}
		if data, ct, ok := f.getIcon(ctx, iconURL.String()); ok {
			return data, ct, nil
		}
	}

	if fb := f.fallbackURL(u.Hostname()); fb != "" {
		if data, ct, ok := f.getIcon(ctx, fb); ok {
			return data, ct, nil
		}
	}
	if fetchErr != nil {
		return nil, "", fetchErr
	}
	return nil, "", ErrNoIcon
}

// pageCandidates GETs the page and, when it is HTML, parses its icon
// links. It returns the candidates (possibly none), the effective URL
// after redirects, and any transport-level error.
func (f *Fetcher) pageCandidates(ctx context.Context, u *url.URL) ([]Candidate, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	f.setHeaders(req, acceptPage)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("favicon: get page: %w", err)
	}
	defer resp.Body.Close()

	effective := resp.Request.URL
	if resp.StatusCode/100 != 2 {
		return nil, effective, nil
	}
	if mediaTypeOf(resp.Header) != "text/html" {
		return nil, effective, nil
	}
	cands := ParseIconLinks(effective, io.LimitReader(resp.Body, maxPageBytes))
	return cands, effective, nil
}

// tryCandidate fetches one parsed candidate; data: URIs decode locally.
func (f *Fetcher) tryCandidate(ctx context.Context, c Candidate) ([]byte, string, bool) {
	if strings.HasPrefix(c.URL, "data:") {
		data, ct, err := decodeDataURI(c.URL)
		if err != nil {
			return nil, "", false
		}
		if nct, ok := acceptIcon(data, ct); ok {
			return data, nct, true
		}
		return nil, "", false
	}
	return f.getIcon(ctx, c.URL)
}

// getIcon GETs one icon URL and validates the response: 2xx, ≤ 512 KiB,
// non-empty, image content type.
func (f *Fetcher) getIcon(ctx context.Context, rawURL string) ([]byte, string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", false
	}
	f.setHeaders(req, acceptImage)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", false
	}
	// Read one byte past the cap to detect oversize bodies.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIconBytes+1))
	if err != nil || len(data) > maxIconBytes {
		return nil, "", false
	}
	if ct, ok := acceptIcon(data, resp.Header.Get("Content-Type")); ok {
		return data, ct, true
	}
	return nil, "", false
}

// fallbackURL maps the configured fallback name to a third-party icon
// URL for host; "" means no fallback.
func (f *Fetcher) fallbackURL(host string) string {
	switch f.opts.Fallback {
	case "google":
		return "https://www.google.com/s2/favicons?domain=" +
			url.QueryEscape(host) + "&sz=64"
	case "duckduckgo":
		return "https://icons.duckduckgo.com/ip3/" +
			url.PathEscape(host) + ".ico"
	default:
		return ""
	}
}

// setHeaders applies the browser-like headers every outgoing request
// carries.
func (f *Fetcher) setHeaders(req *http.Request, accept string) {
	req.Header.Set("User-Agent", f.opts.UserAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
}

// acceptIcon validates a downloaded body as an icon and returns the
// content type to store for it. The declared type wins when it is an
// image. A declared (or sniffed) HTML type is rejected outright — a
// page is never an icon, and checking before the SVG probe keeps
// markup with an early inline "<svg" (e.g. an SPA shell served for
// /favicon.ico) from being adopted. Generic types
// (application/octet-stream, binary/octet-stream, missing) fall
// through to byte sniffing; SVG sniffs as text, so a literal "<svg" in
// the first KiB is accepted as image/svg+xml last.
func acceptIcon(data []byte, headerCT string) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	declared := mediaType(headerCT)
	if strings.HasPrefix(declared, "image/") {
		return declared, true
	}
	if isHTMLMediaType(declared) {
		return "", false
	}
	if ct, ok := sniffImageType(data); ok {
		return ct, true
	}
	sniffed := mediaType(http.DetectContentType(data))
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed, true
	}
	if isHTMLMediaType(sniffed) {
		return "", false
	}
	if looksLikeSVG(data) {
		return "image/svg+xml", true
	}
	return "", false
}

// isHTMLMediaType reports whether mt marks an (X)HTML document.
func isHTMLMediaType(mt string) bool {
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// sniffImageType recognizes icon encodings by magic bytes. It exists
// because http.DetectContentType has no ICO signature — and servers
// frequently label .ico as application/octet-stream — and to keep the
// common types independent of DetectContentType's ordering.
func sniffImageType(data []byte) (string, bool) {
	switch {
	case len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 &&
		data[2] == 0x01 && data[3] == 0x00:
		return "image/x-icon", true
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "image/gif", true
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg", true
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) &&
		bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	return "", false
}

// mediaType parses a Content-Type value down to its bare type/subtype;
// unparsable input degrades to the part before any ';'.
func mediaType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt, _, _ = strings.Cut(ct, ";")
	}
	return strings.ToLower(strings.TrimSpace(mt))
}

// mediaTypeOf is mediaType for a response's Content-Type header.
func mediaTypeOf(h http.Header) string {
	return mediaType(h.Get("Content-Type"))
}

// looksLikeSVG reports whether data plausibly starts an SVG document.
func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, []byte("<svg"))
}

// decodeDataURI decodes "data:[<mediatype>][;base64],<payload>" under
// the icon size cap. Percent-encoded payloads and both padded and raw
// base64 are supported.
func decodeDataURI(uri string) (data []byte, mediaType string, err error) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return nil, "", errors.New("favicon: not a data URI")
	}
	meta, payload, found := strings.Cut(rest, ",")
	if !found {
		return nil, "", errors.New("favicon: malformed data URI")
	}
	// A payload can't decode smaller than 3/4 of its base64 length, so
	// reject early when it is already over the cap.
	if len(payload) > maxIconBytes*2 {
		return nil, "", errors.New("favicon: data URI too large")
	}

	parts := strings.Split(meta, ";")
	if strings.Contains(parts[0], "/") {
		mediaType = strings.ToLower(parts[0])
		parts = parts[1:]
	}
	isB64 := false
	for _, p := range parts {
		if strings.EqualFold(p, "base64") {
			isB64 = true
		}
	}

	if isB64 {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(payload)
		}
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return nil, "", fmt.Errorf("favicon: data URI decode: %w", err)
	}
	if len(data) == 0 || len(data) > maxIconBytes {
		return nil, "", errors.New("favicon: data URI payload out of size limits")
	}
	return data, mediaType, nil
}
