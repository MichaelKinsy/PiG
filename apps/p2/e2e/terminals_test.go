// Ports test/p2/terminals.test.ts (Pi main c23b9ec1): the terminal views against a real server and a real terminal host:
// guests are denied before a detached-host resource is acquired, the byte channel needs an owner and a live one-use grant,
// and terminals survive a compatible reload and a server restart, with controls scoped to their session.

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals"
	terminalscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// byteChannel is a viewer's terminal socket: its events and its output.
type byteChannel struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	events []terminals.TerminalEvent
	output []byte
}

func openByteChannel(t *testing.T, port int, token string) *byteChannel {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+itoa(port)+"/terminal?"+url.Values{"token": {token}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	channel := &byteChannel{conn: conn}
	go func() {
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			channel.mu.Lock()
			if kind == websocket.BinaryMessage {
				channel.output = append(channel.output, data...)
			} else if parsed, err := terminalEvent(data); err == nil && parsed.Type != "ping" {
				channel.events = append(channel.events, parsed)
			}
			channel.mu.Unlock()
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return channel
}

func terminalEvent(data []byte) (terminals.TerminalEvent, error) {
	parsed, err := chordjson.Decode(data)
	if err != nil {
		return terminals.TerminalEvent{}, err
	}
	if object, ok := parsed.(interface{ Value(string) any }); ok && object.Value("type") == "ping" {
		return terminals.TerminalEvent{Type: "ping"}, nil
	}
	return terminals.ParseEvent(parsed)
}

func (c *byteChannel) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.output)
}

func (c *byteChannel) find(match func(terminals.TerminalEvent) bool) (terminals.TerminalEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if match(event) {
			return event, true
		}
	}
	return terminals.TerminalEvent{}, false
}

func pollUntil(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("Timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// refusedUpgrade is the status a /terminal upgrade is refused with.
func refusedUpgrade(t *testing.T, port int, query string, headers map[string]string) int {
	t.Helper()
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	conn, response, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+itoa(port)+"/terminal"+query, header)
	if err == nil {
		_ = conn.Close()
		t.Fatal("Unexpectedly admitted terminal channel")
	}
	if response == nil {
		t.Fatalf("dial: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode
}

// shutdownTerminalHost stops the data directory's terminal host (it and its programs would live on).
func shutdownTerminalHost(t *testing.T, directory string) {
	t.Helper()
	conn, err := terminals.ConnectTerminalHost(context.Background(), directory)
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()
	frame, _ := terminals.EncodeFrame(terminals.TerminalRequest{Type: terminals.RequestShutdown}.JSON())
	_, _ = conn.Write(frame)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, _ = io.Copy(io.Discard, conn)
}

func TestTerminalsObjectsDenyGuestsBeforeAcquiringADetachedHostResource(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	info := delta.JsonObjectOf("app", "", "dataDir", t.TempDir(), "port", float64(0), "fake", true, "allowedHosts", []any{})
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{
		Facets: []host.Facet{terminals.ServerFacet},
		Stubs: []p2test.Stub{{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"info": info}}
		}}},
	})
	guest := fixture.Client(p2runtime.GuestPrincipal{ID: "g", Name: "Guest", Home: "s", Sessions: map[string]p2runtime.GuestMode{"s": "full"}, Revision: 1})
	view, err := guest.Hold(p2test.Background, terminalscontract.Terminals, delta.JsonObjectOf("session", "s", "view", randomView()), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, view), data.CodeForbidden)
}

