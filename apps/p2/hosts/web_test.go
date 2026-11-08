package hosts_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
)

// Ports apps/p2/test/p2/web.test.ts and health.test.ts: the server serves the web UI (one content-named esbuild bundle) and
// the chord WebSocket to the localhost owner only. The in-browser case of web.test.ts (the app runs under its CSP, another
// origin cannot frame it) is e2e/ui_test.go.

func webFixture(t *testing.T) *p2test.Fixture {
	t.Helper()
	enabled := true
	// share.test stands for a remote address (a tunnel, a proxy): requests from it are signed out without a cookie.
	return p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{}, Web: &enabled, AllowedHosts: []string{"share.test"}})
}

func get(t *testing.T, port int, path string, headers map[string]string) p2test.HTTPResult {
	t.Helper()
	result, err := p2test.HTTP(port, path, p2test.HTTPOptions{Headers: headers})
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return result
}

func currentManifest(t *testing.T, port int) web.Manifest {
	t.Helper()
	response := get(t, port, web.ManifestPath, nil)
	if response.Status != 200 {
		t.Fatalf("/__p2_web: %d %s", response.Status, response.Body)
	}
	var manifest web.Manifest
	if err := json.Unmarshal([]byte(response.Body), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestServesTheAppPageNamingTheCurrentBundleWhoseFilesNeverChange(t *testing.T) {
	port := webFixture(t).Server.Port
	bundle := currentManifest(t, port)
	if got := get(t, port, "/__p2_web", nil).Headers.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("manifest Cache-Control %q", got)
	}
	for _, path := range []string{"/", "/s/0b9f3a52-6a39-4d43-9d55-1a0f4ea4f1a2", "/s/0b9f3a52-6a39-4d43-9d55-1a0f4ea4f1a2/c/3"} {
		page := get(t, port, path, nil)
		if page.Status != 200 {
			t.Fatalf("%s: %d", path, page.Status)
		}
		for _, want := range []string{
			`data-web-version="` + bundle.Version + `"`,
			`<script type="module" src="` + bundle.Script + `"></script>`,
			`<link rel="stylesheet" href="` + *bundle.Style + `" />`,
		} {
			if !strings.Contains(page.Body, want) {
				t.Fatalf("%s lacks %s:\n%s", path, want, page.Body)
			}
		}
		if got := page.Headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("%s Content-Type %q", path, got)
		}
	}
	script := get(t, port, bundle.Script, nil)
	if script.Status != 200 || !strings.HasPrefix(script.Headers.Get("Content-Type"), "text/javascript") {
		t.Fatalf("script: %d %q", script.Status, script.Headers.Get("Content-Type"))
	}
	if got := script.Headers.Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("script Cache-Control %q", got)
	}
	if got := script.Headers.Get("Content-Length"); got == "" || got == "0" {
		t.Fatalf("script Content-Length %q", got)
	}
	// An unknown name under the bundle path is not a page, and a session path that is not an id is not one either.
	for _, path := range []string{web.BundlePath + "nothing.js", "/s/NOT-HEX", "/s/abc/c/0", "/s/abc/c/01"} {
		if status := get(t, port, path, nil).Status; status != 404 {
			t.Fatalf("%s: %d", path, status)
		}
	}
	// Anything but GET and HEAD is not served.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		response, err := p2test.HTTP(port, "/", p2test.HTTPOptions{Method: method, Headers: map[string]string{"Origin": "http://127.0.0.1:" + itoa(port)}})
		if err != nil {
			t.Fatal(err)
		}
		if response.Status != 404 {
			t.Fatalf("%s /: %d", method, response.Status)
		}
	}
	head, err := p2test.HTTP(port, bundle.Script, p2test.HTTPOptions{Method: http.MethodHead})
	if err != nil || head.Status != 200 || head.Body != "" || head.Headers.Get("Content-Length") != script.Headers.Get("Content-Length") {
		t.Fatalf("HEAD script: %+v %v", head, err)
	}
}

