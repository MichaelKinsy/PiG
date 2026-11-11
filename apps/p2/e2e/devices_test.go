// Ports the server cases of test/p2/devices.test.ts (Pi main c23b9ec1): owner sign-in from a tunnel host with a one-time
// token and a signed cookie, never granting remote or guest token minting, sign-out of one or all devices with live
// sockets, and the owner-link command. The OwnerAccess unit cases are plugins/devices/contract/owner_access_test.go.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	devicescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	logincontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker"
)

var ownerTunnel = map[string]string{"Host": "owner.test", "Origin": "https://owner.test"}

func merged(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, each := range maps {
		for name, value := range each {
			out[name] = value
		}
	}
	return out
}

func httpGet(t *testing.T, port int, path string, options p2test.HTTPOptions) p2test.HTTPResult {
	t.Helper()
	result, err := p2test.HTTP(port, path, options)
	if err != nil {
		t.Fatalf("%s %s: %v", options.Method, path, err)
	}
	return result
}

func statusOf(t *testing.T, port int, path string, headers map[string]string) int {
	t.Helper()
	return httpGet(t, port, path, p2test.HTTPOptions{Headers: headers}).Status
}

func claimWith(t *testing.T, port int, body string, headers map[string]string) p2test.HTTPResult {
	t.Helper()
	return httpGet(t, port, "/owner/claim", p2test.HTTPOptions{
		Method: http.MethodPost, Headers: merged(headers, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader(body),
	})
}

func tokenBody(token string) string {
	encoded, _ := json.Marshal(map[string]string{"token": token})
	return string(encoded)
}

func cookieOf(t *testing.T, result p2test.HTTPResult) string {
	t.Helper()
	return strings.Split(result.Headers.Get("Set-Cookie"), ";")[0]
}

func mint(t *testing.T, devices *host.Reference) string {
	t.Helper()
	minted, err := devices.Call("token", delta.NewJsonObject(0), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	return object(minted).Value("token").(string)
}

func TestDevicesPluginSignsInARemoteOwnerOverHTTPAndWSNeverGrantsRemoteOrGuestTokenMinting(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"owner.test"}})
	port := fixture.Server.Port
	local := fixture.Browser()
	devices, err := p2test.HoldIn(local, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(devices.Member("canMint"), func(value delta.JsonValue) bool { return value == true }, 0); err != nil {
		t.Fatal(err)
	}
	relayDevices, err := p2test.HoldIn(fixture.As(p2runtime.OwnerPrincipal{Access: p2runtime.AccessRelay}), devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = relayDevices.Call("token", delta.NewJsonObject(0), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	_, err = p2test.HoldIn(fixture.As(p2runtime.WorkerPrincipal{Session: "s", Generation: 1}), devicescontract.Devices, delta.NewJsonObject(0), "owner")
	p2test.ExpectChord(t, err, data.CodeForbidden)

	token := mint(t, devices)
	landing := httpGet(t, port, "/owner", p2test.HTTPOptions{Headers: ownerTunnel})
	if landing.Status != 200 {
		t.Fatalf("/owner: %d", landing.Status)
	}
	for _, want := range []string{"One-time token", "Devices", `fetch("/owner/claim"`, `type="password"`} {
		if !strings.Contains(landing.Body, want) {
			t.Fatalf("the sign-in page lacks %q", want)
		}
	}
	if strings.Contains(landing.Body, token) {
		t.Fatal("the sign-in page shows the token")
	}
	if !regexp.MustCompile(`script-src 'nonce-`).MatchString(landing.Headers.Get("Content-Security-Policy")) {
		t.Fatalf("CSP %q", landing.Headers.Get("Content-Security-Policy"))
	}
	if head := httpGet(t, port, "/owner", p2test.HTTPOptions{Headers: ownerTunnel, Method: http.MethodHead}); head.Body != "" {
		t.Fatalf("HEAD /owner has a body: %q", head.Body)
	}
	if landing.Headers.Get("Set-Cookie") != "" {
		t.Fatal("the sign-in page sets a cookie")
	}
	claim := func(body string, headers ...map[string]string) p2test.HTTPResult {
		t.Helper()
		use := ownerTunnel
		if len(headers) > 0 {
			use = headers[0]
		}
		return claimWith(t, port, body, use)
	}
	if got := claim(tokenBody(token), merged(ownerTunnel, map[string]string{"Origin": "https://foreign.test"})).Status; got != 403 {
		t.Fatalf("foreign origin: %d", got)
	}
	if got := httpGet(t, port, "/owner/claim", p2test.HTTPOptions{Method: http.MethodPost, Headers: map[string]string{"Host": ownerTunnel["Host"]}, Body: strings.NewReader(tokenBody(token))}).Status; got != 403 {
		t.Fatalf("no origin: %d", got)
	}
	if got := claim("{").Status; got != 400 {
		t.Fatalf("invalid JSON: %d", got)
	}
	if got := claim(strings.Repeat("x", 1025)).Status; got != 413 {
		t.Fatalf("too large: %d", got)
	}
	if got := claim(tokenBody("wrong")).Status; got != 403 {
		t.Fatalf("wrong token: %d", got)
	}
	accepted := claim(tokenBody(token))
	if accepted.Status != 200 {
		t.Fatalf("claim: %d %s", accepted.Status, accepted.Body)
	}
	if got := claim(tokenBody(token)).Status; got != 403 {
		t.Fatalf("a token works once: %d", got)
	}
	setCookie := accepted.Headers.Get("Set-Cookie")
	if !regexp.MustCompile(`HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000; Secure`).MatchString(setCookie) {
		t.Fatalf("Set-Cookie %q", setCookie)
	}
	cookie := cookieOf(t, accepted)
	headers := merged(ownerTunnel, map[string]string{"Cookie": cookie})
	if got := statusOf(t, port, "/", headers); got != 200 {
		t.Fatalf("/ with the cookie: %d", got)
	}
	var manifest web.Manifest
	manifestResponse := httpGet(t, port, web.ManifestPath, p2test.HTTPOptions{Headers: headers})
	if manifestResponse.Status != 200 {
		t.Fatalf("manifest: %d", manifestResponse.Status)
	}
	if err := json.Unmarshal([]byte(manifestResponse.Body), &manifest); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, port, manifest.Script, headers); got != 200 {
		t.Fatalf("bundle script: %d", got)
	}
	if got := statusOf(t, port, "/owner-token", headers); got != 403 {
		t.Fatalf("/owner-token with a remote cookie: %d", got)
	}
	localOwner := map[string]string{"Cookie": fixture.Owner().Cookie}
	if got := statusOf(t, port, "/owner-token", nil); got != 403 {
		t.Fatalf("loopback without a credential is nobody: %d", got)
	}
	for _, site := range []string{"cross-site", "same-site", "none"} {
		if got := statusOf(t, port, "/owner-token", merged(localOwner, map[string]string{"Sec-Fetch-Site": site})); got != 403 {
			t.Fatalf("/owner-token refuses Sec-Fetch-Site: %s: %d", site, got)
		}
	}
	if got := statusOf(t, port, "/owner-token", merged(localOwner, map[string]string{"Sec-Fetch-Site": "same-origin"})); got != 200 {
		t.Fatalf("/owner-token same-origin: %d", got)
	}
	if got := statusOf(t, port, "/owner-token", localOwner); got != 200 {
		t.Fatalf("/owner-token as the local owner: %d", got)
	}
	if got := statusOf(t, port, "/", merged(headers, map[string]string{"Cookie": "wrong"})); got != 401 {
		t.Fatalf("/ with a wrong cookie: %d", got)
	}
	if got := statusOf(t, port, "/", merged(headers, map[string]string{"Host": "wrong.test"})); got != 403 {
		t.Fatalf("/ on a wrong host: %d", got)
	}
	if got := statusOf(t, port, "/", merged(localOwner, map[string]string{"X-Forwarded-For": "203.0.113.7"})); got != 403 {
		t.Fatalf("/ forwarded: %d", got)
	}
	if got := statusOf(t, port, "/owner-key", headers); got != 404 {
		t.Fatalf("/owner-key: %d", got)
	}
	if got := statusOf(t, port, "/@fs"+filepath.Join(fixture.DataDir, "owner-token.txt"), headers); got == 200 {
		t.Fatal("the startup token file is served")
	}
	remote, _ := fixture.Client(headers)
	if _, err := p2test.HoldIn(remote, p2runtime.Access, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatal(err)
	}
	remoteDevices, err := p2test.HoldIn(remote, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(remoteDevices.Member("canMint"), func(value delta.JsonValue) bool { return value == false }, 0); err != nil {
		t.Fatal(err)
	}
	_, err = remoteDevices.Call("token", delta.NewJsonObject(0), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	// Associated devices retain remote owner provider access.
	if _, err := p2test.HoldIn(remote, logincontract.ProviderLogin, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatal(err)
	}
	source, err := fixture.Server.Host.Registry(p2test.Background, devicescontract.DeviceAccesses, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := source.Value().(*devicescontract.DeviceAccessParts)
	if parts == nil {
		t.Fatal("no open owner access")
	}
	probe, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	probe.Header.Set("Cookie", cookie)
	if session := parts.Access().Session(probe, port); session == nil || session.Kind != devicescontract.SessionDevice {
		t.Fatalf("the internal access does not know the cookie: %+v", session)
	}
	outstanding := mint(t, devices)
	// Pi: a reload of unchanged code is a no-op, the facet and its open owner access stay.
	if _, err := fixture.Server.Reload([]string{"devices"}, p2test.Background); err != nil {
		t.Fatal(err)
	}
	if got := claim(tokenBody(outstanding)).Status; got != 200 {
		t.Fatalf("compatible reloads retain invitations and the owner key: %d", got)
	}

	sessions, err := p2test.HoldIn(local, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	sharing, err := p2test.HoldIn(local, sharingcontract.Sharing, delta.JsonObjectOf("session", object(created).Value("id")), "owner")
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
	joinBody, _ := json.Marshal(map[string]any{"token": object(list(links)[0]).Value("token"), "name": "Guest"})
	joined := httpGet(t, port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(ownerTunnel, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader(string(joinBody))})
	if joined.Status != 200 {
		t.Fatalf("join: %d %s", joined.Status, joined.Body)
	}
	guestCookie := cookieOf(t, joined)
	guest, _ := fixture.Client(merged(ownerTunnel, map[string]string{"Cookie": guestCookie}))
	_, err = p2test.HoldIn(guest, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	p2test.ExpectChord(t, err, data.CodeForbidden)
	if got := statusOf(t, port, "/owner-token", merged(ownerTunnel, map[string]string{"Cookie": guestCookie})); got != 403 {
		t.Fatalf("/owner-token as a guest: %d", got)
	}
	both, _ := fixture.Client(merged(ownerTunnel, map[string]string{"Cookie": guestCookie + "; " + cookie}))
	if _, err := p2test.HoldIn(both, p2runtime.Access, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatalf("an owner cookie is downgraded by an existing guest cookie: %v", err)
	}
	startupText, err := os.ReadFile(filepath.Join(fixture.DataDir, "owner-token.txt"))
	if err != nil {
		t.Fatal(err)
	}
	startup := strings.TrimSpace(string(startupText))
	if got := claim(tokenBody(startup)).Status; got != 200 {
		t.Fatalf("the headless startup token: %d", got)
	}

	if err := fixture.Server.Close(p2test.Background); err != nil {
		t.Fatal(err)
	}
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake, noWeb := true, false
	restarted, err := hosts.StartServer(hosts.ServerOptions{DataDir: fixture.DataDir, Port: &port, Fake: &fake, Web: &noWeb, AllowedHosts: []string{"owner.test"}, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(p2test.Background) })
	if got := statusOf(t, port, "/owner-token", headers); got != 403 {
		t.Fatalf("the old cookie remains a remote owner after restart: %d", got)
	}
	if got := statusOf(t, port, "/", headers); got != 404 {
		t.Fatalf("an authorized owner reaches the UI-less server: %d", got)
	}
	freshText, err := os.ReadFile(filepath.Join(fixture.DataDir, "owner-token.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.TrimSpace(string(freshText))
	if fresh == startup {
		t.Fatal("a restart keeps the startup token")
	}
	if got := claim(tokenBody(startup)).Status; got != 403 {
		t.Fatalf("the old startup token: %d", got)
	}
	if got := claim(tokenBody(fresh)).Status; got != 200 {
		t.Fatalf("the fresh startup token: %d", got)
	}
}

func TestSignOutThisDeviceAndAllDevicesRevokeLiveSocketsAsWellAsCookies(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"owner.test"}})
	port := fixture.Server.Port
	local, err := p2test.HoldIn(fixture.Browser(), devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	claim := func() string {
		t.Helper()
		response := claimWith(t, port, tokenBody(mint(t, local)), ownerTunnel)
		if response.Status != 200 {
			t.Fatalf("claim: %d", response.Status)
		}
		return cookieOf(t, response)
	}
	one, two := claim(), claim()
	firstHost, firstSocket := fixture.Client(merged(ownerTunnel, map[string]string{"Cookie": one}))
	secondHost, secondSocket := fixture.Client(merged(ownerTunnel, map[string]string{"Cookie": two}))
	for _, each := range []host.Host{firstHost, secondHost} {
		if _, err := p2test.HoldIn(each, p2runtime.Access, delta.NewJsonObject(0), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	logout := func(cookie string, all bool, origin string) p2test.HTTPResult {
		path := "/owner/logout"
		if all {
			path = "/owner/logout-all"
		}
		return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(ownerTunnel, map[string]string{"Origin": origin, "Cookie": cookie})})
	}
	if got := logout(one, true, "https://foreign.test").Status; got != 403 {
		t.Fatalf("logout-all from a foreign origin: %d", got)
	}
	result := logout(one, false, ownerTunnel["Origin"])
	if result.Status != 200 || !strings.Contains(result.Headers.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %q", result.Status, result.Headers.Get("Set-Cookie"))
	}
	if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(firstSocket), nil }, 10*time.Second, "the signed-out device's socket to close"); err != nil {
		t.Fatal(err)
	}
	check := func(cookie string) int {
		return statusOf(t, port, "/owner/check", merged(ownerTunnel, map[string]string{"Cookie": cookie}))
	}
	if got := check(one); got != 401 {
		t.Fatalf("a signed-out device: %d", got)
	}
	if got := check(two); got != 200 {
		t.Fatalf("another device: %d", got)
	}
	if got := logout("wrong", true, ownerTunnel["Origin"]).Status; got != 403 {
		t.Fatalf("logout-all without an owner: %d", got)
	}
	if got := logout(two, true, ownerTunnel["Origin"]).Status; got != 200 {
		t.Fatalf("logout-all: %d", got)
	}
	if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(secondSocket), nil }, 10*time.Second, "the other device's socket to close"); err != nil {
		t.Fatal(err)
	}
	if got := check(two); got != 401 {
		t.Fatalf("after logout-all: %d", got)
	}
}

func TestOwnerLinkMintsAPrivateFragmentLinkFromTheRunningDevicePathWithoutABrowser(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"owner.test"}})
	port := fixture.Server.Port
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	link, err := p2runtime.OwnerLink(context.Background(), nil, env(map[string]string{"PORT": strconv.Itoa(port), "P2_PUBLIC_URL": "https://owner.test", "P2_DATA_DIR": fixture.DataDir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://owner.test/owner#") || !regexp.MustCompile(`^https://owner\.test/owner#[a-f0-9]{64}$`).MatchString(link) {
		t.Fatalf("the CLI emits %q", link)
	}
	body := tokenBody(link[strings.Index(link, "#")+1:])
	if got := claimWith(t, port, body, ownerTunnel).Status; got != 200 {
		t.Fatalf("claim: %d", got)
	}
	if got := claimWith(t, port, body, ownerTunnel).Status; got != 403 {
		t.Fatalf("a link works once: %d", got)
	}
	local := "http://127.0.0.1:" + strconv.Itoa(port)
	overridden, err := p2runtime.OwnerLink(context.Background(), []string{local}, env(map[string]string{"PORT": strconv.Itoa(port), "P2_PUBLIC_URL": "https://owner.test", "P2_DATA_DIR": fixture.DataDir}))
	if err != nil || !strings.HasPrefix(overridden, local+"/owner#") {
		t.Fatalf("override: %q %v", overridden, err)
	}
}

// The data directory is private: the server makes it 0700 and its owner key 0600 (the local owner is whoever can read
// that key).
func TestTheDataDirectoryIsPrivateTheServerMakesItMode0700AndItsOwnerKeyMode0600(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p2test.StartFixture(t, hosts.ServerOptions{DataDir: dataDir, Plugins: []string{"devices"}})
	for path, want := range map[string]os.FileMode{dataDir: 0o700, filepath.Join(dataDir, "owner-key"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: mode %v, %v (want %o)", path, info, err, want)
		}
	}
}

// Loopback alone is nobody: any user of the machine reaches 127.0.0.1.
func TestLoopbackWithoutACredentialIsNobodyPagesAskToSignInOwnerRoutesAndWebSocketsRefuse(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port := fixture.Server.Port
	page := httpGet(t, port, "/", p2test.HTTPOptions{})
	if page.Status != 401 || !strings.Contains(page.Body, "Sign in to Pi") || page.Headers.Get("Set-Cookie") != "" {
		t.Fatalf("/: %d %q %q", page.Status, page.Body, page.Headers.Get("Set-Cookie"))
	}
	for path, want := range map[string]int{"/s/anything": 401, "/owner/check": 401, "/__p2_dev": 403, "/__p2_tui": 401, "/health": 200} {
		if got := statusOf(t, port, path, nil); got != want {
			t.Fatalf("%s without a credential: %d, want %d", path, got, want)
		}
	}
	conn, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(port)+"/chord", nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("a WebSocket without a credential is accepted")
	}
	if response == nil || response.StatusCode != 403 {
		t.Fatalf("WebSocket refusal: %v", response)
	}
	_ = response.Body.Close()
	if got := statusOf(t, port, "/", map[string]string{"Cookie": fixture.Owner().Cookie}); got != 404 && got != 200 {
		t.Fatalf("/ as the local owner: %d", got)
	}
}

