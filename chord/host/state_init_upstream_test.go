package host

// Ports packages/chord/test/state-init.test.ts (Pi aa9cf15b, protocol 3): self.state(name, schema, { init }).

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var (
	initNotesSchema = tArray(tString())
	// initNotes is state-init.test.ts Notes.
	initNotes = Local("t.notes", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"notes", initNotesSchema},
		{"count", tNumber()},
		{"has", Method(tString(), tBoolean())},
		{"add", Method(tString(), nil)},
	}})
)

func initStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, len(items))
	for index, item := range items {
		out[index], _ = item.(string)
	}
	return out
}

// initImplementation is state-init.test.ts's `{ notes, count, has, add }`.
func initImplementation(notes *Cell[any], count Readable[any]) Implementation {
	return chordjson.ObjectOf(
		"notes", notes,
		"count", count,
		"has", methodFunc(func(note any, _ Ctx) (any, error) {
			return slices.Contains(initStrings(notes.Value()), note.(string)), nil
		}),
		"add", methodFunc(func(note any, ctx Ctx) (any, error) {
			return nil, notes.Change(ctx, func(draft any) { _, _ = draft.(*delta.Array).Push(note.(string)) })
		}),
	)
}

// initCount is `self.derive([...cells], (list) => list.length)`.
func initCount(self Self, sources ...Readable[any]) (Readable[any], error) {
	return self.Derive(sources, func(values ...any) any {
		total := 0.0
		for _, value := range values {
			switch value := value.(type) {
			case float64:
				total += value
			default:
				total += float64(len(initStrings(value)))
			}
		}
		return total
	})
}

// state-init.test.ts "runs after the resources are open and before the object goes live: nothing sees another value"
func TestStateInitRunsAfterTheResourcesAreOpenAndBeforeTheObjectGoesLiveNothingSeesAnotherValue(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	loaded := make(chan []any, 1)
	var log reloadLog
	mustServe(t, host, initNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
		db, err := self.Resource("db", ResourceDeclaration[any]{Version: 1, Open: func(ResourceOpenOptions) (any, error) {
			log.push("open")
			return func() []any { return <-loaded }, nil
		}})
		if err != nil {
			return nil, err
		}
		notes, err := self.State("notes", initNotesSchema, StateInit{Init: func(Ctx) (any, error) {
			log.push("init")
			load, err := db.Value()
			if err != nil {
				return nil, err
			}
			return load.(func() []any)(), nil
		}}, nil)
		if err != nil {
			return nil, err
		}
		count, err := initCount(self, notes)
		if err != nil {
			return nil, err
		}
		if err := self.On("notes", notes, func(event any, _ Ctx) error {
			log.push("listener " + strings.Join(initStrings(event), ","))
			return nil
		}); err != nil {
			return nil, err
		}
		if err := self.Task(func(Ctx) error {
			log.push("task " + strings.Join(initStrings(notes.Value()), ","))
			return nil
		}); err != nil {
			return nil, err
		}
		return initImplementation(notes, count), nil
	}, ServeOptions{})
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	var mu sync.Mutex
	var seen []any
	requestsReadable(t, ref, "notes").Subscribe(func(value any) {
		mu.Lock()
		seen = append(seen, value)
		mu.Unlock()
	})
	// A call made while init runs waits until the object is live, then reads the loaded value.
	type outcome struct {
		value any
		err   error
	}
	has := make(chan outcome, 1)
	go func() {
		value, err := host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "has", "a", ctx) }, RootOptions{})
		has <- outcome{value, err}
	}()
	waitFor(t, "init to start", func() bool { return slices.Contains(log.items(), "init") })
	if ref.Status() != RefLoading {
		t.Fatalf("status = %s, want loading", ref.Status())
	}
	loaded <- []any{"a", "b"}
	if got := <-has; got.err != nil || got.value != true {
		t.Fatalf("has = %v, %v", got.value, got.err)
	}
	if got := requestsReadable(t, ref, "count").Value(); got != float64(2) {
		t.Fatalf("count = %v, want 2", got)
	}
	waitFor(t, "the listener and the task", func() bool { return len(log.items()) == 4 })
	if got := log.items(); !slices.Equal(got, []string{"open", "init", "listener a,b", "task a,b"}) {
		t.Fatalf("log = %v", got)
	}
	// Before live, a reference's member has no value (undefined); its first value is the loaded one.
	mu.Lock()
	defer mu.Unlock()
	var values []any
	for _, value := range seen {
		if value != nil {
			values = append(values, value)
		}
	}
	if len(values) != 1 || !slices.Equal(initStrings(values[0]), []string{"a", "b"}) {
		t.Fatalf("values seen = %v", values)
	}
}

