package host

// Ports packages/chord/test/readiness.test.ts (Pi v1.2.0): startup must not report ready while an eager object has
// failed. Pi's promise-returning install/load/ready are blocking calls here; a promise Pi obtains now and awaits later
// is a call started on its own goroutine (or, for ready, the eager holds captured in one loop section).

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var (
	readyEager = emptyLocal("ready.eager")
	readyLazy  = emptyLocal("ready.lazy")
)

// readinessHost is a host whose background errors fail the test (readiness.test.ts unexpectedError).
func readinessHost(t *testing.T, name string) *HostImpl {
	t.Helper()
	host := CreateHost(HostOptions{Self: kindHost(), Name: name, OnError: func(err error) { t.Errorf("unexpected error: %v", err) }})
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func emptyImplementation(any, Principal, Self) (Implementation, error) {
	return chordjson.ObjectOf(), nil
}

var eagerTrue = ServeOptions{Lifetime: LifetimeHost}

// installOrLoad is readiness.test.ts's `operation === "install" ? host.install(facet) : host.load([facet])`.
func installOrLoad(host *HostImpl, operation string, facet Facet, options LoadOptions) error {
	if operation == "install" {
		return host.Install(facet, options)
	}
	return host.Load([]PluginRef{&facet}, options)
}

func started(fn func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	return done
}

// readiness.test.ts:23-71 it("${operation} awaits resource activation and registration, leaving lazy objects lazy")
func TestReadinessAwaitsResourceActivationAndRegistrationLeavingLazyObjectsLazy(t *testing.T) {
	for _, operation := range []string{"install", "load"} {
		t.Run(operation, func(t *testing.T) {
			host := readinessHost(t, "")
			entries := DefineRegistry[string]("ready.entries")
			opened := make(chan struct{})
			var lazyRuns atomic.Int32
			facet := Facet{Name: "ready", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
				if err := api.Serve(readyEager, func(_ any, _ Principal, self Self) (Implementation, error) {
					if _, err := self.Resource("r", ResourceDeclaration[any]{
						Version: 1,
						Open: func(ResourceOpenOptions) (any, error) {
							<-opened
							return nil, nil
						},
					}); err != nil {
						return nil, err
					}
					return chordjson.ObjectOf(), self.Register(Registry[any](entries), []Entry{{Name: "eager", Value: "live"}})
				}, eagerTrue); err != nil {
					return err
				}
				return api.Serve(readyLazy, func(any, Principal, Self) (Implementation, error) {
					lazyRuns.Add(1)
					return chordjson.ObjectOf(), nil
				}, ServeOptions{})
			}}}
			loading := make(chan error, 1)
			go func() { loading <- installOrLoad(host, operation, facet, LoadOptions{}) }()
			view := host.Registry(Registry[any](entries))
			select {
			case err := <-loading:
				t.Fatalf("%s settled before the eager resource opened: %v", operation, err)
			case <-time.After(20 * time.Millisecond):
			}
			if value, ok := view.Get("eager", nil); ok {
				t.Fatalf("registered before activation: %v", value)
			}
			close(opened)
			if err := <-loading; err != nil {
				t.Fatal(err)
			}
			if value, _ := view.Get("eager", nil); value != "live" {
				t.Fatalf("eager = %v", value)
			}
			if lazyRuns.Load() != 0 {
				t.Fatalf("lazy ran %d times", lazyRuns.Load())
			}
		})
	}
}

// readiness.test.ts:73-112 it("${operation} rejects eager ${failure} failure with the activation error")
func TestReadinessRejectsEagerFailureWithTheActivationError(t *testing.T) {
	for _, operation := range []string{"install", "load"} {
		for _, failure := range []string{"factory", "dependency", "resource"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				errs := &errorLog{}
				host := CreateHost(HostOptions{Self: kindHost(), OnError: errs.add})
				t.Cleanup(func() { _ = host.Close() })
				cause := Forbidden(failure + " failed")
				facet := Facet{Name: "bad", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
					if err := api.Serve(readyLazy, func(any, Principal, Self) (Implementation, error) { return nil, cause }, ServeOptions{}); err != nil {
						return err
					}
					return api.Serve(readyEager, func(_ any, _ Principal, self Self) (Implementation, error) {
						switch failure {
						case "factory":
							return nil, cause
						case "dependency":
							if _, err := self.Get(readyLazy, map[string]any{}, nil); err != nil {
								return nil, err
							}
						case "resource":
							if _, err := self.Resource("r", ResourceDeclaration[any]{
								Version: 1,
								Open:    func(ResourceOpenOptions) (any, error) { return nil, cause },
							}); err != nil {
								return nil, err
							}
						}
						return chordjson.ObjectOf(), nil
					}, eagerTrue)
				}}}
				if err := installOrLoad(host, operation, facet, LoadOptions{}); err != error(cause) {
					t.Fatalf("%s: %v, want the cause itself", operation, err)
				}
				if err := host.Ready(); err != error(cause) {
					t.Fatalf("ready: %v, want the cause itself", err)
				}
				waitFor(t, "the reported cause", func() bool { return len(errs.items()) > 0 })
				if items := errs.items(); len(items) != 1 || items[0] != error(cause) {
					t.Fatalf("errors %v", items)
				}
			})
		}
	}
}

