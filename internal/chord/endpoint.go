package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ServiceUpdatePublisher sends one subscription update to the remote
// consumer. Its failure is ignored by the endpoint, as upstream.
type ServiceUpdatePublisher func(ctx context.Context, subscriptionId string, update ServiceProviderUpdate) error

// RemoteServiceEndpoint hosts one provider for one remote consumer and owns
// that consumer's subscriptions (upstream RemoteServiceEndpoint).
type RemoteServiceEndpoint interface {
	Invoke(ctx context.Context, call ServiceCall, publish ServiceUpdatePublisher) (json.RawMessage, error)
	Dispose()
}

type remoteServiceEndpoint struct {
	provider      *RemoteServiceProvider
	mu            sync.Mutex
	subscriptions map[string]ServiceSubscription
	disposed      bool
}

// CreateRemoteServiceEndpoint serves $chord.service catalogue, subscribe and
// unsubscribe control calls and routes every other call to provider.Invoke.
func CreateRemoteServiceEndpoint(provider *RemoteServiceProvider) RemoteServiceEndpoint {
	return &remoteServiceEndpoint{provider: provider, subscriptions: map[string]ServiceSubscription{}}
}

func (endpoint *remoteServiceEndpoint) Invoke(ctx context.Context, call ServiceCall, publish ServiceUpdatePublisher) (json.RawMessage, error) {
	endpoint.mu.Lock()
	if endpoint.disposed {
		endpoint.mu.Unlock()
		return nil, errors.New("Remote service endpoint is disposed")
	}
	endpoint.mu.Unlock()
	control, ok := DecodeServiceControlCall(call)
	if !ok {
		return endpoint.provider.Invoke(ctx, call)
	}
	switch control.Type {
	case controlCatalogue:
		return json.Marshal(endpoint.provider.Catalogue())
	case controlSubscribe:
		endpoint.mu.Lock()
		if _, exists := endpoint.subscriptions[control.SubscriptionId]; exists {
			endpoint.mu.Unlock()
			return nil, errors.New("Service subscription ID is already active")
		}
		subscriptionId := control.SubscriptionId
		subscription, err := endpoint.provider.Subscribe(control.ServiceId, control.Mode, func(updateCtx context.Context, update ServiceProviderUpdate) {
			_ = publish(updateCtx, subscriptionId, update)
		})
		if err != nil {
			endpoint.mu.Unlock()
			return nil, err
		}
		endpoint.subscriptions[subscriptionId] = subscription
		endpoint.mu.Unlock()
		snapshot, err := json.Marshal(subscription.Snapshot())
		if err != nil {
			return nil, err
		}
		if err := subscription.Activate(); err != nil {
			return nil, err
		}
		return snapshot, nil
	case controlUnsubscribe:
		endpoint.mu.Lock()
		subscription, exists := endpoint.subscriptions[control.SubscriptionId]
		delete(endpoint.subscriptions, control.SubscriptionId)
		endpoint.mu.Unlock()
		if !exists {
			return nil, errors.New("Service subscription was not found")
		}
		return nil, subscription.Close(ctx)
	}
	return nil, fmt.Errorf("unknown control call %q", control.Type)
}

func (endpoint *remoteServiceEndpoint) Dispose() {
	endpoint.mu.Lock()
	if endpoint.disposed {
		endpoint.mu.Unlock()
		return
	}
	endpoint.disposed = true
	subscriptions := endpoint.subscriptions
	endpoint.subscriptions = map[string]ServiceSubscription{}
	endpoint.mu.Unlock()
	for _, subscription := range subscriptions {
		_ = subscription.Close(context.Background())
	}
}

// NewLoopbackTransport connects a provider directly to a binding without
// changing remote service semantics (upstream createLoopbackServiceTransport).
// Values are shared, not copied; use NewJSONCopyTransport for isolation.
func NewLoopbackTransport(provider *RemoteServiceProvider) RemoteServiceTransport {
	return loopbackTransport{provider: provider}
}

type loopbackTransport struct{ provider *RemoteServiceProvider }

func (transport loopbackTransport) Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error) {
	return transport.provider.Invoke(ctx, call)
}

func (transport loopbackTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	return transport.provider.Subscribe(serviceId, mode, listener)
}

