package host

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/host/reference.test.ts: references (§7): hydrated when got, released once, cycles (Q5),
// GC, release, remote references and keyed state members (slice 6a §12). The type-level cases (`expectTypeOf`) are
// designed out to Go tests that name the mechanism: Reference.Variant reports bound and the variant.

var (
	refDoc     = kmDefine("doc", documents.StateOptions{})
	refCounter = mustContract(contracts.Local("app.counter", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("doc", kmSchema), contracts.Mem("bump", contracts.NewMethod())},
	}))
	refViewer = mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
)

func refConstant(value any) chord.Watchable[delta.JsonValue] { return Expose(state.NewConstant(value)) }

// refServeCounter is Pi's `serveCounter`: one document, exposed, and a method that increments it.
func refServeCounter(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
	doc := self.State(refDoc).(*documents.Singleton)
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"doc": Expose(doc)},
		Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			_, err := doc.Change(func(draft *delta.Object) (any, error) {
				return nil, draft.Set("n", draft.Get("n").(float64)+1)
			}, ctx)
			return nil, err
		}},
	}}, nil
}

func refLoadCounter(t *testing.T) Host {
	t.Helper()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return h.Serve(refCounter, refServeCounter, ServeOptions{}) }, loadedOptions{})
	return host
}

func refGet(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	ref, err := host.Get(typ, params, background, GetOptions{})
	must(t, err)
	return ref
}

// refReason is `code/reason` of a rejection, `undefined` for no reason.
func refReason(err error) string {
	chordErr, ok := data.IsChordError(err)
	if !ok {
		return fmt.Sprintf("not a ChordError: %v", err)
	}
	reason := string(chordErr.Reason)
	if reason == "" {
		reason = "undefined"
	}
	return string(chordErr.Code) + "/" + reason
}

func refKind(ref *Reference) RefStateKind { return ref.State().Value().Kind }

