package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports the reload describes of packages/chord/test/host/object.test.ts: fates (decision "Reload restarts every affected object"), a
// restart's readiness and old attempt, a stuck routine call past the drain, old work finishing on old code, the reload drain
// bound, GC, a parked attempt's handover, two reloads in a row, a reference detaching from a parked restart's old attempt, and
// the two "final review" cases of a live failure and a native restart. Every case runs a real
// host; the one Pi runs under `fakeTimers()` runs on the scopeLoop. What Pi awaits in a handler is a blocking Go function on
// its task, gated by signals; a call a case starts and awaits later runs on a goroutine and reports on a channel.
//
// Forced Go differences (recorded in the lane's rows): Pi spies `ChordObject.prototype.reloadFate`/`remove` and
// `Attempt.prototype.activate` (`instancesOf`) to reach the objects and attempts of a host; a Go test reaches them through the
// reference's target (`ref.binding.target()`) in a section, and weak pointers to them.

// reloadPure is `Pure`: a local object with empty params: a counter document, `bump` and `label`.
var reloadPure = mustContract(declareLocal("app.pure", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("n", contracts.Int()), contracts.Mem("label", contracts.Str()),
		contracts.Mem("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
	},
}))

// reloadCounterDoc is `defineState("pure", COUNTER)`.
var reloadCounterDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("pure", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// reloadGate is one gate of a `bump`: it fires `entered` when the handler reaches it, and holds the handler until `open` fired.
type reloadGate struct{ entered, open signal }

func newReloadGate() *reloadGate { return &reloadGate{entered: newSignal(), open: newSignal()} }

// reloadGateQueue is `gates: Promise<void>[]`: a `bump` takes the first one queued (`gates.shift()`), none when it is empty; a nil
// gate is a resolved promise.
type reloadGateQueue struct {
	mu    sync.Mutex
	gates []*reloadGate
}

func reloadGates(queued ...*reloadGate) *reloadGateQueue { return &reloadGateQueue{gates: queued} }

func (q *reloadGateQueue) shift() *reloadGate {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.gates) == 0 {
		return nil
	}
	first := q.gates[0]
	q.gates = q.gates[1:]
	return first
}

// reloadPureImplementation is what `reloadServePure` returns: n (a derive of the counter document), label, and `bump` adding `by` after
// the gate it takes.
func reloadPureImplementation(t testing.TB, self Self, label string, by float64, queue *reloadGateQueue, extra func(doc *singletonDoc)) contracts.Implementation {
	doc := stateOf(self, reloadCounterDoc)
	if extra != nil {
		extra(doc)
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("n"), nil
			}),
			"label": constant(label),
		},
		Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			if held := queue.shift(); held != nil {
				held.entered.fire()
				held.open.await(t, ctx)
			}
			if err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+by) }, ctx); err != nil {
				return nil, err
			}
			return doc.Value().Value("n"), nil
		}},
	}}
}

// reloadServePure serves `Pure` labelled label; `bump` adds by, first waiting for the gate queued when there is one.
func reloadServePure(t testing.TB, h FacetHost, label string, by float64, queue *reloadGateQueue) error {
	return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		return reloadPureImplementation(t, self, label, by, queue, nil), nil
	}, ServeOptions{})
}

// reloadApp reloads `host`'s plugin `@test/app` (as `loaded` names it) with a facet whose setup is setup.
func reloadApp(host Host, setup func(h FacetHost) error) (ReloadReport, error) {
	return host.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}},
		background, ReloadOptions{})
}

// reloadMust is `await host.reload(...)`: the report, failing the test with the error.
func reloadMust(t testing.TB) func(ReloadReport, error) ReloadReport {
	return func(report ReloadReport, err error) ReloadReport {
		t.Helper()
		must(t, err)
		return report
	}
}

// reloadCall is a call a case makes and awaits later: its result arrives on the channel.
type reloadCall struct{ done chan reloadOutcome }

type reloadOutcome struct {
	value delta.JsonValue
	err   error
}

// startReloadCall is `const running = ref.method(args, context)`, not awaited.
func startReloadCall(ref *Reference, method string, args delta.JsonValue) *reloadCall {
	call := &reloadCall{done: make(chan reloadOutcome, 1)}
	go func() {
		value, err := ref.Call(method, args, background)
		call.done <- reloadOutcome{value: value, err: err}
	}()
	return call
}

// wait is `await running`: the call's value, failing the test with its error.
func (c *reloadCall) wait(t testing.TB) delta.JsonValue {
	t.Helper()
	outcome := receive(t, c.done)
	must(t, outcome.err)
	return outcome.value
}

// rejected is `await rejection(running)`: the call's error.
func (c *reloadCall) rejected(t testing.TB) error {
	t.Helper()
	outcome := receive(t, c.done)
	if outcome.err == nil {
		t.Fatalf("the call resolved with %v, want a rejection", outcome.value)
	}
	return outcome.err
}

// reloadBump is `await ref.bump(context)`.
func reloadBump(t testing.TB, ref *Reference) delta.JsonValue {
	t.Helper()
	value, err := ref.Call("bump", nil, background)
	must(t, err)
	return value
}

