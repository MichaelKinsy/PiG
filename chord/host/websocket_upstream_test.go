package host

// Ports packages/chord/test/websocket.test.ts: a browser-style client host over a real WebSocket (127.0.0.1), accepted
// by AcceptWebSockets (websocket.ts) and stamped by its authenticator. Pi uses `ws`'s WebSocketServer and the global
// WebSocket; here gorilla/websocket behind WebSocketServerLike and WebSocketLike adapters with the browser socket's
// readyState, open/message/close events and text frames.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Browser WebSocket readyState values.
const (
	wsConnecting = 0
	wsOpen       = 1
	wsClosing    = 2
	wsClosed     = 3
)

// wsSocket is a WebSocketLike over a gorilla connection: a browser WebSocket's surface.
type wsSocket struct {
	mu        sync.Mutex
	state     int
	conn      *websocket.Conn
	closeWant bool
	listeners map[string][]func(WebSocketMessage)
	writeMu   sync.Mutex
	closed    sync.Once
}

func (s *wsSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Send sends a string as a text message and bytes as a binary message.
func (s *wsSocket) Send(data any) error {
	s.mu.Lock()
	conn, state := s.conn, s.state
	s.mu.Unlock()
	if state != wsOpen {
		return errors.New("WebSocket is not open")
	}
	kind, payload := websocket.TextMessage, []byte(nil)
	switch data := data.(type) {
	case string:
		payload = []byte(data)
	case []byte:
		kind, payload = websocket.BinaryMessage, data
	default:
		return errors.New("WebSocket sends strings and bytes")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteMessage(kind, payload)
}

func (s *wsSocket) Close(code int, reason string) {
	s.mu.Lock()
	if s.state >= wsClosing {
		s.mu.Unlock()
		return
	}
	conn := s.conn
	s.state = wsClosing
	s.closeWant = true
	s.mu.Unlock()
	if conn == nil {
		// Still connecting: the dial closes it.
		return
	}
	if code == 0 {
		code = websocket.CloseNormalClosure
	}
	s.writeMu.Lock()
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	s.writeMu.Unlock()
	_ = conn.Close()
}

func (s *wsSocket) AddEventListener(typ string, listener func(event WebSocketMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners == nil {
		s.listeners = map[string][]func(WebSocketMessage){}
	}
	s.listeners[typ] = append(s.listeners[typ], listener)
}

func (s *wsSocket) emit(typ string, event WebSocketMessage) {
	s.mu.Lock()
	listeners := append([]func(WebSocketMessage){}, s.listeners[typ]...)
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
}

// opened marks the socket open on conn; false when it was closed while connecting.
func (s *wsSocket) opened(conn *websocket.Conn) bool {
	s.mu.Lock()
	s.conn = conn
	if s.closeWant {
		s.mu.Unlock()
		_ = conn.Close()
		s.ended()
		return false
	}
	s.state = wsOpen
	s.mu.Unlock()
	return true
}

// read delivers text messages as strings and binary messages as bytes (binaryType "arraybuffer") until the connection
// ends, then the close event.
func (s *wsSocket) read() {
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			s.ended()
			return
		}
		if kind == websocket.BinaryMessage {
			s.emit("message", WebSocketMessage{Data: data})
			continue
		}
		s.emit("message", WebSocketMessage{Data: string(data)})
	}
}

func (s *wsSocket) ended() {
	s.closed.Do(func() {
		s.mu.Lock()
		s.state = wsClosed
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		s.emit("close", WebSocketMessage{})
	})
}

// dialWebSocket is `new WebSocket(url)`: connecting at once, open (or close) later.
func dialWebSocket(url string) *wsSocket {
	socket := &wsSocket{state: wsConnecting}
	go func() {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			socket.ended()
			return
		}
		if !socket.opened(conn) {
			return
		}
		socket.emit("open", WebSocketMessage{})
		socket.read()
	}()
	return socket
}

// wsServer is `ws`'s WebSocketServer on 127.0.0.1: each upgraded connection is emitted as "connection" with its
// request, already open.
type wsServer struct {
	mu        sync.Mutex
	listeners []func(socket WebSocketLike, request *http.Request)
	sockets   []*wsSocket
	http      *httptest.Server
}

func (s *wsServer) On(event string, listener func(socket WebSocketLike, request *http.Request)) {
	if event != "connection" {
		return
	}
	s.mu.Lock()
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
}

