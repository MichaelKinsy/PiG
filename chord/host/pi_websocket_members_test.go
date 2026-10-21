package host

// The WebSocket surfaces Chord drives structurally (Pi packages/chord/src/link.ts:333-339 WebSocketLike and
// packages/chord/src/websocket.ts:6-15 WebSocketServerLike, WebSocketAcceptOptions): PiG implements neither in Go (a
// browser, Node's WebSocket or `ws` does upstream), so these tests call them through WebSocketLike and
// WebSocketServerLike values and pin what WebSocketLink and AcceptWebSockets do with them, as Pi does.

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// piWsServer is a WebSocketServerLike that records the events listened to and hands each connect to the "connection"
// listeners, as `ws`'s WebSocketServer emits "connection".
type piWsServer struct {
	mu        sync.Mutex
	events    []string
	listeners []func(WebSocketLike, string)
}

func (server *piWsServer) On(event string, listener func(socket WebSocketLike, request string)) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.events = append(server.events, event)
	if event == "connection" {
		server.listeners = append(server.listeners, listener)
	}
}

func (server *piWsServer) listened() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return slices.Clone(server.events)
}

func (server *piWsServer) connect(socket WebSocketLike, request string) {
	server.mu.Lock()
	listeners := slices.Clone(server.listeners)
	server.mu.Unlock()
	for _, listener := range listeners {
		listener(socket, request)
	}
}

// piWsPeer is one end of an open in-process WebSocket: what it sends arrives, in order and asynchronously, as the
// other end's "message" events; closing either end fires "close" on both. Like a real socket, whose "message" events
// are emitted only after the connection handler has attached its listeners, delivery starts once the peer has a
// "message" listener: frames sent before that wait in the inbox instead of being dropped.
type piWsPeer struct {
	mu        sync.Mutex
	state     int
	closes    []int
	listeners map[string][]func(WebSocketMessage)
	other     *piWsPeer
	inbox     chan any
	done      chan struct{}
	listening chan struct{}
	listenOne sync.Once
}

// piWsPair returns the two ends of an open socket; the test's cleanup stops their delivery.
func piWsPair(t *testing.T) (*piWsPeer, *piWsPeer) {
	a := &piWsPeer{state: webSocketOpen, listeners: map[string][]func(WebSocketMessage){}, inbox: make(chan any, 4096), done: make(chan struct{}), listening: make(chan struct{})}
	b := &piWsPeer{state: webSocketOpen, listeners: map[string][]func(WebSocketMessage){}, inbox: make(chan any, 4096), done: make(chan struct{}), listening: make(chan struct{})}
	a.other, b.other = b, a
	for _, peer := range []*piWsPeer{a, b} {
		go func() {
			select {
			case <-peer.listening:
			case <-peer.done:
				return
			}
			for {
				select {
				case data := <-peer.inbox:
					peer.emit("message", data)
				case <-peer.done:
					return
				}
			}
		}()
	}
	t.Cleanup(func() { close(a.done); close(b.done) })
	return a, b
}

func (peer *piWsPeer) ReadyState() int {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.state
}

func (peer *piWsPeer) Send(data any) error {
	if peer.ReadyState() != webSocketOpen {
		return errors.New("WebSocket is not open")
	}
	select {
	case peer.other.inbox <- data:
	case <-peer.other.done:
	}
	return nil
}

func (peer *piWsPeer) Close(code int, _ string) {
	for _, end := range []*piWsPeer{peer, peer.other} {
		end.mu.Lock()
		if end.state > webSocketOpen {
			end.mu.Unlock()
			continue
		}
		end.state = 3
		if end == peer {
			end.closes = append(end.closes, code)
		}
		end.mu.Unlock()
		go end.emit("close", nil)
	}
}

func (peer *piWsPeer) AddEventListener(typ string, listener func(event WebSocketMessage)) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	peer.listeners[typ] = append(peer.listeners[typ], listener)
	if typ == "message" {
		peer.listenOne.Do(func() { close(peer.listening) })
	}
}

