package host

// Ports packages/chord/test/host.test.ts (Pi v1.2.0): activation and lifetime, principals and variants, registries
// and composition, on one host. Pi's `await tick()` (one setImmediate) becomes waitFor on the state Pi asserts after
// it: Go's loop tasks and owned tasks have no setImmediate boundary to wait for.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// stringLog is a log written from the loop and from owned tasks.
type stringLog struct {
	mu    sync.Mutex
	items []string
}

func (log *stringLog) add(item string) {
	log.mu.Lock()
	log.items = append(log.items, item)
	log.mu.Unlock()
}

func (log *stringLog) list() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.items)
}

func (log *stringLog) equals(want ...string) bool { return slices.Equal(log.list(), want) }

// errorLog is quiet's errors array.
type errorLog struct {
	mu   sync.Mutex
	list []error
}

func (log *errorLog) add(err error) {
	log.mu.Lock()
	log.list = append(log.list, err)
	log.mu.Unlock()
}

func (log *errorLog) items() []error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.list)
}

// quiet is host.test.ts quiet: a host whose background errors go to errors (dropped when nil).
func quiet(t *testing.T, errs *errorLog) *HostImpl {
	t.Helper()
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(err error) {
		if errs != nil {
			errs.add(err)
		}
	}})
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func inputField(input any, key string) any {
	_, get, ok := objectEntries(input)
	if !ok {
		return nil
	}
	return get(key)
}

func emptyLocal(name string) *Contract {
	return Local(name, LocalDef{Version: 1, Input: tObject()})
}

func mustServe(t *testing.T, host *HostImpl, typ *Contract, factory Factory, options ServeOptions) {
	t.Helper()
	if err := host.Serve(typ, factory, options); err != nil {
		t.Fatal(err)
	}
}

func mustRef(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Hold(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func asChord(err error) *ChordError {
	if chordError, ok := errors.AsType[*ChordError](err); ok {
		return chordError
	}
	return nil
}

func principalOf(fields ...any) *GetOptions {
	object := map[string]any{}
	for index := 0; index < len(fields); index += 2 {
		object[fields[index].(string)] = fields[index+1]
	}
	return &GetOptions{Principal: object, HasPrincipal: true}
}

// asyncCall is Pi's pending method call: a call started now and awaited later.
func asyncCall(ref Ref, name string, args any, ctx Ctx) <-chan [2]any {
	done := make(chan [2]any, 1)
	go func() {
		value, err := callMember(ref, name, args, ctx)
		done <- [2]any{value, err}
	}()
	return done
}

var hostCountSchema = tObject("n", tNumber())

// hostCounter is host.test.ts Counter.
var hostCounter = Local("t.counter", LocalDef{
	Version: 1,
	Input:   tObject("name", tString()),
	Members: Fields[any]{
		{"count", hostCountSchema},
		{"add", Method(tNumber(), tNumber())},
	},
})

// hostServeCounter is host.test.ts serveCounter.
func hostServeCounter(t *testing.T, host *HostImpl, log *stringLog, linger time.Duration) {
	t.Helper()
	mustServe(t, host, hostCounter, func(input any, _ Principal, self Self) (Implementation, error) {
		name := inputField(input, "name").(string)
		log.add("factory " + name)
		count, err := self.State("count", hostCountSchema, delta.JsonObjectOf("n", float64(0)), nil)
		if err != nil {
			return nil, err
		}
		conn, err := self.Resource("conn", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				log.add("open " + name)
				return map[string]any{"name": name}, nil
			},
			Close: func(any) error {
				log.add("close " + name)
				return nil
			},
		})
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"count", count,
			"conn", conn,
			"add", methodFunc(func(args any, ctx Ctx) (any, error) {
				if err := count.Change(ctx, func(draft any) {
					object := draft.(*delta.Object)
					_ = object.Set("n", object.Get("n").(float64)+args.(float64))
				}); err != nil {
					return nil, err
				}
				return countN(count.Value()), nil
			}),
		), nil
	}, ServeOptions{Linger: linger})
}

// host.test.ts:67-84 it("activates, goes live, binds members late, disposes after release")
func TestHostActivatesGoesLiveBindsMembersLateDisposesAfterRelease(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	hostServeCounter(t, host, log, 0)
	var ref Ref
	var pending <-chan [2]any
	// Pi's synchronous segment up to the first await runs as one section of the host's loop.
	host.run(func() {
		ref = mustRef(t, host, hostCounter, map[string]any{"name": "a"}, nil)
		if ref.Status() != RefLoading {
			t.Fatalf("status %s", ref.Status())
		}
		requireNotReady(t, "count before live", func() { readable(t, ref, "count").Value() })
		pending = asyncCall(ref, "add", float64(2), nil)
	})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if ref.Status() != RefLive {
		t.Fatalf("status %s", ref.Status())
	}
	if result := <-pending; result[1] != nil || result[0] != float64(2) {
		t.Fatalf("pending add = %v", result)
	}
	if got := jsonText(t, readable(t, ref, "count").Value()); got != `{"n":2}` {
		t.Fatalf("count = %s", got)
	}
	ref.Release()
	if ref.Status() != RefReleased {
		t.Fatalf("status %s", ref.Status())
	}
	if _, err := callMember(ref, "add", float64(1), nil); !IsChordError(err, CodeReleased) {
		t.Fatalf("add after release: %v", err)
	}
	waitFor(t, "close a", func() bool { return len(log.list()) == 3 })
	if !log.equals("factory a", "open a", "close a") {
		t.Fatalf("log %v", log.list())
	}
}

