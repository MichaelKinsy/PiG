package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Test helpers of the host core's tests (host.test.ts and the files of the host lane that need a whole host).

// closeHosts closes hosts at the end of a test, in the order given; a failing close fails the test.
func closeHosts(t testing.TB, hosts ...Host) {
	t.Helper()
	for _, host := range hosts {
		if err := host.Close(background); err != nil {
			t.Errorf("close: %v", err)
		}
	}
}

// turn is `await flushed()` on a host: its loop's queued microtasks ran, and so did every delivery they scheduled.
func turn(host Host) {
	h := host.(*chordHost)
	h.enter(func() {})
	h.scheduler.Wait()
	h.enter(func() {})
}

// loopTurn is a barrier of the host's loop: what it queued ran, without waiting for deliveries (a watcher may be busy).
func loopTurn(host Host) {
	h := host.(*chordHost)
	h.enter(func() {})
	h.enter(func() {})
}

// sourceValue is the value of source read in a section of host's loop (`source.value`).
func sourceValue(t testing.TB, host Host, source state.DeriveSource) delta.JsonValue {
	t.Helper()
	var value delta.JsonValue
	var err error
	host.(*chordHost).enter(func() { value, err = source.Value() })
	must(t, err)
	return value
}

// startCalls starts n calls of method on ref in one synchronous section, as `ref.method(context)` n times in one turn
// does; waiting for them is awaitCalls.
func startCalls(t testing.TB, ref *Reference, method string, n int) []*parkedCallResult {
	t.Helper()
	b := ref.binding
	results := make([]*parkedCallResult, 0, n)
	var failure error
	b.loop.Run(func() {
		for range n {
			result, err := b.call(method, nil, background)
			if err != nil {
				failure = err
				return
			}
			results = append(results, result)
		}
	})
	must(t, failure)
	return results
}

// awaitCalls waits for every call to settle and fails the test for one that was rejected.
func awaitCalls(t testing.TB, results []*parkedCallResult) {
	t.Helper()
	for _, result := range results {
		_, err := result.wait(background)
		must(t, err)
	}
}

// countingSectionLoop is a scopeLoop that counts the microtasks queued (`vi.spyOn(globalThis, "queueMicrotask")`).
type countingSectionLoop struct {
	*scopeLoop
	mu     sync.Mutex
	queued int
}

var _ sectionLoop = (*countingSectionLoop)(nil)

// Queue counts the microtask and queues it.
func (l *countingSectionLoop) Queue(job func()) {
	l.mu.Lock()
	l.queued++
	l.mu.Unlock()
	l.scopeLoop.Queue(job)
}

// count is the number of microtasks queued so far.
func (l *countingSectionLoop) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queued
}

// selfOpen is `self.open(open, close)` of a native whose value nothing reads: open runs during activation, closer when
// the attempt ends (its reason); both may block, off the loop.
func selfOpen(self Self, open func(ctx context.Context) error, closer func(reason NativeCloseReason)) {
	self.Open(func(ctx context.Context) (any, error) { return nil, open(ctx) },
		func(_ any, reason NativeCloseReason, _ context.Context) error {
			closer(reason)
			return nil
		})
}

// constant is Pi's `constant(value)`: a Chord watchable that never changes.
func constant(value delta.JsonValue) chord.Watchable[delta.JsonValue] {
	return Expose(state.NewConstant(value))
}

// pi: packages/chord/src/next/host/host.ts
// --- host.test.ts ---------------------------------------------------------------------------------------------------

var (
	hostInfo    = mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))
	hostCounter = mustContract(declareLocal("app.counter", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int()), contracts.Mem("bump", contracts.NewMethod())},
	}))
	hostPage = mustContract(declareLocal("app.page", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())},
	}))
	hostSvc = mustContract(declareLocal("app.svc", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	hostCore = mustContract(declareLocal("app.core", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
)

// noopOnError is `{ onError: () => {} }`.
func noopOnError(HostError) {}

// stateOfMembers is a state-member map from name and watchable pairs.
func stateOfMembers(pairs ...any) map[string]chord.Watchable[delta.JsonValue] {
	out := map[string]chord.Watchable[delta.JsonValue]{}
	for i := 0; i < len(pairs); i += 2 {
		out[pairs[i].(string)] = pairs[i+1].(chord.Watchable[delta.JsonValue])
	}
	return out
}

// impl is `{ name: constant(value), ... }` as an implementation.
func impl(pairs ...any) contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: stateOfMembers(pairs...)}}
}

// serveInfoFactory serves Info with a factory counting its runs: `() => ({ title: constant("t") })`.
func serveInfoFactory(h FacetHost, runs *atomic.Int32) error {
	return h.Serve(hostInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
		if runs != nil {
			runs.Add(1)
		}
		return impl("title", constant("t")), nil
	}, ServeOptions{})
}

// servePage serves Page: `({ id }) => ({ id: constant(id) })`.
func servePage(h FacetHost, factory func()) error {
	return h.Serve(hostPage, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
		if factory != nil {
			factory()
		}
		return impl("id", constant(params.Value("id"))), nil
	}, ServeOptions{})
}

// gatedSvc is `serveGated`: one native whose open waits for gate and whose close records its value and reason in closes.
// starts, when given, records the id of each open as it is called: Pi's `await flushed()` runs an activation as far as its
// open waiting on the gate, which a test here waits for by this (the activation goes on over tasks of the host).
func gatedSvc(h FacetHost, gate <-chan struct{}, closes *seenList[string], starts ...*seenList[string]) error {
	return h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		id := params.Value("id")
		self.Open(func(context.Context) (any, error) {
			for _, started := range starts {
				started.add(fmt.Sprint(id))
			}
			<-gate
			return fmt.Sprintf("svc %v", id), nil
		},
			func(value any, reason NativeCloseReason, _ context.Context) error {
				closes.add(fmt.Sprintf("%v %s", value, reason.Kind))
				return nil
			})
		return impl("n", constant(id)), nil
	}, ServeOptions{})
}

// memberOf is `ref.member.value`.
func memberOf(t testing.TB, ref *Reference, name string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(name).Value()
	must(t, err)
	return value
}

// wantChordError fails unless err is a ChordError of code, reason (empty: any) and message (empty: any).
func wantChordError(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason, message string) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != code || (reason != "" && chordErr.Reason != reason) || (message != "" && chordErr.Message != message) {
		t.Fatalf("err = %v, want {%s %s %q}", err, code, reason, message)
	}
}

// startLoad is `host.load(...)` unawaited: its result arrives on the channel.
func startLoad(host Host, facets ...HostFacet) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := host.Load(facets, background)
		done <- err
	}()
	return done
}

// notDone fails unless the load has not returned.
func notDone(t testing.TB, host Host, done <-chan error) {
	t.Helper()
	turn(host)
	select {
	case err := <-done:
		t.Fatalf("load returned early: %v", err)
	default:
	}
}

// Mirrors host.test.ts "load › runs every setup and activates the types with empty params before it resolves".
func TestHostLoadRunsEverySetupAndActivatesTheTypesWithEmptyParamsBeforeItResolves(t *testing.T) {
	var info, page atomic.Int32
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := serveInfoFactory(h, &info); err != nil {
			return err
		}
		return servePage(h, func() { page.Add(1) })
	})
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: facet}}, background))
	if info.Load() != 1 || page.Load() != 0 {
		t.Fatalf("factories: info %d, page %d; want 1 and 0", info.Load(), page.Load())
	}
	closeHosts(t, host)
}

// failedMessages is the plugin and error message of each plugin `load` or `reload` left out.
func failedMessages(failed []PluginFailure) [][2]string {
	out := make([][2]string, 0, len(failed))
	for _, each := range failed {
		out = append(out, [2]string{each.Plugin, each.Error.Error()})
	}
	return out
}

// Mirrors "load › leaves out and reports invalid and duplicate plugin names and double serves (the later); refuses a second load".
func TestHostLoadLeavesOutAndReportsInvalidAndDuplicatePluginNamesAndDoubleServesTheLaterRefusesASecondLoad(t *testing.T) {
	empty := DefineFacet(func(FacetHost, FacetSelf) error { return nil })
	info := DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })
	loadedFailed := func(facets []HostFacet) [][2]string {
		host := newHost(t, HostOptions{OnError: noopOnError})
		report, err := host.Load(facets, background)
		must(t, err)
		closeHosts(t, host)
		return failedMessages(report.Failed)
	}
	if got, want := loadedFailed([]HostFacet{{Name: "Bad Name", Facet: empty}}), [][2]string{{"Bad Name", `"Bad Name" is not a valid plugin name`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	twice := []HostFacet{{Name: "a", Facet: empty}, {Name: "a", Facet: empty}}
	if got, want := loadedFailed(twice), [][2]string{{"a", `plugin "a" is loaded twice`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	throwing := DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("first failed") })
	// The first one's setup failing does not make the second one's name free (Sol).
	failing := newHost(t, HostOptions{OnError: noopOnError})
	report, err := failing.Load([]HostFacet{{Name: "a", Facet: throwing}, {Name: "a", Facet: info}}, background)
	must(t, err)
	if got, want := failedMessages(report.Failed), [][2]string{{"a", `plugin "a": first failed`}, {"a", `plugin "a" is loaded twice`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	held, err := failing.Hold(background, hostInfo, jsonObject(), GetOptions{})
	must(t, err)
	if kind := refStateOf(held).Kind; kind != RefStateFailed {
		t.Fatalf("hold state = %v, want failed", kind)
	}
	closeHosts(t, failing)
	served := []HostFacet{{Name: "a", Facet: info}, {Name: "b", Facet: info}}
	if got, want := loadedFailed(served), [][2]string{{"b", `plugin "b": app.info is served twice (plugin "a")`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load(nil, background))
	if _, err := host.Load(nil, background); err == nil || err.Error() != "load runs once per host" {
		t.Fatalf("second load = %v, want load runs once per host", err)
	}
}

// Mirrors "load › makes a get before it resolves wait for it".
func TestHostLoadMakesAGetBeforeItResolvesWaitForIt(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(background, hostInfo, jsonObject(), GetOptions{})
		must(t, err)
		got <- ref
	}()
	awaitUnbound(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "a", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })}}, background))
	if title := memberOf(t, <-got, "title"); title != "t" {
		t.Fatalf("title = %v, want t", title)
	}
}

// Mirrors "the start barrier › resolves once every host-lifetime object had its first outcome; a failed one is reported and retried".
func TestHostStartBarrierResolvesOnceEveryHostLifetimeObjectHadItsFirstOutcome(t *testing.T) {
	loop := newScopeLoop()
	var attempts atomic.Int32
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveInfoFactory(h, nil); err != nil {
			return err
		}
		return h.Serve(hostCounter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempt := attempts.Add(1)
			self.Open(func(context.Context) (any, error) {
				if attempt == 1 {
					return nil, errors.New("offline")
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			implementation := impl("count", constant(float64(attempt)))
			implementation.Members.Methods = map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }}
			return implementation, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.counter: transient"}) {
		t.Fatalf("failures = %v, want [app.counter: transient]", got)
	}
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(background, hostCounter, jsonObject(), GetOptions{})
		must(t, err)
		got <- ref
	}()
	scopeEventually(t, "the getter holds the counter", func() bool { return countBindings(host) >= 1 })
	loop.AdvanceAsync(1000)
	if count := memberOf(t, <-got, "count"); count != float64(2) {
		t.Fatalf("count = %v, want 2", count)
	}
}

// Mirrors "the start barrier › is not held by a hanging open: its attempt is abandoned at readyTimeout".
func TestHostStartBarrierIsNotHeldByAHangingOpen(t *testing.T) {
	loop := newScopeLoop()
	ready := 100 * time.Millisecond
	host := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: noopOnError, loop: loop})
	defer closeHosts(t, host)
	never := make(chan struct{})
	defer close(never)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostInfo, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { <-never; return nil }, func(NativeCloseReason) {})
			return impl("title", constant("t")), nil
		}, ServeOptions{})
	})
	done := startLoad(host, HostFacet{Name: "a", Facet: facet})
	scopeEventually(t, "the attempt's deadline timer was set", func() bool { return loop.TimerCount() > 0 })
	loop.AdvanceAsync(99)
	notDone(t, host, done)
	loop.AdvanceAsync(1)
	must(t, <-done)
}

var (
	hostHarness  = mustContract(declareLocal("app.harness", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	hostCodeMode = mustContract(declareLocal("app.codemode", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
)

// Mirrors "the start barrier › waits for a local blocker's first outcome: a host object parked on another one here (CodeMode → Harness)".
func TestHostStartBarrierWaitsForALocalBlockersFirstOutcome(t *testing.T) {
	opened := make(chan struct{})
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hostHarness, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { <-opened; return nil }, func(NativeCloseReason) {})
			return impl("n", constant(float64(1))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(hostCodeMode, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, hostHarness, jsonObject(), "n"), nil
		}, ServeOptions{})
	})
	done := startLoad(host, HostFacet{Name: "@app/core", Facet: facet})
	notDone(t, host, done)
	close(opened)
	must(t, <-done)
	ref, err := host.Hold(background, hostCodeMode, jsonObject(), GetOptions{})
	must(t, err)
	awaitRefState(t, "live", ref, func(s *RefState) bool { return s.Kind == RefStateLive })
}

