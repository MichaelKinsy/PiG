package chord

// pi: packages/chord/src/services/instances.ts

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// upstream: packages/chord/src/facets/host.ts:FacetLifecycle.activate starts an isolated facet's in-host keyed observation synchronously, and the loopback KeyedBinding.#start resumes at activate's first await. Pi 1.0.0 (node .upstream/v1.0.0/packages/chord/src) traces ["activate provider","observe","activate later","host created","observe","spawned one"] without callbacks and ["activate provider","activate observer 1","observe","activate observer 2","activate later","host created","observe","spawned one"] with two.
//
// PiG starts the loopback subscription on a goroutine when the observation starts and joins it after the first callback, so its snapshot observer can run before or during that callback; Pi runs it only after the callback's synchronous prefix. That pair is the open review finding of rev-facet-observer-order-100; every other position is Pi's.
func TestIsolatedFacetKeyedObservationJoinsActivation(t *testing.T) {
	for _, tc := range []struct {
		callbacks int
		want      []string
		// unorderedBefore names the trace entry that may follow the first "observe" in PiG although Pi records it first.
		unorderedBefore string
	}{
		{0, []string{"activate provider", "observe", "activate later", "host created", "observe", "spawned one"}, ""},
		{2, []string{"activate provider", "activate observer 1", "observe", "activate observer 2", "activate later", "host created", "observe", "spawned one"}, "activate observer 1"},
	} {
		t.Run(fmt.Sprintf("%d activation callbacks", tc.callbacks), func(t *testing.T) {
			ctx := context.Background()
			trace := &locked[string]{}
			var spawner *ServiceSpawner[Reader]
			observer := Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
				if err := ObserveFacetService(env, keyedValueDefinition.Id(), false, func(context.Context, *FacetService) error {
					trace.add("observe")
					return nil
				}); err != nil {
					return err
				}
				if err := ProvideService(env, sourceDefinition, constReader("source")); err != nil {
					return err
				}
				for index := range tc.callbacks {
					if err := env.OnActivate(func(context.Context) error {
						trace.add(fmt.Sprintf("activate observer %d", index+1))
						return nil
					}); err != nil {
						return err
					}
				}
				return nil
			}}
			later := Facet{Id: "later", Setup: func(env *FacetEnvironment) error {
				if _, err := UseService(env, sourceDefinition); err != nil {
					return err
				}
				return env.OnActivate(func(context.Context) error {
					trace.add("activate later")
					return nil
				})
			}}
			provider := Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
				var err error
				if spawner, err = ProvideMany(env, keyedValueDefinition); err != nil {
					return err
				}
				return env.OnActivate(func(context.Context) error {
					trace.add("activate provider")
					_, err := spawner.Spawn("zero", constReader("zero"))
					return err
				})
			}}
			host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{later, observer, provider}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := host.Dispose(ctx); err != nil {
					t.Error(err)
				}
			})
			trace.add("host created")
			if _, err := spawner.Spawn("one", constReader("one")); err != nil {
				t.Fatal(err)
			}
			trace.add("spawned one")
			got := trace.get()
			if tc.unorderedBefore != "" {
				observe := slices.Index(got, "observe")
				if observe > 0 && got[observe-1] == "activate provider" && observe+1 < len(got) && got[observe+1] == tc.unorderedBefore {
					got = slices.Clone(got)
					got[observe], got[observe+1] = got[observe+1], got[observe]
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("trace = %q, want %q", trace.get(), tc.want)
			}
		})
	}
}
