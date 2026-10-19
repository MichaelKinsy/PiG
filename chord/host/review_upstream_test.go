package host

// Ports packages/chord/test/review.test.ts: regression tests for the phase 1a review (findings #1-#11) and the
// requested race tests. Each Go test names its Pi it(...) and line.
//
// Pi's tick()/ticks() await setImmediate turns so pending callbacks and async functions progress. Go's host work runs
// on the host loop and on owned goroutines, so a positive expectation waits for its condition (waitFor), and a
// negative one ("nothing happened yet") is checked after reviewQuiet: the loop drained and a short window for owned
// goroutines, then drained again.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// ─── helpers (review.test.ts:22-51) ─────────────────────────────────────────

// reviewQuiet stands for Pi's `await ticks(n)` before a negative assertion.
func reviewQuiet(host *HostImpl) {
	host.run(func() {})
	time.Sleep(20 * time.Millisecond)
	host.run(func() {})
}

// reviewLatch is review.test.ts:27-33 latch().
type reviewLatch struct {
	once sync.Once
	ch   chan struct{}
}

func newReviewLatch() *reviewLatch { return &reviewLatch{ch: make(chan struct{})} }
func (l *reviewLatch) open()       { l.once.Do(func() { close(l.ch) }) }
func (l *reviewLatch) wait()       { <-l.ch }

// reviewEmitter is review.test.ts:35-51 emitter(): a Set of listeners; subscribe throws when failSubscribe.
type reviewEmitter struct {
	mu            sync.Mutex
	next          int
	ids           []int
	fns           map[int]func(any)
	failSubscribe bool
}

func newReviewEmitter(failSubscribe bool) *reviewEmitter {
	return &reviewEmitter{fns: map[int]func(any){}, failSubscribe: failSubscribe}
}

func (e *reviewEmitter) Subscribe(fn func(event any)) func() {
	if e.failSubscribe {
		panic(errors.New("subscribe fails"))
	}
	e.mu.Lock()
	id := e.next
	e.next++
	e.ids = append(e.ids, id)
	e.fns[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.fns[id]; ok {
			delete(e.fns, id)
			e.ids = slices.DeleteFunc(e.ids, func(other int) bool { return other == id })
		}
	}
}

func (e *reviewEmitter) emit(event any) {
	e.mu.Lock()
	fns := make([]func(any), 0, len(e.ids))
	for _, id := range e.ids {
		fns = append(fns, e.fns[id])
	}
	e.mu.Unlock()
	for _, fn := range fns {
		fn(event)
	}
}

func (e *reviewEmitter) size() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.ids)
}

// reviewLog is a log written from host tasks and read by the test.
type reviewLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *reviewLog) add(entry string) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

func (l *reviewLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

func (l *reviewLog) reset() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}

func (l *reviewLog) count(prefix string) int {
	n := 0
	for _, entry := range l.list() {
		if strings.HasPrefix(entry, prefix) {
			n++
		}
	}
	return n
}

func reviewFacet(name string, setup func(api FacetApi) error) *Facet {
	return &Facet{Name: name, FacetDefinition: FacetDefinition{Setup: setup}}
}

func reviewID(input any) string {
	switch input := input.(type) {
	case map[string]any:
		return input["id"].(string)
	case *chordjson.Object:
		return input.Value("id").(string)
	}
	return ""
}