// reloadObjectOf is the object `ref` follows, read in a section.
func reloadObjectOf(ref *Reference) *chordObject {
	var object *chordObject
	ref.binding.loop.Run(func() { object, _ = ref.binding.target().(*chordObject) })
	return object
}

// reloadUntilLabel waits until `ref.label` reads want.
func reloadUntilLabel(t testing.TB, ref *Reference, want string) {
	t.Helper()
	scopeEventually(t, "label "+want, func() bool {
		value, err := ref.Member("label").Value()
		return err == nil && value == want
	})
}

// reloadWantObjects fails unless the report's objects are want.
func reloadWantObjects(t testing.TB, report ReloadReport, want ...ReloadedObject) {
	t.Helper()
	if !reflect.DeepEqual(report.Objects, want) {
		t.Fatalf("report objects = %+v, want %+v", report.Objects, want)
	}
}

// --- reload: fates (slice 7b §4) ------------------------------------------------------------------------------------------

func TestReloadFatesRestartsALiveObjectItsDocumentsKeptNewCodeForNewCalls(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	reloadBump(t, pure)
	var instance string
	object := reloadObjectOf(pure)
	host.(*chordHost).enter(func() { instance = object.instanceID })
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v2", 10, nil) }))
	reloadWantObjects(t, report, ReloadedObject{Object: "app.pure", Kind: ReloadRestarted})
	turn(host)
	untilKind(t, pure, RefStateLive)
	reloadUntilLabel(t, pure, "v2")
	if bumped := reloadBump(t, pure); bumped != 11.0 {
		t.Fatalf("bump = %v, want 11", bumped)
	}
	var after string
	host.(*chordHost).enter(func() { after = object.instanceID })
	if object != reloadObjectOf(pure) || after == instance {
		t.Fatalf("the object changed, or its instance id did not (%q -> %q): a restart is a new instance on the same object", instance, after)
	}
}

func TestReloadFatesShowsTheOldAttemptLiveWhileItDrainsClosesItsNativesWithReasonReloadThenOpensTheNewOnes(t *testing.T) {
	var mu sync.Mutex
	var closes []string
	push := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		closes = append(closes, text)
	}
	serve := func(h FacetHost, label string, queue *reloadGateQueue) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, reloadCounterDoc)
			self.Open(func(context.Context) (any, error) { return label, nil },
				func(value any, reason NativeCloseReason, _ context.Context) error {
					push(fmt.Sprintf("%v %s", value, reason.Kind))
					return nil
				})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
					"label": constant(label),
				},
				Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if held := queue.shift(); held != nil {
						held.entered.fire()
						held.open.await(t, ctx)
					}
					if err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx); err != nil {
						return nil, err
					}
					push("bump done on " + label)
					return doc.Value().Value("n"), nil
				}},
			}}, nil
		}, ServeOptions{})
	}
	held := newReloadGate()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serve(h, "v1", reloadGates(held)) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	running := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return serve(h, "v2", nil) }))
	reloadWantObjects(t, report, ReloadedObject{Object: "app.pure", Kind: ReloadRestarted})
	// The old attempt binds this reference's version: it sees it live while it drains (rule 5).
	if state := stateOfRef(pure); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	// A new call waits at the gate until the new attempt is live.
	parked := startReloadCall(pure, "bump", nil)
	turn(host)
	mu.Lock()
	early := append([]string(nil), closes...)
	mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("closes = %v, want none yet", early)
	}
	held.open.fire()
	if got := running.wait(t); got != 1.0 {
		t.Fatalf("running call = %v, want 1", got)
	}
	if got := parked.wait(t); got != 2.0 {
		t.Fatalf("parked call = %v, want 2", got)
	}
	mu.Lock()
	got := append([]string(nil), closes...)
	mu.Unlock()
	if want := []string{"bump done on v1", "v1 reload", "bump done on v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("closes = %v, want %v", got, want)
	}
	reloadUntilLabel(t, pure, "v2")
}

func TestReloadFatesEndsTheObjectsOfARemovedTypeItsHoldersWaitNotServedAndBindAgainWhenItIsServedAgain(t *testing.T) {
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustHold(t, host, reloadPure, jsonObject())
	untilKind(t, pure, RefStateLive)
	reloadBump(t, pure)
	label, err := pure.Member("label").Watch(background)
	must(t, err)
	frames := nexttest.ChangesOf(label)
	report, err := host.Reload(nil, background, ReloadOptions{})
	must(t, err)
	if len(report.Plugins.Added) != 0 || len(report.Plugins.Changed) != 0 || !reflect.DeepEqual(report.Plugins.Removed, []string{"@test/app"}) ||
		len(report.Failed) != 0 {
		t.Fatalf("report plugins = %+v, failed %+v, want only @test/app removed", report.Plugins, report.Failed)
	}
	reloadWantObjects(t, report, ReloadedObject{Object: "app.pure", Kind: ReloadEnded})
	state := stateOfRef(pure)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Code != data.CodeUnavailable ||
		state.Cause.Error.Message != "app.pure is not served here" {
		t.Fatalf("state = %+v (cause %+v), want failed waiting unavailable: app.pure is not served here", state, state.Cause)
	}
	// The member keeps its last value while waiting.
	if got := memberOf(t, pure, "label"); got != "v1" {
		t.Fatalf("label = %v, want v1", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return reloadServePure(t, h, "v2", 1, nil)
	})}}, background, ReloadOptions{}))
	untilKind(t, pure, RefStateLive)
	// A new object: in-memory documents went with the old one.
	if got := memberOf(t, pure, "n"); got != 0.0 {
		t.Fatalf("n = %v, want 0", got)
	}
	deliveredOn(host)
	// The removal's failure once, then the new object's value, whole.
	want := []any{nexttest.FailedValue{Failed: "app.pure is not served here"}, "v2"}
	if got := nexttest.ValuesOf(frames.Items()); !reflect.DeepEqual(got, want) {
		t.Fatalf("label frames = %v, want %v", got, want)
	}
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v, want none", got)
	}
}

