package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// RelayByteConnectionHandler receives ordered data and exactly one terminal callback. Callbacks run on the host's reader, not the TUI loop; they must not wait for host shutdown.
type RelayByteConnectionHandler struct {
	OnData  func([]byte)
	OnClose func()
	OnError func(error)
}

func (h RelayByteConnectionHandler) finish(err error) {
	if err != nil {
		if h.OnError != nil {
			h.OnError(err)
		}
		return
	}
	if h.OnClose != nil {
		h.OnClose()
	}
}

// RadiusRelayHostStatus reports authentication, connection, and retry transitions.
type RadiusRelayHostStatus struct {
	Status string
	Error  string
}

// RadiusRelayHostOptions binds the relay to a server's accept operation. No command or default endpoint is activated by construction.
type RadiusRelayHostOptions struct {
	ServerID         string
	Accept           func(*RelayServerByteConnection) RelayByteConnectionHandler
	Auth             *RadiusRelayAuthResolver
	WebSocketFactory RadiusRelayWebSocketFactory
	OnStatus         func(RadiusRelayHostStatus)
}

// RadiusRelayHost owns a reconnecting, multiplexed host connection. Incoming controls enqueue ordered replies without waiting for socket writes. Close cancels and joins every owned operation.
type RadiusRelayHost struct {
	options RadiusRelayHostOptions
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
	started bool
}

// NewRadiusRelayHost validates the collaborators without starting work.
func NewRadiusRelayHost(options RadiusRelayHostOptions) (*RadiusRelayHost, error) {
	if !connectionIDPattern.MatchString(options.ServerID) {
		return nil, errors.New("Invalid Radius relay server ID")
	}
	if options.Accept == nil || options.Auth == nil {
		return nil, errors.New("Radius relay host requires server accept and authentication")
	}
	return &RadiusRelayHost{options: options, done: make(chan struct{})}, nil
}

// Start begins the owned reconnect loop once. The context owns its lifetime.
func (h *RadiusRelayHost) Start(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started || h.closed {
		return
	}
	h.started = true
	ctx, h.cancel = context.WithCancel(ctx)
	go func() { defer close(h.done); h.run(ctx) }()
}

// Close is idempotent and joins shutdown, including a pending authentication or opening attempt.
func (h *RadiusRelayHost) Close() {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		if h.started {
			h.cancel()
		} else {
			close(h.done)
		}
	}
	h.mu.Unlock()
	<-h.done
}
func (h *RadiusRelayHost) status(status, message string) {
	if h.options.OnStatus != nil {
		h.options.OnStatus(RadiusRelayHostStatus{Status: status, Error: message})
	}
}

// upstream: packages/coding-agent/src/experimental/radius-relay.ts: HOST_RETRY_INITIAL_MS
const relayRetryInitial = time.Second

// upstream: packages/coding-agent/src/experimental/radius-relay.ts: HOST_RETRY_MAX_MS
const relayRetryMax = 30 * time.Second

