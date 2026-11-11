package contract

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/gorilla/websocket"
)

// Ports the reconnection cases of test/p2/radius.test.ts (Pi main b22e525b3): a relay whose network can fail. `silent` drops
// everything in both directions without closing (as a lost network does), `stallHandshakes` leaves upgrade requests
// unanswered, `drop()` closes the host socket. `occupied` answers upgrades with 409 as while another host is connected, and
// `replace()` closes the host as a relay does when another brain with the same server ID takes over. `urls` records every
// upgrade request, `renames` every name the brain sent with `PUT /v1/servers/<id>/name`, which answers `renameStatus`.
// The brain starts under `name`.
type flakyRelay struct {
	t  *testing.T
	mu sync.Mutex

	silent, stallHandshakes, occupied bool
	connections, pings, stalled       int
	urls                              []string
	// renames is every name the brain sent with `PUT /v1/servers/<id>/name`, which answers renameStatus.
	renames      []string
	renameStatus int

	host     *websocket.Conn
	hostSend sync.Mutex
	held     []net.Conn
	conns    []*websocket.Conn
	server   *http.Server

	statusMu  sync.Mutex
	statuses  []Status
	brain     *Brain
	directory string
}

func (f *flakyRelay) snapshot() (silent bool, connections, pings int, urls []string, stalled int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.silent, f.connections, f.pings, append([]string(nil), f.urls...), f.stalled
}

func (f *flakyRelay) set(change func()) {
	f.mu.Lock()
	change()
	f.mu.Unlock()
}

func (f *flakyRelay) statusList() []Status {
	f.statusMu.Lock()
	defer f.statusMu.Unlock()
	return append([]Status(nil), f.statuses...)
}

func (f *flakyRelay) connected() int {
	count := 0
	for _, status := range f.statusList() {
		if status.Kind == StatusConnected {
			count++
		}
	}
	return count
}

func (f *flakyRelay) errors() []string {
	var out []string
	for _, status := range f.statusList() {
		if status.Kind == StatusRetrying {
			out = append(out, status.Error)
		}
	}
	return out
}

func (f *flakyRelay) replaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, raw := range f.urls {
		parsed, _ := url.Parse(raw)
		out = append(out, parsed.Query().Get("replace"))
	}
	return out
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *flakyRelay) renameList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.renames...)
}

func (f *flakyRelay) snapshotURLs() []string {
	_, _, _, urls, _ := f.snapshot()
	return urls
}

// nameQueries is the `name` parameter of every upgrade request, in order ("" when absent).
func (f *flakyRelay) nameQueries() []string {
	var out []string
	for _, raw := range f.snapshotURLs() {
		parsed, err := url.Parse(raw)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, parsed.Query().Get("name"))
	}
	return out
}

// lastConnected is the latest connected status.
func (f *flakyRelay) lastConnected() Status {
	var last Status
	for _, status := range f.statusList() {
		if status.Kind == StatusConnected {
			last = status
		}
	}
	return last
}

func startFlakyRelay(t *testing.T) *flakyRelay { return startFlakyRelayNamed(t, "") }