// settledHeap is the live heap after collections have run.
func settledHeap() uint64 {
	var stats runtime.MemStats
	for range 4 {
		runtime.GC()
	}
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// reference.test.ts › references › are hydrated when got; their state members are watchables.
func TestReferencesAreHydratedWhenGotTheirStateMembersAreWatchables(t *testing.T) {
	host := refLoadCounter(t)
	counter := refGet(t, host, refCounter, jsonObject())
	nexttest.RequireJSONEqual(t, kmMember(t, counter, "doc"), kmN(0))
	watch, err := counter.Member("doc").Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	_, err = counter.Call("bump", nil, background)
	must(t, err)
	deliveredOn(host)
	got := frames.Items()
	nexttest.RequireJSONEqual(t, valuesOfFrames(got), []any{kmN(1)})
	nexttest.RequireJSONEqual(t, opsOf(got), []any{[]any{[]any{"s", []any{"n"}, float64(1)}}})
	nexttest.RequireJSONEqual(t, kmMember(t, counter, "doc"), kmN(1))
}

// reference.test.ts › references › throw released on every use after release, which is idempotent.
func TestReferencesThrowReleasedOnEveryUseAfterReleaseWhichIsIdempotent(t *testing.T) {
	host := refLoadCounter(t)
	counter := refGet(t, host, refCounter, jsonObject())
	counter.Release()
	counter.Release()
	released := map[string]any{"code": "released", "message": "app.counter: released"}
	_, err := counter.Member("doc").Value()
	if err == nil || err.Error() != "app.counter: released" {
		t.Fatalf("doc.value = %v, want app.counter: released", err)
	}
	_, err = counter.Member("doc").Watch(background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	_, err = counter.Call("bump", nil, background)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
}

// reference.test.ts › references › end their watches when released, while other references keep the object.
func TestReferencesEndTheirWatchesWhenReleasedWhileOtherReferencesKeepTheObject(t *testing.T) {
	host := refLoadCounter(t)
	first := refGet(t, host, refCounter, jsonObject())
	second := refGet(t, host, refCounter, jsonObject())
	one, err := first.Member("doc").Watch(background)
	must(t, err)
	two, err := second.Member("doc").Watch(background)
	must(t, err)
	first.Release()
	<-one.Closed()
	if end := one.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("end = %+v, want ended", end)
	}
	frames := nexttest.FramesOf(two)
	_, err = second.Call("bump", nil, background)
	must(t, err)
	deliveredOn(host)
	nexttest.RequireJSONEqual(t, valuesOfFrames(frames.Items()), []any{kmN(1)})
}

// reference.test.ts › references › declared with self.get keep their object alive and are released when the declaring
// object ends.
func TestReferencesDeclaredWithSelfGetKeepTheirObjectAliveAndAreReleasedWhenTheDeclaringObjectEnds(t *testing.T) {
	inner := mustContract(contracts.Local("app.inner", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	var runs atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(inner, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			runs.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(float64(1))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(refViewer, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": Expose(self.Get(inner, jsonObject("id", params.Value("id"))).Member("n")),
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	viewer := refGet(t, host, refViewer, jsonObject("id", float64(1)))
	direct := refGet(t, host, inner, jsonObject("id", float64(1)))
	direct.Release()
	nexttest.RequireJSONEqual(t, kmMember(t, viewer, "n"), float64(1))
	if got := runs.Load(); got != 1 {
		t.Fatalf("inner factory runs = %d, want 1", got)
	}
	viewer.Release()
	// The viewer's teardown releases its reference in the background.
	scopeEventually(t, "the inner object lost its last reference", func() bool {
		return len(presenceList(t, host, inner)) == 0
	})
	refGet(t, host, inner, jsonObject("id", float64(1)))
	if got := runs.Load(); got != 2 {
		t.Fatalf("inner factory runs = %d, want 2", got)
	}
}

// reference.test.ts › cycles (Q5) › fail a two-object cycle with invalid/cycle and keep nothing.
func TestCyclesQ5FailATwoObjectCycleWithInvalidCycleAndKeepNothing(t *testing.T) {
	a, b, _ := refCycleTypes()
	var runs [2]atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(a, refCycleFactory(b, &runs[0]), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(b, refCycleFactory(a, &runs[1]), ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(a, jsonObject("n", float64(1)), background, GetOptions{})
	nexttest.RequireJSONEqual(t, rejection(t, err), map[string]any{
		"code": "invalid", "reason": "cycle", "message": "dependency cycle: a(1) → b(1) → a(1)",
	})
	// Nothing is kept: the failed objects end with their references before the next get.
	scopeEventually(t, "the cycle's objects ended", func() bool {
		ended := true
		host.(*chordHost).loop().Run(func() { ended = len(host.(*chordHost).objectList()) == 0 })
		return ended
	})
	if _, err := host.Get(a, jsonObject("n", float64(1)), background, GetOptions{}); err == nil || !refContains(err.Error(), "dependency cycle") {
		t.Fatalf("second get = %v, want a dependency cycle", err)
	}
	if got := [2]int32{runs[0].Load(), runs[1].Load()}; got != [2]int32{2, 2} {
		t.Fatalf("factory runs = %v, want [2 2]", got)
	}
}

func refContains(text, part string) bool { return strings.Contains(text, part) }

func refCycleTypes() (*contracts.Contract, *contracts.Contract, *contracts.Contract) {
	make := func(name string) *contracts.Contract {
		return mustContract(contracts.Local(name, contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("n", contracts.Int())},
			Members: []contracts.LocalEntry{contracts.Mem("v", contracts.Str())},
		}))
	}
	return make("a"), make("b"), make("c")
}

// refCycleFactory is `({ n }, self) => ({ v: self.get(next, { n }).v })`; runs counts its calls.
func refCycleFactory(next *contracts.Contract, runs *atomic.Int32) LocalFactory {
	return func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		runs.Add(1)
		return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
			"v": Expose(self.Get(next, jsonObject("n", params.Value("n"))).Member("v")),
		}}}, nil
	}
}

// reference.test.ts › cycles (Q5) › fail a three-object cycle.
func TestCyclesQ5FailAThreeObjectCycle(t *testing.T) {
	a, b, c := refCycleTypes()
	var runs atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		for _, pair := range [][2]*contracts.Contract{{a, b}, {b, c}, {c, a}} {
			if err := h.Serve(pair[0], refCycleFactory(pair[1], &runs), ServeOptions{}); err != nil {
				return err
			}
		}
		return nil
	}, loadedOptions{})
	_, err := host.Get(a, jsonObject("n", float64(1)), background, GetOptions{})
	if err == nil || err.Error() != "dependency cycle: a(1) → b(1) → c(1) → a(1)" {
		t.Fatalf("get = %v, want dependency cycle: a(1) → b(1) → c(1) → a(1)", err)
	}
}

// reference.test.ts › cycles (Q5) › fail an object that gets itself.
func TestCyclesQ5FailAnObjectThatGetsItself(t *testing.T) {
	a, _, _ := refCycleTypes()
	var runs atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return h.Serve(a, refCycleFactory(a, &runs), ServeOptions{}) }, loadedOptions{})
	_, err := host.Get(a, jsonObject("n", float64(1)), background, GetOptions{})
	if err == nil || err.Error() != "dependency cycle: a(1) → a(1)" {
		t.Fatalf("get = %v, want dependency cycle: a(1) → a(1)", err)
	}
}

// reference.test.ts › GC › 10,000 calls on one reference retain flat memory, without a caller's signal / with a
// long-lived caller's signal.
func TestGC10000CallsOnOneReferenceRetainFlatMemory(t *testing.T) {
	long, cancel := chordctx.WithCancel(background)
	defer cancel(nil)
	for _, tc := range []struct {
		name   string
		caller context.Context
	}{
		{"without a caller's signal", background},
		{"with a long-lived caller's signal", chordctx.WithAbortSignal(background, long)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := refLoadCounter(t)
			counter := refGet(t, host, refCounter, jsonObject())
			run := func() {
				for range 10_000 {
					if _, err := counter.Call("bump", nil, tc.caller); err != nil {
						t.Fatalf("bump: %v", err)
					}
				}
			}
			run()
			before := settledHeap()
			run()
			mid := settledHeap()
			run()
			after := settledHeap()
			// The leak retained about 350 B per call, every round; a document commit itself retains nothing. Garbage of
			// other tests' background work shows in one round at most: a leak grows in both.
			grew := func(from, to uint64) uint64 {
				if to <= from {
					return 0
				}
				return to - from
			}
			if first, second := grew(before, mid), grew(mid, after); first >= 500_000 && second >= 500_000 {
				t.Fatalf("heap grew by %d and %d bytes over two rounds of 10,000 calls, want less than 500000", first, second)
			}
		})
	}
}

