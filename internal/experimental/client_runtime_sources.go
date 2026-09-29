package experimental

// Ports packages/coding-agent/src/experimental/services/connection.ts (real client/binding adapters).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ClientServiceSourceOptions selects an error sink and an optional request/subscription decorator. Lifecycle state always belongs to the original client.
type ClientServiceSourceOptions struct {
	OnError         func(error)
	TransportClient client.ServiceTransportClient
}

type serviceClientAdapter struct{ client *client.Client }

func (adapter serviceClientAdapter) ServerID() string { return adapter.client.ServerId() }
func (adapter serviceClientAdapter) ConnectionState() string {
	return string(adapter.client.ConnectionState())
}
func (adapter serviceClientAdapter) Attachment() *services.SessionTarget {
	return sourceAttachment(adapter.client.Attachment())
}
func sourceAttachment(attachment *protocol.SessionTarget) *services.SessionTarget {
	if attachment == nil {
		return nil
	}
	return &services.SessionTarget{ServerID: attachment.ServerId, SessionID: attachment.SessionId, AttachmentID: attachment.AttachmentId}
}
func sourceTarget(target services.ServiceTarget) protocol.RpcTarget {
	if target.SessionID == nil {
		return protocol.ServerTarget{ServerId: target.ServerID}
	}
	attachment := ""
	if target.AttachmentID != nil {
		attachment = *target.AttachmentID
	}
	return protocol.SessionTarget{ServerId: target.ServerID, SessionId: *target.SessionID, AttachmentId: attachment}
}
func sourceCatalogue(entries []services.ServiceCatalogueEntry) []chord.ServiceCatalogueEntry {
	result := make([]chord.ServiceCatalogueEntry, len(entries))
	for i, entry := range entries {
		result[i] = chord.ServiceCatalogueEntry{ServiceId: entry.ServiceID, Mode: chord.ServiceMode(entry.Mode)}
	}
	return result
}
func (adapter serviceClientAdapter) ServiceCatalogue(ctx context.Context, target services.ServiceTarget, complete func([]services.ServiceCatalogueEntry, error)) {
	adapter.client.ServiceCatalogueCallback(ctx, sourceTarget(target), func(entries []chord.ServiceCatalogueEntry, err error) {
		if err != nil {
			complete(nil, err)
			return
		}
		result := make([]services.ServiceCatalogueEntry, len(entries))
		for i, entry := range entries {
			result[i] = services.ServiceCatalogueEntry{ServiceID: entry.ServiceId, Mode: string(entry.Mode)}
		}
		complete(result, nil)
	})
}
func (adapter serviceClientAdapter) OnConnectionStateChange(listener func(string, error)) func() {
	stop, err := adapter.client.OnConnectionStateChange(client.NewConnectionStateChangeListener(func(change client.ConnectionStateChange) { listener(string(change.State), change.Error) }))
	if err != nil {
		panic(err)
	}
	return stop
}
func (adapter serviceClientAdapter) OnAttachmentChange(listener func(*services.SessionTarget)) func() {
	stop, err := adapter.client.OnAttachmentChange(client.NewAttachmentChangeListener(func(target *protocol.SessionTarget) { listener(sourceAttachment(target)) }))
	if err != nil {
		panic(err)
	}
	return stop
}

type clientBindingAdapter struct {
	binding  *chord.RemoteServiceBinding
	mu       sync.Mutex
	disposed bool
	work     sync.WaitGroup
}

