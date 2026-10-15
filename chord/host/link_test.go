package host

import (
	"bytes"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// waitFor polls cond until it holds; memory links deliver asynchronously (link.ts:103-106).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type frameLog struct {
	mu     sync.Mutex
	frames []Frame
}

func (log *frameLog) add(frame Frame) {
	log.mu.Lock()
	log.frames = append(log.frames, frame)
	log.mu.Unlock()
}

func (log *frameLog) list() []Frame {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.frames)
}

type reasonLog struct {
	mu      sync.Mutex
	reasons []string
}

func (log *reasonLog) add(reason string) {
	log.mu.Lock()
	log.reasons = append(log.reasons, reason)
	log.mu.Unlock()
}

func (log *reasonLog) list() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.reasons)
}

// link.ts:7, 33-36, 60, 98-106: frames sent before the receiver is set are queued, then handed over in order; delivery
// is FIFO and asynchronous: a frame sent from inside the receiver arrives after that receiver returned.
func TestMemoryLinkQueuesFramesUntilReceiveAndKeepsOrder(t *testing.T) {
	a, b := MemoryLinkPair()
	for n := 1; n <= 3; n++ {
		if _, err := a.Send(PingFrame{N: n}); err != nil {
			t.Fatal(err)
		}
	}
	var log frameLog
	b.Receive(func(frame Frame) {
		log.add(frame)
		if ping := frame.(PingFrame); ping.N >= 3 && ping.N < 5 {
			_, _ = a.Send(PingFrame{N: ping.N + 1})
			log.add(PongFrame(ping))
		}
	})
	waitFor(t, "seven entries", func() bool { return len(log.list()) == 7 })
	want := []Frame{
		PingFrame{N: 1}, PingFrame{N: 2}, PingFrame{N: 3}, PongFrame{N: 3}, PingFrame{N: 4}, PongFrame{N: 4},
		PingFrame{N: 5},
	}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("frames = %#v, want %#v", got, want)
	}
}

// link.ts:103-106: send returns before the peer's receiver runs (delivery is a later turn, never inside send).
func TestMemoryLinkSendDoesNotWaitForTheReceiver(t *testing.T) {
	a, b := MemoryLinkPair()
	release := make(chan struct{})
	received := make(chan Frame, 1)
	b.Receive(func(frame Frame) {
		<-release
		received <- frame
	})
	sent := make(chan error, 1)
	go func() { _, err := a.Send(PingFrame{N: 1}); sent <- err }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("send waited for the receiver")
	}
	close(release)
	if frame := <-received; frame != (PingFrame{N: 1}) {
		t.Fatalf("frame = %#v", frame)
	}
}

// link.ts:43-51, 56-58, 64-75, 109-112: close sends a bye, so the peer closes with the same reason; both ends are
// closed, send is a no-op afterwards, close handlers run once, and onClose on a closed link runs at once with "closed".
func TestMemoryLinkCloseSendsByeAndClosesBothEnds(t *testing.T) {
	a, b := MemoryLinkPair()
	var aReasons, bReasons reasonLog
	a.OnClose(aReasons.add)
	b.OnClose(bReasons.add)
	var log frameLog
	b.Receive(log.add)
	a.Close("bye now")
	if !a.Closed() {
		t.Fatal("closing end not closed")
	}
	if got := aReasons.list(); !slices.Equal(got, []string{"bye now"}) {
		t.Fatalf("closing end reasons = %q", got)
	}
	waitFor(t, "peer close", func() bool { return len(bReasons.list()) > 0 })
	if got := bReasons.list(); !slices.Equal(got, []string{"bye now"}) {
		t.Fatalf("peer reasons = %q", got)
	}
	if got := log.list(); len(got) != 0 {
		t.Fatalf("bye reached the receiver: %#v", got)
	}
	a.Close("again")
	if _, err := a.Send(PingFrame{N: 1}); err != nil {
		t.Fatal(err)
	}
	if got := aReasons.list(); len(got) != 1 {
		t.Fatalf("close handlers ran %d times", len(got))
	}
	var late reasonLog
	b.OnClose(late.add)
	if got := late.list(); !slices.Equal(got, []string{"closed"}) {
		t.Fatalf("onClose on a closed link = %q", got)
	}
}

