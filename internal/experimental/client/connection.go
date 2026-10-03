package client

// Ports packages/client/src/connection.ts.
// Ports packages/client/src/types.ts.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// ConnectionState is the transport/handshake lifecycle state.
type ConnectionState string

const (
	Disconnected ConnectionState = "disconnected"
	Connecting   ConnectionState = "connecting"
	Connected    ConnectionState = "connected"
)

// Unsubscribe removes the listener a registration added.
type Unsubscribe = func()

// ListenerErrorHandler receives listener failures without letting them change client state.
type ListenerErrorHandler = func(error)

// ConnectionStateChange is delivered after committing the connection's state.
type ConnectionStateChange struct {
	State ConnectionState
	Error error
}

// ConnectionOptions supplies the transport and client-owned callbacks. Callbacks run on the transport's ordered dispatcher and may synchronously disconnect or start a new connection.
type ConnectionOptions struct {
	TransportFactory ByteTransportFactory
	ServerId         string
	MaxFrameLength   *float64
	OnHandshake      func(protocol.ServerHello) error
	OnMessage        func(protocol.ServerMessage)
	OnStateChange    func(ConnectionStateChange)
}

type connectionHandshake struct {
	done    chan struct{}
	once    sync.Once
	settled atomic.Bool
	hello   protocol.ServerHello
	err     error
}

func (handshake *connectionHandshake) reserve(hello protocol.ServerHello, err error) (publish func()) {
	handshake.once.Do(func() {
		handshake.hello, handshake.err = hello, err
		handshake.settled.Store(true)
		publish = func() { close(handshake.done) }
	})
	return publish
}

type connectionTransport struct{ transport ByteTransport }
type connectionLifecycle struct {
	state     ConnectionState
	id        uint64
	decoder   *protocol.ServerMessageDecoder
	transport *connectionTransport
	handshake *connectionHandshake
	cancel    context.CancelFunc
}

// Connection validates handshakes and routes all subsequent messages. Each attempt has its own decoder and identity; stale transport callbacks cannot replace a newer connection.
type Connection struct {
	options        ConnectionOptions
	maxFrameLength float64
	mu             sync.Mutex
	decoderMu      sync.Mutex
	lifecycle      *connectionLifecycle
	sequence       uint64
	deliveryDepth  int
	flushing       bool
	continuations  []func()
	lifetimes      []<-chan struct{}
}

// NewConnection validates the frame limit without opening a transport.
func NewConnection(options ConnectionOptions) (*Connection, error) {
	if options.TransportFactory == nil || options.OnHandshake == nil || options.OnMessage == nil || options.OnStateChange == nil {
		return nil, errors.New("Connection requires a transport factory and callbacks")
	}
	limit := float64(protocol.DefaultMaxFrameLength)
	if options.MaxFrameLength != nil {
		limit = *options.MaxFrameLength
	}
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit <= 0 || limit > math.MaxUint32 || math.Trunc(limit) != limit {
		return nil, fmt.Errorf("Client maxFrameLength must be an integer between 1 and 4294967295")
	}
	return &Connection{options: options, maxFrameLength: limit, lifecycle: &connectionLifecycle{state: Disconnected}}, nil
}
func (connection *Connection) State() ConnectionState {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.lifecycle.state
}
func (connection *Connection) MaxFrameLength() float64 { return connection.maxFrameLength }

