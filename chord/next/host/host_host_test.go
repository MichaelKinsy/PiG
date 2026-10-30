package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
func gatedSvc(h FacetHost, gate <-chan struct{}, closes *seenList[string]) error {
	return h.Serve(hostSvc, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		id := params.Value("id")
		self.Open(func(context.Context) (any, error) { <-gate; return fmt.Sprintf("svc %v", id), nil },
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
	go func() { done <- host.Load(facets, background) }()
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
	must(t, host.Load([]HostFacet{{Name: "@app/info", Facet: facet}}, background))
	if info.Load() != 1 || page.Load() != 0 {
		t.Fatalf("factories: info %d, page %d; want 1 and 0", info.Load(), page.Load())
	}
	closeHosts(t, host)
}

// Mirrors "load › refuses invalid and duplicate plugin names, double serves and a second load".
func TestHostLoadRefusesInvalidAndDuplicatePluginNamesDoubleServesAndASecondLoad(t *testing.T) {
	empty := DefineFacet(func(FacetHost, FacetSelf) error { return nil })
	info := DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })
	for _, c := range []struct {
		facets []HostFacet
		want   string
	}{
		{[]HostFacet{{Name: "Bad Name", Facet: empty}}, `"Bad Name" is not a valid plugin name`},
		{[]HostFacet{{Name: "a", Facet: empty}, {Name: "a", Facet: empty}}, `plugin "a" is loaded twice`},
		{[]HostFacet{{Name: "a", Facet: info}, {Name: "b", Facet: info}}, "app.info is served twice"},
	} {
		if err := newHost(t, HostOptions{OnError: noopOnError}).Load(c.facets, background); err == nil || err.Error() != c.want {
			t.Fatalf("load = %v, want %q", err, c.want)
		}
	}
	host := newHost(t, HostOptions{OnError: noopOnError})
	must(t, host.Load(nil, background))
	if err := host.Load(nil, background); err == nil || err.Error() != "load runs once per host" {
		t.Fatalf("second load = %v, want load runs once per host", err)
	}
}

// Mirrors "load › makes a get before it resolves wait for it".
func TestHostLoadMakesAGetBeforeItResolvesWaitForIt(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(hostInfo, jsonObject(), background, GetOptions{})
		must(t, err)
		got <- ref
	}()
	awaitUnbound(t, host)
	must(t, host.Load([]HostFacet{{Name: "a", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })}}, background))
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
		ref, err := host.Get(hostCounter, jsonObject(), background, GetOptions{})
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
	ref, err := host.Hold(hostCodeMode, jsonObject(), background, GetOptions{})
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
	ref, err := host.Hold(hostCodeMode, jsonObject(), background, GetOptions{})
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
	must(t, host.Load([]HostFacet{{Name: "@app/codemode", Facet: facet}}, background))
	if held := parent.Next("hold").(*link.HoldMessage); held.Type != Notes.Name {
		t.Fatalf("hold type = %s, want %s", held.Type, Notes.Name)
	}
	ref, err := host.Hold(hostCodeMode, jsonObject(), background, GetOptions{})
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
	middle, err := host.Hold(hostMiddle, jsonObject("id", float64(1)), background, GetOptions{})
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
	bootstrap, err := r.Server.Hold(hostBootstrap, jsonObject(), background, GetOptions{})
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
	mine, err := worker.Hold(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	transported(a, b, c, d)
	if kind := refStateOf(mine).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
	must(t, worker.Load([]HostFacet{{Name: "@test/w", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, PlacedNotes); return err })}}, background))
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
	view, err := r.Tab.Hold(chat, jsonObject("session", "S1"), background, GetOptions{})
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
	_, err := host.Get(hostPage, jsonObject("id", float64(1)), background, quick)
	wantChordError(t, err, data.CodeUnavailable, "", "app.page is not served here")
	infoV2 := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 2, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))
	changed := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Int())}}))
	for _, typ := range []*contracts.Contract{infoV2, changed} {
		_, err := host.Get(typ, jsonObject(), background, quick)
		wantChordError(t, err, data.CodeContractMismatch, "", "")
	}
	same := mustContract(declareLocal("app.info", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())}}))
	ref, err := host.Get(same, jsonObject(), background, GetOptions{})
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
	_, err := host.Get(hostPage, jsonObject("id", "x"), background, GetOptions{})
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
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(500 * time.Millisecond)})
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
		_, err := host.Get(hostInfo, jsonObject(), background, GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
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
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
		early <- err
	}()
	go func() {
		ref, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(time.Second)})
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
func TestHostCloseEndsEveryObjectTheLastToGoLiveFirstAndRejectsNewCalls(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveInfoFactory(h, nil); err != nil {
			return err
		}
		return h.Serve(hostOuter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsAs(self, hostInfo, jsonObject(), "title", "title"), nil
		}, ServeOptions{})
	}, loadedOptions{})
	var ends seenList[string]
	outer, err := host.Get(hostOuter, jsonObject(), background, GetOptions{})
	must(t, err)
	info, err := host.Get(hostInfo, jsonObject(), background, GetOptions{})
	must(t, err)
	for name, ref := range map[string]*Reference{"outer": outer, "info": info} {
		watch, err := ref.Member("title").Watch(background)
		must(t, err)
		go func() { <-watch.Closed(); ends.add(name) }()
	}
	must(t, host.Close(background))
	scopeEventually(t, "both watches ended", func() bool { return ends.len() == 2 })
	if got := ends.all(); !reflect.DeepEqual(got, []string{"outer", "info"}) {
		t.Fatalf("ends = %v, want [outer info]", got)
	}
	_, err = host.Get(hostInfo, jsonObject(), background, GetOptions{})
	wantChordError(t, err, data.CodeReleased, data.ReasonClosed, "the host is closed")
	if _, err := info.Member("title").Value(); err == nil || err.Error() != "app.info: ended" {
		t.Fatalf("a read after the close = %v, want app.info: ended", err)
	}
}

