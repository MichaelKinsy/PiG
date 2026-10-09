package routing

// Ports packages/server/src/server.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

type activeServerRequest struct {
	cancel context.CancelCauseFunc
	target protocol.RpcTarget
}
type connectionState struct {
	connection           ByteConnection
	receiveMu            sync.Mutex
	mu                   sync.Mutex
	decoder              *protocol.ClientMessageDecoder
	encoders             map[string]*chord.ServiceStateEncoder
	stage                string
	disconnected         bool
	handshakeTimer       *time.Timer
	finishHandshakeTimer func()
	pending              []protocol.ClientMessage
	services             RoutedServerServiceAttachment
	requests             map[string]*activeServerRequest
	lastEntered          <-chan struct{}
}

// requestTurn orders the moment a connection's requests reach the host. Upstream's handleRequest runs synchronously up to the
// service call, so requests from one connection enter the host in arrival order (packages/server/src/server.ts:317-345).
type requestTurn struct {
	previous <-chan struct{}
	entered  chan struct{}
	once     sync.Once
}

// enter releases the next request once the previous one has entered; it is safe to call more than once. A request that ends
// before reaching the host still releases its successor only after its predecessor, so the chain never lets a later request
// overtake an earlier one.
func (turn *requestTurn) enter() {
	turn.once.Do(func() {
		turn.wait()
		close(turn.entered)
	})
}

// wait blocks until the previous request on the connection has entered the host.
func (turn *requestTurn) wait() {
	if turn.previous != nil {
		<-turn.previous
	}
}

func (c *connectionState) stopHandshakeTimer() {
	if c.handshakeTimer != nil && c.handshakeTimer.Stop() {
		c.finishHandshakeTimer()
	}
}
func (c *connectionState) terminal() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnected || c.stage == "closing" || c.stage == "closed"
}

// Server routes the exact framed protocol over caller-selected listeners. Request, handshake, and cleanup tasks are owned and joined by Close; callbacks must not wait for shutdown on their own delivery stack.
type Server struct {
	ServerId                   string
	host                       ServerHost
	options                    ServerOptions
	frameOptions               protocol.FrameDecoderOptions
	handshakeTimeout           time.Duration
	mu                         sync.Mutex
	connections                []*connectionState
	sessions                   *SessionRouter[*connectionState]
	closing, started, starting bool
	startDone                  chan struct{}
	closeDone                  chan struct{}
	closeError                 error
	closed                     chan struct{}
	closedError                error
	closedSettled              bool
	work                       sync.WaitGroup
	countQueue                 []int
	deliveringCount            bool
}

func NewServer(host ServerHost, options ServerOptions) (*Server, error) {
	if options.Listeners == nil {
		return nil, errors.New("Server listeners must be an array")
	}
	if !protocol.IsServerId(options.ServerId) {
		return nil, errors.New("serverId must be a canonical lowercase UUIDv4")
	}
	frame := float64(protocol.DefaultMaxFrameLength)
	if options.MaxFrameLength != nil {
		frame = *options.MaxFrameLength
	}
	if !serverInteger(frame, 1, math.MaxUint32) {
		return nil, errors.New("Server maxFrameLength must be an integer between 1 and 4294967295")
	}
	timeout := float64(5_000)
	if options.HandshakeTimeoutMs != nil {
		timeout = *options.HandshakeTimeoutMs
	}
	if !serverInteger(timeout, 1, math.MaxInt32) {
		return nil, errors.New("Server handshakeTimeoutMs must be an integer between 1 and 2147483647")
	}
	s := &Server{ServerId: options.ServerId, host: host, options: options, frameOptions: protocol.FrameDecoderOptions{MaxFrameLength: &frame}, handshakeTimeout: time.Duration(timeout) * time.Millisecond, closed: make(chan struct{})}
	s.sessions = NewSessionRouter(SessionRouterOptions[*connectionState]{Host: host, ServerId: s.ServerId, IsClosing: s.isClosing, ReportError: s.reportError, PublishAttachment: func(_ context.Context, client *connectionState, attachment *protocol.SessionTarget) error {
		s.sendMessage(client, protocol.AttachmentEnvelope{Attachment: attachment})
		return nil
	}})
	return s, nil
}
func serverInteger(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value >= minimum && value <= maximum
}
func (s *Server) isClosing() bool         { s.mu.Lock(); defer s.mu.Unlock(); return s.closing }
func (s *Server) Closed() <-chan struct{} { return s.closed }
func (s *Server) ClosedError() error      { s.mu.Lock(); defer s.mu.Unlock(); return s.closedError }