// link.ts:43: close() without a reason closes with "closed".
func TestMemoryLinkCloseDefaultsToClosed(t *testing.T) {
	a, b := MemoryLinkPair()
	var reasons reasonLog
	b.OnClose(reasons.add)
	a.Close()
	// Closed turns true before the handlers run (ended notifies after shutdown); wait for the handler.
	waitFor(t, "peer close", func() bool { return len(reasons.list()) > 0 })
	if got := reasons.list(); !slices.Equal(got, []string{"closed"}) {
		t.Fatalf("reasons = %q", got)
	}
}

// link.ts:92-96, 98-99, 109-112: a severed pair drops frames in both directions without signalling a close; closing
// a severed end still ends the peer with "peer closed" (shutdown ignores the severing; the bye is dropped).
func TestMemoryLinkSeverDropsFramesSilently(t *testing.T) {
	a, b := MemoryLinkPair()
	var aLog, bLog frameLog
	a.Receive(aLog.add)
	b.Receive(bLog.add)
	a.Sever()
	_, _ = a.Send(PingFrame{N: 1})
	_, _ = b.Send(PingFrame{N: 2})
	time.Sleep(20 * time.Millisecond)
	if a.Closed() || b.Closed() || len(aLog.list()) != 0 || len(bLog.list()) != 0 {
		t.Fatalf("severed: closed %v/%v, frames %v/%v", a.Closed(), b.Closed(), aLog.list(), bLog.list())
	}
	var reasons reasonLog
	b.OnClose(reasons.add)
	a.Close("x")
	waitFor(t, "peer close", func() bool { return len(reasons.list()) > 0 })
	if got := reasons.list(); !slices.Equal(got, []string{"peer closed"}) {
		t.Fatalf("reasons = %q", got)
	}
}

// link.ts:65-69, 54-55: closing drops frames still queued for a receiver that was never set.
func TestMemoryLinkCloseDropsQueuedFrames(t *testing.T) {
	a, b := MemoryLinkPair()
	_, _ = a.Send(PingFrame{N: 1})
	waitFor(t, "queued frame", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.queue) == 1
	})
	b.Close()
	var log frameLog
	b.Receive(log.add)
	if got := log.list(); len(got) != 0 {
		t.Fatalf("queued frame delivered after close: %#v", got)
	}
}

// link.ts:100-101: send fails when the frame is not JSON, as JSON.stringify throws.
func TestMemoryLinkSendReturnsTheEncodingError(t *testing.T) {
	a, _ := MemoryLinkPair()
	if _, err := a.Send(ResultFrame{ID: 1, Value: func() {}, HasValue: true}); err == nil {
		t.Fatal("send of a non-JSON value succeeded")
	}
}

type fakeIpcChannel struct {
	connected bool
	sent      []any
	listeners map[string][]*IpcListener
}

func newFakeIpcChannel() *fakeIpcChannel {
	return &fakeIpcChannel{connected: true, listeners: map[string][]*IpcListener{}}
}

func (channel *fakeIpcChannel) Connected() bool { return channel.connected }

func (channel *fakeIpcChannel) Send(message any, _ func(err error)) bool {
	channel.sent = append(channel.sent, message)
	return true
}

func (channel *fakeIpcChannel) On(event string, listener *IpcListener) {
	channel.listeners[event] = append(channel.listeners[event], listener)
}

func (channel *fakeIpcChannel) Off(event string, listener *IpcListener) {
	channel.listeners[event] = slices.DeleteFunc(channel.listeners[event], func(l *IpcListener) bool {
		return l == listener
	})
}

func (channel *fakeIpcChannel) emit(event string, message any) {
	for _, listener := range slices.Clone(channel.listeners[event]) {
		(*listener)(message)
	}
}

