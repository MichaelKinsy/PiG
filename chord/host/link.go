package host

// Ports packages/chord/src/link.ts.

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Link is an ordered (FIFO), bidirectional frame channel between two hosts.
type Link interface {
	Closed() bool
	// Send transmits frame; it does nothing once the link is closed. It returns the size of the encoded frame as sent
	// (JSON text length in UTF-16 units, or bytes of a packed message) when the transport knows it, else 0. The
	// connection's flow control counts these sizes; a link that never reports sizes is not windowed (link.ts:7-12). A
	// transport failure is returned, as Pi's transmit throws to the caller of send.
	Send(frame Frame) (int, error)
	// Receive sets the single receiver. Frames arriving earlier are queued.
	Receive(handler func(frame Frame))
	OnClose(handler func(reason string))
	// Close closes both ends: the peer gets a bye. Without a reason the reason is "closed" (link.ts:43).
	Close(reason ...string)
}

// MalformedFrame is what a transport received that is not a valid Frame. Pi's links deliver any JSON object with a
// string "t" (link.ts:79-83) or any IPC chord2 payload (link.ts:140-141), and the connection rejects what fails
// isFrame (connection.ts:383); a Go Frame can only carry a valid frame, so the rest is delivered as this value.
type MalformedFrame struct{ Value any }

// T is the value's "t" member when it is a string.
func (frame MalformedFrame) T() string {
	if object, ok := frame.Value.(*chordjson.Object); ok {
		t, _ := object.Value("t").(string)
		return t
	}
	return ""
}

func (frame MalformedFrame) encode() *chordjson.Object {
	object, _ := frame.Value.(*chordjson.Object)
	return object
}

// frameOfValue is a received JSON value as the frame the connection sees: a valid frame, or a MalformedFrame.
func frameOfValue(value any) Frame {
	if object, ok := value.(*chordjson.Object); ok && IsFrame(object) {
		if frame, err := frameOf(object); err == nil {
			return frame
		}
	}
	return MalformedFrame{Value: value}
}

// errNotAFrame is parseFrame's TypeError (link.ts:81).
var errNotAFrame = &TypeError{Message: "not a frame"}

// parseFrame parses a JSON frame; anything but an object with a string "t", or a malformed packed payload, is a
// protocol error (link.ts parseFrame).
func parseFrame(data []byte) (Frame, error) {
	value, err := chordjson.Decode(data)
	if err != nil {
		return nil, err
	}
	value, payload, err := unpackObject(value)
	if err != nil {
		return nil, err
	}
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil, errNotAFrame
	}
	if _, ok := object.Value("t").(string); !ok {
		return nil, errNotAFrame
	}
	return withPacked(frameOfValue(object), payload), nil
}

// ─── payload packing ─────────────────────────────────────────────────────────
//
// A transport optimization, invisible above the links. A frame's payload (call args, a result value, a state snapshot)
// whose JSON exceeds PackThreshold characters is sent deflated. A host that only relays the frame forwards the deflated
// bytes unread (packed); the host that uses the payload inflates it (unpack). Pi deflates only where Node's zlib exists
// and a browser inflates asynchronously before delivery; a Go host always has compress/flate, so it is Pi's zlib host.

// PackThreshold is link.ts PACK_THRESHOLD: payloads whose JSON is longer than this many characters (UTF-16 code units)
// are deflated.
const PackThreshold = 64 * 1024

// packed is link.ts Packed: a deflated JSON payload, kept as received until a host uses it.
type packed struct{ bytes []byte }

// unpack is link.ts unpack: a frame payload as its user needs it, inflated and parsed if it arrived packed.
func unpack(value any) (any, error) {
	payload, ok := value.(packed)
	if !ok {
		return value, nil
	}
	data, err := inflateRaw(payload.bytes)
	if err != nil {
		return nil, err
	}
	return chordjson.Decode(data)
}

func inflateRaw(data []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(data))
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

// isPayloadField is link.ts PAYLOAD_FIELDS.has(field).
func isPayloadField(field string) bool {
	return field == "args" || field == "value"
}

// payloadField is the member of frame that carries its payload; "" for none.
func payloadField(frame Frame) string {
	switch frame := frame.(type) {
	case CallFrame:
		if frame.HasArgs {
			return "args"
		}
	case ResultFrame:
		if frame.HasValue {
			return "value"
		}
	case StateFrame:
		if frame.HasValue {
			return "value"
		}
	}
	return ""
}