// NewJSONCopyTransport connects a binding to an endpoint through JSON
// serialization of every call, result, snapshot and update, in memory. It is
// the local stand-in for a framed connection: subscription control goes
// through the endpoint's $chord.service calls, and updates published before
// the consumer activates are buffered in order.
func NewJSONCopyTransport(endpoint RemoteServiceEndpoint) RemoteServiceTransport {
	return &jsonCopyTransport{endpoint: endpoint, subscriptions: map[string]*jsonCopySubscription{}}
}

type jsonCopyTransport struct {
	endpoint      RemoteServiceEndpoint
	nextId        atomic.Int64
	mu            sync.Mutex
	subscriptions map[string]*jsonCopySubscription
}

type jsonCopySubscription struct {
	transport *jsonCopyTransport
	id        string
	listener  UpdateListener
	snapshot  ServiceSubscriptionSnapshot
	mu        sync.Mutex
	buffer    []bufferedUpdate
	active    bool
	closed    bool
}

func jsonCopy[T any](value T) (T, error) {
	var copied T
	encoded, err := json.Marshal(value)
	if err != nil {
		return copied, err
	}
	err = json.Unmarshal(encoded, &copied)
	return copied, err
}

func (transport *jsonCopyTransport) Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error) {
	copied, err := jsonCopy(call)
	if err != nil {
		return nil, err
	}
	result, err := transport.endpoint.Invoke(ctx, copied, transport.publish)
	if err != nil || result == nil {
		return nil, err
	}
	return append(json.RawMessage(nil), result...), nil
}

func (transport *jsonCopyTransport) publish(ctx context.Context, subscriptionId string, update ServiceProviderUpdate) error {
	encoded, err := json.Marshal(update)
	if err != nil {
		return err
	}
	var copied ServiceProviderUpdate
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return err
	}
	transport.mu.Lock()
	subscription := transport.subscriptions[subscriptionId]
	transport.mu.Unlock()
	if subscription == nil {
		return nil
	}
	subscription.mu.Lock()
	if subscription.closed {
		subscription.mu.Unlock()
		return nil
	}
	if !subscription.active {
		subscription.buffer = append(subscription.buffer, bufferedUpdate{update: copied, ctx: ctx})
		subscription.mu.Unlock()
		return nil
	}
	subscription.mu.Unlock()
	return callUpdate(subscription.listener, ctx, copied)
}

func (transport *jsonCopyTransport) Subscribe(ctx context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	id := fmt.Sprintf("s%d", transport.nextId.Add(1))
	subscription := &jsonCopySubscription{transport: transport, id: id, listener: listener}
	transport.mu.Lock()
	transport.subscriptions[id] = subscription
	transport.mu.Unlock()
	result, err := transport.endpoint.Invoke(ctx, CreateServiceSubscribeCall(id, serviceId, mode), transport.publish)
	if err != nil {
		transport.remove(id)
		return nil, err
	}
	if err := json.Unmarshal(result, &subscription.snapshot); err != nil {
		transport.remove(id)
		return nil, err
	}
	return subscription, nil
}

func (transport *jsonCopyTransport) remove(id string) {
	transport.mu.Lock()
	delete(transport.subscriptions, id)
	transport.mu.Unlock()
}

func (subscription *jsonCopySubscription) Snapshot() ServiceSubscriptionSnapshot {
	return subscription.snapshot
}

func (subscription *jsonCopySubscription) Activate() error {
	var errs []error
	for {
		subscription.mu.Lock()
		if subscription.closed || (subscription.active && len(subscription.buffer) == 0) {
			subscription.active = true
			subscription.mu.Unlock()
			return joinErrors(errs)
		}
		if len(subscription.buffer) == 0 {
			subscription.active = true
			subscription.mu.Unlock()
			return joinErrors(errs)
		}
		entry := subscription.buffer[0]
		subscription.buffer = subscription.buffer[1:]
		subscription.mu.Unlock()
		if err := callUpdate(subscription.listener, entry.ctx, entry.update); err != nil {
			errs = append(errs, err)
		}
	}
}

func (subscription *jsonCopySubscription) Close(ctx context.Context) error {
	subscription.mu.Lock()
	if subscription.closed {
		subscription.mu.Unlock()
		return nil
	}
	subscription.closed = true
	subscription.buffer = nil
	subscription.mu.Unlock()
	subscription.transport.remove(subscription.id)
	_, err := subscription.transport.endpoint.Invoke(ctx, CreateServiceUnsubscribeCall(subscription.id), subscription.transport.publish)
	return err
}