func (binding *clientBindingAdapter) Use(service services.Service) (any, error) {
	if service.Local {
		return nil, errors.New("Local services cannot cross a remote binding")
	}
	return binding.binding.Use(service.ID)
}
func (binding *clientBindingAdapter) Observe(service services.Service, handler func(context.Context, any) error) (func(), error) {
	if service.Local {
		return nil, errors.New("Local services cannot cross a remote binding")
	}
	return binding.binding.Observe(service.ID, func(ctx context.Context, service *chord.RemoteService) error { return handler(ctx, service) })
}
func (binding *clientBindingAdapter) start(operation func() *chord.BindingWait, complete func(error)) {
	binding.mu.Lock()
	if binding.disposed {
		binding.mu.Unlock()
		complete(errors.New("Remote service binding is disposed"))
		return
	}
	binding.work.Add(1)
	binding.mu.Unlock()
	waiting := operation()
	if settled, err := waiting.Settled(); settled {
		defer binding.work.Done()
		complete(err)
		return
	}
	go func() { defer binding.work.Done(); complete(waiting.Wait()) }()
}
func (binding *clientBindingAdapter) Ready(ctx context.Context, complete func(error)) {
	binding.start(func() *chord.BindingWait { return binding.binding.BeginReady(ctx) }, complete)
}
func (binding *clientBindingAdapter) Rebind(ctx context.Context, bound bool, complete func(error)) {
	binding.start(func() *chord.BindingWait { return binding.binding.BeginRebind(ctx, bound) }, complete)
}
func (binding *clientBindingAdapter) Dispose(ctx context.Context) error {
	binding.mu.Lock()
	binding.disposed = true
	binding.mu.Unlock()
	err := binding.binding.Dispose(ctx)
	binding.work.Wait()
	return err
}

func clientSourceOptions(peer *client.Client, options ClientServiceSourceOptions) services.ServiceSourceOptions {
	transportClient := options.TransportClient
	if transportClient == nil {
		transportClient = peer
	}
	return services.ServiceSourceOptions{OnError: options.OnError, NewBinding: func(options services.RemoteServiceBindingOptions) services.RemoteServiceBinding {
		ids := make([]string, len(options.Services))
		for i, service := range options.Services {
			ids[i] = service.ID
		}
		transport := client.CreateClientServiceTransport(transportClient, func() protocol.RpcTarget {
			target := options.GetTarget()
			if target == nil {
				return nil
			}
			return sourceTarget(*target)
		})
		binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: ids, Transport: initiatedClientTransport{transport}, Unbound: !options.Bound, OnError: options.OnError, AssertAccess: options.AssertAccess})
		if err != nil {
			panic(err)
		}
		return &clientBindingAdapter{binding: binding}
	}}
}

type initiatedClientTransport struct{ chord.RemoteServiceTransport }

func (transport initiatedClientTransport) BeginInvoke(ctx context.Context, call chord.ServiceCall) (*chord.ServiceInvocation, error) {
	initiating, ok := transport.RemoteServiceTransport.(chord.InitiatingServiceTransport)
	if !ok {
		return nil, errors.New("Client transport does not expose invocation admission")
	}
	operation, err := initiating.BeginInvoke(ctx, call)
	if err != nil {
		return nil, err
	}
	extension.CallInitiated(ctx)
	return operation, nil
}
func (transport initiatedClientTransport) Invoke(ctx context.Context, call chord.ServiceCall) (json.RawMessage, error) {
	operation, err := transport.BeginInvoke(ctx, call)
	if err != nil {
		return nil, err
	}
	return operation.Wait(context.Background())
}

type sourceStateReplica[T comparable] struct{ source *services.SourceState[T] }

func (replica sourceStateReplica[T]) Value() *T { return new(replica.source.Value()) }
func (replica sourceStateReplica[T]) Subscribe(listener func(*T, context.Context, pico3.ReplicatedStateDelivery)) (func(), error) {
	return replica.source.Subscribe(func(value T, ctx context.Context, delivery services.ReplicatedStateDelivery) {
		listener(&value, ctx, pico3.ReplicatedStateDelivery{Kind: delivery.Kind, Sequence: delivery.Sequence})
	}), nil
}

type runtimeServiceBinding struct {
	binding *services.RoutedServiceBinding
}

func (binding runtimeServiceBinding) Use(id string) (*chord.RemoteService, error) {
	service, err := binding.binding.Use(services.Service{ID: id})
	if err != nil {
		return nil, err
	}
	remote, ok := service.(*chord.RemoteService)
	if !ok {
		return nil, fmt.Errorf("Service %s did not return a remote facade", id)
	}
	return remote, nil
}
func (binding runtimeServiceBinding) Observe(id string, handler func(context.Context, *chord.RemoteService) error) (func(), error) {
	return binding.binding.Observe(services.Service{ID: id}, func(ctx context.Context, value any) error {
		service, ok := value.(*chord.RemoteService)
		if !ok {
			return fmt.Errorf("Service %s did not return a remote facade", id)
		}
		return handler(ctx, service)
	})
}
func (binding runtimeServiceBinding) BeginReady(ctx context.Context) func() error {
	return binding.binding.BeginReady(ctx)
}
func (binding runtimeServiceBinding) Ready(ctx context.Context) error {
	return binding.binding.Ready(ctx)
}
func (binding runtimeServiceBinding) Dispose(ctx context.Context) error {
	return binding.binding.Dispose(ctx)
}
func runtimeBindingOptions(options chord.RemoteServiceSourceOpenOptions) services.ServiceBindingOptions {
	list := make([]services.Service, len(options.Services))
	for i, id := range options.Services {
		list[i] = services.Service{ID: id}
	}
	return services.ServiceBindingOptions{Services: list, AssertAccess: options.AssertAccess, OnError: options.OnError}
}

