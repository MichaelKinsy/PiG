package client

// Ports packages/client/src/client.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// ClientOptions selects one logical server and a fresh transport factory.
type ClientOptions struct {
	TransportFactory ByteTransportFactory
	ServerId         string
	MaxFrameLength   *float64
	OnListenerError  func(error)
}

type pendingRequest struct {
	id                      string
	done                    chan struct{}
	once                    sync.Once
	value                   any
	err                     error
	resolve                 func(json.RawMessage)
	mu                      sync.Mutex
	stopAbort               func() bool
	finished, sent, aborted bool
}

func (request *pendingRequest) reserve(value any, err error) (publish func()) {
	request.once.Do(func() { request.value, request.err = value, err; publish = func() { close(request.done) } })
	return publish
}
func (request *pendingRequest) cleanup() {
	request.mu.Lock()
	request.finished = true
	stop := request.stopAbort
	request.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// ConnectionStateChangeListener gives Go callbacks the stable identity that JavaScript functions have in a Set.
type ConnectionStateChangeListener struct{ call func(ConnectionStateChange) }

func NewConnectionStateChangeListener(call func(ConnectionStateChange)) *ConnectionStateChangeListener {
	return &ConnectionStateChangeListener{call: call}
}

// AttachmentChangeListener retains callback identity across removal and re-registration.
type AttachmentChangeListener struct{ call func(*protocol.SessionTarget) }

func NewAttachmentChangeListener(call func(*protocol.SessionTarget)) *AttachmentChangeListener {
	return &AttachmentChangeListener{call: call}
}

type connectionListener struct {
	identity *ConnectionStateChangeListener
	closed   bool
}
type attachmentListener struct {
	identity *AttachmentChangeListener
	closed   bool
}

type activeServiceListener struct {
	target          protocol.RpcTarget
	listener        func(chord.ServiceProviderUpdate) error
	decoder         *chord.ServiceStateDecoder
	mu              sync.Mutex
	queuedWire      []json.RawMessage
	queued          []chord.ServiceProviderUpdate
	hydrated, ready bool
	delivery        []chord.ServiceProviderUpdate
	running         bool
	delivered       chan struct{}
}

// Client correlates framed requests, maintains attachment state, and hydrates ordered Chord subscriptions. It does not interpret application service methods.
type Client struct {
	options                                ClientOptions
	connection                             *Connection
	mu                                     sync.Mutex
	pending                                map[string]*pendingRequest
	pendingOrder                           []string
	connectionListeners                    []*connectionListener
	attachmentListeners                    []*attachmentListener
	connectionDispatch, attachmentDispatch int
	serviceListeners                       map[string]*activeServiceListener
	requestSequence, subscriptionSequence  uint64
	hello                                  *protocol.ServerHello
	attachment                             *protocol.SessionTarget
	disposed                               bool
}

// NewClient validates identity and creates a disconnected client without opening a transport.
func NewClient(options ClientOptions) (*Client, error) {
	if !protocol.IsServerId(options.ServerId) {
		return nil, errors.New("serverId must be a canonical lowercase UUIDv4")
	}
	client := &Client{options: options, pending: map[string]*pendingRequest{}, serviceListeners: map[string]*activeServiceListener{}}
	connection, err := NewConnection(ConnectionOptions{
		TransportFactory: options.TransportFactory, ServerId: options.ServerId, MaxFrameLength: options.MaxFrameLength,
		OnHandshake: func(hello protocol.ServerHello) error {
			client.mu.Lock()
			client.hello = &hello
			client.mu.Unlock()
			return nil
		},
		OnMessage: client.handleMessage, OnStateChange: client.handleConnectionStateChange,
	})
	if err != nil {
		return nil, err
	}
	client.connection = connection
	return client, nil
}

// Connect constructs a client and waits for its handshake. Failed startup disposes the client and joins Go-owned attempts and transport I/O before returning the original error.
func Connect(ctx context.Context, options ClientOptions) (*Client, error) {
	client, err := NewClient(options)
	if err != nil {
		return nil, err
	}
	if _, err := client.Connect(ctx); err != nil {
		if cleanup := client.Dispose(); cleanup != nil {
			return nil, cleanup
		}
		if cleanup := client.WaitClosed(context.WithoutCancel(ctx)); cleanup != nil {
			return nil, cleanup
		}
		return nil, err
	}
	return client, nil
}
func (client *Client) Connect(ctx context.Context) (protocol.ServerHello, error) {
	client.mu.Lock()
	if client.disposed {
		client.mu.Unlock()
		return protocol.ServerHello{}, &ClientDisposedError{}
	}
	client.hello = nil
	client.mu.Unlock()
	return client.connection.Connect(ctx)
}
func (client *Client) Reconnect(ctx context.Context) (protocol.ServerHello, error) {
	return client.Connect(ctx)
}
func (client *Client) Disconnect(reason error) { client.connection.Disconnect(reason) }
func (client *Client) Disposed() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.disposed
}
func (client *Client) ConnectionState() ConnectionState { return client.connection.State() }
func (client *Client) Connected() bool                  { return client.ConnectionState() == Connected }
func (client *Client) ServerId() string                 { return client.options.ServerId }
func (client *Client) Hello() *protocol.ServerHello {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.hello
}
func (client *Client) Attachment() *protocol.SessionTarget {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.attachment
}