// Mirrors "the start barrier › ends its pass at a local blocker's own failure: the dependent parked failed { waiting } has its first outcome".
func TestHostStartBarrierEndsItsPassAtALocalBlockersOwnFailure(t *testing.T) {
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hostHarness, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { return errors.New("offline") }, func(NativeCloseReason) {})
			return impl("n", constant(float64(1))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(hostCodeMode, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, hostHarness, jsonObject(), "n"), nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.harness: transient"}) {
		t.Fatalf("failures = %v, want [app.harness: transient]", got)
	}
	ref, err := host.Hold(background, hostCodeMode, jsonObject(), GetOptions{})
	must(t, err)
	state := awaitRefState(t, "failed", ref, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "waiting for app.harness: app.harness: offline" {
		t.Fatalf("state = %+v, want failed waiting for app.harness: app.harness: offline", state)
	}
}

// Mirrors "the start barrier › ends its pass at a remote blocker: a wait on another host is a first outcome".
func TestHostStartBarrierEndsItsPassAtARemoteBlocker(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	parent, _ := rawParentLinks(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostCodeMode, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, Notes, jsonObject("session", "S1"), "count"), nil
		}, ServeOptions{})
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/codemode", Facet: facet}}, background))
	if held := parent.Next("hold").(*link.HoldMessage); held.Type != Notes.Name {
		t.Fatalf("hold type = %s, want %s", held.Type, Notes.Name)
	}
	ref, err := host.Hold(background, hostCodeMode, jsonObject(), GetOptions{})
	must(t, err)
	if kind := refStateOf(ref).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
}

// Mirrors "the start barrier › is settled by host.close: an object of its cohort that ends before its first outcome settles it as ended".
func TestHostStartBarrierIsSettledByHostClose(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	never := make(chan struct{})
	defer close(never)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostInfo, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { <-never; return nil }, func(NativeCloseReason) {})
			return impl("title", constant("t")), nil
		}, ServeOptions{})
	})
	done := startLoad(host, HostFacet{Name: "a", Facet: facet})
	scopeEventually(t, "the cohort's object started", func() bool { return countObjects(host) >= 1 })
	notDone(t, host, done)
	must(t, host.Close(background))
	must(t, <-done)
}

var (
	hostMiddle  = mustContract(declareLocal("app.middle", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	hostSlow    = mustContract(declareLocal("app.slow", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	hostMissing = mustContract(declareLocal("app.missing", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
)

// Mirrors "the start barrier › waits for a local blocker parked failed { waiting } on one still in its first attempt (fix round 1, Sol 4)".
func TestHostStartBarrierWaitsForALocalBlockerParkedWaitingOnOneStillInItsFirstAttempt(t *testing.T) {
	gate := make(chan struct{})
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hostSlow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { <-gate; return nil }, func(NativeCloseReason) {})
			return impl("n", constant(float64(1))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(hostMiddle, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			slow := readsN(self, hostSlow, jsonObject("id", params.Value("id")), "n")
			self.Get(hostMissing, jsonObject())
			return slow, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(hostCore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, hostMiddle, jsonObject("id", float64(1)), "n"), nil
		}, ServeOptions{})
	})
	done := startLoad(host, HostFacet{Name: "@app/core", Facet: facet})
	notDone(t, host, done)
	middle, err := host.Hold(background, hostMiddle, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	awaitRefState(t, "middle waiting", middle, func(s *RefState) bool { return s.Kind == RefStateFailed && s.Cause.Kind == data.FailureWaiting })
	notDone(t, host, done)
	close(gate)
	must(t, <-done)
}

// Mirrors "the start barrier › waits for it whichever it declares first (fix round 1, Astra 3)".
func TestHostStartBarrierWaitsForItWhicheverItDeclaresFirst(t *testing.T) {
	typ := func(name string, params contracts.Params) *contracts.Contract {
		return mustContract(declareLocal(name, contracts.LocalDeclaration{Version: 1, Params: params}))
	}
	a := typ("app.a", nil)
	b := typ("app.b", contracts.Params{contracts.Prop("id", contracts.Int())})
	slow := typ("app.slow", contracts.Params{contracts.Prop("id", contracts.Int())})
	missing := typ("app.missing", nil)
	gate := make(chan struct{})
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(a, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Get(b, jsonObject("id", float64(1)))
			return contracts.Implementation{}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(b, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Get(missing, jsonObject())
			self.Get(slow, jsonObject("id", float64(1)))
			return contracts.Implementation{}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(slow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { <-gate; return nil }, func(NativeCloseReason) {})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})
	done := startLoad(host, HostFacet{Name: "@app/a", Facet: facet})
	turn(host)
	notDone(t, host, done)
	close(gate)
	must(t, <-done)
}

var (
	hostBootstrap = mustContract(declareLocal("app.bootstrap", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	hostAgent     = mustContract(declareRemote("app.agent", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			session, _ := params.Value("session").(string)
			return Session.Address(jsonObject("id", session)), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	hostCatalog = mustContract(declareRemote("app.catalog", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
)

// remoteImpl is `{ variant: "v", members: { n } }`.
func remoteImpl(n chord.Watchable[delta.JsonValue]) contracts.Implementation {
	implementation := impl("n", n)
	implementation.Variant = "v"
	return implementation
}

// Mirrors "startup across hosts (dependency model §6) › starts two hosts that would have deadlocked (Sol's probe): an acyclic graph needs no timeout".
func TestHostStartupAcrossHostsStartsTwoHostsThatWouldHaveDeadlocked(t *testing.T) {
	loop := newScopeLoop()
	r := relay(t, relayedOptions{
		Loop: loop,
		Server: func(h FacetHost) error {
			if err := h.Serve(hostBootstrap, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				return readsN(self, hostAgent, jsonObject("session", "S1"), "n"), nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(hostCatalog, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
				return remoteImpl(constant(float64(7))), nil
			}, ServeOptions{})
		},
		Worker: func(h FacetHost) error {
			// The worker's own host object waits for the server's catalog during the worker's load.
			return h.Serve(hostAgent, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				implementation := readsN(self, hostCatalog, jsonObject(), "n")
				implementation.Variant = "v"
				return implementation, nil
			}, ServeOptions{})
		},
	})
	defer closeHosts(t, r.Tab, r.Server)
	r.Transported()
	r.Transported()
	bootstrap, err := r.Server.Hold(background, hostBootstrap, jsonObject(), GetOptions{})
	must(t, err)
	awaitRefState(t, "bootstrap live", bootstrap, func(s *RefState) bool { return s.Kind == RefStateLive })
	if n := memberOf(t, bootstrap, "n"); n != float64(7) {
		t.Fatalf("n = %v, want 7", n)
	}
	if r.Errors.Server.Len() != 0 || r.Errors.Worker.Len() != 0 {
		t.Fatalf("errors: server %v, worker %v; want none", r.Errors.Server.All(), r.Errors.Worker.All())
	}
}

// Mirrors "startup across hosts (dependency model §6) › places a hold arriving before the publication here, not over the parent".
func TestHostStartupAcrossHostsPlacesAHoldArrivingBeforeThePublicationHereNotOverTheParent(t *testing.T) {
	address := contracts.Address(Session.Address(jsonObject("id", "S1")))
	worker := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal("S1"), OnError: noopOnError})
	defer closeHosts(t, worker)
	a, b := link.MemoryLinkPair()
	must(t, worker.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	parent := newRawPeer(t, b, rawPeerOptions{})
	c, d := link.MemoryLinkPair()
	must(t, worker.Accept(c, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	child := newRawPeer(t, d, rawPeerOptions{})
	params, err := link.JsonPart(jsonObject("session", "S1"))
	must(t, err)
	child.Send(&link.HoldMessage{
		Hold: 0, Type: PlacedNotes.Name, Version: PlacedNotes.Version, Fingerprint: PlacedNotes.Fingerprint, Params: params,
		Address: link.WireAddress{Kind: "session", Params: jsonObject("id", "S1")},
	})
	mine, err := worker.Hold(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	transported(a, b, c, d)
	if kind := refStateOf(mine).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
	mustLoad(t)(worker.Load([]HostFacet{{Name: "@test/w", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, PlacedNotes); return err })}}, background))
	if bound := child.Next("bound").(*link.BoundMessage); bound.Holds[0].Hold != 0 {
		t.Fatalf("bound hold = %d, want 0", bound.Holds[0].Hold)
	}
	transported(a, b, c, d)
	awaitRefState(t, "live", mine, func(s *RefState) bool { return s.Kind == RefStateLive })
	if kinds := parent.Kinds(); !reflect.DeepEqual(kinds, []string{"hello"}) {
		t.Fatalf("parent kinds = %v, want [hello]", kinds)
	}
}

// Mirrors "startup across hosts (dependency model §6) › wakes a parked chain over three hosts in order, its causes passed on (tab → worker → server)".
func TestHostStartupAcrossHostsWakesAParkedChainOverThreeHostsInOrder(t *testing.T) {
	chat := mustContract(declareRemote("app.chat", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			session, _ := params.Value("session").(string)
			return Session.Address(jsonObject("id", session)), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	loop := newScopeLoop()
	var order seenList[string]
	var catalogs atomic.Int32
	r := relay(t, relayedOptions{
		Loop: loop,
		Server: func(h FacetHost) error {
			return h.Serve(hostCatalog, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				attempt := catalogs.Add(1)
				selfOpen(self, func(context.Context) error {
					if attempt == 1 {
						return errors.New("offline")
					}
					return nil
				}, func(NativeCloseReason) {})
				self.OnLive(func(Ctx) *Handled { order.add("catalog"); return nil })
				return remoteImpl(constant(float64(attempt))), nil
			}, ServeOptions{})
		},
		Worker: func(h FacetHost) error {
			return h.Serve(chat, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				implementation := readsN(self, hostCatalog, jsonObject(), "n")
				self.OnLive(func(Ctx) *Handled { order.add("chat"); return nil })
				implementation.Variant = "v"
				return implementation, nil
			}, ServeOptions{})
		},
	})
	defer closeHosts(t, r.Tab, r.Server)
	view, err := r.Tab.Hold(background, chat, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	for range 5 {
		r.Transported()
	}
	state := awaitRefState(t, "view waiting", view, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "waiting for app.catalog: app.catalog: offline" || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed waiting for app.catalog: app.catalog: offline", state)
	}
	// The catalog's own backoff (1 s) is the only timer: it retries, and the chain goes on in order.
	loop.AdvanceAsync(1000)
	for range 5 {
		r.Transported()
	}
	awaitRefState(t, "view live", view, func(s *RefState) bool { return s.Kind == RefStateLive })
	if got := order.all(); !reflect.DeepEqual(got, []string{"catalog", "chat"}) {
		t.Fatalf("order = %v, want [catalog chat]", got)
	}
	if n := memberOf(t, view, "n"); n != float64(2) {
		t.Fatalf("n = %v, want 2", n)
	}
}

// Mirrors "get › waits for an unserved type and a contract mismatch until its readyTimeout, then rejects with the cause".
func TestHostGetWaitsForAnUnservedTypeAndAContractMismatchUntilItsReadyTimeout(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	quick := GetOptions{ReadyTimeout: new(5 * time.Millisecond)}
	_, err := host.Get(background, hostPage, jsonObject("id", float64(1)), quick)
	wantChordError(t, err, data.CodeUnavailable, "", "app.page is not served here")
	infoV2 := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 2, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))
	changed := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Int())}}))
	for _, typ := range []*contracts.Contract{infoV2, changed} {
		_, err := host.Get(background, typ, jsonObject(), quick)
		wantChordError(t, err, data.CodeContractMismatch, "", "")
	}
	same := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))
	ref, err := host.Get(background, same, jsonObject(), GetOptions{})
	must(t, err)
	if title := memberOf(t, ref, "title"); title != "t" {
		t.Fatalf("title = %v, want t", title)
	}
}

// Mirrors "get › checks params with L1 (invalid/params)".
func TestHostGetChecksParamsWithL1(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return servePage(h, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	// A string is not an int.
	_, err := host.Get(background, hostPage, jsonObject("id", "x"), GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonParams, "")
}

// Mirrors "deadlines of gets › reject with unavailable while no attempt has failed (the last failure: see attempt.test.ts)".
func TestHostDeadlinesOfGetsRejectWithUnavailableWhileNoAttemptHasFailed(t *testing.T) {
	loop := newScopeLoop()
	gate := make(chan struct{})
	defer close(gate)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &seenList[string]{}) }, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(500 * time.Millisecond)})
		waiting <- err
	}()
	awaitUnboundOrObject(t, host)
	loop.AdvanceAsync(500)
	wantChordError(t, <-waiting, data.CodeUnavailable, "", "app.svc(1): did not become live within 500 ms")
}

// awaitUnboundOrObject waits until the host has an object or a waiting request (a getter made its reference).
func awaitUnboundOrObject(t testing.TB, host Host) {
	t.Helper()
	h := host.(*chordHost)
	for {
		var made bool
		h.enter(func() { made = len(h.objects) > 0 || h.unbound.names.size() > 0 || len(h.pathHolders()) > 0 })
		if made {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal("no request was ever made")
		default:
		}
		turn(host)
	}
}