func TestLocalSignInAOneTimePortBoundChallengeAnsweredWithTheDataDirectorysOwnerKey(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"owner.test"}})
	port := fixture.Server.Port
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	keyText, err := os.ReadFile(filepath.Join(fixture.DataDir, "owner-key"))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(keyText))
	post := func(path string, body any, headers map[string]string) p2test.HTTPResult {
		t.Helper()
		encoded, _ := json.Marshal(body)
		return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(headers, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader(string(encoded))})
	}
	sameOrigin := map[string]string{"Origin": origin}
	challenge := func() string {
		t.Helper()
		response := post(p2runtime.LocalChallengePath, map[string]any{}, sameOrigin)
		var value struct {
			Challenge string `json:"challenge"`
		}
		if response.Status != 200 || json.Unmarshal([]byte(response.Body), &value) != nil || value.Challenge == "" {
			t.Fatalf("challenge: %d %q", response.Status, response.Body)
		}
		return value.Challenge
	}
	claim := func(challenge, proofKey string, proofPort int, headers map[string]string) p2test.HTTPResult {
		return post(p2runtime.LocalClaimPath, map[string]any{"challenge": challenge, "proof": p2runtime.LocalOwnerProof(proofKey, proofPort, challenge)}, headers)
	}
	// Not for another origin, a tunnel host or without the exact Origin.
	for name, headers := range map[string]map[string]string{"another origin": {"Origin": "http://127.0.0.1:1"}, "no origin": nil, "a tunnel host": ownerTunnel} {
		if got := post(p2runtime.LocalChallengePath, map[string]any{}, headers).Status; got != 403 {
			t.Fatalf("a challenge for %s: %d", name, got)
		}
	}
	first := challenge()
	if got := claim(first, strings.Repeat("f", 64), port, sameOrigin).Status; got != 403 {
		t.Fatalf("another key's answer: %d", got)
	}
	if got := claim(first, key, port, sameOrigin).Status; got != 403 {
		t.Fatalf("a challenge works once, even after a wrong answer: %d", got)
	}
	if got := claim(challenge(), key, port+1, sameOrigin).Status; got != 403 {
		t.Fatalf("an answer for another port: %d", got)
	}
	if got := claim(challenge(), key, port, ownerTunnel).Status; got != 403 {
		t.Fatalf("no local sign-in on a tunnel host: %d", got)
	}
	claimed := claim(challenge(), key, port, sameOrigin)
	if claimed.Status != 200 {
		t.Fatalf("claim: %d %s", claimed.Status, claimed.Body)
	}
	setCookie := claimed.Headers.Get("Set-Cookie")
	if !regexp.MustCompile(`^p2_owner_\d+=local\.[a-f0-9]{32}\.\d+\.[a-f0-9]{64}; HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000$`).MatchString(setCookie) {
		t.Fatalf("Set-Cookie %q", setCookie)
	}
	if strings.Contains(setCookie, key) {
		t.Fatal("the cookie is a session, never the key")
	}
	cookie := cookieOf(t, claimed)
	owner, _ := fixture.Client(map[string]string{"Cookie": cookie})
	devices, err := p2test.HoldIn(owner, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(devices.Member("canMint"), func(value delta.JsonValue) bool { return value == true }, 0); err != nil {
		t.Fatalf("a local session has local powers: %v", err)
	}
	if got := statusOf(t, port, "/owner/check", merged(ownerTunnel, map[string]string{"Cookie": cookie})); got != 401 {
		t.Fatalf("a local session copied to a tunnel host is nobody: %d", got)
	}
	// The client helper signs in the same way; another data directory gets a clear error, a missing key a reason.
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	other := t.TempDir()
	missing, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, target, other)
	if err != nil || missing.Kind != p2runtime.LocalUnavailable {
		t.Fatalf("missing key: %+v %v", missing, err)
	}
	if err := os.WriteFile(filepath.Join(other, "owner-key"), []byte(strings.Repeat("a", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatch, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, target, other)
	if err != nil || mismatch.Kind != p2runtime.LocalUnavailable || !strings.Contains(mismatch.Message, "does not use the data directory") {
		t.Fatalf("another key: %+v %v", mismatch, err)
	}
	if again, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, target, fixture.DataDir); err != nil || again.Kind != p2runtime.LocalSignedIn {
		t.Fatalf("the data directory's key: %+v %v", again, err)
	}
}