// link.ts:136-160: frames travel wrapped as { chord2: frame }; other messages pass by; nothing is sent while the
// channel is disconnected; a bye without a reason ends with "peer closed"; disconnect ends with "ipc disconnected" and
// removes both listeners.
func TestIpcLinkWrapsFramesAndFollowsTheChannel(t *testing.T) {
	channel := newFakeIpcChannel()
	link := NewIpcLink(channel)
	if len(channel.listeners["message"]) != 1 || len(channel.listeners["disconnect"]) != 1 {
		t.Fatalf("listeners = %v", channel.listeners)
	}
	if _, err := link.Send(PingFrame{N: 7}); err != nil {
		t.Fatal(err)
	}
	data, err := chordjson.ObjectOf("chord2", PingFrame{N: 7}.encode()).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := channel.sent[0].(*chordjson.Object).MarshalJSON()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("sent %s (%v), want %s", got, err, data)
	}
	channel.connected = false
	_, _ = link.Send(PingFrame{N: 8})
	if len(channel.sent) != 1 {
		t.Fatalf("sent while disconnected: %d messages", len(channel.sent))
	}
	var log frameLog
	link.Receive(log.add)
	channel.emit("message", chordjson.ObjectOf("control", "x"))
	channel.emit("message", chordjson.ObjectOf("chord2", chordjson.ObjectOf("t", "pong", "n", float64(3))))
	channel.emit("message", chordjson.ObjectOf("chord2", chordjson.ObjectOf("t", "pong")))
	frames := log.list()
	if len(frames) != 2 || frames[0] != (PongFrame{N: 3}) {
		t.Fatalf("frames = %#v", frames)
	}
	if malformed, ok := frames[1].(MalformedFrame); !ok || malformed.T() != "pong" {
		t.Fatalf("invalid frame delivered as %#v", frames[1])
	}
	var reasons reasonLog
	link.OnClose(reasons.add)
	channel.emit("message", chordjson.ObjectOf("chord2", chordjson.ObjectOf("t", "bye")))
	if got := reasons.list(); !slices.Equal(got, []string{"peer closed"}) {
		t.Fatalf("reasons = %q", got)
	}
	if len(channel.listeners["message"]) != 0 || len(channel.listeners["disconnect"]) != 0 {
		t.Fatalf("listeners left after close: %v", channel.listeners)
	}

	other := NewIpcLink(newFakeIpcChannel())
	var otherReasons reasonLog
	other.OnClose(otherReasons.add)
	other.channel.(*fakeIpcChannel).emit("disconnect", nil)
	if got := otherReasons.list(); !slices.Equal(got, []string{"ipc disconnected"}) {
		t.Fatalf("disconnect reasons = %q", got)
	}
}

type fakeWebSocket struct {
	readyState int
	sent       []string
	binary     [][]byte
	closes     []int
	listeners  map[string][]func(WebSocketMessage)
}

func newFakeWebSocket(readyState int) *fakeWebSocket {
	return &fakeWebSocket{readyState: readyState, listeners: map[string][]func(WebSocketMessage){}}
}

func (socket *fakeWebSocket) ReadyState() int { return socket.readyState }
func (socket *fakeWebSocket) Send(data any) error {
	switch data := data.(type) {
	case string:
		socket.sent = append(socket.sent, data)
	case []byte:
		socket.binary = append(socket.binary, data)
	}
	return nil
}
func (socket *fakeWebSocket) Close(code int, _ string) { socket.closes = append(socket.closes, code) }
func (socket *fakeWebSocket) AddEventListener(typ string, listener func(WebSocketMessage)) {
	socket.listeners[typ] = append(socket.listeners[typ], listener)
}
func (socket *fakeWebSocket) emit(typ string, data any) {
	for _, listener := range socket.listeners[typ] {
		listener(WebSocketMessage{Data: data})
	}
}

