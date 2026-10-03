package chord

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Ports packages/chord/test/facets.test.ts. A service contract that a facet consumes needs a guarded view (RegisterServiceView) and, for remote contracts, a typed client (RegisterRemoteClient); the contracts below register both. A Go facet's setup is synchronous by signature, so the asynchronous-setup assertion of the last case has no Go form.

type Reader interface {
	Read(ctx context.Context) (string, error)
}

type readerFunc func(ctx context.Context) (string, error)

func (read readerFunc) Read(ctx context.Context) (string, error) { return read(ctx) }

func constReader(value string) Reader {
	return readerFunc(func(context.Context) (string, error) { return value, nil })
}

type readerView struct{ resolve func() (Reader, error) }

func (view readerView) Read(ctx context.Context) (string, error) {
	target, err := view.resolve()
	if err != nil {
		return "", err
	}
	return target.Read(ctx)
}

type remoteReader struct{ service *RemoteService }

func (reader remoteReader) Read(ctx context.Context) (string, error) {
	return CallResult[string](ctx, reader.service, "read")
}

func defineReader(id string) ServiceDefinition[Reader] {
	definition := DefineService[Reader](id)
	RegisterServiceView(definition, func(resolve func() (Reader, error)) Reader { return readerView{resolve} })
	RegisterRemoteClient(definition, func(service *RemoteService) Reader { return remoteReader{service} })
	return definition
}

var (
	sourceDefinition     = defineReader("test.experimental.source")
	projectionDefinition = defineReader("test.experimental.projection")
	keyedValueDefinition = defineReader("test.experimental.keyed-value")
	leftValueDefinition  = defineReader("test.experimental.left-value")
	rightValueDefinition = defineReader("test.experimental.right-value")
	combinedDefinition   = defineReader("test.experimental.combined-value")
)

type Watched interface {
	State() ReplicatedStateOf[*valueDocState]
}

type watchedImpl struct {
	state *MutableReplicatedState[*valueDocState]
}

func (watched watchedImpl) State() ReplicatedStateOf[*valueDocState] { return watched.state }

type watchedView struct{ resolve func() (Watched, error) }