// host.test.ts:86-100 it("coalesces concurrent gets and keeps the object while held")
func TestHostCoalescesConcurrentGetsAndKeepsTheObjectWhileHeld(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	hostServeCounter(t, host, log, 0)
	a := mustRef(t, host, hostCounter, map[string]any{"name": "x"}, nil)
	b := mustRef(t, host, hostCounter, map[string]any{"name": "x"}, nil)
	if err := errors.Join(readyErr(t, a), readyErr(t, b)); err != nil {
		t.Fatal(err)
	}
	if !log.equals("factory x", "open x") {
		t.Fatalf("log %v", log.list())
	}
	a.Release()
	if value := mustCall(t, b, "add", float64(1), nil); value != float64(1) {
		t.Fatalf("b.add = %v", value)
	}
	if !log.equals("factory x", "open x") {
		t.Fatalf("closed while held: %v", log.list())
	}
	b.Release()
	waitFor(t, "close x", func() bool { return len(log.list()) == 3 })
	if !log.equals("factory x", "open x", "close x") {
		t.Fatalf("log %v", log.list())
	}
}

// host.test.ts:102-115 it("lingers after the last release and revives on a new get")
func TestHostLingersAfterTheLastReleaseAndRevivesOnANewGet(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	hostServeCounter(t, host, log, 30*time.Millisecond)
	a := mustRef(t, host, hostCounter, map[string]any{"name": "l"}, nil)
	mustCall(t, a, "add", float64(5), nil)
	a.Release()
	b := mustRef(t, host, hostCounter, map[string]any{"name": "l"}, nil)
	if err := readyErr(t, b); err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, readable(t, b, "count").Value()); got != `{"n":5}` {
		t.Fatalf("revived count = %s", got)
	}
	b.Release()
	waitFor(t, "close l", func() bool { return len(log.list()) == 3 })
	if !log.equals("factory l", "open l", "close l") {
		t.Fatalf("log %v", log.list())
	}
}

// host.test.ts:117-151 it("disposes an abandoned activation once it settles")
func TestHostDisposesAnAbandonedActivationOnceItSettles(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	opens := make(chan chan string, 2)
	slow := emptyLocal("t.slow")
	mustServe(t, host, slow, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.Resource("r", ResourceDeclaration[any]{
			Version: 1,
			Open: func(options ResourceOpenOptions) (any, error) {
				release := make(chan string)
				opens <- release
				// Pi's abort listener (signal.addEventListener("abort", ...)) watches the signal only; it must not
				// share the release channel with the open, or it may take the value meant for the open.
				returned := make(chan struct{})
				defer close(returned)
				go func() {
					select {
					case <-options.Signal.Done():
						log.add("abort seen")
					case <-returned:
					}
				}()
				return <-release, nil
			},
			Close: func(any) error {
				log.add("closed")
				return nil
			},
		})
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	ref := mustRef(t, host, slow, map[string]any{}, nil)
	release := <-opens
	ref.Release()
	waitFor(t, "abort seen", func() bool { return log.equals("abort seen") })
	release <- "r"
	waitFor(t, "closed", func() bool { return len(log.list()) == 2 })
	if !log.equals("abort seen", "closed") {
		t.Fatalf("log %v", log.list())
	}
	again := mustRef(t, host, slow, map[string]any{}, nil)
	release = <-opens
	release <- "r"
	if err := readyErr(t, again); err != nil {
		t.Fatal(err)
	}
	if again.Status() != RefLive {
		t.Fatalf("status %s", again.Status())
	}
}

// host.test.ts:153-182 it("propagates activation failures to dependents and retries on the next get")
func TestHostPropagatesActivationFailuresToDependentsAndRetriesOnTheNextGet(t *testing.T) {
	host := quiet(t, nil)
	var mu sync.Mutex
	fail := true
	leaf, parent := emptyLocal("t.leaf"), emptyLocal("t.parent")
	mustServe(t, host, leaf, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.Resource("r", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				if fail {
					return nil, errors.New("boom")
				}
				return float64(1), nil
			},
		})
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	mustServe(t, host, parent, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.Get(leaf, map[string]any{}, nil)
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	ref := mustRef(t, host, parent, map[string]any{}, nil)
	// A plain error from a dependency surfaces as unavailable, with the original as cause.
	if err := readyErr(t, ref); !IsChordError(err, CodeUnavailable) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("ready: %v", err)
	}
	if ref.Status() != RefGone {
		t.Fatalf("status %s", ref.Status())
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	retry := mustRef(t, host, parent, map[string]any{}, nil)
	if err := readyErr(t, retry); err != nil {
		t.Fatal(err)
	}
	if retry.Status() != RefLive {
		t.Fatalf("status %s", retry.Status())
	}
}

// host.test.ts:184-213 it("unwinds declarations when a factory throws and rejects async factories"). A Go factory returns its implementation synchronously, so the async-factory half (Pi rejects a
// Promise-returning factory with "must be synchronous") is the skipped subtest.
func TestHostUnwindsDeclarationsWhenAFactoryThrowsAndRejectsAsyncFactories(t *testing.T) {
	host := quiet(t, nil)
	leaf := emptyLocal("t.leaf2")
	bad := Local("t.bad", LocalDef{Version: 1, Input: tObject("async", tBoolean())})
	var mu sync.Mutex
	leafLive := 0
	mustServe(t, host, leaf, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.Resource("r", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				mu.Lock()
				leafLive++
				mu.Unlock()
				return nil, nil
			},
			Close: func(any) error {
				mu.Lock()
				leafLive--
				mu.Unlock()
				return nil
			},
		})
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	mustServe(t, host, bad, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Get(leaf, map[string]any{}, nil); err != nil {
			return nil, err
		}
		return nil, errors.New("factory failed")
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, bad, map[string]any{"async": false}, nil)); err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("ready: %v", err)
	}
	t.Run("async factory", func(t *testing.T) {
		t.Skip("Pi host.test.ts:207: a factory returning a Promise has no Go form (Factory returns its implementation synchronously)")
	})
	waitFor(t, "leaf disposed", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return leafLive == 0
	})
}