// payloadJSON is JSON.stringify(value) for a payload that may be deflated now; nil when it arrived packed or when
// compress is off (link.ts packObject, packMessage).
func payloadJSON(value any, compress bool) ([]byte, error) {
	if _, ok := value.(packed); ok || !compress {
		return nil, nil
	}
	return chordjson.Marshal(value)
}

// packedBytes is link.ts packedBytes: the payload's deflated bytes, as received, or deflated now (level 1) when
// compress is set and its JSON is longer than PackThreshold. nil: send it as JSON.
func packedBytes(value any, compress bool, json []byte) ([]byte, error) {
	if payload, ok := value.(packed); ok {
		return payload.bytes, nil
	}
	if !compress || json == nil || utf16Length(json) <= PackThreshold {
		return nil, nil
	}
	var out bytes.Buffer
	writer, err := flate.NewWriter(&out, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(json); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// utf16Length is the JavaScript length of UTF-8 text.
func utf16Length[T string | []byte](text T) int {
	length := 0
	for _, r := range string(text) {
		length += max(utf16.RuneLen(r), 1)
	}
	return length
}

// packObject is link.ts packObject, for object transports (Node IPC, memory): a packed payload travels as
// `z: [field, base64]` in place of its member.
func packObject(frame Frame, compress bool) (*chordjson.Object, error) {
	object := frame.encode()
	field := payloadField(frame)
	if field == "" {
		return object, nil
	}
	value := object.Value(field)
	json, err := payloadJSON(value, compress)
	if err != nil {
		return nil, err
	}
	data, err := packedBytes(value, compress, json)
	if err != nil || data == nil {
		return object, err
	}
	object.Delete(field)
	object.Set("z", []any{field, base64.StdEncoding.EncodeToString(data)})
	return object, nil
}

// packedField is a payload a transport received deflated: the frame member it belongs in and its bytes.
type packedField struct {
	field   string
	payload packed
}

var errBadPackedPayload = &TypeError{Message: "bad packed payload"}

// unpackObject is link.ts unpackObject: an object message's `z: [field, base64]` becomes a packed payload in field. The
// returned message keeps field present (null) for validation, as every payload member is unknown to the frame schema;
// withPacked puts the payload into the frame.
func unpackObject(message any) (any, *packedField, error) {
	object, ok := message.(*chordjson.Object)
	if !ok || !object.Has("z") {
		return message, nil, nil
	}
	z, _ := object.Value("z").([]any)
	if len(z) < 2 {
		return nil, nil, errBadPackedPayload
	}
	field, isString := z[0].(string)
	text, isText := z[1].(string)
	if !isString || !isPayloadField(field) || !isText {
		return nil, nil, errBadPackedPayload
	}
	rest := object.Clone()
	rest.Delete("z")
	rest.Set(field, nil)
	return rest, &packedField{field: field, payload: packed{bytes: decodeBase64(text)}}, nil
}

// decodeBase64 is Node's Buffer.from(text, "base64"): standard and URL-safe digits, other characters skipped, decoding
// stops at the first "=", and leftover bits that make no whole byte are dropped.
func decodeBase64(text string) []byte {
	out := make([]byte, 0, len(text)*3/4) //portlint:allow numbers capacity hint only; the decoded bytes do not depend on it
	var bits uint32
	count := 0
	for index := range len(text) {
		var digit byte
		switch c := text[index]; {
		case 'A' <= c && c <= 'Z':
			digit = c - 'A'
		case 'a' <= c && c <= 'z':
			digit = c - 'a' + 26
		case '0' <= c && c <= '9':
			digit = c - '0' + 52
		case c == '+' || c == '-':
			digit = 62
		case c == '/' || c == '_':
			digit = 63
		case c == '=':
			return out
		default:
			continue
		}
		bits = bits<<6 | uint32(digit)
		count += 6
		if count >= 8 {
			count -= 8
			out = append(out, byte(bits>>count))
			bits &= 1<<count - 1
		}
	}
	return out
}

// withPacked is frame with a packed payload in its member, as link.ts delivers `{ ...rest, [field]: Packed }`. A
// payload in a member the frame type does not carry is ignored, as the connection ignores an unknown member.
func withPacked(frame Frame, field *packedField) Frame {
	if field == nil {
		return frame
	}
	switch typed := frame.(type) {
	case CallFrame:
		if field.field == "args" {
			typed.Args, typed.HasArgs = field.payload, true
		}
		return typed
	case ResultFrame:
		if field.field == "value" {
			typed.Value, typed.HasValue = field.payload, true
		}
		return typed
	case StateFrame:
		if field.field == "value" {
			typed.Value, typed.HasValue = field.payload, true
		}
		return typed
	}
	return frame
}

// packMessage is link.ts packMessage, for WebSockets: a frame with a packed payload is a binary message,
// `[envelope length: u32 BE][envelope JSON, with z: field][deflated payload]`; any other frame is text, with the
// payload's JSON spliced in last rather than encoded twice. The result is a string (text) or []byte (binary).
func packMessage(frame Frame, compress bool) (any, error) {
	field := payloadField(frame)
	if field == "" {
		data, err := EncodeFrame(frame)
		return string(data), err
	}
	envelope := frame.encode()
	value := envelope.Value(field)
	var json []byte
	if _, ok := value.(packed); !ok {
		var err error
		if json, err = chordjson.Marshal(value); err != nil {
			return nil, err
		}
	}
	data, err := packedBytes(value, compress, json)
	if err != nil {
		return nil, err
	}
	envelope.Delete(field)
	if data == nil {
		return splice(envelope, field, json)
	}
	envelope.Set("z", field)
	head, err := envelope.MarshalJSON()
	if err != nil {
		return nil, err
	}
	size := uint64(len(head))
	if size > math.MaxUint32 {
		return nil, errors.New("frame envelope too large")
	}
	message := make([]byte, 4+len(head)+len(data))
	binary.BigEndian.PutUint32(message, uint32(size))
	copy(message[4:], head)
	copy(message[4+len(head):], data)
	return message, nil
}

// splice is `${head.slice(0, -1)},${JSON.stringify(field)}:${json}}`: envelope's JSON with field set to json last.
func splice(envelope *chordjson.Object, field string, json []byte) (string, error) {
	head, err := envelope.MarshalJSON()
	if err != nil {
		return "", err
	}
	key, err := chordjson.Marshal(field)
	if err != nil {
		return "", err
	}
	return string(head[:len(head)-1]) + "," + string(key) + ":" + string(json) + "}", nil
}

// splitMessage is link.ts splitMessage: the envelope of a binary message (without z), its payload field and bytes.
func splitMessage(data []byte) (*chordjson.Object, string, []byte, error) {
	if len(data) < 4 {
		return nil, "", nil, errors.New("offset is outside the bounds of the message")
	}
	end := len(data)
	if length := uint64(binary.BigEndian.Uint32(data)); length < uint64(len(data)-4) {
		end = 4 + int(length)
	}
	value, err := chordjson.Decode(data[4:end])
	if err != nil {
		return nil, "", nil, err
	}
	object, ok := value.(*chordjson.Object)
	if !ok {
		if _, isArray := value.([]any); isArray {
			return nil, "", nil, errBadPackedPayload
		}
		return nil, "", nil, &TypeError{Message: "bad envelope"}
	}
	field, ok := object.Value("z").(string)
	if !ok || !isPayloadField(field) {
		return nil, "", nil, errBadPackedPayload
	}
	envelope := object.Clone()
	envelope.Delete("z")
	return envelope, field, slices.Clone(data[end:]), nil
}

// InflateMessage is link.ts inflateMessage: the text frame of a binary (packed) WebSocket message, inflated, for a
// transport that relays text frames only (the TUI launcher feeding its sandbox).
func InflateMessage(data []byte) (string, error) {
	envelope, field, payload, err := splitMessage(data)
	if err != nil {
		return "", err
	}
	json, err := inflateRaw(payload)
	if err != nil {
		return "", err
	}
	return splice(envelope, field, json)
}

// byeReason is `frame.reason ?? "peer closed"` of a received bye (link.ts:57).
func byeReason(frame Frame) string {
	switch frame := frame.(type) {
	case ByeFrame:
		if frame.Reason != nil {
			return *frame.Reason
		}
	case MalformedFrame:
		if object, ok := frame.Value.(*chordjson.Object); ok {
			if reason, ok := object.Get("reason"); ok && reason != nil {
				if text, ok := reason.(string); ok {
					return text
				}
				return fmt.Sprint(reason)
			}
		}
	}
	return "peer closed"
}

// transport is what a link's transport implements under baseLink: transmit and shutdown (link.ts:25-26).
type transport interface {
	transmit(frame Frame) (int, error)
	shutdown()
}

// baseLink is Pi's BaseLink: the receive queue, close handlers and bye. Frames reach the receiver one at a time and
// in arrival order, also when a transport delivers from another goroutine or a handler re-enters the link.
type baseLink struct {
	transport transport

	mu            sync.Mutex
	handler       func(frame Frame)
	queue         []Frame
	delivering    bool
	closeHandlers []func(reason string)
	closed        bool
}

func (link *baseLink) Closed() bool {
	link.mu.Lock()
	defer link.mu.Unlock()
	return link.closed
}

func (link *baseLink) Send(frame Frame) (int, error) {
	if link.Closed() {
		return 0, nil
	}
	return link.transport.transmit(frame)
}

func (link *baseLink) Receive(handler func(frame Frame)) {
	link.mu.Lock()
	link.handler = handler
	link.mu.Unlock()
	link.drain()
}

// OnClose registers handler; on a closed link it is called at once with "closed" (link.ts:38-41). Handlers are a
// set, as Pi's: the same func value registered again is not added twice. Identity is the func value itself (funcID),
// so it is the same handler only when the same value is passed again, as Pi's function identity.
func (link *baseLink) OnClose(handler func(reason string)) {
	link.mu.Lock()
	if !link.closed {
		id := funcID(handler)
		if !slices.ContainsFunc(link.closeHandlers, func(registered func(string)) bool { return funcID(registered) == id }) {
			link.closeHandlers = append(link.closeHandlers, handler)
		}
		link.mu.Unlock()
		return
	}
	link.mu.Unlock()
	handler("closed")
}

// funcID is a func value's identity: the pointer to its closure (Go's func value representation). Two evaluations of a
// closure capturing variables differ; passing the same value again is the same.
func funcID[F any](fn F) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&fn)) //nolint:gosec // G103: reads the func value's closure pointer, its identity
}

