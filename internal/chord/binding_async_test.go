package chord

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
)

type closeGateTransport struct {
	RemoteServiceTransport
	release <-chan struct{}
}

func (transport closeGateTransport) Subscribe(ctx context.Context, id string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	subscription, err := transport.RemoteServiceTransport.Subscribe(ctx, id, mode, listener)
	if err != nil {
		return nil, err
	}
	return closeGateSubscription{ServiceSubscription: subscription, release: transport.release}, nil
}

type closeGateSubscription struct {
	ServiceSubscription
	release <-chan struct{}
}

func (subscription closeGateSubscription) Close(ctx context.Context) error {
	<-subscription.release
	return subscription.ServiceSubscription.Close(ctx)
}

// upstream: packages/chord/src/services/consumer.ts:521-551 fences before its first await and joins completion without applying caller cancellation to the join.
func TestBeginRebindFencesBeforeCompletionAndJoinsCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fixture := newRemoteFixture(t, SingletonService(counterDefinition))
		counter := newCounter(t)
		if err := Provide[Counter](fixture.provider, counterDefinition, counter); err != nil {
			t.Fatal(err)
		}
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer func() {
			unblock()
			if err := fixture.binding.Dispose(context.Background()); err != nil {
				t.Error(err)
			}
			fixture.endpoint.Dispose()
			if err := fixture.provider.Dispose(); err != nil {
				t.Error(err)
			}
		}()
		fixture.binding.transport = closeGateTransport{RemoteServiceTransport: NewJSONCopyTransport(fixture.endpoint), release: release}
		remote, err := UseRemote(fixture.binding, counterDefinition)
		if err != nil {
			t.Fatal(err)
		}
		replica, err := remote.State("state")
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.binding.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, present := replica.Value(); !present {
			t.Fatal("state did not hydrate")
		}
		ctx, cancel := context.WithCancel(t.Context())
		operation := fixture.binding.BeginRebind(ctx, false)
		if _, present := replica.Value(); present {
			t.Fatal("BeginRebind returned before invalidating state")
		}
		cancel()
		finished := make(chan error, 1)
		go func() { finished <- operation.Wait() }()
		synctest.Wait()
		select {
		case err := <-finished:
			t.Fatalf("rebind joined early after cancellation: %v", err)
		default:
		}
		unblock()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := fixture.binding.Dispose(context.Background()); err != nil {
			t.Fatal(err)
		}
		fixture.endpoint.Dispose()
		if err := fixture.provider.Dispose(); err != nil {
			t.Fatal(err)
		}
	})
}

type readinessRaceTransport struct {
	firstStarted chan struct{}
	release      <-chan struct{}
	failure      error
}

func (*readinessRaceTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, errors.New("unused invocation")
}

func (transport *readinessRaceTransport) Subscribe(_ context.Context, id string, _ ServiceMode, _ UpdateListener) (ServiceSubscription, error) {
	if id == "first" {
		close(transport.firstStarted)
		<-transport.release
		return nil, errors.New("first settled after release")
	}
	return nil, transport.failure
}

