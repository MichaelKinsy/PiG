package experimental

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// RadiusClientTransportOptions selects one authenticated raw client relay.
type RadiusClientTransportOptions struct {
	ServerID         string
	Auth             *RadiusRelayAuthResolver
	WebSocketFactory RadiusRelayWebSocketFactory
}

// RadiusClientTransportFactory resolves fresh credentials and connects one client byte stream. The context owns the resulting transport until Close.
type RadiusClientTransportFactory func(context.Context, RelayByteConnectionHandler) (*RadiusClientByteTransport, error)

// CreateRadiusClientTransportFactory creates an inert connection factory.
func CreateRadiusClientTransportFactory(options RadiusClientTransportOptions) RadiusClientTransportFactory {
	return func(ctx context.Context, handlers RelayByteConnectionHandler) (*RadiusClientByteTransport, error) {
		if options.Auth == nil {
			return nil, errors.New("Radius authentication is required")
		}
		if !connectionIDPattern.MatchString(options.ServerID) {
			return nil, errors.New("Invalid Radius relay server ID")
		}
		auth, err := options.Auth.Resolve(ctx, true)
		if err != nil {
			return nil, err
		}
		socket, err := openRadiusRelayWebSocket(ctx, auth, options.ServerID, RadiusRelayClientSubprotocol, options.WebSocketFactory)
		if err != nil {
			return nil, err
		}
		transport := &RadiusClientByteTransport{socket: socket, writer: newOrderedWebSocketWriter(socket), handlers: handlers, done: make(chan struct{})}
		go transport.run(ctx)
		return transport, nil
	}
}

// RadiusClientByteTransport carries raw binary messages and reports one remote terminal event. Close initiates local shutdown; Done joins reads, writes, and cancellation cleanup.
type RadiusClientByteTransport struct {
	socket   RadiusRelayWebSocket
	writer   *orderedWebSocketWriter
	handlers RelayByteConnectionHandler
	closed   atomic.Bool
	done     chan struct{}
}

// Send copies and writes a chunk, in submission order, with bounded pending bytes.
func (t *RadiusClientByteTransport) Send(chunk []byte) error {
	if t.closed.Load() {
		return errors.New("Radius relay client is closed")
	}
	return t.writer.send(true, chunk)
}

// Close initiates shutdown without emitting a remote terminal callback. It is safe in a data callback.
func (t *RadiusClientByteTransport) Close() {
	if t.closed.Swap(true) {
		return
	}
	t.writer.close()
	_ = t.socket.Close(1000, "Pi client closed")
}

// Done closes when every operation owned by this transport has finished.
func (t *RadiusClientByteTransport) Done() <-chan struct{} { return t.done }
func (t *RadiusClientByteTransport) run(ctx context.Context) {
	readDone, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			t.Close()
		case <-readDone:
		}
	}()
	defer func() { close(readDone); <-watchDone; t.writer.wait(); close(t.done) }()
	for {
		binary, data, err := t.socket.Read()
		if t.closed.Load() {
			return
		}
		if err != nil {
			if _, ok := errors.AsType[*websocket.CloseError](err); ok {
				t.finish(nil)
			} else {
				t.finish(relayWebSocketError(err))
			}
			return
		}
		if !binary {
			t.finish(errors.New("Radius relay client received a non-binary message"))
			return
		}
		if t.handlers.OnData != nil {
			t.handlers.OnData(data)
		}
	}
}
func (t *RadiusClientByteTransport) finish(err error) {
	if t.closed.Swap(true) {
		return
	}
	t.writer.close()
	if err != nil {
		_ = t.socket.Close(4001, "Radius relay transport error")
	} else {
		_ = t.socket.Close(1000, "Pi client closed")
	}
	t.handlers.finish(err)
}

// RadiusClientAttachment identifies the currently selected Session.
type RadiusClientAttachment struct{ SessionID string }

// RadiusReconnectClient is the established-client contract used by the reconnect owner. Listener removers must be safe during notification; methods and listeners may run concurrently.
type RadiusReconnectClient interface {
	Attachment() *RadiusClientAttachment
	Connected() bool
	ConnectionState() string
	Disconnect(reason string)
	OnAttachmentChange(func(*RadiusClientAttachment)) func()
	OnConnectionStateChange(func(string)) func()
	Reconnect(context.Context) error
}

// RadiusClientReconnect reconnects an established client and restores its last selected Session. Dispose removes listeners, cancels, disconnects, and joins the retry loop.
type RadiusClientReconnect struct {
	client           RadiusReconnectClient
	reattach         func(context.Context, string) error
	cancel           context.CancelFunc
	removeConnection func()
	removeAttachment func()
	mu               sync.Mutex
	desiredSessionID *string
	reconnecting     bool
	disposed         bool
	wg               sync.WaitGroup
}

// NewRadiusClientReconnect observes an already established client; it does not initiate a connection until a disconnection event.
func NewRadiusClientReconnect(ctx context.Context, client RadiusReconnectClient, reattach func(context.Context, string) error) *RadiusClientReconnect {
	ctx, cancel := context.WithCancel(ctx)
	r := &RadiusClientReconnect{client: client, reattach: reattach, cancel: cancel}
	if attachment := client.Attachment(); attachment != nil {
		id := attachment.SessionID
		r.desiredSessionID = &id
	}
	r.removeAttachment = client.OnAttachmentChange(func(attachment *RadiusClientAttachment) {
		connected := client.Connected()
		r.mu.Lock()
		defer r.mu.Unlock()
		if attachment != nil {
			id := attachment.SessionID
			r.desiredSessionID = &id
		} else if connected {
			r.desiredSessionID = nil
		}
	})
	r.removeConnection = client.OnConnectionStateChange(func(state string) {
		if state == "disconnected" {
			r.start(ctx)
		}
	})
	return r
}

// Dispose is idempotent and joins all retry and reattachment work.
func (r *RadiusClientReconnect) Dispose() {
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		r.wg.Wait()
		return
	}
	r.disposed = true
	r.cancel()
	r.mu.Unlock()
	r.removeConnection()
	r.removeAttachment()
	if r.client.ConnectionState() != "disconnected" {
		r.client.Disconnect("Radius reconnect stopped")
	}
	r.wg.Wait()
}
func (r *RadiusClientReconnect) start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disposed || r.reconnecting {
		return
	}
	r.reconnecting = true
	r.wg.Go(func() {
		defer func() { r.mu.Lock(); r.reconnecting = false; r.mu.Unlock() }()
		r.run(ctx)
	})
}
func (r *RadiusClientReconnect) run(ctx context.Context) {
	retry := relayRetryInitial
	for ctx.Err() == nil && !r.client.Connected() {
		if relayDelay(ctx, retry) != nil {
			return
		}
		err := r.client.Reconnect(ctx)
		if err == nil {
			r.mu.Lock()
			sessionID := r.desiredSessionID
			r.mu.Unlock()
			if sessionID != nil {
				err = r.reattach(ctx, *sessionID)
			}
			if err == nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		if r.client.Connected() {
			r.client.Disconnect(err.Error())
		}
		retry = min(retry*2, relayRetryMax)
	}
}
