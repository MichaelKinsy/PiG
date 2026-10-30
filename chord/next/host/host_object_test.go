package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/host/object.test.ts: objects (§6): identity and lifetime, retries, attempts parked on
// dependencies, cycle checks, collection, `ref.retry()`, linger, cycles across hosts, a failed activation's cleanup and a
// live object failing. Every case runs a real host; the cases Pi runs under `fakeTimers()` run on the scopeLoop and move the
// clock with advanceHost. What Pi awaits in a handler or an opener is a blocking Go function on its task.

var (
	objStore = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("count", contracts.Int()),
			contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())))),
			contracts.Mem("clear", contracts.NewMethod()),
		},
	}))
	objSvc = mustContract(contracts.Local("app.svc", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	objCore = mustContract(contracts.Local("app.core", contracts.LocalDeclaration{
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	objView = mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
)

// serveStore is the store: a list document, its count, and methods to add and clear.
func serveStore(self Self) contracts.Implementation {
	list := self.State(attemptListDoc).(*documents.Singleton)
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"count": self.Derive([]any{list}, func(values []any) (delta.JsonValue, error) {
				return float64(len(values[0].(*delta.JsonObject).Value("items").([]any))), nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{
			"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				text := args.(*delta.JsonObject).Value("text")
				_, err := list.Change(func(draft *delta.Object) (any, error) {
					_, err := draft.Array("items").Push(text)
					return nil, err
				}, ctx)
				return nil, err
			},
			"clear": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, list.Retire(ctx) },
		},
	}}
}

func nOnly(n delta.JsonValue) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(n)}}}
}

// flakyFactory is a factory with one native whose open throws "offline" on the attempts fails picks (counted from 1), and
// the times (fake, from 0) of its attempts. gate, when given, holds every open that does not fail until it fires.
type flakyFactory struct {
	mu    sync.Mutex
	times []int64
}

func (f *flakyFactory) all() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.times)
}

func (f *flakyFactory) factory(loop eventLoop, fails func(attempt int) bool, gate signal) func(*delta.JsonObject, Self) (contracts.Implementation, error) {
	return func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		f.mu.Lock()
		f.times = append(f.times, loop.Now())
		attempt := len(f.times)
		f.mu.Unlock()
		self.Open(func(context.Context) (any, error) {
			if fails(attempt) {
				return nil, errors.New("offline")
			}
			if gate != nil {
				<-gate
			}
			return nil, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		return nOnly(float64(attempt)), nil
	}
}

// --- identity and lifetime ---------------------------------------------------------------------------------------

func TestObjectIdentityOneObjectPerParamsKeyAParamsTypeLivesFromItsFirstGetToItsLastRelease(t *testing.T) {
	var factory atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) { return serveStore(self), nil }, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factory.Add(1)
			id := params.Value("id")
			store := self.Get(objStore, jsonObject())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": self.Derive([]any{store.Member("count")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%v: %v", id, values[0]), nil
				})},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	a := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	b := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	c := mustGet(t, host, attemptPage, jsonObject("id", 2.0))
	if got := factory.Load(); got != 2 {
		t.Fatalf("factory ran %d times, want 2", got)
	}
	watch, err := a.Member("label").Watch(background)
	must(t, err)
	a.Release()
	if v, err := b.Member("label").Value(); err != nil || v != "1: 0" {
		t.Fatalf("label = %v, %v, want 1: 0", v, err)
	}
	b.Release()
	<-watch.Closed()
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %+v, want ended", end)
	}
	again := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	if got := factory.Load(); got != 3 {
		t.Fatalf("factory ran %d times, want 3", got)
	}
	x, _ := again.Member("label").Value()
	y, _ := c.Member("label").Value()
	if x != "1: 0" || y != "2: 0" {
		t.Fatalf("labels = %v, %v, want 1: 0 and 2: 0", x, y)
	}
}

func TestObjectIdentityKeepsATypeWithEmptyParamsAcrossReleases(t *testing.T) {
	var factory atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factory.Add(1)
			return serveStore(self), nil
		}, ServeOptions{})
	}, loadedOptions{})
	first := mustGet(t, host, objStore, jsonObject())
	_, err := first.Call("add", jsonObject("text", "milk"), background)
	must(t, err)
	first.Release()
	second := mustGet(t, host, objStore, jsonObject())
	if v, _ := second.Member("count").Value(); v != 1.0 || factory.Load() != 1 {
		t.Fatalf("count %v, factory %d, want 1 and 1", v, factory.Load())
	}
}