// host.test.ts:215-224 it("rejects duplicate declaration names")
func TestHostRejectsDuplicateDeclarationNames(t *testing.T) {
	host := quiet(t, nil)
	dup := emptyLocal("t.dup")
	mustServe(t, host, dup, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.State("a", tObject(), delta.JsonObjectOf(), nil); err != nil {
			return nil, err
		}
		if _, err := self.State("a", tObject(), delta.JsonObjectOf(), nil); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(), nil
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, dup, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "duplicate state name a") {
		t.Fatalf("ready: %v", err)
	}
}

// host.test.ts:226-239 it("detects hold cycles")
func TestHostDetectsRefCycles(t *testing.T) {
	host := quiet(t, nil)
	a, b := emptyLocal("t.cycle-a"), emptyLocal("t.cycle-b")
	holding := func(other *Contract) Factory {
		return func(_ any, _ Principal, self Self) (Implementation, error) {
			_, err := self.Get(other, map[string]any{}, nil)
			return chordjson.ObjectOf(), err
		}
	}
	mustServe(t, host, a, holding(b), ServeOptions{})
	mustServe(t, host, b, holding(a), ServeOptions{})
	if err := readyErr(t, mustRef(t, host, a, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("ready: %v", err)
	}
}

// host.test.ts:241-280 it("closes resources in reverse order, runs all steps and aggregates errors")
func TestHostClosesResourcesInReverseOrderRunsAllStepsAndAggregatesErrors(t *testing.T) {
	errs := &errorLog{}
	host := quiet(t, errs)
	log := &stringLog{}
	multi := emptyLocal("t.multi")
	mustServe(t, host, multi, func(_ any, _ Principal, self Self) (Implementation, error) {
		a, err := self.Resource("a", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return "a", nil },
			Close:   func(any) error { log.add("close a"); return nil },
		})
		if err != nil {
			return nil, err
		}
		if _, err := self.Resource("b", ResourceDeclaration[any]{
			Version: 1,
			Deps:    []any{a},
			Open:    func(ResourceOpenOptions) (any, error) { return "b", nil },
			Close: func(any) error {
				log.add("close b")
				return errors.New("b failed")
			},
		}); err != nil {
			return nil, err
		}
		_, err = self.Resource("c", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return "c", nil },
			Close: func(any) error {
				log.add("close c")
				return errors.New("c failed")
			},
		})
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	ref := mustRef(t, host, multi, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	ref.Release()
	waitFor(t, "the disposal error", func() bool { return len(errs.items()) > 0 })
	if items := log.list(); len(items) != 3 || items[2] != "close a" {
		t.Fatalf("log %v", items)
	}
	var aggregate *AggregateError
	if !errors.As(errs.items()[0], &aggregate) || len(aggregate.Errors) != 2 {
		t.Fatalf("errors %v", errs.items())
	}
}

// host.test.ts:282-293 it("releases ctx.get holds when the call settles")
func TestHostReleasesCtxGetRefsWhenTheCallSettles(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	hostServeCounter(t, host, log, 0)
	value, err := host.Root(func(ctx Ctx) (any, error) {
		counter, err := ctx.Get(hostCounter, map[string]any{"name": "c"}, nil)
		if err != nil {
			return nil, err
		}
		return callMember(counter, "add", float64(3), ctx)
	}, RootOptions{})
	if err != nil || value != float64(3) {
		t.Fatalf("root = %v, %v", value, err)
	}
	waitFor(t, "close c", func() bool { return len(log.list()) == 3 })
	if !log.equals("factory c", "open c", "close c") {
		t.Fatalf("log %v", log.list())
	}
}