func TestPageCarriesACSPRefusingEveryFramerAndLoopbackOwnerOnlyToItsOwnOriginsRequests(t *testing.T) {
	port := webFixture(t).Server.Port
	page := get(t, port, "/", nil)
	if page.Status != 200 {
		t.Fatalf("/: %d", page.Status)
	}
	csp := page.Headers.Get("Content-Security-Policy")
	if !strings.HasSuffix(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP %q", csp)
	}
	match := regexp.MustCompile(`script-src 'self' 'nonce-([^']+)'`).FindStringSubmatch(csp)
	if match == nil {
		t.Fatalf("CSP has no nonce: %q", csp)
	}
	if got := page.Headers.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options %q", got)
	}
	// The page's own inline script (the theme) carries the nonce.
	for _, script := range regexp.MustCompile(`<script[^>]*>`).FindAllString(page.Body, -1) {
		if !strings.Contains(script, ` src=`) && !strings.Contains(script, `nonce="`+match[1]+`"`) {
			t.Fatalf("inline script without the nonce: %s", script)
		}
	}
	// Another site's page, link, frame or fetch, or another local port (same-site), is not the device owner.
	for _, headers := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "document"},
		{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "iframe"},
		{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Dest": "iframe"},
		{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Dest": "empty"},
	} {
		for _, path := range []string{"/", currentManifest(t, port).Script, "/__p2_web", "/__p2_tui", "/__p2_dev"} {
			response := get(t, port, path, headers)
			if response.Status != 401 && response.Status != 403 {
				t.Fatalf("%s %v: %d", path, headers, response.Status)
			}
			if !strings.HasSuffix(response.Headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatalf("%s %v: CSP %q", path, headers, response.Headers.Get("Content-Security-Policy"))
			}
		}
	}
	// File routes check the same principal: no owner download or workspace read for another site.
	for _, path := range []string{"/download?session=x&id=y", "/workspace-file?session=x&conversation=1&path=a"} {
		if get(t, port, path, map[string]string{"Sec-Fetch-Site": "cross-site"}).Status == 200 {
			t.Fatalf("%s answered another site", path)
		}
	}
	// The owner's own requests: typed or bookmarked (none), its pages and modules (same-origin).
	if got := get(t, port, "/", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Dest": "document"}).Status; got != 200 {
		t.Fatalf("typed page: %d", got)
	}
	if got := get(t, port, currentManifest(t, port).Script, map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": "script"}).Status; got != 200 {
		t.Fatalf("same-origin module: %d", got)
	}
}