// state-init.test.ts "an init cell cannot be read inside the factory"
func TestStateInitAnInitCellCannotBeReadInsideTheFactory(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	mustServe(t, host, initNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
		notes, err := self.State("notes", initNotesSchema, StateInit{Init: func(Ctx) (any, error) { return []any{}, nil }}, nil)
		if err != nil {
			return nil, err
		}
		count, err := initCount(self, notes)
		if err != nil {
			return nil, err
		}
		notes.Value()
		return initImplementation(notes, count), nil
	}, ServeOptions{})
	if err := readyErr(t, requestsGet(t, host, initNotes, map[string]any{}, nil)); !IsChordError(err, CodeNotReady) {
		t.Fatalf("ready = %v, want not_ready", err)
	}
}

// state-init.test.ts "a throwing or invalid init fails the activation and closes its resources; the next get tries again"
func TestStateInitAThrowingOrInvalidInitFailsTheActivationAndClosesItsResourcesTheNextGetTriesAgain(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	var mu sync.Mutex
	result := func() (any, error) { return nil, errors.New("db down") }
	var closed reloadLog
	mustServe(t, host, initNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Resource("db", ResourceDeclaration[any]{Version: 1,
			Open:  func(ResourceOpenOptions) (any, error) { return "db", nil },
			Close: func(db any) error { closed.push(db.(string)); return nil }}); err != nil {
			return nil, err
		}
		notes, err := self.State("notes", initNotesSchema, StateInit{Init: func(Ctx) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			return result()
		}}, nil)
		if err != nil {
			return nil, err
		}
		count, err := initCount(self, notes)
		if err != nil {
			return nil, err
		}
		return initImplementation(notes, count), nil
	}, ServeOptions{})
	if err := readyErr(t, requestsGet(t, host, initNotes, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("ready = %v, want db down", err)
	}
	if got := closed.items(); !slices.Equal(got, []string{"db"}) {
		t.Fatalf("closed = %v", got)
	}
	mu.Lock()
	result = func() (any, error) { return []any{1.0}, nil }
	mu.Unlock()
	if err := readyErr(t, requestsGet(t, host, initNotes, map[string]any{}, nil)); !IsChordError(err, CodeInvalid) {
		t.Fatalf("ready = %v, want invalid", err)
	}
	if got := closed.items(); !slices.Equal(got, []string{"db", "db"}) {
		t.Fatalf("closed = %v", got)
	}
	mu.Lock()
	result = func() (any, error) { return []any{"ok"}, nil }
	mu.Unlock()
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if got := initStrings(requestsReadable(t, ref, "notes").Value()); !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("notes = %v", got)
	}
}

// state-init.test.ts "init runs in a root: it can call other objects"
func TestStateInitRunsInARootItCanCallOtherObjects(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	source := Local("t.source", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"all", Method(nil, initNotesSchema)}}})
	mustServe(t, host, source, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("all", methodFunc(func(any, Ctx) (any, error) { return []any{"from source"}, nil })), nil
	}, ServeOptions{})
	mustServe(t, host, initNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
		notes, err := self.State("notes", initNotesSchema, StateInit{Init: func(ctx Ctx) (any, error) {
			ref, err := ctx.Get(source, map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			return callMember(ref, "all", nil, ctx)
		}}, nil)
		if err != nil {
			return nil, err
		}
		count, err := initCount(self, notes)
		if err != nil {
			return nil, err
		}
		return initImplementation(notes, count), nil
	}, ServeOptions{})
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if got := initStrings(requestsReadable(t, ref, "notes").Value()); !slices.Equal(got, []string{"from source"}) {
		t.Fatalf("notes = %v", got)
	}
}

// initReloadSpec is state-init.test.ts's reload facet() spec.
type initReloadSpec struct {
	inits        *reloadLog
	extra        func() ([]any, error)
	countVersion int
}

