package host

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/activity.test.ts for the part that needs no host. Every case of upstream's file runs
// on a host (createHost, linked server and client hosts, serveNotes, defineFacet, host.get/hold): they belong to the host
// core and the remote lane. These are own tests of `activity` on a batch and a fake loop with the behavior the cases that
// are about the source itself assert, each naming its case: the one publication per microtask turn, the newest counts for a
// slow watcher, nothing scheduled while nobody follows it, derives over it, no jobs.

// countingLoop is a fakeLoop that counts the microtasks queued (`vi.spyOn(globalThis, "queueMicrotask")`).
type countingLoop struct {
	*fakeLoop
	queued int
}

func (l *countingLoop) Queue(job func()) {
	l.queued++
	l.fakeLoop.Queue(job)
}

// activityHarness is an activity on a batch whose watches deliver on a scheduler of their own.
type activityHarness struct {
	t         *testing.T
	loop      *countingLoop
	scheduler *chord.DeliveryScheduler
	batch     *state.Batch
	activity  *activity
}

func newActivityHarness(t *testing.T) *activityHarness {
	t.Helper()
	h := &activityHarness{t: t, loop: &countingLoop{fakeLoop: newFakeLoop()}, scheduler: chord.NewDeliveryScheduler()}
	h.batch = state.NewBatch(func(err state.BatchError) { t.Errorf("batch reported %s: %v", err.Kind, err.Error) }, nil).UseScheduler(h.scheduler)
	h.activity = newActivity(h.loop, h.batch)
	return h
}

// turn runs the microtasks queued so far, as an `await` lets them run, and waits for the frames they made.
func (h *activityHarness) turn() {
	h.t.Helper()
	h.loop.Flush()
	h.scheduler.Wait()
}

func (h *activityHarness) watch() chord.WatchHandle[delta.JsonValue] {
	h.t.Helper()
	watch, err := h.activity.Watch(chordctx.Background())
	if err != nil {
		h.t.Fatalf("Watch: %v", err)
	}
	return watch
}

// countsText is the JSON of a counts value, as `{ references: 0, calls, jobs: 0, lingering: 0 }`.
func activityCountsText(calls int) string {
	return fmt.Sprintf(`{"references":0,"calls":%d,"jobs":0,"lingering":0}`, calls)
}

func activityValueText(t *testing.T, a *activity) string {
	t.Helper()
	value, err := a.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	return nexttest.JSONText(t, value)
}

// Mirrors "publishes once per microtask turn with changes; a slow watcher gets the newest counts".
func TestActivityPublishesOncePerMicrotaskTurnWithChanges(t *testing.T) {
	h := newActivityHarness(t)
	frames := nexttest.FramesOf(h.watch())
	// Ten calls started in one turn: ten changes, one publication; they settle in a later turn: one more.
	for range 10 {
		h.activity.count(activityCalls, 1)
	}
	if h.loop.queued != 1 {
		t.Fatalf("microtasks after ten changes = %d, want 1", h.loop.queued)
	}
	h.turn()
	for range 10 {
		h.activity.count(activityCalls, -1)
	}
	if h.loop.queued != 2 {
		t.Fatalf("microtasks after the next ten changes = %d, want 2", h.loop.queued)
	}
	h.turn()
	got := frames.Items()
	if len(got) != 2 {
		t.Fatalf("frames = %d, want 2", len(got))
	}
	for i, calls := range []int{10, 0} {
		if text := nexttest.JSONText(t, got[i].Value); text != activityCountsText(calls) {
			t.Errorf("frame %d value = %s, want %s", i, text, activityCountsText(calls))
		}
		if len(got[i].Ops) != 1 || !reflect.DeepEqual(got[i].Ops[0], delta.Op{"r", got[i].Value}) {
			t.Errorf("frame %d ops = %v, want [[r value]]", i, got[i].Ops)
		}
	}
}

