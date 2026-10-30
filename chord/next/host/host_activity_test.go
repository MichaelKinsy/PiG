package host

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
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
