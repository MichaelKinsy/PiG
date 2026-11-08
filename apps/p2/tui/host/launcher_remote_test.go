package host_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/chord/delta"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/gorilla/websocket"
)

// tunnelTransport plays the TLS-terminating tunnel of a remote origin (Pi's `tunnel` of tui-guest.test.ts): requests and sockets go to the fixture's loopback
// port as `https://<host>` (Host and Origin as a tunnel presents them), so loopback's device-owner rule does not apply.
type tunnelTransport struct {
	port   int
	host   string
	cookie string
}

func (t *tunnelTransport) headers() http.Header {
	headers := http.Header{"Origin": {"https://" + t.host}}
	if t.cookie != "" {
		headers.Set("Cookie", t.cookie)
	}
	return headers
}

func (t *tunnelTransport) Request(ctx context.Context, path string, request host.Request) (*http.Response, error) {
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(t.port)+path, request.Body)
	if err != nil {
		return nil, err
	}
	req.Host = t.host
	for name, values := range t.headers() {
		req.Header[name] = values
	}
	for name, value := range request.Headers {
		req.Header.Set(name, value)
	}
	return (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
}

type tunnelSocket struct {
	mu        sync.Mutex
	conn      *websocket.Conn
	open      atomic.Bool
	cancelled bool
	write     sync.Mutex
}

func (s *tunnelSocket) Open() bool { return s.open.Load() }

func (s *tunnelSocket) Send(data []byte) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return io.ErrClosedPipe
	}
	s.write.Lock()
	defer s.write.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *tunnelSocket) Terminate() {
	s.mu.Lock()
	s.cancelled = true
	conn := s.conn
	s.mu.Unlock()
	s.open.Store(false)
	if conn != nil {
		_ = conn.Close()
	}
}

func (t *tunnelTransport) Socket(path string, events host.SocketEvents) host.Socket {
	socket := &tunnelSocket{}
	go func() {
		headers := t.headers()
		headers.Set("Host", t.host)
		conn, response, err := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).Dial("ws://127.0.0.1:"+strconv.Itoa(t.port)+path, headers)
		if err != nil {
			if response != nil {
				_ = response.Body.Close()
				if events.Refused != nil {
					events.Refused(response.StatusCode)
				}
			}
			events.Close()
			return
		}
		socket.mu.Lock()
		socket.conn = conn
		cancelled := socket.cancelled
		socket.mu.Unlock()
		if cancelled {
			_ = conn.Close()
			events.Close()
			return
		}
		socket.open.Store(true)
		events.Open()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			events.Message(data, kind == websocket.BinaryMessage)
		}
		socket.open.Store(false)
		_ = conn.Close()
		events.Close()
	}()
	return socket
}