// Connect starts the transport factory's synchronous prefix before waiting for a validated hello. Cancelling ctx cancels only this attempt; a completed connection stays live until Disconnect or a terminal transport event.
func (connection *Connection) Connect(ctx context.Context) (protocol.ServerHello, error) {
	connection.mu.Lock()
	if connection.lifecycle.state != Disconnected {
		state := connection.lifecycle.state
		connection.mu.Unlock()
		return protocol.ServerHello{}, &DisconnectedError{Message: fmt.Sprintf("Client is already %s", state)}
	}
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{MaxFrameLength: &connection.maxFrameLength})
	if err != nil {
		connection.mu.Unlock()
		return protocol.ServerHello{}, err
	}
	connection.sequence++
	id := connection.sequence
	handshake := &connectionHandshake{done: make(chan struct{})}
	attemptCtx, cancel := context.WithCancel(ctx)
	connection.lifecycle = &connectionLifecycle{state: Connecting, id: id, decoder: decoder, handshake: handshake, cancel: cancel}
	factoryDone := make(chan struct{})
	connection.pruneLifetimesLocked()
	connection.lifetimes = append(connection.lifetimes, factoryDone)
	connection.mu.Unlock()
	defer cancel()
	connection.options.OnStateChange(ConnectionStateChange{State: Connecting})
	handlers := ByteTransportHandlers{
		OnData:  func(chunk []byte) { connection.handleData(id, chunk) },
		OnClose: func() { connection.handleClose(id) },
		OnError: func(err error) { connection.failFor(id, toDisconnectedError(err), true) },
	}
	var completed atomic.Bool
	complete := func(transport ByteTransport, err error) {
		if completed.Swap(true) {
			return
		}
		defer close(factoryDone)
		connection.opened(id, transport, err)
	}
	connection.callFactory(attemptCtx, handlers, complete)
	select {
	case <-handshake.done:
		return handshake.hello, handshake.err
	case <-ctx.Done():
		select {
		case <-handshake.done:
			return handshake.hello, handshake.err
		default:
		}
		connection.failAttempt(id, handshake, context.Cause(ctx), true)
		<-handshake.done
		return handshake.hello, handshake.err
	}
}

func (connection *Connection) callFactory(ctx context.Context, handlers ByteTransportHandlers, complete func(ByteTransport, error)) {
	// Upstream catches a thrown factory error as well as a rejected Promise.
	defer func() {
		if failure := recover(); failure != nil {
			complete(nil, panicError(failure))
		}
	}()
	connection.options.TransportFactory(ctx, handlers, complete)
}

func (connection *Connection) opened(id uint64, transport ByteTransport, err error) {
	if lifetime, ok := transport.(interface{ Done() <-chan struct{} }); ok {
		connection.ownLifetime(lifetime.Done())
	}
	if err == nil && transport == nil {
		err = errors.New("Byte transport factory returned no transport")
	}
	if err != nil {
		connection.failFor(id, toDisconnectedError(err), false)
		return
	}
	connection.mu.Lock()
	current := connection.lifecycle
	if current.state != Connecting || current.id != id {
		connection.mu.Unlock()
		transport.Close()
		return
	}
	next := *current
	next.transport = &connectionTransport{transport: transport}
	connection.lifecycle = &next
	connection.mu.Unlock()
	frame, err := protocol.EncodeClientMessage(protocol.ClientHello{Version: protocol.ProtocolVersion}, protocol.FrameDecoderOptions{MaxFrameLength: &connection.maxFrameLength})
	if err != nil {
		connection.failFor(id, toDisconnectedError(err), true)
		return
	}
	connection.sendTransport(transport, frame, func(err error) {
		if err != nil {
			connection.failFor(id, toDisconnectedError(err), true)
		}
	})
}

// Send admits an ordered write without awaiting it. A later transport rejection fails the connection that owns that write, not a replacement connection.
func (connection *Connection) Send(frame []byte) error {
	connection.mu.Lock()
	current := connection.lifecycle
	if current.state != Connected {
		connection.mu.Unlock()
		return disconnectedError()
	}
	owner := current.transport
	id := current.id
	connection.mu.Unlock()
	connection.sendTransport(owner.transport, frame, func(err error) {
		if err == nil {
			return
		}
		connection.mu.Lock()
		active := connection.lifecycle.state != Disconnected && connection.lifecycle.transport == owner
		connection.mu.Unlock()
		if active {
			connection.failFor(id, toDisconnectedError(err), true)
		}
	})
	return nil
}
func (connection *Connection) sendTransport(transport ByteTransport, frame []byte, complete func(error)) {
	var completed atomic.Bool
	finish := func(err error) {
		if !completed.Swap(true) {
			complete(err)
		}
	}
	// Upstream handles both synchronous send throws and asynchronous write rejection.
	defer func() {
		if failure := recover(); failure != nil {
			finish(panicError(failure))
		}
	}()
	transport.Send(frame, finish)
}

