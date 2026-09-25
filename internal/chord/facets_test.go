package chord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) add(event string) {
	log.mu.Lock()
	log.events = append(log.events, event)
	log.mu.Unlock()
}

func (log *eventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

func counterFacet(t *testing.T, id string, log *eventLog, initial int, created chan<- *counterImpl) Facet {
	return Facet{Id: id, Setup: func(env *FacetEnvironment) error {
		state, err := NewReplicatedState(&counterState{Count: initial, Log: []string{}})
		if err != nil {
			return err
		}
		counter := &counterImpl{state: state}
		if created != nil {
			created <- counter
		}
		if err := ProvideService[Counter](env, counterDefinition, counter); err != nil {
			return err
		}
		if err := env.OnActivate(func(context.Context) error { log.add("activate " + id); return nil }); err != nil {
			return err
		}
		return env.Own(func(context.Context) error { log.add("dispose " + id); return nil })
	}}
}

// host.ts connects every binding with BACKGROUND_CONTEXT, independently of the host caller.
func TestFacetHostReadinessUsesBackgroundContext(t *testing.T) {
	server, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{counterFacet(t, "counter", &eventLog{}, 10, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Dispose(context.Background()) })
	endpoint := CreateRemoteServiceEndpoint(server.Services())
	t.Cleanup(endpoint.Dispose)
	source := &backgroundReadySource{transportSource: &transportSource{transport: NewJSONCopyTransport(endpoint)}}
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		_, err := UseService(env, counterDefinition)
		return err
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Dispose(context.Background()) })
}

type backgroundReadySource struct{ *transportSource }

func (source *backgroundReadySource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	services, err := source.transportSource.Open(options)
	return backgroundReadyServices{services}, err
}

type backgroundReadyServices struct{ RemoteServices }

func (services backgroundReadyServices) Ready(ctx context.Context) error {
	if ctx.Done() != nil {
		return errors.New("host readiness must use BACKGROUND_CONTEXT")
	}
	return services.RemoteServices.Ready(ctx)
}

