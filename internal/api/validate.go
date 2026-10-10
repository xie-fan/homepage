package api

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Validation bounds per docs/PLAN.md.
const (
	maxURLLen     = 2048 // chars
	maxTitleRunes = 200
)

// validateBookmarkInput normalizes and validates a bookmark title/URL
// pair: the URL must be absolute http(s), have a host and no userinfo;
// an empty title defaults to the URL host. Returns the cleaned values,
// or an *apiError naming the bad field.
func validateBookmarkInput(title, rawURL string) (string, string, *apiError) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return "", "", badInput(codeURLRequired, "url", "url is required")
	}
	if len(u) > maxURLLen {
		return "", "", badInput(codeURLTooLong, "url",
			fmt.Sprintf("url too long: %d chars (max %d)", len(u), maxURLLen))
	}
	parsed, err := url.Parse(u)
	if err != nil {
		// Raw url.Parse errors expose parser internals; the client only
		// ever sees a fixed message.
		return "", "", badInput(codeURLInvalid, "url", "invalid URL")
	}
	// url.Parse lowercases the scheme, so this accepts e.g. "HTTPS://".
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", badInput(codeURLScheme, "url",
			fmt.Sprintf("url scheme %q: want http or https", parsed.Scheme))
	}
	if parsed.User != nil {
		return "", "", badInput(codeURLUserinfo, "url", "url must not contain user info")
	}
	if parsed.Host == "" {
		return "", "", badInput(codeURLInvalid, "url", "url must have a host")
	}

	title = strings.TrimSpace(title)
	if title == "" {
		title = parsed.Host
	}
	if n := utf8.RuneCountInString(title); n > maxTitleRunes {
		return "", "", badInput(codeTitleTooLong, "title",
			fmt.Sprintf("title too long: %d runes (max %d)", n, maxTitleRunes))
	}
	return title, u, nil
}

// NormalizeURL reduces a URL to the canonical form used for duplicate
// detection: scheme and host lowercased, the scheme's default port
// dropped (80 for http, 443 for https), an empty path treated as "/",
// and the fragment removed. Path, query and non-default ports are kept
// as-is, so e.g. case differences in the path still distinguish URLs.
// Returns "" when rawURL cannot be parsed.
func NormalizeURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // re-bracket an IPv6 literal
	}
	if port := parsed.Port(); port != "" && !defaultPort(parsed.Scheme, port) {
		host += ":" + port
	}
	parsed.Host = host
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

// defaultPort reports whether port is the implied port for scheme.
func defaultPort(scheme, port string) bool {
	return scheme == "http" && port == "80" || scheme == "https" && port == "443"
}