// Disconnect ends the current attempt or connection. A nil reason selects the upstream default diagnostic.
func (connection *Connection) Disconnect(reason error) {
	if reason == nil {
		reason = &DisconnectedError{Message: "Client disconnected"}
	}
	connection.Fail(reason)
}
func (connection *Connection) Fail(err error) {
	connection.mu.Lock()
	id := connection.lifecycle.id
	connection.mu.Unlock()
	connection.failFor(id, err, true)
}

func (connection *Connection) failFor(id uint64, err error, closeTransport bool) {
	connection.failAttempt(id, nil, err, closeTransport)
}
func (connection *Connection) failAttempt(id uint64, expected *connectionHandshake, err error, closeTransport bool) {
	connection.mu.Lock()
	if expected != nil && expected.settled.Load() {
		connection.mu.Unlock()
		return
	}
	current := connection.lifecycle
	if current.state == Disconnected || current.id != id {
		connection.mu.Unlock()
		return
	}
	var publish func()
	if current.handshake != nil {
		publish = current.handshake.reserve(protocol.ServerHello{}, err)
	}
	connection.lifecycle = &connectionLifecycle{state: Disconnected}
	connection.mu.Unlock()
	if current.cancel != nil {
		current.cancel()
	}
	connection.options.OnStateChange(ConnectionStateChange{State: Disconnected, Error: err})
	if closeTransport && current.transport != nil {
		current.transport.transport.Close()
	}
	// Awaiters resume after the synchronous state callbacks and close, as after Pi's Promise microtask boundary.
	connection.afterDispatch(publish)
}

func (connection *Connection) handleData(id uint64, chunk []byte) {
	connection.mu.Lock()
	connection.deliveryDepth++
	connection.mu.Unlock()
	defer connection.endDispatch()
	connection.mu.Lock()
	current := connection.lifecycle
	if current.state == Disconnected || current.id != id {
		connection.mu.Unlock()
		return
	}
	if current.state == Connecting && current.transport == nil {
		connection.mu.Unlock()
		connection.failFor(id, &protocol.ProtocolValidationError{Message: "Received server data before the client hello was sent"}, true)
		return
	}
	connection.mu.Unlock()
	connection.decoderMu.Lock()
	messages, err := current.decoder.Push(chunk)
	connection.decoderMu.Unlock()
	if err != nil {
		connection.failFor(id, err, true)
		return
	}
	for _, message := range messages {
		if connection.State() == Disconnected {
			return
		}
		connection.handleMessage(message)
	}
}