// host.test.ts:296-315 it("refuses a second declaration of one name per kind; kinds have their own names")
func TestHostRefusesASecondDeclarationOfOneNamePerKindKindsHaveTheirOwnNames(t *testing.T) {
	host := quiet(t, nil)
	twice := Local("t.twice", LocalDef{Version: 1, Input: tObject("kind", tString())})
	mustServe(t, host, twice, func(input any, _ Principal, self Self) (Implementation, error) {
		kind := inputField(input, "kind").(string)
		open := ResourceDeclaration[any]{Version: 1, Open: func(ResourceOpenOptions) (any, error) { return nil, nil }}
		if _, err := self.State("x", tNumber(), float64(0), nil); err != nil {
			return nil, err
		}
		if _, err := self.Resource("x", open); err != nil {
			return nil, err
		}
		y, err := self.State("y", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		nothing := func(any, Ctx) error { return nil }
		if err := self.On("x", y, nothing); err != nil {
			return nil, err
		}
		switch kind {
		case "state":
			_, err = self.State("x", tNumber(), float64(1), nil)
		case "resource":
			_, err = self.Resource("x", open)
		case "listener":
			var z *Cell[any]
			if z, err = self.State("z", tNumber(), float64(0), nil); err == nil {
				err = self.On("x", z, nothing)
			}
		}
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, twice, map[string]any{"kind": "none"}, nil)); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"state", "resource", "listener"} {
		err := readyErr(t, mustRef(t, host, twice, map[string]any{"kind": kind}, nil))
		chordError := asChord(err)
		if chordError == nil || chordError.Code != CodeInvalid || chordError.Reason != "duplicate" || !strings.Contains(chordError.Message, kind+" name x") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

// host.test.ts:317-343 it("methods without arguments are called with ctx alone; void methods resolve to undefined"). Pi's
// `tool.ping()` and `tool.ping(ctx)` both pass nil args here: a method without arguments ignores them.
func TestHostMethodsWithoutArgumentsAreCalledWithCtxAloneVoidMethodsResolveToUndefined(t *testing.T) {
	host := quiet(t, nil)
	tool := Local("t.tool", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"ping", Method(nil, nil)},
		{"count", Method(nil, tNumber())},
		{"note", Method(tString(), nil)},
	}})
	seen := &stringLog{}
	typeOfRootID := func(ctx Ctx) string {
		if ctx.RootID() != "" {
			return "string"
		}
		return "undefined"
	}
	mustServe(t, host, tool, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf(
			"ping", methodFunc(func(_ any, ctx Ctx) (any, error) {
				seen.add(typeOfRootID(ctx))
				return "ignored", nil
			}),
			"count", methodFunc(func(any, Ctx) (any, error) { return float64(3), nil }),
			"note", methodFunc(func(args any, ctx Ctx) (any, error) {
				seen.add(args.(string))
				seen.add(typeOfRootID(ctx))
				return nil, nil
			}),
		), nil
	}, ServeOptions{})
	ref := mustRef(t, host, tool, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if value := mustCall(t, ref, "ping", nil, nil); value != nil {
		t.Fatalf("ping = %v", value)
	}
	value, err := host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "count", nil, ctx) }, RootOptions{})
	if err != nil || value != float64(3) {
		t.Fatalf("count = %v, %v", value, err)
	}
	if value := mustCall(t, ref, "note", "hi", nil); value != nil {
		t.Fatalf("note = %v", value)
	}
	if !seen.equals("string", "hi", "string") {
		t.Fatalf("seen %v", seen.list())
	}
}