// reference.test.ts › GC › a released reference keeps neither its contract nor its object.
func TestGCAReleasedReferenceKeepsNeitherItsContractNorItsObject(t *testing.T) {
	var objects []weak.Pointer[attempt]
	var mu sync.Mutex
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refViewer, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			objects = append(objects, weak.Make(self.(*attempt)))
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(params.Value("id"))},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	var reference *Reference
	var same weak.Pointer[contracts.Contract]
	func() {
		// The caller's own declaration, equal to the served one.
		contract := mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
			Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
		reference = refGet(t, host, contract, jsonObject("id", float64(1)))
		_, err := reference.Member("n").Watch(background)
		must(t, err)
		reference.Release()
		same = weak.Make(contract)
	}()
	expectCollectedEventually(t, same)
	mu.Lock()
	expectCollectedEventually(t, objects...)
	mu.Unlock()
	if _, err := reference.Member("n").Value(); err == nil || err.Error() != "app.viewer: released" {
		t.Fatalf("n.value = %v, want app.viewer: released", err)
	}
}

// reference.test.ts › GC › a released reference whose state was read keeps no host.
func TestGCAReleasedReferenceWhoseStateWasReadKeepsNoHost(t *testing.T) {
	var reference *Reference
	var hosts []weak.Pointer[chordHost]
	func() {
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(refViewer, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(params.Value("id"))},
				}}, nil
			}, ServeOptions{})
		}, loadedOptions{})
		reference = refGet(t, host, refViewer, jsonObject("id", float64(1)))
		if got := reference.State().Value(); got.Kind != RefStateLive {
			t.Fatalf("state = %+v, want live", got)
		}
		watch, err := reference.State().Watch(background)
		must(t, err)
		_, err = watch.Stop()
		must(t, err)
		reference.Release()
		hosts = append(hosts, weak.Make(host.(*chordHost)))
	}()
	expectCollectedEventually(t, hosts...)
	if got := reference.State().Value(); got.Kind != RefStateEnded || got.Error.Code != data.CodeReleased {
		t.Fatalf("state = %+v, want ended released", got)
	}
}

// reference.test.ts › GC › a released reference whose state was read after a failure keeps neither the failure's error nor
// the host.
func TestGCAReleasedReferenceWhoseStateWasReadAfterAFailureKeepsNeitherTheFailuresErrorNorTheHost(t *testing.T) {
	var reference *Reference
	var errs []weak.Pointer[data.ChordError]
	var hosts []weak.Pointer[chordHost]
	func() {
		host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(refViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Open(func(context.Context) (any, error) { return nil, data.Broken(errors.New("no configuration")) },
					func(any, NativeCloseReason, context.Context) error { return nil })
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(float64(1))},
				}}, nil
			}, ServeOptions{})
		}, loadedOptions{})
		var err error
		reference, err = host.Hold(refViewer, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		scopeEventually(t, "the reference failed", func() bool { return refKind(reference) == RefStateFailed })
		failure := reference.State().Value()
		errs = append(errs, weak.Make(failure.Cause.Error))
		hosts = append(hosts, weak.Make(host.(*chordHost)))
		reference.Release()
	}()
	expectCollectedEventually(t, errs...)
	expectCollectedEventually(t, hosts...)
	if got := reference.State().Value(); got.Kind != RefStateEnded || got.Error.Code != data.CodeReleased {
		t.Fatalf("state = %+v, want ended released", got)
	}
}

