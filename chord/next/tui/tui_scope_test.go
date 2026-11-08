package tui

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// pi: packages/chord/test/tui/scope.test.ts

var (
	bg    = context.Background()
	Notes = mustContract(contracts.Local("test.notes", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str()), contracts.Mem("write", contracts.NewMethod(contracts.Str()))},
	}))
	Panels = mustRegistry("test.panels")
)

func mustContract(c *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return c
}

func mustRegistry(name string) registries.Registry[string, []string] {
	r, err := registries.DefineRegistry[string, []string](name, registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return r
}

// newHost is a host serving Notes (a writable text) and Panels with one contribution.
func newHost(t testing.TB) host.Host {
	t.Helper()
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(bg); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	facet := host.DefineFacet(func(fh host.FacetHost, self host.FacetSelf) error {
		err := fh.Serve(Notes, func(_ *delta.JsonObject, object host.Self) (contracts.Implementation, error) {
			text := object.Writable("first")
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"text": object.Derive([]any{text}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"write": func(next delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return nil, text.Set(next)
					},
				},
			}}, nil
		}, host.ServeOptions{})
		if err != nil {
			return err
		}
		provider := registries.RegistryProvider[string, []string]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) ([]string, error) {
				var all []string
				for _, entry := range table {
					all = append(all, entry.Value)
				}
				return all, nil
			},
		}
		if err := fh.Serve(registries.Registry[any, any](Panels), provider.Erase(), host.ServeOptions{}); err != nil {
			return err
		}
		return self.Contribute(registries.Registry[any, any](Panels), "notes")
	})
	report, err := h.Load([]host.HostFacet{{Name: "@test/notes", Facet: facet}}, bg)
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("load: %v %v", err, report.Failed)
	}
	return h
}

// wakeups is signalled by every change a test scope reports: until and settle wait on it, not on the clock.
var wakeups = &wakeup{ch: make(chan struct{})}

type wakeup struct {
	mu sync.Mutex
	ch chan struct{}
}

// next is the channel the next poke closes.
func (w *wakeup) next() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

func (w *wakeup) poke() {
	w.mu.Lock()
	defer w.mu.Unlock()
	close(w.ch)
	w.ch = make(chan struct{})
}

// scopeOn is a scope on h whose changes call changed and wake the tests' waits.
func scopeOn(h host.Host, changed func()) *TuiScope {
	return NewTuiScope(bg, h, func() { changed(); wakeups.poke() })
}

// until waits until check holds, re-checking at every change any test scope reports (a missing one fails after 30 s).
func until(t testing.TB, check func() bool) {
	t.Helper()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		woken := wakeups.next()
		if check() {
			return
		}
		select {
		case <-woken:
		case <-timeout.C:
			t.Fatal("condition not reached")
		}
	}
}

// settle waits until no change was reported for a quiet period: the watches of Go deliver on goroutines, where Pi's microtasks
// have all run by the time its `until` returns.
func settle(n *atomic.Int64) {
	for last := n.Load(); ; {
		woken := wakeups.next()
		quiet := time.NewTimer(100 * time.Millisecond)
		select {
		case <-woken:
			quiet.Stop()
		case <-quiet.C:
			if n.Load() == last {
				return
			}
		}
		last = n.Load()
	}
}

func TestScopeHoldsFollowsAndCallsChangedOnEveryChangeReleasingEverythingAtDispose(t *testing.T) {
	h := newHost(t)
	var changes atomic.Int64
	scope := scopeOn(h, func() { changes.Add(1) })
	notes, err := scope.Hold(Notes, delta.NewJsonObject(0))
	if err != nil {
		t.Fatal(err)
	}
	text := WatchMember(scope, notes.Member("text"))
	panels := Registry(scope, Panels)
	until(t, func() bool {
		got, ok := text()
		list, listed := panels()
		return ok && got == "first" && listed && len(list) > 0 && list[0] == "notes"
	})
	before := changes.Load()
	if _, err := notes.Call("write", "second", bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { got, _ := text(); return got == "second" && changes.Load() > before })
	scope.Dispose()
	if kind := notes.State().Value().Kind; kind != host.RefStateEnded {
		t.Errorf("state after dispose = %s, want ended", kind)
	}
	after := changes.Load()
	// A change arriving after Dispose reports nothing (a delivery already past the check may still land: D452).
	scope.redraw()
	if got := changes.Load(); got != after {
		t.Errorf("changed called after dispose: %d -> %d", after, got)
	}
}

type noValue struct{}

func (noValue) Value() string { panic(errors.New("not ready")) }
func (noValue) Watch(context.Context) (chord.NativeWatchHandle[string], error) {
	return nil, errors.New("not ready")
}