// Mirrors "gives a slow watcher the newest counts once it is free: its pending frames fold".
func TestActivityGivesASlowWatcherTheNewestCountsOnceItIsFree(t *testing.T) {
	h := newActivityHarness(t)
	watch := h.watch()
	gate := make(chan struct{})
	var seen nexttest.Collector[string]
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		if seen.Append(nexttest.JSONText(t, call.Value)) == 1 {
			<-gate
		}
		return nil
	})
	h.activity.count(activityCalls, 1)
	h.loop.Flush()
	if err := h.scheduler.AwaitEntered(chordctx.Background()); err != nil {
		t.Fatalf("AwaitEntered: %v", err)
	}
	// More publications while the watcher is busy with the first: the bumps settle and two more run, ending at 0.
	h.activity.count(activityCalls, -1)
	h.loop.Flush()
	for range 2 {
		h.activity.count(activityCalls, 1)
		h.loop.Flush()
		h.activity.count(activityCalls, -1)
		h.loop.Flush()
	}
	close(gate)
	h.scheduler.Wait()
	if got, want := seen.Items(), []string{activityCountsText(1), activityCountsText(0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seen = %v, want %v", got, want)
	}
}

// Mirrors "schedules nothing while nobody watches or derives from it; a later watch reads the counts then".
func TestActivitySchedulesNothingWhileNobodyFollowsIt(t *testing.T) {
	h := newActivityHarness(t)
	h.activity.count(activityCalls, 1)
	h.activity.count(activityCalls, -1)
	h.activity.count(activityCalls, 1)
	h.loop.Flush()
	if h.loop.queued != 0 {
		t.Fatalf("microtasks queued with no watch = %d, want 0", h.loop.queued)
	}
	watch := h.watch()
	if text := nexttest.JSONText(t, watch.Value()); text != activityCountsText(1) {
		t.Fatalf("a later watch reads %s, want %s", text, activityCountsText(1))
	}
	h.activity.count(activityCalls, -1)
	if h.loop.queued != 1 {
		t.Fatalf("microtasks queued once watched = %d, want 1", h.loop.queued)
	}
	h.turn()
}

// Mirrors "feeds derives (base code holding the host): they recompute in the publishing batch".
func TestActivityFeedsDerivesWhichRecomputeInThePublishingBatch(t *testing.T) {
	h := newActivityHarness(t)
	busy := state.NewDerive([]any{h.activity}, func(values []any) (delta.JsonValue, error) {
		return values[0].(*delta.JsonObject).Value("calls").(float64) > 0, nil
	}, h.batch)
	if err := busy.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil }, func(err error) { t.Errorf("derive reported %v", err) }); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	watch, err := busy.Watch(chordctx.Background())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	frames := nexttest.FramesOf(watch)
	h.activity.count(activityCalls, 1)
	h.turn()
	h.activity.count(activityCalls, -1)
	h.turn()
	var values []any
	for _, frame := range frames.Items() {
		values = append(values, frame.Value)
	}
	if want := []any{true, false}; !reflect.DeepEqual(values, want) {
		t.Fatalf("derive frames = %v, want %v", values, want)
	}
	if value, err := busy.Value(); err != nil || value != false {
		t.Fatalf("derive value = %v, %v; want false, nil", value, err)
	}
}

// Mirrors "counts no jobs before slice 5", and the four counters: each kind adds to its own count.
func TestActivityCountsEachKindApartAndNoJobsBeforeSlice5(t *testing.T) {
	h := newActivityHarness(t)
	if got, want := activityValueText(t, h.activity), `{"references":0,"calls":0,"jobs":0,"lingering":0}`; got != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
	h.activity.count(activityReferences, 2)
	h.activity.count(activityCalls, 3)
	h.activity.count(activityLingering, 1)
	if got, want := activityValueText(t, h.activity), `{"references":2,"calls":3,"jobs":0,"lingering":1}`; got != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
	h.activity.count(activityJobs, 1)
	h.activity.count(activityReferences, -2)
	if got, want := activityValueText(t, h.activity), `{"references":0,"calls":3,"jobs":1,"lingering":1}`; got != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
}

// Own test of `#value ??=`: the value is one object until the next change.
func TestActivityValueIsOneObjectUntilTheNextChange(t *testing.T) {
	h := newActivityHarness(t)
	first, _ := h.activity.Value()
	again, _ := h.activity.Value()
	if first != again {
		t.Fatal("a second read built another object")
	}
	h.activity.count(activityCalls, 1)
	next, _ := h.activity.Value()
	if next == first {
		t.Fatal("a read after a change returned the old object")
	}
}