// reference.test.ts › GC › a hold released before load keeps nothing: the reference and its contract go while the host
// waits.
func TestGCAHoldReleasedBeforeLoadKeepsNothingTheReferenceAndItsContractGoWhileTheHostWaits(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	var references []weak.Pointer[Reference]
	var contractRefs []weak.Pointer[contracts.Contract]
	func() {
		same := mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
			Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
		reference, err := host.Hold(same, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		reference.Release()
		references = append(references, weak.Make(reference))
		contractRefs = append(contractRefs, weak.Make(same))
	}()
	expectCollectedEventually(t, references...)
	expectCollectedEventually(t, contractRefs...)
	if host == nil {
		t.Fatal("no host")
	}
}

// reference.test.ts › GC › a released hold keeps neither its object, nor its state's watches, nor a remote object without a
// link.
func TestGCAReleasedHoldKeepsNeitherItsObjectNorItsStatesWatchesNorARemoteObjectWithoutALink(t *testing.T) {
	var mu sync.Mutex
	var objects []weak.Pointer[attempt]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refViewer, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			objects = append(objects, weak.Make(self.(*attempt)))
			mu.Unlock()
			self.Open(func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, context.Cause(ctx) },
				func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(params.Value("id"))},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	client := newHost(t, HostOptions{OnError: func(HostError) {}})
	must(t, client.Load(nil, background))
	var watches []weak.Pointer[chord.NativeWatch[*RefState]]
	var remotes []weak.Pointer[remoteObject]
	func() {
		local, err := host.Hold(refViewer, jsonObject("id", float64(1)), background, GetOptions{})
		must(t, err)
		remote, err := client.Hold(Notes, routeS("S1"), background, GetOptions{})
		must(t, err)
		for _, ref := range []*Reference{local, remote} {
			watch, err := ref.State().Watch(background)
			must(t, err)
			watches = append(watches, weak.Make(watch.(*chord.NativeWatch[*RefState])))
		}
		client.(*chordHost).loop().Run(func() {
			if object, ok := remote.binding.target().(*remoteObject); ok {
				remotes = append(remotes, weak.Make(object))
			}
		})
		// A member watch waiting for the first value, and a parked call: both rejected by the release.
		var waits sync.WaitGroup
		waits.Add(2)
		go func() { defer waits.Done(); _, _ = local.Member("n").Watch(background) }()
		go func() { defer waits.Done(); _, _ = remote.Call("wait", nil, background) }()
		deliveredOn(host, client)
		local.Release()
		remote.Release()
		waits.Wait()
	}()
	mu.Lock()
	expectCollectedEventually(t, objects...)
	mu.Unlock()
	expectCollectedEventually(t, watches...)
	expectCollectedEventually(t, remotes...)
}

// reference.test.ts › release › rejects the calls made through the reference with released and aborts their handlers'
// contexts.
func TestReleaseRejectsTheCallsMadeThroughTheReferenceWithReleasedAndAbortsTheirHandlersContexts(t *testing.T) {
	slow := mustContract(contracts.Local("app.slow", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("wait", contracts.NewMethod())},
	}))
	var mu sync.Mutex
	var aborted []error
	entered := make(chan struct{}, 2)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(slow, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					entered <- struct{}{}
					<-ctx.Done()
					mu.Lock()
					aborted = append(aborted, context.Cause(ctx))
					mu.Unlock()
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	first := refGet(t, host, slow, jsonObject())
	second := refGet(t, host, slow, jsonObject())
	pending := make(chan error, 1)
	other := make(chan error, 1)
	go func() { _, err := first.Call("wait", nil, background); pending <- err }()
	<-entered
	go func() { _, err := second.Call("wait", nil, background); other <- err }()
	<-entered
	first.Release()
	nexttest.RequireJSONEqual(t, rejection(t, <-pending), map[string]any{"code": "released", "message": "app.slow: released"})
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(aborted) }
	scopeEventually(t, "the first handler's context aborted", func() bool { return count() == 1 })
	second.Release()
	<-other
	scopeEventually(t, "the second handler's context aborted", func() bool { return count() == 2 })
}

// reference.test.ts › release › reads, watches and calls after the object ended carry the end's reason; the holder's own
// release has none.
func TestReleaseReadsWatchesAndCallsAfterTheObjectEndedCarryTheEndsReasonTheHoldersOwnReleaseHasNone(t *testing.T) {
	ended := mustContract(contracts.Local("app.ended", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("m", contracts.NewMethod())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(ended, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(float64(1))},
				Methods: map[string]contracts.MethodFunction{"m": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	end := refGet(t, host, ended, jsonObject())
	released := refGet(t, host, ended, jsonObject())
	released.Release()
	must(t, host.Close(background))
	_, readErr := end.Member("n").Value()
	_, watchErr := end.Member("n").Watch(background)
	_, callErr := end.Call("m", nil, background)
	if got := []string{refReason(readErr), refReason(watchErr), refReason(callErr)}; !slices.Equal(got, []string{"released/ended", "released/ended", "released/ended"}) {
		t.Fatalf("reasons = %v, want released/ended three times", got)
	}
	_, ownErr := released.Member("n").Value()
	if got := refReason(ownErr); got != "released/undefined" {
		t.Fatalf("the holder's own release = %s, want released/undefined", got)
	}
}

var refTwo = mustContract(contracts.Remote("app.two", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
	Variants: []contracts.VariantEntry{
		contracts.Var("full", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("n", contracts.Int())}, Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod())},
		}),
		contracts.Var("view", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("m", contracts.Int())}}),
	},
}))

// refTwoHost is a server host (principal owner) serving Two's view variant and a Holder that gets Two in its factory.
func refTwoHost(t *testing.T, seen func(two *Reference)) Host {
	t.Helper()
	holder := mustContract(contracts.Local("app.holder", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	host := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: ownerPrincipal(), OnError: func(HostError) {}})
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(refTwo, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "view", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"m": refConstant(float64(1))},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(holder, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			seen(self.Get(refTwo, jsonObject()))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": refConstant(float64(1))},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	return host
}

// reference.test.ts › remote references › have every member of every variant; variant is undefined before live and released
// after release.
func TestRemoteReferencesHaveEveryMemberOfEveryVariantVariantIsUndefinedBeforeLiveAndReleasedAfterRelease(t *testing.T) {
	var mu sync.Mutex
	var seen []any
	host := refTwoHost(t, func(two *Reference) {
		_, hasBump := two.binding.methods["bump"]
		variant, bound, err := two.Variant()
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, []any{two.Member("m") != nil, two.Member("n") != nil, hasBump}, []any{variant, bound, err})
	})
	mu.Lock()
	nexttest.RequireJSONEqual(t, []any{seen[0], seen[1].([]any)[0], seen[1].([]any)[1]}, []any{[]any{true, true, true}, "", false})
	mu.Unlock()
	two := refGet(t, host, refTwo, jsonObject())
	if _, err := two.Member("n").Value(); err == nil || err.Error() != `app.two (variant view) has no state member "n"` {
		t.Fatalf("n.value = %v, want it to throw has no state member", err)
	}
	two.Release()
	if _, _, err := two.Variant(); err == nil || err.Error() != "app.two: released" {
		t.Fatalf("variant after release = %v, want app.two: released", err)
	}
}