// host.test.ts:345-377 it("a listener on state gets the current value at start and again after a reload replaced its derivation")
func TestHostAListenerOnStateGetsTheCurrentValueAtStartAndAgainAfterAReloadReplacedItsDerivation(t *testing.T) {
	host := quiet(t, nil)
	doubled := Local("t.doubled", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"set", Method(tNumber(), nil)}}})
	seen := &stringLog{}
	facet := func() Facet {
		return Facet{Name: "doubled", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(doubled, func(_ any, _ Principal, self Self) (Implementation, error) {
				n, err := self.State("n", tNumber(), float64(1), nil)
				if err != nil {
					return nil, err
				}
				double, err := self.Derive(Sources{n}, func(values ...any) any { return values[0].(float64) * 2 })
				if err != nil {
					return nil, err
				}
				if err := self.On("doubled", double, func(value any, _ Ctx) error {
					seen.add(fmt.Sprint(value))
					return nil
				}); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("set", methodFunc(func(value any, ctx Ctx) (any, error) {
					return nil, n.Replace(ctx, value, CellReplaceOptions{})
				})), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	if err := host.Install(facet(), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first value", func() bool { return len(seen.list()) == 1 })
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		return callMember(mustRef(t, host, doubled, map[string]any{}, nil), "set", float64(5), ctx)
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second value", func() bool { return len(seen.list()) == 2 })
	if !seen.equals("2", "10") {
		t.Fatalf("seen %v", seen.list())
	}
	// The reload makes a new derivation: the listener subscribes again and gets its current value once.
	v2 := facet()
	if _, err := host.Reload([]PluginRef{&v2}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the value after the reload", func() bool { return len(seen.list()) >= 3 })
	time.Sleep(20 * time.Millisecond)
	if !seen.equals("2", "10", "10") {
		t.Fatalf("seen %v", seen.list())
	}
}

var hostStore = Local("t.store", LocalDef{
	Version: 1,
	Input:   tObject("scope", tOptional(tString(Schema{"default": "all"}))),
	Members: Fields[any]{{"seen", Opaque("t.seen@1")}},
})

// host.test.ts:360-387 it('serve refuses a lifetime "host" the type cannot have, at compile time and at runtime'). Go has no
// compile-time ServeOptionsFor: Pi's @ts-expect-error part has no Go form, the runtime refusals are asserted.
func TestHostServeRefusesALifetimeHostTheTypeCannotHaveAtCompileTimeAndAtRuntime(t *testing.T) {
	host := quiet(t, nil)
	remote := Remote("t.remote-store", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{}}}})
	keyed := Local("t.keyed", LocalDef{Version: 1, Input: tObject("id", tString())})
	mainVariant := func(any, Principal, Self) (Implementation, error) { return chordjson.ObjectOf("variant", "main"), nil }
	seenNull := func(any, Principal, Self) (Implementation, error) { return chordjson.ObjectOf("seen", nil), nil }
	empty := func(any, Principal, Self) (Implementation, error) { return chordjson.ObjectOf(), nil }
	refused := []struct {
		name    string
		typ     *Contract
		factory Factory
		options ServeOptions
		message string
	}{
		{"a remote type is never of lifetime host", remote, mainVariant, ServeOptions{Lifetime: LifetimeHost}, "needs a local type"},
		{"the one object has the empty input, and id is required", keyed, empty, ServeOptions{Lifetime: LifetimeHost}, "input that accepts {}"},
		{"an object of lifetime host does not linger", hostStore, seenNull, ServeOptions{Lifetime: LifetimeHost, Linger: 10 * time.Millisecond, HasLinger: true}, "does not linger"},
		{"no such lifetime", hostStore, seenNull, ServeOptions{Lifetime: Lifetime("forever")}, "unknown lifetime"},
	}
	for _, refusal := range refused {
		err := host.Serve(refusal.typ, refusal.factory, refusal.options)
		if err == nil || !strings.Contains(err.Error(), refusal.message) {
			t.Fatalf("%s: serve = %v, want %q", refusal.name, err, refusal.message)
		}
	}
	// Allowed: a local type whose input has only optional fields, and any type with the default lifetime.
	optional := Local("t.optional", LocalDef{Version: 1, Input: tObject("id", tOptional(tString()))})
	mustServe(t, host, optional, empty, ServeOptions{Lifetime: LifetimeHost})
	mustServe(t, host, keyed, empty, ServeOptions{Lifetime: LifetimeReferenced, Linger: 10 * time.Millisecond, HasLinger: true})
	mustServe(t, host, remote, mainVariant, ServeOptions{Linger: 10 * time.Millisecond, HasLinger: true})
}

// host.test.ts:389-420 it("every get reaches the one object, created at serve for the host principal; another input is invalid")
func TestHostEveryGetReachesTheOneObjectCreatedAtServeForTheHostPrincipalAnotherInputIsInvalid(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), PrincipalKey: func(principal Principal) string { return jsonText(t, principal) }, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var created principalLog
	mustServe(t, host, hostStore, func(_ any, principal Principal, _ Self) (Implementation, error) {
		created.add(principal)
		return chordjson.ObjectOf("seen", principal), nil
	}, ServeOptions{Lifetime: LifetimeHost})
	if err := host.Ready(); err != nil {
		t.Fatal(err)
	}
	if len(created.items()) != 1 {
		t.Fatalf("created %d objects when served, before anyone asked", len(created.items()))
	}
	guest := mustRef(t, host, hostStore, map[string]any{}, principalOf("guest", "g"))
	defaults := mustRef(t, host, hostStore, map[string]any{"scope": "all"}, nil)
	if err := errors.Join(readyErr(t, guest), readyErr(t, defaults)); err != nil {
		t.Fatal(err)
	}
	if len(created.items()) != 1 {
		t.Fatalf("created %d objects: want the same object for every principal and the default input", len(created.items()))
	}
	if !DeepEqual(guest.Member("seen"), created.items()[0]) {
		t.Fatalf("seen = %v, want %v", guest.Member("seen"), created.items()[0])
	}
	if _, err := host.Hold(hostStore, map[string]any{"scope": "other"}, nil); !IsChordError(err, CodeInvalid) {
		t.Fatalf("another input: %v", err)
	}
	// References do not change its lifetime: released by everyone else, it stays until the host closes.
	guest.Release()
	defaults.Release()
	time.Sleep(20 * time.Millisecond)
	again := mustRef(t, host, hostStore, map[string]any{}, nil)
	if err := readyErr(t, again); err != nil {
		t.Fatal(err)
	}
	if len(created.items()) != 1 {
		t.Fatalf("created %d objects after release", len(created.items()))
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if again.Status() != RefGone {
		t.Fatalf("status %s", again.Status())
	}
}

var hostViewSchema = tObject("text", tString())

// hostDoc is host.test.ts Doc.
var hostDoc = Remote("t.doc", RemoteDef{
	Version: 1,
	Input:   tObject("id", tString()),
	Host:    NewHostNamed("host"),
	Variants: Fields[VariantDef]{
		{"viewer", VariantDef{State: Fields[Schema]{{"view", hostViewSchema}}}},
		{"editor", VariantDef{
			State:   Fields[Schema]{{"view", hostViewSchema}},
			Methods: Fields[MethodSpec]{{"edit", Method(tObject("text", tString()), tObject("ok", tBoolean()))}},
		}},
	},
})

// hostServeDoc is host.test.ts serveDoc.
func hostServeDoc(t *testing.T, host *HostImpl, principals *principalLog) {
	t.Helper()
	mustServe(t, host, hostDoc, func(_ any, principal Principal, self Self) (Implementation, error) {
		if principals != nil {
			principals.add(principal)
		}
		view, err := self.State("view", hostViewSchema, delta.JsonObjectOf("text", ""), nil)
		if err != nil {
			return nil, err
		}
		if role, _ := inputField(principal, "role").(string); role != "editor" {
			return chordjson.ObjectOf("variant", "viewer", "view", view), nil
		}
		return chordjson.ObjectOf(
			"variant", "editor",
			"view", view,
			"edit", methodFunc(func(args any, ctx Ctx) (any, error) {
				text := inputField(args, "text").(string)
				if err := view.Replace(ctx, delta.JsonObjectOf("text", text), CellReplaceOptions{}); err != nil {
					return nil, err
				}
				if text == "bad" {
					return map[string]any{"ok": "no"}, nil
				}
				return map[string]any{"ok": true}, nil
			}),
		), nil
	}, ServeOptions{})
}

// host.test.ts:325-339 it("keeps objects per principal key and enforces variants")
func TestHostKeepsObjectsPerPrincipalKeyAndEnforcesVariants(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(),
		// host.test.ts `(principal as { user: string }).user`: a principal without one (the host's own) has the key undefined.
		PrincipalKey: func(principal Principal) string {
			if user, ok := inputField(principal, "user").(string); ok {
				return user
			}
			return "undefined"
		},
		OnError: func(error) {},
	})
	t.Cleanup(func() { _ = host.Close() })
	var principals principalLog
	hostServeDoc(t, host, &principals)
	viewer := mustRef(t, host, hostDoc, map[string]any{"id": "1"}, principalOf("user", "v", "role", "viewer"))
	editor := mustRef(t, host, hostDoc, map[string]any{"id": "1"}, principalOf("user", "e", "role", "editor"))
	if err := errors.Join(readyErr(t, viewer), readyErr(t, editor)); err != nil {
		t.Fatal(err)
	}
	if len(principals.items()) != 2 {
		t.Fatalf("principals %v", principals.items())
	}
	if kind := viewer.Variant(); kind == nil || *kind != "viewer" {
		t.Fatalf("viewer kind %v", kind)
	}
	if shape := viewer.Shape(); shape == nil || !slices.Equal(shape.Members, []string{"view"}) {
		t.Fatalf("viewer shape %+v", shape)
	}
	if _, err := callMember(viewer, "edit", map[string]any{"text": "x"}, nil); !IsChordError(err, CodeNotInVariant) {
		t.Fatalf("viewer edit: %v", err)
	}
	if got := jsonText(t, mustCall(t, editor, "edit", map[string]any{"text": "x"}, nil)); got != `{"ok":true}` {
		t.Fatalf("editor edit = %s", got)
	}
	if got := jsonText(t, readable(t, editor, "view").Value()); got != `{"text":"x"}` {
		t.Fatalf("editor view = %s", got)
	}
	if got := jsonText(t, readable(t, viewer, "view").Value()); got != `{"text":""}` {
		t.Fatalf("viewer view = %s", got)
	}
}