// initFacet is state-init.test.ts describe("reload") facet(spec).
func initFacet(spec initReloadSpec) Facet {
	return Facet{Name: "notes", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(initNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
			notes, err := self.State("notes", initNotesSchema, StateInit{Init: func(Ctx) (any, error) {
				spec.inits.push("notes")
				return []any{"loaded"}, nil
			}}, nil)
			if err != nil {
				return nil, err
			}
			version := spec.countVersion
			if version == 0 {
				version = 1
			}
			counter, err := self.State("counter", tNumber(), 0.0, &StateOptions{Version: version})
			if err != nil {
				return nil, err
			}
			sources := Sources{notes, counter}
			if spec.extra != nil {
				more, err := self.State("more", initNotesSchema, StateInit{Init: func(Ctx) (any, error) {
					spec.inits.push("more")
					return spec.extra()
				}}, nil)
				if err != nil {
					return nil, err
				}
				sources = append(sources, more)
			}
			count, err := initCount(self, sources...)
			if err != nil {
				return nil, err
			}
			return initImplementation(notes, count), nil
		}, ServeOptions{})
	}}}
}

// state-init.test.ts describe("reload") "keeps an existing cell without running init; a cell the reload adds runs init
// while it prepares"
func TestStateInitReloadKeepsAnExistingCellWithoutRunningInitACellTheReloadAddsRunsInitWhileItPrepares(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	var inits reloadLog
	if err := host.Install(initFacet(initReloadSpec{inits: &inits}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "add", "written", ctx) }, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	extra := initFacet(initReloadSpec{inits: &inits, extra: func() ([]any, error) { return []any{"x", "y"}, nil }})
	if _, err := host.Reload([]PluginRef{&extra}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := inits.items(); !slices.Equal(got, []string{"notes", "more"}) {
		t.Fatalf("inits = %v", got)
	}
	if got := initStrings(requestsReadable(t, ref, "notes").Value()); !slices.Equal(got, []string{"loaded", "written"}) {
		t.Fatalf("notes = %v", got)
	}
	// The new derivation counts the kept cell and the added one: 2 + 0 + 2.
	if got := requestsReadable(t, ref, "count").Value(); got != float64(4) {
		t.Fatalf("count = %v, want 4", got)
	}
}

// state-init.test.ts describe("reload") "a failing init of an added cell refuses the reload; the old code keeps running"
func TestStateInitReloadAFailingInitOfAnAddedCellRefusesTheReloadTheOldCodeKeepsRunning(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	var inits reloadLog
	if err := host.Install(initFacet(initReloadSpec{inits: &inits}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	failing := initFacet(initReloadSpec{inits: &inits, extra: func() ([]any, error) { return nil, errors.New("cannot load more") }})
	if _, err := host.Reload([]PluginRef{&failing}, ReloadOptions{}); err == nil || !strings.Contains(err.Error(), "cannot load more") {
		t.Fatalf("reload = %v, want cannot load more", err)
	}
	if got := requestsReadable(t, ref, "count").Value(); got != float64(1) {
		t.Fatalf("count = %v, want 1", got)
	}
	has, err := host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "has", "loaded", ctx) }, RootOptions{})
	if err != nil || has != true {
		t.Fatalf("has = %v, %v", has, err)
	}
}

// state-init.test.ts describe("reload") "in a gated reload, an added cell runs init at the gate"
func TestStateInitReloadInAGatedReloadAnAddedCellRunsInitAtTheGate(t *testing.T) {
	host := requestsHost(t, HostOptions{Self: kindHost()})
	var inits reloadLog
	if err := host.Install(initFacet(initReloadSpec{inits: &inits}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, host, initNotes, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	gated := initFacet(initReloadSpec{inits: &inits, countVersion: 2, extra: func() ([]any, error) { return []any{"x"}, nil }})
	report, err := host.Reload([]PluginRef{&gated}, ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 1 {
		t.Fatalf("gated = %v, want 1", report.Gated)
	}
	if got := inits.items(); !slices.Equal(got, []string{"notes", "more"}) {
		t.Fatalf("inits = %v", got)
	}
	if got := requestsReadable(t, ref, "count").Value(); got != float64(2) {
		t.Fatalf("count = %v, want 2", got)
	}
}