// Start starts every listener and returns the server, as upstream start(): Promise<this> (server.ts:103-111). It fails when the server is already started, starting, closing or closed, and a listener that fails to start closes the ones already started and the server.
func (s *Server) Start() (*Server, error) {
	s.mu.Lock()
	switch {
	case s.started:
		s.mu.Unlock()
		return nil, errors.New("Server is already started")
	case s.starting:
		s.mu.Unlock()
		return nil, errors.New("Server is already starting")
	case s.closing:
		s.mu.Unlock()
		return nil, errors.New("Server is closing or closed")
	}
	s.starting = true
	s.startDone = make(chan struct{})
	done := s.startDone
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.starting = false; close(done); s.mu.Unlock() }()
	var started []ServerListener
	for _, listener := range s.options.Listeners {
		if err := listener.Start(s.Accept); err != nil {
			s.mu.Lock()
			s.closing = true
			s.mu.Unlock()
			cleanup := closeListeners(started)
			cleanup = append(cleanup, s.closeServerState())
			failure := routingErrors("Server startup and cleanup failed", append([]error{err}, cleanup...), false)
			hasCleanupError := slices.ContainsFunc(cleanup, func(err error) bool { return err != nil })
			if hasCleanupError {
				s.settleClosed(failure)
				return nil, failure
			}
			s.settleClosed(nil)
			return nil, err
		}
		started = append(started, listener)
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return s, nil
}
func closeListeners(listeners []ServerListener) []error {
	failures := make([]error, len(listeners))
	var group sync.WaitGroup
	for i, listener := range listeners {
		group.Go(func() { failures[i] = listener.Close() })
	}
	group.Wait()
	return failures
}