func TestObjectIdentityDropsAnEndedObjectsDocumentsAndDetachesItsDerivesFromOtherObjectsMembers(t *testing.T) {
	meter := nexttest.Metered(t)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) { return serveStore(self), nil }, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			store := self.Get(objStore, jsonObject())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": self.Derive([]any{store.Member("count")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%v: %v", params.Value("id"), values[0]), nil
				})},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Telemetry: meter.Telemetry})
	store := mustGet(t, host, objStore, jsonObject())
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	watch, err := page.Member("label").Watch(background)
	must(t, err)
	page.Release()
	<-watch.Closed()
	before := meter.Counts()["chord.derive.computes"]
	_, err = store.Call("add", jsonObject("text", "x"), background)
	must(t, err)
	turn(host)
	if after := meter.Counts()["chord.derive.computes"]; after != before {
		t.Fatalf("derive computes %v -> %v, want no more", before, after)
	}
}

// --- retries ----------------------------------------------------------------------------------------------------------

func TestObjectRetriesRetryATransientFailureAfter1sDoublingUpTo60sAndStartOverAfterASuccess(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(attempt int) bool { return attempt <= 8 || attempt == 10 }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	ready := 1_000_000 * time.Millisecond
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(objSvc, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ready})
		must(t, err)
		got <- ref
	}()
	until(t, "the first attempt", func() bool { return len(flaky.all()) == 1 })
	advanceHost(loop, host, 183_000)
	receive(t, got).Release()
	want := []int64{0, 1000, 3000, 7000, 15000, 31000, 63000, 123000, 183000}
	if !reflect.DeepEqual(flaky.all(), want) {
		t.Fatalf("attempts at %v, want %v", flaky.all(), want)
	}
	// The success deleted the backoff: a new object tries at once and waits 1 s after its failure.
	again := make(chan *Reference, 1)
	go func() { again <- mustGet(t, host, objSvc, jsonObject("id", 1.0)) }()
	until(t, "the next attempt", func() bool { return len(flaky.all()) == 10 })
	advanceHost(loop, host, 1000)
	ref := receive(t, again)
	if v, _ := ref.Member("n").Value(); v != 11.0 {
		t.Fatalf("n = %v, want 11", v)
	}
	if got := flaky.all()[9:]; !reflect.DeepEqual(got, []int64{183000, 184000}) {
		t.Fatalf("last attempts at %v, want [183000 184000]", got)
	}
}

func TestObjectRetriesShareOneAttemptAmongConcurrentGets(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(int) bool { return false }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	var values []any
	for range 3 {
		v, _ := mustGet(t, host, objSvc, jsonObject("id", 1.0)).Member("n").Value()
		values = append(values, v)
	}
	if !reflect.DeepEqual(flaky.all(), []int64{0}) || !reflect.DeepEqual(values, []any{1.0, 1.0, 1.0}) {
		t.Fatalf("attempts %v, values %v, want [0] and [1 1 1]", flaky.all(), values)
	}
}

func TestObjectRetriesNeverRetryABrokenFailureANewObjectAfterItsEndTriesAtOnce(t *testing.T) {
	loop := newScopeLoop()
	var bug atomic.Int32
	failing := func(*delta.JsonObject, Self) (contracts.Implementation, error) {
		bug.Add(1)
		return contracts.Implementation{}, errors.New("bug")
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objSvc, failing, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(objCore, failing, ServeOptions{})
	}, loadedOptions{Loop: loop})
	_, err := host.Get(objSvc, jsonObject("id", 1.0), background, GetOptions{})
	wantChordError(t, err, data.CodeUnavailable, "", "app.svc(1): bug")
	advanceHost(loop, host, 120_000)
	if got := bug.Load(); got != 2 {
		// The core's own activation at load is the second.
		t.Fatalf("factory calls = %d, want 2", got)
	}
	_, err = host.Get(objCore, jsonObject(), background, GetOptions{})
	wantChordError(t, err, data.CodeUnavailable, "", "app.core: bug")
	if _, err = host.Get(objSvc, jsonObject("id", 1.0), background, GetOptions{}); err == nil || err.Error() != "app.svc(1): bug" {
		t.Fatalf("third get = %v", err)
	}
	if got := bug.Load(); got != 4 {
		t.Fatalf("factory calls = %d, want 4", got)
	}
}

func TestObjectRetriesEndAFailedObjectWithItsLastReferenceItsBackoffEntryExpiresOnItsOwn(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(int) bool { return true }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	get := func(ms time.Duration) chan error {
		out := make(chan error, 1)
		go func() {
			_, err := host.Get(objSvc, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ms})
			out <- err
		}()
		return out
	}
	first := get(500 * time.Millisecond)
	until(t, "the first attempt", func() bool { return len(flaky.all()) == 1 })
	advanceHost(loop, host, 500)
	wantChordError(t, receive(t, first), data.CodeUnavailable, "", "app.svc(1): offline")
	advanceHost(loop, host, 4500)
	if got, timers := flaky.all(), loop.TimerCount(); !reflect.DeepEqual(got, []int64{0}) || timers != 0 {
		t.Fatalf("attempts %v, timers %d, want [0] and 0", got, timers)
	}
	// The entry expired: a new object tries at once and its window starts over at 1 s.
	second := get(1500 * time.Millisecond)
	until(t, "the second attempt", func() bool { return len(flaky.all()) == 2 })
	advanceHost(loop, host, 1500)
	receive(t, second)
	if got := flaky.all(); !reflect.DeepEqual(got, []int64{0, 5000, 6000}) {
		t.Fatalf("attempts at %v, want [0 5000 6000]", got)
	}
}

