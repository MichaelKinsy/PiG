package chord

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

func TestReviewReentrantSubscribeHydratesSynchronously(t *testing.T) {
	counter := newCounter(t)
	var events []string
	_, err := counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		if info.Kind != DeliveryUpdate {
			return
		}
		events = append(events, "first update")
		_, err := counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
			events = append(events, "late "+info.Kind)
		})
		if err != nil {
			t.Error(err)
		}
		events = append(events, "after subscribe")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		if info.Kind == DeliveryUpdate {
			events = append(events, "second update")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	want := []string{"first update", "late hydrate", "after subscribe", "second update"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, upstream = %v", events, want)
	}
}

func TestReviewReentrantProviderDisposeDeliversUnavailable(t *testing.T) {
	provider, err := NewRemoteServiceProvider(SingletonService(counterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	counter := newCounter(t)
	if err := Provide[Counter](provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	var first, second []string
	a, err := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
		first = append(first, update.Type)
		if update.Type == UpdateState {
			if err := provider.Dispose(); err != nil {
				t.Error(err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
		second = append(second, update.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := b.Activate(); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, []string{UpdateState, UpdateUnavailable}) || !reflect.DeepEqual(second, []string{UpdateUnavailable}) {
		t.Fatalf("first=%v second=%v; upstream first=[state unavailable] second=[unavailable]", first, second)
	}
}

func TestReviewRetainedFacetImplementationRetargetsAndRevokes(t *testing.T) {
	ctx := context.Background()
	var ref *ServiceRef[Counter]
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		ref, err = UseService(env, counterDefinition)
		return err
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", &eventLog{}, 0, nil), consumer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(ctx); err != nil {
			t.Error(err)
		}
	})
	held, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	add := held.Add
	if err := host.Reload(ctx, []Facet{counterFacet(t, "counter", &eventLog{}, 100, nil)}); err != nil {
		t.Fatal(err)
	}
	if got, err := add(ctx, 1, "retained"); err != nil || got != 101 {
		t.Errorf("retained method after reload = %d, %v; upstream = 101", got, err)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := add(ctx, 1, "after disposal"); err == nil {
		t.Errorf("retained method after disposal succeeded with %d; upstream rejects revoked facet access", got)
	}
}

type reviewPausedTransport struct {
	RemoteServiceTransport
	entered chan struct{}
	release chan struct{}
}

type reviewPausedSubscription struct {
	ServiceSubscription
	entered chan struct{}
	release chan struct{}
}

func (transport reviewPausedTransport) Subscribe(ctx context.Context, id string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	sub, err := transport.RemoteServiceTransport.Subscribe(ctx, id, mode, listener)
	if err != nil {
		return nil, err
	}
	return reviewPausedSubscription{ServiceSubscription: sub, entered: transport.entered, release: transport.release}, nil
}

func (sub reviewPausedSubscription) Snapshot() ServiceSubscriptionSnapshot {
	snapshot := sub.ServiceSubscription.Snapshot()
	close(sub.entered)
	<-sub.release
	return snapshot
}

func TestReviewUnbindFencesSnapshotAlreadyBeingInstalled(t *testing.T) {
	ctx := context.Background()
	provider, err := NewRemoteServiceProvider(SingletonService(counterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if err := Provide[Counter](provider, counterDefinition, newCounter(t)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:  []string{counterDefinition.Id()},
		Transport: reviewPausedTransport{RemoteServiceTransport: NewLoopbackTransport(provider), entered: entered, release: release},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	binding.mu.Lock()
	oldStart := binding.singletons[counterDefinition.Id()].starting
	binding.mu.Unlock()
	if err := binding.Rebind(ctx, false); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := oldStart.wait(ctx); err != nil {
		t.Fatal(err)
	}
	_, hydrated := replica.Value()
	if err := binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if err := provider.Dispose(); err != nil {
		t.Fatal(err)
	}
	if hydrated {
		t.Fatal("stale snapshot rehydrated replica after unbind completed")
	}
}

func TestReviewFacetSetupPanicCleansOwnedResources(t *testing.T) {
	cleaned := false
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		_, _ = CreateFacetHost(context.Background(), FacetOptions{Facets: []Facet{{Id: "broken", Setup: func(env *FacetEnvironment) error {
			if err := env.Own(func(context.Context) error { cleaned = true; return nil }); err != nil {
				return err
			}
			panic("setup failed")
		}}}})
	}()
	if escaped != nil || !cleaned {
		t.Fatalf("setup panic escaped=%v cleanup ran=%v; upstream rejects startup after running owned cleanup", escaped, cleaned)
	}
}

func TestReviewFacetCleanupPanicStillRunsRemainingEffects(t *testing.T) {
	ctx := context.Background()
	cleaned := false
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{{Id: "broken", Setup: func(env *FacetEnvironment) error {
		if err := env.Own(func(context.Context) error { cleaned = true; return nil }); err != nil {
			return err
		}
		return env.Own(func(context.Context) error { panic("cleanup failed") })
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		err = host.Dispose(ctx)
	}()
	if escaped != nil || !cleaned || err == nil {
		t.Fatalf("cleanup panic escaped=%v remaining cleanup ran=%v returned error=%v; upstream runs all effects then rejects disposal", escaped, cleaned, err)
	}
}