// --- upstream activity.test.ts cases that run on hosts (createHost, linked server and client, serveNotes) ----------

// Kept is a remote type with a linger on the server (activity.test.ts `Kept`).
var Kept = mustContract(declareRemote("app.kept", contracts.RemoteDeclaration{
	Version:     1,
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
	Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
}))

// activityOf is host.activity.value: the counts now.
func activityOf(host Host) ActivityCounts {
	h := host.(*chordHost)
	var counts ActivityCounts
	h.enter(func() { counts = h.activity.counts })
	return counts
}

// activityCounter is activity.test.ts `Counter`: one method `bump`.
var activityCounter = mustContract(declareLocal("app.counter", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{contracts.Mem("bump", contracts.NewMethod())},
}))

// bumpImplementation is `{ bump: async () => null }`.
func bumpImplementation() (contracts.Implementation, error) {
	return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
	}}}, nil
}

// Mirrors activity.test.ts "host.activity › counts holds arriving over links and served here, not this host's own references".
func TestHostActivityCountsHoldsArrivingOverLinksAndServedHereNotItsOwnReferences(t *testing.T) {
	l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
	defer closeHosts(t, l.Client, l.Server)
	if got := activityOf(l.Server); got != (ActivityCounts{}) {
		t.Fatalf("activity = %+v, want zero", got)
	}
	notes, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	own, err := l.Server.Get(Notes, jsonObject("session", "S2"), background, GetOptions{})
	must(t, err)
	defer own.Release()
	if got := activityOf(l.Server).References; got != 1 {
		t.Fatalf("references = %d, want 1", got)
	}
	notes.Release()
	transported(l.Links[0], l.Links[1])
	if got := activityOf(l.Server).References; got != 0 {
		t.Fatalf("references after release = %d, want 0", got)
	}
}

// Mirrors "counts a hold placed here from its arrival, bound or not: a missing provider keeps the worker alive (M§10)".
func TestHostActivityCountsAHoldPlacedHereFromItsArrivalBoundOrNot(t *testing.T) {
	l := linked(t, func(FacetHost) error { return nil }, linkedOptions{})
	defer closeHosts(t, l.Client, l.Server)
	missing, err := l.Client.Hold(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	transported(l.Links[0], l.Links[1])
	if state := refStateOf(missing); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	if got := activityOf(l.Server).References; got != 1 {
		t.Fatalf("references = %d, want 1", got)
	}
	// Presence lists bound references only.
	if text := nexttest.JSONText(t, sourceValue(t, l.Server, l.Server.References(Notes))); text != "[]" {
		t.Fatalf("presence = %s, want []", text)
	}
	missing.Release()
	transported(l.Links[0], l.Links[1])
	if got := activityOf(l.Server).References; got != 0 {
		t.Fatalf("references after release = %d, want 0", got)
	}
}

// Mirrors "counts method calls running on this host, local and remote, until they settle".
func TestHostActivityCountsMethodCallsRunningOnThisHostUntilTheySettle(t *testing.T) {
	l := linked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, linkedOptions{})
	defer closeHosts(t, l.Client, l.Server)
	remoteRef, err := l.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	localRef, err := l.Server.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
	must(t, err)
	calls := make(chan error, 2)
	for _, ref := range []*Reference{remoteRef, localRef} {
		go func() { _, err := ref.Call("wait", nil, background); calls <- err }()
	}
	awaitActivity(t, l.Server, func(counts ActivityCounts) bool { return counts.Calls == 2 })
	remoteRef.Release()
	localRef.Release()
	<-calls
	<-calls
	transported(l.Links[0], l.Links[1])
	// The aborted handlers are owned tasks: their calls leave the count as they return.
	awaitActivity(t, l.Server, func(counts ActivityCounts) bool { return counts.Calls == 0 })
}

