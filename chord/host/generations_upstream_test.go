package host

// Ports packages/chord/test/generations.test.ts (Pi 00d8d049): old code generations and disposed objects must be
// collectable. A "generation" is the code of one facet module evaluation: here a facet built by a function call, whose
// factory closes over a per-generation sentinel. Pi registers the sentinels in a FinalizationRegistry under V8's
// garbage collector; Go has no reachability oracle but the collector, so each sentinel gets a runtime.AddCleanup and
// the tests assert the same retention property: a generation is alive while anything Chord keeps references its code.

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// generationSentinel stands for the code of one generation. It is not zero-sized, so every one has its own address, and
// it holds a pointer, so the allocator gives it a block of its own: a pointer-free object under 16 bytes shares a tiny
// block with its neighbours, and is collected, and its cleanup run, only with all of them.
type generationSentinel struct {
	generation int
	_          *generationSentinel
}

// generationTracker tracks which sentinels are still reachable.
type generationTracker struct {
	mu    sync.Mutex
	alive map[int]struct{}
}

func newGenerationTracker() *generationTracker { return &generationTracker{alive: map[int]struct{}{}} }

// trackGeneration registers value as the sentinel of id: id stays alive until value is collected.
func trackGeneration[T any](tracker *generationTracker, id int, value *T) *T {
	tracker.mu.Lock()
	tracker.alive[id] = struct{}{}
	tracker.mu.Unlock()
	runtime.AddCleanup(value, func(id int) {
		tracker.mu.Lock()
		delete(tracker.alive, id)
		tracker.mu.Unlock()
	}, id)
	return value
}

func (tracker *generationTracker) sorted() []int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	ids := make([]int, 0, len(tracker.alive))
	for id := range tracker.alive {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// collect collects garbage until only expected is alive, at most five seconds (cleanups run some time after a
// collection), and returns what is alive then.
func (tracker *generationTracker) collect(expected ...int) []int {
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		if got := tracker.sorted(); slices.Equal(got, expected) || time.Now().After(deadline) {
			return got
		}
	}
}

var generationStore = Local("t.gen-store", LocalDef{
	Version: 1,
	Input:   tObject(),
	Members: Fields[any]{{"get", Method(tNull(), tNumber())}, {"count", tNumber()}},
})

var generationRoutes = Registry[any](DefineRegistry[func() float64]("t.gen-routes"))

const generationReloads = 6

func generationCount(self Self) (Readable[any], error) {
	return self.State("count", tNumber(), float64(0), nil)
}