func TestReloadFatesALiveLocalDependentOfARemovedTypeKeepsItsLastValueServedAgainItsDeriveHealsOneFrame(t *testing.T) {
	panelType := mustContract(declareLocal("app.panel", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())},
	}))
	panel := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(panelType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			label := self.Get(reloadPure, jsonObject()).Member("label")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"text": self.Derive([]any{label}, func(values []any) (delta.JsonValue, error) { return "panel " + values[0].(string), nil }),
			}}}, nil
		}, ServeOptions{})
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	app := func(label string) HostFacet {
		return HostFacet{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, label, 1, nil) })}
	}
	mustLoad(t)(host.Load([]HostFacet{app("v1"), {Name: "@test/panel", Facet: panel}}, background))
	view := mustGet(t, host, panelType, jsonObject())
	watch, err := view.Member("text").Watch(background)
	must(t, err)
	text := nexttest.ChangesOf(watch)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/panel", Facet: panel}}, background, ReloadOptions{}))
	if state := stateOfRef(view); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	if got := memberOf(t, view, "text"); got != "panel v1" {
		t.Fatalf("text = %v, want panel v1", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{app("v2"), {Name: "@test/panel", Facet: panel}}, background, ReloadOptions{}))
	scopeEventually(t, "the panel's text heals", func() bool {
		value, err := view.Member("text").Value()
		return err == nil && value == "panel v2"
	})
	deliveredOn(host)
	if got := nexttest.ValuesOf(text.Items()); !reflect.DeepEqual(got, []any{"panel v2"}) {
		t.Fatalf("text frames = %v, want [panel v2]", got)
	}
}

func TestReloadFatesRetriesAFailedObjectOnTheNewCode(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(reloadPure, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("bug")
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustHold(t, host, reloadPure, jsonObject())
	failed := untilKind(t, pure, RefStateFailed)
	if failed.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", failed)
	}
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "fixed", 1, nil) }))
	reloadWantObjects(t, report, ReloadedObject{Object: "app.pure", Kind: ReloadRetried})
	untilKind(t, pure, RefStateLive)
	reloadUntilLabel(t, pure, "fixed")
}

func TestReloadFatesReportsARestartParkedWhoseNewFactoryNeedsAMissingDependencyItGoesLiveOnceThatIsServed(t *testing.T) {
	dep := mustContract(declareLocal("app.dep", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("name", contracts.Str())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	needsDep := func(h FacetHost) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			name := self.Get(dep, jsonObject()).Member("name")
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": constant(0.0),
					"label": self.Derive([]any{name}, func(values []any) (delta.JsonValue, error) {
						return "with " + values[0].(string), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return 0.0, nil
				}},
			}}, nil
		}, ServeOptions{})
	}
	report := reloadMust(t)(reloadApp(host, needsDep))
	if len(report.Objects) != 1 || report.Objects[0].Object != "app.pure" || report.Objects[0].Kind != ReloadParked ||
		report.Objects[0].Cause.Kind != data.FailureWaiting || !strings.Contains(report.Objects[0].Cause.Error.Message, "app.dep") {
		t.Fatalf("report objects = %+v, want app.pure parked waiting for app.dep", report.Objects)
	}
	if state := stateOfRef(pure); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	_, err := pure.Call("bump", nil, background)
	wantChordError(t, err, data.CodeUnavailable, "", "")
	reloadMust(t)(host.Reload([]HostFacet{
		{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return needsDep(h) })},
		{Name: "@test/dep", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(dep, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"name": constant("dep"),
				}}}, nil
			}, ServeOptions{})
		})},
	}, background, ReloadOptions{}))
	untilKind(t, pure, RefStateLive)
	reloadUntilLabel(t, pure, "with dep")
}

// --- reload: a restart's old attempt (rules 1-4) ---------------------------------------------------------------------------

func TestReloadARestartLetsARunningCallFinishOnTheOldAttemptsHandlesANewCallWaitsAtTheGateForTheNewCode(t *testing.T) {
	held := newReloadGate()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	old := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v2", 10, nil) }))
	// The old call commits through its old handle after the commit.
	held.open.fire()
	if got := old.wait(t); got != 1.0 {
		t.Fatalf("old call = %v, want 1", got)
	}
	if got := reloadBump(t, pure); got != 11.0 {
		t.Fatalf("new call = %v, want 11", got)
	}
}