// awaitActivity waits until host.activity's counts satisfy ok, polling at the loop's turns.
func awaitActivity(t testing.TB, host Host, ok func(ActivityCounts) bool) {
	t.Helper()
	h := host.(*chordHost)
	changed := make(chan struct{}, 1)
	var watch chord.WatchHandle[delta.JsonValue]
	var err error
	h.enter(func() { watch, err = h.Activity().Watch(background) })
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	})
	defer func() { _, _ = watch.Stop() }()
	for !ok(activityOf(host)) {
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatalf("activity never satisfied: %+v", activityOf(host))
		}
	}
}

// Mirrors "publishes once per microtask turn with changes; a slow watcher gets the newest counts".
func TestHostActivityPublishesOncePerMicrotaskTurnWithChanges(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(activityCounter, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return bumpImplementation() }, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	counter, err := host.Get(activityCounter, jsonObject(), background, GetOptions{})
	must(t, err)
	watch, err := inSection(host, func() (chord.WatchHandle[delta.JsonValue], error) { return host.Activity().Watch(background) })
	must(t, err)
	frames := nexttest.FramesOf(watch)
	// Ten calls started in one turn: ten changes, one publication. Go form of "they settle in a later turn: one more": the
	// ten handlers are owned tasks, each settling in a section of its own, so the settlements are ten turns (ten
	// publications, the last at 0), where Pi's ten promises resolve in one microtask checkpoint.
	results := startCalls(t, counter, "bump", 10)
	awaitCalls(t, results)
	turn(host)
	got := frames.Items()
	if len(got) < 2 || len(got) > 11 {
		t.Fatalf("frames = %d, want 1 for the ten starts and one per settling turn (2 to 11)", len(got))
	}
	for i, frame := range got {
		if len(frame.Ops) != 1 || !reflect.DeepEqual(frame.Ops[0], delta.Op{"r", frame.Value}) {
			t.Errorf("frame %d ops = %v, want [[r value]]", i, frame.Ops)
		}
	}
	if text := nexttest.JSONText(t, got[0].Value); text != activityCountsText(10) {
		t.Errorf("first frame value = %s, want %s: the ten starts are one publication", text, activityCountsText(10))
	}
	if text := nexttest.JSONText(t, got[len(got)-1].Value); text != activityCountsText(0) {
		t.Errorf("last frame value = %s, want %s", text, activityCountsText(0))
	}
}

// Mirrors "gives a slow watcher the newest counts once it is free: its pending frames fold".
func TestHostActivityGivesASlowWatcherTheNewestCountsOnceItIsFree(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(activityCounter, func(*delta.JsonObject, Self) (contracts.Implementation, error) { return bumpImplementation() }, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	counter, err := host.Get(activityCounter, jsonObject(), background, GetOptions{})
	must(t, err)
	watch, err := inSection(host, func() (chord.WatchHandle[delta.JsonValue], error) { return host.Activity().Watch(background) })
	must(t, err)
	gate := make(chan struct{})
	var seen nexttest.Collector[string]
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		if seen.Append(nexttest.JSONText(t, call.Value)) == 1 {
			<-gate
		}
		return nil
	})
	// Two more publications while the watcher is busy with the first.
	for range 2 {
		_, err := counter.Call("bump", nil, background)
		must(t, err)
		loopTurn(host)
	}
	close(gate)
	turn(host)
	if got, want := seen.Items(), []string{activityCountsText(1), activityCountsText(0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seen = %v, want %v", got, want)
	}
}