func (link *baseLink) Close(reason ...string) {
	text := "closed"
	if len(reason) > 0 {
		text = reason[0]
	}
	// The transport may already be gone (link.ts:45-49).
	link.end(text, func() { _, _ = link.transport.transmit(ByeFrame{Reason: &text}) })
}

// deliver delivers a parsed frame from the transport (link.ts:54-62).
func (link *baseLink) deliver(frame Frame) {
	link.mu.Lock()
	if link.closed {
		link.mu.Unlock()
		return
	}
	if frame.T() == "bye" {
		link.mu.Unlock()
		link.ended(byeReason(frame))
		return
	}
	link.queue = append(link.queue, frame)
	link.mu.Unlock()
	link.drain()
}

// drain hands queued frames to the handler in order. One caller drains at a time; a frame delivered meanwhile (from a
// handler or another goroutine) is handed over by that caller after the frames before it.
func (link *baseLink) drain() {
	link.mu.Lock()
	if link.delivering {
		link.mu.Unlock()
		return
	}
	link.delivering = true
	defer func() {
		link.mu.Lock()
		link.delivering = false
		link.mu.Unlock()
	}()
	for {
		if link.closed || link.handler == nil || len(link.queue) == 0 {
			link.mu.Unlock()
			return
		}
		frame := link.queue[0]
		link.queue = link.queue[1:]
		handler := link.handler
		link.mu.Unlock()
		handler(frame)
		link.mu.Lock()
	}
}