// tunnelHTTP is an HTTP client for the sign-in requests of a tunnelled origin.
func tunnelHTTP(port int) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			next := r.Clone(r.Context())
			next.URL.Scheme, next.URL.Host = "http", "127.0.0.1:"+strconv.Itoa(port)
			next.Host = r.URL.Host
			return http.DefaultTransport.RoundTrip(next)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func sharedSession(t *testing.T, fixture *p2test.Fixture) (session string, sharing *chordhost.Reference, joinPath string) {
	t.Helper()
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session = created.(*delta.JsonObject).Value("id").(string)
	held, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(held.Member("links"), func(value delta.JsonValue) bool { items, _ := value.([]any); return len(items) > 0 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := links.([]any)[0].(*delta.JsonObject)
	link, err := url.Parse(first.Value("url").(string))
	if err != nil {
		t.Fatal(err)
	}
	return session, held, link.Path
}

func TestTUIAdmissionMatchesWebStrangersRefusedAssociatedOwnersAndShareGuestsAdmitted(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"tui.test"}})
	get := func(path string, headers map[string]string, options ...func(*p2test.HTTPOptions)) p2test.HTTPResult {
		t.Helper()
		request := p2test.HTTPOptions{Headers: headers}
		for _, option := range options {
			option(&request)
		}
		result, err := p2test.HTTP(fixture.Server.Port, path, request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	anonymous := get("/__p2_tui", map[string]string{"Host": "tui.test"})
	if anonymous.Status != 401 {
		t.Fatalf("a stranger: %d", anonymous.Status)
	}
	// Pi's server sent the TUI bundle (`export default ...`) to admitted visitors only and never to a stranger; here nothing is ever sent: the facets are compiled in
	// (D473), so what a stranger must not get is the same and there is nothing more to hand an admitted visitor.
	if strings.Contains(anonymous.Body, "export default") || strings.Contains(anonymous.Body, "compiledIn") {
		t.Fatalf("a stranger was told about the TUI: %q", anonymous.Body)
	}
	_, _, joinPath := sharedSession(t, fixture)
	token := joinPath[strings.LastIndex(joinPath, "/")+1:]
	body, _ := json.Marshal(map[string]string{"token": token, "name": "TUI guest"})
	joined := get("/join", map[string]string{"Host": "tui.test", "Origin": "https://tui.test", "Content-Type": "application/json"}, func(o *p2test.HTTPOptions) {
		o.Method = http.MethodPost
		o.Body = strings.NewReader(string(body))
	})
	if joined.Status != 200 {
		t.Fatalf("join: %d %s", joined.Status, joined.Body)
	}
	cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
	admitted := get("/__p2_tui", map[string]string{"Host": "tui.test", "Cookie": cookie})
	if admitted.Status != 200 {
		t.Fatalf("a share guest: %d", admitted.Status)
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(admitted.Body), &manifest); err != nil || manifest["compiledIn"] != true || manifest["bundle"] != nil || manifest["version"] != nil {
		t.Fatalf("the admission answer ships no code or version, only that the facets are compiled in: %q (%v)", admitted.Body, err)
	}
	if get("/__p2_tui", map[string]string{"Host": "tui.test", "Cookie": cookie}, func(o *p2test.HTTPOptions) { o.Method = http.MethodHead }).Status != 200 {
		t.Fatal("the launcher's HEAD poll is refused for a guest")
	}
	if get("/__p2_tui", map[string]string{"Host": "tui.test"}).Status != 401 {
		t.Fatal("without the cookie the stranger is still refused after a guest joined")
	}
}

func TestAGuestJoinsByLinkWithANameSeesItsSessionAsAGuestAndTheLauncherEndsOnRevocation(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"tui.test"}})
	session, sharing, joinPath := sharedSession(t, fixture)
	if !strings.HasPrefix(joinPath, "/join/") {
		t.Fatalf("share link path %q", joinPath)
	}
	target := mustURL("https://tui.test")
	config := host.Config{Connections: map[string]host.Connection{}}
	admission, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, JoinLink: host.OriginOf(target) + joinPath, HTTP: tunnelHTTP(fixture.Server.Port)}, &config,
		func(string) (string, error) { return "TUI guest", nil })
	if err != nil {
		t.Fatal(err)
	}
	if admission.Role != "guest" || admission.Session != session || !strings.HasPrefix(admission.Cookie, "p2_guest_") {
		t.Fatalf("admission = %+v", admission)
	}
	var ended, exited atomic.Int32
	l := startLauncher(t, launcherOptions{fixture: fixture, address: target, role: "guest", options: func(options *host.ClientOptions) {
		options.Transport = &tunnelTransport{port: fixture.Server.Port, host: "tui.test", cookie: admission.Cookie}
		options.Credentials = host.Credentials{Cookie: admission.Cookie}
		options.Poll = 100 * time.Millisecond
		options.Reconnect = 20 * time.Millisecond
		options.OnAccessEnded = func() { ended.Add(1) }
		options.OnExit = func() { exited.Add(1) }
	}})
	pollFor(t, 20*time.Second, func() bool {
		return strings.Contains(l.terminal.screen(), "guest · TUI guest (unverified) · view only")
	})
	pollFor(t, 20*time.Second, func() bool { return l.client.Snapshot().Session == session })
	if encoded, _ := json.Marshal(l.client.Snapshot()); strings.Contains(string(encoded), admission.Cookie) {
		t.Fatal("the cookie reached the runtime")
	}
	// The owner resets the link: the guest's connection ends and the launcher confirms it itself.
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view", "reset", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	pollFor(t, 15*time.Second, func() bool { return ended.Load() == 1 })
	if exited.Load() != 1 {
		t.Fatalf("exited = %d", exited.Load())
	}
}
