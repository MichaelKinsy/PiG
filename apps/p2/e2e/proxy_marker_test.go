// Ports test/p2/proxy-marker.test.ts (Pi main c23b9ec1): proxy marking is never authentication, including through a
// loopback Unix-socket bridge in front of the server.

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	devicescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	logincontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	proxycontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/gorilla/websocket"
)

var proxied = map[string]string{"Host": "p2.proxy.test", "Origin": "https://p2.proxy.test", proxycontract.ProxyMarkerHeader: "1"}

// refused dials path with headers and expects the handshake's 403.
func refused(t *testing.T, port int, path string, headers map[string]string) {
	t.Helper()
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	conn, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+itoa(port)+path, header)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("%s: a stranger was admitted to a WebSocket", path)
	}
	if response == nil || response.StatusCode != 403 {
		t.Fatalf("%s: handshake %v, want 403 (%v)", path, response, err)
	}
	_ = response.Body.Close()
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestProxyMarkingIsNeverAuthenticationIncludingTheLoopbackUnixSocketBridge(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"p2.proxy.test"}})
	port := fixture.Server.Port
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "PRIVATE catalogue sentinel"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	source := filepath.Join(fixture.Workspace, "private.txt")
	if err := os.WriteFile(source, []byte("PRIVATE download payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		store, err := ctx.Get(filescontract.DownloadStore, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return store.Call("save", delta.JsonObjectOf("session", session, "path", source), ctx)
	}, p2test.Background, host.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	file := object(saved).Value("id").(string)
	devices, err := p2test.HoldIn(owner, devicescontract.Devices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	signed := claimWith(t, port, tokenBody(mint(t, devices)), merged(proxied, map[string]string{}))
	if signed.Status != 200 {
		t.Fatalf("claim: %d %s", signed.Status, signed.Body)
	}
	cookie := cookieOf(t, signed)

	// The loopback Unix-socket bridge: bytes from the socket to the server's port.
	socketPath := filepath.Join(fixture.DataDir, "proxy.sock")
	bridge, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			client, err := bridge.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				upstream, err := net.Dial("tcp", "127.0.0.1:"+itoa(port))
				if err != nil {
					return
				}
				defer upstream.Close()
				finished := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(upstream, client); finished <- struct{}{} }()
				go func() { _, _ = io.Copy(client, upstream); finished <- struct{}{} }()
				<-finished
			}()
		}
	}()
	t.Cleanup(func() { _ = bridge.Close(); <-done })
	viaBridge := func(path string) (int, string) {
		t.Helper()
		client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}}
		request, err := http.NewRequest(http.MethodGet, "http://p2.proxy.test"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range proxied {
			if name == "Host" {
				request.Host = value
				continue
			}
			request.Header.Set(name, value)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}

	t.Run("a bridge request without a cookie is a stranger, not the machine owner", func(t *testing.T) {
		code, body := viaBridge("/")
		if code != 401 || !strings.Contains(body, "Sign in to Pi") {
			t.Fatalf("/ through the bridge: %d %s", code, body)
		}
		if strings.Contains(body, "PRIVATE") || strings.Contains(body, "/__p2_web/") {
			t.Fatal("the sign-in page leaks the app")
		}
		if code, _ := viaBridge("/owner-token"); code != 403 {
			t.Fatalf("/owner-token through the bridge: %d", code)
		}
	})
	t.Run("a forged marker can only remove localhost/device trust", func(t *testing.T) {
		for _, marker := range []string{"1", "forged", ""} {
			if got := statusOf(t, port, "/owner-token", map[string]string{proxycontract.ProxyMarkerHeader: marker}); got != 403 {
				t.Fatalf("/owner-token with marker %q: %d", marker, got)
			}
			if got := statusOf(t, port, "/", merged(proxied, map[string]string{proxycontract.ProxyMarkerHeader: marker})); got != 401 {
				t.Fatalf("/ with marker %q: %d", marker, got)
			}
		}
		for _, path := range []string{"/chord", "/terminal"} {
			refused(t, port, path, proxied)
		}
	})
	t.Run("strangers cannot obtain application modules, sessions, files or upload anything", func(t *testing.T) {
		for _, path := range []string{"/", "/s/" + session, "/__p2_web", "/__p2_web/app-ANY.js",
			"/download?session=" + session + "&id=" + file, "/workspace-file?session=" + session + "&path=private.txt",
			"/artifact?session=" + session + "&name=private&version=1"} {
			response := httpGet(t, port, path, p2test.HTTPOptions{Headers: proxied})
			if response.Status != 401 && response.Status != 403 {
				t.Fatalf("%s: %d", path, response.Status)
			}
			if strings.Contains(response.Body, "PRIVATE") || strings.Contains(response.Body, session) {
				t.Fatalf("%s leaks: %s", path, response.Body)
			}
		}
		for _, path := range []string{"/upload?session=" + session + "&name=intrude.txt", "/workspace-upload?session=" + session + "&name=intrude.txt"} {
			response := httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: proxied, Body: strings.NewReader("intrusion")})
			if response.Status != 403 || strings.Contains(response.Body, "PRIVATE") {
				t.Fatalf("%s: %d %s", path, response.Status, response.Body)
			}
		}
	})
	t.Run("only a cookie grants proxied owner access, including provider controls", func(t *testing.T) {
		remote, _ := fixture.Client(merged(proxied, map[string]string{"Cookie": cookie}))
		held, err := p2test.HoldIn(remote, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p2test.Until(held.Member("sessions"), func(value delta.JsonValue) bool {
			for _, each := range list(value) {
				if object(each).Value("id") == session {
					return true
				}
			}
			return false
		}, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := p2test.HoldIn(remote, logincontract.ProviderLogin, delta.NewJsonObject(0), "owner"); err != nil {
			t.Fatal(err)
		}
		if got := statusOf(t, port, "/owner-token", merged(proxied, map[string]string{"Cookie": cookie})); got != 403 {
			t.Fatalf("/owner-token with a proxied cookie: %d", got)
		}
	})
	t.Run("guests still enter only through a share link, independently of the marker", func(t *testing.T) {
		sharing, err := p2test.HoldIn(fixture.Browser(), sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }, 0)
		if err != nil {
			t.Fatal(err)
		}
		joinBody, _ := json.Marshal(map[string]any{"token": object(list(links)[0]).Value("token"), "name": "Guest"})
		joined := httpGet(t, port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(proxied, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader(string(joinBody))})
		if joined.Status != 200 {
			t.Fatalf("join: %d %s", joined.Status, joined.Body)
		}
		guestCookie := cookieOf(t, joined)
		if got := statusOf(t, port, "/download?session="+session+"&id="+file, merged(proxied, map[string]string{"Cookie": guestCookie})); got != 200 {
			t.Fatalf("a guest's download: %d", got)
		}
		if got := statusOf(t, port, "/owner-token", merged(proxied, map[string]string{"Cookie": guestCookie})); got != 403 {
			t.Fatalf("/owner-token as a guest: %d", got)
		}
	})
}