// ended marks the link closed (transport ended) and notifies once (link.ts:65-75).
func (link *baseLink) ended(reason string) { link.end(reason, nil) }

// end marks the link closed, runs beforeShutdown (if any), shuts the transport down and calls the close handlers with
// reason, once. A link ended by Close sends its bye in beforeShutdown: the peer's side of a transport that delivers
// from its own goroutine can answer the bye, and end this link ("peer closed"), before Close's own step would run, so
// the link is marked closed first and the answer finds it ended with Close's reason, as upstream's one synchronous block
// leaves it.
func (link *baseLink) end(reason string, beforeShutdown func()) {
	link.mu.Lock()
	if link.closed {
		link.mu.Unlock()
		return
	}
	link.closed = true
	link.queue = nil
	handlers := link.closeHandlers
	link.closeHandlers = nil
	link.mu.Unlock()
	defer func() {
		for _, handler := range handlers {
			handler(reason)
		}
	}()
	if beforeShutdown != nil {
		beforeShutdown()
	}
	link.transport.shutdown()
}

// ─── memory ──────────────────────────────────────────────────────────────────

// MemoryLink is one end of an in-memory link pair.
type MemoryLink struct {
	baseLink
	Peer *MemoryLink

	severedMu sync.Mutex
	severed   bool
	// receiver is the loop of the host whose connection receives on this end (nil for other receivers).
	receiver *loop
}