func (peer *piWsPeer) emit(typ string, data any) {
	peer.mu.Lock()
	listeners := slices.Clone(peer.listeners[typ])
	peer.mu.Unlock()
	for _, listener := range listeners {
		listener(WebSocketMessage{Data: data})
	}
}

func (peer *piWsPeer) closeCodes() []int {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return slices.Clone(peer.closes)
}

// acceptPiWsClient accepts one client over an in-process socket through AcceptWebSockets with options (its principal
// {user: "u"}) and returns the client host and the server's end of the socket once the server holds the connection.
func acceptPiWsClient(t *testing.T, server *HostImpl, options WebSocketAcceptOptions[string]) (*HostImpl, *piWsPeer) {
	t.Helper()
	options.Principal = func(string) (Principal, bool, error) { return map[string]any{"user": "u"}, true, nil }
	listener := &piWsServer{}
	AcceptWebSockets[string](listener, server, options)
	serverEnd, clientEnd := piWsPair(t)
	client := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: NewWebSocketLink(clientEnd, WebSocketLinkOptions{}), ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	listener.connect(serverEnd, "/")
	waitFor(t, "the server accepted the socket", func() bool {
		return reviewOnLoop(server, func() bool { return server.connections.Len() == 1 })
	})
	return client, serverEnd
}

// Pi packages/chord/src/link.ts:335 `readonly readyState: number` and link.ts:393 `if (this.#socket.readyState ===
// OPEN) this.#socket.send(data); else this.#outbox.push(data)`: a frame sent while the socket's readyState is
// CONNECTING waits; once it is OPEN frames are sent at once.
func TestPiWebSocketLikeReadyStateGatesSend(t *testing.T) {
	fake := newFakeWebSocket(0)
	var socket WebSocketLike = fake
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	_, _ = link.Send(PingFrame{N: 1})
	if socket.ReadyState() != 0 || len(fake.sent) != 0 {
		t.Fatalf("readyState %d: sent while CONNECTING: %q", socket.ReadyState(), fake.sent)
	}
	open := newFakeWebSocket(webSocketOpen)
	var openSocket WebSocketLike = open
	_, _ = NewWebSocketLink(openSocket, WebSocketLinkOptions{}).Send(PingFrame{N: 3})
	if want := []string{`{"t":"ping","n":3}`}; openSocket.ReadyState() != webSocketOpen || !slices.Equal(open.sent, want) {
		t.Fatalf("readyState OPEN: sent = %q, want %q at once", open.sent, want)
	}
}

// Pi packages/chord/src/link.ts:336 `send(data: string | Uint8Array): void` and link.ts:363-364: the "open" listener
// sends every frame queued while connecting, in order, then frames go straight to socket.send (link.ts:393), in order
// with whatever else the app sends on the socket.
func TestPiWebSocketLikeSendFlushesTheOutboxOnOpen(t *testing.T) {
	fake := newFakeWebSocket(0)
	var socket WebSocketLike = fake
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	_, _ = link.Send(PingFrame{N: 1})
	_, _ = link.Send(PingFrame{N: 2})
	fake.readyState = webSocketOpen
	fake.emit("open", nil)
	if want := []string{`{"t":"ping","n":1}`, `{"t":"ping","n":2}`}; !slices.Equal(fake.sent, want) {
		t.Fatalf("send after open = %q, want %q", fake.sent, want)
	}
	if err := socket.Send(`{"t":"pong","n":9}`); err != nil {
		t.Fatal(err)
	}
	_, _ = link.Send(PingFrame{N: 3})
	if want := []string{`{"t":"ping","n":1}`, `{"t":"ping","n":2}`, `{"t":"pong","n":9}`, `{"t":"ping","n":3}`}; !slices.Equal(fake.sent, want) {
		t.Fatalf("send while open = %q, want %q", fake.sent, want)
	}
}