// reference.test.ts › remote reference types › narrow on variant; RefIn picks variants. Type-level in Pi (expectTypeOf):
// the Go mechanism is Reference.Variant, which names the variant a remote reference is bound to and is the one place a
// local contract's reference reports none.
func TestRemoteReferenceTypesNarrowOnVariantRefInPicksVariants(t *testing.T) {
	host := refTwoHost(t, func(*Reference) {})
	two := refGet(t, host, refTwo, jsonObject())
	if variant, bound, err := two.Variant(); err != nil || !bound || variant != "view" {
		t.Fatalf("variant = %q, %v, %v, want view bound", variant, bound, err)
	}
	counter := refGet(t, refLoadCounter(t), refCounter, jsonObject())
	if variant, bound, err := counter.Variant(); err != nil || bound || variant != "" {
		t.Fatalf("a local reference's variant = %q, %v, %v, want none", variant, bound, err)
	}
	if two.Member("m") == nil || two.Member("zzz") != nil {
		t.Fatalf("Member does not tell the contract's members from others")
	}
}

// reference.test.ts › remote reference types › a held reference's variant may be undefined (before its first binding) and
// still narrows. Type-level in Pi: Held is Reference, and Variant reports bound false before the first binding.
func TestRemoteReferenceTypesAHeldReferencesVariantMayBeUndefinedBeforeItsFirstBindingAndStillNarrows(t *testing.T) {
	// A host that is not loaded has no publication: a held reference waits unbound.
	host := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: ownerPrincipal(), OnError: func(HostError) {}})
	var held *Held
	var err error
	held, err = host.Hold(refTwo, jsonObject(), background, GetOptions{})
	must(t, err)
	if variant, bound, err := held.Variant(); err != nil || bound || variant != "" {
		t.Fatalf("variant before the first binding = %q, %v, %v, want none", variant, bound, err)
	}
	if kind := refKind(held); kind != RefStateLoading {
		t.Fatalf("held state = %s, want loading", kind)
	}
	held.Retry()
	held.Release()
}

// --- keyed state members (slice 6a §12) ---

var (
	refNote = contracts.Obj(contracts.Prop("title", contracts.Str()))
	refMemo = func() *documents.StateDefinition {
		definition, err := documents.DefineState("memo", documents.StateOptions{
			Schema: refNote, Version: 1, Keyed: true, Initial: func() *delta.JsonObject { return jsonObject("title", "") },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	refNoteStore = mustContract(contracts.Local("app.note-store", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("notes", contracts.Keyed(refNote)),
			contracts.Mem("write", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("title", contracts.Str())))),
			contracts.Mem("remove", contracts.NewMethod(contracts.Str())),
		},
	}))
	refRelay = mustContract(contracts.Local("app.relay", contracts.LocalDeclaration{
		Params:  contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(refNote))},
	}))
	// refNoteView is a view of note `id`; views of one note with another `copy` are other objects.
	refNoteView = mustContract(contracts.Local("app.note-view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str()), contracts.Prop("copy", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("note", refNote)},
	}))
)

func refTitle(title string) *delta.JsonObject { return jsonObject("title", title) }

// refStoreFactory serves the note store over definition: write and remove change and retire a note; memos receives the handle.
func refStoreFactory(definition *documents.StateDefinition, memos func(*documents.Keyed)) LocalFactory {
	return func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		notes := self.State(definition).(*documents.Keyed)
		memos(notes)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Keyed: kmKeyed(notes),
			Methods: map[string]contracts.MethodFunction{
				"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					_, err := notes.Change(object.Value("id").(string), func(draft *delta.Object) (any, error) {
						return nil, draft.Set("title", object.Value("title"))
					}, ctx)
					return nil, err
				},
				"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, notes.Retire(args.(string), ctx)
				},
			},
		}}, nil
	}
}

// refNoteViewFactory pins one key of the store's notes (through the relay when viaRelay); pinned receives its at(key).
func refNoteViewFactory(viaRelay bool, pinned func(*KeyedMemberAt), refs func(*Reference)) LocalFactory {
	return func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		var source *Reference
		if viaRelay {
			source = self.Get(refRelay, jsonObject())
		} else {
			source = self.Get(refNoteStore, jsonObject())
		}
		refs(source)
		note, err := source.Keyed("notes").At(params.Value("id").(string))
		if err != nil {
			return contracts.Implementation{}, err
		}
		pinned(note)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"note": Expose(note)},
		}}, nil
	}
}