// generations.test.ts "a reference held across reloads keeps only the current generation alive".
func TestGenerationGCAReferenceHeldAcrossReloadsKeepsOnlyTheCurrentGenerationAlive(t *testing.T) {
	generations := newGenerationTracker()
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	facet := func(generation int) *Facet {
		sentinel := trackGeneration(generations, generation, &generationSentinel{generation: generation})
		return &Facet{Name: "store", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(generationStore, func(_ any, _ Principal, self Self) (Implementation, error) {
				count, err := generationCount(self)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("count", count, "get", methodFunc(func(any, Ctx) (any, error) {
					return float64(sentinel.generation), nil
				})), nil
			}, ServeOptions{})
		}}}
	}
	if err := host.Install(*facet(0), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reloadGet(t, host, generationStore, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	for generation := 1; generation <= generationReloads; generation++ {
		if _, err := host.Reload(reloadPlugins(facet(generation)), ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := mustCall(t, ref, "get", nil, nil); got != float64(generation) {
			t.Fatalf("get = %v, want %d", got, generation)
		}
	}
	if got := generations.collect(generationReloads); !slices.Equal(got, []int{generationReloads}) {
		t.Fatalf("alive generations = %v, want [%d]", got, generationReloads)
	}
	runtime.KeepAlive(ref)
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

// generations.test.ts: a lifetime "host" object with state, a kept resource, a listener, a task and
// registrations keeps only the current generation alive.
func TestGenerationGCAHostObjectWithStateAKeptResourceAListenerATaskAndRegistrationsKeepsOnlyTheCurrentGenerationAlive(t *testing.T) {
	generations := newGenerationTracker()
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	facet := func(generation int) *Facet {
		sentinel := trackGeneration(generations, generation, &generationSentinel{generation: generation})
		return &Facet{Name: "store", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(generationStore, func(_ any, _ Principal, self Self) (Implementation, error) {
				count, err := generationCount(self)
				if err != nil {
					return nil, err
				}
				// Kept across reloads (same version): adopts each new declaration's close.
				if _, err := self.Resource("conn", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { return chordjson.ObjectOf("open", true), nil },
					Close:   func(any) error { _ = sentinel.generation; return nil },
				}); err != nil {
					return nil, err
				}
				if err := self.On("count", count, func(any, Ctx) error {
					_ = sentinel.generation
					return nil
				}); err != nil {
					return nil, err
				}
				if err := self.Task(func(ctx Ctx) error {
					<-ctx.Signal().Done()
					_ = sentinel.generation
					return nil
				}); err != nil {
					return nil, err
				}
				if err := self.Register(generationRoutes, []Entry{{Name: "generation", Value: func() float64 { return float64(sentinel.generation) }}}); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("count", count, "get", methodFunc(func(any, Ctx) (any, error) {
					return float64(sentinel.generation), nil
				})), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	if err := host.Install(*facet(0), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	for generation := 1; generation <= generationReloads; generation++ {
		if _, err := host.Reload(reloadPlugins(facet(generation)), ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		route, ok := host.Registry(generationRoutes).Get("generation", nil)
		if !ok || route.(func() float64)() != float64(generation) {
			t.Fatalf("registered generation = %v %v, want %d", route, ok, generation)
		}
	}
	if got := generations.collect(generationReloads); !slices.Equal(got, []int{generationReloads}) {
		t.Fatalf("alive generations = %v, want [%d]", got, generationReloads)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

// generations.test.ts "abort listeners that calls and tasks add to their ctx.signal do not outlive them" (Go: a
// context.AfterFunc on ctx.Signal() is the listener).
func TestGenerationGCAbortListenersThatCallsAndTasksAddToTheirCtxSignalDoNotOutliveThem(t *testing.T) {
	generations := newGenerationTracker()
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	facet := func(generation int) *Facet {
		sentinel := trackGeneration(generations, generation, &generationSentinel{generation: generation})
		return &Facet{Name: "store", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(generationStore, func(_ any, _ Principal, self Self) (Implementation, error) {
				count, err := generationCount(self)
				if err != nil {
					return nil, err
				}
				if err := self.Task(func(ctx Ctx) error {
					context.AfterFunc(ctx.Signal(), func() { _ = sentinel.generation })
					<-ctx.Signal().Done()
					_ = sentinel.generation
					return nil
				}); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("count", count, "get", methodFunc(func(_ any, ctx Ctx) (any, error) {
					context.AfterFunc(ctx.Signal(), func() { _ = sentinel.generation })
					return float64(sentinel.generation), nil
				})), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	if err := host.Install(*facet(0), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	for generation := 1; generation <= generationReloads; generation++ {
		if _, err := host.Reload(reloadPlugins(facet(generation)), ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
		got, err := host.Root(func(ctx Ctx) (any, error) {
			ref, err := ctx.Get(generationStore, map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			return callMember(ref, "get", nil, ctx)
		}, RootOptions{})
		if err != nil || got != float64(generation) {
			t.Fatalf("get = %v, %v, want %d", got, err, generation)
		}
	}
	if got := generations.collect(generationReloads); !slices.Equal(got, []int{generationReloads}) {
		t.Fatalf("alive generations = %v, want [%d]", got, generationReloads)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

// generations.test.ts "disposed objects are not kept once their disposal finished".
func TestGenerationGCDisposedObjectsAreNotKeptOnceTheirDisposalFinished(t *testing.T) {
	objects := newGenerationTracker()
	item := Local("t.gen-item", LocalDef{
		Version: 1,
		Input:   tObject("id", tNumber()),
		Members: Fields[any]{{"id", Method(tNull(), tNumber())}},
	})
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	if err := host.Serve(item, func(input any, _ Principal, self Self) (Implementation, error) {
		// Pi tracks the normalized input, which its object holds: it stands for the object. Go's input is a map, which
		// has no address to track; the object is its object, whose own reachability is what the host must release.
		fields, ok := input.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("normalized input is %T", input)
		}
		id, _ := fields["id"].(float64)
		trackGeneration(objects, int(id), self.(*SelfImpl).object)
		return chordjson.ObjectOf("id", methodFunc(func(any, Ctx) (any, error) { return id, nil })), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	// In a function of its own, so no reference outlives the iteration in this frame.
	use := func(id int) {
		ref, err := host.Hold(item, map[string]any{"id": float64(id)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustCall(t, ref, "id", nil, nil); got != float64(id) {
			t.Fatalf("id = %v, want %d", got, id)
		}
		ref.Release()
	}
	for id := range 50 {
		use(id)
	}
	time.Sleep(10 * time.Millisecond)
	if got := objects.collect(); len(got) != 0 {
		t.Fatalf("alive objects = %v, want none", got)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}
