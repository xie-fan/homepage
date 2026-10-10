package api

import "testing"

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"HTTPS://EXAMPLE.COM/Path", "https://example.com/Path"},
		{"http://example.com:80/a", "http://example.com/a"},
		{"https://example.com:443/a", "https://example.com/a"},
		{"http://example.com:8080/a", "http://example.com:8080/a"},
		{"https://example.com:8443", "https://example.com:8443/"},
		{"https://example.com/p?q=1#frag", "https://example.com/p?q=1"},
		{"https://example.com/p#frag", "https://example.com/p"},
		{"https://example.com/?q=a+b", "https://example.com/?q=a+b"},
		{"https://example.com/a%20b", "https://example.com/a%20b"},
		{"http://[::1]:80/x", "http://[::1]/x"},
		{"http://[::1]:8080/x", "http://[::1]:8080/x"},
		{"http://exa mple.com", ""}, // unparsable
		{"%zz", ""},                 // unparsable
	} {
		if got := NormalizeURL(tc.in); got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// URLs that differ only in case, default port, empty-vs-"/" path,
// surrounding space or fragment must normalize equal — these are the
// pairs duplicate detection catches.
func TestNormalizeURLEquivalent(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://example.com", "https://example.com/"},
		{"https://example.com", "HTTPS://EXAMPLE.COM"},
		{"http://example.com:80/", "http://example.com"},
		{"https://example.com:443", "https://EXAMPLE.com/"},
		{"https://example.com/p#a", "https://example.com/p#b"},
		{"https://example.com", "  https://example.com  "},
		{"https://a.example.com", "https://A.Example.COM:443/#frag"},
	} {
		if got, want := NormalizeURL(pair[0]), NormalizeURL(pair[1]); got != want {
			t.Errorf("NormalizeURL(%q) = %q != NormalizeURL(%q) = %q",
				pair[0], got, pair[1], want)
		}
	}
}

// Path case, query, scheme and non-default ports keep URLs distinct.
func TestNormalizeURLDistinct(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://example.com/a", "https://example.com/A"},
		{"https://example.com/?q=1", "https://example.com/?q=2"},
		{"https://example.com/page", "https://example.com/other"},
		{"http://example.com/", "https://example.com/"},
		{"http://example.com:8080/", "http://example.com/"},
		{"https://example.com", "https://other.example.com"},
	} {
		if got, want := NormalizeURL(pair[0]), NormalizeURL(pair[1]); got == want {
			t.Errorf("NormalizeURL(%q) == NormalizeURL(%q) = %q, want distinct",
				pair[0], pair[1], got)
		}
	}
}
