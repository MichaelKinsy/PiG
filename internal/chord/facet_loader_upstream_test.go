package chord

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// Ports packages/chord/test/facet-loader.test.ts. A "retained read method" is a view obtained from a handle before the reload; the view resolves the current binding on every call.

var (
	localGenerationDefinition = func() ServiceDefinition[Reader] {
		definition := DefineService[Reader]("test.experimental.local-generation-value", ServiceOptions{Local: true})
		RegisterServiceView(definition, func(resolve func() (Reader, error)) Reader { return readerView{resolve} })
		return definition
	}()
	remoteGenerationDefinition = defineReader("test.experimental.remote-generation-value")
)

var (
	firstFacet  = DefineFacet(Facet{Id: "first", Setup: func(*FacetEnvironment) error { return nil }})
	secondFacet = DefineFacet(Facet{Id: "second", Setup: func(*FacetEnvironment) error { return nil }})
)

func facetIds(facets []Facet) []string {
	ids := make([]string, len(facets))
	for at, facet := range facets {
		ids[at] = facet.Id
	}
	return ids
}

// renamedContract exposes a method set that differs from Reader.
type renamedContract interface {
	Renamed(ctx context.Context) (string, error)
}

type renamedImpl struct{}

func (renamedImpl) Renamed(context.Context) (string, error) { return "B", nil }