// host.test.ts:341-353 it("validates remote args and results at the provider boundary")
func TestHostValidatesRemoteArgsAndResultsAtTheProviderBoundary(t *testing.T) {
	host := quiet(t, nil)
	hostServeDoc(t, host, nil)
	editor := mustRef(t, host, hostDoc, map[string]any{"id": "2"}, principalOf("role", "editor"))
	_, err := callMember(editor, "edit", map[string]any{"text": float64(1)}, nil)
	if chordError := asChord(err); chordError == nil || chordError.Code != CodeInvalid || chordError.Reason != "args" {
		t.Fatalf("bad args: %v", err)
	}
	_, err = callMember(editor, "edit", map[string]any{"text": "bad"}, nil)
	if chordError := asChord(err); chordError == nil || chordError.Code != CodeInvalid || chordError.Reason != "result" {
		t.Fatalf("bad result: %v", err)
	}
}

// host.test.ts:355-384 it("forced disposal makes references gone and aborts in-flight calls")
func TestHostForcedDisposalMakesRefsGoneAndAbortsInFlightCalls(t *testing.T) {
	host := quiet(t, nil)
	var mu sync.Mutex
	seenAbort := false
	waiting := make(chan struct{}, 1)
	slow := Local("t.inflight", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"wait", Method(nil, nil)}}})
	mustServe(t, host, slow, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("wait", methodFunc(func(_ any, ctx Ctx) (any, error) {
			waiting <- struct{}{}
			<-ctx.Signal().Done()
			mu.Lock()
			seenAbort = true
			mu.Unlock()
			return nil, nil
		})), nil
	}, ServeOptions{})
	ref := mustRef(t, host, slow, map[string]any{}, principalOf("user", "u"))
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	call := asyncCall(ref, "wait", nil, nil)
	<-waiting
	if err := host.InvalidatePrincipal(PrincipalMatch{Predicate: func(principal Principal) bool {
		return inputField(principal, "user") == "u"
	}}); err != nil {
		t.Fatal(err)
	}
	if ref.Status() != RefGone {
		t.Fatalf("status %s", ref.Status())
	}
	<-call
	mu.Lock()
	aborted := seenAbort
	mu.Unlock()
	if !aborted {
		t.Fatal("the in-flight call did not see the abort")
	}
	if _, err := callMember(ref, "wait", nil, nil); !IsChordError(err, CodeGone) {
		t.Fatalf("wait after disposal: %v", err)
	}
	fresh := mustRef(t, host, slow, map[string]any{}, principalOf("user", "u"))
	if err := readyErr(t, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status() != RefLive {
		t.Fatalf("fresh status %s", fresh.Status())
	}
}

