// Ports the HTTP case of test/p2/loopback-join.test.ts (Pi main 7d073328): share links opened on loopback. The owner's browser
// carries a local owner session (from a sign-in link opened on the host) and skips the join form; a browser without it (a
// fresh profile, incognito) gets the join form, and once joined its guest cookie makes it that guest everywhere, while the
// owner's browser stays the owner. Loopback alone is nobody. The Chromium case is not carried.

package e2e_test

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

func TestShareLinksOnLoopbackGiveACookielessBrowserTheJoinFormAndTheOwnersBrowserTheSession(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port := fixture.Server.Port
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Shared"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) == 1 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	view := "/join/" + object(list(links)[0]).Value("token").(string)
	host := map[string]string{"Host": "127.0.0.1:" + strconv.Itoa(port)}
	get := func(path string, headers ...map[string]string) p2test.HTTPResult {
		t.Helper()
		result, err := p2test.HTTP(port, path, p2test.HTTPOptions{Headers: merged(append([]map[string]string{host}, headers...)...)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	// No cookie: someone joining.
	if fresh := get(view); fresh.Status != 200 || !strings.Contains(fresh.Body, `<form id="join">`) {
		t.Fatalf("cookieless join page: %d", fresh.Status)
	}
	// A cookieless browser loading the app is asked to sign in, and gets no cookie.
	app := get("/", map[string]string{"Sec-Fetch-Site": "none"})
	if app.Status != 401 || len(app.Headers.Values("Set-Cookie")) != 0 {
		t.Fatalf("cookieless app load: %d %q", app.Status, app.Headers.Values("Set-Cookie"))
	}
	// The owner's browser signs in with an owner link opened on the host: a local session.
	minted, err := fixture.HTTP("/owner-token", p2test.HTTPOptions{})
	if err != nil || minted.Status != 200 {
		t.Fatalf("mint: %+v %v", minted, err)
	}
	token := regexp.MustCompile(`"token":"([a-f0-9]{64})"`).FindStringSubmatch(minted.Body)[1]
	claim, err := p2test.HTTP(port, "/owner/claim", p2test.HTTPOptions{
		Method: http.MethodPost, Headers: merged(host, map[string]string{"Origin": origin, "Content-Type": "application/json"}),
		Body: strings.NewReader(`{"token":"` + token + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	device := cookieNamed(claim, "p2_owner_"+strconv.Itoa(port))
	if device == "" || !regexp.MustCompile(`=local\..*; HttpOnly; SameSite=Lax; Path=/`).MatchString(strings.Join(claim.Headers.Values("Set-Cookie"), ",")) {
		t.Fatalf("the claim sets the local owner session cookie: %q", claim.Headers.Values("Set-Cookie"))
	}
	if ownerJoin := get(view, map[string]string{"Cookie": device}); ownerJoin.Status != 303 || ownerJoin.Headers.Get("Location") != "/s/"+session {
		t.Fatalf("the owner goes to the session: %d %q", ownerJoin.Status, ownerJoin.Headers.Get("Location"))
	}
	// A forged cookie is not the owner's.
	forged := "p2_owner_" + strconv.Itoa(port) + "=local." + strings.Repeat("0", 32) + ".9999999999999." + strings.Repeat("0", 64)
	if got := get(view, map[string]string{"Cookie": forged}).Status; got != 200 {
		t.Fatalf("a forged cookie: %d", got)
	}
	// Joining sets a guest cookie, which makes that browser the guest for every request.
	joined, err := p2test.HTTP(port, "/join", p2test.HTTPOptions{
		Method: http.MethodPost, Headers: merged(host, map[string]string{"Origin": origin, "Content-Type": "application/json"}),
		Body: strings.NewReader(`{"token":"` + strings.TrimPrefix(view, "/join/") + `","name":"Guest"}`),
	})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %+v %v", joined, err)
	}
	guest := cookieNamed(joined, "p2_guest_"+strconv.Itoa(port))
	if guest == "" {
		t.Fatal("no guest cookie")
	}
	if got := get("/owner/check", map[string]string{"Cookie": guest}).Status; got != 401 {
		t.Fatalf("a guest is not the owner on loopback: %d", got)
	}
	page := get("/", map[string]string{"Cookie": guest, "Sec-Fetch-Site": "none"})
	if cookieNamed(page, "p2_owner_"+strconv.Itoa(port)) != "" {
		t.Fatal("a guest never gets the owner cookie")
	}
	// The owner's browser with a guest cookie too: its owner cookie wins.
	if got := get("/owner/check", map[string]string{"Cookie": guest + "; " + device}).Status; got != 200 {
		t.Fatalf("owner before guest: %d", got)
	}
	// Cookieless loopback clients are nobody: the TUI and owner-link sign in with the data directory's key.
	if got := get("/owner/check").Status; got != 401 {
		t.Fatalf("a cookieless client: %d", got)
	}
}