func TestFacetHostOrdersActivationAndDisposesOnce(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	var ref *ServiceRef[Counter]
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		if ref, err = UseService(env, counterDefinition); err != nil {
			return err
		}
		if _, err := ref.Get(); err == nil {
			t.Error("service handle usable during setup")
		}
		if err := env.OnActivate(func(ctx context.Context) error {
			counter, err := ref.Get()
			if err != nil {
				return err
			}
			_, err = counter.Add(ctx, 1, "consumer-activate")
			log.add("activate consumer")
			return err
		}); err != nil {
			return err
		}
		if err := env.Own(func(context.Context) error { log.add("dispose consumer first-owned"); return nil }); err != nil {
			return err
		}
		return env.OnDeactivate(func(context.Context) error { log.add("dispose consumer"); return nil })
	}}
	// Listed before its provider: activation must still follow dependencies.
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{consumer, counterFacet(t, "counter", log, 0, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	counter, err := Use(host.Services(), counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if got := counter.State().Value().Count; got != 1 {
		t.Fatalf("count after activation = %d", got)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"activate counter", "activate consumer", "dispose consumer", "dispose consumer first-owned", "dispose counter"}
	if got := log.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if _, err := ref.Get(); err == nil {
		t.Fatal("service handle usable after dispose")
	}
}

func TestFacetHostRemoteConsumerAndReloadCutover(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	created := make(chan *counterImpl, 4)
	var ref *ServiceRef[Counter]
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		ref, err = UseService(env, counterDefinition)
		return err
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", log, 0, created), consumer}})
	if err != nil {
		t.Fatal(err)
	}
	first := <-created
	endpoint := CreateRemoteServiceEndpoint(host.Services())
	errs := make(chan error, 8)
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services: []string{counterDefinition.Id()}, Transport: NewJSONCopyTransport(endpoint),
		OnError: func(err error) { errs <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	replica, _ := service.State("state")
	rec := newRecorder()
	if _, err := TypedReplica[*counterState](replica).Subscribe(rec.listen); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(ctx, "add", 2, "remote"); err != nil {
		t.Fatal(err)
	}

	// A shape-changing reload is rejected and the generation stays active.
	bad := Facet{Id: "counter", Setup: func(env *FacetEnvironment) error { return nil }}
	if err := host.Reload(ctx, []Facet{bad}); err == nil || !strings.Contains(err.Error(), "preserve") {
		t.Fatalf("shape-changing reload = %v", err)
	}
	if _, err := service.Call(ctx, "add", 1, "after-rejected"); err != nil {
		t.Fatal(err)
	}

	if err := host.Reload(ctx, []Facet{counterFacet(t, "counter", log, 100, created)}); err != nil {
		t.Fatal(err)
	}
	<-created
	if _, err := first.Add(ctx, 1, "retired"); err != nil {
		t.Fatal(err)
	}
	current, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	if current.State().Value().Count != 100 {
		t.Fatalf("in-host handle not cut over: %+v", current.State().Value())
	}
	if _, err := service.Call(ctx, "add", 5, "new-generation"); err != nil {
		t.Fatal(err)
	}
	got := rec.waitFor(t, 5)
	want := []delivery{
		{DeliveryHydrate, 0, counterState{Log: []string{}}},
		{DeliveryUpdate, 1, counterState{Count: 2, Log: []string{"remote"}}},
		{DeliveryUpdate, 2, counterState{Count: 3, Log: []string{"remote", "after-rejected"}}},
		{DeliveryHydrate, 0, counterState{Count: 100, Log: []string{}}},
		{DeliveryUpdate, 1, counterState{Count: 105, Log: []string{"new-generation"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
	if err := binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	endpoint.Dispose()
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"activate counter", "activate counter", "dispose counter", "dispose counter"}
	if events := log.snapshot(); !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	select {
	case err := <-errs:
		t.Fatalf("binding error: %v", err)
	default:
	}
}

func TestFacetHostKeyedProvisionObservedLocallyAndRemotely(t *testing.T) {
	ctx := context.Background()
	var spawner *ServiceSpawner[Counter]
	var closeLane func()
	provider := Facet{Id: "lanes", Setup: func(env *FacetEnvironment) error {
		var err error
		if spawner, err = ProvideMany(env, keyedCounterDefinition); err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			state, err := NewReplicatedState(&counterState{Log: []string{}})
			if err != nil {
				return err
			}
			closeLane, err = spawner.Spawn("main", &counterImpl{state: state})
			return err
		})
	}}
	observed := make(chan context.Context, 4)
	observer := Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
		return ObserveService(env, keyedCounterDefinition, func(observeCtx context.Context, counter Counter) error {
			if _, err := counter.Add(observeCtx, 1, "observed"); err != nil {
				return err
			}
			observed <- observeCtx
			<-observeCtx.Done()
			return nil
		})
	}}
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{observer, provider}})
	if err != nil {
		t.Fatal(err)
	}
	var local context.Context
	select {
	case local = <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("local keyed observer did not run")
	}
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services: []string{keyedCounterDefinition.Id()}, Transport: NewJSONCopyTransport(CreateRemoteServiceEndpoint(host.Services())),
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := make(chan *RemoteService, 1)
	if _, err := ObserveRemote(binding, keyedCounterDefinition, func(_ context.Context, service *RemoteService) error {
		remote <- service
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var service *RemoteService
	select {
	case service = <-remote:
	case <-time.After(5 * time.Second):
		t.Fatal("remote keyed observer did not run")
	}
	replica, _ := service.State("state")
	waitUntil(t, "remote keyed hydration", func() bool {
		value, ok, _ := TypedReplica[*counterState](replica).Load()
		return ok && value.Count == 1
	})
	closeLane()
	select {
	case <-local.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closing the instance did not cancel the local observer")
	}
	if err := binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFacetHostValidationAndStartupCleanup(t *testing.T) {
	ctx := context.Background()
	missing := Facet{Id: "needs", Setup: func(env *FacetEnvironment) error {
		_, err := UseService(env, counterDefinition)
		return err
	}}
	if _, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{missing}}); err == nil || !strings.Contains(err.Error(), "requires local/test.counter/singleton") {
		t.Fatalf("missing provider = %v", err)
	}
	if _, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{{Id: "a"}, {Id: "a"}}}); err == nil {
		t.Fatal("duplicate facet IDs accepted")
	}
	other := pico3.DefineService[Counter]("test.other")
	cycleA := Facet{Id: "a", Setup: func(env *FacetEnvironment) error {
		if _, err := UseService(env, other); err != nil {
			return err
		}
		return ProvideService[Counter](env, counterDefinition, &counterImpl{state: mustState(t)})
	}}
	cycleB := Facet{Id: "b", Setup: func(env *FacetEnvironment) error {
		if _, err := UseService(env, counterDefinition); err != nil {
			return err
		}
		return ProvideService[Counter](env, other, &counterImpl{state: mustState(t)})
	}}
	if _, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{cycleA, cycleB}}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle = %v", err)
	}
	log := &eventLog{}
	failing := Facet{Id: "failing", Setup: func(*FacetEnvironment) error { return errors.New("setup failed") }}
	_, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", log, 0, nil), failing}})
	if err == nil || err.Error() != "setup failed" {
		t.Fatalf("setup failure = %v", err)
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, []string{"dispose counter"}) {
		t.Fatalf("startup cleanup events = %v", got)
	}
	failingActivation := Facet{Id: "boom", Setup: func(env *FacetEnvironment) error {
		return env.OnActivate(func(context.Context) error { return errors.New("activate failed") })
	}}
	log2 := &eventLog{}
	if _, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", log2, 0, nil), failingActivation}}); err == nil {
		t.Fatal("activation failure ignored")
	}
	if got := log2.snapshot(); !reflect.DeepEqual(got, []string{"activate counter", "dispose counter"}) {
		t.Fatalf("activation cleanup events = %v", got)
	}
}