// host.test.ts:386-405 it("state members are late-bound readables: values, then snapshot/reset/ops events")
func TestHostStateMembersAreLateBoundReadablesValuesThenSnapshotResetOpsEvents(t *testing.T) {
	host := quiet(t, nil)
	hostServeDoc(t, host, nil)
	var editor Ref
	var mu sync.Mutex
	var shapes, events, values []string
	// Pi's synchronous segment up to the first await runs as one section of the host's loop.
	host.run(func() {
		editor = mustRef(t, host, hostDoc, map[string]any{"id": "3"}, principalOf("role", "editor"))
		editor.OnShape(func(shape Shape) {
			mu.Lock()
			shapes = append(shapes, *shape.Variant)
			mu.Unlock()
		})
		view := readable(t, editor, "view")
		view.Events(func(event StateEvent[any]) {
			mu.Lock()
			defer mu.Unlock()
			if event.Type == StateEventOps {
				events = append(events, "ops "+jsonText(t, event.Ops))
				return
			}
			events = append(events, string(event.Type)+" "+jsonText(t, event.Value))
		})
		view.Subscribe(func(value any) {
			mu.Lock()
			values = append(values, jsonText(t, value))
			mu.Unlock()
		})
	})
	if err := readyErr(t, editor); err != nil {
		t.Fatal(err)
	}
	if variant := editor.Variant(); variant == nil || *variant != "editor" {
		t.Fatalf("variant %v, want editor", variant)
	}
	mustCall(t, editor, "edit", map[string]any{"text": "hi"}, nil)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(shapes, []string{"editor"}) {
		t.Fatalf("shapes %v", shapes)
	}
	// Listeners added while loading get their snapshot once the reference is live (host.test.ts, b45e0ebc).
	if want := []string{`snap {"text":""}`, `ops [["r",{"text":"hi"}]]`}; !slices.Equal(events, want) {
		t.Fatalf("events %q, want %q", events, want)
	}
	if want := []string{`{"text":""}`, `{"text":"hi"}`}; !slices.Equal(values, want) {
		t.Fatalf("values %q, want %q", values, want)
	}
}

var hostRoutes = DefineRegistry[string]("t.routes")

func registerRoutes(entries ...Entry) Factory {
	return func(_ any, _ Principal, self Self) (Implementation, error) {
		return chordjson.ObjectOf(), self.Register(Registry[any](hostRoutes), entries)
	}
}

// host.test.ts:411-445 it('only objects of lifetime "host" register; tables follow registration order')
func TestHostOnlyObjectsOfLifetimeHostRegisterTablesFollowRegistrationOrder(t *testing.T) {
	host := quiet(t, nil)
	view := host.Registry(Registry[any](hostRoutes))
	var mu sync.Mutex
	var tables [][]string
	// Pi's synchronous segment up to the first await runs as one section of the host's loop.
	host.run(func() {
		mustServe(t, host, emptyLocal("t.routes-a"), registerRoutes(Entry{"GET /a", "a"}, Entry{"GET /c", "c"}), ServeOptions{Lifetime: LifetimeHost})
		mustServe(t, host, emptyLocal("t.routes-b"), registerRoutes(Entry{"GET /b", "b"}), ServeOptions{Lifetime: LifetimeHost})
		view.Subscribe(func(table RegistryTable[any], _ int) {
			mu.Lock()
			tables = append(tables, table.Keys())
			mu.Unlock()
		})
	})
	want := []string{"GET /a", "GET /c", "GET /b"}
	waitFor(t, "both registrations", func() bool { return view.Table(nil).Len() == 3 })
	if keys := view.Table(nil).Keys(); !slices.Equal(keys, want) {
		t.Fatalf("table keys %v", keys)
	}
	if value, _ := view.Get("GET /b", nil); value != "b" {
		t.Fatalf("GET /b = %v", value)
	}
	waitFor(t, "the subscriber's full table", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(tables) > 0 && len(tables[len(tables)-1]) == 3
	})
	mu.Lock()
	last := tables[len(tables)-1]
	mu.Unlock()
	if !slices.Equal(last, want) {
		t.Fatalf("last table %v", last)
	}
	lazy := emptyLocal("t.lazy-register")
	mustServe(t, host, lazy, registerRoutes(Entry{"x", "x"}), ServeOptions{})
	if err := readyErr(t, mustRef(t, host, lazy, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), `only objects of lifetime "host" may register`) {
		t.Fatalf("lazy register: %v", err)
	}
}

// host.test.ts:447-463 it("rejects conflicting registrations")
func TestHostRejectsConflictingRegistrations(t *testing.T) {
	errs := &errorLog{}
	host := quiet(t, errs)
	for _, name := range []string{"t.conflict-a", "t.conflict-b"} {
		mustServe(t, host, emptyLocal(name), registerRoutes(Entry{"same", name}), ServeOptions{Lifetime: LifetimeHost})
	}
	waitFor(t, "registry_conflict", func() bool {
		return slices.ContainsFunc(errs.items(), func(err error) bool { return strings.Contains(err.Error(), "registry_conflict") })
	})
}

