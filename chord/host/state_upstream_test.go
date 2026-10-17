package host

// Ports packages/chord/test/state.test.ts (Pi v1.2.0) through the host, as Pi drives it: cells, derivations,
// listeners and tasks declared by factories and read through references. cell_upstream_test.go keeps the cell-level unit
// tests of cell.ts. Pi's `await tick()` becomes waitFor on the state Pi asserts after it.

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// stateEmitter is state.test.ts emitter: listeners in a Set, called synchronously by emit.
type stateEmitter struct {
	mu        sync.Mutex
	listeners *orderedMap[*func(any), struct{}]
}

func newStateEmitter() *stateEmitter {
	return &stateEmitter{listeners: newOrderedMap[*func(any), struct{}]()}
}

func (emitter *stateEmitter) emit(event any) {
	emitter.mu.Lock()
	listeners := emitter.listeners.Keys()
	emitter.mu.Unlock()
	for _, listener := range listeners {
		(*listener)(event)
	}
}

func (emitter *stateEmitter) Subscribe(listener func(event any)) func() {
	entry := &listener
	emitter.mu.Lock()
	emitter.listeners.Set(entry, struct{}{})
	emitter.mu.Unlock()
	return func() {
		emitter.mu.Lock()
		emitter.listeners.Delete(entry)
		emitter.mu.Unlock()
	}
}

func (emitter *stateEmitter) size() int {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return emitter.listeners.Len()
}

var (
	stateItem  = tObject("id", tNumber(), "label", tString())
	stateItems = tObject("items", tArray(stateItem), "count", tNumber())
	// stateList is state.test.ts List.
	stateList = Local("t.list", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"list", stateItems},
		{"labels", tArray(tString())},
		{"push", Method(stateItem, nil)},
		{"set", Method(stateItems, nil)},
	}})
)

// stateServeList is state.test.ts serveList.
func stateServeList(t *testing.T, host *HostImpl) {
	t.Helper()
	mustServe(t, host, stateList, func(_ any, _ Principal, self Self) (Implementation, error) {
		list, err := self.State("list", stateItems, delta.JsonObjectOf("items", []any{}, "count", float64(0)), nil)
		if err != nil {
			return nil, err
		}
		labels, err := self.Derive(Sources{list}, func(values ...any) any {
			labels := []any{}
			for _, item := range inputField(values[0], "items").([]any) {
				labels = append(labels, inputField(item, "label"))
			}
			return labels
		})
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"list", list,
			"labels", labels,
			"push", methodFunc(func(item any, ctx Ctx) (any, error) {
				return nil, list.Change(ctx, func(draft any) {
					object := draft.(*delta.Object)
					if _, err := object.Array("items").Push(item); err != nil {
						panic(err)
					}
					_ = object.Set("count", object.Get("count").(float64)+1)
				})
			}),
			"set", methodFunc(func(value any, ctx Ctx) (any, error) {
				return nil, list.Replace(ctx, value, CellReplaceOptions{})
			}),
		), nil
	}, ServeOptions{})
}

func eventText(t *testing.T, event StateEvent[any]) string {
	t.Helper()
	if event.Type == StateEventOps {
		return "ops " + jsonText(t, event.Ops) + " " + jsonText(t, event.Value)
	}
	return string(event.Type) + " " + jsonText(t, event.Value)
}