func (client *Client) OnConnectionStateChange(identity *ConnectionStateChangeListener) (func(), error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.disposed {
		return nil, &ClientDisposedError{}
	}
	present := slices.ContainsFunc(client.connectionListeners, func(listener *connectionListener) bool { return !listener.closed && listener.identity == identity })
	if !present {
		client.connectionListeners = append(client.connectionListeners, &connectionListener{identity: identity})
	}
	return func() {
		client.mu.Lock()
		for _, listener := range client.connectionListeners {
			if listener.identity == identity {
				listener.closed = true
			}
		}
		if client.connectionDispatch == 0 {
			client.connectionListeners = slices.DeleteFunc(client.connectionListeners, func(listener *connectionListener) bool { return listener.closed })
		}
		client.mu.Unlock()
	}, nil
}
func (client *Client) OnAttachmentChange(identity *AttachmentChangeListener) (func(), error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.disposed {
		return nil, &ClientDisposedError{}
	}
	present := slices.ContainsFunc(client.attachmentListeners, func(listener *attachmentListener) bool { return !listener.closed && listener.identity == identity })
	if !present {
		client.attachmentListeners = append(client.attachmentListeners, &attachmentListener{identity: identity})
	}
	return func() {
		client.mu.Lock()
		for _, listener := range client.attachmentListeners {
			if listener.identity == identity {
				listener.closed = true
			}
		}
		if client.attachmentDispatch == 0 {
			client.attachmentListeners = slices.DeleteFunc(client.attachmentListeners, func(listener *attachmentListener) bool { return listener.closed })
		}
		client.mu.Unlock()
	}, nil
}

// Request waits for one service result. Cancellation rejects this caller and sends a fenced cancel envelope without dropping the response correlation.
func (client *Client) Request(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall) (json.RawMessage, error) {
	operation, err := client.BeginInvoke(ctx, target, call)
	if err != nil {
		return nil, err
	}
	return operation.Wait(context.Background())
}
func (client *Client) request(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall, transform func(json.RawMessage) (any, error)) (any, error) {
	pending, err := client.beginRequest(ctx, target, call, transform)
	if err != nil {
		return nil, err
	}
	<-pending.done
	return pending.value, pending.err
}

