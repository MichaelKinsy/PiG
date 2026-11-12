package http

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// The `ws` WebSocket as plugin code and the links endpoint use it (Pi's `WebSocketHandle = import("ws").WebSocket`; `ws` is
// an optional peer dependency Pi imports only in the HTTP directory). Go has no `ws`: WebSocket is the part of it the
// component and its plugins use, over github.com/gorilla/websocket, with `ws`'s observable behavior: events (`message`,
// `close`, `error`, `ping`, `pong`) emitted in order from the reading task, `send` queued and its pending bytes counted
// (`bufferedAmount`), `close(code, reason)` sent after what is queued and the connection terminated 30 s later when the peer
// never answers, `terminate` closing the connection at once (close code 1006), a frame above the largest payload
// answered with a 1009 close frame and an `error` event. Go-only file.
//
// Reading starts at once for a plugin socket too, so pings, pongs and a close frame are answered while its handler runs
// (the heartbeat depends on it). Pi's handler attaches its listeners in its synchronous part, before any frame is read; a Go
// handler runs to its return, so the messages that arrive meanwhile are held and delivered in order when it returned.

// WebSocketHandle is Pi's `WebSocketHandle`: the socket a plugin's handler gets.
type WebSocketHandle = WebSocket

// WebSocket ready states, as `ws` numbers them.
const (
	WebSocketConnecting  = 0
	WebSocketOpenState   = 1
	WebSocketClosing     = 2
	WebSocketClosedState = 3
)

// Events beyond link's (`open`, `close`, `error`, `message`): the control frames, and a frame of any kind arriving (the
// heartbeat's).
const (
	webSocketPing  link.WebSocketEvent = "ping"
	webSocketPong  link.WebSocketEvent = "pong"
	webSocketFrame link.WebSocketEvent = "frame"
)

// Message is a data frame: its payload and whether it was a binary frame (else text, UTF-8).
type Message struct {
	Data   []byte
	Binary bool
}

// Text is the payload as text.
func (m Message) Text() string { return string(m.Data) }

// CloseEvent is the data of a `close` event: the close code (1006 for a connection that ended without a close frame) and
// reason.
type CloseEvent struct {
	Code   int
	Reason string
}

// closeTimeout is how long `ws` waits for the peer's close frame before it destroys the connection.
const closeTimeout = 30 * time.Second

type socketListener struct {
	event link.WebSocketEvent
	fn    func(data any)
}

type queued struct {
	frame     int
	data      []byte
	code      int
	hasCode   bool
	reason    string
	done      func(error)
	byteCount int64
}

// WebSocket is an upgraded connection.
type WebSocket struct {
	conn *websocket.Conn

	mu           sync.Mutex
	state        int
	listeners    []*socketListener
	queue        []queued
	writing      bool
	buffered     int64
	started      bool
	closeClaimed atomic.Bool
	timer        *time.Timer

	// deliverMu serializes the delivery of messages; holding keeps them in held (until release) while a handler runs.
	deliverMu sync.Mutex
	holding   bool
	held      []Message

	// attached sees each listener right after it was registered, before its attaching call goes on (a test's: the close that
	// races a registration, forced).
	attached func()
}

// newWebSocket is the socket over conn (an upgraded server connection).
func newWebSocket(conn *websocket.Conn) *WebSocket {
	socket := &WebSocket{conn: conn, state: WebSocketOpenState}
	conn.SetPingHandler(func(payload string) error {
		// The pong first, then the event, as `ws` does.
		//portlint:allow clock a write deadline of the connection: wall-clock I/O, not behavior a test drives
		err := conn.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
		socket.emit(webSocketPing, []byte(payload))
		if errors.Is(err, websocket.ErrCloseSent) {
			return nil
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil
		}
		return err
	})
	conn.SetPongHandler(func(payload string) error {
		socket.emit(webSocketPong, []byte(payload))
		return nil
	})
	return socket
}

// ReadyState is 1 open, 2 closing, 3 closed.
func (s *WebSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// BufferedAmount is the bytes `Send` queued that have not been written.
func (s *WebSocket) BufferedAmount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffered
}

// on attaches a listener for event and returns its removal (the data is what emit gets).
func (s *WebSocket) on(event link.WebSocketEvent, fn func(data any)) (remove func()) {
	listener := &socketListener{event: event, fn: fn}
	s.attach(listener)
	return func() { s.detach(listener) }
}

// attach registers a listener that is complete: emit may run it from another goroutine as soon as this returns.
func (s *WebSocket) attach(listener *socketListener) {
	s.mu.Lock()
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
	if s.attached != nil {
		s.attached()
	}
}