// state.test.ts:75 it("publishes snapshots then exact ops; derivations follow")
func TestStatePublishesSnapshotsThenExactOpsDerivationsFollow(t *testing.T) {
	host := quiet(t, nil)
	stateServeList(t, host)
	var ref Ref
	var mu sync.Mutex
	var events, labels []StateEvent[any]
	// Pi's synchronous segment up to the first await runs as one section of the host's loop.
	host.run(func() {
		ref = mustRef(t, host, stateList, map[string]any{}, nil)
		readable(t, ref, "list").Events(func(event StateEvent[any]) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		})
		readable(t, ref, "labels").Events(func(event StateEvent[any]) {
			mu.Lock()
			labels = append(labels, event)
			mu.Unlock()
		})
	})
	mustCall(t, ref, "push", map[string]any{"id": float64(1), "label": "a"}, nil)
	mu.Lock()
	// Subscribed while loading: the snapshot once live, then exact ops (state.test.ts, b45e0ebc).
	if len(events) < 2 {
		mu.Unlock()
		t.Fatalf("events %v", events)
	}
	if got := eventText(t, events[0]); got != `snap {"items":[],"count":0}` {
		t.Errorf("events[0] = %s", got)
	}
	var ops []string
	if events[1].Type == StateEventOps {
		for _, op := range events[1].Ops {
			ops = append(ops, jsonText(t, op))
		}
	}
	sort.Strings(ops)
	if want := []string{`["p",["items"],0,0,[{"id":1,"label":"a"}]]`, `["s",["count"],1]`}; !slices.Equal(ops, want) {
		t.Errorf("ops %q, want %q", ops, want)
	}
	if got := jsonText(t, events[1].Value); got != `{"items":[{"id":1,"label":"a"}],"count":1}` {
		t.Errorf("events[1].value = %s", got)
	}
	var types []string
	for _, event := range labels {
		types = append(types, string(event.Type))
	}
	mu.Unlock()
	if got := jsonText(t, readable(t, ref, "labels").Value()); got != `["a"]` {
		t.Fatalf("labels = %s", got)
	}
	if !slices.Equal(types, []string{"snap", "ops"}) {
		t.Fatalf("label event types %v", types)
	}
	mustCall(t, ref, "set", map[string]any{"items": []any{}, "count": float64(0)}, nil)
	if got := jsonText(t, readable(t, ref, "labels").Value()); got != `[]` {
		t.Fatalf("labels after set = %s", got)
	}
}

// state.test.ts:99 it("supports primitive cells and value-returning changes")
func TestStateSupportsPrimitiveCellsAndValueReturningChanges(t *testing.T) {
	host := quiet(t, nil)
	counter := Local("t.prim", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"n", tNumber()},
		{"inc", Method(nil, tNumber())},
	}})
	mustServe(t, host, counter, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "inc", methodFunc(func(_ any, ctx Ctx) (any, error) {
			if err := n.ChangeValue(ctx, func(value any) any { return value.(float64) + 1 }); err != nil {
				return nil, err
			}
			return n.Value(), nil
		})), nil
	}, ServeOptions{})
	ref := mustRef(t, host, counter, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var values, events []string
	n := readable(t, ref, "n")
	n.Events(func(event StateEvent[any]) {
		mu.Lock()
		events = append(events, eventText(t, event))
		mu.Unlock()
	})
	n.Subscribe(func(value any) {
		mu.Lock()
		values = append(values, jsonText(t, value))
		mu.Unlock()
	})
	if got := mustCall(t, ref, "inc", nil, nil); got != float64(1) {
		t.Fatalf("inc = %v", got)
	}
	if got := mustCall(t, ref, "inc", nil, nil); got != float64(2) {
		t.Fatalf("inc = %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(values, []string{"0", "1", "2"}) {
		t.Fatalf("values %v", values)
	}
	if want := []string{"snap 0", `ops [["r",1]] 1`, `ops [["r",2]] 2`}; !slices.Equal(events, want) {
		t.Fatalf("events %q, want %q", events, want)
	}
}

// state.test.ts:132 it("rejects a returned value that does not fit the schema, even outside dev mode")
func TestStateRejectsAReturnedValueThatDoesNotFitTheSchemaEvenOutsideDevMode(t *testing.T) {
	host := quiet(t, nil)
	box := Local("t.box", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"put", Method(nil, nil)}}})
	mustServe(t, host, box, func(_ any, _ Principal, self Self) (Implementation, error) {
		list, err := self.State("list", tArray(tNumber()), []any{}, nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("put", methodFunc(func(_ any, ctx Ctx) (any, error) {
			// The classic slip: an expression-bodied arrow returns push()'s length.
			return nil, list.ChangeValue(ctx, func(draft any) any {
				length, err := draft.(*delta.Array).Push(float64(1))
				if err != nil {
					panic(err)
				}
				return float64(length)
			})
		})), nil
	}, ServeOptions{})
	if _, err := callMember(mustRef(t, host, box, map[string]any{}, nil), "put", nil, nil); !IsChordError(err, CodeInvalid) {
		t.Fatalf("put: %v", err)
	}
}

