package api

import (
	"errors"
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
// an empty title defaults to the URL host. Returns the cleaned values.
func validateBookmarkInput(title, rawURL string) (string, string, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return "", "", errors.New("url is required")
	}
	if len(u) > maxURLLen {
		return "", "", fmt.Errorf("url too long: %d chars (max %d)", len(u), maxURLLen)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", "", fmt.Errorf("invalid url: %v", err)
	}
	// url.Parse lowercases the scheme, so this accepts e.g. "HTTPS://".
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("url scheme %q: want http or https", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", "", errors.New("url must not contain user info")
	}
	if parsed.Host == "" {
		return "", "", errors.New("url must have a host")
	}

	title = strings.TrimSpace(title)
	if title == "" {
		title = parsed.Host
	}
	if n := utf8.RuneCountInString(title); n > maxTitleRunes {
		return "", "", fmt.Errorf("title too long: %d runes (max %d)", n, maxTitleRunes)
	}
	return title, u, nil
}