func (client *Client) beginRequest(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall, transform func(json.RawMessage) (any, error)) (*pendingRequest, error) {
	client.mu.Lock()
	if client.disposed {
		client.mu.Unlock()
		return nil, &ClientDisposedError{}
	}
	if !client.Connected() {
		client.mu.Unlock()
		return nil, disconnectedError()
	}
	if ctx.Err() != nil {
		client.mu.Unlock()
		return nil, context.Cause(ctx)
	}
	client.requestSequence++
	id := fmt.Sprintf("request-%d", client.requestSequence)
	pending := &pendingRequest{id: id, done: make(chan struct{})}
	pending.resolve = func(raw json.RawMessage) {
		var value any = raw
		if transform != nil {
			var err error
			value, err = transform(raw)
			if err != nil {
				failure := &protocol.ProtocolValidationError{Message: err.Error()}
				client.connection.Fail(failure)
				client.connection.afterDispatch(pending.reserve(nil, failure))
				return
			}
		}
		client.connection.afterDispatch(pending.reserve(value, nil))
	}
	client.pending[id] = pending
	client.pendingOrder = append(client.pendingOrder, id)
	client.mu.Unlock()
	sendCancel := func() {
		if !client.Connected() {
			return
		}
		frame, err := protocol.EncodeClientMessage(protocol.CancelEnvelope{Id: id, Target: target}, protocol.FrameDecoderOptions{MaxFrameLength: &client.connection.maxFrameLength})
		if err != nil {
			client.connection.Fail(err)
			return
		}
		if err := client.connection.Send(frame); err != nil {
			client.connection.Fail(err)
		}
	}
	stop := context.AfterFunc(ctx, func() {
		client.connection.afterDispatch(func() {
			pending.mu.Lock()
			if pending.finished || pending.aborted {
				pending.mu.Unlock()
				return
			}
			pending.aborted = true
			sent := pending.sent
			publish := pending.reserve(nil, context.Cause(ctx))
			pending.mu.Unlock()
			if sent {
				sendCancel()
			}
			client.connection.afterDispatch(publish)
		})
	})
	pending.mu.Lock()
	pending.stopAbort = stop
	finished := pending.finished
	pending.mu.Unlock()
	if finished {
		stop()
	}
	data, err := json.Marshal(call)
	if err == nil {
		_, err = chord.ParseServiceCall(data)
	}
	var value any
	if err == nil {
		value, err = protocol.FromJSON(data)
	}
	var frame []byte
	if err == nil {
		frame, err = protocol.EncodeClientMessage(protocol.RequestEnvelope{Id: id, Target: target, Call: value}, protocol.FrameDecoderOptions{MaxFrameLength: &client.connection.maxFrameLength})
	}
	if err != nil {
		if request := client.takePending(id); request != nil {
			client.connection.afterDispatch(request.reserve(nil, err))
		}
	} else {
		if err := client.connection.Send(frame); err != nil {
			if request := client.takePending(id); request != nil {
				client.connection.afterDispatch(request.reserve(nil, err))
			}
		}
		pending.mu.Lock()
		pending.sent = true
		aborted := pending.aborted
		pending.mu.Unlock()
		if aborted {
			sendCancel()
		}
	}
	return pending, nil
}

func (client *Client) takePending(id string) *pendingRequest {
	client.mu.Lock()
	pending := client.pending[id]
	if pending != nil {
		delete(client.pending, id)
		client.pendingOrder = slices.DeleteFunc(client.pendingOrder, func(value string) bool { return value == id })
	}
	client.mu.Unlock()
	if pending != nil {
		pending.cleanup()
	}
	return pending
}
func (client *Client) rejectPending(err error) {
	client.mu.Lock()
	requests := make([]*pendingRequest, 0, len(client.pendingOrder))
	for _, id := range client.pendingOrder {
		requests = append(requests, client.pending[id])
	}
	clear(client.pending)
	client.pendingOrder = nil
	client.mu.Unlock()
	for _, request := range requests {
		request.cleanup()
		client.connection.afterDispatch(request.reserve(nil, err))
	}
}

// ServiceCatalogue validates the returned catalogue; malformed server data terminates the connection.
func (client *Client) ServiceCatalogue(ctx context.Context, target protocol.RpcTarget) ([]chord.ServiceCatalogueEntry, error) {
	type result struct {
		entries []chord.ServiceCatalogueEntry
		err     error
	}
	completed := make(chan result, 1)
	client.ServiceCatalogueCallback(ctx, target, func(entries []chord.ServiceCatalogueEntry, err error) { completed <- result{entries, err} })
	outcome := <-completed
	return outcome.entries, outcome.err
}

// ServiceSubscription holds the initial snapshot and buffers subsequent updates until Start. Dispose removes its listener before unsubscribing and drains admitted delivery after a successful unsubscribe.
type ServiceSubscription struct {
	Id       string
	Target   protocol.RpcTarget
	Snapshot chord.ServiceSubscriptionSnapshot
	client   *Client
	active   *activeServiceListener
	disposed atomic.Bool
}