// Mirrors "deadlines of gets › include the wait for load".
func TestHostDeadlinesOfGetsIncludeTheWaitForLoad(t *testing.T) {
	loop := newScopeLoop()
	host := newHost(t, HostOptions{OnError: noopOnError, loop: loop})
	defer closeHosts(t, host)
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostInfo, jsonObject(), GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
		waiting <- err
	}()
	awaitUnbound(t, host)
	loop.AdvanceAsync(100)
	wantChordError(t, <-waiting, data.CodeUnavailable, "", "app.info: did not become live within 100 ms")
}

// Mirrors "deadlines of gets › are per getter: the first to pass releases only its own reference, the attempt goes on for the other".
func TestHostDeadlinesOfGetsArePerGetter(t *testing.T) {
	loop := newScopeLoop()
	gate := make(chan struct{})
	var closes seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes) }, loadedOptions{Loop: loop, ReadyTimeout: new(10 * time.Second)})
	defer closeHosts(t, host)
	early := make(chan error, 1)
	late := make(chan *Reference, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
		early <- err
	}()
	go func() {
		ref, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(time.Second)})
		must(t, err)
		late <- ref
	}()
	for {
		var waiting int
		h := host.(*chordHost)
		h.enter(func() { waiting = len(h.objects) })
		if waiting > 0 && countBindings(host) == 2 {
			break
		}
		turn(host)
	}
	loop.AdvanceAsync(100)
	wantChordError(t, <-early, data.CodeUnavailable, "", "")
	close(gate)
	if n := memberOf(t, <-late, "n"); n != float64(1) {
		t.Fatalf("n = %v, want 1", n)
	}
	if got := closes.all(); len(got) != 0 {
		t.Fatalf("closes = %v, want none", got)
	}
}

// countBindings is the number of references holding the host's objects.
func countBindings(host Host) int {
	h := host.(*chordHost)
	var n int
	h.enter(func() {
		for _, object := range h.objectList() {
			n += len(object.holders())
		}
	})
	return n
}

var hostOuter = mustContract(declareLocal("app.outer", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))

// Mirrors "close › ends every object, the last to go live first, and rejects new calls".
//
// Pi: packages/chord/test/next/host/host.test.ts:520 (ends every object, the last to go live first, and rejects new calls)
func TestHostCloseEndsEveryObjectTheLastToGoLiveFirstAndRejectsNewCalls(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveInfoFactory(h, nil); err != nil {
			return err
		}
		return h.Serve(hostOuter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsAs(self, hostInfo, jsonObject(), "title", "title"), nil
		}, ServeOptions{})
	}, loadedOptions{})
	outer, err := host.Get(background, hostOuter, jsonObject(), GetOptions{})
	must(t, err)
	info, err := host.Get(background, hostInfo, jsonObject(), GetOptions{})
	must(t, err)
	outerWatch, err := outer.Member("title").Watch(background)
	must(t, err)
	infoWatch, err := info.Member("title").Watch(background)
	must(t, err)
	// The close ends the objects one after the other in one section: when the last to go live ended first, outer's watch
	// is closed by the time info's is seen closed (goroutines observing both ends would race each other's wake-up).
	outerEndedFirst := make(chan bool, 1)
	go func() {
		<-infoWatch.Closed()
		select {
		case <-outerWatch.Closed():
			outerEndedFirst <- true
		default:
			outerEndedFirst <- false
		}
	}()
	must(t, host.Close(background))
	scopeEventually(t, "both watches ended", func() bool {
		select {
		case <-outerWatch.Closed():
			return true
		default:
			return false
		}
	})
	if !<-outerEndedFirst {
		t.Fatal("info's watch ended before outer's: the last to go live must end first")
	}
	_, err = host.Get(background, hostInfo, jsonObject(), GetOptions{})
	wantChordError(t, err, data.CodeReleased, data.ReasonClosed, "the host is closed")
	if _, err := info.Member("title").Value(); err == nil || err.Error() != "app.info: ended" {
		t.Fatalf("a read after the close = %v, want app.info: ended", err)
	}
}

// Mirrors "close › fails a get waiting for load".
func TestHostCloseFailsAGetWaitingForLoad(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	got := make(chan error, 1)
	go func() { _, err := host.Get(background, hostInfo, jsonObject(), GetOptions{}); got <- err }()
	awaitUnbound(t, host)
	must(t, host.Close(background))
	wantChordError(t, <-got, data.CodeReleased, data.ReasonClosed, "the host is closed")
}

// Mirrors "close › ends every object at once: calls fail released before any close ran".
//
// Pi: packages/chord/test/next/host/host.test.ts:548 (ends every object at once: calls fail released before any close ran)
func TestHostCloseEndsEveryObjectAtOnceCallsFailReleasedBeforeAnyCloseRan(t *testing.T) {
	closing := make(chan struct{})
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostCounter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { <-closing; return nil })
			implementation := impl("count", constant(float64(1)))
			implementation.Members.Methods = map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }}
			return implementation, nil
		}, ServeOptions{})
	}, loadedOptions{})
	counter, err := host.Get(background, hostCounter, jsonObject(), GetOptions{})
	must(t, err)
	closed := make(chan error, 1)
	go func() { closed <- host.Close(background) }()
	scopeEventually(t, "the object ended", func() bool { _, err := counter.Call("bump", nil, background); return err != nil })
	_, err = counter.Call("bump", nil, background)
	wantChordError(t, err, data.CodeReleased, data.ReasonEnded, "app.counter: ended")
	close(closing)
	must(t, <-closed)
}

// Mirrors "close › aborts a pending open and does not wait for it; its late value is closed with close".
func TestHostCloseAbortsAPendingOpenAndDoesNotWaitForItItsLateValueIsClosedWithClose(t *testing.T) {
	gate := make(chan struct{})
	var closes, opens seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes, &opens) }, loadedOptions{})
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
		got <- err
	}()
	awaitUnboundOrObject(t, host)
	turn(host)
	// Pi's `await flushed()` runs the activation as far as the open waiting on its gate; `turn` does not wait for the host's
	// tasks, so wait for the open to have been called.
	scopeEventually(t, "the open started", func() bool { return opens.len() == 1 })
	must(t, host.Close(background))
	wantChordError(t, <-got, data.CodeReleased, data.ReasonEnded, "app.svc(1): ended")
	close(gate)
	scopeEventually(t, "the late value closed", func() bool { return closes.len() == 1 })
	if got := closes.all(); !reflect.DeepEqual(got, []string{"svc 1 close"}) {
		t.Fatalf("closes = %v, want [svc 1 close]", got)
	}
}

// Mirrors "close › joins an ending running in the background".
func TestHostCloseJoinsAnEndingRunningInTheBackground(t *testing.T) {
	closing := make(chan struct{})
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { <-closing; return nil })
			return impl("n", constant(params.Value("id"))), nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	ref.Release()
	closed := make(chan error, 1)
	go func() { closed <- host.Close(background) }()
	turn(host)
	select {
	case <-closed:
		t.Fatal("close returned before the ending did")
	default:
	}
	close(closing)
	must(t, <-closed)
}

// Mirrors "close › closes object by object, a dependent before its dependencies".
//
// Pi: packages/chord/test/next/host/host.test.ts:605 (closes object by object, a dependent before its dependencies)
func TestHostCloseClosesObjectByObjectADependentBeforeItsDependencies(t *testing.T) {
	view := mustContract(declareLocal("app.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	var events seenList[string]
	// open is a native whose close records when it starts and, a turn later, when it ends.
	var host Host
	open := func(self Self, name string) {
		self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error {
			events.add(name + " closing")
			turn(host)
			events.add(name + " closed")
			return nil
		})
	}
	host, _ = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			open(self, fmt.Sprintf("svc %v", params.Value("id")))
			return impl("n", constant(params.Value("id"))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(view, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			open(self, fmt.Sprintf("view %v", params.Value("id")))
			return readsN(self, hostSvc, jsonObject("id", params.Value("id")), "n"), nil
		}, ServeOptions{})
	}, loadedOptions{})
	for _, id := range []float64{1, 2} {
		_, err := host.Get(background, view, jsonObject("id", id), GetOptions{})
		must(t, err)
	}
	must(t, host.Close(background))
	want := []string{"view 2 closing", "view 2 closed", "svc 2 closing", "svc 2 closed", "view 1 closing", "view 1 closed", "svc 1 closing", "svc 1 closed"}
	if got := events.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Mirrors "close, its order and its races › returns one closing to every caller: a second close waits for the first".
//
// Pi: packages/chord/test/next/host/host.test.ts:646 (returns one closing to every caller: a second close waits for the first)
func TestHostCloseReturnsOneClosingToEveryCaller(t *testing.T) {
	closing := make(chan struct{})
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostCounter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, nil },
				func(any, NativeCloseReason, context.Context) error { <-closing; return nil })
			return impl("count", constant(float64(1))), nil
		}, ServeOptions{})
	}, loadedOptions{})
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- host.Close(background) }()
	go func() { second <- host.Close(background) }()
	turn(host)
	select {
	case <-second:
		t.Fatal("a second close returned before the first")
	default:
	}
	close(closing)
	must(t, <-first)
	must(t, <-second)
}

// Mirrors "close, its order and its races › tears down what a failed dependent holds with close, and waits for it".
func TestHostCloseTearsDownWhatAFailedDependentHoldsWithCloseAndWaitsForIt(t *testing.T) {
	pair := mustContract(declareLocal("app.pair", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	closing := make(chan struct{})
	var closes seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, nil }, func(_ any, reason NativeCloseReason, _ context.Context) error {
				closes.add("svc " + string(reason.Kind))
				<-closing
				return nil
			})
			return impl("n", constant(params.Value("id"))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(hostCore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, errors.New("offline") }, func(any, NativeCloseReason, context.Context) error { return nil })
			return impl("n", constant(float64(0))), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(pair, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			a := self.Get(hostSvc, jsonObject("id", params.Value("id"))).Member("n")
			b := self.Get(hostCore, jsonObject()).Member("n")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: stateOfMembers(
				"n", self.Derive([]any{a, b}, func(values []any) (delta.JsonValue, error) { return values[0], nil }))}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	// The pair fails on the core (R18) and keeps its references: svc(1), live before the pair, held by it alone.
	svc, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	go func() { _, _ = host.Get(background, pair, jsonObject("id", float64(1)), GetOptions{}) }()
	scopeEventually(t, "the pair's object exists", func() bool {
		h := host.(*chordHost)
		var n int
		h.enter(func() { n = len(h.objects) })
		return n >= 3
	})
	turn(host)
	svc.Release()
	closed := make(chan error, 1)
	go func() { closed <- host.Close(background) }()
	scopeEventually(t, "svc closed with close", func() bool { return closes.len() == 1 })
	turn(host)
	select {
	case <-closed:
		t.Fatal("close returned before the teardown did")
	default:
	}
	if got := closes.all(); !reflect.DeepEqual(got, []string{"svc close"}) {
		t.Fatalf("closes = %v, want [svc close]", got)
	}
	close(closing)
	must(t, <-closed)
}

// Mirrors "close, its order and its races › goes on when onError throws: failures are still classified, teardowns still finish".
func TestHostCloseGoesOnWhenOnErrorThrows(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) { panic(errors.New("onError threw")) }})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			id := params.Value("id")
			self.Open(func(context.Context) (any, error) {
				if id == float64(2) {
					return nil, data.Broken(errors.New("no config"))
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return errors.New("close threw") })
			return impl("n", constant(id)), nil
		}, ServeOptions{})
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "a", Facet: facet}}, background))
	// The background end reports its throwing close; the next instance waits for that teardown.
	first, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	first.Release()
	again, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	if n := memberOf(t, again, "n"); n != float64(1) {
		t.Fatalf("n = %v, want 1", n)
	}
	_, err = host.Get(background, hostSvc, jsonObject("id", float64(2)), GetOptions{})
	wantChordError(t, err, data.CodeUnavailable, "", "app.svc(2): no config")
}

// Mirrors "close, its order and its races › with a backoff window running › ends an object waiting for its window and clears every timer".
func TestHostCloseWithABackoffWindowRunningEndsAnObjectWaitingForItsWindowAndClearsEveryTimer(t *testing.T) {
	loop := newScopeLoop()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostSvc, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, errors.New("offline") }, func(any, NativeCloseReason, context.Context) error { return nil })
			return impl("n", constant(float64(0))), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	first := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
		first <- err
	}()
	awaitUnboundOrObject(t, host)
	// Pi's advanceTimersByTimeAsync runs the microtasks first, so the open's rejection has failed the attempt and its backoff
	// window runs before the first get's 100 ms deadline fires. The open is a task here: the clock waits for it, as it does
	// for the host's other tasks. Moved at once, the deadline ends the first get with the open still in flight and no window
	// ever runs, so the close below clears a window that never was and finds an abandoned activation still unwinding.
	advanceSettled(loop, host, 100)
	<-first
	var windowRunning bool
	h := host.(*chordHost)
	h.enter(func() {
		for identity := range h.backoff.entries {
			windowRunning = windowRunning || h.backoff.waiting(identity)
		}
	})
	if !windowRunning {
		t.Fatal("no backoff window runs after the first get failed")
	}
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
		waiting <- err
	}()
	scopeEventually(t, "a getter waits", func() bool { return countBindings(host) >= 1 })
	turn(host)
	must(t, host.Close(background))
	wantChordError(t, <-waiting, data.CodeReleased, data.ReasonEnded, "app.svc(1): ended")
	if got := loop.TimerCount(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
}