func TestObjectRetriesMakeAGetRightAfterAFailureWaitForTheScheduledRetry(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(attempt int) bool { return attempt == 1 }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	ms := 500 * time.Millisecond
	first := make(chan error, 1)
	go func() {
		_, err := host.Get(objSvc, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ms})
		first <- err
	}()
	until(t, "the first attempt", func() bool { return len(flaky.all()) == 1 })
	advanceHost(loop, host, 500)
	receive(t, first)
	second := make(chan *Reference, 1)
	go func() { second <- mustGet(t, host, objSvc, jsonObject("id", 1.0)) }()
	advanceHost(loop, host, 499)
	if got := flaky.all(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("attempts at %v, want [0]", got)
	}
	advanceHost(loop, host, 1)
	ref := receive(t, second)
	if v, _ := ref.Member("n").Value(); v != 2.0 || !reflect.DeepEqual(flaky.all(), []int64{0, 1000}) {
		t.Fatalf("n %v, attempts %v, want 2 and [0 1000]", v, flaky.all())
	}
}

func TestObjectRetriesStartANewInstanceOnlyAfterThePreviousOneClosed(t *testing.T) {
	closing := newSignal()
	var factory atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			// The instance's native closes only when the test lets it.
			self.Open(func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { closing.wait(t); return nil })
			n := factory.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(n))}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	mustGet(t, host, objSvc, jsonObject("id", 1.0)).Release()
	got := make(chan *Reference, 1)
	go func() { got <- mustGet(t, host, objSvc, jsonObject("id", 1.0)) }()
	deliveredOn(host)
	host.(*chordHost).loop().Run(func() {})
	if n := factory.Load(); n != 1 {
		t.Fatalf("factory ran %d times while the previous instance was still closing, want 1", n)
	}
	closing.fire()
	ref := receive(t, got)
	if v, _ := ref.Member("n").Value(); v != 2.0 {
		t.Fatalf("n = %v, want 2", v)
	}
}

// --- parked on dependencies -------------------------------------------------------------------------------------------

func TestObjectParkedParksADependentOfAFailedDependencyFailedWaitingNamingItWithNoTimerItGoesOnWhenTheDependencyIsLive(t *testing.T) {
	loop := newScopeLoop()
	var core flakyFactory
	var viewerTimes flakyFactory
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objCore, core.factory(loop, func(attempt int) bool { return attempt == 1 }, nil), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(objView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			viewerTimes.mu.Lock()
			viewerTimes.times = append(viewerTimes.times, loop.Now())
			viewerTimes.mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(objCore, jsonObject()).Member("n")),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	advanceHost(loop, host, 500)
	held := mustHold(t, host, objView, jsonObject("id", 1.0))
	got := make(chan *Reference, 1)
	go func() { got <- mustGet(t, host, objView, jsonObject("id", 1.0)) }()
	untilKind(t, held, RefStateFailed)
	state := stateOfRef(held)
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "waiting for app.core: app.core: offline" || state.RetryAt != nil {
		t.Fatalf("state = %+v (cause %+v), want failed waiting for app.core", state, state.Cause)
	}
	// A parked attempt is no failure of its own: nothing is reported for the view.
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.core: transient"}) {
		t.Fatalf("failures = %v", got)
	}
	// The core's own backoff retries it at 1 s; the view goes on then, its factory not run again.
	advanceHost(loop, host, 500)
	ref := receive(t, got)
	if v, _ := ref.Member("n").Value(); v != 2.0 {
		t.Fatalf("n = %v, want 2", v)
	}
	advanceHost(loop, host, 120_000)
	if got := viewerTimes.all(); !reflect.DeepEqual(got, []int64{500}) {
		t.Fatalf("view factory at %v, want [500]", got)
	}
	held.Release()
}

func TestObjectParkedKeepsItsReferencesWhileParkedAGetterRejectsWithTheWaitingCauseAtItsDeadlineReleasingEndsTheWait(t *testing.T) {
	loop := newScopeLoop()
	var svc, viewer flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objSvc, svc.factory(loop, func(int) bool { return true }, nil), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(objView, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			viewer.mu.Lock()
			viewer.times = append(viewer.times, loop.Now())
			viewer.mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(objSvc, jsonObject("id", params.Value("id"))).Member("n")),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	ms := 1500 * time.Millisecond
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(objView, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ms})
		got <- err
	}()
	until(t, "the dependency's first attempt", func() bool { return len(svc.all()) == 1 })
	advanceHost(loop, host, 1500)
	wantChordError(t, receive(t, got), data.CodeUnavailable, "", "waiting for app.svc(1): app.svc(1): offline")
	advanceHost(loop, host, 10_000)
	// Held by the parked view, the dependency was retried at 1 s; released with it at 1.5 s, it is not any more.
	if !reflect.DeepEqual(svc.all(), []int64{0, 1000}) || !reflect.DeepEqual(viewer.all(), []int64{0}) {
		t.Fatalf("svc %v, viewer %v, want [0 1000] and [0]", svc.all(), viewer.all())
	}
}