func (client *Client) SubscribeService(ctx context.Context, target protocol.RpcTarget, serviceId string, mode chord.ServiceMode, listener func(chord.ServiceProviderUpdate) error) (*ServiceSubscription, error) {
	client.mu.Lock()
	client.subscriptionSequence++
	id := fmt.Sprintf("service-%d", client.subscriptionSequence)
	active := &activeServiceListener{target: target, listener: listener, decoder: chord.CreateServiceStateDecoder()}
	client.serviceListeners[id] = active
	client.mu.Unlock()
	value, err := client.request(ctx, target, chord.CreateServiceSubscribeCall(id, serviceId, mode), func(raw json.RawMessage) (any, error) {
		wire, err := chord.ParseWireServiceSubscriptionSnapshot(raw)
		if err != nil {
			return nil, err
		}
		active.mu.Lock()
		defer active.mu.Unlock()
		snapshot, err := active.decoder.DecodeSnapshot(wire)
		if err != nil {
			return nil, err
		}
		active.hydrated = true
		for _, raw := range active.queuedWire {
			wire, err := chord.ParseWireServiceProviderUpdate(raw)
			if err != nil {
				return nil, err
			}
			update, err := active.decoder.DecodeUpdate(wire)
			if err != nil {
				return nil, err
			}
			active.queued = append(active.queued, update)
		}
		active.queuedWire = nil
		return snapshot, nil
	})
	client.mu.Lock()
	current := client.serviceListeners[id] == active
	if err != nil && current {
		delete(client.serviceListeners, id)
	}
	client.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, disconnectedError()
	}
	return &ServiceSubscription{Id: id, Target: target, Snapshot: value.(chord.ServiceSubscriptionSnapshot), client: client, active: active}, nil
}