// startFlakyRelayNamed is startFlakyRelay with a brain that starts under name ("": none).
func startFlakyRelayNamed(t *testing.T, name string) *flakyRelay {
	t.Helper()
	f := &flakyRelay{t: t, renameStatus: http.StatusNoContent}
	upgrader := websocket.Upgrader{Subprotocols: []string{"pi-server.host.v4"}, CheckOrigin: func(*http.Request) bool { return true }}
	f.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
			if request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/v1/servers/") && strings.HasSuffix(request.URL.Path, "/name") {
				raw, _ := io.ReadAll(request.Body)
				var body struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(raw, &body)
				f.mu.Lock()
				f.renames = append(f.renames, body.Name)
				status := f.renameStatus
				f.mu.Unlock()
				w.WriteHeader(status)
				return
			}
			status := http.StatusNotFound
			if request.URL.Path == "/v1/context" {
				status = http.StatusOK
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"actor":        map[string]any{"principal_id": "usr_1", "principal_kind": "user"},
				"organization": map[string]any{"id": "org_1"},
			})
			return
		}
		f.mu.Lock()
		f.urls = append(f.urls, request.URL.RequestURI())
		occupied, stall := f.occupied, f.stallHandshakes
		if stall {
			f.stalled++
		}
		f.mu.Unlock()
		if occupied {
			body := `{"error":"A server host is already connected"}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(body))
			return
		}
		if stall {
			hijacked, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				f.mu.Lock()
				f.held = append(f.held, hijacked)
				f.mu.Unlock()
			}
			return
		}
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.connections++
		f.host = conn
		f.conns = append(f.conns, conn)
		f.mu.Unlock()
		go func() {
			for {
				kind, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				f.mu.Lock()
				silent := f.silent
				f.mu.Unlock()
				if silent || kind != websocket.TextMessage {
					continue
				}
				var message struct{ Type string }
				if json.Unmarshal(data, &message) != nil || message.Type != "ping" {
					continue
				}
				f.mu.Lock()
				f.pings++
				f.mu.Unlock()
				f.hostSend.Lock()
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"version":2,"type":"pong"}`))
				f.hostSend.Unlock()
			}
		}()
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = f.server.Serve(listener) }()
	directory := t.TempDir()
	f.directory = directory
	if err := WriteConfig(directory, Config{
		Gateway: "http://" + listener.Addr().String(), ServerID: NewServerID(),
		Credential: ai.OAuthCredentials{Access: "fixture-token", Refresh: "fixture-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
	f.brain = NewBrain(BrainOptions{
		Name:      name,
		Directory: directory,
		Port:      func() int { return 1 },
		Relay:     NewRelayAccess(),
		OnStatus: func(status Status) {
			f.statusMu.Lock()
			f.statuses = append(f.statuses, status)
			f.statusMu.Unlock()
		},
		// The silence timeout leaves ample margin for scheduling stalls on a loaded machine.
		Timing: Timing{HeartbeatIdle: 250 * time.Millisecond, HeartbeatTimeout: 2 * time.Second, HandshakeTimeout: 300 * time.Millisecond},
	})
	t.Cleanup(func() {
		f.brain.Close()
		f.mu.Lock()
		for _, conn := range f.conns {
			_ = conn.Close()
		}
		for _, conn := range f.held {
			_ = conn.Close()
		}
		f.mu.Unlock()
		_ = f.server.Close()
	})
	return f
}

func (f *flakyRelay) closeHost(code int, reason string) {
	f.mu.Lock()
	host := f.host
	f.mu.Unlock()
	if host == nil {
		return
	}
	f.hostSend.Lock()
	defer f.hostSend.Unlock()
	_ = host.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(5*time.Second))
}

func TestBrainPingsAQuietRelayAndReconnectsWhenTheNetworkGoesSilent(t *testing.T) {
	f := startFlakyRelay(t)
	f.brain.Start()
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	// Answered pings keep the connection well past the silence timeout.
	time.Sleep(3 * time.Second)
	if _, connections, pings, _, _ := f.snapshot(); pings < 3 || connections != 1 || f.connected() != 1 {
		t.Fatalf("pings %d, connections %d, connected %d", pings, connections, f.connected())
	}
	// A lost network: no close, just no traffic. The brain gives up on the socket and connects again.
	f.set(func() { f.silent = true })
	waitFor(t, "a retry", func() bool { return len(f.errors()) > 0 })
	if first := f.errors()[0]; !strings.Contains(first, "no message from Radius") {
		t.Fatalf("first error = %q", first)
	}
	f.set(func() { f.silent = false })
	waitFor(t, "the second connection", func() bool { return f.connected() == 2 })
	if _, connections, _, _, _ := f.snapshot(); connections != 2 {
		t.Fatalf("connections = %d", connections)
	}
}

func TestBrainReconnectsAfterTheRelayDropsItOrAHandshakeHangs(t *testing.T) {
	f := startFlakyRelay(t)
	f.set(func() { f.stallHandshakes = true })
	f.brain.Start()
	waitFor(t, "a stalled handshake", func() bool { _, _, _, _, stalled := f.snapshot(); return stalled >= 1 })
	waitFor(t, "a retry", func() bool { return len(f.errors()) > 0 })
	f.set(func() { f.stallHandshakes = false })
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	f.closeHost(1001, "")
	waitFor(t, "the second connection", func() bool { return f.connected() == 2 })
	if _, connections, _, _, _ := f.snapshot(); connections != 2 {
		t.Fatalf("connections = %d", connections)
	}
}

func TestBrainReplacesItsLostHostSocketAndYieldsToAnotherBrainThatReplacedIt(t *testing.T) {
	f := startFlakyRelay(t)
	f.brain.Start()
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	if got := f.replaces(); len(got) != 1 || got[0] != "1" {
		t.Fatalf("replaces = %q", got)
	}
	// Another brain with this server ID took over. This one waits, without replacing, until the relay is free again.
	f.set(func() { f.occupied = true })
	f.closeHost(1012, "Server host replaced")
	waitFor(t, "three retries", func() bool { return len(f.errors()) >= 3 })
	for _, replace := range f.replaces()[1:] {
		if replace != "" {
			t.Fatalf("replaces = %q", f.replaces())
		}
	}
	errors := f.errors()
	if !strings.Contains(errors[0], "another p2") {
		t.Fatalf("first error = %q", errors[0])
	}
	for _, each := range errors[1:] {
		if !strings.HasPrefix(each, "409: ") {
			t.Fatalf("error = %q", each)
		}
	}
	f.set(func() { f.occupied = false })
	waitFor(t, "the second connection", func() bool { return f.connected() == 2 })
	// Once connected, a later loss reconnects by replacing again.
	_, _, _, urls, _ := f.snapshot()
	f.closeHost(1001, "")
	waitFor(t, "the third connection", func() bool { return f.connected() == 3 })
	if got := f.replaces()[len(urls):]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("replaces after the loss = %q", got)
	}
}