// state.test.ts:152 it("validates initial values always and writes only in dev mode")
func TestStateValidatesInitialValuesAlwaysAndWritesOnlyInDevMode(t *testing.T) {
	bad := emptyLocal("t.bad-initial")
	host := quiet(t, nil)
	mustServe(t, host, bad, func(_ any, _ Principal, self Self) (Implementation, error) {
		_, err := self.State("x", tObject("n", tNumber()), delta.JsonObjectOf("n", "no"), nil)
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, bad, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "initial value of state x") {
		t.Fatalf("bad initial: %v", err)
	}

	lax := quiet(t, nil)
	stateServeList(t, lax)
	if _, err := callMember(mustRef(t, lax, stateList, map[string]any{}, nil), "set", map[string]any{"items": []any{}, "count": "x"}, nil); err != nil {
		t.Fatalf("lax set: %v", err)
	}

	dev := CreateHost(HostOptions{Self: kindHost(), Dev: true, OnError: func(error) {}})
	t.Cleanup(func() { _ = dev.Close() })
	stateServeList(t, dev)
	ref := mustRef(t, dev, stateList, map[string]any{}, nil)
	_, err := callMember(ref, "set", map[string]any{"items": []any{}, "count": "x"}, nil)
	if chordError := asChord(err); chordError == nil || chordError.Code != CodeInvalid || chordError.Reason != "state" {
		t.Fatalf("dev set: %v", err)
	}
	if got := jsonText(t, readable(t, ref, "list").Value()); got != `{"items":[],"count":0}` {
		t.Fatalf("list after a refused write = %s", got)
	}
}

// state.test.ts:175 it("rejects writes from inside a factory and placeholder use before activation")
func TestStateRejectsWritesFromInsideAFactoryAndPlaceholderUseBeforeActivation(t *testing.T) {
	host := quiet(t, nil)
	writer := emptyLocal("t.writer")
	var captured Ctx
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		captured = ctx
		return nil, nil
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	mustServe(t, host, writer, func(_ any, _ Principal, self Self) (Implementation, error) {
		x, err := self.State("x", tObject(), delta.JsonObjectOf(), nil)
		if err != nil {
			return nil, err
		}
		r, err := self.Resource("r", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return float64(1), nil },
		})
		if err != nil {
			return nil, err
		}
		if _, err := r.Value(); !IsChordError(err, CodeNotReady) {
			t.Errorf("resource value inside the factory: %v", err)
		}
		return chordjson.ObjectOf(), x.Replace(captured, delta.JsonObjectOf(), CellReplaceOptions{})
	}, ServeOptions{})
	if err := readyErr(t, mustRef(t, host, writer, map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("ready: %v", err)
	}
}