// link.ts:177-207: frames sent while connecting wait in an outbox flushed on open; text messages are parsed JSON
// frames; a schema-invalid object is delivered for the connection to reject; the socket closing ends the link with
// "socket closed".
func TestWebSocketLinkOutboxParsingAndClose(t *testing.T) {
	socket := newFakeWebSocket(0)
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	_, _ = link.Send(PingFrame{N: 1})
	if len(socket.sent) != 0 {
		t.Fatalf("sent before open: %q", socket.sent)
	}
	socket.readyState = webSocketOpen
	socket.emit("open", nil)
	_, _ = link.Send(PingFrame{N: 2})
	if want := []string{`{"t":"ping","n":1}`, `{"t":"ping","n":2}`}; !slices.Equal(socket.sent, want) {
		t.Fatalf("sent = %q, want %q", socket.sent, want)
	}
	var log frameLog
	link.Receive(log.add)
	socket.emit("message", `{"t":"pong","n":5}`)
	socket.emit("message", `{"t":"pong","n":"x"}`)
	frames := log.list()
	if len(frames) != 2 || frames[0] != (PongFrame{N: 5}) {
		t.Fatalf("frames = %#v", frames)
	}
	if _, ok := frames[1].(MalformedFrame); !ok {
		t.Fatalf("invalid frame delivered as %#v", frames[1])
	}
	var reasons reasonLog
	link.OnClose(reasons.add)
	socket.emit("close", nil)
	if got := reasons.list(); !slices.Equal(got, []string{"socket closed"}) {
		t.Fatalf("reasons = %q", got)
	}
	if !slices.Equal(socket.closes, []int{1000}) {
		t.Fatalf("closes = %v", socket.closes)
	}
}

// link.ts:184-200: frames go out in send order across the open transition. Upstream's readyState becomes OPEN and the
// "open" event flushes the outbox in one task, so no frame can be sent between the two. A Go socket reports OPEN on
// one goroutine before its open listener runs on another, and a frame sent in that window must not overtake the queued
// ones (a client's hello and then its first hold: the server rejects a host hello only after serving the hold).
func TestWebSocketLinkKeepsSendOrderAcrossOpen(t *testing.T) {
	socket := newFakeWebSocket(0)
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	_, _ = link.Send(PingFrame{N: 1})
	socket.readyState = webSocketOpen
	_, _ = link.Send(PingFrame{N: 2})
	socket.emit("open", nil)
	_, _ = link.Send(PingFrame{N: 3})
	if want := []string{`{"t":"ping","n":1}`, `{"t":"ping","n":2}`, `{"t":"ping","n":3}`}; !slices.Equal(socket.sent, want) {
		t.Fatalf("sent = %q, want %q", socket.sent, want)
	}
}

// link.ts:79-83, 187-192, 203-206: data that is not a JSON object with a string "t", a malformed packed payload, or a
// receiver that throws, closes the link with "protocol error": a bye goes out first, then the socket is closed with
// 1000 and the outbox is dropped.
func TestWebSocketLinkProtocolErrorCloses(t *testing.T) {
	for name, data := range map[string]any{
		"not json":            "nope",
		"no t":                `{"n":1}`,
		"array":               `[1]`,
		"non string":          42,
		"bad z":               `{"t":"result","id":1,"z":["id","AA=="]}`,
		"short binary":        []byte{0, 0},
		"binary without z":    append([]byte{0, 0, 0, 2}, "{}"...),
		"binary with array":   append([]byte{0, 0, 0, 2}, "[]"...),
		"binary with a non-z": append([]byte{0, 0, 0, 11}, `{"z":"bad"}`...),
	} {
		t.Run(name, func(t *testing.T) {
			socket := newFakeWebSocket(webSocketOpen)
			link := NewWebSocketLink(socket, WebSocketLinkOptions{})
			var reasons reasonLog
			link.OnClose(reasons.add)
			socket.emit("message", data)
			if got := reasons.list(); !slices.Equal(got, []string{"protocol error"}) {
				t.Fatalf("reasons = %q", got)
			}
			if want := []string{`{"t":"bye","reason":"protocol error"}`}; !slices.Equal(socket.sent, want) {
				t.Fatalf("sent = %q", socket.sent)
			}
			if !slices.Equal(socket.closes, []int{1000}) {
				t.Fatalf("closes = %v", socket.closes)
			}
		})
	}
	socket := newFakeWebSocket(webSocketOpen)
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	link.Receive(func(Frame) { panic("receiver failed") })
	var reasons reasonLog
	link.OnClose(reasons.add)
	socket.emit("message", `{"t":"ping","n":1}`)
	if got := reasons.list(); !slices.Equal(got, []string{"protocol error"}) {
		t.Fatalf("throwing receiver: reasons = %q", got)
	}
	closed := newFakeWebSocket(3)
	closedLink := NewWebSocketLink(closed, WebSocketLinkOptions{})
	closedLink.Close()
	if len(closed.closes) != 0 {
		t.Fatalf("closed socket closed again: %v", closed.closes)
	}
}

