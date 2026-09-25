package experimental

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// RadiusRelayWebSocket is a connected transport. Read has one owner; Send is serialized by the relay. Close must unblock both and may run concurrently with them.
type RadiusRelayWebSocket interface {
	Protocol() string
	Read() (binary bool, data []byte, err error)
	Send(binary bool, data []byte) error
	Close(code int, reason string) error
}

// RadiusRelayWebSocketOptions carries the exact URL, subprotocol, and bearer header for an attempt.
type RadiusRelayWebSocketOptions struct {
	URL           string
	Protocol      string
	Authorization string
}

// RadiusRelayWebSocketFactory completes the opening handshake or returns its error. The context owns cancellation of the opening attempt.
type RadiusRelayWebSocketFactory func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error)

type nativeRelaySocket struct{ conn *websocket.Conn }

func (s *nativeRelaySocket) Protocol() string { return s.conn.Subprotocol() }
func (s *nativeRelaySocket) Read() (bool, []byte, error) {
	kind, data, err := s.conn.ReadMessage()
	return kind == websocket.BinaryMessage, data, err
}
func (s *nativeRelaySocket) Send(binary bool, data []byte) error {
	kind := websocket.TextMessage
	if binary {
		kind = websocket.BinaryMessage
	}
	return s.conn.WriteMessage(kind, data)
}
func (s *nativeRelaySocket) Close(code int, reason string) error {
	// Close control is best-effort; the connection close always releases blocked I/O.
	// Gorilla's control deadline bounds only the graceful close, not relay data writes.
	err := s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	return errors.Join(err, s.conn.Close())
}

func defaultWebSocketFactory(ctx context.Context, options RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, Subprotocols: []string{options.Protocol}}
	conn, response, err := dialer.DialContext(ctx, options.URL, http.Header{"Authorization": {options.Authorization}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return &nativeRelaySocket{conn: conn}, nil
}

func openRadiusRelayWebSocket(ctx context.Context, auth *RadiusRelayAuth, serverID, protocol string, factory RadiusRelayWebSocketFactory) (RadiusRelayWebSocket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, err := relayWebSocketURL(auth.Gateway, serverID)
	if err != nil {
		return nil, err
	}
	if factory == nil {
		factory = defaultWebSocketFactory
	}
	socket, err := factory(ctx, RadiusRelayWebSocketOptions{URL: endpoint, Protocol: protocol, Authorization: "Bearer " + auth.Token})
	if err != nil {
		return nil, relayWebSocketError(err)
	}
	if socket.Protocol() != protocol {
		_ = socket.Close(1000, "Radius relay connection failed")
		return nil, errors.New("Radius relay selected unexpected WebSocket protocol " + strconv.Quote(socket.Protocol()))
	}
	if err := ctx.Err(); err != nil {
		_ = socket.Close(1000, "Radius relay connection failed")
		return nil, err
	}
	return socket, nil
}

// upstream: packages/protocol/src/framing.ts: DEFAULT_MAX_FRAME_LENGTH
const defaultMaxFrameLength = 16 * 1024 * 1024

// upstream: packages/coding-agent/src/experimental/radius-relay.ts: MAX_PENDING_BYTES
const maxPendingBytes = defaultMaxFrameLength * 4

// orderedWebSocketWriter copies and reserves submissions synchronously. One owned drain task serializes socket writes and completion callbacks; wait joins it after close.
type orderedWebSocketWriter struct {
	socket       RadiusRelayWebSocket
	mu           sync.Mutex
	pendingBytes int
	queue        []relaySocketWrite
	done         chan struct{}
	running      bool
	closed       bool
}

type relaySocketWrite struct {
	binary   bool
	data     []byte
	complete func(error)
}

func newOrderedWebSocketWriter(socket RadiusRelayWebSocket) *orderedWebSocketWriter {
	done := make(chan struct{})
	close(done)
	return &orderedWebSocketWriter{socket: socket, done: done}
}
func (w *orderedWebSocketWriter) send(binary bool, data []byte) error {
	result := make(chan error, 1)
	if err := w.enqueue(binary, data, func(err error) { result <- err }); err != nil {
		return err
	}
	return <-result
}
func (w *orderedWebSocketWriter) enqueue(binary bool, data []byte, complete func(error)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("Radius relay WebSocket is closed")
	}
	if len(data) > maxPendingBytes-w.pendingBytes {
		return errors.New("Radius relay exceeded its pending byte limit")
	}
	w.queue = append(w.queue, relaySocketWrite{binary: binary, data: bytes.Clone(data), complete: complete})
	w.pendingBytes += len(data)
	if !w.running {
		w.running = true
		w.done = make(chan struct{})
		go w.drain()
	}
	return nil
}
func (w *orderedWebSocketWriter) drain() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.queue = nil
			w.running = false
			close(w.done)
			w.mu.Unlock()
			return
		}
		write := w.queue[0]
		w.queue[0] = relaySocketWrite{}
		w.queue = w.queue[1:]
		closed := w.closed
		w.mu.Unlock()
		var err error
		if closed {
			err = errors.New("Radius relay WebSocket is closed")
		} else {
			err = w.socket.Send(write.binary, write.data)
		}
		w.mu.Lock()
		w.pendingBytes -= len(write.data)
		w.mu.Unlock()
		write.complete(err)
	}
}
func (w *orderedWebSocketWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}
func (w *orderedWebSocketWriter) wait() { w.mu.Lock(); done := w.done; w.mu.Unlock(); <-done }

// relayWebSocketError preserves detailed errors and supplies a stable fallback when the transport omits them.
func relayWebSocketError(err error) error {
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		return errors.New("Radius WebSocket connection failed")
	}
	return err
}

func relayDelay(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