func TestReloadARestartOldWorkWritesADocumentWhoseVersionTheReloadRaisesTheNewCodeMigratesWhatItWrote(t *testing.T) {
	v2, err := documents.DefineState("doc", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int()), contracts.Prop("unit", contracts.Str())),
		Version: 2,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0, "unit", "") },
		Migrate: func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
			return jsonObject("n", old.Value("n"), "unit", "pcs"), nil
		},
	})
	must(t, err)
	v1, err := documents.DefineState("doc", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	must(t, err)
	serve := func(h FacetHost, definition *documents.StateDefinition, queue *reloadGateQueue) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, definition)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
					// JSON.stringify(d): the document's keys in order.
					"label": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						// JSON.stringify(d): the document's keys in order (n, then unit when it has one).
						doc := values[0].(*delta.JsonObject)
						if doc.Has("unit") {
							return fmt.Sprintf(`{"n":%v,"unit":%q}`, doc.Value("n"), doc.Value("unit")), nil
						}
						return fmt.Sprintf(`{"n":%v}`, doc.Value("n")), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if held := queue.shift(); held != nil {
						held.entered.fire()
						held.open.await(t, ctx)
					}
					if err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx); err != nil {
						return nil, err
					}
					return doc.Value().Value("n"), nil
				}},
			}}, nil
		}, ServeOptions{})
	}
	held := newReloadGate()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serve(h, v1, reloadGates(nil, held)) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	reloadBump(t, pure)
	old := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return serve(h, v2, nil) }))
	turn(host)
	// The new factory has not run: the old attempt's documents are as the old code declared them.
	if got := memberOf(t, pure, "label"); got != `{"n":1}` {
		t.Fatalf("label = %v, want {\"n\":1}", got)
	}
	held.open.fire()
	if got := old.wait(t); got != 2.0 {
		t.Fatalf("old call = %v, want 2", got)
	}
	reloadUntilLabel(t, pure, `{"n":2,"unit":"pcs"}`)
	if got := reloadBump(t, pure); got != 3.0 {
		t.Fatalf("new call = %v, want 3", got)
	}
}

func TestReloadARestartBindsTheNewAttemptsRequestsBeforeReleasingTheOldOnesADependencyHeldOnlyByItNeverEnds(t *testing.T) {
	depType := mustContract(declareLocal("app.dep", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{
			contracts.Mem("n", contracts.Int()),
			contracts.Mem("set", contracts.NewMethod(contracts.Obj(contracts.Prop("n", contracts.Int())))),
		},
	}))
	dep := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(depType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, reloadCounterDoc)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					n := args.(*delta.JsonObject).Value("n")
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", n) }, ctx)
				}},
			}}, nil
		}, ServeOptions{})
	})
	app := func(label string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				held := self.Get(depType, jsonObject("id", 1.0))
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"n":     self.Derive([]any{held.Member("n")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
						"label": constant(label),
					},
					Methods: map[string]contracts.MethodFunction{"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := held.Call("set", jsonObject("n", 7.0), ctx)
						return 7.0, err
					}},
				}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: app("v1")}, {Name: "@test/dep", Facet: dep}}, background))
	pure := mustGet(t, host, reloadPure, jsonObject())
	reloadBump(t, pure)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: app("v2")}, {Name: "@test/dep", Facet: dep}}, background, ReloadOptions{}))
	reloadUntilLabel(t, pure, "v2")
	// The dependency's in-memory document is still there: its object never ended in between.
	if got := memberOf(t, pure, "n"); got != 7.0 {
		t.Fatalf("n = %v, want 7", got)
	}
}

func TestReloadARestartReBindsMembersByNameOneFrameFromTheLastValueAndRebuildsInternalDerives(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, nil) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	labelWatch, err := pure.Member("label").Watch(background)
	must(t, err)
	label := nexttest.FramesOf(labelWatch)
	nWatch, err := pure.Member("n").Watch(background)
	must(t, err)
	n := nexttest.ChangesOf(nWatch)
	reloadBump(t, pure)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v2", 1, nil) }))
	scopeEventually(t, "the label's frame", func() bool { deliveredOn(host); return label.Len() > 0 })
	deliveredOn(host)
	frames := label.Items()
	if len(frames) != 1 || frames[0].Value != "v2" || len(frames[0].Ops) != 1 || !reflect.DeepEqual([]any(frames[0].Ops[0]), []any{"r", "v2"}) {
		t.Fatalf("label frames = %+v, want one value v2 with ops [[r v2]]", frames)
	}
	// Its derive over the same document gave the same value: no frame besides the commit's.
	if got := nexttest.ValuesOf(n.Items()); !reflect.DeepEqual(got, []any{1.0}) {
		t.Fatalf("n frames = %v, want [1]", got)
	}
}