func TestObjectParkedShowsEveryChangeOfADependencyLoadingFailedANewCauseLoadingAgainLive(t *testing.T) {
	loop := newScopeLoop()
	var cores atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objCore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempt := cores.Add(1)
			self.Open(func(context.Context) (any, error) {
				if attempt < 3 {
					return nil, fmt.Errorf("offline %d", attempt)
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return nOnly(float64(attempt)), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(objView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(objCore, jsonObject()).Member("n")),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	held := mustHold(t, host, objView, jsonObject("id", 1.0))
	failed := untilKind(t, held, RefStateFailed)
	if failed.Cause.Error.Message != "waiting for app.core: app.core: offline 1" {
		t.Fatalf("cause = %q", failed.Cause.Error.Message)
	}
	watch, err := held.State().Watch(background)
	must(t, err)
	states := statesOf(t, watch)
	advanceHost(loop, host, 10_000)
	until(t, "the view to be live", func() bool { return stateOfRef(held).Kind == RefStateLive })
	turn(host)
	want := []string{"loading", "failed: waiting for app.core: app.core: offline 2", "loading", "live"}
	if got := states.All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	held.Release()
}

func TestObjectParkedPassesARetryOnToItsBrokenDependencyOnceThroughADiamond(t *testing.T) {
	top := mustContract(contracts.Local("app.top", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	left := mustContract(contracts.Local("app.left", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	loop := newScopeLoop()
	var cores atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objCore, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			if n := cores.Add(1); n == 1 {
				return contracts.Implementation{}, errors.New("bug")
			}
			return nOnly(float64(cores.Load())), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(left, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(objCore, jsonObject()).Member("n")),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(top, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			a := self.Get(left, jsonObject("id", 1.0)).Member("n")
			b := self.Get(left, jsonObject("id", 2.0)).Member("n")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{a, b}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) + values[1].(float64), nil }),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	held := mustHold(t, host, top, jsonObject())
	failed := untilKind(t, held, RefStateFailed)
	if failed.Cause.Kind != data.FailureWaiting || failed.Cause.Error.Message != "waiting for app.left(1): app.core: bug" {
		t.Fatalf("state = %+v", failed.Cause)
	}
	advanceHost(loop, host, 60_000)
	// A broken dependency is not retried by any timer.
	if got := cores.Load(); got != 1 {
		t.Fatalf("cores = %d, want 1", got)
	}
	held.Retry()
	untilKind(t, held, RefStateLive)
	if v, _ := held.Member("n").Value(); cores.Load() != 2 || v != 4.0 {
		t.Fatalf("cores %d, n %v, want 2 and 4", cores.Load(), v)
	}
	held.Release()
}

func TestObjectParkedFailsEveryObjectOfACycleBrokenNoneIsRetried(t *testing.T) {
	ping := mustContract(contracts.Local("app.ping", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	pong := mustContract(contracts.Local("app.pong", contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	loop := newScopeLoop()
	var pings, pongs atomic.Int32
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(ping, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			pings.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(pong, jsonObject()).Member("n"))}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(pong, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			pongs.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(ping, jsonObject()).Member("n"))}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	got := failures(errs.All())
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"app.ping: broken", "app.pong: broken"}) {
		t.Fatalf("failures = %v", got)
	}
	advanceHost(loop, host, 120_000)
	if pings.Load() != 1 || pongs.Load() != 1 {
		t.Fatalf("factories = %d, %d, want 1, 1", pings.Load(), pongs.Load())
	}
	_, err := host.Get(pong, jsonObject(), background, GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonCycle, "dependency cycle: app.ping → app.pong → app.ping")
}

func TestObjectCycleChecksStayLinearOnSharedDependenciesALayeredGraphEveryObjectDependingOnTheWholeNextLayer(t *testing.T) {
	layer := mustContract(contracts.Local("app.layer", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("layer", contracts.Int()), contracts.Prop("index", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("depth", contracts.Int())},
	}))
	const layers = 28
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(layer, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			l := params.Value("layer").(float64)
			if l == layers {
				return nOnlyDepth(0.0), nil
			}
			var next []any
			for index := range 2 {
				next = append(next, self.Get(layer, jsonObject("layer", l+1, "index", float64(index))).Member("depth"))
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"depth": self.Derive(next, func(values []any) (delta.JsonValue, error) { return values[0].(float64) + 1, nil }),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	meter := nexttest.Metered(t)
	top, err := host.Get(layer, jsonObject("layer", 0.0, "index", 0.0), meter.Context, GetOptions{})
	must(t, err)
	if v, _ := top.Member("depth").Value(); v != float64(layers) {
		t.Fatalf("depth = %v, want %d", v, layers)
	}
	// 55 objects, 2 waits each: a search per wait visits at most every object once (an exponential one 2^28 chains).
	if visits := meter.Counts()["chord.cycle.visits"]; visits > 2*55*55 {
		t.Fatalf("cycle visits = %v, want at most %d", visits, 2*55*55)
	}
}

func nOnlyDepth(n delta.JsonValue) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"depth": constant(n)}}}
}

// --- GC ---------------------------------------------------------------------------------------------------------------

func TestObjectGCCollectsAnEndedObjectWithItsDocumentsDerivesAndWatches(t *testing.T) {
	var mu sync.Mutex
	var attempts []weak.Pointer[attempt]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) { return serveStore(self), nil }, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			list := self.State(attemptListDoc)
			label := self.Derive([]any{list, self.Get(objStore, jsonObject()).Member("count")}, func(values []any) (delta.JsonValue, error) {
				return fmt.Sprintf("%d/%v", len(values[0].(*delta.JsonObject).Value("items").([]any)), values[1]), nil
			})
			mu.Lock()
			attempts = append(attempts, weak.Make(self.(*attempt)))
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": label},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	store := mustGet(t, host, objStore, jsonObject())
	func() {
		page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
		_, err := page.Member("label").Watch(background)
		must(t, err)
		page.Release()
	}()
	_, err := store.Call("add", jsonObject("text", "x"), background)
	must(t, err)
	turn(host)
	mu.Lock()
	held := slices.Clone(attempts)
	mu.Unlock()
	nexttest.ExpectCollected(t, held...)
}

func TestObjectGCOfFailedObjectsCollectsOneAfterItsLastReferenceWhileItsBackoffEntryExistsTheEntryExpiresOnItsOwn(t *testing.T) {
	loop := newScopeLoop()
	var mu sync.Mutex
	var attempts []weak.Pointer[attempt]
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return make([]byte, 16), nil }, func(any, NativeCloseReason, context.Context) error { return nil })
			self.Open(func(context.Context) (any, error) { return nil, errors.New("offline") }, func(any, NativeCloseReason, context.Context) error { return nil })
			mu.Lock()
			attempts = append(attempts, weak.Make(self.(*attempt)))
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	ms := 100 * time.Millisecond
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(attemptPage, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ms})
		got <- err
	}()
	until(t, "the attempt", func() bool { mu.Lock(); defer mu.Unlock(); return len(attempts) == 1 })
	advanceHost(loop, host, 100)
	receive(t, got)
	if got := loop.TimerCount(); got != 1 {
		t.Fatalf("timers = %d, want 1", got)
	}
	_ = errs
	mu.Lock()
	held := slices.Clone(attempts)
	mu.Unlock()
	nexttest.ExpectCollected(t, held...)
	advanceHost(loop, host, 900)
	if got := loop.TimerCount(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
}

