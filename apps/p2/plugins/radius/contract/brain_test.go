package contract

import (
	"encoding/json"
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

// Ports the reconnection cases of test/p2/radius.test.ts (Pi main c23b9ec1): a relay whose network can fail. `silent` drops
// everything in both directions without closing (as a lost network does), `stallHandshakes` leaves upgrade requests
// unanswered, `drop()` closes the host socket. `occupied` answers upgrades with 409 as while another host is connected, and
// `replace()` closes the host as a relay does when another brain with the same server ID takes over. `urls` records every
// upgrade request.
type flakyRelay struct {
	t  *testing.T
	mu sync.Mutex

	silent, stallHandshakes, occupied bool
	connections, pings, stalled       int
	urls                              []string

	host     *websocket.Conn
	hostSend sync.Mutex
	held     []net.Conn
	conns    []*websocket.Conn
	server   *http.Server

	statusMu sync.Mutex
	statuses []Status
	brain    *Brain
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

func startFlakyRelay(t *testing.T) *flakyRelay {
	t.Helper()
	f := &flakyRelay{t: t}
	upgrader := websocket.Upgrader{Subprotocols: []string{"pi-server.host.v4"}, CheckOrigin: func(*http.Request) bool { return true }}
	f.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
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
	if err := WriteConfig(directory, Config{
		Gateway: "http://" + listener.Addr().String(), ServerID: NewServerID(),
		Credential: ai.OAuthCredentials{Access: "fixture-token", Refresh: "fixture-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
	f.brain = NewBrain(BrainOptions{
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

// Ports radius.test.ts "Radius brain connects under its name, the device's at first, and a rename goes with the next connect" (Pi 5c37d3be1).
func TestBrainConnectsUnderItsNameTheDevicesAtFirstAndARenameGoesWithTheNextConnect(t *testing.T) {
	f := startFlakyRelay(t)
	names := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out []string
		for _, raw := range f.urls {
			parsed, _ := url.Parse(raw)
			out = append(out, parsed.Query().Get("name"))
		}
		return out
	}
	device := DeviceName()
	f.brain.Start()
	waitFor(t, "the first connection", func() bool { return f.connected() == 1 })
	if got := names(); len(got) != 1 || got[0] != device {
		t.Fatalf("names = %q, want [%q]", got, device)
	}
	// The first connect created identity.json with the device's name; it stays the brain's until changed here.
	if identity, ok := ReadBrainIdentity(f.brain.directory); !ok || identity.Name != device {
		t.Fatalf("identity = %v, %v", identity, ok)
	}
	if err := WriteBrainIdentity(f.brain.directory, BrainIdentity{Name: "Studio brain"}); err != nil {
		t.Fatal(err)
	}
	f.closeHost(1001, "")
	waitFor(t, "the second connection", func() bool { return f.connected() == 2 })
	if got := names(); got[len(got)-1] != "Studio brain" {
		t.Fatalf("names = %q", got)
	}
	var last Status
	for _, status := range f.statusList() {
		if status.Kind == StatusConnected {
			last = status
		}
	}
	if last.Name != "Studio brain" {
		t.Fatalf("connected status name = %q", last.Name)
	}
}