func TestAnOwnerLinkRedeemedOnLoopbackIsALocalSessionRenewedOnAppLoadsAndEndedBySignOutOfAllDevices(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	web := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &web})
	port := fixture.Server.Port
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	minted, err := fixture.HTTP("/owner-token", p2test.HTTPOptions{})
	if err != nil || minted.Status != 200 {
		t.Fatalf("mint: %+v %v", minted, err)
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(minted.Body), &token); err != nil {
		t.Fatal(err)
	}
	claimed := claimWith(t, port, tokenBody(token.Token), map[string]string{"Origin": origin})
	if claimed.Status != 200 || !strings.Contains(claimed.Headers.Get("Set-Cookie"), "=local.") || strings.Contains(claimed.Headers.Get("Set-Cookie"), "Secure") {
		t.Fatalf("claim: %d %q", claimed.Status, claimed.Headers.Get("Set-Cookie"))
	}
	cookie := cookieOf(t, claimed)
	browser, _ := fixture.Client(map[string]string{"Cookie": cookie})
	devices, err := p2test.HoldIn(browser, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(devices.Member("canMint"), func(value delta.JsonValue) bool { return value == true }, 0); err != nil {
		t.Fatal(err)
	}
	// Each app load renews the same session for another 30 days; other routes leave it alone.
	sessionID := regexp.MustCompile(`=local\.([a-f0-9]{32})\.`)
	loaded := httpGet(t, port, "/", p2test.HTTPOptions{Headers: map[string]string{"Cookie": cookie}})
	renewed := loaded.Headers.Get("Set-Cookie")
	if loaded.Status != 200 || !strings.HasSuffix(renewed, "SameSite=Lax; Path=/; Max-Age=2592000") ||
		sessionID.FindStringSubmatch(renewed) == nil || sessionID.FindStringSubmatch(renewed)[1] != sessionID.FindStringSubmatch(cookie)[1] {
		t.Fatalf("app load: %d %q (cookie %q)", loaded.Status, renewed, cookie)
	}
	if got := httpGet(t, port, "/owner/check", p2test.HTTPOptions{Headers: map[string]string{"Cookie": cookie}}).Headers.Get("Set-Cookie"); got != "" {
		t.Fatalf("other routes renew the cookie: %q", got)
	}
	if got := statusOf(t, port, "/", map[string]string{"Cookie": strings.SplitN(renewed, ";", 2)[0]}); got != 200 {
		t.Fatalf("the renewed cookie: %d", got)
	}
	// Sign-out of this local browser ends its cookie only; sign-out of all devices ends every local session too.
	tui, tuiSocket := fixture.Client(map[string]string{"Cookie": fixture.Owner().Cookie})
	if _, err := p2test.HoldIn(tui, p2runtime.Access, delta.NewJsonObject(0), "owner"); err != nil {
		t.Fatal(err)
	}
	logout := func(path, value string) p2test.HTTPResult {
		return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: map[string]string{"Origin": origin, "Cookie": value}})
	}
	if got := logout("/owner/logout", cookie).Status; got != 200 {
		t.Fatalf("logout: %d", got)
	}
	if got := statusOf(t, port, "/owner/check", map[string]string{"Cookie": cookie}); got != 401 {
		t.Fatalf("the signed-out local browser: %d", got)
	}
	if got := statusOf(t, port, "/owner/check", map[string]string{"Cookie": fixture.Owner().Cookie}); got != 200 {
		t.Fatalf("another local session: %d", got)
	}
	if p2test.SocketClosed(tuiSocket) {
		t.Fatal("other local sessions keep their links")
	}
	if got := logout("/owner/logout-all", fixture.Owner().Cookie).Status; got != 200 {
		t.Fatalf("logout-all: %d", got)
	}
	if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(tuiSocket), nil }, 10*time.Second, "the local session's socket to close"); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, port, "/owner/check", map[string]string{"Cookie": fixture.Owner().Cookie}); got != 401 {
		t.Fatalf("after logout-all: %d", got)
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	if again, err := p2runtime.LocalOwnerSignIn(context.Background(), nil, target, fixture.DataDir); err != nil || again.Kind != p2runtime.LocalSignedIn {
		t.Fatalf("the new key signs in: %+v %v", again, err)
	}
}

// Another user (no readable key) or another data directory gets a clear error from `p2 owner-link`, never a link.
func TestOwnerLinkNeedsTheServersDataDirectoryAndItsOwnerKey(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	other := t.TempDir()
	run := func(dataDir string) error {
		_, err := p2runtime.OwnerLink(context.Background(), nil, func(name string) string {
			return map[string]string{"PORT": strconv.Itoa(fixture.Server.Port), "P2_DATA_DIR": dataDir}[name]
		})
		return err
	}
	if err := run(other); err == nil || !regexp.MustCompile(`^Cannot read .*owner-key: local sign-in needs the user running the p2 server`).MatchString(err.Error()) {
		t.Fatalf("no key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "owner-key"), []byte(strings.Repeat("a", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(other); err == nil || !strings.Contains(err.Error(), "does not use the data directory") {
		t.Fatalf("another key: %v", err)
	}
	if err := run(fixture.DataDir); err != nil {
		t.Fatalf("the server's data directory: %v", err)
	}
}