func mustState(t *testing.T) *MutableReplicatedState[*counterState] {
	t.Helper()
	state, err := NewReplicatedState(&counterState{Log: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCombineFacetLoadersReleasesInReverseOnce(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	loader := func(name string, fail bool) FacetLoader {
		return facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
			if fail {
				return LoadedFacets{}, errors.New("load " + name + " failed")
			}
			return LoadedFacets{Facets: []Facet{{Id: name}}, Dispose: func(context.Context) error { log.add("release " + name); return nil }}, nil
		})
	}
	if _, err := CombineFacetLoaders(loader("a", false), loader("b", false), loader("c", true)).Load(ctx); err == nil {
		t.Fatal("load failure ignored")
	}
	if got := log.snapshot(); !reflect.DeepEqual(got, []string{"release b", "release a"}) {
		t.Fatalf("failure cleanup = %v", got)
	}
	log = &eventLog{}
	loaded, err := CombineFacetLoaders(loader("a", false), CreateStaticFacetLoader([]Facet{{Id: "s"}}), loader("b", false)).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, facet := range loaded.Facets {
		ids = append(ids, facet.Id)
	}
	if !reflect.DeepEqual(ids, []string{"a", "s", "b"}) {
		t.Fatalf("facets = %v", ids)
	}
	_ = loaded.Dispose(ctx)
	_ = loaded.Dispose(ctx)
	if got := log.snapshot(); !reflect.DeepEqual(got, []string{"release b", "release a"}) {
		t.Fatalf("release = %v", got)
	}
}

// remoteCounter is the kind of typed client adapter a service lane supplies.
type remoteCounter struct{ service *RemoteService }

func (counter remoteCounter) State() pico3.ReplicatedStateOf[*counterState] {
	replica, err := counter.service.State("state")
	if err != nil {
		panic(err)
	}
	return TypedReplica[*counterState](replica)
}

func (counter remoteCounter) Add(ctx context.Context, amount int, label string) (int, error) {
	return CallResult[int](ctx, counter.service, "add", amount, label)
}

func (counter remoteCounter) Fail(ctx context.Context) error {
	_, err := counter.service.Call(ctx, "fail")
	return err
}

// transportSource is a minimal RemoteServiceSource over one transport.
type transportSource struct {
	transport RemoteServiceTransport
	opened    []*RemoteServiceBinding
}

func (source *transportSource) AcceptsUnavailableServices() bool { return false }

func (source *transportSource) Catalogue(ctx context.Context) ([]ServiceCatalogueEntry, error) {
	raw, err := source.transport.Invoke(ctx, CreateServiceCatalogueCall())
	if err != nil {
		return nil, err
	}
	var entries []ServiceCatalogueEntry
	err = json.Unmarshal(raw, &entries)
	return entries, err
}

func (source *transportSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services: options.Services, Transport: source.transport, AssertAccess: options.AssertAccess, OnError: options.OnError,
	})
	if err != nil {
		return nil, err
	}
	source.opened = append(source.opened, binding)
	return binding, nil
}

