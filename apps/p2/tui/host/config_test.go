package host_test

// test/p2/tui.test.ts: the launcher's command line, private config, owner claim and sign-in (the first four tests of the file; the bare-host one is in
// tui/clienttest).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	devicescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	proxycontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker"
)

func TestTUIAuthOptionsPrivateConfigAndOwnerClaimCookieRoundTrip(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "p2-tui-config")
	parse := func(env map[string]string, args ...string) host.Args {
		t.Helper()
		parsed, err := host.ParseArgs(args, argsEnv(env))
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return parsed
	}
	refuses := func(want string, env map[string]string, args ...string) {
		t.Helper()
		if _, err := host.ParseArgs(args, argsEnv(env)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v: %v, want %q", args, err, want)
		}
	}
	if got := host.OriginOf(parse(nil).URL); got != "http://127.0.0.1:8788" {
		t.Fatalf("default origin %s", got)
	}
	if got := host.OriginOf(parse(map[string]string{"PORT": "9000", "P2_PUBLIC_URL": "https://remote.test"}).URL); got != "http://127.0.0.1:9000" {
		t.Fatalf("PORT origin %s", got)
	}
	link := "https://example.test/owner#" + strings.Repeat("a", 64)
	if got := parse(nil, "https://example.test", "--owner-link", link).OwnerLink; got != link {
		t.Fatalf("owner link %q", got)
	}
	refuses("Unknown TUI option", nil, "https://example.test", "--basic-auth", "fixture:password")
	refuses("this origin", nil, "https://elsewhere.test", "--owner-link", link)
	refuses("fragment", nil, "https://example.test", "--owner-link", "https://example.test/owner?token=secret")
	refuses("HTTPS", nil, "http://remote.test")
	refuses("without credentials", nil, "https://fixture:secret@example.test")
	// Through Radius: optionally the server's ID; Radius admits the owner, so no URL or link, and no owner link from env.
	radiusServer := "0b5a3e8e-6f1d-4c55-9d3a-2a7b1c9e4f10"
	if got := parse(nil, "--radius"); !got.Radius || got.RadiusServer != "" {
		t.Fatalf("--radius = %+v", got)
	}
	if got := parse(nil, "--radius", radiusServer, "--theme", "dark"); !got.Radius || got.RadiusServer != radiusServer {
		t.Fatalf("--radius <id> = %+v", got)
	}
	if got := parse(map[string]string{"P2_TUI_OWNER_LINK": link}, "--radius").OwnerLink; got != "" {
		t.Fatalf("radius with an env owner link: %q", got)
	}
	refuses("no URL", nil, "--radius", "https://example.test")
	refuses("no URL", nil, "https://example.test", "--radius", "--owner-link", link)
	if err := host.SaveConfig(host.Config{Connections: map[string]host.Connection{"https://example.test": {Credentials: host.Credentials{Cookie: "owner=fixture"}}}}, directory); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{filepath.Join(directory, "config.json"): 0o600, directory: 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: mode %v, %v; want %o", path, info, err, want)
		}
	}
	config, err := host.ReadConfig(directory)
	if err != nil || config.Connections["https://example.test"].Cookie != "owner=fixture" {
		t.Fatalf("read config = %+v, %v", config, err)
	}
	example, _ := url.Parse("https://example.test")
	headers := host.ConnectionHeaders(example, host.Credentials{Cookie: "owner=fixture"})
	if headers.Get("Origin") != "https://example.test" || headers.Get("Cookie") != "owner=fixture" || headers.Get("Authorization") != "" {
		t.Fatalf("headers = %v", headers)
	}
	var claimed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer claimed.Store(true)
		var body struct {
			Token string `json:"token"`
		}
		if request.Header.Get("Origin") != "http://"+request.Host || request.URL.Path != "/owner/claim" || request.Method != http.MethodPost ||
			json.NewDecoder(request.Body).Decode(&body) != nil || body.Token != "fixture-token" {
			http.Error(response, "unexpected claim", http.StatusBadRequest)
			return
		}
		response.Header().Set("Set-Cookie", "owner=fixture; HttpOnly; Path=/")
		_, _ = response.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	credentials, err := host.ClaimOwner(nil, context.Background(), target, "fixture-token", host.Credentials{})
	if err != nil || credentials.Cookie != "owner=fixture" || !claimed.Load() {
		t.Fatalf("claim = %+v, %v", credentials, err)
	}
}