func reviewGet(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Hold(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func reviewReady(t *testing.T, refs ...Ref) {
	t.Helper()
	for _, ref := range refs {
		if err := readyErr(t, ref); err != nil {
			t.Fatal(err)
		}
	}
}

func reviewState(t *testing.T, self Self, name string, schema Schema, initial any) *Cell[any] {
	t.Helper()
	return reviewStateWith(t, self, name, schema, initial, nil)
}

func reviewStateWith(t *testing.T, self Self, name string, schema Schema, initial any, options *StateOptions) *Cell[any] {
	t.Helper()
	cell, err := self.State(name, schema, initial, options)
	if err != nil {
		panic(err)
	}
	return cell
}

func reviewResource(self Self, name string, spec ResourceDeclaration[any]) Resource[any] {
	resource, err := self.Resource(name, spec)
	if err != nil {
		panic(err)
	}
	return resource
}

func reviewEmpty() Schema { return tObject() }

// reviewItem is review.test.ts:53-57 Item.
var reviewItem = Local("t.item", LocalDef{
	Version: 1,
	Input:   tObject("id", tString()),
	Members: Fields[any]{{"version", Method(nil, tString())}},
})

func reviewVersion(version string) Implementation {
	return chordjson.ObjectOf("version", methodFunc(func(any, Ctx) (any, error) { return version, nil }))
}

// ─── review: reload commit ─────────────────────────────────────────────────

// review.test.ts:60 "#1 a failing listener subscription fails prepare; the code version and all objects stay unchanged"
func TestReviewAFailingListenerSubscriptionFailsPrepareTheCodeVersionAndAllObjectsStayUnchanged(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	sources := map[string]*reviewEmitter{"a": newReviewEmitter(false), "b": newReviewEmitter(true), "c": newReviewEmitter(false)}
	facet := func(version string, listen bool) *Facet {
		return reviewFacet("item", func(api FacetApi) error {
			return api.Serve(reviewItem, func(input any, _ Principal, self Self) (Implementation, error) {
				if listen {
					if err := self.On("events", sources[reviewID(input)], func(any, Ctx) error { return nil }); err != nil {
						return nil, err
					}
				}
				return reviewVersion(version), nil
			}, ServeOptions{})
		})
	}
	if err := host.Install(*facet("v1", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	var refs []Ref
	for _, id := range []string{"a", "b", "c"} {
		refs = append(refs, reviewGet(t, host, reviewItem, map[string]any{"id": id}, nil))
	}
	reviewReady(t, refs...)
	codeVersion := host.CodeVersion()
	if _, err := host.Reload([]PluginRef{facet("v2", true)}, ReloadOptions{}); err == nil || !strings.Contains(err.Error(), "subscribe fails") {
		t.Fatalf("reload = %v, want subscribe fails", err)
	}
	if got := host.CodeVersion(); got != codeVersion {
		t.Fatalf("code version = %d, want %d", got, codeVersion)
	}
	var versions []any
	for _, ref := range refs {
		versions = append(versions, mustCall(t, ref, "version", nil, nil))
	}
	if fmt.Sprint(versions) != "[v1 v1 v1]" {
		t.Fatalf("versions = %v", versions)
	}
	if sources["a"].size() != 0 || sources["c"].size() != 0 {
		t.Fatalf("subscriptions left: a=%d c=%d", sources["a"].size(), sources["c"].size())
	}
}

// review.test.ts:87 "#2 migration is validated against the new remote schemas, not the obsolete ones (dev mode)"
func TestReviewMigrationIsValidatedAgainstTheNewRemoteSchemasNotTheObsoleteOnes(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), Dev: true, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	countV1 := tObject("n", tNumber())
	countV2 := tObject("total", tNumber())
	v1 := Remote("t.counted", RemoteDef{Version: 1, Input: reviewEmpty(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"count", countV1}}}}}})
	v2 := Remote("t.counted", RemoteDef{Version: 2, Input: reviewEmpty(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{State: Fields[Schema]{{"count", countV2}}}}}})
	if err := host.Install(*reviewFacet("counted", func(api FacetApi) error {
		return api.Serve(v1, func(_ any, _ Principal, self Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "count", reviewState(t, self, "count", countV1, delta.JsonObjectOf("n", float64(3)))), nil
		}, ServeOptions{Linger: 60 * time.Second, HasLinger: true})
	}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	first := reviewGet(t, host, v1, map[string]any{}, nil)
	reviewReady(t, first)
	first.Release()
	if _, err := host.Reload([]PluginRef{reviewFacet("counted", func(api FacetApi) error {
		return api.Serve(v2, func(_ any, _ Principal, self Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "count", reviewStateWith(t, self, "count", countV2, delta.JsonObjectOf("total", float64(0)), &StateOptions{Version: 2, Migrate: func(old any, _ int) (any, error) {
				return delta.JsonObjectOf("total", old.(*chordjson.Object).Value("n")), nil
			}})), nil
		}, ServeOptions{Linger: 60 * time.Second, HasLinger: true})
	})}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	second := reviewGet(t, host, v2, map[string]any{}, nil)
	count, ok := second.Member("count").(Readable[any])
	if !ok {
		t.Fatal("count is not state")
	}
	var values reviewLog
	count.Subscribe(func(value any) { values.add(fmt.Sprint(value)) })
	reviewReady(t, second)
	waitFor(t, "migrated count", func() bool { return jsonText(t, count.Value()) == `{"total":3}` })
}

