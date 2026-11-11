package surfaces

import "testing"

// Proof of pageScripts (web.ts): the inline bootstrap carries the nonce, then the stylesheets, then the deferred scripts
// (the plugins' before the surface's own).

func TestPageScriptsGivesTheBootstrapWithItsNonceThenStylesThenDeferredScripts(t *testing.T) {
	got := PageScripts(PageAssets{Bootstrap: "boot()", Script: "/s.js", Plugins: "/p.js", Styles: []string{"/a.css", "/b.css"}}, "N1")
	want := `<script nonce="N1">boot()</script>` + "\n" +
		`<link rel="stylesheet" href="/a.css">` + "\n" +
		`<link rel="stylesheet" href="/b.css">` + "\n" +
		`<script defer src="/p.js"></script>` + "\n" +
		`<script defer src="/s.js"></script>`
	if got != want {
		t.Fatalf("PageScripts =\n%s\nwant\n%s", got, want)
	}
	bare := PageScripts(PageAssets{Bootstrap: "b", Script: "/s", Plugins: "/p"}, "n")
	if bare != "<script nonce=\"n\">b</script>\n<script defer src=\"/p\"></script>\n<script defer src=\"/s\"></script>" {
		t.Fatalf("without styles: %s", bare)
	}
}