// refNotes is Pi's `notes(options)`.
type refNotes struct {
	Host   Host
	Errors *hostErrors
	Store  *Reference
	mu     sync.Mutex
	memos  *documents.Keyed
	pinned []*KeyedMemberAt
	refs   []*Reference
}

func (n *refNotes) Memos() *documents.Keyed {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.memos
}

func (n *refNotes) Pinned() []*KeyedMemberAt {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.pinned)
}

func refServeNotes(t *testing.T, viaRelay bool) *refNotes {
	t.Helper()
	n := &refNotes{}
	n.Host, n.Errors = loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(refNoteStore, refStoreFactory(refMemo, func(notes *documents.Keyed) {
			n.mu.Lock()
			n.memos = notes
			n.mu.Unlock()
		}), ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(refRelay, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: kmKeyed(self.Get(refNoteStore, jsonObject()).Keyed("notes")),
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(refNoteView, refNoteViewFactory(viaRelay, func(at *KeyedMemberAt) {
			n.mu.Lock()
			n.pinned = append(n.pinned, at)
			n.mu.Unlock()
		}, func(ref *Reference) {
			n.mu.Lock()
			n.refs = append(n.refs, ref)
			n.mu.Unlock()
		}), ServeOptions{})
	}, loadedOptions{})
	n.Store = refGet(t, n.Host, refNoteStore, jsonObject())
	return n
}

func refWrite(t testing.TB, store *Reference, id, title string) {
	t.Helper()
	_, err := store.Call("write", jsonObject("id", id, "title", title), background)
	must(t, err)
}

// refSourcePins is the pins on the key's source of the notes slot, and whether the slot has it.
func refSourcePins(host Host, notes *documents.Keyed, key string) (pins int, has bool) {
	host.(*chordHost).loop().Run(func() {
		if source := notes.Slot.KeySourceOf(key); source != nil {
			pins, has = source.Pins(), true
		}
	})
	return pins, has
}

