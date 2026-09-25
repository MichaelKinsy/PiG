package chord

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// host.ts routes remotely exposable in-host services through a retained replica.
// Reload hydrates existing subscribers, and retired providers no longer deliver.
func TestDeltaRetainedFacetStateSubscriptionFollowsReload(t *testing.T) {
	ctx := context.Background()
	created := make(chan *counterImpl, 2)
	var ref *ServiceRef[Counter]
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		ref, err = UseService(env, counterDefinition)
		return err
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", &eventLog{}, 0, created), consumer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(ctx); err != nil {
			t.Error(err)
		}
	})
	old := <-created
	held, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	var deliveries []int
	stop, err := held.State().Subscribe(func(value *counterState, _ context.Context, _ pico3.ReplicatedStateDelivery) {
		deliveries = append(deliveries, value.Count)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := host.Reload(ctx, []Facet{counterFacet(t, "counter", &eventLog{}, 100, created)}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Add(ctx, 7, "retired"); err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 100}; !reflect.DeepEqual(deliveries, want) {
		t.Fatalf("retained state subscription = %v, upstream = %v", deliveries, want)
	}
}

// handle.ts rechecks access on the state value property and throws on revocation.
func TestDeltaRetainedFacetStateValueRejectsRevocation(t *testing.T) {
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
	held, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	state := held.State()
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	var failure any
	func() { defer func() { failure = recover() }(); _ = state.Value() }()
	if failure == nil {
		t.Fatal("retained state Value silently returns zero after revocation; upstream throws")
	}
}

// state.ts Subscribe hydrates on the caller stack. Change during that initial
// hydration delivers an update before Change returns when no drain is active.
func TestDeltaSubscribeHydrationChangeIsSynchronous(t *testing.T) {
	counter := newCounter(t)
	var events []string
	stop, err := counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		events = append(events, info.Kind)
		if info.Kind == DeliveryHydrate {
			if _, err := counter.Add(context.Background(), 1, "nested"); err != nil {
				t.Error(err)
			}
			events = append(events, "after change")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if want := []string{"hydrate", "update", "after change"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, upstream = %v", events, want)
	}
}

// api.ts combineFacetLoaders catches a synchronous loader throw, cleans already
// loaded resources, then rejects the load operation with the original failure.
func TestDeltaLoaderPanicCleansEarlierLoads(t *testing.T) {
	ctx := context.Background()
	cleaned := false
	failure := errors.New("loader failed")
	loader := CombineFacetLoaders(
		facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
			return LoadedFacets{Dispose: func(context.Context) error { cleaned = true; return nil }}, nil
		}),
		facetLoaderFunc(func(context.Context) (LoadedFacets, error) { panic(failure) }),
	)
	var escaped any
	var err error
	func() { defer func() { escaped = recover() }(); _, err = loader.Load(ctx) }()
	if escaped != nil || !cleaned || !errors.Is(err, failure) {
		t.Fatalf("loader panic escaped=%v cleaned=%v returned=%v; upstream rejects after cleanup", escaped, cleaned, err)
	}
}

// host.ts StagedServiceSpawner.connect iterates its Map in insertion order.
// Replacement activation stages instances before connecting them at cutover.
func TestDeltaStagedKeyedInstancesConnectInInsertionOrder(t *testing.T) {
	ctx := context.Background()
	provider := func(keys []string) Facet {
		return Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
			spawner, err := ProvideMany(env, keyedCounterDefinition)
			if err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error {
				for _, key := range keys {
					if _, err := spawner.Spawn(key, newCounter(t)); err != nil {
						return err
					}
				}
				return nil
			})
		}}
	}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{provider(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(ctx); err != nil {
			t.Error(err)
		}
	})
	var spawned []string
	sub, err := host.Services().Subscribe(keyedCounterDefinition.Id(), ServiceKeyed, func(_ context.Context, update ServiceProviderUpdate) {
		if update.Type == UpdateSpawned {
			spawned = append(spawned, update.Snapshot.Instance.Key)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sub.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := sub.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := host.Reload(ctx, []Facet{provider([]string{"z", "a"})}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"z", "a"}; !reflect.DeepEqual(spawned, want) {
		t.Fatalf("staged spawn order = %v, upstream = %v", spawned, want)
	}
}
