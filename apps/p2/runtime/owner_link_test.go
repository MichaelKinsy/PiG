package runtime_test

import (
	"strings"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// apps/p2/src/runtime/owner-link.ts is tested upstream through parseTuiArgs (tui.test.ts "TUI auth options, private config
// and owner-claim cookie round trip", tui-guest.test.ts): the same links and refusals, here on the two functions.

func TestOwnerOrigin(t *testing.T) {
	for _, ok := range []string{
		"https://example.test", "https://example.test/", "http://127.0.0.1:8788", "http://localhost", "http://[::1]:1/",
		"HTTPS://EXAMPLE.test", "https://example.test/#", "https://example.test/?", "https://example.test:443",
	} {
		if _, err := p2runtime.OwnerOrigin(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for url, want := range map[string]string{
		"not a url":                     "Invalid URL; use an http(s) origin",
		"":                              "Invalid URL; use an http(s) origin",
		"https://example.test/path":     "Use an http(s) origin URL without credentials, path or query",
		"https://example.test/?token=x": "Use an http(s) origin URL without credentials, path or query",
		"https://example.test/#frag":    "Use an http(s) origin URL without credentials, path or query",
		"https://user@example.test":     "Use an http(s) origin URL without credentials, path or query",
		"https://user:pw@example.test":  "Use an http(s) origin URL without credentials, path or query",
		"ftp://example.test":            "Use an http(s) origin URL without credentials, path or query",
		"file:///tmp":                   "Use an http(s) origin URL without credentials, path or query",
		"http://example.test":           "Remote connections require HTTPS",
		"http://127.0.0.2":              "Remote connections require HTTPS",
		"http://localhost.example.test": "Remote connections require HTTPS",
		"https://example.test/a/../b":   "Use an http(s) origin URL without credentials, path or query",
	} {
		if _, err := p2runtime.OwnerOrigin(url); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %q", url, err, want)
		}
	}
	// Dot segments resolve as the URL parser does: this is the origin itself.
	if _, err := p2runtime.OwnerOrigin("https://example.test/a/.."); err != nil {
		t.Errorf("dot segments: %v", err)
	}
}

func TestOwnerLinkToken(t *testing.T) {
	origin, err := p2runtime.OwnerOrigin("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	got, err := p2runtime.OwnerLinkToken("  https://example.test/owner#"+token+"\n", origin)
	if err != nil || got != token {
		t.Fatalf("%q, %v", got, err)
	}
	const refused = "Use an owner link for this origin with its token in the fragment"
	for _, link := range []string{
		"https://elsewhere.test/owner#" + token,                 // another origin
		"https://example.test/owner?token=secret",               // tui.test.ts: a query
		"https://example.test/owner?x=1#" + token,               // fragment too, but a query
		"https://example.test/owner",                            // no token
		"https://example.test/owner#" + strings.Repeat("a", 63), // short
		"https://example.test/owner#" + strings.Repeat("A", 64), // upper case
		"https://example.test/owner#" + strings.Repeat("g", 64),
		"https://example.test/other#" + token,
		"https://example.test/owner/#" + token,
		"https://u@example.test/owner#" + token,
		"https://u:p@example.test/owner#" + token,
		"http://example.test/owner#" + token,
	} {
		if _, err := p2runtime.OwnerLinkToken(link, origin); err == nil || err.Error() != refused {
			t.Errorf("%q: got %v", link, err)
		}
	}
	if _, err := p2runtime.OwnerLinkToken("nope", origin); err == nil || err.Error() != "Invalid owner link" {
		t.Errorf("invalid: %v", err)
	}
	// An empty query ("?") is no search in JS either.
	if _, err := p2runtime.OwnerLinkToken("https://example.test/owner?#"+token, origin); err != nil {
		t.Errorf("empty query: %v", err)
	}
}