func TestReloadARestartStopsItsRoutinesAtOnceTheirCallsDrainBeforeTheNewOnesStart(t *testing.T) {
	var mu sync.Mutex
	var events []string
	push := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, text)
	}
	release := newSignal()
	serve := func(h FacetHost, label string) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.OnLive(func(ctx Ctx) *Handled {
				push(label + " started")
				return Awaiting(ctx.(*callContext).host.loop(), func() error {
					UntilAborted(ctx)
					if label == "v1" {
						<-release
					}
					push(label + " stopped")
					return nil
				})
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"n": constant(0.0), "label": constant(label)},
				Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 0.0, nil }},
			}}, nil
		}, ServeOptions{})
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serve(h, "v1") }, loadedOptions{})
	defer closeHosts(t, host)
	mustGet(t, host, reloadPure, jsonObject())
	turn(host)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return serve(h, "v2") }))
	turn(host)
	read := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
	if got := read(); !reflect.DeepEqual(got, []string{"v1 started"}) {
		t.Fatalf("events = %v, want [v1 started]", got)
	}
	close(release)
	scopeEventually(t, "the old routine stops", func() bool { return len(read()) >= 2 })
	scopeEventually(t, "the new routines start", func() bool { return len(read()) == 3 })
	if got := read(); !reflect.DeepEqual(got, []string{"v1 started", "v1 stopped", "v2 started"}) {
		t.Fatalf("events = %v, want [v1 started v1 stopped v2 started]", got)
	}
}

func TestReloadARestartStartsAWritableAtItsNewInitialValueAndReplacesTheContributionsInOneBatch(t *testing.T) {
	tools, err := registries.DefineRegistry[any, any]("app.tools", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	var mu sync.Mutex
	var compositions [][]any
	registry := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(tools, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				var value []any
				for _, entry := range table {
					value = append(value, entry.Value.([]any)...)
				}
				mu.Lock()
				compositions = append(compositions, value)
				mu.Unlock()
				return value, nil
			},
		}, ServeOptions{})
	})
	app := func(tool string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(reloadPure, func(_ *delta.JsonObject, own Self) (contracts.Implementation, error) {
				list := own.Writable([]any{tool})
				own.(StoreSelf).Contribute(tools, list)
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(0.0), "label": constant(tool)},
					Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
						return float64(len(list.Value().([]any))), nil
					}},
				}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: app("a")}, {Name: "@test/tools", Facet: registry}}, background))
	value, err := host.Registry(background, registries.Registry[any, any](tools), GetOptions{})
	must(t, err)
	read := func() any {
		var current any
		host.(*chordHost).enter(func() { current = value.Value() })
		return current
	}
	if got := read(); !reflect.DeepEqual(got, []any{"a"}) {
		t.Fatalf("tools = %v, want [a]", got)
	}
	mu.Lock()
	compositions = nil
	mu.Unlock()
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: app("b")}, {Name: "@test/tools", Facet: registry}}, background, ReloadOptions{}))
	scopeEventually(t, "the tools recompose", func() bool { return reflect.DeepEqual(read(), []any{"b"}) })
	// One composition with the new table, never one without a contribution in between.
	mu.Lock()
	got := compositions
	mu.Unlock()
	if !reflect.DeepEqual(got, [][]any{{"b"}}) {
		t.Fatalf("compositions = %v, want [[b]]", got)
	}
}

// --- reload: a stuck routine call past the drain ---------------------------------------------------------------------------

func TestReloadAStuckRoutineCallPastTheDrainAnOldRoutineCallStuckPastTheReloadDrainBoundDrainTimeoutTheOldAttemptSealedItsCommitRefused(t *testing.T) {
	stuck := newSignal()
	t.Cleanup(stuck.fire)
	late := make(chan error, 1)
	loop := newScopeLoop()
	closeTimeout := time.Second
	serve := func(h FacetHost, label string) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, reloadCounterDoc)
			self.OnLive(func(ctx Ctx) *Handled {
				if label != "v1" {
					return nil
				}
				return Awaiting(ctx.(*callContext).host.loop(), func() error {
					<-stuck
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						draft, err := tx.Draft(doc.s)
						if err != nil {
							return nil, err
						}
						return nil, draft.Set("n", 99.0)
					}, ctx)
					late <- err
					return nil
				})
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
					"label": constant(label),
				},
				Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 0.0, nil }},
			}}, nil
		}, ServeOptions{})
	}
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return serve(h, "v1") },
		loadedOptions{Loop: loop, CloseTimeout: &closeTimeout})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	turn(host)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return serve(h, "v2") }))
	advanceSettled(loop, host, 1_000)
	var kinds []HostErrorKind
	for _, err := range errs.All() {
		kinds = append(kinds, err.Kind)
	}
	if !reloadContainsKind(kinds, ErrDrainTimeout) {
		t.Fatalf("error kinds = %v, want drain_timeout among them", kinds)
	}
	stuck.fire()
	wantChordError(t, receive(t, late), data.CodeReleased, "", "")
	if got := memberOf(t, pure, "n"); got != 0.0 {
		t.Fatalf("n = %v, want 0", got)
	}
}

// reloadContainsKind is whether kinds has kind.
func reloadContainsKind(kinds []HostErrorKind, kind HostErrorKind) bool {
	return slices.Contains(kinds, kind)
}