func (connection *Connection) handleMessage(message protocol.ServerMessage) {
	connection.mu.Lock()
	current := connection.lifecycle
	connection.mu.Unlock()
	if current.state == Connecting {
		if failure, ok := message.(protocol.ServerHelloError); ok {
			connection.failFor(current.id, NewServerError(failure.Error), true)
			return
		}
		hello, ok := message.(protocol.ServerHello)
		if !ok {
			connection.failFor(current.id, &protocol.ProtocolValidationError{Message: "Expected server hello as first message"}, true)
			return
		}
		if hello.ServerId != connection.options.ServerId {
			connection.failFor(current.id, &protocol.ProtocolValidationError{Message: fmt.Sprintf("Connected server %q does not match %q", hello.ServerId, connection.options.ServerId)}, true)
			return
		}
		if current.transport == nil {
			connection.failFor(current.id, &protocol.ProtocolValidationError{Message: "Received server hello before the client hello was sent"}, true)
			return
		}
		connected := *current
		connected.state = Connected
		connection.mu.Lock()
		if connection.lifecycle != current {
			connection.mu.Unlock()
			return
		}
		connection.lifecycle = &connected
		connection.mu.Unlock()
		if err := connection.options.OnHandshake(hello); err != nil {
			if connection.isLifecycle(&connected) {
				connection.failFor(current.id, err, true)
			}
			return
		}
		if !connection.isLifecycle(&connected) {
			return
		}
		connection.options.OnStateChange(ConnectionStateChange{State: Connected})
		connection.mu.Lock()
		if connection.lifecycle != &connected {
			connection.mu.Unlock()
			return
		}
		settled := connected
		settled.handshake = nil
		connection.lifecycle = &settled
		publish := current.handshake.reserve(hello, nil)
		connection.mu.Unlock()
		connection.afterDispatch(publish)
		return
	}
	if current.state != Connected {
		return
	}
	switch message.(type) {
	case protocol.ServerHello, protocol.ServerHelloError:
		connection.failFor(current.id, &protocol.ProtocolValidationError{Message: "Unexpected handshake message"}, true)
	default:
		connection.options.OnMessage(message)
	}
}
func (connection *Connection) isLifecycle(expected *connectionLifecycle) bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.lifecycle == expected
}
func (connection *Connection) handleClose(id uint64) {
	connection.mu.Lock()
	current := connection.lifecycle
	if current.state == Disconnected || current.id != id {
		connection.mu.Unlock()
		return
	}
	connection.mu.Unlock()
	var err error = &DisconnectedError{Message: "Byte transport closed"}
	connection.decoderMu.Lock()
	decoderError := current.decoder.End()
	connection.decoderMu.Unlock()
	if decoderError != nil {
		err = decoderError
	}
	connection.failFor(id, err, false)
}

// Promise continuations run after the complete incoming chunk, including all coalesced messages and reentrant deliveries.
func (connection *Connection) afterDispatch(run func()) {
	if run == nil {
		return
	}
	connection.mu.Lock()
	connection.continuations = append(connection.continuations, run)
	start := connection.deliveryDepth == 0 && !connection.flushing
	if start {
		connection.flushing = true
	}
	connection.mu.Unlock()
	if start {
		connection.flushContinuations()
	}
}

func (connection *Connection) endDispatch() {
	connection.mu.Lock()
	connection.deliveryDepth--
	start := connection.deliveryDepth == 0 && !connection.flushing
	if start {
		connection.flushing = true
	}
	connection.mu.Unlock()
	if start {
		connection.flushContinuations()
	}
}

func (connection *Connection) ownLifetime(done <-chan struct{}) {
	if done == nil {
		return
	}
	connection.mu.Lock()
	connection.pruneLifetimesLocked()
	connection.lifetimes = append(connection.lifetimes, done)
	connection.mu.Unlock()
}

func (connection *Connection) pruneLifetimesLocked() {
	live := connection.lifetimes[:0]
	for _, done := range connection.lifetimes {
		select {
		case <-done:
		default:
			live = append(live, done)
		}
	}
	clear(connection.lifetimes[len(live):])
	connection.lifetimes = live
}

func (connection *Connection) waitClosed(ctx context.Context) error {
	for {
		connection.mu.Lock()
		connection.pruneLifetimesLocked()
		if len(connection.lifetimes) == 0 {
			connection.mu.Unlock()
			return nil
		}
		done := connection.lifetimes[0]
		connection.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func (connection *Connection) flushContinuations() {
	for {
		connection.mu.Lock()
		if connection.deliveryDepth != 0 || len(connection.continuations) == 0 {
			connection.flushing = false
			connection.mu.Unlock()
			return
		}
		pending := connection.continuations
		connection.continuations = nil
		connection.mu.Unlock()
		for _, run := range pending {
			run()
		}
	}
}