func TestFacetHostExternalServiceSource(t *testing.T) {
	ctx := context.Background()
	serverLog := &eventLog{}
	server, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "counter", serverLog, 10, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingEndpoint{RemoteServiceEndpoint: CreateRemoteServiceEndpoint(server.Services())}
	source := &transportSource{transport: NewJSONCopyTransport(counting)}
	hydratedAtActivation := make(chan int, 1)
	client := Facet{Id: "client", Setup: func(env *FacetEnvironment) error {
		ref, err := UseService(env, counterDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(ctx context.Context) error {
			counter, err := ref.Get()
			if err != nil {
				return err
			}
			hydratedAtActivation <- counter.State().Value().Count
			_, err = counter.Add(ctx, 5, "from-client")
			return err
		})
	}}
	errs := make(chan error, 4)
	options := FacetOptions{
		Facets:         []Facet{client},
		ServiceSources: []RemoteServiceSource{source},
		OnError:        func(err error) { errs <- err },
	}
	options.RemoteClients = map[string]func(*RemoteService) any{counterDefinition.Id(): func(service *RemoteService) any { return remoteCounter{service} }}
	before := counting.unsubscribes.Load()
	host, err := CreateFacetHost(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-hydratedAtActivation; got != 10 {
		t.Fatalf("state at activation = %d, want hydrated 10", got)
	}
	serverCounter, err := Use(server.Services(), counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if got := serverCounter.State().Value().Count; got != 15 {
		t.Fatalf("server count = %d", got)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if got := counting.unsubscribes.Load() - before; got != 1 {
		t.Fatalf("client host dispose unsubscribed %d times, want 1", got)
	}
	if err := server.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatalf("host error: %v", err)
	default:
	}
}

// waitingSource's catalogue waits until another source's catalogue has started.
type waitingSource struct {
	*transportSource
	started    chan struct{}
	waitFor    chan struct{}
	disposeErr error
}

func (source *waitingSource) Catalogue(ctx context.Context) ([]ServiceCatalogueEntry, error) {
	close(source.started)
	select {
	case <-source.waitFor:
	case <-time.After(5 * time.Second):
		return nil, errors.New("catalogues were requested serially")
	}
	return source.transportSource.Catalogue(ctx)
}

type failingDispose struct {
	RemoteServices
	err error
}

func (services failingDispose) Dispose(ctx context.Context) error {
	_ = services.RemoteServices.Dispose(ctx)
	return services.err
}

func (source *waitingSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	services, err := source.transportSource.Open(options)
	if err != nil || source.disposeErr == nil {
		return services, err
	}
	return failingDispose{RemoteServices: services, err: source.disposeErr}, nil
}

// host.ts: source catalogues are requested concurrently (Promise.all) and
// source bindings are disposed independently (Promise.allSettled).
func TestFacetHostSourcesRunConcurrentlyAndDisposeIndependently(t *testing.T) {
	ctx := context.Background()
	serverA, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{counterFacet(t, "a", &eventLog{}, 1, nil)}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverA.Dispose(ctx) }()
	serverB, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{{Id: "b", Setup: func(env *FacetEnvironment) error {
		return ProvideService[Counter](env, keyedOtherDefinition, &counterImpl{state: mustState(t)})
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverB.Dispose(ctx) }()
	aStarted, bStarted := make(chan struct{}), make(chan struct{})
	sourceA := &waitingSource{transportSource: &transportSource{transport: NewJSONCopyTransport(CreateRemoteServiceEndpoint(serverA.Services()))}, started: aStarted, waitFor: bStarted, disposeErr: errors.New("source A dispose failed")}
	sourceB := &waitingSource{transportSource: &transportSource{transport: NewJSONCopyTransport(CreateRemoteServiceEndpoint(serverB.Services()))}, started: bStarted, waitFor: aStarted}
	client := Facet{Id: "client", Setup: func(env *FacetEnvironment) error {
		if _, err := UseService(env, counterDefinition); err != nil {
			return err
		}
		_, err := UseService(env, keyedOtherDefinition)
		return err
	}}
	adapter := func(service *RemoteService) any { return remoteCounter{service} }
	host, err := CreateFacetHost(ctx, FacetOptions{
		Facets:         []Facet{client},
		ServiceSources: []RemoteServiceSource{sourceA, sourceB},
		RemoteClients:  map[string]func(*RemoteService) any{counterDefinition.Id(): adapter, keyedOtherDefinition.Id(): adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = host.Dispose(ctx)
	if err == nil || !strings.Contains(err.Error(), "source A dispose failed") {
		t.Fatalf("dispose = %v", err)
	}
	for _, binding := range sourceB.opened {
		if err := binding.Ready(ctx); err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("source B binding not disposed after A failed: %v", err)
		}
	}
}

var keyedOtherDefinition = pico3.DefineService[Counter]("test.second-counter")