func TestServesTheInstallableAppsFilesWithoutSigningInNotToAnotherSiteAndNothingElse(t *testing.T) {
	// Adding the app to a home screen fetches them without the page's cookies: Chrome loads the manifest without
	// credentials, iOS often fetches the touch icon on its own (no browser site headers). A remote address without a cookie
	// is signed out.
	port := webFixture(t).Server.Port
	remote := map[string]string{"Host": "share.test"}
	with := func(extra map[string]string) map[string]string {
		merged := map[string]string{}
		for key, value := range remote {
			merged[key] = value
		}
		for key, value := range extra {
			merged[key] = value
		}
		return merged
	}
	manifest := get(t, port, "/manifest.webmanifest", with(map[string]string{"Sec-Fetch-Site": "same-origin"}))
	if manifest.Status != 200 {
		t.Fatalf("manifest: %d", manifest.Status)
	}
	var app struct {
		Name  string `json:"name"`
		Icons []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal([]byte(manifest.Body), &app); err != nil {
		t.Fatal(err)
	}
	if app.Name != "Pi" {
		t.Fatalf("manifest name %q", app.Name)
	}
	paths := []string{"/icons/apple-touch-icon.png", "/apple-touch-icon.png", "/apple-touch-icon-precomposed.png", "/icons/favicon.svg"}
	for _, icon := range app.Icons {
		paths = append(paths, icon.Src)
	}
	for _, path := range paths {
		icon := get(t, port, path, remote)
		if icon.Status != 200 || !strings.HasPrefix(icon.Headers.Get("Content-Type"), "image/") {
			t.Fatalf("%s: %d %q", path, icon.Status, icon.Headers.Get("Content-Type"))
		}
		if got := icon.Headers.Get("Cache-Control"); got != "public, max-age=86400" {
			t.Fatalf("%s Cache-Control %q", path, got)
		}
	}
	worker := get(t, port, "/sw.js", remote)
	if worker.Status != 200 || worker.Headers.Get("Cache-Control") != "no-cache" {
		t.Fatalf("/sw.js: %d %q", worker.Status, worker.Headers.Get("Cache-Control"))
	}
	// Nothing else without signing in.
	for _, path := range []string{"/", "/__p2_web", "/icons/other.png", "/manifest.webmanifest/x", "/__proto__"} {
		if get(t, port, path, remote).Status == 200 {
			t.Fatalf("%s answered a signed-out remote", path)
		}
	}
	// Another site's page may not load them: an icon that loads would tell it a p2 runs at that address.
	for _, headers := range []map[string]string{
		with(map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "image"}),
		{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "image"},
		{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Dest": "image"},
	} {
		if get(t, port, "/icons/icon-192.png", headers).Status == 200 {
			t.Fatalf("another site loaded an icon: %v", headers)
		}
	}
}

func TestRefusesAnotherSitesWebSocketEvenWithAMatchingOrigin(t *testing.T) {
	port := webFixture(t).Server.Port
	origin := "http://127.0.0.1:" + itoa(port)
	opens := func(headers map[string]string) bool {
		conn, err := p2test.Dial(port, headers)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}
	if opens(map[string]string{"Origin": origin, "Sec-Fetch-Site": "cross-site"}) {
		t.Fatal("cross-site socket opened")
	}
	if opens(map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-site"}) {
		t.Fatal("same-site socket opened")
	}
	if !opens(map[string]string{"Origin": origin, "Sec-Fetch-Site": "same-origin"}) {
		t.Fatal("same-origin socket refused")
	}
}

func TestRefusesOtherHostsAndOrigins(t *testing.T) {
	port := webFixture(t).Server.Port
	if got := get(t, port, "/", map[string]string{"Host": "evil.example"}).Status; got != 403 {
		t.Fatalf("forged Host: %d", got)
	}
	if got := get(t, port, "/", map[string]string{"X-Forwarded-For": "1.2.3.4"}).Status; got != 403 {
		t.Fatalf("forwarded: %d", got)
	}
	if conn, err := p2test.Dial(port, map[string]string{"Origin": "http://evil.example"}); err == nil {
		_ = conn.Close()
		t.Fatal("a WebSocket from another origin was accepted")
	}
}

// health.test.ts: /health without signing in (Pi Mobile's readiness check for launchers), known hosts only, and the
// installable app's manifest.
func TestServesHealthWithoutSigningInAndTheAppManifest(t *testing.T) {
	port := webFixture(t).Server.Port
	for _, headers := range []map[string]string{nil, {"Host": "share.test", "Origin": "https://share.test"}} {
		health := get(t, port, "/health", headers)
		if health.Status != 200 || health.Body != `{"ok":true,"name":"p2"}` {
			t.Fatalf("/health %v: %d %s", headers, health.Status, health.Body)
		}
	}
	if got := get(t, port, "/health", map[string]string{"Host": "evil.test"}).Status; got != 403 {
		t.Fatalf("/health on a foreign Host: %d", got)
	}
	manifest := get(t, port, "/manifest.webmanifest", nil)
	var app struct {
		Name string `json:"name"`
	}
	if manifest.Status != 200 || json.Unmarshal([]byte(manifest.Body), &app) != nil || app.Name != "Pi" {
		t.Fatalf("manifest: %d %s", manifest.Status, manifest.Body)
	}
}

// Without web the owner's request no route answers is "Not found" (`web: false`); the public files too.
func TestWithoutWebNothingIsServed(t *testing.T) {
	disabled := false
	port := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{}, Web: &disabled}).Server.Port
	for _, path := range []string{"/", "/__p2_web", "/manifest.webmanifest", "/sw.js"} {
		if got := get(t, port, path, nil).Status; got != 404 {
			t.Fatalf("%s: %d", path, got)
		}
	}
}