func TestScopeReadsNothingFromAWatchableWithoutAValueNeverPanicking(t *testing.T) {
	scope := scopeOn(newHost(t), func() {})
	defer scope.Dispose()
	if got, ok := WatchNative[string](scope, noValue{})(); ok || got != "" {
		t.Errorf("read = %q, %v", got, ok)
	}
}

func TestOptimisticShowsTheValueWhileItsCallIsPendingThenTheReadersCallingChangedOnEachChange(t *testing.T) {
	h := newHost(t)
	var changes atomic.Int64
	scope := scopeOn(h, func() { changes.Add(1) })
	defer scope.Dispose()
	notes, _ := scope.Hold(Notes, delta.NewJsonObject(0))
	read := WatchMember(scope, notes.Member("text"))
	text, add, _ := OptimisticLast[delta.JsonValue, struct{}](scope, func() delta.JsonValue { v, _ := read(); return v })
	until(t, func() bool { return text() == "first" })
	settle(&changes)
	before := changes.Load()
	gate, shown := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		done <- addOnly(add, "second", func() error { close(shown); <-gate; _, err := notes.Call("write", "second", bg); return err })
	}()
	<-shown
	if got := text(); got != "second" {
		t.Errorf("shown = %v, want second", got)
	}
	if got := changes.Load(); got != before+1 {
		t.Errorf("changes = %d, want %d", got, before+1)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return changes.Load() > before+1 })
	if got := text(); got != "second" {
		t.Errorf("after = %v, want second", got)
	}
}

func TestOptimisticShowsTheLastValueUntilEveryCallSettledAFailedCallDropsItAsASucceededOneDoes(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var text func() string
	scope := scopeOn(newHost(t), func() { mu.Lock(); calls = append(calls, text()); mu.Unlock() })
	defer scope.Dispose()
	shown, add, _ := OptimisticLast[string, struct{}](scope, func() string { return "real" })
	text = shown
	first, second := make(chan error), make(chan error)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- addOnly(add, "one", func() error { return <-first }) }()
	// add's redraw ran before it called: wait for it, so the order of actions is the order of calls.
	until(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 1 })
	go func() { secondDone <- addOnly(add, "two", func() error { return <-second }) }()
	until(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 2 })
	first <- nil
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 3 })
	if got := shown(); got != "two" {
		t.Errorf("shown = %q, want two", got)
	}
	second <- errors.New("refused")
	if err := <-secondDone; err == nil || err.Error() != "refused" {
		t.Errorf("failing call = %v", err)
	}
	until(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 4 })
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"one", "two", "two", "real"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestOptimisticAppliesAReducerOverEveryActionInFlightInOrderAndSaysWhetherACallIsPending(t *testing.T) {
	scope := scopeOn(newHost(t), func() {})
	defer scope.Dispose()
	var mu sync.Mutex
	real := []string{"a", "b", "c"}
	list, add, pending := Optimistic[[]string, string, struct{}](scope,
		func() []string { mu.Lock(); defer mu.Unlock(); return real },
		func(current []string, removed string) []string {
			var kept []string
			for _, each := range current {
				if each != removed {
					kept = append(kept, each)
				}
			}
			return kept
		})
	first, second := make(chan error), make(chan error)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- addOnly(add, "a", func() error { return <-first }) }()
	until(t, pending)
	go func() { secondDone <- addOnly(add, "c", func() error { return <-second }) }()
	until(t, func() bool { return reflect.DeepEqual(list(), []string{"b"}) })
	// Unchanged reader and actions: the same value, not a new slice per read.
	if a, b := list(), list(); &a[0] != &b[0] {
		t.Error("two reads gave two slices")
	}
	// The first call settled with its change committed: both actions stay until the second settles too.
	mu.Lock()
	real = []string{"b", "c"}
	mu.Unlock()
	first <- nil
	<-firstDone
	if got := list(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("after first = %v", got)
	}
	mu.Lock()
	real = []string{"b"}
	mu.Unlock()
	second <- nil
	<-secondDone
	if got := list(); &got[0] != &real[0] || pending() {
		t.Errorf("after second = %v, pending = %v", got, pending())
	}
}

func TestOptimisticNeverCallsChangedAfterDispose(t *testing.T) {
	var changes atomic.Int64
	scope := scopeOn(newHost(t), func() { changes.Add(1) })
	_, add, _ := OptimisticLast[string, struct{}](scope, func() string { return "real" })
	call := make(chan error)
	done := make(chan error, 1)
	go func() { done <- addOnly(add, "shown", func() error { return <-call }) }()
	until(t, func() bool { return changes.Load() == 1 })
	scope.Dispose()
	call <- nil
	<-done
	if got := changes.Load(); got != 1 {
		t.Errorf("changes = %d, want 1", got)
	}
}