// Mirrors "getters › one cancelled while another waits for the same object: the other gets it, the object lives".
func TestHostGettersOneCancelledWhileAnotherWaitsForTheSameObjectTheOtherGetsIt(t *testing.T) {
	cancellable, cancel := chordctx.WithCancel(background)
	var factories atomic.Int32
	gate := make(chan struct{})
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factories.Add(1)
			// Pi's factory cancels the first getter while the second waits: both are waiting while the open is.
			selfOpen(self, func(context.Context) error { <-gate; return nil }, func(NativeCloseReason) {})
			return impl("id", constant(float64(1))), nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	type outcome struct {
		ref *Reference
		err error
	}
	results := make([]chan outcome, 2)
	for i, ctx := range []context.Context{cancellable, background} {
		results[i] = make(chan outcome, 1)
		go func() {
			ref, err := host.Get(ctx, hostPage, jsonObject("id", float64(1)), GetOptions{})
			results[i] <- outcome{ref, err}
		}()
		scopeEventually(t, "the getter waits", func() bool { return countBindings(host) >= i+1 })
	}
	cancel(errors.New("cancelled"))
	if cancelled := <-results[0]; cancelled.err == nil || cancelled.err.Error() != "cancelled" {
		t.Fatalf("the cancelled getter = %v, want cancelled", cancelled.err)
	}
	close(gate)
	kept := <-results[1]
	must(t, kept.err)
	if id := memberOf(t, kept.ref, "id"); id != float64(1) {
		t.Fatalf("id = %v, want 1", id)
	}
	_, err := host.Get(background, hostPage, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	if got := factories.Load(); got != 1 {
		t.Fatalf("factory runs = %d, want 1", got)
	}
}

var (
	principalWho = mustContract(declareRemote("app.who", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("who", contracts.Str())}})},
	}))
	principalOuter = mustContract(declareRemote("app.outer", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("who", contracts.Str())}})},
	}))
	principalInner = mustContract(declareLocal("app.inner", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("who", contracts.Str())},
	}))
	principalDesk = mustContract(declareLocal("app.desk", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("who", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	}))
	principalGate = mustContract(declareRemote("app.gate", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("who", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
		}})},
	}))
)

// serveAll serves every type above: `Who` shows the principal its object is for.
func serveAll(factories *seenList[string]) func(h FacetHost) error {
	return func(h FacetHost) error {
		who := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			ref, err := ctx.(Ctx).Get(principalWho, jsonObject("id", float64(2)), GetOptions{})
			if err != nil {
				return nil, err
			}
			return ref.Member("who").Value()
		}
		for _, serve := range []func() error{
			func() error {
				return h.Serve(principalWho, func(params *delta.JsonObject, principal data.Principal, _ Self) (contracts.Implementation, error) {
					factories.add(fmt.Sprintf("%v %s", params.Value("id"), principalKind(principal)))
					implementation := impl("who", constant(principalKind(principal)))
					implementation.Variant = "v"
					return implementation, nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(principalOuter, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					implementation := readsAs(self, principalWho, jsonObject("id", float64(3)), "who", "who")
					implementation.Variant = "v"
					return implementation, nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(principalInner, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
					return readsAs(self, principalWho, jsonObject("id", params.Value("id")), "who", "who"), nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(principalDesk, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{"who": who}}}, nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(principalGate, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{"who": who}}}, nil
				}, ServeOptions{})
			},
		} {
			if err := serve(); err != nil {
				return err
			}
		}
		return nil
	}
}

// Mirrors "principals and where a reference goes › host.get acts as the host's principal; a host without one (a client) serves no remote type".
func TestHostPrincipalsHostGetActsAsTheHostsPrincipalAHostWithoutOneServesNoRemoteType(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var factories seenList[string]
		l := linked(t, serveAll(&factories), linkedOptions{})
		defer closeHosts(t, l.Client, l.Server)
		ref, err := l.Server.Get(background, principalWho, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		if who := memberOf(t, ref, "who"); who != "server" {
			t.Fatalf("who = %v, want server", who)
		}
		client := newHost(t, HostOptions{OnError: noopOnError})
		report, err := client.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&seenList[string]{})(h) })}}, background)
		must(t, err)
		if got, want := failedMessages(report.Failed), [][2]string{{"@test/app", `plugin "@test/app": app.who: a host without a principal serves only local types`}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("failed = %v, want %v", got, want)
		}
		closeHosts(t, client)
	})
}

// Mirrors "› a host without an address serves no remote type, whatever its principal".
func TestHostPrincipalsAHostWithoutAnAddressServesNoRemoteType(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: noopOnError})
		report, err := host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&seenList[string]{})(h) })}}, background)
		must(t, err)
		if got, want := failedMessages(report.Failed), [][2]string{{"@test/app", `plugin "@test/app": app.who: a host without an address serves only local types`}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("failed = %v, want %v", got, want)
		}
		closeHosts(t, host)
	})
}

// Mirrors "› ctx.get acts as ctx.principal: a guest's call gets the guest's view; a local handler's acts as the host".
func TestHostPrincipalsCtxGetActsAsCtxPrincipal(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, serveAll(&seenList[string]{}), linkedOptions{Child: guestPrincipal("S1")})
		defer closeHosts(t, l.Client, l.Server)
		gate, err := l.Client.Get(background, principalGate, jsonObject(), GetOptions{})
		must(t, err)
		if who, err := gate.Call("who", nil, background); err != nil || who != "guest" {
			t.Fatalf("gate.who = %v, %v; want guest", who, err)
		}
		desk, err := l.Server.Get(background, principalDesk, jsonObject(), GetOptions{})
		must(t, err)
		if who, err := desk.Call("who", nil, background); err != nil || who != "server" {
			t.Fatalf("desk.who = %v, %v; want server", who, err)
		}
	})
}

// Mirrors "› self.get acts as a remote object's principal, a local object's as the host's".
func TestHostPrincipalsSelfGetActsAsARemoteObjectsPrincipalALocalObjectsAsTheHosts(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, serveAll(&seenList[string]{}), linkedOptions{Child: guestPrincipal("S1")})
		defer closeHosts(t, l.Client, l.Server)
		outer, err := l.Client.Get(background, principalOuter, jsonObject(), GetOptions{})
		must(t, err)
		if who := memberOf(t, outer, "who"); who != "guest" {
			t.Fatalf("outer.who = %v, want guest", who)
		}
		inner, err := l.Server.Get(background, principalInner, jsonObject("id", float64(4)), GetOptions{})
		must(t, err)
		if who := memberOf(t, inner, "who"); who != "server" {
			t.Fatalf("inner.who = %v, want server", who)
		}
	})
}

// Mirrors "› identity includes the principal key: equal keys share one object".
func TestHostPrincipalsIdentityIncludesThePrincipalKeyEqualKeysShareOneObject(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var factories seenList[string]
		address := serverAddress()
		host := newHost(t, HostOptions{
			Address: &address, Principal: guestPrincipal("S1"), OnError: noopOnError,
			PrincipalKey: data.NewPrincipalKeyFunction(principalKind),
		})
		defer closeHosts(t, host)
		mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&factories)(h) })}}, background))
		a, b := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: guestPrincipal("S2"), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{})
		_, err := host.Get(background, principalWho, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		params, err := link.JsonPart(jsonObject("id", float64(1)))
		must(t, err)
		hold := &link.HoldMessage{Hold: 0, Type: principalWho.Name, Version: principalWho.Version, Fingerprint: principalWho.Fingerprint, Params: params, Address: serverWireAddress}
		peer.Send(hold)
		peer.Next("bound")
		if got := factories.all(); !reflect.DeepEqual(got, []string{"1 guest"}) {
			t.Fatalf("factories = %v, want [1 guest]", got)
		}
		c, d := link.MemoryLinkPair()
		must(t, host.Accept(c, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		owner := newRawPeer(t, d, rawPeerOptions{})
		owner.Send(hold)
		owner.Next("bound")
		if got := factories.all(); !reflect.DeepEqual(got, []string{"1 guest", "1 owner"}) {
			t.Fatalf("factories = %v, want [1 guest, 1 owner]", got)
		}
	})
}

// Mirrors "› remote objects with empty params are not activated by load; they live while referenced".
func TestHostPrincipalsRemoteObjectsWithEmptyParamsAreNotActivatedByLoadTheyLiveWhileReferenced(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		var factories seenList[string]
		empty := mustContract(declareRemote("app.empty", contracts.RemoteDeclaration{
			Version:     1,
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
			Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
		}))
		l := linked(t, func(h FacetHost) error {
			return h.Serve(empty, func(_ *delta.JsonObject, principal data.Principal, _ Self) (contracts.Implementation, error) {
				factories.add(principalKind(principal))
				return remoteImpl(constant(float64(1))), nil
			}, ServeOptions{})
		}, linkedOptions{})
		defer closeHosts(t, l.Client, l.Server)
		if got := factories.all(); len(got) != 0 {
			t.Fatalf("factories after load = %v, want none", got)
		}
		first, err := l.Server.Get(background, empty, jsonObject(), GetOptions{})
		must(t, err)
		first.Release()
		turn(l.Server)
		_, err = l.Server.Get(background, empty, jsonObject(), GetOptions{})
		must(t, err)
		if got := factories.all(); !reflect.DeepEqual(got, []string{"server", "server"}) {
			t.Fatalf("factories = %v, want [server server]", got)
		}
	})
}

// Mirrors "› a local type never crosses a link; a remote type without a parent link waits for one".
func TestHostPrincipalsALocalTypeNeverCrossesALinkARemoteTypeWithoutAParentLinkWaitsForOne(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, func(FacetHost) error { return nil }, linkedOptions{})
		defer closeHosts(t, l.Client, l.Server)
		inner, err := l.Client.Hold(background, principalInner, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		if state := refStateOf(inner); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
			t.Fatalf("state = %+v, want failed waiting", state)
		}
		_, err = l.Client.Get(background, principalInner, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(5 * time.Millisecond)})
		wantChordError(t, err, data.CodeUnavailable, "", "app.inner is not served here")
		alone := newHost(t, HostOptions{OnError: noopOnError})
		defer closeHosts(t, alone)
		mustLoad(t)(alone.Load(nil, background))
		held, err := alone.Hold(background, principalWho, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		if kind := refStateOf(held).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		_, err = alone.Get(background, principalWho, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(10 * time.Millisecond)})
		wantChordError(t, err, data.CodeUnavailable, "", "app.who(1): did not become live within 10 ms")
	})
}

// Mirrors "› connect takes one open parent link".
func TestHostPrincipalsConnectTakesOneOpenParentLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, func(FacetHost) error { return nil }, linkedOptions{})
		defer closeHosts(t, l.Client, l.Server)
		next, _ := link.MemoryLinkPair()
		err := l.Client.Connect(next, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
		if err == nil || err.Error() != "connect: this host has an open parent link" {
			t.Fatalf("connect = %v, want connect: this host has an open parent link", err)
		}
	})
}

// Mirrors "› close closes the links first: the consumer sees the link drop, no gone".
func TestHostPrincipalsCloseClosesTheLinksFirstTheConsumerSeesTheLinkDropNoGone(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, serveAll(&seenList[string]{}), linkedOptions{})
		defer closeHosts(t, l.Client)
		who, err := l.Client.Get(background, principalWho, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		watch, err := who.Member("who").Watch(background)
		must(t, err)
		must(t, l.Server.Close(background))
		transported(l.Links[0], l.Links[1])
		if kind := refStateOf(who).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		if value := memberOf(t, who, "who"); value != "owner" {
			t.Fatalf("who = %v, want owner", value)
		}
		transported(l.Links[0], l.Links[1])
		select {
		case <-watch.Closed():
			t.Fatal("the watch ended: the consumer saw a gone")
		default:
		}
	})
}

var absentRegistry = registries.Registry[any, any]{Kind: "registry", Name: "app.absent", Version: 1}

// Mirrors "host.registry and the host's close (fix round 1) › ends a waiting host.registry at the close with released/closed, its deadline timer with it (Sol 5, Astra 5)".
func TestHostRegistryEndsAWaitingHostRegistryAtTheCloseWithReleasedClosed(t *testing.T) {
	loop := newScopeLoop()
	host := newHost(t, HostOptions{OnError: noopOnError, loop: loop})
	mustLoad(t)(host.Load(nil, background))
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Registry(background, absentRegistry, GetOptions{ReadyTimeout: new(time.Minute)})
		outcome <- err
	}()
	scopeEventually(t, "the registry wait started", func() bool { return loop.TimerCount() > 0 })
	must(t, host.Close(background))
	wantChordError(t, <-outcome, data.CodeReleased, data.ReasonClosed, "")
	if got := loop.TimerCount(); got != 0 {
		t.Fatalf("timers = %d, want 0: the deadline timer went with the wait", got)
	}
}

// Mirrors "› ends one still waiting for the publication at the close".
func TestHostRegistryEndsOneStillWaitingForThePublicationAtTheClose(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Registry(background, absentRegistry, GetOptions{ReadyTimeout: new(time.Minute)})
		waiting <- err
	}()
	turn(host)
	must(t, host.Close(background))
	wantChordError(t, <-waiting, data.CodeReleased, data.ReasonClosed, "")
}