// --- reload: GC -----------------------------------------------------------------------------------------------------------

func TestReloadGCCollectsARestartsOldAttemptAndItsClosuresOnceItEnded(t *testing.T) {
	held := newReloadGate()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) }, loadedOptions{})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	old := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	var swapped weak.Pointer[attempt]
	func() {
		object := reloadObjectOf(pure)
		host.(*chordHost).enter(func() { swapped = weak.Make(object.phase.attempt) })
	}()
	// The reload starts a restart: the live attempt is the old one, which the object lets go of once it ended.
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v2", 1, nil) }))
	held.open.fire()
	old.wait(t)
	turn(host)
	nexttest.ExpectCollected(t, swapped)
	untilKind(t, pure, RefStateLive)
}

func TestReloadGCCollectsARemovedTypesObjectsItsProviderItsRegistryEntriesAndTheOldFacetsCode(t *testing.T) {
	names, err := registries.DefineRegistry[any, any]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	var facet weak.Pointer[facetDefinition]
	var removed weak.Pointer[chordObject]
	// Loads the plugin; only the host keeps its facet.
	func() {
		made := DefineFacet(func(h FacetHost, self FacetSelf) error {
			if err := reloadServePure(t, h, "v1", 1, nil); err != nil {
				return err
			}
			if err := h.Serve(names, registries.RegistryProvider[any, any]{
				Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
					values := make([]any, 0, len(table))
					for _, entry := range table {
						values = append(values, entry.Value)
					}
					return values, nil
				},
			}, ServeOptions{}); err != nil {
				return err
			}
			return self.Contribute(names, "x")
		})
		facet = weak.Make(made.definition)
		mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: made}}, background))
	}()
	func() {
		pure := mustGet(t, host, reloadPure, jsonObject())
		object := reloadObjectOf(pure)
		removed = weak.Make(object)
		pure.Release()
	}()
	// The object lives on while the host keeps it (a type with empty params lives as long as the host).
	reloadMust(t)(host.Reload(nil, background, ReloadOptions{}))
	turn(host)
	nexttest.ExpectCollected(t, removed)
	nexttest.ExpectCollected(t, facet)
}

// --- reload: a parked attempt's handover (fix round 1) --------------------------------------------------------------------

func TestReloadAParkedAttemptsHandoverKeepsADependencyHeldOnlyByAParkedAttemptAliveThroughReload(t *testing.T) {
	a := mustContract(declareLocal("astra.a", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	dep := mustContract(declareLocal("astra.dep", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{},
	}))
	missing := mustContract(declareLocal("astra.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	var mu sync.Mutex
	opens, closes := 0, 0
	counts := func() [2]int {
		mu.Lock()
		defer mu.Unlock()
		return [2]int{opens, closes}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	depFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(dep, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				mu.Lock()
				opens++
				mu.Unlock()
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error {
				mu.Lock()
				closes++
				mu.Unlock()
				return nil
			})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})
	aFacet := func() Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(a, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Get(dep, jsonObject("id", 1.0))
				self.Get(missing, jsonObject())
				return contracts.Implementation{}, nil
			}, ServeOptions{})
		})
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/dep", Facet: depFacet}, {Name: "@astra/a", Facet: aFacet()}}, background))
	turn(host)
	scopeEventually(t, "the dependency opens", func() bool { return counts()[0] == 1 })
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@astra/dep", Facet: depFacet}, {Name: "@astra/a", Facet: aFacet()}}, background, ReloadOptions{}))
	turn(host)
	seen := counts()
	closeHosts(t, host)
	if seen != [2]int{1, 0} {
		t.Fatalf("opens/closes = %v, want [1 0]", seen)
	}
}

func TestReloadAParkedAttemptsHandoverBindsReplacementRequestsBeforeReleasingTheParkedPredecessors(t *testing.T) {
	a := mustContract(declareLocal("sol.parked", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	b := mustContract(declareLocal("sol.native", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{},
	}))
	missing := mustContract(declareLocal("sol.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	var mu sync.Mutex
	opens, closes := 0, 0
	counts := func() [2]int {
		mu.Lock()
		defer mu.Unlock()
		return [2]int{opens, closes}
	}
	bFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(b, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				mu.Lock()
				opens++
				mu.Unlock()
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error {
				mu.Lock()
				closes++
				mu.Unlock()
				return nil
			})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})
	aFacet := func() Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(a, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Get(b, jsonObject("id", 1.0))
				self.Get(missing, jsonObject())
				return contracts.Implementation{}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@sol/b", Facet: bFacet}, {Name: "@sol/a", Facet: aFacet()}}, background))
	scopeEventually(t, "b opens", func() bool { return counts() == [2]int{1, 0} })
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@sol/b", Facet: bFacet}, {Name: "@sol/a", Facet: aFacet()}}, background, ReloadOptions{}))
	deliveredOn(host)
	turn(host)
	if got := counts(); got != [2]int{1, 0} {
		t.Fatalf("opens/closes = %v, want [1 0]", got)
	}
}

// --- reload: two reloads in a row -------------------------------------------------------------------------------------------

func TestReloadTwoReloadsInARowTheSecondDoesNotWaitForTheFirstsOldCallWhichFinishesOnItsOldCodeTheLastCodeServes(t *testing.T) {
	held := newReloadGate()
	closeTimeout := time.Minute
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) },
		loadedOptions{CloseTimeout: &closeTimeout})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	old := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v2", 10, nil) }))
	// Its report does not wait for the old attempt's drain.
	reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v3", 100, nil) }))
	// The old attempt still runs v1's call: the reference sees it live meanwhile.
	if got := memberOf(t, pure, "label"); got != "v1" {
		t.Fatalf("label = %v, want v1 (the old attempt still shown)", got)
	}
	held.open.fire()
	if got := old.wait(t); got != 1.0 {
		t.Fatalf("old call = %v, want 1", got)
	}
	if got := reloadBump(t, pure); got != 101.0 {
		t.Fatalf("new call = %v, want 101", got)
	}
	reloadUntilLabel(t, pure, "v3")
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
}