// Pi packages/chord/src/link.ts:338 `addEventListener(type: "message", listener: (event: { readonly data: unknown }) =>
// void)` and link.ts:366-370: a text message's data is parsed as a frame and delivered to the link's receiver; another
// "message" listener on the socket sees the same data.
func TestPiWebSocketLikeAddEventListenerMessageDeliversFrames(t *testing.T) {
	fake := newFakeWebSocket(webSocketOpen)
	var socket WebSocketLike = fake
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	var seen []any
	socket.AddEventListener("message", func(event WebSocketMessage) { seen = append(seen, event.Data) })
	if len(fake.listeners["message"]) != 2 {
		t.Fatalf(`addEventListener("message") listeners = %d, want the link's and the test's`, len(fake.listeners["message"]))
	}
	var log frameLog
	link.Receive(log.add)
	fake.emit("message", `{"t":"pong","n":5}`)
	if frames := log.list(); len(frames) != 1 || frames[0] != (PongFrame{N: 5}) {
		t.Fatalf(`frames from the addEventListener("message") listener = %#v`, frames)
	}
	if !reflect.DeepEqual(seen, []any{`{"t":"pong","n":5}`}) {
		t.Fatalf(`the test's "message" listener saw %q`, seen)
	}
}

// Pi packages/chord/src/link.ts:339 `addEventListener(type: "open" | "close" | "error", listener: () => void)` and
// link.ts:363, 388: the link listens for "open" (flushing its outbox) and "close", which ends it with "socket closed"
// before a "close" listener added after it runs.
func TestPiWebSocketLikeAddEventListenerOpenAndClose(t *testing.T) {
	fake := newFakeWebSocket(webSocketOpen)
	var socket WebSocketLike = fake
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	if len(fake.listeners["open"]) != 1 || len(fake.listeners["close"]) != 1 {
		t.Fatalf(`addEventListener("open") listeners = %d, addEventListener("close") listeners = %d, want 1 each`,
			len(fake.listeners["open"]), len(fake.listeners["close"]))
	}
	var reasons reasonLog
	link.OnClose(reasons.add)
	closedFirst := false
	socket.AddEventListener("close", func(WebSocketMessage) { closedFirst = link.Closed() })
	fake.emit("close", nil)
	if got := reasons.list(); !slices.Equal(got, []string{"socket closed"}) {
		t.Fatalf(`reasons after the socket's "close" = %q, want ["socket closed"]`, got)
	}
	if !closedFirst {
		t.Fatal(`the link was still open when a later "close" listener ran`)
	}
}

// Pi packages/chord/src/link.ts:337 `close(code?: number, reason?: string): void` and link.ts:398-401: closing the
// link closes a CONNECTING or OPEN socket with 1000 and drops the outbox; a socket already closing (closed by the app
// through socket.close) is not closed again.
func TestPiWebSocketLikeCloseOnShutdown(t *testing.T) {
	for _, readyState := range []int{0, webSocketOpen} {
		fake := newFakeWebSocket(readyState)
		link := NewWebSocketLink(fake, WebSocketLinkOptions{})
		_, _ = link.Send(PingFrame{N: 1})
		link.Close("done")
		if !slices.Equal(fake.closes, []int{1000}) {
			t.Fatalf("readyState %d: close codes = %v, want [1000]", readyState, fake.closes)
		}
		fake.readyState = webSocketOpen
		fake.emit("open", nil)
		for _, data := range fake.sent {
			if data == `{"t":"ping","n":1}` && readyState == 0 {
				t.Fatalf("the outbox was sent after close: %q", fake.sent)
			}
		}
	}
	fake := newFakeWebSocket(webSocketOpen)
	var socket WebSocketLike = fake
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	socket.Close(4000, "app")
	fake.readyState = 2
	link.Close("done")
	if !slices.Equal(fake.closes, []int{4000}) {
		t.Fatalf("closing socket: close codes = %v, want only the app's [4000]", fake.closes)
	}
}