// reference.test.ts › keyed state members (slice 6a §12) › a consumer's at(key) is the provider's per-key source, loaded
// before it goes live; retirement carries the flag.
func TestKeyedStateMembersAConsumersAtKeyIsTheProvidersPerKeySourceLoadedBeforeItGoesLiveRetirementCarriesTheFlag(t *testing.T) {
	n := refServeNotes(t, false)
	refWrite(t, n.Store, "a", "x")
	view := refGet(t, n.Host, refNoteView, jsonObject("id", "a", "copy", float64(1)))
	nexttest.RequireJSONEqual(t, kmMember(t, view, "note"), refTitle("x"))
	changes := kmWatch(t, view.Member("note"))
	refWrite(t, n.Store, "a", "y")
	deliveredOn(n.Host)
	_, err := n.Store.Call("remove", "a", background)
	must(t, err)
	deliveredOn(n.Host)
	nexttest.RequireJSONEqual(t, msCalls(changes.Items()), []any{
		[]any{"value", refTitle("y"), []any{[]any{"s", []any{"title"}, "y"}}, false},
		[]any{"value", refTitle(""), []any{[]any{"r", refTitle("")}}, true},
	})
	view.Release()
	must(t, n.Host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › two consumers of one key share one source and one pin; each hold
// is released once, with its reference.
func TestKeyedStateMembersTwoConsumersOfOneKeyShareOneSourceAndOnePinEachHoldIsReleasedOnceWithItsReference(t *testing.T) {
	n := refServeNotes(t, false)
	refWrite(t, n.Store, "a", "x")
	first := refGet(t, n.Host, refNoteView, jsonObject("id", "a", "copy", float64(1)))
	second := refGet(t, n.Host, refNoteView, jsonObject("id", "a", "copy", float64(2)))
	pinned := n.Pinned()
	sources := make([]state.DeriveSource, len(pinned))
	for i, at := range pinned {
		n.Host.(*chordHost).loop().Run(func() { sources[i], _ = at.source() })
	}
	// The store object's source of the key, which follows its attempts.
	if len(sources) != 2 || sources[0] != sources[1] {
		t.Fatalf("the two views' sources are not one: %v", sources)
	}
	shared := func() *documents.KeySource {
		var source *documents.KeySource
		n.Host.(*chordHost).loop().Run(func() { source = n.Memos().Slot.KeySourceOf("a") })
		return source
	}()
	if pins, _ := refSourcePins(n.Host, n.Memos(), "a"); shared == nil || pins != 1 {
		t.Fatalf("pins = %d, want one pin on one source", pins)
	}
	first.Release()
	// The first view's teardown releases its pin in the background: wait for it before asserting what stays.
	scopeEventually(t, "the first view ended", func() bool { return len(presenceList(t, n.Host, refNoteView)) == 1 })
	if got := func() *documents.KeySource {
		var source *documents.KeySource
		n.Host.(*chordHost).loop().Run(func() { source = n.Memos().Slot.KeySourceOf("a") })
		return source
	}(); got != shared {
		t.Fatalf("the source changed after the first release")
	}
	second.Release()
	scopeEventually(t, "the key's source is dropped", func() bool {
		_, has := refSourcePins(n.Host, n.Memos(), "a")
		return !has
	})
	must(t, n.Host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › a re-exposed keyed member resolves to the ultimate provider's
// source (no second tracker).
func TestKeyedStateMembersAReExposedKeyedMemberResolvesToTheUltimateProvidersSourceNoSecondTracker(t *testing.T) {
	n := refServeNotes(t, true)
	refWrite(t, n.Store, "a", "x")
	view := refGet(t, n.Host, refNoteView, jsonObject("id", "a", "copy", float64(1)))
	nexttest.RequireJSONEqual(t, kmMember(t, view, "note"), refTitle("x"))
	// One pin on the ultimate slot, through the relay's key and the store's.
	if pins, has := refSourcePins(n.Host, n.Memos(), "a"); !has || pins != 1 {
		t.Fatalf("pins = %d (has %v), want 1", pins, has)
	}
	refWrite(t, n.Store, "a", "y")
	nexttest.RequireJSONEqual(t, kmMember(t, view, "note"), refTitle("y"))
	must(t, n.Host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › ref.member.get and watch work anywhere; at(key) only in a factory.
func TestKeyedStateMembersRefMemberGetAndWatchWorkAnywhereAtKeyOnlyInAFactory(t *testing.T) {
	n := refServeNotes(t, false)
	notes := n.Store.Keyed("notes")
	if _, found, err := notes.Get(background, "a"); err != nil || found {
		t.Fatalf("get of an absent key = %v, %v, want absent", found, err)
	}
	if watch, err := notes.Watch(background, "a"); err != nil || watch != nil {
		t.Fatalf("watch of an absent key = %v, %v, want none", watch, err)
	}
	refWrite(t, n.Store, "a", "x")
	value, found, err := notes.Get(background, "a")
	must(t, err)
	nexttest.RequireJSONEqual(t, []any{value, found}, []any{refTitle("x"), true})
	watch, err := notes.Watch(background, "a")
	must(t, err)
	nexttest.RequireJSONEqual(t, watch.Value(), refTitle("x"))
	if _, err := notes.At("a"); err == nil || !refContains(err.Error(), "valid only in the factory") {
		t.Fatalf("at = %v, want it valid only in the factory", err)
	}
	n.Store.Release()
	<-watch.Closed()
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("end = %+v, want ended", end)
	}
	must(t, n.Host.Close(background))
}

// refPersisted is a persisted keyed `memo` at version, whose `migrate` (from older versions) throws.
func refPersistedMemo(version int) *documents.StateDefinition {
	definition, err := documents.DefineState("memo", documents.StateOptions{
		Schema: refNote, Version: version, Keyed: true, Persist: true,
		Initial:        func() *delta.JsonObject { return jsonObject("title", "") },
		Migrate:        func(*delta.JsonObject, int) (*delta.JsonObject, error) { return nil, errors.New("cannot migrate") },
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	if err != nil {
		panic(err)
	}
	return definition
}

// refPersistedNotes serves the note store over definition (persisted) on st, and views pinning one key each.
func refPersistedNotes(t *testing.T, st store.Store, definition *documents.StateDefinition) (Host, *hostErrors) {
	t.Helper()
	return loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(refNoteStore, refStoreFactory(definition, func(*documents.Keyed) {}), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(refNoteView, refNoteViewFactory(false, func(*KeyedMemberAt) {}, func(*Reference) {}), ServeOptions{})
	}, loadedOptions{Store: st})
}

// refSeeded is a store holding memo `a` = "x" at version 1, written by a host of its own.
func refSeeded(t *testing.T) *store.ChordStore {
	t.Helper()
	st := memoryStore(t)
	host, _ := refPersistedNotes(t, st, refPersistedMemo(1))
	refWrite(t, refGet(t, host, refNoteStore, jsonObject()), "a", "x")
	must(t, host.Close(background))
	return st
}

// reference.test.ts › keyed state members (slice 6a §12) › a watch(key) settling after its reference was released is ended
// at once with its pin; it rejects released. Pi lets the release land between the slot's read and the pin with two
// microtasks; Go holds the key's cold read at the store (where the watch waits on its own task) and releases then.
func TestKeyedStateMembersAWatchKeySettlingAfterItsReferenceWasReleasedIsEndedAtOnceWithItsPinItRejectsReleased(t *testing.T) {
	gate := newReadGate()
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &readGateDatabase{SqliteDatabase: db, gate: gate}
	}, nil)
	t.Cleanup(gate.Release)
	seed, _ := refPersistedNotes(t, st, refPersistedMemo(1))
	refWrite(t, refGet(t, seed, refNoteStore, jsonObject()), "a", "x")
	must(t, seed.Close(background))
	var memos atomic.Pointer[documents.Keyed]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refNoteStore, refStoreFactory(refPersistedMemo(1), func(notes *documents.Keyed) { memos.Store(notes) }), ServeOptions{})
	}, loadedOptions{Store: st})
	storeRef := refGet(t, host, refNoteStore, jsonObject())
	gate.armed.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := storeRef.Keyed("notes").Watch(background, "a")
		result <- err
	}()
	<-gate.entered
	storeRef.Release()
	gate.Release()
	err := <-result
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok || chordErr == nil {
		t.Fatalf("watch = %v, want it to reject released", err)
	}
	scopeEventually(t, "the key's source is dropped", func() bool {
		_, has := refSourcePins(host, memos.Load(), "a")
		return !has
	})
	must(t, host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › a retained released factory reference keeps no provider: its
// at(key) drops the source.
func TestKeyedStateMembersARetainedReleasedFactoryReferenceKeepsNoProviderItsAtKeyDropsTheSource(t *testing.T) {
	var objects []weak.Pointer[chordObject]
	var n *refNotes
	func() {
		n = refServeNotes(t, false)
		refWrite(t, n.Store, "a", "x")
		view := refGet(t, n.Host, refNoteView, jsonObject("id", "a", "copy", float64(1)))
		h := n.Host.(*chordHost)
		h.loop().Run(func() {
			for _, object := range h.objectList() {
				objects = append(objects, weak.Make(object))
			}
		})
		view.Release()
		n.Store.Release()
		must(t, n.Host.Close(background))
		// Pi's `memos` closure is not kept by this case; the slot's handle reaches its object.
		n.mu.Lock()
		n.memos = nil
		n.mu.Unlock()
	}()
	// The factory's reference and its at(key) are still held here (and by the host's factory).
	n.mu.Lock()
	retained := len(n.refs)
	n.mu.Unlock()
	if retained != 1 {
		t.Fatalf("factory references = %d, want 1", retained)
	}
	expectCollectedEventually(t, objects...)
	if _, err := n.Pinned()[0].Value(); err == nil || !refContains(err.Error(), "released") {
		t.Fatalf("pinned.value = %v, want it to throw released", err)
	}
}

// reference.test.ts › keyed state members (slice 6a §12) › a persisted key a consumer pins is loaded from the store before the
// consumer goes live.
func TestKeyedStateMembersAPersistedKeyAConsumerPinsIsLoadedFromTheStoreBeforeTheConsumerGoesLive(t *testing.T) {
	st := refSeeded(t)
	host, _ := refPersistedNotes(t, st, refPersistedMemo(1))
	view := refGet(t, host, refNoteView, jsonObject("id", "a", "copy", float64(1)))
	nexttest.RequireJSONEqual(t, kmMember(t, view, "note"), refTitle("x"))
	must(t, host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › a pin load that fails its provider parks the consumer failed {
// waiting } on it (a dependency failure).
func TestKeyedStateMembersAPinLoadThatFailsItsProviderParksTheConsumerFailedWaitingOnItADependencyFailure(t *testing.T) {
	st := refSeeded(t)
	host, errs := refPersistedNotes(t, st, refPersistedMemo(2))
	provider := refGet(t, host, refNoteStore, jsonObject())
	view, err := host.Hold(refNoteView, jsonObject("id", "a", "copy", float64(1)), background, GetOptions{})
	must(t, err)
	scopeEventually(t, "the view failed", func() bool { return refKind(view) == RefStateFailed })
	got := view.State().Value()
	if got.Cause.Kind != data.FailureWaiting || !refContains(got.Cause.Error.Message, "cannot migrate") {
		t.Fatalf("view state = %+v, want failed waiting on cannot migrate", got)
	}
	if p := provider.State().Value(); p.Kind != RefStateFailed || p.Cause.Kind != data.FailureBroken {
		t.Fatalf("provider state = %+v, want failed broken", p)
	}
	var kinds []HostErrorKind
	for _, each := range errs.All() {
		kinds = append(kinds, each.Kind)
	}
	if !slices.Contains(kinds, ErrObjectFailed) {
		t.Fatalf("errors = %v, want object_failed among them", kinds)
	}
	view.Release()
	must(t, host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › a failed provider's ref.member.get rejects with the failure's
// cause.
func TestKeyedStateMembersAFailedProvidersRefMemberGetRejectsWithTheFailuresCause(t *testing.T) {
	st := refSeeded(t)
	host, _ := refPersistedNotes(t, st, refPersistedMemo(2))
	provider := refGet(t, host, refNoteStore, jsonObject())
	for _, key := range []string{"a", "b"} {
		if _, _, err := provider.Keyed("notes").Get(background, key); err == nil || !refContains(err.Error(), "cannot migrate") {
			t.Fatalf("get %s = %v, want it to reject with cannot migrate", key, err)
		}
	}
	must(t, host.Close(background))
}

// reference.test.ts › keyed state members (slice 6a §12) › a provider returning something else for a keyed member fails
// broken.
func TestKeyedStateMembersAProviderReturningSomethingElseForAKeyedMemberFailsBroken(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		// Plugin code returning a wrong value for the keyed member.
		return h.Serve(refNoteStore, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"notes": refConstant(jsonObject())},
				Methods: map[string]contracts.MethodFunction{
					"write":  func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
					"remove": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(refNoteStore, jsonObject(), background, GetOptions{})
	if err == nil || !refContains(err.Error(), "notes: a keyed member is a keyed document handle") {
		t.Fatalf("get = %v, want notes: a keyed member is a keyed document handle", err)
	}
	must(t, host.Close(background))
}