func (link *MemoryLink) setReceiver(receiver *loop) {
	link.severedMu.Lock()
	link.receiver = receiver
	link.severedMu.Unlock()
}

func (link *MemoryLink) receiverLoop() *loop {
	link.severedMu.Lock()
	defer link.severedMu.Unlock()
	return link.receiver
}

// flush waits until everything this link sent so far was handled by the peer: its deliveries left the shared queue
// and, when a host receives on the peer, that host's loop ran the tasks they queued (Pi: the microtasks an awaiting
// caller lets run before it resumes, link.ts:103-112). It must not be called from the queue or the peer's loop.
func (link *MemoryLink) flush() {
	handed := make(chan struct{})
	memoryDeliveries.then(func() { close(handed) })
	<-handed
	if link.Peer == nil {
		return
	}
	if receiver := link.Peer.receiverLoop(); receiver != nil {
		handled := make(chan struct{})
		receiver.post(func() { close(handled) })
		<-handled
	}
}

// Sever simulates a dead network: frames are dropped silently in both directions and no close is signalled.
func (link *MemoryLink) Sever() {
	link.setSevered()
	if link.Peer != nil {
		link.Peer.setSevered()
	}
}

func (link *MemoryLink) setSevered() {
	link.severedMu.Lock()
	link.severed = true
	link.severedMu.Unlock()
}

func (link *MemoryLink) isSevered() bool {
	link.severedMu.Lock()
	defer link.severedMu.Unlock()
	return link.severed
}

func (link *MemoryLink) transmit(frame Frame) (int, error) {
	if link.isSevered() {
		return 0, nil
	}
	// A JSON round trip detaches the frame (and rejects what JSON would mangle silently); large payloads are packed.
	object, err := packObject(frame, true)
	if err != nil {
		return 0, err
	}
	data, err := object.MarshalJSON()
	if err != nil {
		return 0, err
	}
	peer := link.Peer
	// Delivery is FIFO and asynchronous, like a real transport (link.ts:103-106).
	memoryDeliveries.then(func() {
		if link.isSevered() || peer == nil {
			return
		}
		parsed, err := parseFrame(data)
		if err != nil {
			panic(err)
		}
		peer.deliver(parsed)
	})
	return utf16Length(data), nil
}

func (link *MemoryLink) shutdown() {
	peer := link.Peer
	memoryDeliveries.then(func() {
		if peer != nil {
			peer.ended("peer closed")
		}
	})
}

// memoryQueue is the queue memory links deliver through. Pi chains each direction's deliveries on a promise
// (link.ts:90, 104-106), and every chain runs on the one microtask queue: deliveries reach their peers in the order
// they were sent, across links too. One process-wide queue keeps that order; a link's tasks keep their own order in
// it. It orders only the hand-off: a host's connection queues each frame on its own loop and returns (newConnection),
// so a busy host holds up neither the queue nor other links.
type memoryQueue struct {
	mu      sync.Mutex
	pending []func()
	pumping bool
}

var memoryDeliveries memoryQueue

// then appends task to the queue: tasks run one at a time, in order, after the caller returns. The goroutine that
// runs them exits when the queue is empty.
func (queue *memoryQueue) then(task func()) {
	queue.mu.Lock()
	queue.pending = append(queue.pending, task)
	if queue.pumping {
		queue.mu.Unlock()
		return
	}
	queue.pumping = true
	queue.mu.Unlock()
	//portlint:allow asyncorder one pump at a time runs the process-wide FIFO in queue order (link.ts:90 `queue = queue.then(task)`); pumping hands off the drain
	go queue.pump() //portlint:allow golifetime the pump returns once the queue is empty; then() starts the next one
}

func (queue *memoryQueue) pump() {
	for {
		queue.mu.Lock()
		if len(queue.pending) == 0 {
			queue.pumping = false
			queue.mu.Unlock()
			return
		}
		task := queue.pending[0]
		queue.pending[0] = nil
		queue.pending = queue.pending[1:]
		queue.mu.Unlock()
		task()
	}
}

