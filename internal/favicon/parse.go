package favicon

import (
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Candidate is one icon link found in a page's <head>: an absolute URL
// (or a data: URI) plus metadata used to rank it.
type Candidate struct {
	URL  string // absolute URL or data: URI
	Rel  string // normalized rel value (lowercase, single-spaced)
	Size int    // largest dimension from the sizes attr; 0 if absent, 1000 for "any"
}

// sizeAny is the pseudo-dimension assigned to sizes="any" so scalable
// icons sort above every fixed size.
const sizeAny = 1000

// ParseIconLinks scans the document head in r for <link> tags that point
// at icons and returns them as ranked candidates: apple-touch-icon
// variants first, then icon, then shortcut icon; larger sizes win within
// each tier. Scanning stops at </head> or <body>. Relative hrefs resolve
// against base, honoring an in-document <base href>; data: URIs pass
// through untouched. mask-icon links are skipped (they are monochrome
// SVGs for Safari pinned tabs, not real favicons).
func ParseIconLinks(base *url.URL, r io.Reader) []Candidate {
	docBase := base
	baseSeen := false
	var cands []Candidate

	z := html.NewTokenizer(r)
	for {
		switch z.Next() {
		case html.ErrorToken:
			return sortCandidates(cands)
		case html.StartTagToken, html.SelfClosingTagToken:
			tag, hasAttr := z.TagName()
			switch string(tag) {
			case "body":
				return sortCandidates(cands)
			case "base":
				// Per HTML spec only the first <base href> applies.
				if !baseSeen {
					baseSeen = true
					if href, ok := attrVal(z, hasAttr, "href"); ok {
						if b, err := url.Parse(strings.TrimSpace(href)); err == nil {
							docBase = base.ResolveReference(b)
						}
					}
				}
			case "link":
				if c, ok := linkCandidate(docBase, z, hasAttr); ok {
					cands = append(cands, c)
				}
			}
		case html.EndTagToken:
			if tag, _ := z.TagName(); string(tag) == "head" {
				return sortCandidates(cands)
			}
		}
	}
}

// linkCandidate converts a <link> token into a Candidate, reporting
// false when the rel is not an icon rel or the href is unusable.
func linkCandidate(base *url.URL, z *html.Tokenizer, hasAttr bool) (Candidate, bool) {
	var href, rel, sizes string
	for hasAttr {
		var key, val []byte
		key, val, hasAttr = z.TagAttr()
		switch string(key) {
		case "href":
			href = string(val)
		case "rel":
			rel = string(val)
		case "sizes":
			sizes = string(val)
		}
	}
	normRel := normalizeRel(rel)
	if normRel == "" {
		return Candidate{}, false
	}
	href = strings.TrimSpace(href)
	if href == "" {
		return Candidate{}, false
	}
	u, err := url.Parse(href)
	if err != nil {
		return Candidate{}, false
	}
	if u.Scheme != "data" {
		u = base.ResolveReference(u)
	}
	// Only fetchable schemes are useful candidates.
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "data" {
		return Candidate{}, false
	}
	return Candidate{URL: u.String(), Rel: normRel, Size: parseSizes(sizes)}, true
}

// normalizeRel parses the rel attribute into a canonical icon kind:
// "icon", "shortcut icon", "apple-touch-icon" or
// "apple-touch-icon-precomposed". It returns "" for anything else,
// including any rel containing mask-icon (a monochrome Safari pinned-tab
// icon, not a usable favicon). Extra keywords alongside an icon rel are
// tolerated ("alternate icon" still counts).
func normalizeRel(rel string) string {
	toks := strings.Fields(strings.ToLower(rel))
	if len(toks) == 0 || slices.Contains(toks, "mask-icon") {
		return ""
	}
	for _, t := range toks {
		switch t {
		case "apple-touch-icon", "apple-touch-icon-precomposed":
			return t
		case "shortcut":
			if slices.Contains(toks, "icon") {
				return "shortcut icon"
			}
		case "icon":
			return "icon"
		}
	}
	return ""
}

// parseSizes returns the largest dimension named by a sizes attribute
// ("16x16 48x48" → 48); "any" maps to sizeAny and garbage to 0.
func parseSizes(sizes string) int {
	best := 0
	for _, tok := range strings.Fields(sizes) {
		if strings.EqualFold(tok, "any") {
			return sizeAny
		}
		w, h, ok := strings.Cut(strings.ToLower(tok), "x")
		if !ok {
			continue
		}
		wi, errW := strconv.Atoi(w)
		hi, errH := strconv.Atoi(h)
		if errW != nil || errH != nil {
			continue
		}
		best = max(best, wi, hi)
	}
	return best
}

// attrVal fetches the value of the wanted attribute from the current
// token, consuming the remaining attributes.
func attrVal(z *html.Tokenizer, hasAttr bool, want string) (string, bool) {
	var out string
	found := false
	for hasAttr {
		var key, val []byte
		key, val, hasAttr = z.TagAttr()
		if string(key) == want {
			out, found = string(val), true
		}
	}
	return out, found
}

// sortCandidates orders candidates by rel tier then size, descending.
// Sorting is stable so document order breaks ties.
func sortCandidates(c []Candidate) []Candidate {
	slices.SortStableFunc(c, func(a, b Candidate) int {
		if d := relRank(a.Rel) - relRank(b.Rel); d != 0 {
			return d
		}
		return b.Size - a.Size
	})
	return c
}

// relRank ranks icon kinds: apple-touch-icon variants beat plain icons,
// which beat "shortcut icon".
func relRank(rel string) int {
	switch rel {
	case "apple-touch-icon", "apple-touch-icon-precomposed":
		return 0
	case "icon":
		return 1
	default: // "shortcut icon"
		return 2
	}
}
