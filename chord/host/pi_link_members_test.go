package host

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// recordingIpcChannel is an IpcChannel that records every call IpcLink makes on it.
type recordingIpcChannel struct {
	connected bool
	calls     []string
	sent      []any
	listeners map[string][]*IpcListener
}

func (channel *recordingIpcChannel) Connected() bool {
	channel.calls = append(channel.calls, "connected")
	return channel.connected
}

func (channel *recordingIpcChannel) Send(message any, _ func(err error)) bool {
	channel.calls = append(channel.calls, "send")
	channel.sent = append(channel.sent, message)
	return true
}

func (channel *recordingIpcChannel) On(event string, listener *IpcListener) {
	channel.calls = append(channel.calls, "on "+event)
	channel.listeners[event] = append(channel.listeners[event], listener)
}

func (channel *recordingIpcChannel) Off(event string, listener *IpcListener) {
	channel.calls = append(channel.calls, "off "+event)
	channel.listeners[event] = slices.DeleteFunc(channel.listeners[event], func(l *IpcListener) bool { return l == listener })
}

func (channel *recordingIpcChannel) emit(event string, message any) {
	for _, listener := range slices.Clone(channel.listeners[event]) {
		(*listener)(message)
	}
}

// Pi packages/chord/src/link.ts:5-17 declares the Link interface (closed, send, receive, onClose, close) and
// link.ts:29-49 its shared implementation: send does nothing once closed, receive hands over the frames queued before it,
// onClose on a closed link is called at once with "closed". Every Go link (MemoryLink, IpcLink, WebSocketLink)
// satisfies Link with that implementation.
func TestPiLinkMembersClosedSendReceiveOnClose(t *testing.T) {
	type pair struct {
		name    string
		link    Link
		inject  func(frame string)
		sentLen func() int
	}
	memA, memB := MemoryLinkPair()
	ipc := &recordingIpcChannel{connected: true, listeners: map[string][]*IpcListener{}}
	socket := newFakeWebSocket(webSocketOpen)
	pairs := []pair{
		{"memory", memB, func(string) { _, _ = memA.Send(PingFrame{N: 7}) }, nil},
		{"ipc", NewIpcLink(ipc), func(string) {
			ipc.emit("message", chordjson.ObjectOf("chord2", chordjson.ObjectOf("t", "ping", "n", float64(7))))
		}, func() int { return len(ipc.sent) }},
		{"websocket", NewWebSocketLink(socket, WebSocketLinkOptions{}), func(text string) { socket.emit("message", text) },
			func() int { return len(socket.sent) }},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			link := p.link
			if link.Closed() {
				t.Fatal("a new link is closed")
			}
			// Frames arriving before receive are queued, then handed over (link.ts:13-14, 41-44).
			p.inject(`{"t":"ping","n":7}`)
			var frames frameLog
			waitFor(t, "frame queued", func() bool {
				link.Receive(frames.add)
				return len(frames.list()) == 1
			})
			if got := frames.list()[0]; got != (PingFrame{N: 7}) {
				t.Fatalf("received %#v", got)
			}
			var reasons reasonLog
			link.OnClose(reasons.add)
			if _, err := link.Send(PongFrame{N: 1}); err != nil {
				t.Fatal(err)
			}
			link.Close("done")
			if !link.Closed() {
				t.Fatal("Closed() after Close = false")
			}
			if got := reasons.list(); !slices.Equal(got, []string{"done"}) {
				t.Fatalf("onClose reasons = %q", got)
			}
			// Send on a closed link does nothing (link.ts:36-38).
			if p.sentLen != nil {
				before := p.sentLen()
				if size, err := link.Send(PongFrame{N: 2}); size != 0 || err != nil || p.sentLen() != before {
					t.Fatalf("send after close: size %d err %v sent %d → %d", size, err, before, p.sentLen())
				}
			}
			// onClose on a closed link is called at once with "closed" (link.ts:46-48).
			var late reasonLog
			link.OnClose(late.add)
			if got := late.list(); !slices.Equal(got, []string{"closed"}) {
				t.Fatalf("late onClose = %q", got)
			}
		})
	}
	waitFor(t, "memory peer closed", memA.Closed)
}