func newMemoryLink() *MemoryLink {
	link := &MemoryLink{}
	link.transport = link
	return link
}

// MemoryLinkPair is two connected in-memory links (tests).
func MemoryLinkPair() (*MemoryLink, *MemoryLink) {
	a := newMemoryLink()
	b := newMemoryLink()
	a.Peer = b
	b.Peer = a
	return a, b
}

// ─── Node IPC ────────────────────────────────────────────────────────────────

// IpcListener is a listener on an IpcChannel. Off removes a listener by its pointer, as Node removes one by function
// identity (Go functions are not comparable). A "disconnect" listener is called with a nil message.
type IpcListener func(message any)

// IpcChannel is `process` in a forked child, or the parent's child process reference. Messages are JSON values
// (chordjson), as Node's default IPC serialization carries them.
type IpcChannel interface {
	Connected() bool
	// Send writes message. With a callback, a failed write (the peer process exited and the pipe is broken: EPIPE) is
	// reported to it, off the sender's stack, instead of being raised where nobody would handle it; nil is Node's send
	// without one. The error may carry a Node error code (a Code() string method).
	Send(message any, callback func(err error)) bool
	// On adds listener for event "message" or "disconnect".
	On(event string, listener *IpcListener)
	// Off removes listener from event "message" or "disconnect".
	Off(event string, listener *IpcListener)
}

// ipcSendErrorText is `error.code ?? error.message` of a failed IPC write.
func ipcSendErrorText(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() != "" {
		return coded.Code()
	}
	return err.Error()
}

// IpcLink carries Chord frames on an IPC channel wrapped as `{ chord2: frame }`; other messages (control) pass by. Both
// ends are processes of one application, so large payloads are packed both ways.
type IpcLink struct {
	baseLink
	channel      IpcChannel
	onMessage    *IpcListener
	onDisconnect *IpcListener
	// onSent: a write can fail after the peer exited and before the channel reports disconnect (an ack or a bye sent
	// then). The peer is gone either way: the link ends, as on disconnect.
	onSent func(err error)
}

// NewIpcLink is Pi's ipcLink.
func NewIpcLink(channel IpcChannel) *IpcLink {
	link := &IpcLink{channel: channel}
	link.transport = link
	onMessage := IpcListener(func(message any) {
		object, ok := message.(*chordjson.Object)
		if !ok {
			return
		}
		frame, ok := object.Get("chord2")
		if !ok {
			return
		}
		if !link.receiveMessage(frame) {
			link.Close("protocol error")
		}
	})
	onDisconnect := IpcListener(func(any) { link.ended("ipc disconnected") })
	link.onSent = func(err error) {
		if err != nil {
			link.ended("ipc send failed: " + ipcSendErrorText(err))
		}
	}
	link.onMessage = &onMessage
	link.onDisconnect = &onDisconnect
	channel.On("message", link.onMessage)
	channel.On("disconnect", link.onDisconnect)
	return link
}

// receiveMessage unpacks and delivers one chord2 payload; false when unpacking or delivery threw (link.ts IpcLink's
// message listener catches both).
func (link *IpcLink) receiveMessage(frame any) (ok bool) {
	defer func() {
		// upstream: packages/chord/src/link.ts:IpcLink (the message listener's bare `catch {}` closes the link with "protocol error")
		if recovered := recover(); recovered != nil {
			ok = false
		}
	}()
	value, payload, err := unpackObject(frame)
	if err != nil {
		return false
	}
	link.deliver(withPacked(frameOfValue(value), payload))
	return true
}

// transmit reports no size: Node serializes IPC messages itself, so the link is not windowed; both ends are local
// processes (link.ts:292-296).
func (link *IpcLink) transmit(frame Frame) (int, error) {
	if !link.channel.Connected() {
		return 0, nil
	}
	object, err := packObject(frame, true)
	if err != nil {
		return 0, err
	}
	link.channel.Send(chordjson.ObjectOf("chord2", object), link.onSent)
	return 0, nil
}

func (link *IpcLink) shutdown() {
	link.channel.Off("message", link.onMessage)
	link.channel.Off("disconnect", link.onDisconnect)
}

// ─── WebSocket ───────────────────────────────────────────────────────────────

