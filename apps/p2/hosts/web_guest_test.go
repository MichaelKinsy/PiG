package hosts

import (
	"net/http"
	"net/url"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
)

// A guest loads GET/HEAD of its session's pages, the app's public files, the bundle manifest and the bundle's files
// (`guestMayLoad`); encoded or traversing paths are refused before normalizing (sharing.test.ts exercises the same over
// a share link).
func TestGuestMayLoad(t *testing.T) {
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	served := &Web{Nonce: "n", Bundle: bundle}
	guest := p2runtime.GuestPrincipal{ID: "g", Sessions: map[string]p2runtime.GuestMode{"abc-1": p2runtime.GuestView}}
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/", true},
		{"HEAD", "/", true},
		{"POST", "/", false},
		{"GET", "/s/abc-1", true},
		{"GET", "/s/abc-1/c/2", true},
		{"GET", "/s/abc-1/c/0", false},
		{"GET", "/s/abc-2", false},
		{"GET", "/s/__proto__", false},
		{"GET", "/s/abc-1/c/2/", false},
		{"GET", "/__p2_web", true},
		{"GET", web.BundlePath + bundle.Script, true},
		{"GET", web.BundlePath + "nothing.js", false},
		{"GET", web.BundlePath, false},
		{"GET", "/manifest.webmanifest", true},
		{"GET", "/sw.js", true},
		{"GET", "/__p2_dev", false},
		{"GET", "/__p2_tui", false},
		{"GET", "/s/abc%2D1", false},
		{"GET", web.BundlePath + "../__p2_dev", false},
		{"GET", "/s/abc-1/../abc-1", false},
		{"GET", "//", false},
		{"GET", "/./", false},
		{"GET", "/s\\abc-1", false},
	}
	for _, c := range cases {
		target := &url.URL{Path: c.path}
		if c.path == "/s/abc%2D1" {
			target = &url.URL{Path: "/s/abc-1", RawPath: "/s/abc%2D1"}
		}
		request := &http.Request{Method: c.method, URL: target}
		if got := served.GuestMayLoad(request, guest); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