// link.ts (Pi 018c87dd) packMessage, splitMessage, inflateMessage: with compress, a frame whose payload's JSON exceeds
// PackThreshold leaves as one binary message, `[u32 BE envelope length][envelope with z: field][deflated payload]`; a
// smaller payload stays text with the payload spliced in last. The receiving link delivers the payload packed; a link
// that does not accept packed frames closes on a binary message.
func TestWebSocketLinkPacksLargePayloadsAsBinaryMessages(t *testing.T) {
	socket := newFakeWebSocket(webSocketOpen)
	link := NewWebSocketLink(socket, WebSocketLinkOptions{Compress: true})
	big := make([]any, 20)
	for index := range big {
		big[index] = strings.Repeat("y", 4000)
	}
	_, _ = link.Send(CallFrame{ID: 1, Hold: 2, Member: "m", Args: "x", HasArgs: true, Root: &RootRef{RootID: "r", CodeVersion: 1}})
	_, _ = link.Send(StateFrame{Hold: 3, M: "notes", As: "snap", Value: big, HasValue: true})
	if want := []string{`{"t":"call","id":1,"hold":2,"member":"m","root":{"rootId":"r","codeVersion":1},"args":"x"}`}; !slices.Equal(socket.sent, want) {
		t.Fatalf("text = %q, want %q", socket.sent, want)
	}
	if len(socket.binary) != 1 {
		t.Fatalf("binary messages = %d", len(socket.binary))
	}
	message := socket.binary[0]
	length := int(message[0])<<24 | int(message[1])<<16 | int(message[2])<<8 | int(message[3])
	if head := string(message[4 : 4+length]); head != `{"t":"state","hold":3,"m":"notes","as":"snap","z":"value"}` {
		t.Fatalf("envelope = %s", head)
	}
	text, err := InflateMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"t":"state","hold":3,"m":"notes","as":"snap","value":` + jsonText(t, big) + `}`; text != want {
		t.Fatalf("inflated message has %d characters, want %d", len(text), len(want))
	}

	peer := newFakeWebSocket(webSocketOpen)
	receiver := NewWebSocketLink(peer, WebSocketLinkOptions{})
	var log frameLog
	receiver.Receive(log.add)
	peer.emit("message", message)
	frames := log.list()
	if len(frames) != 1 {
		t.Fatalf("frames = %#v", frames)
	}
	state, ok := frames[0].(StateFrame)
	if !ok || state.Hold != 3 || state.M != "notes" || !state.HasValue {
		t.Fatalf("frame = %#v", frames[0])
	}
	payload, ok := state.Value.(packed)
	if !ok {
		t.Fatalf("value %T is not packed", state.Value)
	}
	if value, err := unpack(payload); err != nil || jsonText(t, value) != jsonText(t, big) {
		t.Fatalf("unpacked = %v", err)
	}
	// A relaying link sends the packed bytes as they are, also without compress.
	relay := newFakeWebSocket(webSocketOpen)
	_, _ = NewWebSocketLink(relay, WebSocketLinkOptions{}).Send(state)
	if len(relay.binary) != 1 || !bytes.Equal(relay.binary[0], message) {
		t.Fatal("the relayed message is not the received one")
	}

	refuse := false
	strict := newFakeWebSocket(webSocketOpen)
	strictLink := NewWebSocketLink(strict, WebSocketLinkOptions{AcceptPacked: &refuse})
	var reasons reasonLog
	strictLink.OnClose(reasons.add)
	strict.emit("message", message)
	if got := reasons.list(); !slices.Equal(got, []string{"protocol error"}) {
		t.Fatalf("refused binary: reasons = %q", got)
	}
}
