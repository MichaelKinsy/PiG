// Ports the server side of apps/p2/plugins/glyphs (server.ts, Pi main ea153dd28): `/glyph.svg?seed=&size=` serves a project glyph
// to anyone, and refuses a query that is not within the limits.

package e2e_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/glyphs"
)

func TestGlyphSVGIsServedToAnyoneAndRefusesAQueryOutsideItsLimits(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"glyphs"}})
	port := fixture.Server.Port
	// No sign-in: the glyph reads no data.
	svg := func(method, query string) p2test.HTTPResult {
		t.Helper()
		result, err := p2test.HTTP(port, "/glyph.svg"+query, p2test.HTTPOptions{Method: method})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	got := svg(http.MethodGet, "?seed="+url.QueryEscape("~/Development/pi-mono")+"&size=48")
	if got.Status != 200 || got.Body != glyphs.SVG(glyphs.Of("~/Development/pi-mono"), 48) {
		t.Fatalf("GET: %d %s", got.Status, got.Body)
	}
	for name, want := range map[string]string{
		"Content-Type":                 "image/svg+xml; charset=utf-8",
		"Cache-Control":                "public, max-age=86400",
		"Content-Security-Policy":      "default-src 'none'; sandbox",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "cross-origin",
	} {
		if value := got.Headers.Get(name); value != want {
			t.Errorf("%s = %q, want %q", name, value, want)
		}
	}
	if size := svg(http.MethodGet, "?seed=radius").Body; !strings.Contains(size, `width="64" height="64"`) {
		t.Errorf("default size: %s", size)
	}
	head := svg(http.MethodHead, "?seed=radius&size=48")
	if head.Status != 200 || head.Body != "" || head.Headers.Get("Content-Length") == "" {
		t.Errorf("HEAD: %d %q %q", head.Status, head.Body, head.Headers.Get("Content-Length"))
	}
	for _, query := range []string{
		"", "?size=48", "?seed=radius&size=7", "?seed=radius&size=1025", "?seed=radius&size=48.5", "?seed=radius&size=big",
		"?seed=radius&size=", "?seed=" + strings.Repeat("x", 1025),
	} {
		if refused := svg(http.MethodGet, query); refused.Status != 400 {
			t.Errorf("%.40s: %d, want 400", query, refused.Status)
		}
	}
	if edge := svg(http.MethodGet, "?seed="+strings.Repeat("x", 1024)+"&size=1024"); edge.Status != 200 {
		t.Errorf("the limits themselves: %d", edge.Status)
	}
}
