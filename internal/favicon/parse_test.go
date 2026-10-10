package favicon

import (
	"net/url"
	"strings"
	"testing"
)

func parse(t *testing.T, base, doc string) []Candidate {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("bad base %q: %v", base, err)
	}
	return ParseIconLinks(u, strings.NewReader(doc))
}

func urls(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.URL
	}
	return out
}

func TestParseIconLinksHrefKinds(t *testing.T) {
	doc := `<html><head>
		<link rel="icon" href="/abs.png" sizes="32x32">
		<link rel="icon" href="https://cdn.example.com/full.png">
		<link rel="icon" href="//static.example.net/proto.png">
		<link rel="apple-touch-icon" href="apple.png">
	</head><body></body></html>`
	got := parse(t, "https://example.com/dir/page.html", doc)
	want := []string{
		// apple-touch-icon tier first (relative to /dir/).
		"https://example.com/dir/apple.png",
		// Then icon tier, largest size first.
		"https://example.com/abs.png",
		"https://cdn.example.com/full.png",
		"https://static.example.net/proto.png",
	}
	if urls := urls(got); !equalStrings(urls, want) {
		t.Fatalf("candidates = %v, want %v", urls, want)
	}
	// Rel normalization and size extraction.
	if got[0].Rel != "apple-touch-icon" || got[1].Rel != "icon" || got[1].Size != 32 {
		t.Fatalf("fields = %+v", got)
	}
}

func TestParseIconLinksBaseHref(t *testing.T) {
	doc := `<head>
		<base href="https://other.example/sub/">
		<link rel="icon" href="i.png">
		<base href="https://ignored.example/">
		<link rel="icon" href="j.png">
	</head>`
	got := parse(t, "https://example.com/", doc)
	want := []string{
		"https://other.example/sub/i.png",
		"https://other.example/sub/j.png", // only the first <base> applies
	}
	if urls := urls(got); !equalStrings(urls, want) {
		t.Fatalf("candidates = %v, want %v", urls, want)
	}
}

func TestParseIconLinksSizesOrdering(t *testing.T) {
	doc := `<head>
		<link rel="icon" sizes="16x16" href="small.png">
		<link rel="icon" sizes="48x48 32x32" href="large.png">
		<link rel="icon" sizes="any" href="any.svg">
		<link rel="icon" href="nosize.png">
	</head>`
	got := parse(t, "https://example.com/", doc)
	want := []string{
		"https://example.com/any.svg",   // "any" outranks fixed sizes
		"https://example.com/large.png", // largest of "48x48 32x32" = 48
		"https://example.com/small.png",
		"https://example.com/nosize.png",
	}
	if urls := urls(got); !equalStrings(urls, want) {
		t.Fatalf("candidates = %v, want %v", urls, want)
	}
	if got[0].Size != sizeAny || got[1].Size != 48 || got[2].Size != 16 || got[3].Size != 0 {
		t.Fatalf("sizes = %+v", got)
	}
}

func TestParseIconLinksDataURI(t *testing.T) {
	uri := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="
	doc := `<head><link rel="icon" href="` + uri + `"></head>`
	got := parse(t, "https://example.com/", doc)
	if len(got) != 1 || got[0].URL != uri {
		t.Fatalf("candidates = %+v, want the data URI verbatim", got)
	}
}

func TestParseIconLinksFiltering(t *testing.T) {
	doc := `<head>
		<link rel="ICON" href="/upper.png">
		<link rel="mask-icon" href="/mask.svg" color="#5bbad5">
		<link rel="stylesheet" href="/style.css">
		<link rel="shortcut icon" href="/legacy.ico">
		<link href="/norel.png">
		<link rel="icon">
		<link rel="icon" href="javascript:alert(1)">
	</head>
	<body><link rel="icon" href="/too-late.png"></body>`
	got := parse(t, "https://example.com/", doc)
	want := []string{
		"https://example.com/upper.png",  // case-insensitive rel
		"https://example.com/legacy.ico", // "shortcut icon" tier
	}
	// mask-icon, stylesheet, missing rel/href, javascript: and the
	// post-<body> link must all be absent.
	if urls := urls(got); !equalStrings(urls, want) {
		t.Fatalf("candidates = %v, want %v", urls, want)
	}
	if got[1].Rel != "shortcut icon" {
		t.Fatalf("rel = %q", got[1].Rel)
	}
}

func TestParseIconLinksStopsAtHead(t *testing.T) {
	doc := `<head><link rel="icon" href="/in.png"></head>
		<link rel="icon" href="/out.png">`
	got := parse(t, "https://example.com/", doc)
	if len(got) != 1 || got[0].URL != "https://example.com/in.png" {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestParseIconLinksNone(t *testing.T) {
	for _, doc := range []string{
		"",
		"<html><head><title>no links</title></head><body></body></html>",
		"not html at all",
	} {
		if got := parse(t, "https://example.com/", doc); len(got) != 0 {
			t.Fatalf("doc %q: candidates = %+v, want none", doc, got)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
