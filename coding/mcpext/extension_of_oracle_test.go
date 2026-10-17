package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// extensionOf (extensions/mcp/tools.ts:155-158) is not exported by Pi's module, so the oracle body transcribes it and runs it on Node's own URL: the extension of a saved binary resource is the one its URL pathname ends in, or its raw text when it is not a URL.
func TestExtensionOfMatchesNodeURL(t *testing.T) {
	uris := []string{
		"file:///tmp/report.pdf", "https://example.com/a/b.png", "https://example.com/a/b.png?x=1.jpg", "https://example.com/a/b.png#frag.gif", "https://example.com/a/b.tar.gz", "https://example.com/a/noext", "https://example.com/", "https://example.com",
		"https://example.com/a.verylongext", "https://example.com/a.12345678", "https://example.com/a.123456789", "https://example.com/a/b.PNG", "https://example.com/a/../b.txt", "https://example.com/a/b.txt/..", "https://example.com/a\\b.txt", "https:\\\\example.com\\a.zip",
		"https://example.com/a b.txt", "https://example.com/é.txt", "https://example.com/a.é", "https://example.com/a%2Etxt", "https://example.com/a.%74xt", "https://example.com/a.txt%20", " https://example.com/a.txt ", "https://example.com/a.txt\t", "https://exämple.com/a.md",
		"note:readme.pdf", "note:readme", "mailto:a@b.com", "data:text/plain;base64,AAAA", "data:image/png,x.png", "custom://host/path/x.mp3", "custom:/x.mp3", "custom:x.mp3?q=1", "urn:isbn:0451450523", "resource://server/file.json",
		"a.txt", "dir/a.txt", "/abs/a.txt", "./a.txt", "../a.txt", "a.txt?x", "a b.txt", "C:\\dir\\a.txt", "C:/dir/a.txt", "c:a.txt", "x:", "", ".", ".gitignore", "a.", "a.b.c", "a/b/c.", "a.\n", "https://example.com/a.txt\n",
	}
	var want []string
	pioracle.Run(t, `
emit(input.map((uri) => {
	const path = URL.canParse(uri) ? new URL(uri).pathname : uri;
	return /\.[A-Za-z0-9]{1,8}$/.exec(path)?.[0] ?? ".bin";
}));`, uris, &want)
	for i, uri := range uris {
		if got := extensionOf(uri); got != want[i] {
			t.Errorf("extensionOf(%q) = %q, Pi %q", uri, got, want[i])
		}
	}
}