func (subscription *ServiceSubscription) Start() {
	if subscription.disposed.Load() {
		return
	}
	active := subscription.active
	active.mu.Lock()
	if active.ready {
		active.mu.Unlock()
		return
	}
	active.ready = true
	queued := active.queued
	active.queued = nil
	for _, update := range queued {
		subscription.client.admitDeliveryLocked(active, update)
	}
	active.mu.Unlock()
}
func (subscription *ServiceSubscription) Dispose() error {
	if subscription.disposed.Swap(true) {
		return nil
	}
	client, active := subscription.client, subscription.active
	client.mu.Lock()
	if client.serviceListeners[subscription.Id] == active {
		delete(client.serviceListeners, subscription.Id)
	}
	client.mu.Unlock()
	defer func() { active.mu.Lock(); active.queuedWire = nil; active.queued = nil; active.mu.Unlock() }()
	if client.Connected() && client.targetIsCurrent(subscription.Target) {
		if _, err := client.Request(context.Background(), subscription.Target, chord.CreateServiceUnsubscribeCall(subscription.Id)); err != nil {
			return err
		}
	}
	active.mu.Lock()
	done := active.delivered
	active.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (client *Client) handleMessage(message protocol.ServerMessage) {
	switch message := message.(type) {
	case protocol.AttachmentEnvelope:
		if message.Attachment != nil && message.Attachment.ServerId != client.options.ServerId {
			client.connection.Fail(&protocol.ProtocolValidationError{Message: "Attachment update belongs to another server"})
			return
		}
		client.setAttachment(message.Attachment)
	case protocol.ServiceEventEnvelope:
		client.handleServiceUpdate(message)
	case protocol.ResponseEnvelope:
		pending := client.takePending(message.Id)
		if pending == nil {
			client.connection.Fail(&protocol.ProtocolValidationError{Message: "Response has no matching request"})
			return
		}
		if !message.Ok {
			client.connection.afterDispatch(pending.reserve(nil, NewServerError(*message.Error)))
			return
		}
		var result json.RawMessage
		if message.HasResult {
			var err error
			result, err = protocol.ToJSON(message.Result)
			if err != nil {
				client.connection.Fail(err)
				client.connection.afterDispatch(pending.reserve(nil, err))
				return
			}
		}
		pending.resolve(result)
	}
}
func (client *Client) handleServiceUpdate(message protocol.ServiceEventEnvelope) {
	client.mu.Lock()
	active := client.serviceListeners[message.SubscriptionId]
	if active == nil {
		client.mu.Unlock()
		return
	}
	raw, err := protocol.ToJSON(message.Update)
	if err != nil {
		client.mu.Unlock()
		client.connection.Fail(err)
		return
	}
	active.mu.Lock()
	if !active.hydrated {
		active.queuedWire = append(active.queuedWire, raw)
		active.mu.Unlock()
		client.mu.Unlock()
		return
	}
	wire, err := chord.ParseWireServiceProviderUpdate(raw)
	var update chord.ServiceProviderUpdate
	if err == nil {
		update, err = active.decoder.DecodeUpdate(wire)
	}
	if err == nil {
		if active.ready {
			client.admitDeliveryLocked(active, update)
		} else {
			active.queued = append(active.queued, update)
		}
	}
	active.mu.Unlock()
	client.mu.Unlock()
	if err != nil {
		client.connection.Fail(&protocol.ProtocolValidationError{Message: err.Error()})
	}
}
func (client *Client) admitDeliveryLocked(active *activeServiceListener, update chord.ServiceProviderUpdate) {
	active.delivery = append(active.delivery, update)
	if active.running {
		return
	}
	active.running = true
	active.delivered = make(chan struct{})
	client.connection.ownLifetime(active.delivered)
	client.connection.afterDispatch(func() { go client.deliver(active) })
}
func (client *Client) deliver(active *activeServiceListener) {
	for {
		active.mu.Lock()
		if len(active.delivery) == 0 {
			active.running = false
			close(active.delivered)
			active.mu.Unlock()
			return
		}
		update := active.delivery[0]
		active.delivery[0] = chord.ServiceProviderUpdate{}
		active.delivery = active.delivery[1:]
		active.mu.Unlock()
		func() {
			defer func() {
				if failure := recover(); failure != nil {
					client.reportListenerError(panicError(failure))
				}
			}()
			if err := active.listener(update); err != nil {
				client.reportListenerError(err)
			}
		}()
	}
}
func (client *Client) handleConnectionStateChange(change ConnectionStateChange) {
	if change.State == Disconnected {
		client.mu.Lock()
		client.hello = nil
		client.mu.Unlock()
		client.setAttachment(nil)
		failure := change.Error
		if failure == nil {
			failure = disconnectedError()
		}
		client.rejectPending(failure)
		client.mu.Lock()
		clear(client.serviceListeners)
		client.mu.Unlock()
	}
	client.mu.Lock()
	client.connectionDispatch++
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		client.connectionDispatch--
		if client.connectionDispatch == 0 {
			client.connectionListeners = slices.DeleteFunc(client.connectionListeners, func(listener *connectionListener) bool { return listener.closed })
		}
		client.mu.Unlock()
	}()
	for i := 0; ; i++ {
		client.mu.Lock()
		if i >= len(client.connectionListeners) {
			client.mu.Unlock()
			return
		}
		listener := client.connectionListeners[i]
		closed := listener.closed
		client.mu.Unlock()
		if !closed {
			client.callListener(func() { listener.identity.call(change) })
		}
	}
}
func (client *Client) setAttachment(attachment *protocol.SessionTarget) {
	client.mu.Lock()
	previous := client.attachment
	if sameAttachment(previous, attachment) {
		client.mu.Unlock()
		return
	}
	client.attachment = attachment
	client.attachmentDispatch++
	client.mu.Unlock()
	defer func() {
		client.mu.Lock()
		client.attachmentDispatch--
		if client.attachmentDispatch == 0 {
			client.attachmentListeners = slices.DeleteFunc(client.attachmentListeners, func(listener *attachmentListener) bool { return listener.closed })
		}
		client.mu.Unlock()
	}()
	for i := 0; ; i++ {
		client.mu.Lock()
		if i >= len(client.attachmentListeners) {
			client.mu.Unlock()
			return
		}
		listener := client.attachmentListeners[i]
		closed := listener.closed
		client.mu.Unlock()
		if !closed {
			client.callListener(func() { listener.identity.call(attachment) })
		}
	}
}
func sameAttachment(left, right *protocol.SessionTarget) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
func (client *Client) targetIsCurrent(target protocol.RpcTarget) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	switch target := target.(type) {
	case protocol.ServerTarget:
		return client.hello != nil && client.hello.ServerId == target.ServerId
	case *protocol.ServerTarget:
		return target != nil && client.hello != nil && client.hello.ServerId == target.ServerId
	case protocol.SessionTarget:
		return sameAttachment(client.attachment, &target)
	case *protocol.SessionTarget:
		return sameAttachment(client.attachment, target)
	}
	return false
}
func (client *Client) callListener(call func()) {
	defer func() {
		if failure := recover(); failure != nil {
			client.reportListenerError(panicError(failure))
		}
	}()
	call()
}
func (client *Client) reportListenerError(err error) {
	if client.options.OnListenerError == nil {
		return
	}
	defer func() {
		if failure := recover(); failure != nil {
			_ = failure
		}
	}()
	client.options.OnListenerError(err)
}