func TestObjectGCOfAParkedChainCollectsAChainParkedOnATypeNotServedReleasedFromItsTopObjectsAttemptsAndIndexEntries(t *testing.T) {
	top := mustContract(contracts.Local("app.top", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	mid := mustContract(contracts.Local("app.mid", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	var mu sync.Mutex
	var attempts []weak.Pointer[attempt]
	keep := func(self Self) {
		mu.Lock()
		attempts = append(attempts, weak.Make(self.(*attempt)))
		mu.Unlock()
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(mid, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			keep(self)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(objCore, jsonObject()).Member("n")),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(top, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			keep(self)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(mid, jsonObject("id", params.Value("id"))).Member("n")),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	held := mustHold(t, host, top, jsonObject("id", 1.0))
	failed := untilKind(t, held, RefStateFailed)
	if failed.Cause.Kind != data.FailureWaiting || failed.Cause.Error.Message != "waiting for app.mid(1): app.core is not served here" {
		t.Fatalf("state = %+v", failed.Cause)
	}
	held.Release()
	turn(host)
	mu.Lock()
	refs := slices.Clone(attempts)
	mu.Unlock()
	if len(refs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(refs))
	}
	nexttest.ExpectCollected(t, refs...)
}

// --- ref.retry() ----------------------------------------------------------------------------------------------------------

func TestObjectRefRetrySkipsATransientFailuresBackoffWindowAnAttemptNow(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(attempt int) bool { return attempt == 1 }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	svc := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	untilKind(t, svc, RefStateFailed)
	loop.fakeLoop.Advance(200)
	svc.Retry()
	untilKind(t, svc, RefStateLive)
	v, _ := svc.Member("n").Value()
	if !reflect.DeepEqual(flaky.all(), []int64{0, 200}) || v != 2.0 {
		t.Fatalf("attempts %v, n %v, want [0 200] and 2", flaky.all(), v)
	}
}

func TestObjectRefRetryStartsAnObjectWaitingForItsIdentitysBackoffWindowAtOnce(t *testing.T) {
	loop := newScopeLoop()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(attempt int) bool { return attempt == 1 }, nil), ServeOptions{})
	}, loadedOptions{Loop: loop})
	first := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	untilKind(t, first, RefStateFailed)
	// The failed object ends with its reference; its identity's window (1 s) still runs.
	first.Release()
	loop.fakeLoop.Advance(100)
	second := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	turn(host)
	if got := stateOfRef(second).Kind; got != RefStateLoading || !reflect.DeepEqual(flaky.all(), []int64{0}) {
		t.Fatalf("state %s, attempts %v, want loading and [0]", got, flaky.all())
	}
	second.Retry()
	untilKind(t, second, RefStateLive)
	if !reflect.DeepEqual(flaky.all(), []int64{0, 100}) {
		t.Fatalf("attempts %v, want [0 100]", flaky.all())
	}
}