func TestFacetLoader(t *testing.T) {
	ctx := context.Background()

	t.Run("combines loaded facets in loader order and disposes generations in reverse", func(t *testing.T) {
		trace := &locked[string]{}
		loader := func(name string, facet Facet) FacetLoader {
			return facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
				trace.add("load " + name)
				return LoadedFacets{Facets: []Facet{facet}, Dispose: func(context.Context) error { trace.add("dispose " + name); return nil }}, nil
			})
		}
		loaded, err := CombineFacetLoaders(loader("first", firstFacet), loader("second", secondFacet)).Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := facetIds(loaded.Facets); !reflect.DeepEqual(got, []string{"first", "second"}) {
			t.Fatalf("facets = %v", got)
		}
		if err := loaded.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if err := loaded.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if got := trace.get(); !reflect.DeepEqual(got, []string{"load first", "load second", "dispose second", "dispose first"}) {
			t.Fatalf("trace = %v", got)
		}
	})

	t.Run("cleans up loaded facets when a later loader fails", func(t *testing.T) {
		disposed := 0
		failure := errors.New("load failed")
		first := facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
			return LoadedFacets{Facets: []Facet{firstFacet}, Dispose: func(context.Context) error { disposed++; return nil }}, nil
		})
		second := facetLoaderFunc(func(context.Context) (LoadedFacets, error) { return LoadedFacets{}, failure })
		if _, err := CombineFacetLoaders(first, second).Load(ctx); err != failure {
			t.Fatalf("error = %v, want the load failure itself", err)
		}
		if disposed != 1 {
			t.Fatalf("disposed = %d", disposed)
		}
	})

	t.Run("keeps local and RPC service handles stable when their provider facet reloads", func(t *testing.T) {
		trace := &locked[string]{}
		var localRef *ServiceRef[Reader]
		generation := 0
		replacementStarted, continueReplacement := make(chan struct{}), make(chan struct{})
		consumer := DefineFacet(Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
			trace.add("setup consumer")
			localRef = useService(t, env, localGenerationDefinition)
			if err := env.OnActivate(func(ctx context.Context) error {
				value, err := get(t, localRef).Read(context.Background())
				trace.add("activate consumer:" + value)
				return err
			}); err != nil {
				return err
			}
			return env.OnDeactivate(func(context.Context) error { trace.add("deactivate consumer"); return nil })
		}})
		load := func() LoadedFacets {
			name := "B"
			if generation == 0 {
				name = "A"
			}
			generation++
			trace.add("load " + name)
			return LoadedFacets{
				Facets: []Facet{DefineFacet(Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
					trace.add("setup provider " + name)
					implementation := constReader(name)
					if err := ProvideService(env, localGenerationDefinition, implementation); err != nil {
						return err
					}
					if err := ProvideService(env, remoteGenerationDefinition, implementation); err != nil {
						return err
					}
					if err := env.OnActivate(func(context.Context) error {
						trace.add("activate provider " + name)
						if name == "B" {
							close(replacementStarted)
							<-continueReplacement
						}
						return nil
					}); err != nil {
						return err
					}
					return env.OnDeactivate(func(context.Context) error { trace.add("deactivate provider " + name); return nil })
				}})},
				Dispose: func(context.Context) error { trace.add("unload " + name); return nil },
			}
		}
		loadedA := load()
		host := mustFacetHost(t, FacetOptions{Facets: append([]Facet{consumer}, loadedA.Facets...)})
		localRead := get(t, localRef).Read
		remote := loopbackBinding(t, host.Services(), RemoteServiceBindingOptions{Services: ServiceIDs(remoteGenerationDefinition.Id())})
		originalRemote := use(t, remote, remoteGenerationDefinition.Id())
		remoteRead := remoteReader{originalRemote}.Read
		if err := remote.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		expectReads := func(want string) {
			t.Helper()
			for name, read := range map[string]func(context.Context) (string, error){"local": localRead, "remote": remoteRead} {
				if got, err := read(ctx); err != nil || got != want {
					t.Fatalf("%s read = %q, %v, want %q", name, got, err, want)
				}
			}
		}
		expectReads("A")

		invalid := DefineFacet(Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
			return ProvideService(env, localGenerationDefinition, constReader("invalid"))
		}})
		expectErrorContaining(t, host.Reload(ctx, []Facet{invalid}), "Reloaded facet provider must preserve its service requirements and provisions")
		expectReads("A")

		loadedB := load()
		reloaded := make(chan error, 1)
		go func() { reloaded <- host.Reload(ctx, loadedB.Facets) }()
		<-replacementStarted
		expectReads("A")
		close(continueReplacement)
		if err := <-reloaded; err != nil {
			t.Fatal(err)
		}
		if err := loadedA.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if again := use(t, remote, remoteGenerationDefinition.Id()); again.facade != originalRemote.facade {
			t.Fatal("the remote handle changed across the reload")
		}
		expectReads("B")
		_ = remote.Dispose(ctx)
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if err := loadedB.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		want := []string{"load A", "setup consumer", "setup provider A", "activate provider A", "activate consumer:A", "load B", "setup provider B", "activate provider B", "deactivate provider A", "unload A", "deactivate consumer", "deactivate provider B", "unload B"}
		if got := trace.get(); !reflect.DeepEqual(got, want) {
			t.Fatalf("trace = %v\nwant    %v", got, want)
		}
	})

	t.Run("rejects remote singleton member shape changes before reload cutover", func(t *testing.T) {
		var retainedRef *ServiceRef[Reader]
		var mu sync.Mutex
		providerDisposed := false
		consumer := DefineFacet(Facet{Id: "shape-consumer", Setup: func(env *FacetEnvironment) error {
			retainedRef = useService(t, env, remoteGenerationDefinition)
			return nil
		}})
		provider := DefineFacet(Facet{Id: "shape-provider", Setup: func(env *FacetEnvironment) error {
			if err := ProvideService(env, remoteGenerationDefinition, constReader("A")); err != nil {
				return err
			}
			return env.OnDeactivate(func(context.Context) error { mu.Lock(); providerDisposed = true; mu.Unlock(); return nil })
		}})
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider}})
		renamed := DefineFacet(Facet{Id: "shape-provider", Setup: func(env *FacetEnvironment) error {
			return ProvideService(env, DefineService[renamedContract](remoteGenerationDefinition.Id()), renamedContract(renamedImpl{}))
		}})
		expectErrorContaining(t, host.Reload(ctx, []Facet{renamed}), "replacement must preserve its member shape")
		mu.Lock()
		disposedEarly := providerDisposed
		mu.Unlock()
		if disposedEarly {
			t.Fatal("the old provider was disposed by a rejected reload")
		}
		if got := read(t, get(t, retainedRef)); got != "A" {
			t.Fatalf("retained read = %q", got)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if !providerDisposed {
			t.Fatal("dispose did not run the provider's cleanup")
		}
	})

	t.Run("terminates the host when old cleanup fails after cutover", func(t *testing.T) {
		cleanupFailure := errors.New("cleanup failed")
		provider := func(name string, failCleanup bool) Facet {
			return DefineFacet(Facet{Id: "cleanup-provider", Setup: func(env *FacetEnvironment) error {
				if err := ProvideService(env, remoteGenerationDefinition, constReader(name)); err != nil {
					return err
				}
				return env.OnDeactivate(func(context.Context) error {
					if failCleanup {
						return cleanupFailure
					}
					return nil
				})
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{provider("A", true)}})
		err := host.Reload(ctx, []Facet{provider("B", false)})
		expectErrorContaining(t, err, "Facet reload failed after cutover")
		if !errors.Is(err, cleanupFailure) {
			t.Fatalf("error = %v does not carry the cleanup failure", err)
		}
		expectErrorContaining(t, host.Reload(ctx, nil), "Facet host cannot reload while dead")
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cleans failed candidate activation in reverse dependency order", func(t *testing.T) {
		failure := errors.New("consumer activation failed")
		trace := &locked[string]{}
		provider := func(name string) Facet {
			return DefineFacet(Facet{Id: "ordered-provider", Setup: func(env *FacetEnvironment) error {
				if err := ProvideService(env, remoteGenerationDefinition, constReader(name)); err != nil {
					return err
				}
				if err := env.OnActivate(func(context.Context) error { trace.add("activate provider " + name); return nil }); err != nil {
					return err
				}
				return env.OnDeactivate(func(context.Context) error { trace.add("deactivate provider " + name); return nil })
			}})
		}
		consumer := func(name string, fail bool) Facet {
			return DefineFacet(Facet{Id: "ordered-consumer", Setup: func(env *FacetEnvironment) error {
				useService(t, env, remoteGenerationDefinition)
				if err := env.OnActivate(func(context.Context) error {
					trace.add("activate consumer " + name)
					if fail {
						return failure
					}
					return nil
				}); err != nil {
					return err
				}
				return env.OnDeactivate(func(context.Context) error { trace.add("deactivate consumer " + name); return nil })
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer("A", false), provider("A")}})
		trace.mu.Lock()
		trace.items = nil
		trace.mu.Unlock()
		if err := host.Reload(ctx, []Facet{consumer("B", true), provider("B")}); err != failure {
			t.Fatalf("error = %v, want the activation failure itself", err)
		}
		want := []string{"activate provider B", "activate consumer B", "deactivate consumer B", "deactivate provider B"}
		if got := trace.get(); !reflect.DeepEqual(got, want) {
			t.Fatalf("trace = %v", got)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("terminates the host when replacement publication fails after cutover", func(t *testing.T) {
		provider := func(name string) Facet {
			return DefineFacet(Facet{Id: "publication-provider", Setup: func(env *FacetEnvironment) error {
				return ProvideService(env, remoteGenerationDefinition, constReader(name))
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{provider("A")}})
		subscription, err := host.Services().Subscribe(remoteGenerationDefinition.Id(), ServiceSingleton, func(context.Context, ServiceProviderUpdate) { panic(errors.New("publication failed")) })
		if err != nil {
			t.Fatal(err)
		}
		activate(t, subscription)
		expectErrorContaining(t, host.Reload(ctx, []Facet{provider("B")}), "Facet reload failed after cutover")
		expectErrorContaining(t, host.Reload(ctx, nil), "Facet host cannot reload while dead")
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("keeps the old generation active when replacement activation fails before cutover", func(t *testing.T) {
		failure := errors.New("replacement activation failed")
		trace := &locked[string]{}
		var retainedRef *ServiceRef[Reader]
		consumer := DefineFacet(Facet{Id: "terminal-consumer", Setup: func(env *FacetEnvironment) error {
			retainedRef = useService(t, env, remoteGenerationDefinition)
			return env.OnDeactivate(func(context.Context) error { trace.add("deactivate consumer"); return nil })
		}})
		provider := func(name string, fail bool) Facet {
			return DefineFacet(Facet{Id: "terminal-provider", Setup: func(env *FacetEnvironment) error {
				if err := ProvideService(env, remoteGenerationDefinition, constReader(name)); err != nil {
					return err
				}
				if err := env.OnActivate(func(context.Context) error {
					trace.add("activate " + name)
					if fail {
						return failure
					}
					return nil
				}); err != nil {
					return err
				}
				return env.OnDeactivate(func(context.Context) error { trace.add("deactivate " + name); return nil })
			}})
		}
		host := mustFacetHost(t, FacetOptions{Facets: []Facet{consumer, provider("A", false)}})
		if got := read(t, get(t, retainedRef)); got != "A" {
			t.Fatalf("read = %q", got)
		}
		if err := host.Reload(ctx, []Facet{provider("B", true)}); err != failure {
			t.Fatalf("error = %v, want the activation failure itself", err)
		}
		if got := trace.get(); !reflect.DeepEqual(got, []string{"activate A", "activate B", "deactivate B"}) {
			t.Fatalf("trace = %v", got)
		}
		if got := read(t, get(t, retainedRef)); got != "A" {
			t.Fatalf("read after failed reload = %q", got)
		}
		if err := host.Reload(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := host.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		want := []string{"activate A", "activate B", "deactivate B", "deactivate consumer", "deactivate A"}
		if got := trace.get(); !reflect.DeepEqual(got, want) {
			t.Fatalf("trace = %v", got)
		}
	})

	t.Run("creates a reusable static loader", func(t *testing.T) {
		loader := CreateStaticFacetLoader([]Facet{firstFacet})
		first, err := loader.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		second, err := loader.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(facetIds(first.Facets), []string{"first"}) || !reflect.DeepEqual(facetIds(second.Facets), []string{"first"}) {
			t.Fatalf("facets = %v %v", facetIds(first.Facets), facetIds(second.Facets))
		}
		if err := first.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
		if err := second.Dispose(ctx); err != nil {
			t.Fatal(err)
		}
	})
}
