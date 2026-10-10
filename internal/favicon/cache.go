package favicon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// hostKey is the per-host dedupe key for a page URL: the exact
// lowercased host name (no port, no www.-normalizing so icons stay
// correct for genuinely different hosts). Bookmarks sharing a hostKey
// share one icon file and at most one in-flight fetch.
func hostKey(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostFilePrefix is the name stem every icon file for a host shares:
// <sha256(host)[:32]>. All cached files for the host match
// "<prefix>.*" in the icons dir.
func hostFilePrefix(host string) string {
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:])[:32]
}

// iconFileName derives the cache file name for a page URL:
// <sha256(host)[:32]>.<ext>. Host-keyed names deduplicate icons across
// bookmarks that point at the same site.
func iconFileName(pageURL, ext string) string {
	return hostFilePrefix(hostKey(pageURL)) + "." + ext
}

// extForContentType maps an icon content type to a file extension.
// Unknown image/* subtypes fall through to their subtype name.
func extForContentType(ct string) string {
	switch mediaType(ct) {
	case "image/x-icon", "image/vnd.microsoft.icon":
		return "ico"
	case "image/png":
		return "png"
	case "image/svg+xml":
		return "svg"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	}
	if sub, ok := strings.CutPrefix(mediaType(ct), "image/"); ok && isNameChar(sub) {
		return sub
	}
	return "ico"
}

// iconHeadBytes is how much of a cached icon file is read to validate
// its format: the magic signatures need a dozen bytes and the SVG probe
// a KiB past any prolog/comments, so a few KiB of head is ample.
const iconHeadBytes = 8 << 10

// validateIconFile reads the leading bytes of the cached icon at path
// and reports its detected content type — the on-disk counterpart of
// acceptIcon. A file that fails validation is not an icon.
func validateIconFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, iconHeadBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", false
	}
	return acceptIcon(head[:n])
}

// isNameChar reports whether s is a safe file-extension body.
func isNameChar(s string) bool {
	if s == "" || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// writeFileAtomic writes data to dir/name via a temp file + rename so a
// crash mid-write never leaves a half-written icon.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}