// --- reload: a reference detaching from a parked restart's old attempt (Opus G1) ------------------------------------------

func TestReloadAReferenceDetachingFromAParkedRestartsOldAttemptFollowsTheOldInstanceWhileItsCallDrainsThenKeepsItsLastValueWithTheFailureOnce(t *testing.T) {
	missing := mustContract(declareLocal("app.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	held := newReloadGate()
	closeTimeout := time.Minute
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) },
		loadedOptions{CloseTimeout: &closeTimeout})
	defer closeHosts(t, host)
	pure := mustGet(t, host, reloadPure, jsonObject())
	nWatch, err := pure.Member("n").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(nWatch)
	bumping := startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	parked := func(h FacetHost) error {
		return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Get(missing, jsonObject())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"n": constant(9.0), "label": constant("v2")},
				Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 0.0, nil }},
			}}, nil
		}, ServeOptions{})
	}
	report := reloadMust(t)(reloadApp(host, parked))
	if len(report.Objects) == 0 || report.Objects[0].Kind != ReloadParked {
		t.Fatalf("report objects = %+v, want the first parked", report.Objects)
	}
	if state := stateOfRef(pure); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	// The old attempt's call still commits: the reference follows it until it ended.
	held.open.fire()
	if got := bumping.wait(t); got != 1.0 {
		t.Fatalf("old call = %v, want 1", got)
	}
	scopeEventually(t, "the failure reaches the watch", func() bool { deliveredOn(host); return calls.Len() >= 2 })
	deliveredOn(host)
	items := calls.Items()
	kinds := make([]chord.WatchCallKind, len(items))
	for index, call := range items {
		kinds[index] = call.Kind
	}
	if got := memberOf(t, pure, "n"); got != 1.0 || !reflect.DeepEqual(kinds, []chord.WatchCallKind{chord.WatchCallValue, chord.WatchCallFailed}) ||
		nexttest.ValuesOf(items)[0] != 1.0 {
		t.Fatalf("n %v, call kinds %v, first value %v, want 1, [value failed], 1", memberOf(t, pure, "n"), kinds, nexttest.ValuesOf(items)[0])
	}
}

// --- a live failure: an older version's mapping follows the admitted commits (final review) -------------------------------

func TestALiveFailureAnOlderVersionsMappingFollowsTheAdmittedCommitsStopsAnOlderReferenceAfterItsInstancesAdmittedFinalCommitPublishedAstraG1(t *testing.T) {
	v1 := mustContract(declareLocal("app.admitted", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	v2, err := contracts.Evolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("n", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	must(t, err)
	state := objCounterPersisted
	chordStore, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	var own *singletonDoc
	var self Self
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(v2, func(_ *delta.JsonObject, declared Self) (contracts.Implementation, error) {
			mu.Lock()
			defer mu.Unlock()
			self = declared
			own = &singletonDoc{s: declared.(StoreSelf).State(state).(*documents.Singleton)}
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": own.Member(),
			}}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			State: []contracts.Entry[contracts.StateMapping]{{Name: "n", Value: contracts.StateMapping{
				Func: func(value delta.JsonValue) (delta.JsonValue, error) { return value.(*delta.JsonObject).Value("n"), nil },
			}}},
		}}}})
	})
	host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/admitted", Facet: facet}}, background))
	old := mustGet(t, host, v1, jsonObject())
	current := mustGet(t, host, v2, jsonObject())
	watch, err := old.Member("n").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	// A commit admitted before the failure: v1's mapping and v2's member both get it, then the failure.
	held := holdNextCommit()
	mu.Lock()
	doc, failing := own, self
	mu.Unlock()
	changed := make(chan error, 1)
	go func() {
		changed <- doc.Change(func(draft *delta.Object) { _ = draft.Set("n", 5.0) }, background)
	}()
	receive(t, held.Entered())
	failLive(failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	held.Release()
	must(t, receive(t, changed))
	scopeEventually(t, "the failure reaches the old watch", func() bool { deliveredOn(host); return calls.Len() >= 2 })
	deliveredOn(host)
	gotOld, _ := old.Member("n").Value()
	gotCurrent, _ := current.Member("n").Value()
	want := []any{5.0, nexttest.FailedValue{Failed: "boom"}}
	if !reflect.DeepEqual(gotCurrent, jsonObject("n", 5.0)) || gotOld != 5.0 || !reflect.DeepEqual(nexttest.ValuesOf(calls.Items()), want) {
		t.Fatalf("old %v, current %v, calls %v, want 5, {n:5}, %v", gotOld, gotCurrent, nexttest.ValuesOf(calls.Items()), want)
	}
	old.Release()
	current.Release()
	must(t, chordStore.Close(background))
}