// state.test.ts:195 it("listeners run each event in a fresh root; tasks get an abortable ctx")
func TestStateListenersRunEachEventInAFreshRootTasksGetAnAbortableCtx(t *testing.T) {
	host := quiet(t, nil)
	events := newStateEmitter()
	watcher := Local("t.watcher", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"seen", tObject("sum", tNumber())}}})
	var mu sync.Mutex
	roots := map[string]bool{}
	var taskAborted atomic.Bool
	mustServe(t, host, watcher, func(_ any, _ Principal, self Self) (Implementation, error) {
		seen, err := self.State("seen", tObject("sum", tNumber()), delta.JsonObjectOf("sum", float64(0)), nil)
		if err != nil {
			return nil, err
		}
		sourceResource, err := self.Resource("source", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return events, nil },
		})
		if err != nil {
			return nil, err
		}
		if err := self.OnSelect("onEvent", sourceResource,
			func(source any) Subscribable[any] { return source.(*stateEmitter) },
			func(value any, ctx Ctx) error {
				mu.Lock()
				roots[ctx.RootID()] = true
				mu.Unlock()
				return seen.Change(ctx, func(draft any) {
					object := draft.(*delta.Object)
					_ = object.Set("sum", object.Get("sum").(float64)+value.(float64))
				})
			}); err != nil {
			return nil, err
		}
		if err := self.Task(func(ctx Ctx) error {
			<-ctx.Signal().Done()
			taskAborted.Store(true)
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("seen", seen), nil
	}, ServeOptions{})
	ref := mustRef(t, host, watcher, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	events.emit(float64(2))
	events.emit(float64(3))
	seen := readable(t, ref, "seen")
	waitFor(t, "sum 5", func() bool { return jsonText(t, seen.Value()) == `{"sum":5}` })
	mu.Lock()
	distinct := len(roots)
	mu.Unlock()
	if distinct != 2 {
		t.Fatalf("roots %d, want 2", distinct)
	}
	ref.Release()
	waitFor(t, "unsubscribed and aborted", func() bool { return events.size() == 0 && taskAborted.Load() })
}

// state.test.ts:242 it("re-entrant writes publish in adoption order; derivations never go stale (Sol #3)")
func TestStateReEntrantWritesPublishInAdoptionOrderDerivationsNeverGoStale(t *testing.T) {
	host := quiet(t, nil)
	echo := Local("t.reentrant", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"n", tNumber()},
		{"d", tNumber()},
		{"set", Method(tNumber(), nil)},
	}})
	mustServe(t, host, echo, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		d, err := self.Derive(Sources{n}, func(values ...any) any { return values[0].(float64) * 2 })
		if err != nil {
			return nil, err
		}
		// Subscribed first: re-enters the write while revision 1 is being published.
		if err := self.On("bump", n, func(value any, ctx Ctx) error {
			if value == float64(1) {
				return n.Replace(ctx, float64(2), CellReplaceOptions{})
			}
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "d", d, "set", methodFunc(func(value any, ctx Ctx) (any, error) {
			return nil, n.Replace(ctx, value, CellReplaceOptions{})
		})), nil
	}, ServeOptions{})
	ref := mustRef(t, host, echo, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var values, doubled []string
	var replica any
	readable(t, ref, "n").Events(func(event StateEvent[any]) {
		mu.Lock()
		defer mu.Unlock()
		if event.Type == StateEventOps {
			applied, err := delta.ApplyImmutable(replica, event.Ops)
			if err != nil {
				t.Error(err)
			}
			replica = applied
		} else {
			replica = event.Value
		}
		values = append(values, jsonText(t, replica))
	})
	readable(t, ref, "d").Subscribe(func(value any) {
		mu.Lock()
		doubled = append(doubled, jsonText(t, value))
		mu.Unlock()
	})
	mustCall(t, ref, "set", float64(1), nil)
	// Pi asserts after one setImmediate, when the listener's re-entrant write and the derivation's delivery settled.
	waitFor(t, "the re-entrant write and its derivation", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(values) >= 3 && len(doubled) >= 3
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(values, []string{"0", "1", "2"}) {
		t.Fatalf("values %v", values)
	}
	if got := readable(t, ref, "d").Value(); got != float64(4) {
		t.Fatalf("d = %v", got)
	}
	if !slices.Equal(doubled, []string{"0", "2", "4"}) {
		t.Fatalf("doubled %v", doubled)
	}
}