// ClientServerServiceSource adapts the real client to the structural source consumed by presentations and FacetHost.
type ClientServerServiceSource struct{ source *services.ServerServiceSource }

func NewClientServerServiceSource(peer *client.Client, options ClientServiceSourceOptions) (result *ClientServerServiceSource, err error) {
	defer recoverSourceError(&err)
	result = &ClientServerServiceSource{source: services.CreateServerServiceSource(serviceClientAdapter{peer}, clientSourceOptions(peer, options))}
	return result, nil
}
func (source *ClientServerServiceSource) AcceptsUnavailableServices() bool {
	return source.source.AcceptsUnavailableServices()
}
func (source *ClientServerServiceSource) Connection() pico3.ReplicatedStateOf[*services.ServerConnectionState] {
	return sourceStateReplica[services.ServerConnectionState]{source: source.source.Connection}
}
func (source *ClientServerServiceSource) Catalogue(ctx context.Context) ([]chord.ServiceCatalogueEntry, error) {
	entries, err := source.source.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	return sourceCatalogue(entries), nil
}
func (source *ClientServerServiceSource) Open(options chord.RemoteServiceSourceOpenOptions) (result chord.RemoteServices, err error) {
	defer recoverSourceError(&err)
	binding, err := source.source.Open(runtimeBindingOptions(options))
	if err != nil {
		return nil, err
	}
	return runtimeServiceBinding{binding}, nil
}
func (source *ClientServerServiceSource) Dispose(ctx context.Context) error {
	return source.source.Dispose(ctx)
}

// ClientSessionServiceSource retains attachment generation fencing while adapting the real client to a structural Chord source.
type ClientSessionServiceSource struct {
	source *services.SessionServiceSource
}

func NewClientSessionServiceSource(peer *client.Client, options ClientServiceSourceOptions) (result *ClientSessionServiceSource, err error) {
	defer recoverSourceError(&err)
	result = &ClientSessionServiceSource{source: services.CreateSessionServiceSource(serviceClientAdapter{peer}, clientSourceOptions(peer, options))}
	return result, nil
}
func (source *ClientSessionServiceSource) AcceptsUnavailableServices() bool {
	return source.source.AcceptsUnavailableServices()
}
func (source *ClientSessionServiceSource) Attachment() pico3.ReplicatedStateOf[*services.SessionAttachmentState] {
	return sourceStateReplica[services.SessionAttachmentState]{source: source.source.Attachment}
}
func (source *ClientSessionServiceSource) Catalogue(ctx context.Context) ([]chord.ServiceCatalogueEntry, error) {
	entries, err := source.source.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	return sourceCatalogue(entries), nil
}
func (source *ClientSessionServiceSource) Open(options chord.RemoteServiceSourceOpenOptions) (result chord.RemoteServices, err error) {
	defer recoverSourceError(&err)
	binding, err := source.source.Open(runtimeBindingOptions(options))
	if err != nil {
		return nil, err
	}
	return runtimeServiceBinding{binding}, nil
}
func (source *ClientSessionServiceSource) WhenAttached(ctx context.Context, id string) error {
	return source.source.WhenAttached(id, ctx)
}
func (source *ClientSessionServiceSource) WhenDetached(ctx context.Context) error {
	return source.source.WhenDetached(ctx)
}
func (source *ClientSessionServiceSource) Dispose(ctx context.Context) error {
	return source.source.Dispose(ctx)
}

func recoverSourceError(err *error) {
	if failure := recover(); failure != nil {
		if cause, ok := failure.(error); ok {
			*err = cause
		} else {
			*err = fmt.Errorf("%v", failure)
		}
	}
}