// detach removes listener (no-op when it is not attached).
func (s *WebSocket) detach(listener *socketListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, each := range s.listeners {
		if each == listener {
			s.listeners = append(append([]*socketListener(nil), s.listeners[:i]...), s.listeners[i+1:]...)
			return
		}
	}
}

// once is on for the first event only: Node's `once`, whose wrapper removes itself and then calls fn, at most once however
// emissions race. The listener is built whole (its removal needs only its own identity) before it is attached, so no emission
// can reach it before it can remove itself.
func (s *WebSocket) once(event link.WebSocketEvent, fn func(data any)) (remove func()) {
	var fired atomic.Bool
	listener := &socketListener{event: event}
	listener.fn = func(data any) {
		if fired.CompareAndSwap(false, true) {
			s.detach(listener)
			fn(data)
		}
	}
	s.attach(listener)
	return func() { s.detach(listener) }
}

// OnMessage attaches a listener for data frames and returns its removal.
func (s *WebSocket) OnMessage(fn func(Message)) (remove func()) {
	return s.on(link.WebSocketMessage, func(data any) { fn(data.(Message)) })
}

// OnClose attaches a listener for the close of the socket (once) and returns its removal.
func (s *WebSocket) OnClose(fn func(CloseEvent)) (remove func()) {
	return s.on(link.WebSocketClose, func(data any) { fn(data.(CloseEvent)) })
}

// OnceClose is OnClose for the first close only.
func (s *WebSocket) OnceClose(fn func(CloseEvent)) (remove func()) {
	return s.once(link.WebSocketClose, func(data any) { fn(data.(CloseEvent)) })
}

// OnError attaches a listener for errors of the socket (a frame above the largest payload) and returns its removal.
func (s *WebSocket) OnError(fn func(error)) (remove func()) {
	return s.on(link.WebSocketError, func(data any) { fn(data.(error)) })
}

// OnPing attaches a listener for ping frames (answered by the socket itself) and returns its removal.
func (s *WebSocket) OnPing(fn func(payload []byte)) (remove func()) {
	return s.on(webSocketPing, func(data any) { fn(data.([]byte)) })
}

// OncePing is OnPing for the first ping only.
func (s *WebSocket) OncePing(fn func(payload []byte)) (remove func()) {
	return s.once(webSocketPing, func(data any) { fn(data.([]byte)) })
}

// OnPong attaches a listener for pong frames and returns its removal.
func (s *WebSocket) OnPong(fn func(payload []byte)) (remove func()) {
	return s.on(webSocketPong, func(data any) { fn(data.([]byte)) })
}

// emit calls the listeners of event, in the order they were attached.
func (s *WebSocket) emit(event link.WebSocketEvent, data any) {
	s.mu.Lock()
	var each []*socketListener
	for _, listener := range s.listeners {
		if listener.event == event {
			each = append(each, listener)
		}
	}
	s.mu.Unlock()
	for _, listener := range each {
		listener.fn(data)
	}
}

// Send queues a frame (binary or text); done gets the write's error (nil when written). It reports an error when the
// socket is not open.
func (s *WebSocket) Send(data []byte, binary bool, done func(error)) {
	frame := websocket.TextMessage
	if binary {
		frame = websocket.BinaryMessage
	}
	if !s.push(queued{frame: frame, data: data, done: done, byteCount: int64(len(data))}, false) && done != nil {
		done(errors.New("WebSocket is not open"))
	}
}

// Close starts the close handshake with code and reason (no code: code 0), after what is queued; the connection is
// terminated if the peer does not answer within 30 s.
func (s *WebSocket) Close(code int, reason string) {
	s.push(queued{frame: websocket.CloseMessage, code: code, hasCode: code != 0, reason: reason}, true)
}

// Terminate closes the connection at once, no close handshake.
func (s *WebSocket) Terminate() {
	s.mu.Lock()
	if s.state == WebSocketClosedState {
		s.mu.Unlock()
		return
	}
	s.state = WebSocketClosedState
	s.mu.Unlock()
	_ = s.conn.UnderlyingConn().Close()
	// `ws`'s terminate() only destroys the connection: its `close` event is emitted on a later turn, never inside terminate()
	// (the caller may hold the host's loop, which a close listener takes: the reader's close ends the link and runs
	// hostLink.Closed on that loop), and a second terminate() never waits for it.
	//portlint:allow golifetime ends when the close was emitted, or found claimed by the socket's reader
	go s.closed(CloseEvent{Code: 1006})
}