var (
	hereKind        = mustKind(contracts.NewHostKind("app-here"))
	elsewhereKind   = mustKind(contracts.NewHostKind("app-elsewhere"))
	providerShape   = []contracts.VariantEntry{contracts.Var("view", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})}
	viewImplementer = func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
		implementation := impl("n", constant(float64(1)))
		implementation.Variant = "view"
		return implementation, nil
	}
)

// Mirrors "the selected provider's own checks for a request made here (fix round 1) › waits a same-host remote request the provider's code places elsewhere: invalid/host_address (Astra 4)".
func TestHostProviderChecksWaitsASameHostRemoteRequestTheProvidersCodePlacesElsewhere(t *testing.T) {
	consumer := mustContract(declareRemote("app.placed", contracts.RemoteDeclaration{
		Version: 1, Variants: providerShape,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return hereKind.Address(nil), nil },
	}))
	provider := mustContract(declareRemote("app.placed", contracts.RemoteDeclaration{
		Version: 1, Variants: providerShape,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return elsewhereKind.Address(nil), nil },
	}))
	address := hereKind.Address(nil)
	host := newHost(t, HostOptions{Address: &address, Principal: ownerPrincipal(), OnError: noopOnError})
	defer closeHosts(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error { return h.Serve(provider, viewImplementer, ServeOptions{}) })
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/provider", Facet: facet}}, background))
	ref, err := host.Hold(background, consumer, jsonObject(), GetOptions{})
	must(t, err)
	state := awaitRefState(t, "waiting", ref, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Code != data.CodeInvalid || state.Cause.Error.Reason != data.ReasonHostAddress {
		t.Fatalf("state = %+v, want failed waiting invalid/host_address", state)
	}
	ref.Release()
}

// Mirrors "› fails broken when the provider's hostAddress throws: its own code (Sol 7)".
func TestHostProviderChecksFailsBrokenWhenTheProvidersHostAddressThrows(t *testing.T) {
	consumer := mustContract(declareRemote("app.remote", contracts.RemoteDeclaration{
		Version: 1, Variants: providerShape,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	}))
	provider := mustContract(declareRemote("app.remote", contracts.RemoteDeclaration{
		Version: 1, Variants: providerShape,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
			return contracts.Address{}, errors.New("provider address bug")
		},
	}))
	address := serverAddress()
	server := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: noopOnError})
	client := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, client, server)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error { return h.Serve(provider, viewImplementer, ServeOptions{}) })
	mustLoad(t)(server.Load([]HostFacet{{Name: "@app/provider", Facet: facet}}, background))
	mustLoad(t)(client.Load(nil, background))
	down, up := link.MemoryLinkPair()
	must(t, server.Accept(down, LinkOptions{Principal: serverPrincipal()}, background))
	must(t, client.Connect(up, LinkOptions{Principal: serverPrincipal()}, background))
	held, err := client.Hold(background, consumer, jsonObject(), GetOptions{})
	must(t, err)
	transported(down, up)
	state := awaitRefState(t, "broken", held, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureBroken || state.Cause.Error.Message != "app.remote: provider address bug" {
		t.Fatalf("state = %+v, want failed broken app.remote: provider address bug", state)
	}
	// The same throw for a request made on the provider's host fails it broken too.
	here, err := server.Hold(background, consumer, jsonObject(), GetOptions{})
	must(t, err)
	if state := refStateOf(here); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", state)
	}
}

// Mirrors "a load whose setup throws (decision "Per plugin at startup and reload") › leaves that plugin out and reports it; the others publish, the host and its links stay open".
func TestHostALoadWhoseSetupThrowsLeavesThatPluginOutAndReportsItTheOthersPublishTheHostAndItsLinksStayOpen(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: noopOnError})
		a, b := link.MemoryLinkPair()
		c, d := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, host.Connect(c, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		child := newRawPeer(t, b, rawPeerOptions{})
		parent := newRawPeer(t, d, rawPeerOptions{})
		broken := DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("setup threw") })
		report, err := host.Load([]HostFacet{
			{Name: "@test/broken", Facet: broken},
			{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })},
		}, background)
		must(t, err)
		if got, want := failedMessages(report.Failed), [][2]string{{"@test/broken", `plugin "@test/broken": setup threw`}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("failed = %v, want %v", got, want)
		}
		transported(a, b, c, d)
		if got := []bool{child.Closed(), parent.Closed()}; !reflect.DeepEqual(got, []bool{false, false}) {
			t.Fatalf("closed = %v, want [false false]", got)
		}
		info, err := host.Get(background, hostInfo, jsonObject(), GetOptions{})
		must(t, err)
		if title := memberOf(t, info, "title"); title != "t" {
			t.Fatalf("title = %v, want t", title)
		}
		// What the left-out plugin would have served waits as any missing type does.
		page, err := host.Hold(background, hostPage, jsonObject("id", float64(1)), GetOptions{})
		must(t, err)
		if state := refStateOf(page); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
			t.Fatalf("hold state = %+v, want failed waiting", state)
		}
		closeHosts(t, host)
	})
}

// flakySvc is `serveFlaky`: Svc whose open fails `offline` (broken when broken) while left is above 0.
func flakySvc(h FacetHost, left *atomic.Int32, broken bool) error {
	return h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		self.Open(func(context.Context) (any, error) {
			if left.Add(-1) >= 0 {
				if broken {
					return nil, data.Broken(errors.New("offline"))
				}
				return nil, errors.New("offline")
			}
			return nil, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		return impl("n", constant(params.Value("id"))), nil
	}, ServeOptions{})
}

// Mirrors "acquiring: hold and get › invalid params at once; a local type not served here: hold waits failed, get rejects at its readyTimeout".
func TestHostAcquiringInvalidParamsAtOnceALocalTypeNotServedHereHoldWaitsGetRejectsAtItsReadyTimeout(t *testing.T) {
	loop := newScopeLoop()
	address := serverAddress()
	host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: noopOnError, loop: loop})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, nil); return err })}}, background))
	if _, err := host.Hold(background, Notes, jsonObject("session", float64(1)), GetOptions{}); err == nil || !strings.Contains(err.Error(), "params") {
		t.Fatalf("hold = %v, want an error naming params", err)
	}
	_, err := host.Get(background, Notes, jsonObject("session", float64(1)), GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonParams, "")
	held, err := host.Hold(background, hostInfo, jsonObject(), GetOptions{})
	must(t, err)
	state := refStateOf(held)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "app.info is not served here" || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed waiting: app.info is not served here", state)
	}
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostInfo, jsonObject(), GetOptions{ReadyTimeout: new(5 * time.Millisecond)})
		got <- err
	}()
	// The getter's deadline timer is set in the section that made its reference.
	scopeEventually(t, "the getter's deadline timer was set", func() bool { return loop.TimerCount() > 0 })
	loop.AdvanceAsync(5)
	wantChordError(t, <-got, data.CodeUnavailable, "", "app.info is not served here")
}

// Mirrors "acquiring › before load resolved: hold is loading, get waits; once it resolved both bind, or an unserved type waits".
func TestHostAcquiringBeforeLoadResolvedHoldIsLoadingGetWaits(t *testing.T) {
	loop := newScopeLoop()
	host := newHost(t, HostOptions{OnError: noopOnError, loop: loop})
	defer closeHosts(t, host)
	held, err := host.Hold(background, hostInfo, jsonObject(), GetOptions{})
	must(t, err)
	unserved, err := host.Hold(background, hostPage, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(background, hostInfo, jsonObject(), GetOptions{})
		must(t, err)
		got <- ref
	}()
	refused := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostPage, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(50 * time.Millisecond)})
		refused <- err
	}()
	scopeEventually(t, "both getters wait", func() bool { return len(indexedBindings(host)) >= 4 })
	for _, ref := range []*Reference{held, unserved} {
		if kind := refStateOf(ref).Kind; kind != RefStateLoading {
			t.Fatalf("state before the load = %s, want loading", kind)
		}
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })}}, background))
	turn(host)
	awaitRefState(t, "held live", held, func(s *RefState) bool { return s.Kind == RefStateLive })
	if title := memberOf(t, <-got, "title"); title != "t" {
		t.Fatalf("title = %v, want t", title)
	}
	state := awaitRefState(t, "unserved waits", unserved, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "app.page is not served here" || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed waiting: app.page is not served here", state)
	}
	loop.AdvanceAsync(50)
	wantChordError(t, <-refused, data.CodeUnavailable, "", "app.page is not served here")
}

// Mirrors "acquiring › invalid params before load resolved: hold throws and get rejects invalid/params at once (audit, 7a)".
func TestHostAcquiringInvalidParamsBeforeLoadResolvedHoldThrowsAndGetRejectsInvalidParamsAtOnce(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	if _, err := host.Hold(background, hostPage, jsonObject("id", "x"), GetOptions{}); err == nil || !strings.Contains(err.Error(), "params") {
		t.Fatalf("hold = %v, want an error naming params", err)
	}
	_, err := host.Get(background, hostPage, jsonObject("id", "x"), GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonParams, "")
	mustLoad(t)(host.Load(nil, background))
	closeHosts(t, host)
}

// Mirrors "acquiring › a hold released before load resolved makes nothing once it did".
func TestHostAcquiringAHoldReleasedBeforeLoadResolvedMakesNothingOnceItDid(t *testing.T) {
	var factories atomic.Int32
	host := newHost(t, HostOptions{OnError: noopOnError})
	ref, err := host.Hold(background, hostPage, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	ref.Release()
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return servePage(h, func() { factories.Add(1) }) })}}, background))
	turn(host)
	must(t, host.Close(background))
	if got := factories.Load(); got != 0 {
		t.Fatalf("factory runs = %d, want 0", got)
	}
}

// Mirrors "acquiring › through transient failures: hold is failed with retryAt, then loading; get waits, then rejects with the last cause".
func TestHostAcquiringThroughTransientFailuresHoldIsFailedWithRetryAtThenLoadingGetWaitsThenRejects(t *testing.T) {
	loop := newScopeLoop()
	var left atomic.Int32
	left.Store(5)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return flakySvc(h, &left, false) }, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	held, err := host.Hold(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: new(1500 * time.Millisecond)})
		got <- err
	}()
	scopeEventually(t, "both references wait", func() bool { return countBindings(host) >= 2 })
	state := awaitRefState(t, "failed transient", held, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureTransient || state.Cause.Error.Message != "app.svc(1): offline" || state.RetryAt == nil || *state.RetryAt != 1000 {
		t.Fatalf("state = %+v, want failed transient retryAt 1000", state)
	}
	loop.AdvanceAsync(1000)
	awaitRefState(t, "failed again", held, func(s *RefState) bool { return s.Kind == RefStateFailed && s.RetryAt != nil && *s.RetryAt == 3000 })
	loop.AdvanceAsync(500)
	wantChordError(t, <-got, data.CodeUnavailable, "", "app.svc(1): offline")
	// The getter released only its own reference: the hold follows the object's retries.
	loop.AdvanceAsync(1500)
	awaitRefState(t, "retryAt 7000", held, func(s *RefState) bool { return s.Kind == RefStateFailed && s.RetryAt != nil && *s.RetryAt == 7000 })
}

// Mirrors "acquiring › a broken failure: hold is failed without retryAt; get releases its reference and rejects at once".
func TestHostAcquiringABrokenFailureHoldIsFailedWithoutRetryAtGetRejectsAtOnce(t *testing.T) {
	var left atomic.Int32
	left.Store(1)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return flakySvc(h, &left, true) }, loadedOptions{})
	defer closeHosts(t, host)
	held, err := host.Hold(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	_, err = host.Get(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	wantChordError(t, err, data.CodeUnavailable, "", "app.svc(1): offline")
	state := refStateOf(held)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed broken without retryAt", state)
	}
}

// Mirrors "acquiring › a cancelled context: no effect on a hold; a get releases its reference and rejects".
func TestHostAcquiringACancelledContextNoEffectOnAHoldAGetReleasesItsReferenceAndRejects(t *testing.T) {
	gate := make(chan struct{})
	var closes, opens seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes, &opens) }, loadedOptions{})
	defer closeHosts(t, host)
	cancellable, cancel := chordctx.WithCancel(background)
	held, err := host.Hold(cancellable, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(cancellable, hostSvc, jsonObject("id", float64(2)), GetOptions{})
		got <- err
	}()
	scopeEventually(t, "the getter waits", func() bool { return countBindings(host) >= 2 })
	// Pi cancels right after `get` and still finds svc 2's late value closed, so its opener was called by then. Here the
	// activations go on over tasks of the host, so wait for both openers: only an open that started has a late value to close.
	scopeEventually(t, "both opens started", func() bool { return opens.len() == 2 })
	cancel(errors.New("user cancelled"))
	if err := <-got; err == nil || err.Error() != "user cancelled" {
		t.Fatalf("get = %v, want user cancelled", err)
	}
	close(gate)
	awaitRefState(t, "held live", held, func(s *RefState) bool { return s.Kind == RefStateLive })
	// The getter's object ended with its only reference: its late value was closed.
	scopeEventually(t, "the late value closed", func() bool { return closes.len() == 1 })
	if got := closes.all(); !reflect.DeepEqual(got, []string{"svc 2 end"}) {
		t.Fatalf("closes = %v, want [svc 2 end]", got)
	}
}