func (h *RadiusRelayHost) run(ctx context.Context) {
	retry := relayRetryInitial
	for ctx.Err() == nil {
		auth, err := h.options.Auth.Resolve(ctx, false)
		if err == nil && auth == nil {
			h.status("not_authenticated", "")
			if relayDelay(ctx, relayRetryMax) != nil {
				return
			}
			continue
		}
		if err == nil {
			h.status("connecting", "")
			var socket RadiusRelayWebSocket
			socket, err = openRadiusRelayWebSocket(ctx, auth, h.options.ServerID, RadiusRelayHostSubprotocol, h.options.WebSocketFactory)
			if err == nil {
				retry = relayRetryInitial
				h.status("connected", "")
				err = h.serve(ctx, socket)
				if err == nil {
					err = errors.New("Radius relay host disconnected")
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		h.status("retrying", err.Error())
		if relayDelay(ctx, retry) != nil {
			return
		}
		retry = min(retry*2, relayRetryMax)
	}
}

type activeRelayConnection struct {
	connection *RelayServerByteConnection
	handler    RelayByteConnectionHandler
}
type relayHostSession struct {
	writer         *orderedWebSocketWriter
	accept         func(*RelayServerByteConnection) RelayByteConnectionHandler
	onControlError func(error)
	mu             sync.Mutex
	connections    map[string]activeRelayConnection
	order          []string
}

func (h *RadiusRelayHost) serve(ctx context.Context, socket RadiusRelayWebSocket) error {
	session := &relayHostSession{writer: newOrderedWebSocketWriter(socket), accept: h.options.Accept, connections: make(map[string]activeRelayConnection)}
	var stopOnce sync.Once
	var stopErr error
	stop := func(code int, reason string, cause error) {
		stopOnce.Do(func() {
			stopErr = cause
			session.writer.close()
			_ = socket.Close(code, reason)
		})
	}
	session.onControlError = func(err error) {
		if err != nil {
			stop(4001, "Radius relay send failed", err)
		}
	}
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			stop(1000, "Pi server stopped", nil)
		case <-watchStop:
		}
	}()
	defer func() { close(watchStop); stop(1000, "Pi server stopped", nil); <-watchDone; session.writer.wait() }()
	for {
		binary, data, err := socket.Read()
		if err != nil {
			// Joining stop publishes any asynchronous control-write error to the reader that owns terminal callbacks.
			stop(1000, "Pi server stopped", nil)
			if stopErr != nil {
				session.drop(stopErr)
				return stopErr
			}
			if ctx.Err() != nil || websocket.IsCloseError(err, 1000) {
				session.drop(nil)
				return nil
			}
			if closeErr, ok := errors.AsType[*websocket.CloseError](err); ok {
				reason := ""
				if closeErr.Text != "" {
					reason = ": " + closeErr.Text
				}
				err = fmt.Errorf("Radius relay host closed (%d%s)", closeErr.Code, reason)
			} else {
				err = relayWebSocketError(err)
			}
			session.drop(err)
			return err
		}
		if err := session.handle(binary, data); err != nil {
			if _, ok := errors.AsType[*relayControlSendError](err); ok {
				stop(4001, "Radius relay send failed", err)
			} else {
				stop(4000, "Radius relay protocol error", err)
			}
			session.drop(err)
			return err
		}
	}
}
func (s *relayHostSession) handle(binary bool, data []byte) error {
	if binary {
		frame, ok := ParseRelayDataFrame(data)
		if !ok {
			return errors.New("Invalid Radius relay data frame")
		}
		s.mu.Lock()
		active, exists := s.connections[frame.ConnectionID]
		s.mu.Unlock()
		if !exists {
			return s.queueClose(frame.ConnectionID, 1000)
		}
		if active.handler.OnData != nil {
			active.handler.OnData(frame.Payload)
		}
		return nil
	}
	message, err := parseHostControlMessage(data)
	if err != nil {
		return err
	}
	switch message.Type {
	case "ping":
		return s.queueControl(hostControlMessage{Version: 1, Type: "pong"})
	case "pong":
		return nil
	case "connection_open":
		return s.openConnection(message.ConnectionID)
	case "connection_close":
		if active, ok := s.remove(message.ConnectionID); ok {
			active.connection.closed.Store(true)
			active.handler.finish(nil)
		}
	}
	return nil
}
func (s *relayHostSession) openConnection(id string) error {
	s.mu.Lock()
	_, exists := s.connections[id]
	s.mu.Unlock()
	if exists {
		return errors.New("Radius relay reused a connection ID")
	}
	connection := &RelayServerByteConnection{session: s, id: id}
	handler := s.accept(connection)
	s.mu.Lock()
	if connection.Closed() {
		s.mu.Unlock()
		return s.queueClose(id, 1012)
	}
	s.connections[id] = activeRelayConnection{connection: connection, handler: handler}
	s.order = append(s.order, id)
	s.mu.Unlock()
	return nil
}
func (s *relayHostSession) remove(id string) (activeRelayConnection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, ok := s.connections[id]
	if ok {
		delete(s.connections, id)
		index := slices.Index(s.order, id)
		s.order = slices.Delete(s.order, index, index+1)
	}
	return active, ok
}
func (s *relayHostSession) drop(err error) {
	s.mu.Lock()
	active := make([]activeRelayConnection, 0, len(s.order))
	for _, id := range s.order {
		active = append(active, s.connections[id])
	}
	clear(s.connections)
	s.order = nil
	s.mu.Unlock()
	for _, item := range active {
		item.connection.closed.Store(true)
		item.handler.finish(err)
	}
}

type relayControlSendError struct{ error }

func (s *relayHostSession) queueControl(message hostControlMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if err := s.writer.enqueue(false, data, s.onControlError); err != nil {
		return &relayControlSendError{err}
	}
	return nil
}
func (s *relayHostSession) queueClose(id string, code int) error {
	return s.queueControl(hostControlMessage{Version: 1, Type: "connection_close", ConnectionID: id, Code: &code})
}

func (s *relayHostSession) sendControl(message hostControlMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if err := s.writer.send(false, data); err != nil {
		return &relayControlSendError{err}
	}
	return nil
}
func (s *relayHostSession) sendClose(id string, code int) error {
	return s.sendControl(hostControlMessage{Version: 1, Type: "connection_close", ConnectionID: id, Code: &code})
}

// RelayServerByteConnection is one server-side virtual byte stream. Send and Close await ordered writes; a final chunk precedes its close control.
type RelayServerByteConnection struct {
	session *relayHostSession
	id      string
	closed  atomic.Bool
}

// Closed reports local or remote terminal state.
func (c *RelayServerByteConnection) Closed() bool { return c.closed.Load() }

// Send sends one copied payload on this connection.
func (c *RelayServerByteConnection) Send(chunk []byte) error {
	c.session.mu.Lock()
	_, exists := c.session.connections[c.id]
	c.session.mu.Unlock()
	if c.Closed() || !exists {
		return errors.New("Radius relay connection is closed")
	}
	frame, err := EncodeRelayDataFrame(c.id, chunk)
	if err != nil {
		return err
	}
	return c.session.writer.send(true, frame)
}

// Close removes the connection once, then sends a non-nil final chunk before the close control. It does not synthesize a remote-close callback.
func (c *RelayServerByteConnection) Close(finalChunk []byte) error {
	if c.closed.Swap(true) {
		return nil
	}
	if _, ok := c.session.remove(c.id); !ok {
		return nil
	}
	if finalChunk != nil {
		frame, err := EncodeRelayDataFrame(c.id, finalChunk)
		if err != nil {
			return err
		}
		if err := c.session.writer.send(true, frame); err != nil {
			return err
		}
	}
	return c.session.sendClose(c.id, 1000)
}