// WebSocketMessage is the event of a WebSocket "message" listener.
type WebSocketMessage struct{ Data any }

// WebSocketLike is the WHATWG WebSocket surface.
type WebSocketLike interface {
	ReadyState() int
	// Send sends a string as a text message and a []byte as a binary message.
	Send(data any) error
	// Close closes the socket; code 0 and reason "" are absent.
	Close(code int, reason string)
	// AddEventListener adds listener for "message" (called with the message: a string for a text message, a []byte
	// for a binary one, as binaryType "arraybuffer") or "open", "close" and "error" (called with a zero
	// WebSocketMessage).
	AddEventListener(typ string, listener func(event WebSocketMessage))
}

const webSocketOpen = 1

// WebSocketLinkOptions are link.ts WebSocketLinkOptions.
type WebSocketLinkOptions struct {
	// Compress deflates large payloads of outgoing frames (default false; the server's side of client sockets).
	Compress bool
	// AcceptPacked accepts packed (binary) frames from the peer (nil: true; false for a server's untrusted clients).
	AcceptPacked *bool
}

// WebSocketLink is a link over a WebSocket.
type WebSocketLink struct {
	baseLink
	socket       WebSocketLike
	compress     bool
	acceptPacked bool
	outboxMu     sync.Mutex
	// outbox holds the messages sent while connecting: strings (text) and []byte (binary).
	outbox []any
}

// NewWebSocketLink is Pi's webSocketLink.
func NewWebSocketLink(socket WebSocketLike, options WebSocketLinkOptions) *WebSocketLink {
	link := &WebSocketLink{socket: socket, compress: options.Compress, acceptPacked: options.AcceptPacked == nil || *options.AcceptPacked}
	link.transport = link
	// Upstream's socket becomes OPEN and flushes the outbox in one task (link.ts:184-186); here the flush holds outboxMu,
	// which transmit takes too, so frames leave in send order.
	socket.AddEventListener("open", func(WebSocketMessage) {
		link.outboxMu.Lock()
		defer link.outboxMu.Unlock()
		for _, data := range link.outbox {
			_ = socket.Send(data)
		}
		link.outbox = nil
	})
	socket.AddEventListener("message", func(event WebSocketMessage) {
		if !link.receiveMessage(event.Data) {
			link.Close("protocol error")
		}
	})
	socket.AddEventListener("close", func(WebSocketMessage) { link.ended("socket closed") })
	return link
}

// receiveMessage parses and delivers one message; false when parsing or delivery threw (link.ts WebSocketLink's message
// listener catches both). A text message is a JSON frame; a binary one is a frame with a packed payload, refused unless
// packed frames are accepted.
func (link *WebSocketLink) receiveMessage(data any) (ok bool) {
	defer func() {
		// upstream: packages/chord/src/link.ts:WebSocketLink (the message listener's bare `catch {}` closes the link with "protocol error")
		if recovered := recover(); recovered != nil {
			ok = false
		}
	}()
	if text, isText := data.(string); isText {
		frame, err := parseFrame([]byte(text))
		if err != nil {
			return false
		}
		link.deliver(frame)
		return true
	}
	message, isBinary := data.([]byte)
	if !link.acceptPacked || !isBinary {
		return false
	}
	envelope, field, payload, err := splitMessage(message)
	if err != nil {
		return false
	}
	envelope.Set(field, nil)
	link.deliver(withPacked(frameOfValue(envelope), &packedField{field: field, payload: packed{bytes: payload}}))
	return true
}

func (link *WebSocketLink) transmit(frame Frame) (int, error) {
	data, err := packMessage(frame, link.compress)
	if err != nil {
		return 0, err
	}
	size := 0
	switch data := data.(type) {
	case string:
		size = utf16Length(data)
	case []byte:
		size = len(data)
	}
	link.outboxMu.Lock()
	defer link.outboxMu.Unlock()
	// A frame queued while connecting leaves first: the socket may report OPEN before its open listener flushed them.
	if link.socket.ReadyState() == webSocketOpen && len(link.outbox) == 0 {
		return size, link.socket.Send(data)
	}
	link.outbox = append(link.outbox, data)
	return size, nil
}

func (link *WebSocketLink) shutdown() {
	link.outboxMu.Lock()
	link.outbox = nil
	link.outboxMu.Unlock()
	if link.socket.ReadyState() <= webSocketOpen {
		link.socket.Close(1000, "")
	}
}