// Dispose rejects pending requests, disconnects, and removes listeners. Repeated calls are harmless. Subscription owners dispose their subscriptions to join pending listener deliveries.
func (client *Client) Dispose() error {
	client.mu.Lock()
	if client.disposed {
		client.mu.Unlock()
		return nil
	}
	client.disposed = true
	client.mu.Unlock()
	failure := &ClientDisposedError{}
	client.rejectPending(failure)
	client.connection.Disconnect(failure)
	client.mu.Lock()
	client.hello = nil
	client.mu.Unlock()
	client.setAttachment(nil)
	client.mu.Lock()
	client.connectionListeners = nil
	client.attachmentListeners = nil
	clear(client.serviceListeners)
	client.mu.Unlock()
	return nil
}

// WaitClosed joins Go-owned transport attempts, native I/O, and admitted subscription deliveries after Dispose. It is separate from callback-safe Dispose so a callback never waits for its own transport reader.
func (client *Client) WaitClosed(ctx context.Context) error { return client.connection.waitClosed(ctx) }

// ServiceTransportClient is the request/subscription capability consumed by a routed Chord transport. Decorators retain the production Client's lifecycle while interposing one service operation.
type ServiceTransportClient interface {
	Request(context.Context, protocol.RpcTarget, chord.ServiceCall) (json.RawMessage, error)
	SubscribeService(context.Context, protocol.RpcTarget, string, chord.ServiceMode, func(chord.ServiceProviderUpdate) error) (*ServiceSubscription, error)
}

// CreateClientServiceTransport resolves the current target for each operation. Update listeners receive the background context, matching upstream's transport adapter.
func CreateClientServiceTransport(client ServiceTransportClient, getTarget func() protocol.RpcTarget) chord.RemoteServiceTransport {
	return &clientServiceTransport{client: client, getTarget: getTarget}
}

type clientServiceTransport struct {
	client    ServiceTransportClient
	getTarget func() protocol.RpcTarget
}

func (transport *clientServiceTransport) Invoke(ctx context.Context, call chord.ServiceCall) (json.RawMessage, error) {
	target := transport.getTarget()
	if target == nil {
		return nil, errors.New("Remote service target is unavailable")
	}
	return transport.client.Request(ctx, target, call)
}
func (transport *clientServiceTransport) Subscribe(ctx context.Context, serviceId string, mode chord.ServiceMode, listener chord.UpdateListener) (chord.ServiceSubscription, error) {
	target := transport.getTarget()
	if target == nil {
		return nil, errors.New("Remote service target is unavailable")
	}
	subscription, err := transport.client.SubscribeService(ctx, target, serviceId, mode, func(update chord.ServiceProviderUpdate) error { listener(context.Background(), update); return nil })
	if err != nil {
		return nil, err
	}
	return serviceTransportSubscription{subscription}, nil
}

type serviceTransportSubscription struct{ subscription *ServiceSubscription }

func (subscription serviceTransportSubscription) Snapshot() chord.ServiceSubscriptionSnapshot {
	return subscription.subscription.Snapshot
}
func (subscription serviceTransportSubscription) Activate() error {
	subscription.subscription.Start()
	return nil
}
func (subscription serviceTransportSubscription) Close(context.Context) error {
	return subscription.subscription.Dispose()
}