// Mirrors "close › fails a get waiting for load".
func TestHostCloseFailsAGetWaitingForLoad(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	got := make(chan error, 1)
	go func() { _, err := host.Get(hostInfo, jsonObject(), background, GetOptions{}); got <- err }()
	awaitUnbound(t, host)
	must(t, host.Close(background))
	wantChordError(t, <-got, data.CodeReleased, data.ReasonClosed, "the host is closed")
}

// Mirrors "close › ends every object at once: calls fail released before any close ran".
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
	counter, err := host.Get(hostCounter, jsonObject(), background, GetOptions{})
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
	var closes seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes) }, loadedOptions{})
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
		got <- err
	}()
	awaitUnboundOrObject(t, host)
	turn(host)
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
	ref, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
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
		_, err := host.Get(view, jsonObject("id", id), background, GetOptions{})
		must(t, err)
	}
	must(t, host.Close(background))
	want := []string{"view 2 closing", "view 2 closed", "svc 2 closing", "svc 2 closed", "view 1 closing", "view 1 closed", "svc 1 closing", "svc 1 closed"}
	if got := events.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Mirrors "close, its order and its races › returns one closing to every caller: a second close waits for the first".
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
	svc, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	go func() { _, _ = host.Get(pair, jsonObject("id", float64(1)), background, GetOptions{}) }()
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
	must(t, host.Load([]HostFacet{{Name: "a", Facet: facet}}, background))
	// The background end reports its throwing close; the next instance waits for that teardown.
	first, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	first.Release()
	again, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	if n := memberOf(t, again, "n"); n != float64(1) {
		t.Fatalf("n = %v, want 1", n)
	}
	_, err = host.Get(hostSvc, jsonObject("id", float64(2)), background, GetOptions{})
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
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(100 * time.Millisecond)})
		first <- err
	}()
	awaitUnboundOrObject(t, host)
	turn(host)
	loop.AdvanceAsync(100)
	<-first
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
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
			ref, err := host.Get(hostPage, jsonObject("id", float64(1)), ctx, GetOptions{})
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
	_, err := host.Get(hostPage, jsonObject("id", float64(1)), background, GetOptions{})
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
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str()))},
	}))
	principalGate = mustContract(declareRemote("app.gate", contracts.RemoteDeclaration{
		Version:     1,
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
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
		ref, err := l.Server.Get(principalWho, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		if who := memberOf(t, ref, "who"); who != "server" {
			t.Fatalf("who = %v, want server", who)
		}
		client := newHost(t, HostOptions{OnError: noopOnError})
		err = client.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&seenList[string]{})(h) })}}, background)
		if err == nil || err.Error() != "app.who: a host without a principal serves only local types" {
			t.Fatalf("load = %v, want a host without a principal serves only local types", err)
		}
	})
}

// Mirrors "› a host without an address serves no remote type, whatever its principal".
func TestHostPrincipalsAHostWithoutAnAddressServesNoRemoteType(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: noopOnError})
		err := host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&seenList[string]{})(h) })}}, background)
		if err == nil || err.Error() != "app.who: a host without an address serves only local types" {
			t.Fatalf("load = %v, want a host without an address serves only local types", err)
		}
	})
}