func TestScopePageStateIsTheHostsAScopeOpenedAfterAnotherWasDisposedFindsItsValue(t *testing.T) {
	h := newHost(t)
	before := scopeOn(h, func() {})
	PageState(before, "draft", "").Set("oat mi")
	before.Dispose()
	after := scopeOn(h, func() {})
	defer after.Dispose()
	if got := PageState(after, "draft", "").Read(); got != "oat mi" {
		t.Errorf("draft = %q", got)
	}
	if got := PageState(after, "scroll", 0).Read(); got != 0 {
		t.Errorf("scroll = %d", got)
	}
}

func TestScopePageStateCallsChangedOnEachOpenScopeFollowingTheKeyAndNeverAfterDispose(t *testing.T) {
	h := newHost(t)
	var calls []string
	redraw := func(name string) func() { return func() { calls = append(calls, name) } }
	first, second := scopeOn(h, redraw("first")), scopeOn(h, redraw("second"))
	draft := PageState(first, "draft", "")
	PageState(second, "draft", "")
	draft.Set("a")
	first.Dispose()
	draft.Set("b")
	if want := []string{"first", "second", "second"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	second.Dispose()
}

func TestScopeMessagesRenderInTheHostsLanguageAtEachCallAChangeOfItCallingChangedUntilDispose(t *testing.T) {
	h := newHost(t)
	count := func(suffix string) surfaces.MessageFunc {
		return func(_ *surfaces.Language, args ...any) (string, error) { return fmtN(args[0], suffix) }
	}
	notes, err := surfaces.DefineMessages("notes",
		surfaces.LocaleCatalog{Locale: "en", Catalog: surfaces.Catalog{"title": "Notes", "count": count("notes")}},
		surfaces.LocaleCatalog{Locale: "de", Catalog: surfaces.Catalog{"title": "Notizen", "count": count("Notizen")}})
	if err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int64
	scope := scopeOn(h, func() { changes.Add(1) })
	tr := scope.Messages(notes)
	if got, _ := tr.T("title"); got != "Notes" {
		t.Errorf("title = %q", got)
	}
	de, _ := surfaces.CreateLanguage("de", surfaces.NoTranslations, func(surfaces.MessagesReport) {})
	surfaces.HostLanguage(h).Set(de)
	if changes.Load() != 1 {
		t.Errorf("changes = %d, want 1", changes.Load())
	}
	title, _ := tr.T("title")
	two, _ := tr.T("count", 2.0)
	if got, want := []string{title, two, tr.Render(notes.Message("count", 3.0))}, []string{"Notizen", "2 Notizen", "3 Notizen"}; !reflect.DeepEqual(got, want) {
		t.Errorf("messages = %v, want %v", got, want)
	}
	scope.Dispose()
	en, _ := surfaces.CreateLanguage("en", surfaces.NoTranslations, func(surfaces.MessagesReport) {})
	surfaces.HostLanguage(h).Set(en)
	if changes.Load() != 1 {
		t.Errorf("changes after dispose = %d, want 1", changes.Load())
	}
}

func fmtN(n any, suffix string) (string, error) {
	formatted, err := surfaces.FormatNumber(nil, n.(float64), nil)
	return formatted + " " + suffix, err
}

// addOnly runs call as an optimistic add of action whose result is none.
func addOnly[A any](add OptimisticAdd[A, struct{}], action A, call func() error) error {
	_, err := add(action, func() (struct{}, error) { return struct{}{}, call() })
	return err
}

// OptimisticAdd is Pi's `<R>(action, call: Promise<R>) => Promise<R>`: it returns call's result and error.
func TestOptimisticAddReturnsTheResultOfItsCall(t *testing.T) {
	scope := scopeOn(newHost(t), func() {})
	defer scope.Dispose()
	_, add, _ := OptimisticLast[string, int](scope, func() string { return "real" })
	run := func(add OptimisticAdd[string, int]) (int, error) { return add("x", func() (int, error) { return 7, nil }) }
	got, err := run(add)
	if err != nil || got != 7 {
		t.Fatalf("add = %v, %v; want 7, nil", got, err)
	}
}

type noJSONValue struct{}

func (noJSONValue) Value() string { panic(errors.New("not ready")) }
func (noJSONValue) Watch(context.Context) (chord.WatchHandle[string], error) {
	return nil, errors.New("not ready")
}

// Pi's TuiScope.watch takes a Watchable too: a JSON watchable that has no value reads as none.
func TestScopeWatchReadsNothingFromAJSONWatchableWithoutAValue(t *testing.T) {
	scope := scopeOn(newHost(t), func() {})
	defer scope.Dispose()
	if got, ok := WatchJSON[string](scope, noJSONValue{})(); ok || got != "" {
		t.Errorf("read = %q, %v", got, ok)
	}
}