var (
	hostSource = Local("t.source", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"n", tNumber()},
		{"set", Method(tNumber(), nil)},
	}})
	hostMirror = Local("t.mirror", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"copy", tNumber()},
		{"doubled", tNumber()},
	}})
)

func numberOr0(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

// host.test.ts:478-506 it("reference state members are sources for listeners and derivations")
func TestHostRefStateMembersAreSourcesForListenersAndDerivations(t *testing.T) {
	host := quiet(t, nil)
	mustServe(t, host, hostSource, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(1), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "set", methodFunc(func(value any, ctx Ctx) (any, error) {
			return nil, n.Replace(ctx, value, CellReplaceOptions{})
		})), nil
	}, ServeOptions{})
	mustServe(t, host, hostMirror, func(_ any, _ Principal, self Self) (Implementation, error) {
		source, err := self.Get(hostSource, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		n, _ := source.Member("n").(Readable[any])
		copied, err := self.State("copy", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		if err := self.On("follow", n, func(value any, ctx Ctx) error {
			return copied.Replace(ctx, numberOr0(value), CellReplaceOptions{})
		}); err != nil {
			return nil, err
		}
		doubled, err := self.Derive(Sources{n}, func(values ...any) any {
			return numberOr0(values[0]) * 2
		})
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("copy", copied, "doubled", doubled), nil
	}, ServeOptions{})
	mirror := mustRef(t, host, hostMirror, map[string]any{}, nil)
	if err := readyErr(t, mirror); err != nil {
		t.Fatal(err)
	}
	copyOf, doubled := readable(t, mirror, "copy"), readable(t, mirror, "doubled")
	waitFor(t, "copy 1", func() bool { return copyOf.Value() == float64(1) })
	if doubled.Value() != float64(2) {
		t.Fatalf("doubled = %v", doubled.Value())
	}
	mustCall(t, mustRef(t, host, hostSource, map[string]any{}, nil), "set", float64(5), nil)
	waitFor(t, "copy 5", func() bool { return copyOf.Value() == float64(5) })
	if doubled.Value() != float64(10) {
		t.Fatalf("doubled = %v", doubled.Value())
	}
}

// host.test.ts:508-531 it("ctx.get acts as the root principal at ingress and as the host inside objects")
func TestHostCtxGetActsAsTheRootPrincipalAtIngressAndAsTheHostInsideObjects(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), PrincipalKey: func(principal Principal) string { return jsonText(t, principal) }, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var seen principalLog
	who := Local("t.who", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"ping", Method(nil, nil)}}})
	front := Local("t.front", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"relay", Method(nil, nil)}}})
	mustServe(t, host, who, func(_ any, principal Principal, _ Self) (Implementation, error) {
		seen.add(principal)
		return chordjson.ObjectOf("ping", methodFunc(func(any, Ctx) (any, error) { return nil, nil })), nil
	}, ServeOptions{})
	mustServe(t, host, front, func(_ any, principal Principal, _ Self) (Implementation, error) {
		seen.add(principal)
		return chordjson.ObjectOf("relay", methodFunc(func(_ any, ctx Ctx) (any, error) {
			ref, err := ctx.Get(who, map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			return callMember(ref, "ping", nil, ctx)
		})), nil
	}, ServeOptions{})
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		ref, err := ctx.Get(front, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		return callMember(ref, "relay", nil, ctx)
	}, RootOptions{Principal: map[string]any{"user": "guest"}}); err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, seen.items()); got != `[{"user":"guest"},{"kind":"host"}]` {
		t.Fatalf("seen %s", got)
	}
}

// host.test.ts:533-546 it("calling a reference inside a factory throws not_ready"). Pi's `void source.set(1)` throws synchronously inside the factory; a Go method call returns
// that error, which the factory propagates as Pi's throw does.
func TestHostCallingARefInsideAFactoryThrowsNotReady(t *testing.T) {
	host := quiet(t, nil)
	mustServe(t, host, hostSource, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "set", methodFunc(func(any, Ctx) (any, error) { return nil, nil })), nil
	}, ServeOptions{})
	eager := emptyLocal("t.eager-call")
	mustServe(t, host, eager, func(_ any, _ Principal, self Self) (Implementation, error) {
		source, err := self.Get(hostSource, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		if _, err := callMember(source, "set", float64(1), nil); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(), nil
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, eager, map[string]any{}, nil)); !IsChordError(err, CodeNotReady) {
		t.Fatalf("ready: %v", err)
	}
}

// host.test.ts:548-560 it("a ChordError from a factory keeps its code through dependents")
func TestHostAChordErrorFromAFactoryKeepsItsCodeThroughDependents(t *testing.T) {
	host := quiet(t, nil)
	secret, page := emptyLocal("t.secret"), emptyLocal("t.page")
	mustServe(t, host, secret, func(any, Principal, Self) (Implementation, error) {
		return nil, Forbidden("not for you")
	}, ServeOptions{})
	mustServe(t, host, page, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.Get(secret, map[string]any{}, nil)
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, page, map[string]any{}, nil)); !IsChordError(err, CodeForbidden) {
		t.Fatalf("ready: %v", err)
	}
}