// review.test.ts:136 "#1 a commit that still fails midway fails the host terminally (no half-published code version serving
// work)". Pi replaces every object's startTasks (white box); Go replaces the package's start step.
func TestReviewACommitThatStillFailsMidwayFailsTheHostTerminally(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Install(*reviewFacet("item", func(api FacetApi) error {
		return api.Serve(reviewItem, func(any, Principal, Self) (Implementation, error) { return reviewVersion("v1"), nil }, ServeOptions{})
	}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, reviewItem, map[string]any{"id": "a"}, nil)
	reviewReady(t, ref)
	// Everything fallible runs in prepare now, so break an install step directly (white box).
	saved := objectStartTasks
	objectStartTasks = func(*ObjectImpl) { panic(errors.New("install step fails")) }
	t.Cleanup(func() { objectStartTasks = saved })
	if _, err := host.Reload([]PluginRef{reviewFacet("item", func(api FacetApi) error {
		return api.Serve(reviewItem, func(any, Principal, Self) (Implementation, error) { return reviewVersion("v2"), nil }, ServeOptions{})
	})}, ReloadOptions{}); err == nil || !strings.Contains(err.Error(), "install step fails") {
		t.Fatalf("reload = %v, want install step fails", err)
	}
	objectStartTasks = saved
	_, err := callMember(ref, "version", nil, nil)
	var chordError *ChordError
	if !IsChordError(err, CodeUnavailable) || !errors.As(err, &chordError) || chordError.Reason != "host_failed" {
		t.Fatalf("version = %v, want unavailable/host_failed", err)
	}
}

// review.test.ts:159 "reentrant observers calling another object during a commit see the whole new code version"
func TestReviewReentrantObserversCallingAnotherObjectDuringACommitSeeTheWholeNewCodeVersion(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	other := Local("t.other", LocalDef{Version: 1, Input: reviewEmpty(), Members: Fields[any]{{"version", Method(nil, tString())}}})
	facet := func(version string) *Facet {
		return reviewFacet("pair", func(api FacetApi) error {
			if err := api.Serve(reviewItem, func(any, Principal, Self) (Implementation, error) { return reviewVersion(version), nil }, ServeOptions{}); err != nil {
				return err
			}
			return api.Serve(other, func(any, Principal, Self) (Implementation, error) { return reviewVersion(version), nil }, ServeOptions{})
		})
	}
	if err := host.Install(*facet("v1"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	item := reviewGet(t, host, reviewItem, map[string]any{"id": "a"}, nil)
	otherRef := reviewGet(t, host, other, map[string]any{}, nil)
	reviewReady(t, item, otherRef)
	// Pi pushes the call's promise synchronously in the observer; Go starts the call there and awaits it later.
	type pending struct {
		value any
		err   error
	}
	var mu sync.Mutex
	var seen []chan pending
	item.OnShape(func(Shape) {
		result := make(chan pending, 1)
		mu.Lock()
		seen = append(seen, result)
		mu.Unlock()
		method, _ := otherRef.Member("version").(methodFunc)
		go func() {
			value, err := method(nil, nil)
			result <- pending{value, err}
		}()
	})
	if _, err := host.Reload([]PluginRef{facet("v2")}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	results := slices.Clone(seen)
	mu.Unlock()
	var got []any
	for _, result := range results {
		outcome := <-result
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		got = append(got, outcome.value)
	}
	if fmt.Sprint(got) != "[v2]" {
		t.Fatalf("seen = %v, want [v2]", got)
	}
}

// ─── review: leases, invocations, activation ───────────────────────────────

// reviewStore is review.test.ts:180-184 Store.
var reviewStore = Local("t.store", LocalDef{Version: 1, Input: reviewEmpty(), Members: Fields[any]{{"count", tUnknown()}}})

// review.test.ts:186 "#4 a running listener holds a lease: the gate waits for it before migrating"
func TestReviewARunningListenerRefsALeaseTheGateWaitsForItBeforeMigrating(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	events := newReviewEmitter(false)
	gate := newReviewLatch()
	var seen reviewLog
	if err := host.Install(*reviewFacet("store", func(api FacetApi) error {
		return api.Serve(reviewStore, func(_ any, _ Principal, self Self) (Implementation, error) {
			count := reviewState(t, self, "count", tObject("n", tNumber()), delta.JsonObjectOf("n", float64(1)))
			if err := self.On("slow", events, func(any, Ctx) error {
				gate.wait()
				seen.add(jsonText(t, count.Value()))
				return nil
			}); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("count", count), nil
		}, ServeOptions{})
	}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, reviewStore, map[string]any{}, nil)
	reviewReady(t, ref)
	events.emit(1.0)
	reviewQuiet(host)
	codeVersion := host.CodeVersion()
	deadline := 2000 * time.Millisecond
	reloaded := make(chan error, 1)
	go func() {
		_, err := host.Reload([]PluginRef{reviewFacet("store", func(api FacetApi) error {
			return api.Serve(reviewStore, func(_ any, _ Principal, self Self) (Implementation, error) {
				return chordjson.ObjectOf("count", reviewStateWith(t, self, "count", tObject("total", tNumber()), delta.JsonObjectOf("total", float64(0)), &StateOptions{Version: 2, Migrate: func(old any, _ int) (any, error) {
					return delta.JsonObjectOf("total", old.(*chordjson.Object).Value("n")), nil
				}})), nil
			}, ServeOptions{})
		})}, ReloadOptions{GateDeadline: &deadline})
		reloaded <- err
	}()
	reviewQuiet(host)
	if got := host.CodeVersion(); got != codeVersion {
		t.Fatalf("the gate must wait for the listener: code version %d, want %d", got, codeVersion)
	}
	gate.open()
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	if got := seen.list(); fmt.Sprint(got) != `[{"n":1}]` {
		t.Fatalf("seen = %v", got)
	}
	if got := jsonText(t, ref.Member("count").(Readable[any]).Value()); got != `{"total":1}` {
		t.Fatalf("count = %s", got)
	}
}

// review.test.ts:233 "#5 disposal waits for running methods before closing resources (released and forced)"
func TestReviewDisposalWaitsForRunningMethodsBeforeClosingResources(t *testing.T) {
	worker := Local("t.busy", LocalDef{Version: 1, Input: tObject("id", tString()), Members: Fields[any]{{"work", Method(nil, tBoolean())}}})
	var log reviewLog
	var goMu sync.Mutex
	gate := newReviewLatch()
	currentGate := func() *reviewLatch { goMu.Lock(); defer goMu.Unlock(); return gate }
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	type conn struct {
		mu     sync.Mutex
		closed bool
	}
	if err := host.Serve(worker, func(input any, _ Principal, self Self) (Implementation, error) {
		id := reviewID(input)
		res := reviewResource(self, "conn", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return &conn{}, nil },
			Close: func(value any) error {
				c := value.(*conn)
				c.mu.Lock()
				c.closed = true
				c.mu.Unlock()
				log.add("close " + id)
				return nil
			},
		})
		return chordjson.ObjectOf("work", methodFunc(func(any, Ctx) (any, error) {
			currentGate().wait()
			log.add("done " + id)
			value, err := res.Value()
			if err != nil {
				return nil, err
			}
			c := value.(*conn)
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.closed, nil
		})), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		value any
		err   error
	}
	call := func(ref Ref) chan outcome {
		result := make(chan outcome, 1)
		method, _ := ref.Member("work").(methodFunc)
		go func() {
			value, err := method(nil, nil)
			result <- outcome{value, err}
		}()
		return result
	}

	released := reviewGet(t, host, worker, map[string]any{"id": "r"}, nil)
	releasedCall := call(released)
	reviewQuiet(host)
	released.Release()
	reviewQuiet(host)
	gate.open()
	if got := <-releasedCall; got.err != nil || got.value != false {
		t.Fatalf("released work = %v, %v; want false", got.value, got.err)
	}
	waitFor(t, "close r", func() bool { return len(log.list()) == 2 })
	if got := log.list(); fmt.Sprint(got) != "[done r close r]" {
		t.Fatalf("log = %v", got)
	}

	goMu.Lock()
	gate = newReviewLatch()
	goMu.Unlock()
	log.reset()
	forced := reviewGet(t, host, worker, map[string]any{"id": "f"}, &GetOptions{Principal: map[string]any{"user": "u"}, HasPrincipal: true})
	forcedCall := call(forced)
	reviewQuiet(host)
	invalidated := make(chan error, 1)
	go func() {
		invalidated <- host.InvalidatePrincipal(PrincipalMatch{Predicate: func(principal Principal) bool {
			object, ok := principal.(*chordjson.Object)
			if ok {
				return object.Value("user") == "u"
			}
			mapped, ok := principal.(map[string]any)
			return ok && mapped["user"] == "u"
		}})
	}()
	waitFor(t, "forced gone", func() bool { return forced.Status() == RefGone })
	reviewQuiet(host)
	if got := log.list(); len(got) != 0 {
		t.Fatalf("log before the method finished = %v", got)
	}
	currentGate().open()
	if err := <-invalidated; err != nil {
		t.Fatal(err)
	}
	if got := <-forcedCall; got.err != nil || got.value != false {
		t.Fatalf("forced work = %v, %v; want false", got.value, got.err)
	}
	if got := log.list(); fmt.Sprint(got) != "[done f close f]" {
		t.Fatalf("log = %v", got)
	}
}

// review.test.ts:285 "#6 a registry conflict found while finalizing activation unwinds the loser completely"
func TestReviewARegistryConflictFoundWhileFinalizingActivationUnwindsTheLoserCompletely(t *testing.T) {
	var errMu sync.Mutex
	var reported []error
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(err error) { errMu.Lock(); reported = append(reported, err); errMu.Unlock() }})
	t.Cleanup(func() { _ = host.Close() })
	routes := DefineRegistry[string]("t.review-routes")
	var log reviewLog
	for _, name := range []string{"t.eager-a", "t.eager-b"} {
		typ := Local(name, LocalDef{Version: 1, Input: reviewEmpty()})
		if err := host.Serve(typ, func(_ any, _ Principal, self Self) (Implementation, error) {
			reviewResource(self, "r", ResourceDeclaration[any]{
				Version: 1,
				Open:    func(ResourceOpenOptions) (any, error) { log.add("open " + typ.Name); return nil, nil },
				Close:   func(any) error { log.add("close " + typ.Name); return nil },
			})
			if err := self.Register(Registry[any](routes), []Entry{{Name: "same", Value: typ.Name}}); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(), nil
		}, ServeOptions{Lifetime: LifetimeHost}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "two opens and one close", func() bool { return log.count("open") == 2 && log.count("close") == 1 })
	reviewQuiet(host)
	if log.count("open") != 2 || log.count("close") != 1 {
		t.Fatalf("log = %v", log.list())
	}
	if got := host.Registry(Registry[any](routes)).Table(nil).Len(); got != 1 {
		t.Fatalf("table size = %d", got)
	}
	errMu.Lock()
	defer errMu.Unlock()
	if !slices.ContainsFunc(reported, func(err error) bool {
		var chordError *ChordError
		return IsChordError(err, CodeInvalid) && errors.As(err, &chordError) && chordError.Reason == "registry_conflict"
	}) {
		t.Fatalf("reported = %v, want invalid/registry_conflict", reported)
	}
}

// review.test.ts:318 "#6 releasing the last hold while listeners start up does not resurrect the object"
func TestReviewReleasingTheLastRefWhileListenersStartUpDoesNotResurrectTheObject(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var log reviewLog
	var mu sync.Mutex
	var ref Ref
	runs := 0
	if err := host.Serve(reviewStore, func(_ any, _ Principal, self Self) (Implementation, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		count := reviewState(t, self, "count", tNumber(), float64(0))
		reviewResource(self, "r", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { log.add("open"); return nil, nil },
			Close:   func(any) error { log.add("close"); return nil },
		})
		// A readable delivers its current value as the listener starts: release the only hold right then.
		if err := self.On("drop", count, func(any, Ctx) error {
			mu.Lock()
			current := ref
			mu.Unlock()
			if current != nil {
				current.Release()
			}
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("count", count), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	// Pi's `ref = host.hold(Store, {})` is one synchronous turn: nothing the hold queues, the listener's start included,
	// runs before ref is set. The section is that turn; a hold followed by an assignment outside it lets the activation
	// start the listener first, which then finds no ref to release.
	var first Ref
	host.run(func() {
		first = reviewGet(t, host, reviewStore, map[string]any{}, nil)
		mu.Lock()
		ref = first
		mu.Unlock()
	})
	_ = readyErr(t, first)
	waitFor(t, "open, close", func() bool { return len(log.list()) == 2 })
	reviewQuiet(host)
	if got := log.list(); fmt.Sprint(got) != "[open close]" {
		t.Fatalf("log = %v", got)
	}
	var again Ref
	host.run(func() {
		again = reviewGet(t, host, reviewStore, map[string]any{}, nil)
		mu.Lock()
		ref = nil
		mu.Unlock()
	})
	reviewReady(t, again)
	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Fatalf("runs = %d, want 2", runs)
	}
}

// ─── review: prepare isolation and contracts ───────────────────────────────

// reviewDb is review.test.ts:345-349 Db.
var reviewDb = Local("t.db", LocalDef{Version: 1, Input: reviewEmpty(), Members: Fields[any]{{"read", Method(nil, tUnknown())}}})

// review.test.ts:351 "#7 a prepare rerun cannot touch retained resources, even open ones"
func TestReviewAPrepareRerunCannotTouchRetainedResourcesEvenOpenOnes(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	native := map[string]any{"mutated": false}
	facet := func(touch bool) *Facet {
		return reviewFacet("db", func(api FacetApi) error {
			return api.Serve(reviewDb, func(_ any, _ Principal, self Self) (Implementation, error) {
				db := reviewResource(self, "db", ResourceDeclaration[any]{Version: 1, Open: func(ResourceOpenOptions) (any, error) { return native, nil }})
				if touch {
					value, err := db.Value()
					if err != nil {
						return nil, err
					}
					value.(map[string]any)["mutated"] = true
				}
				return chordjson.ObjectOf("read", methodFunc(func(any, Ctx) (any, error) { return db.Value() })), nil
			}, ServeOptions{})
		})
	}
	if err := host.Install(*facet(false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, reviewDb, map[string]any{}, nil)
	reviewReady(t, ref)
	if _, err := host.Reload([]PluginRef{facet(true)}, ReloadOptions{}); !IsChordError(err, CodeNotReady) {
		t.Fatalf("reload = %v, want not_ready", err)
	}
	if native["mutated"] != false {
		t.Fatal("the prepare rerun touched the retained resource")
	}
	if got := jsonText(t, mustCall(t, ref, "read", nil, nil)); got != `{"mutated":false}` {
		t.Fatalf("read = %s", got)
	}
}

// review.test.ts:372 "#7 objects of types a reload adds start listeners and tasks only at commit"
func TestReviewObjectsOfTypesAReloadAddsStartListenersAndTasksOnlyAtCommit(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var log reviewLog
	fresh := Local("t.fresh", LocalDef{Version: 1, Input: reviewEmpty()})
	if err := host.Install(*reviewFacet("item", func(api FacetApi) error {
		return api.Serve(reviewItem, func(any, Principal, Self) (Implementation, error) { return reviewVersion("v1"), nil }, ServeOptions{})
	}), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	reviewReady(t, reviewGet(t, host, reviewItem, map[string]any{"id": "a"}, nil), reviewGet(t, host, reviewItem, map[string]any{"id": "b"}, nil))
	plugins := func(fail bool) []PluginRef {
		return []PluginRef{
			reviewFacet("fresh", func(api FacetApi) error {
				return api.Serve(fresh, func(_ any, _ Principal, self Self) (Implementation, error) {
					if err := self.Task(func(Ctx) error { log.add("task started"); return nil }); err != nil {
						return nil, err
					}
					return chordjson.ObjectOf(), nil
				}, ServeOptions{})
			}),
			reviewFacet("item", func(api FacetApi) error {
				return api.Serve(reviewItem, func(input any, _ Principal, self Self) (Implementation, error) {
					if _, err := self.Get(fresh, map[string]any{}, nil); err != nil {
						return nil, err
					}
					if fail && reviewID(input) == "b" {
						return nil, errors.New("b fails")
					}
					return reviewVersion("v2"), nil
				}, ServeOptions{})
			}),
		}
	}
	if _, err := host.Reload(plugins(true), ReloadOptions{}); err == nil || !strings.Contains(err.Error(), "b fails") {
		t.Fatalf("reload = %v, want b fails", err)
	}
	reviewQuiet(host)
	if got := log.list(); len(got) != 0 {
		t.Fatalf("log after the failed reload = %v", got)
	}
	if _, err := host.Reload(plugins(false), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task started", func() bool { return len(log.list()) > 0 })
	reviewQuiet(host)
	if got := log.list(); fmt.Sprint(got) != "[task started]" {
		t.Fatalf("log = %v", got)
	}
}

// review.test.ts:411 "#8 a breaking provider change and its updated consumer reload in one batch"
func TestReviewABreakingProviderChangeAndItsUpdatedConsumerReloadInOneBatch(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	p1 := Remote("t.provider", RemoteDef{Version: 1, Input: reviewEmpty(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{"get", Method(nil, tString())}}}}}})
	p2 := Remote("t.provider", RemoteDef{Version: 2, Input: reviewEmpty(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{"fetch", Method(nil, tString())}}}}}})
	consumer := Local("t.consumer", LocalDef{Version: 1, Input: reviewEmpty(), Members: Fields[any]{{"ask", Method(nil, tString())}}})
	facet := func(provider *Contract, member, answer string) *Facet {
		return reviewFacet("both", func(api FacetApi) error {
			if err := api.Serve(provider, func(any, Principal, Self) (Implementation, error) {
				return chordjson.ObjectOf("variant", "main", member, methodFunc(func(any, Ctx) (any, error) { return answer, nil })), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return api.Serve(consumer, func(_ any, _ Principal, self Self) (Implementation, error) {
				held, err := self.Get(provider, map[string]any{}, nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("ask", methodFunc(func(_ any, ctx Ctx) (any, error) { return callMember(held, member, nil, ctx) })), nil
			}, ServeOptions{})
		})
	}
	if err := host.Install(*facet(p1, "get", "old"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, consumer, map[string]any{}, nil)
	if got := mustCall(t, ref, "ask", nil, nil); got != "old" {
		t.Fatalf("ask = %v, want old", got)
	}
	if _, err := host.Reload([]PluginRef{facet(p2, "fetch", "new")}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, ref, "ask", nil, nil); got != "new" {
		t.Fatalf("ask = %v, want new", got)
	}
}

// review.test.ts:457 "#9 an exclusive resource whose close failed is restored on rollback, or marked unavailable"
func TestReviewAnExclusiveResourceWhoseCloseFailedIsRestoredOnRollbackOrMarkedUnavailable(t *testing.T) {
	for _, reopen := range []string{"works", "fails"} {
		t.Run(reopen, func(t *testing.T) {
			host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
			t.Cleanup(func() { _ = host.Close() })
			var log reviewLog
			var mu sync.Mutex
			opens := 0
			facet := func(rev int) *Facet {
				return reviewFacet("db", func(api FacetApi) error {
					return api.Serve(reviewDb, func(_ any, _ Principal, self Self) (Implementation, error) {
						port := reviewResource(self, "port", ResourceDeclaration[any]{
							Version:   rev,
							Exclusive: true,
							Open: func(ResourceOpenOptions) (any, error) {
								if rev == 2 {
									return nil, errors.New("new port fails")
								}
								mu.Lock()
								opens++
								n := opens
								mu.Unlock()
								if n > 1 && reopen == "fails" {
									return nil, errors.New("reopen fails")
								}
								log.add(fmt.Sprintf("open %d", rev))
								return map[string]any{"rev": float64(rev)}, nil
							},
							Close: func(any) error {
								log.add(fmt.Sprintf("close %d", rev))
								return errors.New("close fails")
							},
						})
						return chordjson.ObjectOf("read", methodFunc(func(any, Ctx) (any, error) { return port.Value() })), nil
					}, ServeOptions{})
				})
			}
			if err := host.Install(*facet(1), LoadOptions{}); err != nil {
				t.Fatal(err)
			}
			ref := reviewGet(t, host, reviewDb, map[string]any{}, nil)
			reviewReady(t, ref)
			// The failing close aborts the reload at the gate; the old resource must not stay closed.
			if _, err := host.Reload([]PluginRef{facet(2)}, ReloadOptions{}); err == nil || !strings.Contains(err.Error(), "close fails") {
				t.Fatalf("reload = %v, want close fails", err)
			}
			if reopen == "works" {
				if got := log.list(); fmt.Sprint(got) != "[open 1 close 1 open 1]" {
					t.Fatalf("log = %v", got)
				}
				if got := jsonText(t, mustCall(t, ref, "read", nil, nil)); got != `{"rev":1}` {
					t.Fatalf("read = %s", got)
				}
			} else if _, err := callMember(ref, "read", nil, nil); !IsChordError(err, CodeUnavailable) {
				t.Fatalf("read = %v, want unavailable", err)
			}
		})
	}
}

// review.test.ts:503 "#10 retirement closes dropped resources before releasing dropped holds"
func TestReviewRetirementClosesDroppedResourcesBeforeReleasingDroppedRefs(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	var log reviewLog
	dep := Local("t.dep", LocalDef{Version: 1, Input: reviewEmpty()})
	facet := func(withDecls bool) *Facet {
		return reviewFacet("item", func(api FacetApi) error {
			if err := api.Serve(dep, func(_ any, _ Principal, self Self) (Implementation, error) {
				reviewResource(self, "r", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { return nil, nil },
					Close:   func(any) error { log.add("dep closed"); return nil },
				})
				return chordjson.ObjectOf(), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return api.Serve(reviewItem, func(_ any, _ Principal, self Self) (Implementation, error) {
				if withDecls {
					if _, err := self.Get(dep, map[string]any{}, nil); err != nil {
						return nil, err
					}
					// Slow close: releasing the hold first would let the dependency close before it finishes.
					reviewResource(self, "own", ResourceDeclaration[any]{
						Version: 1,
						Open:    func(ResourceOpenOptions) (any, error) { return nil, nil },
						Close: func(any) error {
							time.Sleep(5 * time.Millisecond) // Pi: await ticks(3)
							log.add("own closed")
							return nil
						},
					})
				}
				version := "v2"
				if withDecls {
					version = "v1"
				}
				return reviewVersion(version), nil
			}, ServeOptions{})
		})
	}
	if err := host.Install(*facet(true), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, reviewItem, map[string]any{"id": "a"}, nil)
	reviewReady(t, ref)
	gate := newReviewLatch()
	entered := make(chan struct{})
	pinned := make(chan error, 1)
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(ref, "version", nil, ctx); err != nil {
				return nil, err
			}
			close(entered)
			gate.wait()
			return nil, nil
		}, RootOptions{})
		pinned <- err
	}()
	<-entered
	if _, err := host.Reload([]PluginRef{facet(false)}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := log.list(); len(got) != 0 {
		t.Fatalf("log before the pinned root ended = %v", got)
	}
	gate.open()
	if err := <-pinned; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both closed", func() bool { return len(log.list()) == 2 })
	reviewQuiet(host)
	if got := log.list(); fmt.Sprint(got) != "[own closed dep closed]" {
		t.Fatalf("log = %v", got)
	}
}

// review.test.ts:552 "#11 fits has no false positives for unmodelled keywords; counterexamples checked with Value.Check"
func TestReviewFitsHasNoFalsePositivesForUnmodelledKeywords(t *testing.T) {
	cases := []struct {
		sub, sup Schema
		value    any
	}{
		{tString(), tString(Schema{"not": Schema{"const": "x"}}), "x"},
		{tArray(tNumber()), tArray(tNumber(), Schema{"contains": tLiteral(1)}), []any{2.0}},
		{tObject("a", tString()), tObject("a", tString(), Schema{"additionalProperties": tNumber()}), map[string]any{"a": "s", "b": "t"}},
		{tObject("x", tString()), tObject(Schema{"patternProperties": Schema{"^x$": tNumber()}}), map[string]any{"x": "s"}},
		// Type.Intersect([...]) is { allOf: [...] }.
		{tNumber(), Schema{"allOf": []any{tNumber(), tNumber(Schema{"minimum": 1})}}, 0.0},
		{tObject("a", tString(), Schema{"additionalProperties": tNumber()}), tObject("a", tString(), "b", tOptional(tString())), map[string]any{"a": "", "b": 1.0}},
	}
	for _, item := range cases {
		if !typebox.Check(item.sub, item.value) || typebox.Check(item.sup, item.value) {
			t.Fatalf("bad counterexample %v", item.value)
		}
		if Fits(item.sub, item.sup) {
			t.Fatalf("fits(%s, %s)", CanonicalJSON(item.sub), CanonicalJSON(item.sup))
		}
	}
	// Additive changes still fit.
	if !Fits(tObject("a", tString()), tObject("a", tString(), "b", tOptional(tNumber()))) {
		t.Fatal("an optional addition does not fit")
	}
	if !Fits(tObject("a", tString(), "extra", tNumber()), tObject("a", tString())) {
		t.Fatal("an extra field does not fit")
	}
}

// review.test.ts:584 "#11 arguments are stripped to what the consumer knows, then to what the provider knows"
func TestReviewArgumentsAreStrippedToWhatTheConsumerKnowsThenToWhatTheProviderKnows(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	strict := Remote("t.strict", RemoteDef{Version: 1, Input: reviewEmpty(), Host: NewHostNamed("host"), Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{
		{"echo", Method(tObject("a", tString(), Schema{"additionalProperties": false}), tUnknown())},
	}}}}})
	if err := host.Serve(strict, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "echo", methodFunc(func(args any, _ Ctx) (any, error) { return args, nil })), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := reviewGet(t, host, strict, map[string]any{}, nil)
	if got := jsonText(t, mustCall(t, ref, "echo", map[string]any{"a": "x", "junk": 1.0}, nil)); got != `{"a":"x"}` {
		t.Fatalf("echo = %s", got)
	}
}

// ─── review: ctx races ─────────────────────────────────────────────────────

// reviewTarget is review.test.ts:602-606 Target.
var reviewTarget = Local("t.target", LocalDef{Version: 1, Input: reviewEmpty(), Members: Fields[any]{{"hit", Method(nil, nil)}}})

// review.test.ts:608 "an aborted task ctx cannot start fresh calls"
func TestReviewAnAbortedTaskCtxCannotStartFreshCalls(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	runner := Local("t.runner", LocalDef{Version: 1, Input: reviewEmpty()})
	var mu sync.Mutex
	hits := 0
	gate := newReviewLatch()
	outcome := make(chan error, 1)
	if err := host.Serve(reviewTarget, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("hit", methodFunc(func(any, Ctx) (any, error) {
			mu.Lock()
			hits++
			mu.Unlock()
			return nil, nil
		})), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := host.Serve(runner, func(_ any, _ Principal, self Self) (Implementation, error) {
		target, err := self.Get(reviewTarget, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		if err := self.Task(func(ctx Ctx) error {
			gate.wait()
			_, err := callMember(target, "hit", nil, ctx)
			outcome <- err
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	runnerRef := reviewGet(t, host, runner, map[string]any{}, nil)
	reviewReady(t, runnerRef)
	target := reviewGet(t, host, reviewTarget, map[string]any{}, nil)
	reviewReady(t, target)
	runnerRef.Release()
	reviewQuiet(host)
	gate.open()
	err := <-outcome
	reviewQuiet(host)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("hits = %d, want 0", hits)
	}
	if !IsChordError(err, CodeAborted) {
		t.Fatalf("outcome = %v, want aborted", err)
	}
}

// review.test.ts:641 "an expired ctx and a foreign ctx are refused"
func TestReviewAnExpiredCtxAndAForeignCtxAreRefused(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	other := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close(); _ = other.Close() })
	if err := host.Serve(reviewTarget, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("hit", methodFunc(func(any, Ctx) (any, error) { return nil, nil })), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	target := reviewGet(t, host, reviewTarget, map[string]any{}, nil)
	reviewReady(t, target)
	expired, err := host.Root(func(ctx Ctx) (any, error) { return ctx, nil }, RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reason := func(err error) string {
		if chordError, ok := errors.AsType[*ChordError](err); ok {
			return string(chordError.Code) + "/" + chordError.Reason
		}
		return fmt.Sprint(err)
	}
	if _, err := callMember(target, "hit", nil, expired.(Ctx)); !IsChordError(err, CodeAborted) || reason(err) != "aborted/expired" {
		t.Fatalf("expired ctx: %v, want aborted/expired", err)
	}
	foreign, err := other.Root(func(ctx Ctx) (any, error) {
		_, err := callMember(target, "hit", nil, ctx)
		if !IsChordError(err, CodeInvalid) || reason(err) != "invalid/foreign_ctx" {
			return nil, fmt.Errorf("foreign ctx: %v, want invalid/foreign_ctx", err)
		}
		return true, nil
	}, RootOptions{})
	if err != nil || foreign != true {
		t.Fatalf("foreign = %v, %v", foreign, err)
	}
}

// forgedCtx is a Ctx not made by Chord (Pi's `{ rootId, codeVersion, signal } as unknown as Ctx`).
type forgedCtx struct{ signal context.Context }

func (ctx forgedCtx) Signal() context.Context { return ctx.signal }
func (forgedCtx) RootID() string              { return "forged" }
func (forgedCtx) CodeVersion() int            { return 1 }
func (forgedCtx) Principal() Principal        { return nil }
func (forgedCtx) Get(*Contract, any, *GetOptions) (Ref, error) {
	return nil, errors.New("forged ctx")
}

// review.test.ts:649-685 (Pi 9a3ca68b, #10959) "a remote reference refuses a foreign ctx and a ctx not made by Chord,
// without calling": the same checks as a local reference (invalid/foreign_ctx), before anything crosses the link.
func TestReviewARemoteReferenceRefusesAForeignCtxAndACtxNotMadeByChordWithoutCalling(t *testing.T) {
	remoteTarget := Remote("t.remote-target", RemoteDef{
		Version: 1, Input: tObject(), Host: NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{"hit", Method(nil, nil)}}}}},
	})
	var hits atomic.Int32
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, remoteTarget, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "hit", methodFunc(func(any, Ctx) (any, error) {
			hits.Add(1)
			return nil, nil
		})), nil
	}, ServeOptions{})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Kind: KindHost, Name: new("worker"), Principal: map[string]any{"worker": true}}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	worker := requestsHost(t, HostOptions{Name: "worker"})
	worker.Connect(link, ConnectOptions{Principal: kindServer()})
	other := requestsHost(t, HostOptions{Name: "other"})
	target := bindingsHold(t, worker, remoteTarget, map[string]any{}, nil)
	ready(t, target)
	foreign, err := other.Root(func(ctx Ctx) (any, error) {
		_, err := callMember(target, "hit", nil, ctx)
		return err, nil
	}, RootOptions{})
	if err != nil || !IsChordError(foreign.(error), CodeInvalid) || foreign.(*ChordError).Reason != "foreign_ctx" {
		t.Fatalf("foreign ctx: %v, %v, want invalid/foreign_ctx", foreign, err)
	}
	var chordError *ChordError
	if _, err := callMember(target, "hit", nil, forgedCtx{signal: context.Background()}); !errors.As(err, &chordError) || chordError.Code != CodeInvalid || chordError.Reason != "foreign_ctx" {
		t.Fatalf("forged ctx: %v, want invalid/foreign_ctx", err)
	}
	reviewQuiet(worker)
	if got := hits.Load(); got != 0 {
		t.Fatalf("hits = %d, want 0", got)
	}
	if got := link.countSent("call"); got != 0 {
		t.Fatalf("calls sent = %d, want 0", got)
	}
	if _, err := worker.Root(func(ctx Ctx) (any, error) { return callMember(target, "hit", nil, ctx) }, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
}