// Mirrors "schedules nothing while nobody watches or derives from it; a later watch reads the counts then".
func TestHostActivitySchedulesNothingWhileNobodyWatchesOrDerivesFromIt(t *testing.T) {
	counter := mustContract(declareLocal("app.counter", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("bump", contracts.NewMethod()), contracts.Mem("wait", contracts.NewMethod())},
	}))
	gate := make(chan struct{})
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(gate) }) }
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(counter, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// Pi: `await gate.promise`.
					chordctx.Suspend(ctx)
					<-gate
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	defer closeHosts(t, host)
	defer release()
	ref, err := host.Get(counter, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = ref.Call("bump", nil, background)
	must(t, err)
	waiting := startCalls(t, ref, "wait", 1)
	turn(host)
	// The spy of Pi counts every queueMicrotask; the host's other microtasks share the queue here, so the Go form asks the
	// activity itself: nothing is scheduled while nobody follows it.
	h := host.(*chordHost)
	var scheduled bool
	h.enter(func() { scheduled = h.activity.scheduled })
	if scheduled {
		t.Fatal("a publication was scheduled with no watch")
	}
	watch, err := inSection(host, func() (chord.WatchHandle[delta.JsonValue], error) { return host.Activity().Watch(background) })
	must(t, err)
	if text := nexttest.JSONText(t, watch.Value()); text != `{"references":0,"calls":1,"jobs":0,"lingering":0}` {
		t.Fatalf("a later watch reads %s", text)
	}
	frames := nexttest.FramesOf(watch)
	release()
	awaitCalls(t, waiting)
	turn(host)
	// Once watched, the one settling is one publication (the host's other microtasks share the queue the spy counts, so the
	// Go form counts the activity's own publications).
	if got := frames.Items(); len(got) != 1 || nexttest.JSONText(t, got[0].Value) != activityCountsText(0) {
		t.Fatalf("frames after the watch = %v, want one at 0 calls", got)
	}
}

// Mirrors "feeds derives (base code holding the host): they recompute in the publishing batch".
func TestHostActivityFeedsDerivesWhichRecomputeInThePublishingBatch(t *testing.T) {
	busy := mustContract(declareLocal("app.busy", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("busy", contracts.Bool()), contracts.Mem("work", contracts.NewMethod())},
	}))
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(busy, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"busy": self.Derive([]any{host.Activity()}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("calls").(float64) > 0, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"work": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				},
			}}, nil
		}, ServeOptions{})
	})
	must(t, host.Load([]HostFacet{{Name: "@test/core", Facet: facet}}, background))
	ref, err := host.Get(busy, jsonObject(), background, GetOptions{})
	must(t, err)
	member := ref.Member("busy")
	watch, err := member.Watch(background)
	must(t, err)
	values := nexttest.FramesOf(watch)
	_, err = ref.Call("work", nil, background)
	must(t, err)
	turn(host)
	var got []any
	for _, frame := range values.Items() {
		got = append(got, frame.Value)
	}
	if want := []any{true, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("derive frames = %v, want %v", got, want)
	}
	if value, err := member.Value(); err != nil || value != false {
		t.Fatalf("busy = %v, want false", value)
	}
}

// Mirrors "lingering › counts live objects kept by their type's linger after their last reference, until they end".
func TestHostActivityLingeringCountsLiveObjectsKeptByTheirTypesLinger(t *testing.T) {
	loop := newScopeLoop()
	linger := 1000 * time.Millisecond
	l := linked(t, func(h FacetHost) error {
		return h.Serve(Kept, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(1))},
			}}, nil
		}, ServeOptions{Linger: &linger})
	}, linkedOptions{Loop: loop})
	defer closeHosts(t, l.Client, l.Server)
	kept, err := l.Client.Get(Kept, jsonObject(), background, GetOptions{})
	must(t, err)
	kept.Release()
	transported(l.Links[0], l.Links[1])
	if got := activityOf(l.Server); got.References != 0 || got.Lingering != 1 {
		t.Fatalf("activity = %+v, want references 0 lingering 1", got)
	}
	// A new reference within the linger takes it back.
	again, err := l.Client.Get(Kept, jsonObject(), background, GetOptions{})
	must(t, err)
	if got := activityOf(l.Server); got.References != 1 || got.Lingering != 0 {
		t.Fatalf("activity = %+v, want references 1 lingering 0", got)
	}
	again.Release()
	transported(l.Links[0], l.Links[1])
	if got := activityOf(l.Server).Lingering; got != 1 {
		t.Fatalf("lingering = %d, want 1", got)
	}
	loop.AdvanceAsync(1000)
	if got := activityOf(l.Server).Lingering; got != 0 {
		t.Fatalf("lingering after the linger = %d, want 0", got)
	}
}

// Mirrors "counts no jobs before slice 5".
func TestHostActivityCountsNoJobsOnAHost(t *testing.T) {
	host, _ := loaded(t, func(FacetHost, FacetSelf) error { return nil }, loadedOptions{})
	defer closeHosts(t, host)
	if got := activityOf(host).Jobs; got != 0 {
		t.Fatalf("jobs = %d, want 0", got)
	}
}
