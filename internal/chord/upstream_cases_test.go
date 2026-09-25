package chord

// Go counterparts of selected cases in upstream packages/chord/test/
// {services,facet-loader}.test.ts. Each test names its upstream case.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// services.test.ts: "does not publish when a transaction restores the prior value"
func TestUpstreamRestoredValueDoesNotPublish(t *testing.T) {
	counter := newCounter(t)
	rec := newRecorder()
	if _, err := counter.state.Subscribe(rec.listen); err != nil {
		t.Fatal(err)
	}
	if err := counter.state.Change(context.Background(), func(draft *counterState) error {
		draft.Count = 9
		draft.Count = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); len(got) != 1 || counter.state.Sequence() != 0 {
		t.Fatalf("restored value published: %+v", got)
	}
}

// services.test.ts: "delivers active subscriber updates before reporting listener failures"
func TestUpstreamDeliversToAllSubscribersBeforeReportingFailure(t *testing.T) {
	provider, err := NewRemoteServiceProvider(SingletonService(counterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	counter := newCounter(t)
	if err := Provide[Counter](provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("first subscriber failed")
	var secondSaw []int
	first, _ := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(context.Context, ServiceProviderUpdate) { panic(failure) })
	second, _ := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
		secondSaw = append(secondSaw, update.Sequence)
	})
	if err := first.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := second.Activate(); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(context.Background(), 1, "x"); !errors.Is(err, failure) {
		t.Fatalf("publication error = %v", err)
	}
	if !reflect.DeepEqual(secondSaw, []int{1}) {
		t.Fatalf("second subscriber saw %v", secondSaw)
	}
}

// services.test.ts: "applies provider disposal buffered while subscriptions are starting"
func TestUpstreamProviderDisposalBufferedWhileStarting(t *testing.T) {
	provider, err := NewRemoteServiceProvider(SingletonService(counterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if err := Provide[Counter](provider, counterDefinition, newCounter(t)); err != nil {
		t.Fatal(err)
	}
	var types []string
	subscription, err := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
		types = append(types, update.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Dispose(); err != nil {
		t.Fatal(err)
	}
	if len(types) != 0 {
		t.Fatalf("delivered before activation: %v", types)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(types, []string{UpdateUnavailable}) {
		t.Fatalf("buffered disposal = %v", types)
	}
	if _, err := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(context.Context, ServiceProviderUpdate) {}); err == nil {
		t.Fatal("disposed provider accepted a subscription")
	}
}

// facet-loader.test.ts: "keeps the old generation active when replacement activation fails before cutover"
func TestUpstreamReloadActivationFailureKeepsOldGeneration(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	created := make(chan *counterImpl, 4)
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", log, 1, created)}})
	if err != nil {
		t.Fatal(err)
	}
	<-created
	failing := Facet{Id: "counter", Setup: func(env *FacetEnvironment) error {
		if err := ProvideService[Counter](env, counterDefinition, &counterImpl{state: mustState(t)}); err != nil {
			return err
		}
		if err := env.Own(func(context.Context) error { log.add("dispose candidate"); return nil }); err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error { return errors.New("candidate activation failed") })
	}}
	if err := host.Reload(ctx, []Facet{failing}); err == nil || err.Error() != "candidate activation failed" {
		t.Fatalf("reload = %v", err)
	}
	current, err := Use(host.Services(), counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if current.State().Value().Count != 1 {
		t.Fatalf("old generation replaced: %+v", current.State().Value())
	}
	// The host is still active and reloadable.
	if err := host.Reload(ctx, []Facet{counterFacet(t, "counter", log, 2, created)}); err != nil {
		t.Fatal(err)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"activate counter", "dispose candidate", "activate counter", "dispose counter", "dispose counter"}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// facet-loader.test.ts: "terminates the host when old cleanup fails after cutover"
func TestUpstreamRetirementFailureTerminatesHost(t *testing.T) {
	ctx := context.Background()
	failingOld := Facet{Id: "counter", Setup: func(env *FacetEnvironment) error {
		if err := ProvideService[Counter](env, counterDefinition, &counterImpl{state: mustState(t)}); err != nil {
			return err
		}
		return env.Own(func(context.Context) error { return errors.New("old cleanup failed") })
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{failingOld}})
	if err != nil {
		t.Fatal(err)
	}
	log := &eventLog{}
	err = host.Reload(ctx, []Facet{counterFacet(t, "counter", log, 0, nil)})
	if err == nil || !strings.Contains(err.Error(), "after cutover") || !strings.Contains(err.Error(), "old cleanup failed") {
		t.Fatalf("reload = %v", err)
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, []string{"activate counter", "dispose counter"}) {
		t.Fatalf("replacement was not terminated: %v", got)
	}
	if err := host.Reload(ctx, nil); err == nil {
		t.Fatal("terminated host accepted a reload")
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatalf("dispose after termination = %v", err)
	}
}

// state.ts: change/replace from inside a change callback is rejected, not deadlocked.
func TestUpstreamReentrantChangeIsRejected(t *testing.T) {
	ctx := context.Background()
	counter := newCounter(t)
	var nestedChange, nestedReplace error
	if err := counter.state.Change(ctx, func(draft *counterState) error {
		nestedChange = counter.state.Change(ctx, func(*counterState) error { return nil })
		nestedReplace = counter.state.Replace(ctx, &counterState{})
		draft.Count = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nestedChange == nil || !strings.Contains(nestedChange.Error(), "reentrantly") || nestedReplace == nil {
		t.Fatalf("nested change=%v replace=%v", nestedChange, nestedReplace)
	}
	if counter.state.Value().Count != 1 {
		t.Fatalf("outer change lost: %+v", counter.state.Value())
	}
	// Other goroutines still serialize rather than being rejected.
	done := make(chan error, 1)
	if err := counter.state.Change(ctx, func(draft *counterState) error {
		go func() { done <- counter.state.Change(ctx, func(d *counterState) error { d.Count += 10; return nil }) }()
		draft.Count = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if counter.state.Value().Count != 12 {
		t.Fatalf("concurrent change = %+v", counter.state.Value())
	}
}

// handle.ts/consumer.ts: a retained remote state handle is revoked with its binding.
func TestUpstreamRetainedRemoteStateRevokedByDispose(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	if err := Provide[Counter](fixture.provider, counterDefinition, newCounter(t)); err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(fixture.binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	replica, err := service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	if _, hydrated, err := replica.Load(); err != nil || !hydrated {
		t.Fatalf("load before dispose = %v %v", hydrated, err)
	}
	if err := fixture.binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replica.Load(); err == nil || !strings.Contains(err.Error(), "disposed") {
		t.Fatalf("retained state after dispose = %v", err)
	}
	if _, err := replica.Subscribe(func(pico3.JsonValue, context.Context, pico3.ReplicatedStateDelivery) error { return nil }); err == nil {
		t.Fatal("subscribe after dispose succeeded")
	}
}

// host.ts HostServiceSlots.observe: a retained keyed view closes with its observation.
func TestUpstreamRetainedKeyedViewClosesWithInstance(t *testing.T) {
	ctx := context.Background()
	var spawner *ServiceSpawner[Counter]
	provider := Facet{Id: "lanes", Setup: func(env *FacetEnvironment) error {
		var err error
		spawner, err = ProvideMany(env, keyedCounterDefinition)
		return err
	}}
	views := make(chan Counter, 1)
	observer := Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
		return ObserveService(env, keyedCounterDefinition, func(_ context.Context, counter Counter) error {
			views <- counter
			return nil
		})
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{provider, observer}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Dispose(ctx) }()
	closeLane, err := spawner.Spawn("main", &counterImpl{state: mustState(t)})
	if err != nil {
		t.Fatal(err)
	}
	view := <-views
	if got, err := view.Add(ctx, 2, "held"); err != nil || got != 2 {
		t.Fatalf("add = %d %v", got, err)
	}
	closeLane()
	if _, err := view.Add(ctx, 1, "after close"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
		t.Fatalf("retained keyed view after close = %v", err)
	}
}

func TestServiceRefWithoutRegisteredViewFails(t *testing.T) {
	ctx := context.Background()
	unviewed := pico3.DefineService[Counter]("test.unviewed")
	var ref *ServiceRef[Counter]
	facets := []Facet{
		{Id: "p", Setup: func(env *FacetEnvironment) error {
			return ProvideService[Counter](env, unviewed, &counterImpl{state: mustState(t)})
		}},
		{Id: "c", Setup: func(env *FacetEnvironment) error {
			var err error
			ref, err = UseService(env, unviewed)
			return err
		}},
	}
	// A remotely exposable in-host service needs a remote client adapter.
	if _, err := CreateFacetHost(ctx, FacetOptions{Facets: facets}); err == nil || !strings.Contains(err.Error(), "RegisterRemoteClient") {
		t.Fatalf("missing remote client = %v", err)
	}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: facets, RemoteClients: map[string]func(*RemoteService) any{
		unviewed.Id(): func(service *RemoteService) any { return remoteCounter{service} },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Dispose(ctx) }()
	if _, err := ref.Get(); err == nil || !strings.Contains(err.Error(), "RegisterServiceView") {
		t.Fatalf("unregistered view = %v", err)
	}
}

// host.ts: a transition requested from inside a transition's callback is
// rejected for the non-active phase instead of waiting on itself.
func TestUpstreamReentrantHostTransitionIsRejected(t *testing.T) {
	ctx := context.Background()
	var host *FacetHost
	nested := make(chan error, 2)
	candidate := Facet{Id: "counter", Setup: func(env *FacetEnvironment) error {
		if err := ProvideService[Counter](env, counterDefinition, &counterImpl{state: mustState(t)}); err != nil {
			return err
		}
		return env.OnActivate(func(ctx context.Context) error {
			nested <- host.Reload(ctx, nil)
			nested <- host.Dispose(ctx)
			return nil
		})
	}}
	var err error
	host, err = CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", &eventLog{}, 0, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- host.Reload(ctx, []Facet{candidate}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reentrant transition deadlocked")
	}
	for range 2 {
		if err := <-nested; err == nil || !strings.Contains(err.Error(), "while reloading") {
			t.Fatalf("nested transition = %v", err)
		}
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
}

// provider.ts spawn: the generation advances before the implementation is
// classified, so a rejected implementation consumes a generation.
func TestUpstreamRejectedSpawnConsumesGeneration(t *testing.T) {
	provider, err := NewRemoteServiceProvider(KeyedService(keyedCounterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Spawn[Counter](provider, keyedCounterDefinition, "k", nil); err == nil {
		t.Fatal("nil implementation accepted")
	}
	if _, err := Spawn[Counter](provider, keyedCounterDefinition, "k", newCounter(t)); err != nil {
		t.Fatal(err)
	}
	subscription, err := provider.Subscribe(keyedCounterDefinition.Id(), ServiceKeyed, func(context.Context, ServiceProviderUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := subscription.Snapshot().Instances[0].Instance.Generation; got != 2 {
		t.Fatalf("generation after rejected spawn = %d, want 2", got)
	}
}

// provider.ts: keyed snapshots sort by key.localeCompare (expected order
// taken from Node: ["10","9","a","b","B","e","é","z","Z"]); disposal closes
// instances in Map insertion order.
func TestUpstreamKeyedSnapshotAndDisposalOrder(t *testing.T) {
	provider, err := NewRemoteServiceProvider(KeyedService(keyedCounterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	insertion := []string{"b", "B", "a", "é", "e", "Z", "z", "10", "9"}
	for _, key := range insertion {
		if _, err := Spawn[Counter](provider, keyedCounterDefinition, key, newCounter(t)); err != nil {
			t.Fatal(err)
		}
	}
	var closed []string
	subscription, err := provider.Subscribe(keyedCounterDefinition.Id(), ServiceKeyed, func(_ context.Context, update ServiceProviderUpdate) {
		if update.Type == UpdateClosed {
			closed = append(closed, update.Address.Key)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshotKeys []string
	for _, instance := range subscription.Snapshot().Instances {
		snapshotKeys = append(snapshotKeys, instance.Instance.Key)
	}
	if want := []string{"10", "9", "a", "b", "B", "e", "é", "z", "Z"}; !reflect.DeepEqual(snapshotKeys, want) {
		t.Fatalf("snapshot order = %v, want %v", snapshotKeys, want)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Dispose(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(closed, insertion) {
		t.Fatalf("disposal order = %v, want insertion order %v", closed, insertion)
	}
}