// Mirrors "acquiring › host.close: queued calls reject released, references go released, their watches end (race 10)".
func TestHostAcquiringHostCloseQueuedCallsRejectReleasedReferencesGoReleasedTheirWatchesEnd(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &seenList[string]{}) }, loadedOptions{})
	local, err := host.Hold(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	remoteHost := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(remoteHost.Load(nil, background))
	remote, err := remoteHost.Hold(background, Notes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	call := startNotesCall(t, remote, "wait")
	localWatch, err := inSection(host, func() (chord.NativeWatchHandle[*RefState], error) { return local.State().Watch(background) })
	must(t, err)
	remoteWatch, err := inSection(remoteHost, func() (chord.NativeWatchHandle[*RefState], error) { return remote.State().Watch(background) })
	must(t, err)
	must(t, host.Close(background))
	must(t, remoteHost.Close(background))
	_, err = call.wait(background)
	wantChordError(t, err, data.CodeReleased, data.ReasonClosed, "the host is closed")
	for i, ref := range []*Reference{local, remote} {
		if kind := refStateOf(ref).Kind; kind != RefStateEnded {
			t.Fatalf("state %d = %s, want ended", i, kind)
		}
	}
	for i, watch := range []chord.NativeWatchHandle[*RefState]{localWatch, remoteWatch} {
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("watch %d ended %s, want ended", i, end.Kind)
		}
	}
}

// Mirrors "connect after a drop › throws while the previous parent link is open or still closing, and takes the next once it closed (race 4)".
func TestHostConnectAfterADropThrowsWhileThePreviousParentLinkIsOpenOrStillClosing(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		queue := newHeldQueue()
		l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{Pair: queue.pair})
		defer closeHosts(t, l.Client, l.Server)
		notes, err := l.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		next := func() error {
			end, _ := link.MemoryLinkPair()
			return l.Client.Connect(end, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
		}
		// The server's end closes now, the client's a microtask later: the queue holds that microtask while the test connects.
		queue.hold()
		defer queue.release()
		l.Links[0].Close()
		if err := next(); err == nil || err.Error() != "connect: this host has an open parent link" {
			t.Fatalf("connect while closing = %v, want connect: this host has an open parent link", err)
		}
		queue.release()
		transported(l.Links[0], l.Links[1])
		if kind := refStateOf(notes).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		if err := next(); err != nil {
			t.Fatalf("connect after the close = %v, want nil", err)
		}
	})
}

// placedWorker is a worker for session S1 serving PlacedNotes, a raw peer as its child, and one as its parent when parent.
type placedWorker struct {
	Host   Host
	Child  *RawPeer
	Parent *RawPeer
	Errors *hostErrors
	ends   []*link.MemoryLink
}

func newPlacedWorker(t *testing.T, parent bool) *placedWorker {
	t.Helper()
	errs := &hostErrors{}
	address := Session.Address(jsonObject("id", "S1"))
	host := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal("S1"), OnError: errs.push})
	w := &placedWorker{Host: host, Errors: errs}
	if parent {
		a, b := link.MemoryLinkPair()
		must(t, host.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		w.Parent = newRawPeer(t, b, rawPeerOptions{})
		w.ends = append(w.ends, a, b)
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/w", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, PlacedNotes); return err })}}, background))
	a, b := link.MemoryLinkPair()
	must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	w.Child = newRawPeer(t, b, rawPeerOptions{})
	w.ends = append(w.ends, a, b)
	return w
}

// holdOfPlaced sends a hold of PlacedNotes (or typ) for session, naming the session address.
func holdOfPlaced(t testing.TB, peer *RawPeer, session, address string, typ string, hold int) {
	t.Helper()
	params, err := link.JsonPart(jsonObject("session", session))
	must(t, err)
	peer.Send(&link.HoldMessage{
		Hold: hold, Type: typ, Version: PlacedNotes.Version, Fingerprint: PlacedNotes.Fingerprint, Params: params,
		Address: link.WireAddress{Kind: "session", Params: jsonObject("id", address)},
	})
}

// wantFailedWaiting fails unless message is a `failed` of hold with a waiting cause.
func wantFailedWaiting(t testing.TB, message link.Message, hold int) *link.FailedMessage {
	t.Helper()
	failed := message.(*link.FailedMessage)
	if failed.Hold != hold || failed.Cause.Kind != "waiting" {
		t.Fatalf("failed = %+v, want hold %d waiting", failed, hold)
	}
	return failed
}

// Mirrors "where a reference or an arriving hold goes (section 1's table) › binds a hold of a type served here that names this host's address".
func TestHostWhereAHoldGoesBindsAHoldOfATypeServedHereThatNamesThisHostsAddress(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, false)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Child, "S1", "S1", PlacedNotes.Name, 0)
		if bound := w.Child.Next("bound").(*link.BoundMessage); bound.Holds[0].Hold != 0 {
			t.Fatalf("bound hold = %d, want 0", bound.Holds[0].Hold)
		}
	})
}

// Mirrors "› keeps a hold naming this host whose params the code here places elsewhere waiting: invalid/host_address (code skew, M§1)".
func TestHostWhereAHoldGoesKeepsAHoldNamingThisHostWhoseParamsTheCodeHerePlacesElsewhereWaiting(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, false)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Child, "S2", "S1", PlacedNotes.Name, 0)
		failed := wantFailedWaiting(t, w.Child.Next("failed"), 0)
		nexttest.RequireJSONEqual(t, failed.Cause.Error, jsonObject("kind", "chord", "error", jsonObject(
			"code", "invalid", "reason", "host_address", "message", "app.placed-notes: placed on session, this host is session|S1")))
		// A refused hold is kept (a publication or a retry starts it again): the next hold takes the next id.
		holdOfPlaced(t, w.Child, "S2", "S1", PlacedNotes.Name, 1)
		wantFailedWaiting(t, w.Child.Next("failed"), 1)
	})
}

// Mirrors "› places a hold by the address it names, never by the name it serves (a relay never captures it)".
func TestHostWhereAHoldGoesPlacesAHoldByTheAddressItNamesNeverByTheNameItServes(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, true)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Child, "S2", "S2", PlacedNotes.Name, 0)
		relayed := w.Parent.Next("hold").(*link.HoldMessage)
		if relayed.Type != PlacedNotes.Name || relayed.Address.Kind != "session" || !reflect.DeepEqual(relayed.Address.Params, jsonObject("id", "S2")) {
			t.Fatalf("relayed = %+v, want %s at session S2", relayed, PlacedNotes.Name)
		}
		holdOfPlaced(t, w.Child, "S1", "S2", PlacedNotes.Name, 1)
		if again := w.Parent.Next("hold").(*link.HoldMessage); again.Hold != 1 {
			t.Fatalf("second relayed hold = %d, want 1", again.Hold)
		}
	})
}

// Mirrors "› sends a reference made here whose host address is another host over the parent link, though it serves the type".
func TestHostWhereAHoldGoesSendsAReferenceMadeHereWhoseHostAddressIsAnotherHostOverTheParentLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, true)
		defer closeHosts(t, w.Host)
		elsewhere, err := w.Host.Hold(background, PlacedNotes, jsonObject("session", "S2"), GetOptions{})
		must(t, err)
		hold := w.Parent.Next("hold").(*link.HoldMessage)
		if hold.Type != PlacedNotes.Name || hold.Address.Kind != "session" || !reflect.DeepEqual(hold.Address.Params, jsonObject("id", "S2")) {
			t.Fatalf("hold = %+v, want %s at session S2", hold, PlacedNotes.Name)
		}
		if kind := refStateOf(elsewhere).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		own, err := w.Host.Get(background, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		if variant, bound, err := own.Variant(); err != nil || !bound || variant != "editor" {
			t.Fatalf("variant = %q, %v, %v; want editor", variant, bound, err)
		}
	})
}

// Mirrors "› sends a hold it cannot serve or route over the parent link, unparsed".
func TestHostWhereAHoldGoesSendsAHoldItCannotServeOrRouteOverTheParentLinkUnparsed(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, true)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Child, "S9", "S9", "app.elsewhere", 0)
		relayed := w.Parent.Next("hold").(*link.HoldMessage)
		if relayed.Type != "app.elsewhere" {
			t.Fatalf("relayed type = %s, want app.elsewhere", relayed.Type)
		}
		nexttest.RequireJSONEqual(t, partValue(t, relayed.Params), jsonObject("session", "S9"))
	})
}

// Mirrors "› never sends a hold from the parent back to it: unavailable (race 8)".
func TestHostWhereAHoldGoesNeverSendsAHoldFromTheParentBackToItUnavailable(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, true)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Parent, "S9", "S9", "app.elsewhere", 0)
		failed := w.Parent.Next("failed").(*link.FailedMessage)
		if failed.Hold != 0 || failed.Cause.Kind != "broken" {
			t.Fatalf("failed = %+v, want hold 0 broken", failed)
		}
		nexttest.RequireJSONEqual(t, failed.Cause.Error, jsonObject("kind", "chord", "error",
			jsonObject("code", "unavailable", "message", "app.elsewhere is not served here")))
		if kinds := w.Parent.Kinds(); !reflect.DeepEqual(kinds, []string{"hello"}) {
			t.Fatalf("parent kinds = %v, want [hello]", kinds)
		}
	})
}

// Mirrors "› refuses a hold it cannot serve or route without a parent link: unavailable".
func TestHostWhereAHoldGoesRefusesAHoldItCannotServeOrRouteWithoutAParentLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		w := newPlacedWorker(t, false)
		defer closeHosts(t, w.Host)
		holdOfPlaced(t, w.Child, "S9", "S9", "app.elsewhere", 0)
		failed := w.Child.Next("failed").(*link.FailedMessage)
		if failed.Cause.Kind != "broken" || failed.Cause.Error.Value("error").(*delta.JsonObject).Value("code") != "unavailable" {
			t.Fatalf("failed = %+v, want broken unavailable", failed)
		}
	})
}

// Mirrors "› fails a reference broken with what its hostAddress throws".
func TestHostWhereAHoldGoesFailsAReferenceBrokenWithWhatItsHostAddressThrows(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		throwing := mustContract(declareRemote("app.throwing", contracts.RemoteDeclaration{
			Version: 1, Variants: providerShape,
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
				return contracts.Address{}, errors.New("hostAddress threw")
			},
		}))
		client := newHost(t, HostOptions{OnError: noopOnError})
		defer closeHosts(t, client)
		mustLoad(t)(client.Load(nil, background))
		_, err := client.Get(background, throwing, jsonObject(), GetOptions{})
		wantChordError(t, err, data.CodeUnavailable, "", "app.throwing: hostAddress threw")
		held, err := client.Hold(background, throwing, jsonObject(), GetOptions{})
		must(t, err)
		if kind := refStateOf(held).Kind; kind != RefStateFailed {
			t.Fatalf("state = %s, want failed", kind)
		}
	})
}

// Mirrors "the host's store › belongs to one open host at a time (rule 7)".
func TestHostStoreBelongsToOneOpenHostAtATime(t *testing.T) {
	st := memoryStore(t)
	first := newHost(t, HostOptions{Store: st, OnError: noopOnError})
	if _, err := CreateHost(HostOptions{Store: st, OnError: noopOnError}); err == nil || !strings.Contains(err.Error(), "store: already attached to an open host") {
		t.Fatalf("second createHost = %v, want store: already attached to an open host", err)
	}
	must(t, first.Close(background))
	must(t, newHost(t, HostOptions{Store: st, OnError: noopOnError}).Close(background))
	must(t, st.Close(background))
}

// noWrite is Pi's `{ order: "write", apply: async () => [] }`.
type noWrite struct{}

func (noWrite) StageOrder() store.WriteOrder { return store.OrderWrite }
func (noWrite) Apply(*store.Step) ([]store.StoreWrite, error) {
	return nil, nil
}

// Mirrors "the host's store › reports storage_failed once and closes the host; the app still closes the store".
func TestHostStoreReportsStorageFailedOnceAndClosesTheHostTheAppStillClosesTheStore(t *testing.T) {
	st, fail, _ := faultyStore(t)
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: st, OnError: errs.push})
	mustLoad(t)(host.Load(nil, background))
	fence := store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
	// A failed `COMMIT`: an unknown outcome (D4).
	fail("COMMIT")
	for range 2 {
		_, err := st.Commit([]store.StagedWrite{noWrite{}}, fence, background)
		if chordErr, ok := data.IsChordError(err); !ok || chordErr.Reason != data.ReasonStorageFailed {
			t.Fatalf("commit = %v, want storage_failed", err)
		}
	}
	// The host closed itself; the app's own close returns that closing.
	scopeEventually(t, "the host closed itself", host.(*chordHost).closed)
	must(t, host.Close(background))
	if got := errs.All(); len(got) != 1 || got[0].Kind != ErrStorageFailed {
		t.Fatalf("errors = %+v, want one storage_failed", got)
	}
	must(t, st.Close(background))
}

// closed is `host.closing`, read in a section.
func (h *chordHost) closed() bool {
	var closed bool
	h.enter(func() { closed = h.closing() })
	return closed
}

// declareLocal is `local(name, declaration)`; an omitted `params` is `{}` (a nil slice is an absent one).
func declareLocal(name string, declaration contracts.LocalDeclaration) (*contracts.Contract, error) {
	if declaration.Params == nil {
		declaration.Params = contracts.Params{}
	}
	if declaration.Members == nil {
		declaration.Members = []contracts.LocalEntry{}
	}
	return contracts.Local(name, declaration)
}

