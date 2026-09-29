package chord

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
)

// upstream: packages/chord/src/services/instances.ts:#start applies each keyed observer synchronously when a ready directory admits an instance, so a snapshot's observers have run before binding readiness resolves.
func TestKeyedObserverRunsBeforeSnapshotReadiness(t *testing.T) {
	fixture := newRemoteFixture(t, KeyedService(keyedCounterDefinition))
	t.Cleanup(func() {
		if err := fixture.binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := Spawn[Counter](fixture.provider, keyedCounterDefinition, "lane", newCounter(t)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var generations []int
	stop, err := ObserveRemote(fixture.binding, keyedCounterDefinition, func(_ context.Context, service *RemoteService) error {
		mu.Lock()
		defer mu.Unlock()
		generations = append(generations, service.Address().Generation)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if err := fixture.binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]int(nil), generations...)
	mu.Unlock()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("observed generations at readiness = %v, want [1]", got)
	}
	noBindingErrors(t, fixture)
}

// upstream: packages/chord/src/facets/host.ts LocalKeyedServiceRegistry.spawn inserts into a ready InstanceDirectory, which applies every observer before spawn returns.
func TestLocalKeyedObserverRunsBeforeSpawnReturns(t *testing.T) {
	ctx := context.Background()
	var spawner *ServiceSpawner[Counter]
	provider := Facet{Id: "lanes", Setup: func(env *FacetEnvironment) error {
		var err error
		spawner, err = ProvideMany(env, keyedCounterDefinition)
		return err
	}}
	var observed []string
	observer := Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
		return ObserveService(env, keyedCounterDefinition, func(context.Context, Counter) error {
			observed = append(observed, "observed")
			return nil
		})
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{provider, observer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := spawner.Spawn("main", &counterImpl{state: mustState(t)}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 {
		t.Fatalf("observations when Spawn returned = %d, want 1", len(observed))
	}
}

// upstream: packages/chord/src/services/instances.ts:#start reports a synchronous throw before the applying delivery returns.
func TestKeyedObservationPrefixFailureReportsDuringSpawn(t *testing.T) {
	ctx := context.Background()
	var spawner *ServiceSpawner[Counter]
	provider := Facet{Id: "lanes", Setup: func(env *FacetEnvironment) error {
		var err error
		spawner, err = ProvideMany(env, keyedCounterDefinition)
		return err
	}}
	observer := Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
		return ObserveService(env, keyedCounterDefinition, func(context.Context, Counter) error {
			return errors.New("prefix failed")
		})
	}}
	reported := make(chan error, 4)
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{provider, observer}, OnError: func(err error) { reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := spawner.Spawn("main", &counterImpl{state: mustState(t)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if err.Error() != "prefix failed" {
			t.Fatalf("reported %v, want prefix failed", err)
		}
	default:
		t.Fatal("synchronous observer failure was not reported before Spawn returned")
	}
}

// upstream: packages/chord/src/services/instances.ts:#start leaves the returned Promise unawaited and reports its rejection only when the observation context was not aborted.
func TestContinueObservationReportsOnlyLiveRejection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reported []error
		observed := observationStart(context.Background(), func(ctx context.Context) error {
			ContinueObservation(ctx, func() error { return errors.New("live continuation failed") })
			return nil
		}, func(err error) { reported = append(reported, err) })
		observed()
		synctest.Wait()
		if len(reported) != 1 || reported[0].Error() != "live continuation failed" {
			t.Fatalf("live continuation reports = %v", reported)
		}

		reported = nil
		observeCtx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		cancelled := observationStart(observeCtx, func(ctx context.Context) error {
			ContinueObservation(ctx, func() error {
				<-release
				return errors.New("cancelled continuation failed")
			})
			return nil
		}, func(err error) { reported = append(reported, err) })
		cancelled()
		cancel()
		close(release)
		synctest.Wait()
		if len(reported) != 0 {
			t.Fatalf("cancelled continuation reports = %v", reported)
		}

		invoked := false
		skipped := observationStart(observeCtx, func(context.Context) error {
			invoked = true
			return nil
		}, func(err error) { reported = append(reported, err) })
		skipped()
		if invoked {
			t.Fatal("an observation cancelled before invocation ran its handler")
		}
	})
}