// Mirrors "› ctx.get acts as ctx.principal: a guest's call gets the guest's view; a local handler's acts as the host".
func TestHostPrincipalsCtxGetActsAsCtxPrincipal(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		l := linked(t, serveAll(&seenList[string]{}), linkedOptions{Child: guestPrincipal("S1")})
		defer closeHosts(t, l.Client, l.Server)
		gate, err := l.Client.Get(principalGate, jsonObject(), background, GetOptions{})
		must(t, err)
		if who, err := gate.Call("who", nil, background); err != nil || who != "guest" {
			t.Fatalf("gate.who = %v, %v; want guest", who, err)
		}
		desk, err := l.Server.Get(principalDesk, jsonObject(), background, GetOptions{})
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
		outer, err := l.Client.Get(principalOuter, jsonObject(), background, GetOptions{})
		must(t, err)
		if who := memberOf(t, outer, "who"); who != "guest" {
			t.Fatalf("outer.who = %v, want guest", who)
		}
		inner, err := l.Server.Get(principalInner, jsonObject("id", float64(4)), background, GetOptions{})
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
		must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAll(&factories)(h) })}}, background))
		a, b := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: guestPrincipal("S2"), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{})
		_, err := host.Get(principalWho, jsonObject("id", float64(1)), background, GetOptions{})
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
		first, err := l.Server.Get(empty, jsonObject(), background, GetOptions{})
		must(t, err)
		first.Release()
		turn(l.Server)
		_, err = l.Server.Get(empty, jsonObject(), background, GetOptions{})
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
		inner, err := l.Client.Hold(principalInner, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		if state := refStateOf(inner); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
			t.Fatalf("state = %+v, want failed waiting", state)
		}
		_, err = l.Client.Get(principalInner, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(5 * time.Millisecond)})
		wantChordError(t, err, data.CodeUnavailable, "", "app.inner is not served here")
		alone := newHost(t, HostOptions{OnError: noopOnError})
		defer closeHosts(t, alone)
		must(t, alone.Load(nil, background))
		held, err := alone.Hold(principalWho, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		if kind := refStateOf(held).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		_, err = alone.Get(principalWho, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(10 * time.Millisecond)})
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
		who, err := l.Client.Get(principalWho, jsonObject("id", float64(1)), background, GetOptions{})
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
	must(t, host.Load(nil, background))
	outcome := make(chan error, 1)
	go func() {
		_, err := host.Registry(absentRegistry, background, GetOptions{ReadyTimeout: new(time.Minute)})
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
		_, err := host.Registry(absentRegistry, background, GetOptions{ReadyTimeout: new(time.Minute)})
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
	must(t, host.Load([]HostFacet{{Name: "@app/provider", Facet: facet}}, background))
	ref, err := host.Hold(consumer, jsonObject(), background, GetOptions{})
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
	must(t, server.Load([]HostFacet{{Name: "@app/provider", Facet: facet}}, background))
	must(t, client.Load(nil, background))
	down, up := link.MemoryLinkPair()
	must(t, server.Accept(down, LinkOptions{Principal: serverPrincipal()}, background))
	must(t, client.Connect(up, LinkOptions{Principal: serverPrincipal()}, background))
	held, err := client.Hold(consumer, jsonObject(), background, GetOptions{})
	must(t, err)
	transported(down, up)
	state := awaitRefState(t, "broken", held, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureBroken || state.Cause.Error.Message != "app.remote: provider address bug" {
		t.Fatalf("state = %+v, want failed broken app.remote: provider address bug", state)
	}
	// The same throw for a request made on the provider's host fails it broken too.
	here, err := server.Hold(consumer, jsonObject(), background, GetOptions{})
	must(t, err)
	if state := refStateOf(here); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", state)
	}
}