// Pi packages/chord/src/link.ts:284-291 declares IpcChannel (connected, send, on/off "message"/"disconnect") and
// link.ts:298-326 how IpcLink drives it: on("message") and on("disconnect") at construction, send({chord2}) only while
// connected, off("message") and off("disconnect") at shutdown, and the disconnect listener ends the link. Messages
// without chord2 (control) pass by the link (link.ts:294, 301-302): the channel's other users call connected, send,
// on and off on the same IpcChannel, and IpcLink's off removes only its own listeners.
func TestPiIpcChannelMembersDrivenByIpcLink(t *testing.T) {
	var channel IpcChannel = &recordingIpcChannel{connected: true, listeners: map[string][]*IpcListener{}}
	ipc := channel.(*recordingIpcChannel)
	link := NewIpcLink(channel)
	if want := []string{"on message", "on disconnect"}; !slices.Equal(ipc.calls, want) {
		t.Fatalf("construction calls = %q, want %q", ipc.calls, want)
	}
	// The control side shares the channel: its listeners sit beside the link's, its messages pass the link by.
	var controlMessages, controlGone []any
	control := IpcListener(func(message any) { controlMessages = append(controlMessages, message) })
	gone := IpcListener(func(message any) { controlGone = append(controlGone, message) })
	channel.On("message", &control)
	channel.On("disconnect", &gone)
	if !channel.Connected() {
		t.Fatal("Connected() = false on a connected channel")
	}
	if !channel.Send(chordjson.ObjectOf("type", "ready"), nil) || jsonText(t, ipc.sent[0]) != `{"type":"ready"}` {
		t.Fatalf("control send: sent %d", len(ipc.sent))
	}
	var linkFrames frameLog
	link.Receive(linkFrames.add)
	ipc.emit("message", chordjson.ObjectOf("type", "control"))
	if len(controlMessages) != 1 || len(linkFrames.list()) != 0 || link.Closed() {
		t.Fatalf("control message: control %d link frames %d closed %v", len(controlMessages), len(linkFrames.list()), link.Closed())
	}
	ipc.sent = nil
	ipc.calls = nil
	_, _ = link.Send(PingFrame{N: 1})
	if want := []string{"connected", "send"}; !slices.Equal(ipc.calls, want) {
		t.Fatalf("send calls = %q, want %q", ipc.calls, want)
	}
	if got := jsonText(t, ipc.sent[0]); got != `{"chord2":{"t":"ping","n":1}}` {
		t.Fatalf("sent %s", got)
	}
	ipc.calls = nil
	ipc.connected = false
	_, _ = link.Send(PingFrame{N: 2})
	if want := []string{"connected"}; !slices.Equal(ipc.calls, want) || len(ipc.sent) != 1 {
		t.Fatalf("disconnected send calls = %q sent %d", ipc.calls, len(ipc.sent))
	}
	ipc.calls = nil
	var reasons reasonLog
	link.OnClose(reasons.add)
	ipc.emit("disconnect", nil)
	if got := reasons.list(); !slices.Equal(got, []string{"ipc disconnected"}) {
		t.Fatalf("reasons = %q", got)
	}
	if want := []string{"off message", "off disconnect"}; !slices.Equal(ipc.calls, want) {
		t.Fatalf("shutdown calls = %q, want %q", ipc.calls, want)
	}
	// Only the link's listeners are gone; the control side's stay until it removes them itself.
	if len(ipc.listeners["message"]) != 1 || ipc.listeners["message"][0] != &control || len(ipc.listeners["disconnect"]) != 1 || len(controlGone) != 1 {
		t.Fatalf("listeners after the link ended: %v (control saw %d disconnects)", ipc.listeners, len(controlGone))
	}
	channel.Off("message", &control)
	channel.Off("disconnect", &gone)
	ipc.emit("message", chordjson.ObjectOf("type", "late"))
	ipc.emit("disconnect", nil)
	if len(controlMessages) != 1 || len(controlGone) != 1 || len(ipc.listeners["message"]) != 0 || len(ipc.listeners["disconnect"]) != 0 {
		t.Fatalf("after Off: control %d gone %d listeners %v", len(controlMessages), len(controlGone), ipc.listeners)
	}
	if channel.Connected() {
		t.Fatal("Connected() = true after the channel disconnected")
	}
}