// /__p2_dev of a server without dev mode (P2_DEV unset; D473 designs the supervisor out): the owner reads `null`, a POST needs
// its own origin, the command is validated and answers "Dev mode is not enabled", and anything else is refused.
func TestDevEndpointAnswersAsAServerWithoutDevMode(t *testing.T) {
	port := webFixture(t).Server.Port
	own := map[string]string{"Origin": "http://127.0.0.1:" + itoa(port)}
	status := get(t, port, "/__p2_dev", nil)
	if status.Status != 200 || status.Body != "null" || status.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /__p2_dev: %d %q %q", status.Status, status.Body, status.Headers.Get("Content-Type"))
	}
	post := func(headers map[string]string, body string) p2test.HTTPResult {
		response, err := p2test.HTTP(port, "/__p2_dev", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(body)})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	// Button API requires same-origin POST.
	if got := post(nil, `{"action":"reload"}`).Status; got != 403 {
		t.Fatalf("POST without Origin: %d", got)
	}
	for _, body := range []string{`{"action":"reload"}`, `{"action":"reload","plugins":["chat","chat"]}`, `{"action":"restart"}`, `{"action":"restart","n":1e400}`, ` {"action":"reload"} `} {
		response := post(own, body)
		if response.Status != 409 || response.Body != `{"ok":false,"message":"Dev mode is not enabled"}` {
			t.Fatalf("%s: %d %s", body, response.Status, response.Body)
		}
	}
	for body, message := range map[string]string{
		`not json`:                            "Error: Invalid dev command JSON",
		`[]`:                                  "Error: Invalid dev command",
		`{"action":"nap"}`:                    "Error: Invalid dev action",
		`{"action":"reload","plugins":"x"}`:   "Error: Invalid plugin names",
		`{"action":"reload","plugins":["A"]}`: "Error: Invalid plugin names",
		`{"x":1}`:                             "Error: Invalid dev command",
	} {
		response := post(own, body)
		want, _ := json.Marshal(hosts.DevResult{Message: message})
		if response.Status != 400 || response.Body != string(want) {
			t.Fatalf("%s: %d %s", body, response.Status, response.Body)
		}
	}
	if response := post(own, `{"action":"reload","pad":"`+strings.Repeat("x", 16*1024)+`"}`); response.Status != 400 || !strings.Contains(response.Body, "Dev command too large") {
		t.Fatalf("oversized: %d %s", response.Status, response.Body)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The page names the app as its bundle's brand says, and the brand's own files are the public files.
func TestPageAndPublicFilesFollowTheBundlesBrand(t *testing.T) {
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	port := webFixture(t).Server.Port
	page := get(t, port, "/", nil).Body
	for _, want := range []string{
		"<title>" + bundle.Brand.Name + "</title>",
		`content="` + bundle.Brand.Name + `" />`,
		`media="(prefers-color-scheme: dark)" content="` + bundle.Brand.Dark + `"`,
		`background: ` + bundle.Brand.Light + `;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for path, name := range map[string]string{"/icons/favicon.svg": "icons/favicon.svg", "/apple-touch-icon.png": "icons/apple-touch-icon.png", "/manifest.webmanifest": "manifest.webmanifest"} {
		if got := get(t, port, path, nil).Body; got != string(bundle.Public[name]) {
			t.Errorf("%s is not the bundle's %s", path, name)
		}
	}
}
