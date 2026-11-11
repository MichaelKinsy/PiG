// Ports the HTTP and WebSocket cases of test/p2/foreign-requests.test.ts (Pi main 7d073328): requests another site or another
// local port makes (Fetch Metadata `cross-site`/`same-site`; SameSite cookies do not separate ports) never authenticate by any
// credential, except top-level navigations to app pages, which load the app for the owner's cookie (SameSite=Lax sends it on
// those). Without a cookie such a navigation gets the sign-in page, which loads itself once more from p2's origin (a client
// may have dropped the cookie). Requests without Fetch Metadata (the TUI, scripts) are unaffected; Radius-relayed ones are in
// radius_test.go. The Chromium case (a page on another local port) is not carried: it needs a second origin in a browser.

package e2e_test

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

var (
	noCORS   = map[string]string{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}
	navigate = map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
)

// cookieNamed is the `name=value` pair of the Set-Cookie header of result for name ("" when it sets none).
func cookieNamed(result p2test.HTTPResult, name string) string {
	for _, header := range result.Headers.Values("Set-Cookie") {
		if pair, _, _ := strings.Cut(header, ";"); strings.HasPrefix(pair, name+"=") {
			return pair
		}
	}
	return ""
}

// upgradeStatus is the status of a /chord WebSocket upgrade with these headers (101 when accepted).
func upgradeStatus(t *testing.T, port int, headers map[string]string) int {
	t.Helper()
	requestHeaders := http.Header{}
	for name, value := range headers {
		requestHeaders.Set(name, value)
	}
	conn, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+"/chord", requestHeaders)
	if err == nil {
		_ = conn.Close()
		return http.StatusSwitchingProtocols
	}
	if response == nil {
		t.Fatalf("dial: %v", err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func TestForeignBrowserRequests(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port := fixture.Server.Port
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Mine"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	device := fixture.Owner().Cookie
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "full"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	joined, err := p2test.HTTP(port, "/join", p2test.HTTPOptions{
		Method:  http.MethodPost,
		Headers: map[string]string{"Origin": origin, "Content-Type": "application/json"},
		Body:    strings.NewReader(`{"token":"` + object(list(links)[0]).Value("token").(string) + `","name":"Guest"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	guest := cookieNamed(joined, "p2_guest_"+strconv.Itoa(port))
	if guest == "" {
		t.Fatalf("a guest cookie: %d %q", joined.Status, joined.Body)
	}
	get := func(path string, headers ...map[string]string) p2test.HTTPResult {
		t.Helper()
		result, err := p2test.HTTP(port, path, p2test.HTTPOptions{Headers: merged(headers...)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	with := func(cookie string) map[string]string { return map[string]string{"Cookie": cookie} }
	check := func(headers ...map[string]string) int { return get("/owner/check", headers...).Status }

	t.Run("authenticates no credential of a foreign subresource, fetch or WebSocket", func(t *testing.T) {
		for _, site := range []string{"same-site", "cross-site"} {
			foreign := merged(noCORS, map[string]string{"Sec-Fetch-Site": site})
			if got := check(foreign, with(device)); got != 401 {
				t.Errorf("%s: the owner cookie: %d", site, got)
			}
			if got := check(foreign); got != 401 {
				t.Errorf("%s: no credential: %d", site, got)
			}
			if got := get("/owner-token", foreign, with(device)).Status; got != 403 {
				t.Errorf("%s: no invitation: %d", site, got)
			}
			// A guest cookie: not that guest (403 for a guest outside the web UI, 401 for nobody).
			if got := get("/s/"+session, foreign, with(guest)).Status; got != 401 {
				t.Errorf("%s: a guest cookie: %d", site, got)
			}
			socket := map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": "websocket", "Sec-Fetch-Dest": "websocket"}
			if got := upgradeStatus(t, port, merged(socket, with(device))); got != 403 {
				t.Errorf("%s: no owner WebSocket: %d", site, got)
			}
			if got := upgradeStatus(t, port, merged(socket, with(guest))); got != 403 {
				t.Errorf("%s: no guest WebSocket: %d", site, got)
			}
			// Framing the app is no navigation either.
			framed := map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}
			if got := get("/", framed, with(device)).Status; got != 401 {
				t.Errorf("%s: framed: %d", site, got)
			}
		}
		// The page's own requests, and clients without Fetch Metadata (the TUI, scripts), are unaffected.
		if got := check(map[string]string{"Sec-Fetch-Site": "same-origin"}, with(device)); got != 200 {
			t.Errorf("same-origin: %d", got)
		}
		if got := check(with(device)); got != 200 {
			t.Errorf("no Fetch Metadata: %d", got)
		}
		if got := upgradeStatus(t, port, with(device)); got != 101 {
			t.Errorf("owner WebSocket: %d", got)
		}
		if got := upgradeStatus(t, port, with(guest)); got != 101 {
			t.Errorf("guest WebSocket: %d", got)
		}
		// Loopback without a credential is nobody.
		if got := check(); got != 401 {
			t.Errorf("no credential: %d", got)
		}
		if got := upgradeStatus(t, port, nil); got != 403 {
			t.Errorf("WebSocket without a credential: %d", got)
		}
	})

	reloads := regexp.MustCompile(`if\(!location\.hash\)location\.replace\(location\.href\);`)
	t.Run("loads app pages on a foreign top-level navigation with the owner's cookie, else signs in", func(t *testing.T) {
		for _, site := range []string{"same-site", "cross-site"} {
			navigation := merged(navigate, map[string]string{"Sec-Fetch-Site": site})
			page := get("/", navigation, with(device))
			if page.Status == 401 {
				t.Errorf("%s navigation with the cookie does not open the app", site)
			}
			if cookieNamed(page, "p2_owner_"+strconv.Itoa(port)) == "" {
				t.Errorf("%s navigation does not renew the owner's local session", site)
			}
			// A sidebar list's address is an app page too (`/list/<key>`).
			if list := get("/list/folder%3AW251bGwsIi93Il0", navigation, with(device)); list.Status == 401 {
				t.Errorf("%s navigation to a list with the cookie does not open the app", site)
			}
			// The new-session screen's address is an app page too (`/new`).
			if got := get("/new", navigation, with(device)).Status; got == 401 {
				t.Errorf("%s navigation to /new with the cookie does not open the app", site)
			}
			signIn := get("/s/"+session, navigation)
			if signIn.Status != 401 || !reloads.MatchString(signIn.Body) {
				t.Errorf("%s navigation without a cookie signs in after one reload: %d", site, signIn.Status)
			}
			// Navigations elsewhere than app pages stay unauthenticated.
			if got := get("/owner-token", navigation, with(device)).Status; got != 403 {
				t.Errorf("%s: /owner-token: %d", site, got)
			}
			if got := get("/owner/check", navigation, with(device)).Status; got != 401 {
				t.Errorf("%s: /owner/check: %d", site, got)
			}
		}
	})

	t.Run("loads the app when Chrome on Android launches the installed app (navigate, Dest empty, no cookie)", func(t *testing.T) {
		// As logged on the phone: a WebAPK launch of start_url `/` is cross-site, `navigate` with `Sec-Fetch-Dest: empty`.
		launch := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "empty"}
		if got := get("/", launch, with(device)).Status; got == 401 {
			t.Errorf("the launch does not open the app: %d", got)
		}
		// Without its cookie the launch gets the sign-in page, which loads itself once more from p2's own origin.
		cookieless := get("/", launch)
		if cookieless.Status != 401 || !strings.Contains(cookieless.Body, "location.replace(location.href)") {
			t.Errorf("cookieless launch: %d", cookieless.Status)
		}
		again := get("/", map[string]string{"Sec-Fetch-Site": "same-origin"}, navigate)
		if again.Status != 401 || strings.Contains(again.Body, "location.replace(location.href)") {
			t.Errorf("the same-origin load must not loop: %d", again.Status)
		}
		// Only a navigation: another site's fetch (Dest empty, mode cors or no-cors) still gets nobody.
		for _, mode := range []string{"cors", "no-cors"} {
			fetched := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": mode, "Sec-Fetch-Dest": "empty"}
			if got := get("/", fetched, with(device)).Status; got != 401 {
				t.Errorf("%s: %d", mode, got)
			}
		}
	})
}