func TestBrainKeepsReplacingAgainstARelayThatIgnoresReplaceAndAnswers409(t *testing.T) {
	f := startFlakyRelay(t)
	f.set(func() { f.occupied = true })
	f.brain.Start()
	waitFor(t, "two attempts", func() bool { _, _, _, urls, _ := f.snapshot(); return len(urls) >= 2 })
	// The old host is still held: a plain conflict, not another brain, so the brain neither yields nor says it does.
	for _, replace := range f.replaces()[:2] {
		if replace != "1" {
			t.Fatalf("replaces = %q", f.replaces())
		}
	}
	waitFor(t, "a retry", func() bool { return len(f.errors()) > 0 })
	for _, each := range f.errors() {
		if !strings.HasPrefix(each, "409: ") {
			t.Fatalf("error = %q", each)
		}
	}
	f.set(func() { f.occupied = false })
	waitFor(t, "the connection", func() bool { return f.connected() == 1 })
}

// Ports "Radius brain connects under the server's name and sends a rename at once, without reconnecting"
// (test/p2/radius.test.ts, Pi main b22e525b3).
func TestBrainConnectsUnderTheServersNameAndSendsARenameAtOnceWithoutReconnecting(t *testing.T) {
	f := startFlakyRelayNamed(t, "Studio")
	names := f.nameQueries
	f.brain.Start()
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	if got := names(); len(got) != 1 || got[0] != "Studio" {
		t.Fatalf("names = %q, want [Studio]", got)
	}
	if last := f.lastConnected(); last.Name != "Studio" {
		t.Fatalf("last connected status = %+v", last)
	}
	// Radius has the name the connect carried: nothing to send.
	f.brain.Rename("Studio")
	f.brain.Rename("Lab")
	waitFor(t, "the rename", func() bool { return len(f.renameList()) == 1 })
	if got := f.renameList(); got[0] != "Lab" {
		t.Fatalf("renames = %q, want [Lab]", got)
	}
	if _, connections, _, _, _ := f.snapshot(); connections != 1 {
		t.Fatalf("connections = %d, want 1: a rename must not reconnect", connections)
	}
	// A relay without the endpoint gets the name with the next connect.
	f.set(func() { f.renameStatus = http.StatusNotFound })
	f.brain.Rename("Attic")
	waitFor(t, "the refused rename", func() bool { return len(f.renameList()) == 2 })
	f.closeHost(1001, "")
	waitFor(t, "the second connection", func() bool { return f.connected() == 2 })
	if got := names(); got[len(got)-1] != "Attic" {
		t.Fatalf("names = %q, want the last to be Attic", got)
	}
	// A rename while offline goes with the next connect, without a request of its own.
	f.set(func() { f.silent = true })
	waitFor(t, "the lost connection", func() bool {
		statuses := f.statusList()
		return statuses[len(statuses)-1].Kind == StatusRetrying
	})
	f.brain.Rename("Cellar")
	f.set(func() { f.silent = false })
	waitFor(t, "the third connection", func() bool { return f.connected() == 3 })
	if got := names(); got[len(got)-1] != "Cellar" {
		t.Fatalf("names = %q, want the last to be Cellar", got)
	}
	if got := f.renameList(); len(got) != 2 {
		t.Fatalf("renames = %q, want the two sent while connected", got)
	}
}

// Ports "Radius brain without a name connects without one, and sends its first name once connected" (test/p2/radius.test.ts,
// Pi main b22e525b3).
func TestBrainWithoutANameConnectsWithoutOneAndSendsItsFirstNameOnceConnected(t *testing.T) {
	f := startFlakyRelay(t)
	f.brain.Start()
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	parsed, err := url.Parse(f.snapshotURLs()[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Has("name") {
		t.Fatalf("the connect carried a name: %s", parsed.RawQuery)
	}
	f.brain.Rename("Studio")
	waitFor(t, "the first name", func() bool { return len(f.renameList()) == 1 })
	if got := f.renameList(); got[0] != "Studio" {
		t.Fatalf("renames = %q, want [Studio]", got)
	}
}