func TestTUILoopbackAutomaticallySignsInAsTheDeviceOwnerWithoutALinkPrompt(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices", "proxy-marker"}})
	target, err := url.Parse("http://127.0.0.1:" + itoa(fixture.Server.Port))
	if err != nil {
		t.Fatal(err)
	}
	devices, err := p2test.HoldIn(fixture.Browser(), devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	minted, err := devices.Call("token", delta.NewJsonObject(0), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	associated, err := host.ClaimOwner(nil, context.Background(), target, minted.(*delta.JsonObject).Value("token").(string), host.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	config := host.Config{Connections: map[string]host.Connection{host.OriginOf(target): {Credentials: associated}}}
	admission, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target}, &config, func(string) (string, error) {
		return "", errors.New("Unexpected owner link prompt")
	})
	if err != nil || admission != (host.Admission{Role: "owner"}) {
		t.Fatalf("loopback uses the device path, not a cached remote cookie: %+v, %v", admission, err)
	}
	headers := host.ConnectionHeaders(target, admission.Credentials)
	client, _ := fixture.Client(map[string]string{"Origin": headers.Get("Origin")})
	owned, err := p2test.HoldIn(client, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(owned.Member("canMint"), func(value delta.JsonValue) bool { return value == true }, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAProxyMarkerRefusesTheTUIAutomaticPathAndFallsBackToALinkPrompt(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices", "proxy-marker"}})
	target, _ := url.Parse("http://127.0.0.1:" + itoa(fixture.Server.Port))
	marked := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.Header.Set(proxycontract.ProxyMarkerHeader, "1")
		return http.DefaultTransport.RoundTrip(request)
	})}
	prompted := false
	_, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: marked}, &host.Config{Connections: map[string]host.Connection{}}, func(string) (string, error) {
		prompted = true
		return "", errors.New("Link prompt required")
	})
	if err == nil || !strings.Contains(err.Error(), "Link prompt required") || !prompted {
		t.Fatalf("authenticate = %v (prompted %v)", err, prompted)
	}
}

func TestRemoteTUIPromptsForAnOwnerLinkSavesOnlyItsCookieAndReusesValidSavedCredentials(t *testing.T) {
	target, _ := url.Parse("https://tui.example.test")
	token := strings.Repeat("f", 64)
	var claims atomic.Int64
	remote := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" {
			t.Errorf("a request carries an Authorization header")
		}
		if request.URL.Path == "/owner/check" {
			return reply(200, `{"owner":true}`), nil
		}
		if request.URL.Path != "/owner/claim" {
			t.Errorf("unexpected request %s", request.URL.Path)
		}
		var body struct {
			Token string `json:"token"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || body.Token != token {
			t.Errorf("claim body token %q", body.Token)
		}
		claims.Add(1)
		return reply(200, "{}", "Set-Cookie", "owner=fixture; HttpOnly; Secure; SameSite=Lax; Path=/"), nil
	})}
	config := host.Config{Connections: map[string]host.Connection{}}
	admission, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: remote}, &config, func(string) (string, error) {
		return target.Scheme + "://" + target.Host + "/owner#" + token, nil
	})
	if err != nil || admission.Cookie != "owner=fixture" {
		t.Fatalf("admission = %+v, %v", admission, err)
	}
	if encoded, _ := json.Marshal(config); strings.Contains(string(encoded), token) {
		t.Fatalf("the config holds the token: %s", encoded)
	}
	directory := t.TempDir()
	if err := host.SaveConfig(config, directory); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(directory, "config.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config.json mode %v, %v", info, err)
	}
	saved, err := host.ReadConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: remote}, &saved, func(string) (string, error) {
		return "", errors.New("Unexpected repeat prompt")
	}); err != nil {
		t.Fatal(err)
	}
	if claims.Load() != 1 {
		t.Fatalf("claims = %d", claims.Load())
	}
}