func TestObjectRefRetryIsIgnoredWhileAnAttemptRunsAndOnceLiveRace11(t *testing.T) {
	loop := newScopeLoop()
	gate := newSignal()
	var flaky flakyFactory
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, flaky.factory(loop, func(attempt int) bool { return attempt == 1 }, gate), ServeOptions{})
	}, loadedOptions{Loop: loop})
	svc := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	untilKind(t, svc, RefStateFailed)
	svc.Retry()
	svc.Retry()
	turn(host)
	if !reflect.DeepEqual(flaky.all(), []int64{0, 0}) {
		t.Fatalf("attempts %v, want [0 0]", flaky.all())
	}
	gate.fire()
	untilKind(t, svc, RefStateLive)
	svc.Retry()
	advanceHost(loop, host, 5_000)
	if got := stateOfRef(svc); !reflect.DeepEqual(flaky.all(), []int64{0, 0}) || got.Kind != RefStateLive {
		t.Fatalf("attempts %v, state %+v, want [0 0] and live", flaky.all(), got)
	}
}

func TestObjectRefRetryRenewsABrokenObjectForEveryReferenceRace13(t *testing.T) {
	loop := newScopeLoop()
	var fixed atomic.Bool
	var factory atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factory.Add(1)
			self.Open(func(context.Context) (any, error) {
				if !fixed.Load() {
					return nil, data.Broken(errors.New("no configuration"))
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return nOnly(1.0), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	one := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	two := mustHold(t, host, objSvc, jsonObject("id", 1.0))
	untilKind(t, one, RefStateFailed)
	advanceHost(loop, host, 120_000)
	if a, b, n := stateOfRef(one).Kind, stateOfRef(two).Kind, factory.Load(); a != RefStateFailed || b != RefStateFailed || n != 1 {
		t.Fatalf("states %s, %s, factory %d, want failed, failed, 1", a, b, n)
	}
	fixed.Store(true)
	one.Retry()
	if got := stateOfRef(two); got.Kind != RefStateLoading {
		t.Fatalf("second state = %+v, want loading", got)
	}
	untilKind(t, one, RefStateLive)
	untilKind(t, two, RefStateLive)
}

// --- linger ---------------------------------------------------------------------------------------------------------------

var objTally = mustContract(contracts.Local("app.tally", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("bump", contracts.NewMethod())},
}))

var objCounterDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("n", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// tallies serves `Tally` with linger: a document, a derive that fails at 2, a native whose closes closes records.
func tallies(t *testing.T, loop *scopeLoop, linger time.Duration, open func(ctx context.Context) error) (Host, *atomic.Int32, *strs) {
	t.Helper()
	var factory atomic.Int32
	var closes strs
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objTally, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factory.Add(1)
			id := params.Value("id")
			doc := stateOf(self, objCounterDoc)
			self.Open(func(ctx context.Context) (any, error) {
				if open != nil {
					return nil, open(ctx)
				}
				return nil, nil
			}, func(_ any, reason NativeCloseReason, _ context.Context) error {
				closes.add(fmt.Sprintf("%v %s", id, reason.Kind))
				return nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					n := values[0].(*delta.JsonObject).Value("n")
					if n == 2.0 {
						return nil, errors.New("two")
					}
					return n, nil
				})},
				Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, bumpDoc(doc, ctx)
				}},
			}}, nil
		}, ServeOptions{Linger: &linger})
	}, loadedOptions{Loop: loop})
	return host, &factory, &closes
}