func TestTerminalByteChannelsNeedAnOwnerAndALiveOneUseChord2Grant(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	t.Cleanup(func() { shutdownTerminalHost(t, fixture.DataDir) })
	client := fixture.Browser()
	sessions, err := p2test.HoldIn(client, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	view, err := p2test.HoldIn(client, terminalscontract.Terminals, delta.JsonObjectOf("session", session, "view", randomView()), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if got := refusedUpgrade(t, fixture.Server.Port, "", nil); got != http.StatusForbidden {
		t.Fatalf("no token: %d", got)
	}
	granted, err := view.Call("channel", delta.NewJsonObject(0), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	token := object(granted).Value("token").(string)
	query := "?" + url.Values{"token": {token}}.Encode()
	if got := refusedUpgrade(t, fixture.Server.Port, query, map[string]string{"Origin": "https://untrusted.invalid"}); got != http.StatusForbidden {
		t.Fatalf("untrusted origin: %d", got)
	}
	bytes := openByteChannel(t, fixture.Server.Port, token)
	if _, err := view.Call("list", delta.NewJsonObject(0), p2test.Background); err != nil {
		t.Fatal(err)
	}
	var list terminals.TerminalEvent
	pollUntil(t, func() bool {
		var ok bool
		list, ok = bytes.find(func(event terminals.TerminalEvent) bool { return event.Type == terminals.EventList })
		return ok
	})
	if len(list.Terminals) != 0 {
		t.Fatalf("list = %+v", list.Terminals)
	}
	if got := refusedUpgrade(t, fixture.Server.Port, query, nil); got != http.StatusForbidden {
		t.Fatalf("a used token: %d", got)
	}
	// Control JSON cannot be smuggled onto the dedicated byte channel.
	closed := make(chan struct{})
	bytes.conn.SetCloseHandler(func(int, string) error { close(closed); return nil })
	go func() {
		// The reader goroutine of the channel sees the close frame.
		_ = bytes.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"open","cwd":"/","sessionId":"`+session+`","cols":40,"rows":10}`))
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the byte channel stayed open for control JSON")
	}
}

func TestTerminalsSurviveCompatibleReloadAndServerRestartControlsStaySessionScoped(t *testing.T) {
	p2test.UseTerminalDaemon(t)
	root := t.TempDir()
	dataDir := root + "/data"
	workspace := root + "/workspace"
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownTerminalHost(t, dataDir) })
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func() *hosts.P2Server {
		zero, fake := 0, true
		server, err := hosts.StartServer(hosts.ServerOptions{DataDir: dataDir, Port: &zero, Fake: &fake, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
		if err != nil {
			t.Fatal(err)
		}
		return server
	}
	connect := func(server *hosts.P2Server) host.Host {
		conn, err := p2test.Dial(server.Port, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := p2test.ClientHost(conn)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	server := start()
	client := connect(server)
	defer func() { _ = server.Close(p2test.Background) }()
	sessions, err := p2test.HoldIn(client, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func() string {
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(created).Value("id").(string)
	}
	session, other := create(), create()
	hold := func(client host.Host, id string) *host.Reference {
		view, err := p2test.HoldIn(client, terminalscontract.Terminals, delta.JsonObjectOf("session", id, "view", randomView()), "owner")
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	channel := func(server *hosts.P2Server, view *host.Reference) *byteChannel {
		granted, err := view.Call("channel", delta.NewJsonObject(0), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return openByteChannel(t, server.Port, object(granted).Value("token").(string))
	}
	call := func(view *host.Reference, method string, args *delta.JsonObject) {
		t.Helper()
		if _, err := view.Call(method, args, p2test.Background); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	view := hold(client, session)
	bytes := channel(server, view)
	call(view, "theme", delta.JsonObjectOf("foreground", float64(0xeeeeee), "background", float64(0x111111), "scheme", "dark"))
	call(view, "open", delta.JsonObjectOf("cols", float64(80), "rows", float64(10), "conversation", float64(1)))
	var opened terminals.TerminalEvent
	pollUntil(t, func() bool {
		var ok bool
		opened, ok = bytes.find(func(event terminals.TerminalEvent) bool { return event.Type == terminals.EventAttached })
		return ok
	})
	if opened.Terminal.Cwd != workspace {
		real, _ := os.Readlink(workspace)
		if opened.Terminal.Cwd != real {
			t.Fatalf("cwd = %q, want %q", opened.Terminal.Cwd, workspace)
		}
	}
	id := opened.Terminal.ID
	pollUntil(t, func() bool { text := bytes.text(); return strings.HasSuffix(text, "$ ") || strings.HasSuffix(text, "# ") })
	_ = bytes.conn.WriteMessage(websocket.BinaryMessage, []byte("printf 'before-%s\\n' re''start\n"))
	pollUntil(t, func() bool { return strings.Contains(bytes.text(), "before-restart") })
	otherView := hold(client, other)
	_, err = otherView.Call("attach", delta.JsonObjectOf("terminalId", id), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeForbidden)
	if _, err := server.Reload([]string{"terminals"}, p2test.Background); err != nil {
		t.Fatal(err)
	}
	call(view, "resize", delta.JsonObjectOf("cols", float64(70), "rows", float64(12)))
	_ = client.Close(p2test.Background)
	_ = bytes.conn.Close()
	if err := server.Close(p2test.Background); err != nil {
		t.Fatal(err)
	}
	server = start()
	client = connect(server)
	view = hold(client, session)
	bytes = channel(server, view)
	call(view, "attach", delta.JsonObjectOf("terminalId", id))
	pollUntil(t, func() bool { return strings.Contains(bytes.text(), "before-restart") })
	_ = bytes.conn.WriteMessage(websocket.BinaryMessage, []byte("stty size; echo after-re''start\n"))
	pollUntil(t, func() bool { text := bytes.text(); return strings.Contains(text, "12 70") && strings.Contains(text, "after-restart") })
	call(view, "close", delta.JsonObjectOf("force", true))
	pollUntil(t, func() bool {
		_, ok := bytes.find(func(event terminals.TerminalEvent) bool { return event.Type == terminals.EventClosed })
		return ok
	})
}

func itoa(value int) string { return strconv.Itoa(value) }

func randomView() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}