func startWebSocketServer(t *testing.T) *wsServer {
	t.Helper()
	server := &wsServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		socket := &wsSocket{}
		socket.opened(conn)
		server.mu.Lock()
		server.sockets = append(server.sockets, socket)
		listeners := append([]func(WebSocketLike, *http.Request){}, server.listeners...)
		server.mu.Unlock()
		for _, listener := range listeners {
			listener(socket, r)
		}
		socket.read()
	}))
	t.Cleanup(func() {
		server.mu.Lock()
		sockets := append([]*wsSocket{}, server.sockets...)
		server.mu.Unlock()
		for _, socket := range sockets {
			socket.Close(0, "")
		}
		server.http.Close()
	})
	return server
}

func (s *wsServer) url(query string) string {
	return "ws" + strings.TrimPrefix(s.http.URL, "http") + "/?" + query
}

// wsBoard is websocket.test.ts:17-26 Board.
var wsBoard = Remote("t.board", RemoteDef{
	Version: 1,
	Input:   tObject(),
	Host:    NewHostNamed("server"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"notes", tArray(tString())}},
		Methods: Fields[MethodSpec]{{"add", Method(tString(), nil)}, {"who", Method(nil, tUnknown())}},
	}}},
})

// websocket.test.ts:29-99
func TestWebSocketLinksServesABrowserStyleClientOverARealSocketStampedByTheAuthenticator(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Serve(wsBoard, func(_ any, principal Principal, self Self) (Implementation, error) {
		notes, err := self.State("notes", tArray(tString()), []any{}, nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main",
			"notes", notes,
			"add", methodFunc(func(text any, ctx Ctx) (any, error) {
				var pushErr error
				if err := notes.Change(ctx, func(draft any) { _, pushErr = draft.(*delta.Array).Push(text) }); err != nil {
					return nil, err
				}
				return nil, pushErr
			}),
			"who", methodFunc(func(any, Ctx) (any, error) { return principal, nil }),
		), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	wss := startWebSocketServer(t)
	AcceptWebSockets[*http.Request](wss, server, WebSocketAcceptOptions[*http.Request]{
		Principal: func(request *http.Request) (Principal, bool, error) {
			if request.URL.Query().Get("token") == "ok" {
				return map[string]any{"guest": "web"}, true, nil
			}
			return nil, false, nil
		},
	})
	open := func(token string) *HostImpl {
		return CreateHost(HostOptions{Self: kindHost(),
			Name:     "browser",
			Kind:     KindClient,
			Upstream: &UpstreamOptions{Link: NewWebSocketLink(dialWebSocket(wss.url("token="+token)), WebSocketLinkOptions{}), ConnectOptions: ConnectOptions{Principal: kindServer()}},
			OnError:  func(error) {},
		})
	}
	getBoard := func(host *HostImpl) Ref {
		ref, err := host.Hold(wsBoard, map[string]any{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}

	browser := open("ok")
	board := getBoard(browser)
	notes := board.Member("notes").(Readable[any])
	var seenMu sync.Mutex
	var seen []any
	notes.Subscribe(func(value any) {
		seenMu.Lock()
		seen = append(seen, value)
		seenMu.Unlock()
	})
	mustCall(t, board, "add", "hello", nil)
	if got := jsonText(t, mustCall(t, board, "who", nil, nil)); got != `{"guest":"web"}` {
		t.Fatalf("who = %s", got)
	}
	// Pi polls up to 50 times, 5 ms apart, for the state op to arrive.
	waitFor(t, "the note", func() bool { return jsonText(t, notes.Value()) == `["hello"]` })
	seenMu.Lock()
	last := seen[len(seen)-1]
	seenMu.Unlock()
	if got := jsonText(t, last); got != `["hello"]` {
		t.Fatalf("last seen = %s", got)
	}
	if err := browser.Close(); err != nil {
		t.Fatal(err)
	}

	// WebSocket peers are always clients: one announcing itself as a host is cut off.
	impostor := CreateHost(HostOptions{Self: kindHost(),
		Name:     "session:S1",
		Upstream: &UpstreamOptions{Link: NewWebSocketLink(dialWebSocket(wss.url("token=ok")), WebSocketLinkOptions{}), ConnectOptions: ConnectOptions{Principal: kindServer()}},
		OnError:  func(error) {},
	})
	signal, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := impostor.Root(func(ctx Ctx) (any, error) {
		ref, err := impostor.Hold(wsBoard, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		return callMember(ref, "add", "x", ctx)
	}, RootOptions{Signal: signal})
	if !IsChordError(err, CodeAborted) {
		t.Fatalf("impostor: %v", err)
	}
	if err := impostor.Close(); err != nil {
		t.Fatal(err)
	}

	refused := open("bad")
	denied := getBoard(refused)
	// The server closes unauthenticated sockets; calls wait for a reconnect until their ctx gives up.
	signal, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = refused.Root(func(ctx Ctx) (any, error) { return callMember(denied, "add", "x", ctx) }, RootOptions{Signal: signal})
	if !IsChordError(err, CodeAborted) {
		t.Fatalf("refused: %v", err)
	}
	if err := refused.Close(); err != nil {
		t.Fatal(err)
	}
}

// websocket.test.ts (Pi 018c87dd) "WebSocket links: packed payloads" "large snapshots and results reach the client as
// binary messages; clients may not send them".
func TestWebSocketLinksPackedPayloadsLargeSnapshotsAndResultsReachTheClientAsBinaryMessagesClientsMayNotSendThem(t *testing.T) {
	notes := make([]any, 40)
	for index := range notes {
		notes[index] = strconv.Itoa(index) + ":" + strings.Repeat("x", 4000)
	}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Serve(wsBoard, func(_ any, _ Principal, self Self) (Implementation, error) {
		state, err := self.State("notes", tArray(tString()), notes, nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main",
			"notes", state,
			"add", methodFunc(func(any, Ctx) (any, error) { return nil, nil }),
			"who", methodFunc(func(any, Ctx) (any, error) { return notes, nil }),
		), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	wss := startWebSocketServer(t)
	AcceptWebSockets[*http.Request](wss, server, WebSocketAcceptOptions[*http.Request]{
		Principal: func(*http.Request) (Principal, bool, error) { return map[string]any{"guest": "web"}, true, nil },
	})
	var binaryMu sync.Mutex
	var binary [][]byte
	socket := dialWebSocket(wss.url(""))
	socket.AddEventListener("message", func(event WebSocketMessage) {
		if data, ok := event.Data.([]byte); ok {
			binaryMu.Lock()
			binary = append(binary, data)
			binaryMu.Unlock()
		}
	})
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: NewWebSocketLink(socket, WebSocketLinkOptions{}), ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	board, err := browser.Hold(wsBoard, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := jsonText(t, notes)
	if got := jsonText(t, mustCall(t, board, "who", nil, nil)); got != want {
		t.Fatalf("who returned %d characters, want %d", len(got), len(want))
	}
	readable := board.Member("notes").(Readable[any])
	readable.Subscribe(func(any) {})
	waitFor(t, "the notes snapshot", func() bool { return readable.Value() != nil && jsonText(t, readable.Value()) == want })
	binaryMu.Lock()
	messages := slices.Clone(binary)
	binaryMu.Unlock()
	if len(messages) < 2 {
		t.Fatalf("binary messages = %d: the result and the snapshot came as binary messages", len(messages))
	}
	// A text-only transport (the TUI launcher) turns a packed message back into its text frame.
	var sawResult, sawSnap bool
	for _, message := range messages {
		text, err := InflateMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := chordjson.Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		object := frame.(*chordjson.Object)
		switch object.Value("t") {
		case "result":
			sawResult = sawResult || jsonText(t, object.Value("value")) == want
		case "state":
			sawSnap = sawSnap || jsonText(t, object.Value("value")) == want
		}
	}
	if !sawResult || !sawSnap {
		t.Fatalf("inflated messages: result %v, snapshot %v", sawResult, sawSnap)
	}
	if err := browser.Close(); err != nil {
		t.Fatal(err)
	}

	// A client's binary message is a protocol error: the server closes the socket.
	raw, _, err := websocket.DefaultDialer.Dial(wss.url(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if err := raw.WriteMessage(websocket.BinaryMessage, []byte{0, 0, 0, 2, 123, 125}); err != nil {
		t.Fatal(err)
	}
	_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := raw.ReadMessage(); err != nil {
			if _, ok := errors.AsType[*websocket.CloseError](err); !ok {
				t.Fatalf("the server did not close the socket: %v", err)
			}
			break
		}
	}
}