func TestObjectLingerKeepsALiveObjectLiveForLingerAfterItsLastReleaseAGetWithinItReusesItWithItsDocuments(t *testing.T) {
	loop := newScopeLoop()
	host, factory, closes := tallies(t, loop, 500*time.Millisecond, nil)
	first := mustGet(t, host, objTally, jsonObject("id", 1.0))
	_, err := refCall(first, "bump", nil)
	must(t, err)
	first.Release()
	advanceHost(loop, host, 499)
	second := mustGet(t, host, objTally, jsonObject("id", 1.0))
	if v, _ := second.Member("n").Value(); v != 1.0 || factory.Load() != 1 || closes.len() != 0 {
		t.Fatalf("n %v, factory %d, closes %v, want 1, 1, none", v, factory.Load(), closes.all())
	}
	second.Release()
	advanceHost(loop, host, 500)
	until(t, "the close", func() bool { return closes.len() == 1 })
	if got := closes.all(); !reflect.DeepEqual(got, []string{"1 end"}) {
		t.Fatalf("closes = %v", got)
	}
	again := mustGet(t, host, objTally, jsonObject("id", 1.0))
	if v, _ := again.Member("n").Value(); v != 0.0 || factory.Load() != 2 {
		t.Fatalf("n %v, factory %d, want 0 and 2", v, factory.Load())
	}
}

func TestObjectLingerAReuseCancelsTheExpiryTheHolderCountDecidesALaterReleaseStartsANewWindowRace8(t *testing.T) {
	loop := newScopeLoop()
	host, _, closes := tallies(t, loop, 500*time.Millisecond, nil)
	mustGet(t, host, objTally, jsonObject("id", 1.0)).Release()
	advanceHost(loop, host, 499)
	held := mustGet(t, host, objTally, jsonObject("id", 1.0))
	advanceHost(loop, host, 5_000)
	held.Release()
	advanceHost(loop, host, 499)
	if closes.len() != 0 {
		t.Fatalf("closes = %v, want none", closes.all())
	}
	advanceHost(loop, host, 1)
	until(t, "the close", func() bool { return closes.len() == 1 })
	if got := closes.all(); !reflect.DeepEqual(got, []string{"1 end"}) {
		t.Fatalf("closes = %v", got)
	}
}

func TestObjectLingerOnlyALiveObjectLingersAnActivatingOrFailedOneEndsWithItsLastReferenceRace9(t *testing.T) {
	loop := newScopeLoop()
	gate := newSignal()
	host, _, closes := tallies(t, loop, 500*time.Millisecond, func(context.Context) error { <-gate; return nil })
	opening := mustHold(t, host, objTally, jsonObject("id", 1.0))
	turn(host)
	opening.Release()
	gate.fire()
	// Its open's late value was closed at once.
	until(t, "the late close", func() bool { return closes.len() == 1 })
	if got := closes.all(); !reflect.DeepEqual(got, []string{"1 end"}) {
		t.Fatalf("closes = %v", got)
	}
	failingLoop := newScopeLoop()
	failingHost, factory, _ := tallies(t, failingLoop, 500*time.Millisecond, func(context.Context) error {
		return data.Broken(errors.New("no configuration"))
	})
	failed := mustHold(t, failingHost, objTally, jsonObject("id", 1.0))
	untilKind(t, failed, RefStateFailed)
	failed.Release()
	again := mustHold(t, failingHost, objTally, jsonObject("id", 1.0))
	untilKind(t, again, RefStateFailed)
	if got := factory.Load(); got != 2 {
		t.Fatalf("factory = %d, want 2", got)
	}
}

func TestObjectLingerAFailedStateMemberDoesNotEndALingeringObject(t *testing.T) {
	loop := newScopeLoop()
	host, factory, closes := tallies(t, loop, 500*time.Millisecond, nil)
	tally := mustGet(t, host, objTally, jsonObject("id", 1.0))
	for range 2 {
		_, err := refCall(tally, "bump", nil)
		must(t, err)
	}
	if _, err := tally.Member("n").Value(); err == nil || err.Error() != "two" {
		t.Fatalf("n = %v, want two", err)
	}
	tally.Release()
	advanceHost(loop, host, 400)
	mustGet(t, host, objTally, jsonObject("id", 1.0))
	if factory.Load() != 1 || closes.len() != 0 {
		t.Fatalf("factory %d, closes %v, want 1 and none", factory.Load(), closes.all())
	}
}

func TestObjectLingerHostCloseEndsALingeringObjectAtOnce(t *testing.T) {
	loop := newScopeLoop()
	host, _, closes := tallies(t, loop, time.Minute, nil)
	mustGet(t, host, objTally, jsonObject("id", 1.0)).Release()
	closeHosts(t, host)
	if got, timers := closes.all(), loop.TimerCount(); !reflect.DeepEqual(got, []string{"1 close"}) || timers != 0 {
		t.Fatalf("closes %v, timers %d, want [1 close] and 0", got, timers)
	}
}

