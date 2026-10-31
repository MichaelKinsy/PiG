package http

import (
	"errors"
	"net"
	"sync"
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
// Reading starts with Start, which the component calls once a handler has attached its listeners (Pi attaches them in the
// handler's synchronous part, before any frame is read): a frame sent before waits in the connection.

// WebSocketHandle is Pi's `WebSocketHandle`: the socket a plugin's handler gets.
type WebSocketHandle = WebSocket

// WebSocket ready states, as `ws` numbers them.
const (
	WebSocketConnecting  = 0
	WebSocketOpenState   = 1
	WebSocketClosing     = 2
	WebSocketClosedState = 3
)

// Events beyond link's (`open`, `close`, `error`, `message`).
const (
	WebSocketPing link.WebSocketEvent = "ping"
	WebSocketPong link.WebSocketEvent = "pong"
)

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

	mu        sync.Mutex
	state     int
	listeners []*socketListener
	queue     []queued
	writing   bool
	buffered  int64
	started   bool
	closeOnce sync.Once
	timer     *time.Timer
}

// newWebSocket is the socket over conn (an upgraded server connection).
func newWebSocket(conn *websocket.Conn) *WebSocket {
	socket := &WebSocket{conn: conn, state: WebSocketOpenState}
	conn.SetPingHandler(func(payload string) error {
		// The pong first, then the event, as `ws` does.
		err := conn.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
		socket.emit(WebSocketPing, []byte(payload))
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
		socket.emit(WebSocketPong, []byte(payload))
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

// On attaches a listener for event and returns its removal. A message event's data is a string for a text frame and
// []byte for a binary one; `close`: a CloseEvent; `error`: an error; `ping` and `pong`: the payload.
func (s *WebSocket) On(event link.WebSocketEvent, fn func(data any)) (remove func()) {
	listener := &socketListener{event: event, fn: fn}
	s.mu.Lock()
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, each := range s.listeners {
			if each == listener {
				s.listeners = append(append([]*socketListener(nil), s.listeners[:i]...), s.listeners[i+1:]...)
				return
			}
		}
	}
}

// Once is On for the first event only.
func (s *WebSocket) Once(event link.WebSocketEvent, fn func(data any)) (remove func()) {
	var once sync.Once
	var removed func()
	removed = s.On(event, func(data any) {
		once.Do(func() {
			removed()
			fn(data)
		})
	})
	return removed
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
	if !s.enqueue(queued{frame: frame, data: data, done: done, byteCount: int64(len(data))}) && done != nil {
		done(errors.New("WebSocket is not open"))
	}
}

// Close starts the close handshake with code and reason (no code: code 0), after what is queued; the connection is
// terminated if the peer does not answer within 30 s.
func (s *WebSocket) Close(code int, reason string) {
	s.mu.Lock()
	if s.state != WebSocketOpenState {
		s.mu.Unlock()
		return
	}
	s.state = WebSocketClosing
	s.timer = time.AfterFunc(closeTimeout, s.Terminate)
	s.mu.Unlock()
	s.enqueueLocked(queued{frame: websocket.CloseMessage, code: code, hasCode: code != 0, reason: reason})
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
	s.conn.UnderlyingConn().Close()
	s.closed(CloseEvent{Code: 1006})
}

// Ping sends a ping frame.
func (s *WebSocket) Ping() {
	s.mu.Lock()
	open := s.state == WebSocketOpenState
	s.mu.Unlock()
	if open {
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
	go s.read()
}

func (s *WebSocket) read() {
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			s.readFailed(err)
			return
		}
		if kind == websocket.BinaryMessage {
			s.emit(link.WebSocketMessage, data)
		} else {
			s.emit(link.WebSocketMessage, string(data))
		}
	}
}

// readFailed ends the socket when its reading did: a close frame, a connection gone, a frame above the largest payload.
func (s *WebSocket) readFailed(err error) {
	var closeErr *websocket.CloseError
	switch {
	case errors.As(err, &closeErr):
		s.finish(CloseEvent{Code: closeErr.Code, Reason: closeErr.Text})
	case errors.Is(err, websocket.ErrReadLimit):
		// `ws` emits an `error` (its frame was above `maxPayload`); the close frame (1009) is out already.
		s.emit(link.WebSocketError, err)
		s.finish(CloseEvent{Code: 1009})
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
		s.conn.Close()
	}
	s.closed(event)
}

// closed emits `close` once and drops what is pending.
func (s *WebSocket) closed(event CloseEvent) {
	s.closeOnce.Do(func() {
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
	})
}

// enqueue queues a frame when the socket is open.
func (s *WebSocket) enqueue(job queued) bool {
	s.mu.Lock()
	if s.state != WebSocketOpenState {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	return s.enqueueLocked(job)
}

// enqueueLocked queues job and starts the writer when it is idle.
func (s *WebSocket) enqueueLocked(job queued) bool {
	s.mu.Lock()
	if s.state == WebSocketClosedState {
		s.mu.Unlock()
		return false
	}
	s.queue = append(s.queue, job)
	s.buffered += job.byteCount
	start := !s.writing
	s.writing = true
	s.mu.Unlock()
	if start {
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