// Pi packages/chord/src/websocket.ts:7-9 `on(event: "connection", listener: (socket: WebSocketLike, request: Request)
// => void): unknown` and websocket.ts:23-26: acceptWebSockets listens for "connection" and wraps each connecting
// socket at once, so a later "connection" listener gets the socket with the link's listeners on it (link.ts:363-388);
// a refused client's link closes with "unauthorized" (websocket.ts:29), closing the socket with 1000.
func TestPiWebSocketServerLikeOnConnection(t *testing.T) {
	chord := requestsHost(t, HostOptions{Name: "server"})
	fake := &piWsServer{}
	var server WebSocketServerLike[string] = fake
	release := make(chan struct{})
	// The host's Close drains the authenticator, so it is released even when the test fails first.
	releaseAuth := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseAuth)
	AcceptWebSockets(server, chord, WebSocketAcceptOptions[string]{
		Principal: func(request string) (Principal, bool, error) {
			<-release
			return nil, false, nil
		},
	})
	var wrapped []string
	server.On("connection", func(socket WebSocketLike, request string) {
		end := socket.(*piWsPeer)
		end.mu.Lock()
		defer end.mu.Unlock()
		wrapped = append(wrapped, request, strconv.Itoa(len(end.listeners["message"])), strconv.Itoa(len(end.listeners["close"])))
	})
	if got := fake.listened(); !slices.Equal(got, []string{"connection", "connection"}) {
		t.Fatalf(`server.on events = %q, want acceptWebSockets's "connection" and the test's`, got)
	}
	serverEnd, clientEnd := piWsPair(t)
	var mu sync.Mutex
	var received []any
	clientEnd.AddEventListener("message", func(event WebSocketMessage) {
		mu.Lock()
		received = append(received, event.Data)
		mu.Unlock()
	})
	fake.connect(serverEnd, "/?token=bad")
	if want := []string{"/?token=bad", "1", "1"}; !slices.Equal(wrapped, want) {
		t.Fatalf(`a later on("connection") listener saw request, message and close listeners %q, want %q`, wrapped, want)
	}
	releaseAuth()
	waitFor(t, "the refused client's bye", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 1
	})
	mu.Lock()
	got := slices.Clone(received)
	mu.Unlock()
	if !reflect.DeepEqual(got, []any{`{"t":"bye","reason":"unauthorized"}`}) {
		t.Fatalf("refused client received %q", got)
	}
	// The link sends the bye and closes its socket after it (TestWebSocketLinkSendsByeBeforeItClosesTheSocket). In
	// Node the client's message event comes in a later tick than the server's whole close(); here the client end
	// can have received the bye before the server end recorded the close, so the test waits for it.
	waitFor(t, "the refused client's socket close", func() bool { return len(serverEnd.closeCodes()) > 0 })
	if codes := serverEnd.closeCodes(); !slices.Equal(codes, []int{1000}) {
		t.Fatalf("refused client's socket close codes = %v, want [1000]", codes)
	}
}