// Pi packages/chord/src/link.ts:113 PACK_THRESHOLD = 64 * 1024, and link.ts:154 packs a payload only when its JSON is
// longer than PACK_THRESHOLD: a payload of exactly PACK_THRESHOLD characters travels as JSON, one character more is
// deflated.
func TestPiPackThresholdBoundsPackedPayloads(t *testing.T) {
	if PackThreshold != 64*1024 {
		t.Fatalf("PackThreshold = %d", PackThreshold)
	}
	at := strings.Repeat("x", PackThreshold-2) // JSON "…" is PackThreshold characters
	over := at + "x"
	a, b := MemoryLinkPair()
	var frames frameLog
	b.Receive(frames.add)
	_, _ = a.Send(ResultFrame{ID: 1, Value: at, HasValue: true})
	_, _ = a.Send(ResultFrame{ID: 2, Value: over, HasValue: true})
	waitFor(t, "two frames", func() bool { return len(frames.list()) == 2 })
	got := frames.list()
	if value, ok := got[0].(ResultFrame).Value.(string); !ok || value != at {
		t.Fatalf("payload at the threshold arrived as %T", got[0].(ResultFrame).Value)
	}
	payload, ok := got[1].(ResultFrame).Value.(packed)
	if !ok {
		t.Fatalf("payload over the threshold arrived as %T", got[1].(ResultFrame).Value)
	}
	if value, err := unpack(payload); err != nil || value != over {
		t.Fatalf("unpacked: %v", err)
	}
}

// Pi packages/chord/src/link.ts:344-349 WebSocketLinkOptions: compress (default false) deflates large outgoing payloads
// (link.ts:359, 392: packMessage(frame, compress)); acceptPacked (default true) refuses binary frames when false
// (link.ts:360, 373: a protocol error).
func TestPiWebSocketLinkOptionsCompressAndAcceptPacked(t *testing.T) {
	big := strings.Repeat("y", PackThreshold+1)
	plain := newFakeWebSocket(webSocketOpen)
	_, _ = NewWebSocketLink(plain, WebSocketLinkOptions{}).Send(ResultFrame{ID: 1, Value: big, HasValue: true})
	if len(plain.sent) != 1 || len(plain.binary) != 0 {
		t.Fatalf("compress unset: text %d binary %d", len(plain.sent), len(plain.binary))
	}
	compressed := newFakeWebSocket(webSocketOpen)
	_, _ = NewWebSocketLink(compressed, WebSocketLinkOptions{Compress: true}).Send(ResultFrame{ID: 1, Value: big, HasValue: true})
	if len(compressed.sent) != 0 || len(compressed.binary) != 1 {
		t.Fatalf("Compress: text %d binary %d", len(compressed.sent), len(compressed.binary))
	}
	message := compressed.binary[0]

	accept := true
	for _, options := range []struct {
		name   string
		accept *bool
		closed bool
	}{{"default", nil, false}, {"true", &accept, false}, {"false", new(bool), true}} {
		socket := newFakeWebSocket(webSocketOpen)
		link := NewWebSocketLink(socket, WebSocketLinkOptions{AcceptPacked: options.accept})
		var frames frameLog
		link.Receive(frames.add)
		var reasons reasonLog
		link.OnClose(reasons.add)
		socket.emit("message", message)
		if options.closed {
			if got := reasons.list(); !slices.Equal(got, []string{"protocol error"}) || len(frames.list()) != 0 {
				t.Fatalf("AcceptPacked %s: reasons %q frames %d", options.name, got, len(frames.list()))
			}
			continue
		}
		if len(reasons.list()) != 0 || len(frames.list()) != 1 {
			t.Fatalf("AcceptPacked %s: reasons %q frames %d", options.name, reasons.list(), len(frames.list()))
		}
		if value, err := unpack(frames.list()[0].(ResultFrame).Value); err != nil || value != big {
			t.Fatalf("AcceptPacked %s: unpacked %v", options.name, err)
		}
	}
}