// --- a native restart: references take their last values after the admitted commits (final review) -------------------------

func TestANativeRestartReferencesTakeTheirLastValuesAfterTheAdmittedCommitsKeepsTheValueOfACommitAdmittedBeforeTheRestartsSealWhileTheNewInstanceOpensSolG1(t *testing.T) {
	typ := mustContract(declareLocal("app.restart-admitted", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("x", contracts.Obj(contracts.Prop("n", contracts.Int())))},
	}))
	chordStore, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	var counter *singletonDoc
	opening := newSignal()
	t.Cleanup(opening.fire)
	facet := func(open func(ctx context.Context) error) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				mu.Lock()
				counter = &singletonDoc{s: self.(StoreSelf).State(objCounterPersisted).(*documents.Singleton)}
				current := counter
				mu.Unlock()
				self.Open(func(ctx context.Context) (any, error) { return nil, open(ctx) },
					func(any, NativeCloseReason, context.Context) error { return nil })
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"x": current.Member(),
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{Store: chordStore, OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/restart", Facet: facet(func(context.Context) error { return nil })}}, background))
	ref := mustGet(t, host, typ, jsonObject())
	// A commit admitted to the store (no running call's promise), then a restart whose new native stays opening.
	held := holdNextCommit()
	mu.Lock()
	outgoing := counter
	mu.Unlock()
	changed := make(chan error, 1)
	go func() {
		changed <- outgoing.Change(func(draft *delta.Object) { _ = draft.Set("n", 1.0) }, background)
	}()
	receive(t, held.Entered())
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/restart", Facet: facet(func(ctx context.Context) error {
		select {
		case <-opening:
		case <-ctx.Done():
		}
		return nil
	})}}, background, ReloadOptions{}))
	deliveredOn(host)
	held.Release()
	must(t, receive(t, changed))
	deliveredOn(host)
	scopeEventually(t, "the reference keeps the admitted commit's value", func() bool {
		value, err := ref.Member("x").Value()
		return err == nil && reflect.DeepEqual(value, jsonObject("n", 1.0))
	})
	if state := stateOfRef(ref); state.Kind != RefStateLoading {
		t.Fatalf("state = %+v, want loading", state)
	}
	opening.fire()
	must(t, chordStore.Close(background))
}

// Mirrors "parked on dependencies › walks a diamond on one host once: a shared parked dependency passes a retry on once (audit A5)".
// Pi spies `Attempt.prototype.retryDependencies` (4 calls: Top, both Lefts, the shared Mid once). Go has no prototype spies, so
// the walk's visited set is read: every object the retry reached (Core, which is failed, is retried, not passed on), each once.
func TestObjectParkedWalksADiamondOnOneHostOnceASharedParkedDependencyPassesARetryOnOnceAudit5(t *testing.T) {
	typed := func(name string, params contracts.Params) *contracts.Contract {
		return mustContract(declareLocal(name, contracts.LocalDeclaration{
			Version: 1, Params: params, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
		}))
	}
	top, mid := typed("app.top", contracts.Params{}), typed("app.mid", contracts.Params{})
	left := typed("app.left", contracts.Params{contracts.Prop("id", contracts.Int())})
	var mu sync.Mutex
	cores := 0
	reads := func(self Self, typ *contracts.Contract, params *delta.JsonObject) contracts.Implementation {
		return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
			"n": Expose(self.Get(typ, params).Member("n")),
		}}}
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(objCore, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			mu.Lock()
			defer mu.Unlock()
			if cores++; cores == 1 {
				return contracts.Implementation{}, errors.New("bug")
			}
			return nOnly(float64(cores)), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(mid, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return reads(self, objCore, jsonObject()), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(left, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return reads(self, mid, jsonObject()), nil
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
	}, loadedOptions{})
	defer closeHosts(t, host)
	held := mustHold(t, host, top, jsonObject())
	untilKind(t, held, RefStateFailed)
	walk := newRetryWalk(link.RETRY_HOPS)
	host.(*chordHost).enter(func() { held.binding.target().retry(walk) })
	var labels []string
	for object := range walk.visited {
		labels = append(labels, object.text)
	}
	sort.Strings(labels)
	if want := []string{"app.core", "app.left(1)", "app.left(2)", "app.mid", "app.top"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("the retry reached %v, want %v", labels, want)
	}
	untilKind(t, held, RefStateLive)
	mu.Lock()
	made := cores
	mu.Unlock()
	if got := memberOf(t, held, "n"); made != 2 || got != 4.0 {
		t.Fatalf("cores %d, n %v, want 2 and 4", made, got)
	}
}