// Pi packages/chord/src/connection.ts:47 `readonly restamp?: (principal: Principal) => Principal | undefined`, which
// WebSocketAcceptOptions keeps (websocket.ts:12 Omit<AcceptOptions, "principal" | "kind" | "name">) and acceptWebSockets
// passes to host.accept (websocket.ts:30 `{ ...options, principal, kind: "client" }`): on invalidatePrincipal the
// client's link is re-stamped with the new principal and stays open instead of closing.
func TestPiWebSocketAcceptOptionsRestampKeepsTheLink(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	var mu sync.Mutex
	var restamped []Principal
	_, serverEnd := acceptPiWsClient(t, server, WebSocketAcceptOptions[string]{
		Restamp: func(principal Principal) (Principal, bool) {
			mu.Lock()
			defer mu.Unlock()
			restamped = append(restamped, principal)
			return map[string]any{"user": "v"}, true
		},
	})
	err := server.InvalidatePrincipal(PrincipalMatch{Predicate: func(principal Principal) bool {
		return reflect.DeepEqual(principal, map[string]any{"user": "u"})
	}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := slices.Clone(restamped)
	mu.Unlock()
	if want := []Principal{map[string]any{"user": "u"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restamp called with %v, want %v", got, want)
	}
	principal := reviewOnLoop(server, func() Principal {
		for _, connection := range server.connections.Keys() {
			return connection.principal
		}
		return nil
	})
	if !reflect.DeepEqual(principal, map[string]any{"user": "v"}) {
		t.Fatalf("link principal after restamp = %v, want {user: v}", principal)
	}
	if codes := serverEnd.closeCodes(); len(codes) != 0 {
		t.Fatalf("the re-stamped client's socket closed: %v", codes)
	}
}

// Pi packages/chord/src/connection.ts:48-52 `readonly maxCalls?: number` (calls beyond it are answered at once with
// unavailable, reason too_many_calls, and the link stays open), which WebSocketAcceptOptions keeps (websocket.ts:12) and
// acceptWebSockets passes to host.accept (websocket.ts:30); the case is flow.test.ts:256 over a WebSocket client.
func TestPiWebSocketAcceptOptionsMaxCallsAnswersCallsBeyondItWithTooManyCalls(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	waiting := newRequestsLatch()
	t.Cleanup(waiting.open)
	serveFlowCounter(t, server, flowCounter, waiting)
	maxCalls := 2
	client, _ := acceptPiWsClient(t, server, WebSocketAcceptOptions[string]{MaxCalls: &maxCalls})
	counter := flowGet(t, client, flowCounter)
	if err := readyErr(t, counter); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := callMember(counter, "wait", nil, nil)
			done <- err
		}()
	}
	waitFor(t, "two calls in progress", func() bool {
		return reviewOnLoop(server, func() bool {
			for _, connection := range server.connections.Keys() {
				if connection.incoming.Len() == 2 {
					return true
				}
			}
			return false
		})
	})
	_, err := callMember(counter, "add", float64(1), nil)
	var chordErr *ChordError
	if !IsChordError(err, CodeUnavailable) || !errors.As(err, &chordErr) || chordErr.Reason != "too_many_calls" {
		t.Fatalf("add beyond maxCalls: %v", err)
	}
	waiting.open()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := addOne(t, counter); got != 1 {
		t.Fatalf("add = %v, want 1", got)
	}
}

// closeCountSocket records how many times its socket was closed when each message was sent.
type closeCountSocket struct {
	*fakeWebSocket
	closesAtSend []int
}

func (socket *closeCountSocket) Send(data any) error {
	socket.closesAtSend = append(socket.closesAtSend, len(socket.closes))
	return socket.fakeWebSocket.Send(data)
}

// Pi packages/chord/src/link.ts close(): the link sends its bye and then closes its socket with 1000 (shutdown), so a
// peer that has received the bye may not have seen the close yet.
func TestWebSocketLinkSendsByeBeforeItClosesTheSocket(t *testing.T) {
	socket := &closeCountSocket{fakeWebSocket: newFakeWebSocket(webSocketOpen)}
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	link.Close("unauthorized")
	if want := []string{`{"t":"bye","reason":"unauthorized"}`}; !slices.Equal(socket.sent, want) {
		t.Fatalf("sent = %q, want %q", socket.sent, want)
	}
	if !slices.Equal(socket.closesAtSend, []int{0}) {
		t.Fatalf("closes when the bye was sent = %v, want none yet", socket.closesAtSend)
	}
	if !slices.Equal(socket.closes, []int{1000}) {
		t.Fatalf("close codes = %v, want [1000] after the bye", socket.closes)
	}
}
