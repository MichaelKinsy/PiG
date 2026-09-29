package experimental

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type experimentalBindingScope struct {
	chord.RemoteServices
	activationDone chan struct{}
	activationErr  error
	closeSource    func(context.Context) error
}

func newExperimentalBindingScope(t *testing.T, scope chord.RemoteServices, closeSource func(context.Context) error, onError func(error)) *experimentalBindingScope {
	t.Helper()
	binding := &experimentalBindingScope{RemoteServices: scope, activationDone: make(chan struct{}), closeSource: closeSource}
	// The real source starts invalidation before this constructor returns, just as services.ready() starts its synchronous prefix upstream.
	wait := scope.(runtimeServiceBinding).BeginReady(context.Background())
	go func() {
		defer close(binding.activationDone)
		binding.activationErr = wait()
		if binding.activationErr != nil && onError != nil {
			onError(binding.activationErr)
		}
	}()
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return binding
}
func (binding *experimentalBindingScope) Ready(ctx context.Context) error {
	<-binding.activationDone
	if binding.activationErr != nil {
		return binding.activationErr
	}
	return binding.RemoteServices.Ready(ctx)
}
func (binding *experimentalBindingScope) Dispose(ctx context.Context) error {
	var failures [2]error
	var work sync.WaitGroup
	work.Go(func() { failures[0] = binding.RemoteServices.Dispose(ctx) })
	work.Go(func() { failures[1] = binding.closeSource(ctx) })
	work.Wait()
	<-binding.activationDone
	if failures[0] == nil {
		return failures[1]
	}
	if failures[1] == nil {
		return failures[0]
	}
	return errors.Join(failures[:]...)
}

type experimentalServerServiceBinding struct {
	ServerServiceSource
	*experimentalBindingScope
}

func (binding *experimentalServerServiceBinding) Dispose(ctx context.Context) error {
	return binding.experimentalBindingScope.Dispose(ctx)
}

// upstream: packages/coding-agent/test/experimental-service-binding.ts:17-47.
func createServerServiceBinding(t *testing.T, peer *client.Client, ids []string, options ClientServiceSourceOptions) *experimentalServerServiceBinding {
	t.Helper()
	source, err := NewClientServerServiceSource(peer, options)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := source.Open(chord.RemoteServiceSourceOpenOptions{Services: ids, OnError: options.OnError})
	if err != nil {
		t.Fatal(err)
	}
	return &experimentalServerServiceBinding{ServerServiceSource: source, experimentalBindingScope: newExperimentalBindingScope(t, scope, source.Dispose, options.OnError)}
}

type experimentalSessionServiceBinding struct {
	SessionServiceSource
	*experimentalBindingScope
}

func (binding *experimentalSessionServiceBinding) Ready(ctx context.Context) error {
	<-binding.activationDone
	if binding.activationErr != nil {
		return binding.activationErr
	}
	attachment := binding.Attachment().Value()
	if attachment == nil || attachment.Status == "detached" {
		return binding.WhenDetached(ctx)
	}
	return binding.WhenAttached(ctx, attachment.SessionID)
}
func (binding *experimentalSessionServiceBinding) Dispose(ctx context.Context) error {
	return binding.experimentalBindingScope.Dispose(ctx)
}

// upstream: packages/coding-agent/test/experimental-service-binding.ts:51-84.
func createSessionServiceBinding(t *testing.T, peer *client.Client, ids []string, options ClientServiceSourceOptions) *experimentalSessionServiceBinding {
	t.Helper()
	source, err := NewClientSessionServiceSource(peer, options)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := source.Open(chord.RemoteServiceSourceOpenOptions{Services: ids, OnError: options.OnError})
	if err != nil {
		t.Fatal(err)
	}
	return &experimentalSessionServiceBinding{SessionServiceSource: source, experimentalBindingScope: newExperimentalBindingScope(t, scope, source.Dispose, options.OnError)}
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:480-533. Release must run before disposing the source that owns the delayed subscription.
func delaySessionServiceSubscription(t *testing.T, peer *client.Client, sessionId, serviceId string) (client.ServiceTransportClient, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return &delayedSessionSubscription{ServiceTransportClient: peer, sessionId: sessionId, serviceId: serviceId, release: release}, unblock
}

type delayedSessionSubscription struct {
	client.ServiceTransportClient
	sessionId, serviceId string
	release              <-chan struct{}
}

func (decorator *delayedSessionSubscription) BeginInvoke(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall) (*chord.ServiceInvocation, error) {
	initiating, ok := decorator.ServiceTransportClient.(client.InitiatingServiceTransportClient)
	if !ok {
		return nil, errors.New("Decorated client does not expose invocation admission")
	}
	return initiating.BeginInvoke(ctx, target, call)
}

func (decorator *delayedSessionSubscription) SubscribeService(ctx context.Context, target protocol.RpcTarget, serviceId string, mode chord.ServiceMode, listener func(chord.ServiceProviderUpdate) error) (*client.ServiceSubscription, error) {
	var sessionId string
	switch target := target.(type) {
	case protocol.SessionTarget:
		sessionId = target.SessionId
	case *protocol.SessionTarget:
		if target != nil {
			sessionId = target.SessionId
		}
	}
	if sessionId == decorator.sessionId && serviceId == decorator.serviceId {
		<-decorator.release
	}
	return decorator.ServiceTransportClient.SubscribeService(ctx, target, serviceId, mode, listener)
}

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:55-66.
func attachSession(t *testing.T, peer *client.Client, id string) {
	t.Helper()
	binding := createServerServiceBinding(t, peer, []string{services.SessionManagementDefinition.Id()}, ClientServiceSourceOptions{})
	if err := binding.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	management, err := chord.UseRemoteClient(binding, services.SessionManagementDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := management.Attach(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := binding.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
}