func (view watchedView) State() ReplicatedStateOf[*valueDocState] {
	return StateView(func() (ReplicatedStateOf[*valueDocState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

type remoteWatched struct{ service *RemoteService }

func (watched remoteWatched) State() ReplicatedStateOf[*valueDocState] {
	replica, err := watched.service.State("state")
	if err != nil {
		panic(err)
	}
	return TypedReplica[*valueDocState](replica)
}

var watchedDefinition = func() ServiceDefinition[Watched] {
	definition := DefineService[Watched]("test.experimental.watched")
	RegisterServiceView(definition, func(resolve func() (Watched, error)) Watched { return watchedView{resolve} })
	RegisterRemoteClient(definition, func(service *RemoteService) Watched { return remoteWatched{service} })
	return definition
}()

// HostValues and LocalKeyedValue are process-local contracts: no remote client.
type HostValues interface {
	Name() string
	Use() string
}

type hostValuesImpl struct{ name, use string }

func (values *hostValuesImpl) Name() string { return values.name }
func (values *hostValuesImpl) Use() string  { return values.use }

type hostValuesView struct{ resolve func() (HostValues, error) }

func (view hostValuesView) Name() string {
	target, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return target.Name()
}

func (view hostValuesView) Use() string {
	target, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return target.Use()
}

var hostValuesDefinition = func() ServiceDefinition[HostValues] {
	definition := DefineServiceWithOptions[HostValues]("test.experimental.host-values", ServiceOptions{Local: true})
	RegisterServiceView(definition, func(resolve func() (HostValues, error)) HostValues { return hostValuesView{resolve} })
	return definition
}()

type LocalKeyedValue interface {
	Metadata() map[string]string
	Read() (string, error)
}

type localKeyedImpl struct{ value string }

func (local localKeyedImpl) Metadata() map[string]string {
	return map[string]string{"value": local.value}
}
func (local localKeyedImpl) Read() (string, error) { return local.value, nil }

type localKeyedView struct {
	resolve func() (LocalKeyedValue, error)
}

func (view localKeyedView) Metadata() map[string]string {
	target, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return target.Metadata()
}

func (view localKeyedView) Read() (string, error) {
	target, err := view.resolve()
	if err != nil {
		return "", err
	}
	return target.Read()
}

var localKeyedDefinition = func() ServiceDefinition[LocalKeyedValue] {
	definition := DefineServiceWithOptions[LocalKeyedValue]("test.experimental.local-keyed-value", ServiceOptions{Local: true})
	RegisterServiceView(definition, func(resolve func() (LocalKeyedValue, error)) LocalKeyedValue { return localKeyedView{resolve} })
	return definition
}()

func mustFacetHost(t *testing.T, options FacetOptions) *FacetHost {
	t.Helper()
	host, err := CreateFacetHost(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func useService[T any](t *testing.T, env *FacetEnvironment, definition ServiceDefinition[T]) *ServiceRef[T] {
	t.Helper()
	ref, err := UseService(env, definition)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func get[T any](t *testing.T, ref *ServiceRef[T]) T {
	t.Helper()
	value, err := ref.Get()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func read(t *testing.T, reader Reader) string {
	t.Helper()
	value, err := reader.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func expectErrorContaining(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %v, want one containing %q", err, text)
	}
}

type keyedProvider struct {
	id    string
	value func() string
}

func keyedProviderFacet(t *testing.T, id string, value string) Facet {
	return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
		values, err := ProvideMany(env, keyedValueDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			_, err := values.Spawn("current", constReader(value))
			return err
		})
	}})
}

type observation struct {
	service Reader
	ctx     context.Context
}

func observingConsumer(t *testing.T, id string, observed *locked[observation]) Facet {
	return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
		return ObserveService(env, keyedValueDefinition, func(ctx context.Context, service Reader) error {
			observed.add(observation{service, ctx})
			return nil
		})
	}})
}

func TestFacetHost(t *testing.T) {
	ctx := context.Background()

	t.Run("discovers setup dependencies before connecting stable service handles", func(t *testing.T) {
		trace := &locked[string]{}
		var sourceRef *ServiceRef[Reader]
		projection := DefineFacet(Facet{Id: "projection", Setup: func(env *FacetEnvironment) error {
			trace.add("setup projection")
			sourceRef = useService(t, env, sourceDefinition)
			_, err := sourceRef.Get()
			expectErrorContaining(t, err, "Facet projection service handles cannot be used while setting_up")
			if err := ProvideService(env, projectionDefinition, Reader(readerFunc(func(ctx context.Context) (string, error) {
				return get(t, sourceRef).Read(ctx)
			}))); err != nil {
				return err
			}
			if err := env.OnActivate(func(context.Context) error { trace.add("activate projection"); return nil }); err != nil {
				return err
			}
			return env.OnDeactivate(func(context.Context) error { trace.add("dispose projection"); return nil })
		}})
		source := DefineFacet(Facet{Id: "source", Setup: func(env *FacetEnvironment) error {
			trace.add("setup source")
			if err := ProvideService(env, sourceDefinition, constReader("value")); err != nil {
				return err
			}
			if err := env.OnActivate(func(context.Context) error { trace.add("activate source"); return nil }); err != nil {
				return err
			}
			return env.OnDeactivate(func(context.Context) error { trace.add("dispose source"); return nil })
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{projection, source}})
		if got := trace.get(); !reflect.DeepEqual(got, []string{"setup projection", "setup source", "activate source", "activate projection"}) {
			t.Fatalf("trace = %v", got)
		}
		if got := read(t, get(t, sourceRef)); got != "value" {
			t.Fatalf("source = %q", got)
		}
		projected, err := Use(host.Services(), projectionDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if got := read(t, projected); got != "value" {
			t.Fatalf("projection = %q", got)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if got := trace.get(); !reflect.DeepEqual(got[len(got)-2:], []string{"dispose projection", "dispose source"}) {
			t.Fatalf("trace = %v", got)
		}
	})

	t.Run("connects keyed observations only when the observing facet activates", func(t *testing.T) {
		trace := &locked[string]{}
		// Upstream's observer awaits the read, so its trace lands after the activation callbacks. The Go handler runs synchronously inside the delivery, so the same hop is a goroutine that waits for the observer's activation.
		activated := make(chan struct{})
		observer := DefineFacet(Facet{Id: "observer", Setup: func(env *FacetEnvironment) error {
			if err := ObserveService(env, keyedValueDefinition, func(ctx context.Context, service Reader) error {
				go func() {
					<-activated
					value, _ := service.Read(ctx)
					trace.add("observe " + value)
				}()
				return nil
			}); err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error { trace.add("activate observer"); close(activated); return nil })
		}})
		provider := DefineFacet(Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
			values, err := ProvideMany(env, keyedValueDefinition)
			if err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error {
				trace.add("activate provider")
				_, err := values.Spawn("one", constReader("one"))
				return err
			})
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{observer, provider}})
		waitFor(t, "keyed observation", func() bool { return slicesContains(trace.get(), "observe one") })
		if got := trace.get(); !reflect.DeepEqual(got, []string{"activate provider", "activate observer", "observe one"}) {
			t.Fatalf("trace = %v", got)
		}
		remote := loopbackBinding(t, host.Services(), RemoteServiceBindingOptions{Services: []string{keyedValueDefinition.Id()}})
		remoteValues := &locked[string]{}
		if _, err := remote.Observe(keyedValueDefinition.Id(), func(ctx context.Context, service *RemoteService) error {
			value, err := remoteReader{service}.Read(ctx)
			remoteValues.add(value)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := remote.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "remote observation", func() bool { return reflect.DeepEqual(remoteValues.get(), []string{"one"}) })
		_ = remote.Dispose(ctx)
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("routes remotely exposable keyed services through the host provider", func(t *testing.T) {
		observed := &locked[observation]{}
		consumer := observingConsumer(t, "remote-keyed-consumer", observed)
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, keyedProviderFacet(t, "remote-keyed-provider", "A")}})
		waitFor(t, "first observation", func() bool { return len(observed.get()) == 1 })
		first := observed.get()[0]
		if got, err := first.service.Read(first.ctx); err != nil || got != "A" {
			t.Fatalf("first read = %q, %v", got, err)
		}
		if err := host.Reload(ctx, []Facet{keyedProviderFacet(t, "remote-keyed-provider", "B")}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "second observation", func() bool { return len(observed.get()) == 2 })
		if first.ctx.Err() == nil {
			t.Fatal("the replaced generation's context was not aborted")
		}
		_, err := first.service.Read(first.ctx)
		expectErrorContaining(t, err, "observation is closed")
		second := observed.get()[1]
		if got, err := second.service.Read(second.ctx); err != nil || got != "B" {
			t.Fatalf("second read = %q, %v", got, err)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if second.ctx.Err() == nil {
			t.Fatal("the last generation's context was not aborted by dispose")
		}
	})

	for _, tc := range []struct{ name, failing, id string }{
		{"terminates the host when keyed replacement publication fails", UpdateSpawned, "failing-keyed-provider"},
		{"terminates the host when keyed retirement publication fails", UpdateClosed, "failing-keyed-retirement-provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := mustFacetHost(t, FacetOptions{Facets: []Facet{keyedProviderFacet(t, tc.id, "A")}})
			subscription, err := host.Services().Subscribe(keyedValueDefinition.Id(), ServiceKeyed, func(_ context.Context, update ServiceProviderUpdate) {
				if update.Type == tc.failing {
					panic(errors.New("publication failed"))
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			activate(t, subscription)
			expectErrorContaining(t, host.Reload(ctx, []Facet{keyedProviderFacet(t, tc.id, "B")}), "Facet reload failed after cutover")
			expectErrorContaining(t, host.Reload(ctx, nil), "Facet host cannot reload while dead")
			if tc.failing == UpdateSpawned {
				_, err := host.Services().Subscribe(keyedValueDefinition.Id(), ServiceKeyed, func(context.Context, ServiceProviderUpdate) {})
				expectErrorContaining(t, err, "Remote service provider is disposed")
			}
			if err := host.Dispose(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("keeps unrestricted local keyed services process-local across provider reloads", func(t *testing.T) {
		type localObservation struct {
			service LocalKeyedValue
			ctx     context.Context
		}
		observed := &locked[localObservation]{}
		consumer := DefineFacet(Facet{Id: "local-keyed-consumer", Setup: func(env *FacetEnvironment) error {
			return ObserveService(env, localKeyedDefinition, func(ctx context.Context, service LocalKeyedValue) error {
				observed.add(localObservation{service, ctx})
				return nil
			})
		}})
		provider := func(value string) Facet {
			return DefineFacet(Facet{Id: "local-keyed-provider", Setup: func(env *FacetEnvironment) error {
				values, err := ProvideMany(env, localKeyedDefinition)
				if err != nil {
					return err
				}
				return env.OnActivate(func(context.Context) error {
					_, err := values.Spawn("current", LocalKeyedValue(localKeyedImpl{value}))
					return err
				})
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider("A")}})
		waitFor(t, "first observation", func() bool { return len(observed.get()) == 1 })
		first := observed.get()[0]
		if got, err := first.service.Read(); err != nil || got != "A" || first.service.Metadata()["value"] != "A" {
			t.Fatalf("first = %q, %v", got, err)
		}
		for _, entry := range host.Services().Catalogue() {
			if entry.ServiceId == localKeyedDefinition.Id() {
				t.Fatalf("local service published: %v", entry)
			}
		}
		_, err := Use(host.Services(), localKeyedDefinition)
		expectErrorContaining(t, err, "process-local")
		if err := host.Reload(ctx, []Facet{provider("B")}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "second observation", func() bool { return len(observed.get()) == 2 })
		if first.ctx.Err() == nil {
			t.Fatal("the replaced generation's context was not aborted")
		}
		_, err = first.service.Read()
		expectErrorContaining(t, err, "Keyed service "+localKeyedDefinition.Id()+" observation is closed")
		second := observed.get()[1]
		if got, err := second.service.Read(); err != nil || got != "B" || second.service.Metadata()["value"] != "B" {
			t.Fatalf("second = %q, %v", got, err)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if second.ctx.Err() == nil {
			t.Fatal("the last generation's context was not aborted by dispose")
		}
	})

	t.Run("keeps remotely exposable local state replicas stable across provider reloads", func(t *testing.T) {
		sources := &locked[*MutableReplicatedState[*valueDocState]]{}
		revisions := &locked[int]{}
		var watchedRef *ServiceRef[Watched]
		consumer := DefineFacet(Facet{Id: "state-consumer", Setup: func(env *FacetEnvironment) error {
			watchedRef = useService(t, env, watchedDefinition)
			return env.OnActivate(func(context.Context) error {
				stop, err := get(t, watchedRef).State().Subscribe(func(value *valueDocState, _ context.Context, _ ReplicatedStateDelivery) { revisions.add(value.Value) })
				if err != nil {
					return err
				}
				return env.Own(func(context.Context) error { stop(); return nil })
			})
		}})
		provider := func(value int) Facet {
			return DefineFacet(Facet{Id: "state-provider", Setup: func(env *FacetEnvironment) error {
				state, err := NewReplicatedState(&valueDocState{Value: value})
				if err != nil {
					return err
				}
				sources.add(state)
				return ProvideService(env, watchedDefinition, Watched(watchedImpl{state}))
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider(1)}})
		retained := get(t, watchedRef).State()
		if retained.Value().Value != 1 || !reflect.DeepEqual(revisions.get(), []int{1}) {
			t.Fatalf("value = %+v revisions = %v", retained.Value(), revisions.get())
		}
		if err := host.Reload(ctx, []Facet{provider(2)}); err != nil {
			t.Fatal(err)
		}
		if retained.Value().Value != 2 || !reflect.DeepEqual(revisions.get(), []int{1, 2}) {
			t.Fatalf("value = %+v revisions = %v", retained.Value(), revisions.get())
		}
		old := sources.get()[0]
		if err := old.Change(ctx, func(draft *valueDocState) error { draft.Value = 3; return nil }); err != nil {
			t.Fatal(err)
		}
		if retained.Value().Value != 2 || !reflect.DeepEqual(revisions.get(), []int{1, 2}) {
			t.Fatalf("a retired provider's change reached the consumer: %+v %v", retained.Value(), revisions.get())
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				recovered := recover()
				err, _ := recovered.(error)
				expectErrorContaining(t, err, "Facet state-consumer service handles cannot be used while dead")
			}()
			_ = retained.Value()
		}()
	})

	t.Run("scopes singleton service views to each facet lifecycle", func(t *testing.T) {
		consumerRefs := &locked[*ServiceRef[Reader]]{}
		cleanup := &locked[string]{}
		var peerRef *ServiceRef[Reader]
		consumer := func(generation string) Facet {
			return DefineFacet(Facet{Id: "scoped-consumer", Setup: func(env *FacetEnvironment) error {
				ref := useService(t, env, sourceDefinition)
				if again := useService(t, env, sourceDefinition); again != ref {
					t.Error("one facet received two handles for one service")
				}
				consumerRefs.add(ref)
				_, err := ref.Get()
				expectErrorContaining(t, err, "Facet scoped-consumer service handles cannot be used while setting_up")
				return env.OnDeactivate(func(ctx context.Context) error {
					value, err := get(t, ref).Read(ctx)
					cleanup.add(generation + ":" + value)
					return err
				})
			}})
		}
		peer := DefineFacet(Facet{Id: "peer-consumer", Setup: func(env *FacetEnvironment) error { peerRef = useService(t, env, sourceDefinition); return nil }})
		provider := DefineFacet(Facet{Id: "scoped-provider", Setup: func(env *FacetEnvironment) error {
			return ProvideService(env, sourceDefinition, constReader("value"))
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer("A"), peer, provider}})
		if consumerRefs.get()[0] == peerRef {
			t.Fatal("two facets share one handle")
		}
		retainedOld := get(t, consumerRefs.get()[0])
		if err := host.Reload(ctx, []Facet{consumer("B")}); err != nil {
			t.Fatal(err)
		}
		if got := cleanup.get(); !reflect.DeepEqual(got, []string{"A:value"}) {
			t.Fatalf("cleanup = %v", got)
		}
		if consumerRefs.get()[1] == consumerRefs.get()[0] {
			t.Fatal("the replacement generation reuses the old handle")
		}
		_, err := consumerRefs.get()[0].Get()
		expectErrorContaining(t, err, "Facet scoped-consumer service handles cannot be used while dead")
		_, err = retainedOld.Read(ctx)
		expectErrorContaining(t, err, "Facet scoped-consumer service handles cannot be used while dead")
		if got := read(t, get(t, consumerRefs.get()[1])); got != "value" {
			t.Fatalf("new generation = %q", got)
		}
		if got := read(t, get(t, peerRef)); got != "value" {
			t.Fatalf("peer = %q", got)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if got := cleanup.get(); !reflect.DeepEqual(got, []string{"A:value", "B:value"}) {
			t.Fatalf("cleanup = %v", got)
		}
		_, err = consumerRefs.get()[1].Get()
		expectErrorContaining(t, err, "Facet scoped-consumer service handles cannot be used while dead")
		_, err = peerRef.Get()
		expectErrorContaining(t, err, "Facet peer-consumer service handles cannot be used while dead")
	})

	t.Run("owns resources registered during activation", func(t *testing.T) {
		var state *MutableReplicatedState[*valueDocState]
		deliveries := 0
		consumer := DefineFacet(Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
			ref := useService(t, env, watchedDefinition)
			return env.OnActivate(func(context.Context) error {
				stop, err := get(t, ref).State().Subscribe(func(*valueDocState, context.Context, ReplicatedStateDelivery) { deliveries++ })
				if err != nil {
					return err
				}
				return env.Own(func(context.Context) error { stop(); return nil })
			})
		}})
		provider := DefineFacet(Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
			var err error
			if state, err = NewReplicatedState(&valueDocState{}); err != nil {
				return err
			}
			return ProvideService(env, watchedDefinition, Watched(watchedImpl{state}))
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider}})
		if deliveries != 1 {
			t.Fatalf("deliveries = %d", deliveries)
		}
		changeValue := func(value int) {
			if err := state.Change(ctx, func(draft *valueDocState) error { draft.Value = value; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		changeValue(1)
		if deliveries != 2 {
			t.Fatalf("deliveries = %d", deliveries)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		changeValue(2)
		if deliveries != 2 {
			t.Fatalf("deliveries after dispose = %d", deliveries)
		}
	})

	t.Run("provides arbitrary host services through the facet graph", func(t *testing.T) {
		values := &hostValuesImpl{name: "session", use: "host value"}
		consumer := DefineFacet(Facet{Id: "host-service-consumer", Setup: func(env *FacetEnvironment) error {
			ref := useService(t, env, hostValuesDefinition)
			_, err := ref.Get()
			expectErrorContaining(t, err, "Facet host-service-consumer service handles cannot be used while setting_up")
			return env.OnActivate(func(context.Context) error {
				hostValues := get(t, ref)
				if HostValues(values) == hostValues {
					t.Error("the consumer received the raw implementation")
				}
				if hostValues.Name() != "session" || hostValues.Use() != "host value" {
					t.Errorf("host values = %q %q", hostValues.Name(), hostValues.Use())
				}
				return nil
			})
		}})
		provider := DefineFacet(Facet{Id: "host-service-provider", Setup: func(env *FacetEnvironment) error {
			return ProvideService(env, hostValuesDefinition, HostValues(values))
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider}})
		_, err := Use(host.Services(), hostValuesDefinition)
		expectErrorContaining(t, err, "Service test.experimental.host-values is process-local")
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("combines connected services and facet-provided services in one host", func(t *testing.T) {
		newSource := func(definition ServiceDefinition[Reader], value string) (*RemoteServiceProvider, *loopbackSource) {
			provider := newDeliveryProvider(t, SingletonService(definition))
			if err := Provide[Reader](provider, definition, constReader(value)); err != nil {
				t.Fatal(err)
			}
			return provider, &loopbackSource{provider: provider}
		}
		leftProvider, leftSource := newSource(leftValueDefinition, "left")
		rightProvider, rightSource := newSource(rightValueDefinition, "right")
		facet := DefineFacet(Facet{Id: "combined", Setup: func(env *FacetEnvironment) error {
			left, right := useService(t, env, leftValueDefinition), useService(t, env, rightValueDefinition)
			return ProvideService(env, combinedDefinition, Reader(readerFunc(func(ctx context.Context) (string, error) {
				l, err := get(t, left).Read(ctx)
				if err != nil {
					return "", err
				}
				r, err := get(t, right).Read(ctx)
				return l + " " + r, err
			})))
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{facet}, ServiceSources: []RemoteServiceSource{leftSource, rightSource}})
		combined, err := Use(host.Services(), combinedDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if got := read(t, combined); got != "left right" {
			t.Fatalf("combined = %q", got)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		_ = leftProvider.Dispose()
		_ = rightProvider.Dispose()
	})

	t.Run("rejects a service offered by multiple sources", func(t *testing.T) {
		duplicate := &catalogueSource{entries: []ServiceCatalogueEntry{{ServiceId: leftValueDefinition.Id(), Mode: ServiceSingleton}}}
		consumer := DefineFacet(Facet{Id: "duplicate-consumer", Setup: func(env *FacetEnvironment) error {
			_, err := UseService(env, leftValueDefinition)
			return err
		}})
		_, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{duplicate, duplicate}})
		expectErrorContaining(t, err, "Facet host service "+leftValueDefinition.Id()+" is offered by more than one source")
	})

	t.Run("reopens source bindings from changed catalogues for a replacement generation", func(t *testing.T) {
		newProvider := func(definition ServiceDefinition[Reader], value string) *RemoteServiceProvider {
			provider := newDeliveryProvider(t, SingletonService(definition))
			if err := Provide[Reader](provider, definition, constReader(value)); err != nil {
				t.Fatal(err)
			}
			return provider
		}
		leftProvider, rightProvider := newProvider(leftValueDefinition, "left"), newProvider(rightValueDefinition, "right")
		var mu sync.Mutex
		current := leftProvider
		opened, disposed := 0, 0
		source := &reopeningSource{
			catalogue: func() []ServiceCatalogueEntry { mu.Lock(); defer mu.Unlock(); return current.Catalogue() },
			transport: func() RemoteServiceTransport { mu.Lock(); defer mu.Unlock(); return NewLoopbackTransport(current) },
			onOpen:    func() { mu.Lock(); opened++; mu.Unlock() },
			onDispose: func() { mu.Lock(); disposed++; mu.Unlock() },
		}
		values := &locked[string]{}
		consumerFor := func(id string, definition ServiceDefinition[Reader]) Facet {
			return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
				ref := useService(t, env, definition)
				return env.OnActivate(func(ctx context.Context) error {
					value, err := get(t, ref).Read(context.Background())
					values.add(value)
					return err
				})
			}})
		}
		first := mustFacetHost(t, FacetOptions{Facets: []Facet{consumerFor("left-consumer", leftValueDefinition)}, ServiceSources: []RemoteServiceSource{source}})
		if err := first.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		current = rightProvider
		mu.Unlock()
		second := mustFacetHost(t, FacetOptions{Facets: []Facet{consumerFor("right-consumer", rightValueDefinition)}, ServiceSources: []RemoteServiceSource{source}})
		if err := second.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(values.get(), []string{"left", "right"}) || opened != 2 || disposed != 2 {
			t.Fatalf("values = %v opened = %d disposed = %d", values.get(), opened, disposed)
		}
		_ = leftProvider.Dispose()
		_ = rightProvider.Dispose()
	})

	t.Run("rejects missing dependencies, cycles, and asynchronous setup", func(t *testing.T) {
		activated := false
		missing := DefineFacet(Facet{Id: "missing", Setup: func(env *FacetEnvironment) error {
			if _, err := UseService(env, sourceDefinition); err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error { activated = true; return nil })
		}})
		_, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{missing}})
		expectErrorContaining(t, err, "Facet missing requires local/test.experimental.source/singleton, but no facet provides it")
		if activated {
			t.Fatal("a facet with a missing dependency activated")
		}
		first := DefineFacet(Facet{Id: "first", Setup: func(env *FacetEnvironment) error {
			if _, err := UseService(env, projectionDefinition); err != nil {
				return err
			}
			return ProvideService(env, sourceDefinition, constReader("first"))
		}})
		second := DefineFacet(Facet{Id: "second", Setup: func(env *FacetEnvironment) error {
			if _, err := UseService(env, sourceDefinition); err != nil {
				return err
			}
			return ProvideService(env, projectionDefinition, constReader("second"))
		}})
		_, err = CreateFacetHost(ctx, FacetOptions{Facets: []Facet{first, second}})
		expectErrorContaining(t, err, "Facet dependency cycle: first, second")
		// The asynchronous-setup rejection has no Go form: Setup returns when setup is over.
	})
}

func slicesContains(items []string, want string) bool {
	return slices.Contains(items, want)
}

// loopbackSource offers one provider's catalogue and opens a binding over its loopback transport.
type loopbackSource struct{ provider *RemoteServiceProvider }

func (source *loopbackSource) AcceptsUnavailableServices() bool { return false }
func (source *loopbackSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	return source.provider.Catalogue(), nil
}
func (source *loopbackSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	return CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: options.Services, Transport: NewLoopbackTransport(source.provider), AssertAccess: options.AssertAccess, OnError: options.OnError})
}

// catalogueSource offers a fixed catalogue and refuses to open: an ambiguous source must never open.
type catalogueSource struct{ entries []ServiceCatalogueEntry }

func (source *catalogueSource) AcceptsUnavailableServices() bool { return false }
func (source *catalogueSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	return source.entries, nil
}
func (source *catalogueSource) Open(RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	return nil, errors.New("Ambiguous sources must not open")
}

// reopeningSource reads its catalogue and transport at each call and counts openings and disposals.
type reopeningSource struct {
	catalogue func() []ServiceCatalogueEntry
	transport func() RemoteServiceTransport
	onOpen    func()
	onDispose func()
}

func (source *reopeningSource) AcceptsUnavailableServices() bool { return false }
func (source *reopeningSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	return source.catalogue(), nil
}
func (source *reopeningSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	source.onOpen()
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: options.Services, Transport: source.transport(), AssertAccess: options.AssertAccess, OnError: options.OnError})
	if err != nil {
		return nil, err
	}
	return disposeCounting{binding, source.onDispose}, nil
}

type disposeCounting struct {
	*RemoteServiceBinding
	onDispose func()
}

func (counting disposeCounting) Dispose(ctx context.Context) error {
	counting.onDispose()
	return counting.RemoteServiceBinding.Dispose(ctx)
}