// readiness.test.ts:115-138 it("load installs every facet before awaiting eager consumers")
func TestReadinessLoadInstallsEveryFacetBeforeAwaitingEagerConsumers(t *testing.T) {
	host := readinessHost(t, "")
	consumer := Facet{Name: "consumer", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(readyEager, func(_ any, _ Principal, self Self) (Implementation, error) {
			_, err := self.Get(readyLazy, map[string]any{}, nil)
			return chordjson.ObjectOf(), err
		}, eagerTrue)
	}}}
	provider := Facet{Name: "provider", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(readyLazy, emptyImplementation, ServeOptions{})
	}}}
	if err := host.Load([]PluginRef{&consumer, &provider}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if status := mustRef(t, host, readyEager, map[string]any{}, nil).Status(); status != RefLive {
		t.Fatalf("status %s", status)
	}
}

// readiness.test.ts:140-163 it("missing eager dependencies reject load instead of only reaching onError")
func TestReadinessMissingEagerDependenciesRejectLoadInsteadOfOnlyReachingOnError(t *testing.T) {
	host := quiet(t, nil)
	missing := Facet{Name: "missing", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(readyEager, func(_ any, _ Principal, self Self) (Implementation, error) {
			_, err := self.Get(readyLazy, map[string]any{}, nil)
			return chordjson.ObjectOf(), err
		}, eagerTrue)
	}}}
	err := host.Load([]PluginRef{&missing}, LoadOptions{})
	if chordError := asChord(err); chordError == nil || chordError.Code != CodeUnavailable || chordError.Reason != "unknown_type" {
		t.Fatalf("load: %v", err)
	}
}

// readiness.test.ts:165-191 it("ready includes direct host serves, but not later serves")
func TestReadinessReadyIncludesDirectHostServesButNotLaterServes(t *testing.T) {
	host := quiet(t, nil)
	first := Local("ready.first", LocalDef{Version: 1, Input: tObject()})
	var created atomic.Int32
	mustServe(t, host, first, func(any, Principal, Self) (Implementation, error) {
		created.Add(1)
		return chordjson.ObjectOf(), nil
	}, ServeOptions{Lifetime: LifetimeHost})
	// `const first = host.ready()`: the host objects present now; awaited after the later serve.
	var ready *Promise[struct{}]
	host.run(func() { ready = host.hostObjectsReady(host.hostObjects.Values()) })
	mustServe(t, host, readyEager, func(any, Principal, Self) (Implementation, error) {
		return nil, Forbidden("later")
	}, eagerTrue)
	if _, err := ready.Await(t.Context()); err != nil {
		t.Fatalf("first ready: %v", err)
	}
	if created.Load() != 1 {
		t.Fatalf("created = %d", created.Load())
	}
	if err := host.Ready(); err == nil || !strings.Contains(err.Error(), "later") {
		t.Fatalf("ready: %v", err)
	}
}

// readiness.test.ts:193-257 it("${operation} can defer host object readiness until the parent admits the worker")
func TestReadinessCanDeferHostObjectReadinessUntilTheParentAdmitsTheWorker(t *testing.T) {
	for _, operation := range []string{"install", "load"} {
		t.Run(operation, func(t *testing.T) {
			server := readinessHost(t, "server")
			parent, upstream := MemoryLinkPair()
			worker := CreateHost(HostOptions{Self: kindHost(), Name: "worker", Upstream: &UpstreamOptions{Link: upstream, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(err error) { t.Errorf("unexpected error: %v", err) }})
			t.Cleanup(func() { _ = worker.Close() })
			status := Remote("ready.status", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"default", VariantDef{}}}})
			mustServe(t, server, status, func(any, Principal, Self) (Implementation, error) {
				return chordjson.ObjectOf("variant", "default"), nil
			}, ServeOptions{})
			var bootstrap, reported atomic.Bool
			facet := Facet{Name: "worker", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
				if err := api.Serve(readyLazy, func(any, Principal, Self) (Implementation, error) {
					bootstrap.Store(true)
					return chordjson.ObjectOf(), nil
				}, eagerTrue); err != nil {
					return err
				}
				return api.Serve(readyEager, func(_ any, _ Principal, self Self) (Implementation, error) {
					hold, err := self.Get(status, map[string]any{}, nil)
					if err != nil {
						return nil, err
					}
					_, err = self.Resource("r", ResourceDeclaration[any]{
						Version: 1,
						Deps:    []any{hold},
						Open: func(ResourceOpenOptions) (any, error) {
							reported.Store(true)
							return nil, nil
						},
					})
					return chordjson.ObjectOf(), err
				}, eagerTrue)
			}}}
			if err := installOrLoad(worker, operation, facet, LoadOptions{AwaitHostObjects: new(false)}); err != nil {
				t.Fatal(err)
			}
			if err := readyErr(t, mustRef(t, worker, readyLazy, map[string]any{}, nil)); err != nil {
				t.Fatal(err)
			}
			if !bootstrap.Load() {
				t.Fatal("the bootstrap eager object did not run")
			}
			ready := started(worker.Ready)
			select {
			case err := <-ready:
				t.Fatalf("ready settled before the parent admitted the worker: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			if reported.Load() {
				t.Fatal("reported before the parent admitted the worker")
			}
			// Control-ready announcement allows the parent to accept the queued upstream holds.
			if err := server.Accept(parent, AcceptOptions{Principal: map[string]any{"host": "worker"}, Kind: KindHost, Name: new("worker")}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-ready:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second): // Pi's { timeout: 2000 }
				t.Fatal("ready did not settle after the parent admitted the worker")
			}
			if !reported.Load() {
				t.Fatal("the eager resource did not open")
			}
		})
	}
}