func (s *Server) Accept(connection ByteConnection) ByteConnectionHandler {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		// A post-shutdown supplied connection remains caller-owned until this close completes; it cannot enter server state.
		s.closeConnection(connection, nil)
		return ByteConnectionHandler{OnData: func([]byte) {}, OnClose: func() {}, OnError: s.reportError}
	}
	decoder, err := protocol.NewClientMessageDecoder(s.frameOptions)
	if err != nil {
		s.mu.Unlock()
		s.reportError(err)
		s.closeConnection(connection, nil)
		return ByteConnectionHandler{OnData: func([]byte) {}, OnClose: func() {}, OnError: s.reportError}
	}
	state := &connectionState{connection: connection, decoder: decoder, encoders: make(map[string]*chord.ServiceStateEncoder), stage: "awaitingHello", requests: make(map[string]*activeServerRequest)}
	var timerFinished sync.Once
	state.finishHandshakeTimer = func() { timerFinished.Do(s.work.Done) }
	s.work.Add(1)
	state.mu.Lock()
	state.handshakeTimer = time.AfterFunc(s.handshakeTimeout, func() {
		defer state.finishHandshakeTimer()
		s.failProtocol(state, protocol.ProtocolError{Code: "invalid_request", Message: "Handshake timeout"})
	})
	state.mu.Unlock()
	s.connections = append(s.connections, state)
	deliver := s.queueConnectionCountLocked()
	s.mu.Unlock()
	if deliver {
		s.deliverConnectionCounts()
	}
	return ByteConnectionHandler{OnData: func(chunk []byte) { s.receive(state, chunk) }, OnClose: func() { s.transportClosed(state) }, OnError: func(err error) {
		s.reportError(err)
		state.mu.Lock()
		if !state.disconnected && state.stage != "closing" && state.stage != "closed" {
			s.work.Go(func() { s.closeConnection(connection, nil); s.disconnect(state) })
		}
		state.mu.Unlock()
	}}
}
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closeDone == nil {
		s.closing = true
		s.closeDone = make(chan struct{})
		go func() { err := s.closeInternal(); s.mu.Lock(); s.closeError = err; close(s.closeDone); s.mu.Unlock() }()
	}
	done := s.closeDone
	s.mu.Unlock()
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeError
}
func (s *Server) closeInternal() error {
	s.mu.Lock()
	starting := s.starting
	startDone := s.startDone
	s.mu.Unlock()
	if starting {
		<-startDone
	}
	failures := closeListeners(s.options.Listeners)
	failures = append(failures, s.closeServerState())
	s.work.Wait()
	failure := routingErrors("Server shutdown failed", failures, true)
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
	s.settleClosed(failure)
	return failure
}
func (s *Server) receive(state *connectionState, chunk []byte) {
	state.receiveMu.Lock()
	defer state.receiveMu.Unlock()
	if state.terminal() {
		return
	}
	messages, err := state.decoder.Push(chunk)
	if err != nil {
		failure := s.toProtocolError(err)
		state.mu.Lock()
		s.queueProtocolFailureLocked(state, failure)
		state.mu.Unlock()
		return
	}
	for _, message := range messages {
		if state.terminal() {
			return
		}
		s.dispatchMessage(state, message)
	}
}
func (s *Server) dispatchMessage(state *connectionState, message protocol.ClientMessage) {
	state.mu.Lock()
	if state.disconnected || state.stage == "closing" || state.stage == "closed" {
		state.mu.Unlock()
		return
	}
	if state.stage == "awaitingHello" {
		hello, ok := message.(protocol.ClientHello)
		if !ok {
			s.queueProtocolFailureLocked(state, protocol.ProtocolError{Code: "invalid_request", Message: "The first client message must be hello"})
			state.mu.Unlock()
			return
		}
		state.stage = "handshaking"
		s.work.Go(func() {
			if err := s.finishHandshake(state, hello); err != nil {
				s.failProtocol(state, s.toProtocolError(err))
			}
		})
		state.mu.Unlock()
		return
	}
	if _, hello := message.(protocol.ClientHello); hello {
		s.queueProtocolFailureLocked(state, protocol.ProtocolError{Code: "invalid_request", Message: "hello may only be sent as the first message"})
		state.mu.Unlock()
		return
	}
	if state.stage == "handshaking" {
		state.pending = append(state.pending, message)
		state.mu.Unlock()
		return
	}
	ready := state.stage == "ready"
	state.mu.Unlock()
	if ready {
		s.dispatchReady(state, message)
	}
}
func (s *Server) dispatchReady(state *connectionState, message protocol.ClientMessage) {
	switch message := message.(type) {
	case protocol.CancelEnvelope:
		s.handleCancel(state, message)
	case protocol.RequestEnvelope:
		s.admitRequest(state, message)
	}
}
func (s *Server) finishHandshake(state *connectionState, hello protocol.ClientHello) error {
	if !protocol.IsSupportedProtocolVersion(hello.Version) {
		s.failProtocol(state, protocol.ProtocolError{Code: "version", Message: fmt.Sprintf("Unsupported protocol version %v; expected %d", hello.Version, protocol.ProtocolVersion)})
		return nil
	}
	if s.isClosing() || state.terminal() || state.connection.Closed() {
		return nil
	}
	if s.host.ServerServices == nil {
		return errors.New("server services host is missing")
	}
	services, err := s.host.ServerServices.AttachClient(context.Background(), serverPresentation{s, state})
	if err != nil {
		return err
	}
	state.mu.Lock()
	if state.disconnected || state.stage != "handshaking" || state.connection.Closed() {
		state.mu.Unlock()
		return services.Release(context.Background())
	}
	state.mu.Unlock()
	if s.isClosing() {
		return services.Release(context.Background())
	}
	state.mu.Lock()
	state.services = services
	state.mu.Unlock()
	sent := s.sendMessage(state, protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: s.ServerId})
	state.mu.Lock()
	if sent && !state.disconnected && state.stage == "handshaking" {
		// Keep arrivals queued while draining the handshake's pending messages, so a newer chunk cannot overtake them.
		state.stopHandshakeTimer()
		for len(state.pending) != 0 {
			pending := state.pending
			state.pending = nil
			state.mu.Unlock()
			for _, message := range pending {
				if state.terminal() {
					break
				}
				s.dispatchReady(state, message)
			}
			state.mu.Lock()
		}
		if !state.disconnected && state.stage == "handshaking" {
			state.stage = "ready"
		}
	}
	state.mu.Unlock()
	return nil
}

type serverPresentation struct {
	server *Server
	state  *connectionState
}

func (p serverPresentation) AttachSession(ctx context.Context, id string) error {
	return p.server.sessions.AttachClient(ctx, p.state, id)
}
func (p serverPresentation) DetachSession(ctx context.Context) error {
	return p.server.sessions.DetachClient(ctx, p.state)
}
func (p serverPresentation) PrepareSessionRemoval(ctx context.Context, id string) error {
	return p.server.sessions.RemoveSession(ctx, id)
}