// declareRemote is `remote(name, declaration)`; an omitted `params` is `{}`.
func declareRemote(name string, declaration contracts.RemoteDeclaration) (*contracts.Contract, error) {
	if declaration.Params == nil {
		declaration.Params = contracts.Params{}
	}
	return contracts.Remote(name, declaration)
}

// inSection runs open (a Watch of a source) in a section of host's loop, as Pi's single thread does.
func inSection[T any](host Host, open func() (T, error)) (T, error) {
	var value T
	var err error
	host.(*chordHost).enter(func() { value, err = open() })
	return value, err
}

// refStateOf is `ref.state.value`, read in a section of the reference's host.
func refStateOf(ref *Reference) *RefState {
	var current *RefState
	ref.binding.loop.Run(func() { current = ref.State().Value() })
	return current
}

// countObjects is the number of objects on host.
func countObjects(host Host) int {
	h := host.(*chordHost)
	var n int
	h.enter(func() { n = len(h.objects) })
	return n
}

// --- reload (slice 7b) ------------------------------------------------------------------------------------------------

// infoFacet is a plugin's facet serving `Info` with the title title (a new facet value each call: a changed plugin).
func infoFacet(title string) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return impl("title", constant(title)), nil
		}, ServeOptions{})
	})
}

// pageFacet is a plugin's facet serving `Page`.
func pageFacet() Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error { return servePage(h, nil) })
}

// emptyFacet is `defineFacet(() => {})`.
func emptyFacet() Facet { return DefineFacet(func(FacetHost, FacetSelf) error { return nil }) }

// Mirrors "reload: the API (slice 7b §1) › throws before load resolved and rejects released/closed after close".
func TestHostReloadTheAPIThrowsBeforeLoadResolvedAndRejectsReleasedClosedAfterClose(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	if _, err := host.Reload(nil, background, ReloadOptions{}); err == nil || err.Error() != "reload runs after load" {
		t.Fatalf("reload before load = %v, want reload runs after load", err)
	}
	mustLoad(t)(host.Load(nil, background))
	closeHosts(t, host)
	_, err := host.Reload(nil, background, ReloadOptions{})
	wantChordError(t, err, data.CodeReleased, data.ReasonClosed, "")
}

// Mirrors "reload: the API › reports plugins added, changed and removed; the same facet object is unchanged and untouched".
func TestHostReloadTheAPIReportsPluginsAddedChangedAndRemovedTheSameFacetObjectIsUnchangedAndUntouched(t *testing.T) {
	core := infoFacet("core")
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	kept := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostCounter, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			implementation := impl("count", constant(float64(1)))
			implementation.Members.Methods = map[string]contracts.MethodFunction{
				"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}
			return implementation, nil
		}, ServeOptions{})
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/core", Facet: kept}, {Name: "@app/info", Facet: core}}, background))
	counter := mustGet(t, host, hostCounter, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@app/core", Facet: kept}, {Name: "@app/page", Facet: pageFacet()}}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, report, []string{"@app/page"}, nil, []string{"@app/info"})
	if want := []ReloadedObject{{Object: "app.info", Kind: ReloadEnded}}; !reflect.DeepEqual(report.Objects, want) {
		t.Fatalf("objects = %+v, want %+v", report.Objects, want)
	}
	// Untouched: the same reference stays live on the same object.
	if state := refStateOf(counter); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	page := mustGet(t, host, hostPage, jsonObject("id", float64(3)))
	if id := memberOf(t, page, "id"); id != float64(3) {
		t.Fatalf("id = %v, want 3", id)
	}
}

// Mirrors "reload: the API › runs reloads one after another".
func TestHostReloadTheAPIRunsReloadsOneAfterAnother(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	var order seenList[string]
	staged := func(title string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			order.add(title)
			return h.Serve(hostInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return impl("title", constant(title)), nil
			}, ServeOptions{})
		})
	}
	h := host.(*chordHost)
	done := make(chan error, 2)
	go func() {
		_, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: staged("b")}}, background, ReloadOptions{})
		done <- err
	}()
	// The second call is made after the first one entered the chain.
	for {
		var queued bool
		h.enter(func() { queued = h.reloading != nil })
		if queued {
			break
		}
	}
	go func() {
		_, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: staged("c")}}, background, ReloadOptions{})
		done <- err
	}()
	must(t, <-done)
	must(t, <-done)
	if got := order.all(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("setups ran in order %v, want [b c]", got)
	}
	info := mustGet(t, host, hostInfo, jsonObject())
	if title := memberOf(t, info, "title"); title != "c" {
		t.Fatalf("title = %v, want c", title)
	}
}

// Mirrors "reload: the API › an abort before the commit rejects aborted and changes nothing".
func TestHostReloadTheAPIAnAbortBeforeTheCommitRejectsAbortedAndChangesNothing(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	aborted, cancel := chordctx.WithCancel(background)
	cancel(errors.New("stop"))
	_, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}}, aborted, ReloadOptions{})
	wantChordError(t, err, data.CodeAborted, "", "")
	info := mustGet(t, host, hostInfo, jsonObject())
	if title := memberOf(t, info, "title"); title != "a" {
		t.Fatalf("title = %v, want a", title)
	}
}

// Mirrors "reload: the API › an abort after the commit rejects aborted naming what the commit applied, which stands".
func TestHostReloadTheAPIAnAbortAfterTheCommitRejectsAbortedNamingWhatTheCommitAppliedWhichStands(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}, {Name: "@app/gone", Facet: emptyFacet()}}, background))
	info := mustGet(t, host, hostInfo, jsonObject())
	aborting, cancel := chordctx.WithCancel(background)
	// The abort lands right after the commit, while the reload waits for its report: the swapped object's new factory
	// runs after the commit's batch (its second run: the first is staging's).
	var runs atomic.Int32
	changed := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			if runs.Add(1) == 2 {
				cancel(errors.New("stop"))
			}
			return impl("title", constant("b")), nil
		}, ServeOptions{})
	})
	_, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: changed}, {Name: "@app/new", Facet: emptyFacet()}}, aborting, ReloadOptions{})
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeAborted {
		t.Fatalf("reload = %v, want aborted", err)
	}
	committed, _ := chordErr.Details.(*delta.JsonObject).Value("committed").(*delta.JsonObject)
	for name, want := range map[string][]any{"added": {"@app/new"}, "changed": {"@app/info"}, "removed": {"@app/gone"}, "failed": {}} {
		if got, _ := committed.Value(name).([]any); !reflect.DeepEqual(got, want) {
			t.Fatalf("committed.%s = %v, want %v", name, got, want)
		}
	}
	turn(host)
	if title := memberOf(t, info, "title"); title != "b" {
		t.Fatalf("title = %v, want b", title)
	}
}

// Own test (Pi's `#report` waits for no listener, and `host.reload` may be called from a watch callback): the watch
// listener of a reference of the reloaded type calls `Reload` itself, from the delivery its first reload's commit
// scheduled. A report that waited for every delivery to finish would wait for that listener, which waits for the first
// reload to end: neither returns.
func TestHostReloadAWatchListenerMayCallReloadItself(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	info := mustGet(t, host, hostInfo, jsonObject())
	watch, err := info.Member("title").Watch(background)
	must(t, err)
	type result struct {
		report ReloadReport
		err    error
	}
	inner := make(chan result, 1)
	var once sync.Once
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		once.Do(func() {
			report, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("c")}}, background, ReloadOptions{})
			inner <- result{report, err}
		})
		return nil
	})
	outer := make(chan result, 1)
	go func() {
		report, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}}, background, ReloadOptions{})
		outer <- result{report, err}
	}()
	for name, done := range map[string]chan result{"the reload": outer, "the reload the listener called": inner} {
		select {
		case got := <-done:
			must(t, got.err)
			if !reflect.DeepEqual(got.report.Plugins.Changed, []string{"@app/info"}) {
				t.Fatalf("%s: changed = %v, want [@app/info]", name, got.report.Plugins.Changed)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not return", name)
		}
	}
	turn(host)
	if title := memberOf(t, info, "title"); title != "c" {
		t.Fatalf("title = %v, want c", title)
	}
}

// Own test (Pi's `#report`: "`context`'s abort ends only this wait"): a listener of the reloaded object's reference is
// still running (busy, not waiting) when the context aborts; the reload does not wait for it to return and rejects
// `aborted` naming what the commit applied.
func TestHostReloadAnAbortEndsTheReportsWaitForAListenerStillRunning(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	info := mustGet(t, host, hostInfo, jsonObject())
	watch, err := info.Member("title").Watch(background)
	must(t, err)
	var release atomic.Bool
	defer release.Store(true)
	running := make(chan struct{})
	var once sync.Once
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Value == "b" {
			once.Do(func() { close(running) })
			for !release.Load() {
				runtime.Gosched()
			}
		}
		return nil
	})
	aborting, cancel := chordctx.WithCancel(background)
	done := make(chan error, 1)
	go func() {
		_, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}}, aborting, ReloadOptions{})
		done <- err
	}()
	<-running
	cancel(errors.New("stop"))
	select {
	case err := <-done:
		chordErr, ok := data.IsChordError(err)
		if !ok || chordErr.Code != data.CodeAborted {
			t.Fatalf("reload = %v, want aborted", err)
		}
		committed, _ := chordErr.Details.(*delta.JsonObject).Value("committed").(*delta.JsonObject)
		if got, _ := committed.Value("changed").([]any); !reflect.DeepEqual(got, []any{"@app/info"}) {
			t.Fatalf("committed.changed = %v, want [@app/info]", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the abort did not end the reload's wait for a running listener")
	}
}

// Pi's report runs the listeners of the batch the reload committed to the end of their synchronous part before it reads the
// context: a watcher of a reference that left `live` aborts the context from its callback, and the reload rejects `aborted`.
func TestHostReloadAListenerThatAbortsTheContextFromItsCallbackEndsTheReportAborted(t *testing.T) {
	// One processor: the listener yields while it works, so a report that did not wait for it would read the context first.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	info := mustGet(t, host, hostInfo, jsonObject())
	watch, err := info.Member("title").Watch(background)
	must(t, err)
	aborting, cancel := chordctx.WithCancel(background)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Value == "b" {
			// A slow synchronous part: a report that read the context before it ran would succeed.
			for range 200 {
				runtime.Gosched()
			}
			cancel(errors.New("stop"))
		}
		return nil
	})
	_, err = host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}}, aborting, ReloadOptions{})
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeAborted {
		t.Fatalf("reload = %v, want aborted", err)
	}
}

// Mirrors "reload: the API › reports a setup's or a factory's throw as failed, naming the plugin, which stays on its old code".
func TestHostReloadTheAPIReportsASetupsOrAFactorysThrowAsFailedNamingThePluginWhichStaysOnItsOldCode(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	info := mustGet(t, host, hostInfo, jsonObject())
	throwing := DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("setup threw") })
	failedOf := func(facets []HostFacet) [][2]string {
		report, err := host.Reload(facets, background, ReloadOptions{})
		must(t, err)
		return failedMessages(report.Failed)
	}
	if got, want := failedOf([]HostFacet{{Name: "@app/info", Facet: throwing}}), [][2]string{{"@app/info", `plugin "@app/info": setup threw`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	factoryThrows := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(hostInfo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("factory threw")
		}, ServeOptions{})
	})
	if got, want := failedOf([]HostFacet{{Name: "@app/info", Facet: factoryThrows}}), [][2]string{{"@app/info", `plugin "@app/info", app.info: factory threw`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	// A type another plugin serves already fails the plugin with new code; an added one is left out.
	report, err := host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}, {Name: "@app/other", Facet: infoFacet("c")}}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, report, nil, []string{"@app/info"}, nil)
	if got, want := failedMessages(report.Failed), [][2]string{{"@app/other", `plugin "@app/other": app.info is served twice (plugin "@app/info")`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	turn(host)
	if title := memberOf(t, info, "title"); title != "b" {
		t.Fatalf("title = %v, want b", title)
	}
	if state := refStateOf(info); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
}

// Mirrors "reload: the commit (slice 7b §3) › serves added types, drops removed ones and their presence lists, and wakes requests per name".
func TestHostReloadTheCommitServesAddedTypesDropsRemovedOnesAndTheirPresenceListsAndWakesRequestsPerName(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}}, background))
	page := mustHold(t, host, hostPage, jsonObject("id", float64(1)))
	other := mustHold(t, host, hostCore, jsonObject())
	turn(host)
	if state := refStateOf(page); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("page state = %+v, want failed waiting", state)
	}
	presence := host.References(hostInfo)
	watch, err := refStateWatch(other, background)
	must(t, err)
	otherStates := statesOf(t, watch)
	_, err = host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}, {Name: "@app/page", Facet: pageFacet()}}, background, ReloadOptions{})
	must(t, err)
	turn(host)
	if state := refStateOf(page); state.Kind != RefStateLive {
		t.Fatalf("page state = %+v, want live", state)
	}
	if id := memberOf(t, page, "id"); id != float64(1) {
		t.Fatalf("id = %v, want 1", id)
	}
	// Not a name the reload serves: still waiting, not checked again.
	if state := refStateOf(other); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("other state = %+v, want failed waiting", state)
	}
	if got := otherStates.All(); len(got) != 0 {
		t.Fatalf("other states = %v, want none", got)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/page", Facet: pageFacet()}}, background, ReloadOptions{})
	must(t, err)
	if host.References(hostInfo) == presence {
		t.Fatal("the removed type's presence list was kept")
	}
}