// Mirrors "a load whose setup throws › closes the host as close does, links included, and rejects with the setup's error".
func TestHostALoadWhoseSetupThrowsClosesTheHostAsCloseDoesLinksIncluded(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: noopOnError})
		a, b := link.MemoryLinkPair()
		c, d := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, host.Connect(c, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		child := newRawPeer(t, b, rawPeerOptions{})
		parent := newRawPeer(t, d, rawPeerOptions{})
		facet := DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("setup threw") })
		if err := host.Load([]HostFacet{{Name: "@test/app", Facet: facet}}, background); err == nil || err.Error() != "setup threw" {
			t.Fatalf("load = %v, want setup threw", err)
		}
		transported(a, b, c, d)
		if got := []bool{child.Closed(), parent.Closed()}; !reflect.DeepEqual(got, []bool{true, true}) {
			t.Fatalf("closed = %v, want [true true]", got)
		}
		_, err := host.Get(hostPage, jsonObject("id", float64(1)), background, GetOptions{})
		wantChordError(t, err, data.CodeReleased, data.ReasonClosed, "the host is closed")
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
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, nil); return err })}}, background))
	if _, err := host.Hold(Notes, jsonObject("session", float64(1)), background, GetOptions{}); err == nil || !strings.Contains(err.Error(), "params") {
		t.Fatalf("hold = %v, want an error naming params", err)
	}
	_, err := host.Get(Notes, jsonObject("session", float64(1)), background, GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonParams, "")
	held, err := host.Hold(hostInfo, jsonObject(), background, GetOptions{})
	must(t, err)
	state := refStateOf(held)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != "app.info is not served here" || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed waiting: app.info is not served here", state)
	}
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(hostInfo, jsonObject(), background, GetOptions{ReadyTimeout: new(5 * time.Millisecond)})
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
	held, err := host.Hold(hostInfo, jsonObject(), background, GetOptions{})
	must(t, err)
	unserved, err := host.Hold(hostPage, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	got := make(chan *Reference, 1)
	go func() {
		ref, err := host.Get(hostInfo, jsonObject(), background, GetOptions{})
		must(t, err)
		got <- ref
	}()
	refused := make(chan error, 1)
	go func() {
		_, err := host.Get(hostPage, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(50 * time.Millisecond)})
		refused <- err
	}()
	scopeEventually(t, "both getters wait", func() bool { return len(indexedBindings(host)) >= 4 })
	for _, ref := range []*Reference{held, unserved} {
		if kind := refStateOf(ref).Kind; kind != RefStateLoading {
			t.Fatalf("state before the load = %s, want loading", kind)
		}
	}
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveInfoFactory(h, nil) })}}, background))
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
	if _, err := host.Hold(hostPage, jsonObject("id", "x"), background, GetOptions{}); err == nil || !strings.Contains(err.Error(), "params") {
		t.Fatalf("hold = %v, want an error naming params", err)
	}
	_, err := host.Get(hostPage, jsonObject("id", "x"), background, GetOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonParams, "")
	must(t, host.Load(nil, background))
	closeHosts(t, host)
}

// Mirrors "acquiring › a hold released before load resolved makes nothing once it did".
func TestHostAcquiringAHoldReleasedBeforeLoadResolvedMakesNothingOnceItDid(t *testing.T) {
	var factories atomic.Int32
	host := newHost(t, HostOptions{OnError: noopOnError})
	ref, err := host.Hold(hostPage, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	ref.Release()
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return servePage(h, func() { factories.Add(1) }) })}}, background))
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
	held, err := host.Hold(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: new(1500 * time.Millisecond)})
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
	held, err := host.Hold(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	_, err = host.Get(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	wantChordError(t, err, data.CodeUnavailable, "", "app.svc(1): offline")
	state := refStateOf(held)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.RetryAt != nil {
		t.Fatalf("state = %+v, want failed broken without retryAt", state)
	}
}

// Mirrors "acquiring › a cancelled context: no effect on a hold; a get releases its reference and rejects".
func TestHostAcquiringACancelledContextNoEffectOnAHoldAGetReleasesItsReferenceAndRejects(t *testing.T) {
	gate := make(chan struct{})
	var closes seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes) }, loadedOptions{})
	defer closeHosts(t, host)
	cancellable, cancel := chordctx.WithCancel(background)
	held, err := host.Hold(hostSvc, jsonObject("id", float64(1)), cancellable, GetOptions{})
	must(t, err)
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(hostSvc, jsonObject("id", float64(2)), cancellable, GetOptions{})
		got <- err
	}()
	scopeEventually(t, "the getter waits", func() bool { return countBindings(host) >= 2 })
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
	local, err := host.Hold(hostSvc, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	remoteHost := newHost(t, HostOptions{OnError: noopOnError})
	must(t, remoteHost.Load(nil, background))
	remote, err := remoteHost.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
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
		l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
		defer closeHosts(t, l.Client, l.Server)
		notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		next := func() error {
			end, _ := link.MemoryLinkPair()
			return l.Client.Connect(end, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background)
		}
		// The server's end closes now, the client's a microtask later.
		l.Links[0].Close()
		if err := next(); err == nil || err.Error() != "connect: this host has an open parent link" {
			t.Fatalf("connect while closing = %v, want connect: this host has an open parent link", err)
		}
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
	must(t, host.Load([]HostFacet{{Name: "@test/w", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, PlacedNotes); return err })}}, background))
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
		elsewhere, err := w.Host.Hold(PlacedNotes, jsonObject("session", "S2"), background, GetOptions{})
		must(t, err)
		hold := w.Parent.Next("hold").(*link.HoldMessage)
		if hold.Type != PlacedNotes.Name || hold.Address.Kind != "session" || !reflect.DeepEqual(hold.Address.Params, jsonObject("id", "S2")) {
			t.Fatalf("hold = %+v, want %s at session S2", hold, PlacedNotes.Name)
		}
		if kind := refStateOf(elsewhere).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		own, err := w.Host.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
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
		must(t, client.Load(nil, background))
		_, err := client.Get(throwing, jsonObject(), background, GetOptions{})
		wantChordError(t, err, data.CodeUnavailable, "", "app.throwing: hostAddress threw")
		held, err := client.Hold(throwing, jsonObject(), background, GetOptions{})
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
	must(t, host.Load(nil, background))
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