func (s *Server) handleCancel(state *connectionState, envelope protocol.CancelEnvelope) {
	if targetServerID(envelope.Target) != s.ServerId {
		return
	}
	state.mu.Lock()
	active := state.requests[envelope.Id]
	state.mu.Unlock()
	if active != nil && sameTarget(active.target, envelope.Target) {
		active.cancel(errors.New("RPC request cancelled"))
	}
}
func (s *Server) admitRequest(state *connectionState, envelope protocol.RequestEnvelope) {
	state.mu.Lock()
	if state.disconnected || state.stage == "closing" || state.stage == "closed" {
		state.mu.Unlock()
		return
	}
	if state.requests[envelope.Id] != nil {
		s.work.Go(func() {
			s.sendMessage(state, protocol.ResponseEnvelope{Id: envelope.Id, Error: &protocol.ProtocolError{Code: "invalid_request", Message: "Request ID is already active"}})
		})
		state.mu.Unlock()
		return
	}
	raw, err := protocol.ToJSON(envelope.Call)
	var call chord.ServiceCall
	if err == nil {
		call, err = chord.ParseServiceCall(raw)
	}
	if err != nil {
		s.work.Go(func() {
			s.sendMessage(state, protocol.ResponseEnvelope{Id: envelope.Id, Error: &protocol.ProtocolError{Code: "invalid_request", Message: "Invalid service call"}})
		})
		state.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	active := &activeServerRequest{cancel: cancel, target: envelope.Target}
	state.requests[envelope.Id] = active
	turn := &requestTurn{previous: state.lastEntered, entered: make(chan struct{})}
	state.lastEntered = turn.entered
	s.work.Go(func() {
		defer turn.enter()
		defer cancel(nil)
		defer func() {
			state.mu.Lock()
			if state.requests[envelope.Id] == active {
				delete(state.requests, envelope.Id)
			}
			state.mu.Unlock()
		}()
		s.handleRequest(ctx, state, envelope, call, turn)
	})
	state.mu.Unlock()
}
func (s *Server) handleRequest(ctx context.Context, state *connectionState, envelope protocol.RequestEnvelope, call chord.ServiceCall, turn *requestTurn) {
	control, isControl := chord.DecodeServiceControlCall(call)
	subscribing := isControl && control.Type == "subscribe"
	var pendingMu sync.Mutex
	var pending []chord.ServiceProviderUpdate
	ready := !subscribing
	publish := func(_ context.Context, id string, update chord.ServiceProviderUpdate) error {
		pendingMu.Lock()
		if subscribing && id == control.SubscriptionId && !ready {
			pending = append(pending, update)
			pendingMu.Unlock()
			return nil
		}
		pendingMu.Unlock()
		return s.sendServiceUpdate(state, id, update)
	}
	installed, responded := false, false
	operation := func() error {
		if targetServerID(envelope.Target) != s.ServerId {
			return NewWrongServerError()
		}
		state.mu.Lock()
		duplicate := subscribing && state.encoders[control.SubscriptionId] != nil
		services := state.services
		state.mu.Unlock()
		if duplicate {
			return &protocol.ProtocolValidationError{Message: "Duplicate service subscription " + control.SubscriptionId}
		}
		var result json.RawMessage
		var err error
		if _, session := envelope.Target.(protocol.SessionTarget); session {
			turn.wait()
			queued := s.sessions.QueueServiceCall(ctx, call, envelope.Target, state, publish)
			turn.enter()
			result, err = queued.Wait()
		} else if services != nil {
			turn.wait()
			var invocation *chord.ServiceInvocation
			invocation, err = beginServiceCall(services, ctx, call, publish, s.work.Go)
			turn.enter()
			if err == nil {
				result, err = invocation.Wait(context.Background())
			}
		} else {
			err = &protocol.ProtocolValidationError{Message: "Unknown service member " + call.ServiceId + "." + call.Member}
		}
		if err != nil {
			return err
		}
		if subscribing {
			if result == nil {
				return &protocol.ProtocolValidationError{Message: "Service subscription did not return a snapshot"}
			}
			snapshot, err := chord.ParseServiceSubscriptionSnapshot(result)
			if err != nil {
				return err
			}
			encoder := chord.CreateServiceStateEncoder()
			wire, err := encoder.EncodeSnapshot(snapshot)
			if err != nil {
				return err
			}
			result, err = json.Marshal(wire)
			if err != nil {
				return err
			}
			state.mu.Lock()
			state.encoders[control.SubscriptionId] = encoder
			state.mu.Unlock()
			installed = true
		} else if isControl && control.Type == "unsubscribe" {
			state.mu.Lock()
			delete(state.encoders, control.SubscriptionId)
			state.mu.Unlock()
		}
		response := protocol.ResponseEnvelope{Id: envelope.Id, Ok: true, HasResult: result != nil}
		if response.HasResult {
			response.Result, err = protocol.FromJSON(result)
			if err != nil {
				return err
			}
		}
		s.sendMessage(state, response)
		responded = true
		if subscribing {
			for {
				pendingMu.Lock()
				if len(pending) == 0 {
					ready = true
					pendingMu.Unlock()
					break
				}
				update := pending[0]
				pending = pending[1:]
				pendingMu.Unlock()
				if err := s.sendServiceUpdate(state, control.SubscriptionId, update); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := operation(); err != nil {
		if subscribing && installed && !responded {
			state.mu.Lock()
			delete(state.encoders, control.SubscriptionId)
			state.mu.Unlock()
		}
		if responded {
			s.reportError(err)
			s.closeConnection(state.connection, nil)
			s.disconnect(state)
			return
		}
		failure := protocol.ProtocolError{Code: "cancelled", Message: "RPC request cancelled"}
		if ctx.Err() == nil {
			failure = s.toProtocolError(err)
		}
		s.sendMessage(state, protocol.ResponseEnvelope{Id: envelope.Id, Error: &failure})
	}
}
func (s *Server) transportClosed(state *connectionState) {
	state.receiveMu.Lock()
	state.mu.Lock()
	checkEnd := !state.disconnected && state.stage != "closing"
	state.mu.Unlock()
	if checkEnd {
		if err := state.decoder.End(); err != nil {
			s.reportError(err)
		}
	}
	state.receiveMu.Unlock()
	s.disconnect(state)
}
func (s *Server) disconnect(state *connectionState) {
	state.mu.Lock()
	if state.disconnected {
		state.mu.Unlock()
		return
	}
	state.disconnected = true
	state.stage = "closed"
	state.stopHandshakeTimer()
	for _, active := range state.requests {
		active.cancel(errors.New("Client disconnected"))
	}
	clear(state.requests)
	clear(state.encoders)
	state.pending = nil
	services := state.services
	state.services = nil
	cleanupReady := make(chan struct{})
	s.work.Go(func() {
		<-cleanupReady
		var errors [2]error
		var group sync.WaitGroup
		group.Go(func() { errors[0] = s.sessions.Disconnect(context.Background(), state) })
		if services != nil {
			group.Go(func() { errors[1] = services.Release(context.Background()) })
		}
		group.Wait()
		for _, err := range errors {
			if err != nil {
				s.reportError(err)
			}
		}
	})
	state.mu.Unlock()
	s.mu.Lock()
	before := len(s.connections)
	s.connections = slices.DeleteFunc(s.connections, func(c *connectionState) bool { return c == state })
	deliver := false
	if len(s.connections) != before {
		deliver = s.queueConnectionCountLocked()
	}
	s.mu.Unlock()
	if deliver {
		s.deliverConnectionCounts()
	}
	close(cleanupReady)
}
func (s *Server) sendServiceUpdate(state *connectionState, id string, update chord.ServiceProviderUpdate) error {
	state.mu.Lock()
	encoder := state.encoders[id]
	state.mu.Unlock()
	if encoder == nil {
		return nil
	}
	wire, err := encoder.EncodeUpdate(update)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	value, err := protocol.FromJSON(raw)
	if err != nil {
		return err
	}
	s.sendMessage(state, protocol.ServiceEventEnvelope{SubscriptionId: id, Update: value})
	return nil
}
func (s *Server) sendMessage(state *connectionState, message protocol.ServerMessage) bool {
	state.mu.Lock()
	disconnected := state.disconnected
	state.mu.Unlock()
	if disconnected || state.connection.Closed() {
		return false
	}
	frame, err := protocol.EncodeServerMessage(message, s.frameOptions)
	if err == nil {
		err = state.connection.Send(frame)
	}
	if err != nil {
		s.reportError(err)
		s.closeConnection(state.connection, nil)
		s.disconnect(state)
		return false
	}
	return true
}
func (s *Server) beginProtocolFailureLocked(state *connectionState) bool {
	if state.disconnected || state.stage == "closing" || state.stage == "closed" {
		return false
	}
	state.stage = "closing"
	state.stopHandshakeTimer()
	return true
}
func (s *Server) queueProtocolFailureLocked(state *connectionState, failure protocol.ProtocolError) {
	if s.beginProtocolFailureLocked(state) {
		s.work.Go(func() { s.closeProtocolFailure(state, failure) })
	}
}
func (s *Server) failProtocol(state *connectionState, failure protocol.ProtocolError) {
	state.mu.Lock()
	started := s.beginProtocolFailureLocked(state)
	state.mu.Unlock()
	if started {
		s.closeProtocolFailure(state, failure)
	}
}
func (s *Server) closeProtocolFailure(state *connectionState, failure protocol.ProtocolError) {
	frame, err := protocol.EncodeServerMessage(protocol.ServerHelloError{Error: failure}, s.frameOptions)
	if err != nil {
		s.reportError(err)
	}
	s.closeConnection(state.connection, frame)
	s.disconnect(state)
}
func (s *Server) closeServerState() error {
	s.mu.Lock()
	connections := slices.Clone(s.connections)
	s.mu.Unlock()
	for _, state := range connections {
		state.mu.Lock()
		state.stage = "closing"
		state.stopHandshakeTimer()
		state.mu.Unlock()
	}
	var group sync.WaitGroup
	for _, state := range connections {
		group.Go(func() { s.closeConnection(state.connection, nil) })
	}
	group.Wait()
	for _, state := range connections {
		s.disconnect(state)
	}
	err := s.sessions.Close(context.Background())
	s.mu.Lock()
	s.connections = nil
	s.mu.Unlock()
	return err
}
func (s *Server) closeConnection(connection ByteConnection, final []byte) {
	if err := connection.Close(final); err != nil {
		s.reportError(err)
	}
}
func (s *Server) toProtocolError(err error) protocol.ProtocolError {
	if failure, ok := errors.AsType[*ServerError](err); ok {
		return protocol.ProtocolError{Code: string(failure.Code), Message: failure.Message}
	}
	if failure, ok := errors.AsType[*chord.RemoteServiceError](err); ok {
		return protocol.ProtocolError{Code: string(failure.Code), Message: failure.Message}
	}
	if failure, ok := errors.AsType[*protocol.ProtocolValidationError](err); ok {
		return protocol.ProtocolError{Code: "invalid_request", Message: failure.Message}
	}
	s.reportError(err)
	return protocol.ProtocolError{Code: "internal_error", Message: InternalServerErrorMessage}
}
func (s *Server) queueConnectionCountLocked() bool {
	s.countQueue = append(s.countQueue, len(s.connections))
	if s.deliveringCount {
		return false
	}
	s.deliveringCount = true
	return true
}
func (s *Server) deliverConnectionCounts() {
	for {
		s.mu.Lock()
		if len(s.countQueue) == 0 {
			s.deliveringCount = false
			s.mu.Unlock()
			return
		}
		count := s.countQueue[0]
		s.countQueue = s.countQueue[1:]
		s.mu.Unlock()
		if s.options.OnConnectionCountChanged != nil {
			s.callConnectionCount(count)
		}
	}
}
func (s *Server) callConnectionCount(count int) {
	defer func() {
		if value := recover(); value != nil {
			s.reportError(fmt.Errorf("%v", value))
		}
	}()
	s.options.OnConnectionCountChanged(count)
}
func (s *Server) reportError(err error) {
	// upstream: packages/server/src/server.ts:reportError
	defer func() { _ = recover() }()
	if s.options.OnError != nil {
		s.options.OnError(err)
	}
}
func (s *Server) settleClosed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closedSettled {
		s.closedSettled = true
		s.closedError = err
		close(s.closed)
	}
}
func targetServerID(target protocol.RpcTarget) string {
	switch value := target.(type) {
	case protocol.ServerTarget:
		return value.ServerId
	case protocol.SessionTarget:
		return value.ServerId
	default:
		return ""
	}
}
func sameTarget(left, right protocol.RpcTarget) bool {
	switch a := left.(type) {
	case protocol.ServerTarget:
		b, ok := right.(protocol.ServerTarget)
		return ok && a == b
	case protocol.SessionTarget:
		b, ok := right.(protocol.SessionTarget)
		return ok && a == b
	default:
		return false
	}
}