// upstream: packages/chord/src/services/consumer.ts:501-518 awaits Promise.all, not the first service followed by the second.
func TestBindingReadyRejectsWithoutWaitingForEarlierPendingService(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		failure := errors.New("second service rejected")
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		transport := &readinessRaceTransport{firstStarted: make(chan struct{}), release: release, failure: failure}
		binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("first", "second"), Transport: transport, OnError: func(error) {}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := binding.Use("first"); err != nil {
			t.Fatal(err)
		}
		if _, err := binding.Use("second"); err != nil {
			t.Fatal(err)
		}
		<-transport.firstStarted
		finished := make(chan error, 1)
		go func() { finished <- binding.Ready(t.Context()) }()
		synctest.Wait()
		select {
		case got := <-finished:
			if got != failure { //nolint:errorlint // Preserve the exact rejection, not a wrapped match.
				t.Fatalf("ready error=%v, want exact second error", got)
			}
		default:
			t.Error("Ready waited for the earlier pending service")
		}
		unblock()
		if err := binding.Dispose(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

type closeFailTransport struct {
	RemoteServiceTransport
	failure error
}

func (transport closeFailTransport) Subscribe(ctx context.Context, id string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	subscription, err := transport.RemoteServiceTransport.Subscribe(ctx, id, mode, listener)
	if err != nil {
		return nil, err
	}
	return closeFailSubscription{ServiceSubscription: subscription, failure: transport.failure}, nil
}

type closeFailSubscription struct {
	ServiceSubscription
	failure error
}

func (subscription closeFailSubscription) Close(ctx context.Context) error {
	return errors.Join(subscription.ServiceSubscription.Close(ctx), subscription.failure)
}

// upstream: packages/chord/src/services/consumer.ts:541-547 (rebind) and :569-573 (dispose) throw AggregateError(errors, "Failed to rebind services" / "Failed to dispose services") even for a single failure. The message excludes the causes, which stay reachable as AggregateError.errors.
func TestBindingRebindAndDisposeRejectWithAggregateErrorMessageOnly(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		run     func(*RemoteServiceBinding) error
	}{
		{"Rebind", "Failed to rebind services", func(b *RemoteServiceBinding) error { return b.Rebind(context.Background(), false) }},
		{"BeginRebind", "Failed to rebind services", func(b *RemoteServiceBinding) error { return b.BeginRebind(context.Background(), false).Wait() }},
		{"Dispose", "Failed to dispose services", func(b *RemoteServiceBinding) error { return b.Dispose(context.Background()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRemoteFixture(t, SingletonService(counterDefinition))
			if err := Provide[Counter](fixture.provider, counterDefinition, newCounter(t)); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("transport down")
			fixture.binding.transport = closeFailTransport{RemoteServiceTransport: NewJSONCopyTransport(fixture.endpoint), failure: failure}
			if _, err := UseRemote(fixture.binding, counterDefinition); err != nil {
				t.Fatal(err)
			}
			if err := fixture.binding.Ready(t.Context()); err != nil {
				t.Fatal(err)
			}
			err := test.run(fixture.binding)
			var aggregate *AggregateError
			if !errors.As(err, &aggregate) {
				t.Fatalf("error = %#v, want *AggregateError", err)
			}
			if err.Error() != test.message {
				t.Fatalf("message = %q, want %q", err.Error(), test.message)
			}
			if len(aggregate.Errors) != 1 || !errors.Is(err, failure) {
				t.Fatalf("causes = %v, want the transport failure", aggregate.Errors)
			}
			fixture.endpoint.Dispose()
			_ = fixture.provider.Dispose()
		})
	}
}

// upstream: state.ts:230-238 (MutableReplicatedStateImpl.change) throws AggregateError(errors, "Replicated state listeners failed") for more than one exact publication listener failure; the message excludes the causes. Subscriber callbacks are not among them since 1.0.0: their failures are reported (state-delivery.test.ts).
func TestReplicatedStateListenerFailuresAggregateMessageOnly(t *testing.T) {
	state := mustState(t)
	first, second := errors.New("first listener"), errors.New("second listener")
	for _, failure := range []error{first, second} {
		stop := state.core.subscribeOps(func(context.Context, []Op, int) { panic(failure) })
		defer stop()
	}
	err := state.Change(context.Background(), func(value *counterState) error { value.Count++; return nil })
	var aggregate *AggregateError
	if !errors.As(err, &aggregate) || err.Error() != "Replicated state listeners failed" || !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("error = %#v, want AggregateError with both listener failures", err)
	}
}

// facets/host.ts:646 passes `serviceIds.map((id) => ({ id }))` to RemoteServiceSource.open and consumer.ts:459-461 reads `options.services.map(({ id }) => id)`: each service is an `{ id }` object, in the requested order, and a repeated id is rejected.
func TestServiceReferencesAreIdObjectsInOrderAndDuplicatesAreRejected(t *testing.T) {
	got := ServiceIDs("a", "b")
	if len(got) != 2 || got[0].Id() != "a" || got[1].Id() != "b" {
		t.Fatalf("ServiceIDs = %+v", got)
	}
	if _, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("a", "a")}); err == nil || err.Error() != "Remote service binding has duplicate service IDs" {
		t.Fatalf("duplicate ids error = %v", err)
	}
}