// namesProvider is the facet of `@app/names` of the registry cases: it serves Names with a compose joining the table with
// separator and contributes "own".
func namesProvider(names registries.Registry[string, string], separator string) Facet {
	return DefineFacet(func(h FacetHost, self FacetSelf) error {
		provider := registries.RegistryProvider[string, string]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (string, error) {
				values := make([]string, 0, len(table))
				for _, entry := range table {
					values = append(values, entry.Value)
				}
				return strings.Join(values, separator), nil
			},
		}
		if err := h.Serve(registries.Registry[any, any](names), provider.Erase(), ServeOptions{}); err != nil {
			return err
		}
		return self.Contribute(registries.Registry[any, any](names), "own")
	})
}

// contributorFacet is a facet contributing name to names.
func contributorFacet(names registries.Registry[string, string], name string) Facet {
	return DefineFacet(func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](names), name)
	})
}

// Mirrors "reload: the commit › replaces a reloaded registry's provider and its plugin's contributions; a removed provider's readers wait".
func TestHostReloadTheCommitReplacesAReloadedRegistrysProviderAndItsPluginsContributionsARemovedProvidersReadersWait(t *testing.T) {
	names, err := registries.DefineRegistry[string, string]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/names", Facet: namesProvider(names, ",")}, {Name: "@app/x", Facet: contributorFacet(names, "x")}}, background))
	value, err := host.Registry(background, registries.Registry[any, any](names), GetOptions{})
	must(t, err)
	current := func() (any, error) {
		var got any
		var err error
		host.(*chordHost).enter(func() { got, err = value.Current() })
		return got, err
	}
	if got, err := current(); err != nil || got != "own,x" {
		t.Fatalf("names = %v, %v; want own,x", got, err)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/names", Facet: namesProvider(names, "+")}, {Name: "@app/x", Facet: contributorFacet(names, "y")}}, background, ReloadOptions{})
	must(t, err)
	if got, err := current(); err != nil || got != "own+y" {
		t.Fatalf("names = %v, %v; want own+y", got, err)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/x", Facet: contributorFacet(names, "z")}}, background, ReloadOptions{})
	must(t, err)
	if _, err := current(); err == nil || !strings.Contains(err.Error(), "no plugin serves it here") {
		t.Fatalf("names after the provider's removal = %v, want no plugin serves it here", err)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/names", Facet: namesProvider(names, ";")}, {Name: "@app/x", Facet: contributorFacet(names, "z")}}, background, ReloadOptions{})
	must(t, err)
	if got, err := current(); err != nil || got != "own;z" {
		t.Fatalf("names = %v, %v; want own;z", got, err)
	}
}

// Mirrors "reload: registries' older readers (slice 7b §3) › a reader of an older version follows the new provider's accepts[old].value; one it no longer serves waits".
func TestHostReloadRegistriesOlderReadersAReaderOfAnOlderVersionFollowsTheNewProvidersAcceptsOldValueOneItNoLongerServesWaits(t *testing.T) {
	namesV1, err := registries.DefineRegistry[string, string]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	namesV2, err := registries.DefineRegistry[string, []string]("app.names", registries.DefineRegistryOptions{Version: 2})
	must(t, err)
	provider := func(mapped func(names []string) string) Facet {
		return DefineFacet(func(h FacetHost, self FacetSelf) error {
			served := registries.RegistryProvider[string, []string]{
				Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) ([]string, error) {
					values := make([]string, 0, len(table))
					for _, entry := range table {
						values = append(values, entry.Value)
					}
					return values, nil
				},
			}
			if mapped != nil {
				served.Accepts = registries.RegistryAccepts[string, []string]{1: {Value: func(current []string) (any, error) { return mapped(current), nil }}}
			}
			if err := h.Serve(registries.Registry[any, any](namesV2), served.Erase(), ServeOptions{}); err != nil {
				return err
			}
			return self.Contribute(registries.Registry[any, any](namesV2), "a")
		})
	}
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/names", Facet: provider(func(names []string) string { return strings.Join(names, ",") })}}, background))
	old, err := host.Registry(background, registries.Registry[any, any](namesV1), GetOptions{})
	must(t, err)
	current := func() (any, error) {
		var got any
		var err error
		host.(*chordHost).enter(func() { got, err = old.Current() })
		return got, err
	}
	if got, err := current(); err != nil || got != "a" {
		t.Fatalf("old = %v, %v; want a", got, err)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/names", Facet: provider(func(names []string) string { return strings.Join(names, "+") + "!" })}}, background, ReloadOptions{})
	must(t, err)
	if got, err := current(); err != nil || got != "a!" {
		t.Fatalf("old = %v, %v; want a!", got, err)
	}
	_, err = host.Reload([]HostFacet{{Name: "@app/names", Facet: provider(nil)}}, background, ReloadOptions{})
	must(t, err)
	if _, err := current(); err == nil || !strings.Contains(err.Error(), "version 1 is not served here") {
		t.Fatalf("old after the adapter's removal = %v, want version 1 is not served here", err)
	}
}

// reloadTitle is `Title`: a worker's title per session (v1: a string).
var reloadTitle = mustContract(declareRemote("app.title", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		session, _ := params.Value("session").(string)
		return Session.Address(jsonObject("id", session)), nil
	},
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("title", contracts.Str())}})},
}))

// reloadTitleV2 is `TitleV2`: an object; v1 consumers map back through `accepts`.
var reloadTitleV2 = func() *contracts.Contract {
	evolved, err := contracts.Evolve(reloadTitle, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("v", "title", contracts.Obj(contracts.Prop("text", contracts.Str())))
	})
	if err != nil {
		panic(err)
	}
	return evolved
}()

// Mirrors "reload across hosts, in the app's order (slice 7b §11) › server, then worker, then tab: an old tab is mapped, then waits for its version, then reloads its own code".
func TestHostReloadAcrossHostsInTheAppsOrderServerThenWorkerThenTabAnOldTabIsMappedThenWaitsForItsVersionThenReloadsItsOwnCode(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		serveV1 := func(h FacetHost) error {
			return h.Serve(reloadTitle, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
				implementation := impl("title", constant("one"))
				implementation.Variant = "v"
				return implementation, nil
			}, ServeOptions{})
		}
		serveV2 := func(text string, accept bool) func(h FacetHost) error {
			return func(h FacetHost) error {
				options := ServeOptions{}
				if accept {
					title := contracts.StateMapping{Func: func(current delta.JsonValue) (delta.JsonValue, error) {
						return current.(*delta.JsonObject).Value("text"), nil
					}}
					variant := []contracts.Entry[contracts.StateMapping]{{Name: "title", Value: title}}
					options.Accepts = contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
						VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "v", Value: variant}},
					}}}
				}
				return h.Serve(reloadTitleV2, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
					implementation := impl("title", constant(jsonObject("text", text)))
					implementation.Variant = "v"
					return implementation, nil
				}, options)
			}
		}
		workerSet := func(setup func(h FacetHost) error) []HostFacet {
			return []HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
				if _, err := serveNotes(h, PlacedNotes); err != nil {
					return err
				}
				return setup(h)
			})}}
		}
		r := relay(t, relayedOptions{Worker: serveV1})
		defer closeHosts(t, r.Server)
		old := mustHold(t, r.Tab, reloadTitle, jsonObject("session", "S1"))
		titleIs := func(ref *Reference, want string) func() bool {
			return func() bool {
				r.Transported()
				title, err := ref.Member("title").Value()
				if err != nil {
					return false
				}
				if object, ok := title.(*delta.JsonObject); ok {
					title = object.Value("text")
				}
				return title == want
			}
		}
		scopeEventually(t, "the old tab reads one", titleIs(old, "one"))
		stored, _ := r.Workers.Load("session|S1")
		worker := stored.(Host)
		// The worker's `host.load` is not awaited by the relay, and `reload` runs after load: the old tab reading one does
		// not mean the worker's load returned.
		r.WorkerLoaded(t, "session|S1")
		// The server installs first: nothing of Title changes there.
		_, err := r.Server.Reload([]HostFacet{{Name: "@test/server", Facet: emptyFacet()}}, background, ReloadOptions{})
		must(t, err)
		// The worker installs v2 accepting v1: the old tab is mapped.
		_, err = worker.Reload(workerSet(serveV2("two", true)), background, ReloadOptions{})
		must(t, err)
		scopeEventually(t, "the old tab reads two through the mapping", titleIs(old, "two"))
		if state := refStateOf(old); state.Kind != RefStateLive {
			t.Fatalf("old state = %+v, want live", state)
		}
		// A worker install without the adapter: the old tab waits for its version (a remote failure is its reads' error).
		_, err = worker.Reload(workerSet(serveV2("three", false)), background, ReloadOptions{})
		must(t, err)
		scopeEventually(t, "the old tab waits for its version", func() bool {
			r.Transported()
			state := refStateOf(old)
			return state.Kind == RefStateFailed && state.Cause.Kind == data.FailureWaiting && state.Cause.Error.Code == data.CodeContractMismatch
		})
		// The tab reloads its own code: its new hold (v2) binds; the old one is released.
		fresh := mustHold(t, r.Tab, reloadTitleV2, jsonObject("session", "S1"))
		old.Release()
		scopeEventually(t, "the new hold reads three", titleIs(fresh, "three"))
	})
}

// Mirrors "reload: an abort while staging (fix round 1) › does not commit when the context is aborted by the synchronous setup".
func TestHostReloadAnAbortWhileStagingDoesNotCommitWhenTheContextIsAbortedByTheSynchronousSetup(t *testing.T) {
	typ := mustContract(declareLocal("astra.cancel", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	serve := func(n float64, before func()) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if before != nil {
				before()
			}
			return h.Serve(typ, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return impl("n", constant(n)), nil }, ServeOptions{})
		})
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/cancel", Facet: serve(1, nil)}}, background))
	ref := mustGet(t, host, typ, jsonObject())
	aborting, cancel := chordctx.WithCancel(background)
	_, _ = host.Reload([]HostFacet{{Name: "@astra/cancel", Facet: serve(2, func() { cancel(errors.New("abort")) })}}, aborting, ReloadOptions{})
	turn(host)
	actual := memberOf(t, ref, "n")
	ref.Release()
	if actual != float64(1) {
		t.Fatalf("n = %v, want 1", actual)
	}
}

// Own test of host.ts's wiring (`new Batch((error) => this.report(error))` and `new RegistryRecord(name, batch, (error) =>
// this.report(error))`): a watch listener that fails is reported to `onError` as the batch's `listener_error`, and a
// registry whose `compose` fails as the registry's `registry_failed`, each with its own error and fields, once.
func TestHostReportsABatchAndARegistryFailureToOnErrorWithTheirOwnFields(t *testing.T) {
	boom := errors.New("listener failed")
	composeBoom := errors.New("compose failed")
	names, err := registries.DefineRegistry[string, string]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: errs.push})
	defer closeHosts(t, host)
	registry := DefineFacet(func(h FacetHost, self FacetSelf) error {
		failing := registries.RegistryProvider[string, string]{
			Compose: func(registries.RegistryTable[string], registries.ComposeContext) (string, error) {
				return "", composeBoom
			},
		}
		if err := h.Serve(registries.Registry[any, any](names), failing.Erase(), ServeOptions{}); err != nil {
			return err
		}
		return self.Contribute(registries.Registry[any, any](names), "own")
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/info", Facet: infoFacet("a")}, {Name: "@app/names", Facet: registry}}, background))

	info := mustGet(t, host, hostInfo, jsonObject())
	watch, err := info.Member("title").Watch(background)
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return boom })
	// The reload changes the title: its frame is the one the failing listener gets.
	_, err = host.Reload([]HostFacet{{Name: "@app/info", Facet: infoFacet("b")}, {Name: "@app/names", Facet: registry}}, background, ReloadOptions{})
	must(t, err)
	_, _ = host.Registry(background, registries.Registry[any, any](names), GetOptions{})

	eventually(t, "a listener_error and a registry_failed are reported", func() bool {
		var kinds []HostErrorKind
		for _, reported := range errs.All() {
			kinds = append(kinds, reported.Kind)
		}
		return slices.Contains(kinds, HostErrorKind(state.BatchListenerError)) && slices.Contains(kinds, HostErrorKind(registries.ErrorRegistryFailed))
	})
	for _, reported := range errs.All() {
		switch reported.Kind {
		case HostErrorKind(state.BatchListenerError):
			if reported.Batch == nil || reported.Batch.Kind != state.BatchListenerError || !errors.Is(reported.Error, boom) || !errors.Is(reported.Batch.Error, boom) {
				t.Fatalf("listener_error = %+v, want the batch's report of %v", reported, boom)
			}
		case HostErrorKind(registries.ErrorRegistryFailed):
			if reported.Registry == nil || reported.Registry.Registry != "app.names" || !errors.Is(reported.Error, composeBoom) {
				t.Fatalf("registry_failed = %+v, want app.names failing with %v", reported, composeBoom)
			}
		default:
			t.Fatalf("unexpected report %+v", reported)
		}
	}
}