func TestObjectLingerALingeringObjectIsCollectedAfterItsExpiry(t *testing.T) {
	loop := newScopeLoop()
	var mu sync.Mutex
	var refs []weak.Pointer[attempt]
	linger := 100 * time.Millisecond
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(objSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			refs = append(refs, weak.Make(self.(*attempt)))
			mu.Unlock()
			return nOnly(params.Value("id")), nil
		}, ServeOptions{Linger: &linger})
	}, loadedOptions{Loop: loop})
	mustGet(t, host, objSvc, jsonObject("id", 1.0)).Release()
	advanceHost(loop, host, 100)
	mu.Lock()
	held := slices.Clone(refs)
	mu.Unlock()
	nexttest.ExpectCollected(t, held...)
}

// --- a failed activation's cleanup, a live object failing ------------------------------------------------------------------

func TestObjectAFailedActivationsCleanupClosesTheNativesThatOpenedBeforeItReleasesItsReferencesACloseMayUseThem(t *testing.T) {
	dependent := mustContract(contracts.Local("app.dependent", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	var seen strs
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objCore, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return nOnly(5.0), nil }, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(dependent, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			core := self.Get(objCore, jsonObject())
			self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error {
				v, err := core.Member("n").Value()
				if err != nil {
					seen.add(err.Error())
				} else {
					seen.add(fmt.Sprint(v))
				}
				return nil
			})
			self.Open(func(context.Context) (any, error) { return nil, data.Broken(errors.New("open failed")) }, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(core.Member("n"))}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(dependent, jsonObject("id", 1.0), background, GetOptions{})
	if err == nil || !hasText(err.Error(), "open failed") {
		t.Fatalf("get = %v, want open failed", err)
	}
	until(t, "the close", func() bool { return seen.len() == 1 })
	var failed *HostError
	for _, report := range errs.All() {
		if report.Kind == ErrActivationFailed {
			failed = &report
			break
		}
	}
	if failed == nil || failed.Failure.Kind != data.FailureBroken || !reflect.DeepEqual(seen.all(), []string{"5"}) {
		t.Fatalf("report %+v, seen %v, want broken and [5]", failed, seen.all())
	}
	closeHosts(t, host)
}

func TestObjectALiveObjectFailingFailsBrokenCallsRejectWithTheCauseOneObjectFailedReportNativesCloseThenReferencesGo(t *testing.T) {
	var events strs
	var list *documents.Singleton
	run := restartable(t, func(_ int, self Self) {
		list = self.State(attemptListDoc).(*documents.Singleton)
		core := self.Get(objCore, jsonObject())
		self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error {
			v, _ := core.Member("n").Value()
			events.add(fmt.Sprintf("close sees core %v", v))
			return nil
		})
	}, loadedOptions{Serve: func(h FacetHost) {
		must(t, h.Serve(objCore, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return nOnly(1.0), nil }, ServeOptions{}))
	}})
	watch, err := run.Reference.State().Watch(background)
	must(t, err)
	states := statesOf(t, watch)
	run.Fail()
	_, err = refCall(run.Reference, "touch", nil)
	wantChordError(t, err, data.CodeUnavailable, "", "boom")
	all := run.Errors.All()
	if len(all) != 1 || all[0].Kind != ErrObjectFailed || all[0].Object != "app.restartable(1)" {
		t.Fatalf("errors = %+v, want one object_failed of app.restartable(1)", all)
	}
	// A failure of the sealed attempt is ignored.
	run.Fail()
	until(t, "the close", func() bool { return events.len() == 1 })
	turn(run.Host)
	if got := events.all(); !reflect.DeepEqual(got, []string{"close sees core 1"}) || run.Errors.Len() != 1 {
		t.Fatalf("events %v, errors %d, want one close and one report", got, run.Errors.Len())
	}
	var value *delta.JsonObject
	run.Host.(*chordHost).enter(func() { value, _ = list.Value() })
	if !reflect.DeepEqual(value, jsonObject("items", []any{})) {
		t.Fatalf("list = %v, want {items: []}", value)
	}
	if got := states.All(); !reflect.DeepEqual(got, []string{"failed"}) {
		t.Fatalf("states = %v, want [failed]", got)
	}
}

func TestObjectALiveObjectFailingIsRecoveredByRefRetryANewAttemptTheDocumentsAsTheyWere(t *testing.T) {
	var mu sync.Mutex
	var lists []*documents.Singleton
	run := restartable(t, func(_ int, self Self) {
		mu.Lock()
		lists = append(lists, self.State(attemptListDoc).(*documents.Singleton))
		mu.Unlock()
	}, loadedOptions{})
	mu.Lock()
	first := lists[0]
	mu.Unlock()
	_, err := first.Change(func(draft *delta.Object) (any, error) {
		_, err := draft.Array("items").Push("a")
		return nil, err
	}, background)
	must(t, err)
	run.Restart()
	mu.Lock()
	second := lists[1]
	mu.Unlock()
	var value *delta.JsonObject
	run.Host.(*chordHost).enter(func() { value, _ = second.Value() })
	if !reflect.DeepEqual(value, jsonObject("items", []any{"a"})) {
		t.Fatalf("list = %v, want {items: [a]}", value)
	}
}