// Ping sends a ping frame.
func (s *WebSocket) Ping() {
	s.mu.Lock()
	open := s.state == WebSocketOpenState
	s.mu.Unlock()
	if open {
		//portlint:allow clock a write deadline of the connection: wall-clock I/O, not behavior a test drives
		_ = s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
	}
}

// Start begins reading frames.
func (s *WebSocket) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	//portlint:allow golifetime ends when the connection ends: a read error closes the socket
	go s.read()
}

func (s *WebSocket) read() {
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			s.readFailed(err)
			return
		}
		s.emit(webSocketFrame, nil)
		s.deliver(Message{Data: data, Binary: kind == websocket.BinaryMessage})
	}
}

// deliver emits a message, or holds it while a handler runs.
func (s *WebSocket) deliver(message Message) {
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	if s.holding {
		s.held = append(s.held, message)
		return
	}
	s.emit(link.WebSocketMessage, message)
}

// hold keeps the messages that arrive from now on until release (before Start).
func (s *WebSocket) hold() {
	s.deliverMu.Lock()
	s.holding = true
	s.deliverMu.Unlock()
}

// release delivers the messages held, in order, then every later one as it arrives.
func (s *WebSocket) release() {
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	s.holding = false
	held := s.held
	s.held = nil
	for _, message := range held {
		s.emit(link.WebSocketMessage, message)
	}
}

// readFailed ends the socket when its reading did: a close frame, a connection gone, a frame above the largest payload.
func (s *WebSocket) readFailed(err error) {
	var closeErr *websocket.CloseError
	switch {
	case errors.As(err, &closeErr):
		s.finish(CloseEvent{Code: closeErr.Code, Reason: closeErr.Text})
	case errors.Is(err, websocket.ErrReadLimit):
		// `ws` emits an `error` (its frame was above `maxPayload`); the close frame (1009) is out already, so the socket is
		// CLOSING when its listeners run.
		s.mu.Lock()
		if s.state == WebSocketOpenState {
			s.state = WebSocketClosing
		}
		s.mu.Unlock()
		s.emit(link.WebSocketError, err)
		// `ws` never reads the peer's close frame after a receiver error, so its `close` event keeps the default 1006 (the
		// peer sees 1009).
		s.finish(CloseEvent{Code: 1006})
	default:
		s.finish(CloseEvent{Code: 1006})
	}
}

// finish ends the socket after its reading ended: closed, the connection released.
func (s *WebSocket) finish(event CloseEvent) {
	s.mu.Lock()
	already := s.state == WebSocketClosedState
	s.state = WebSocketClosedState
	s.mu.Unlock()
	if !already {
		_ = s.conn.Close()
	}
	s.closed(event)
}

// closed emits `close` once and drops what is pending. The first closer does the work; a later one returns at once, never
// waiting for the first's listeners (`ws` has no such wait, and a listener may need what the later caller holds).
func (s *WebSocket) closed(event CloseEvent) {
	if !s.closeClaimed.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	pending := s.queue
	s.queue = nil
	s.buffered = 0
	s.mu.Unlock()
	for _, job := range pending {
		if job.done != nil {
			job.done(errors.New("WebSocket is not open"))
		}
	}
	s.emit(link.WebSocketClose, event)
}

// push queues job when the socket is open, in one critical section with the state change a close frame makes: a send never
// queues behind the close frame. closing makes it the close frame (the first one only). It starts the writer when it is idle.
func (s *WebSocket) push(job queued, closing bool) bool {
	s.mu.Lock()
	if s.state != WebSocketOpenState {
		s.mu.Unlock()
		return false
	}
	if closing {
		s.state = WebSocketClosing
		s.timer = time.AfterFunc(closeTimeout, s.Terminate)
	}
	s.queue = append(s.queue, job)
	s.buffered += job.byteCount
	start := !s.writing
	s.writing = true
	s.mu.Unlock()
	if start {
		//portlint:allow golifetime ends when the queue is empty or the connection fails
		go s.write()
	}
	return true
}

// write writes the queue in order until it is empty.
func (s *WebSocket) write() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.writing = false
			s.mu.Unlock()
			return
		}
		job := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		var err error
		if job.frame == websocket.CloseMessage {
			payload := []byte{}
			if job.hasCode {
				payload = websocket.FormatCloseMessage(job.code, job.reason)
			}
			//portlint:allow clock a write deadline of the connection: wall-clock I/O, not behavior a test drives
			err = s.conn.WriteControl(websocket.CloseMessage, payload, time.Now().Add(5*time.Second))
		} else {
			err = s.conn.WriteMessage(job.frame, job.data)
		}
		s.mu.Lock()
		s.buffered -= job.byteCount
		s.mu.Unlock()
		if job.done != nil {
			job.done(err)
		}
	}
}
