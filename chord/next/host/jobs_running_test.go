package host

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/jobs/running.test.ts: `running`'s list: each change copies the list, keeps the other
// entries identical and gives the ops of exactly that change (a state change re-pathed under its entry); one frame per
// batch; derives over it are marked.

func newRunningList(t *testing.T) (*state.Batch, *chord.DeliveryScheduler, *running) {
	t.Helper()
	scheduler := chord.NewDeliveryScheduler()
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(scheduler)
	return batch, scheduler, newRunning(batch, "app.store job app.echo")
}

func commitRunning(t *testing.T, batch *state.Batch, work func()) {
	t.Helper()
	must(t, batch.Commit(func() error { work(); return nil }, background))
}

func listValue(t *testing.T, list *running) []any {
	t.Helper()
	value, err := list.Value()
	must(t, err)
	return value.([]any)
}

func sameList(a, b []any) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer())
}

// running.test.ts: running's list copies the list per change, keeps other entries identical, and frames apply to the previous value
func TestRunningsListCopiesTheListPerChangeKeepsOtherEntriesIdenticalAndFramesApplyToThePreviousValue(t *testing.T) {
	batch, scheduler, list := newRunningList(t)
	a := jsonObject("id", "a", "phase", "running", "state", jsonObject("output", ""))
	b := jsonObject("id", "b", "phase", "running", "state", jsonObject("output", ""))
	watch, err := list.Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	previous := listValue(t, list)
	commitRunning(t, batch, func() {
		list.add(a)
		list.add(b)
	})
	scheduler.Wait()
	added := listValue(t, list)
	nexttest.RequireJSONEqual(t, added, []any{a, b})
	if sameList(added, previous) {
		t.Fatal("add did not copy the list")
	}
	b2 := jsonObject("id", "b", "phase", "running", "state", jsonObject("output", "x"))
	commitRunning(t, batch, func() { list.stateChanged(b, b2, []delta.Op{{"a", []any{"output"}, "x"}}) })
	scheduler.Wait()
	a2 := jsonObject("id", "a", "phase", "finishing", "state", jsonObject("output", ""))
	commitRunning(t, batch, func() { list.replace(a, a2) })
	scheduler.Wait()
	commitRunning(t, batch, func() { list.remove(a2) })
	scheduler.Wait()
	final := listValue(t, list)
	nexttest.RequireJSONEqual(t, final, []any{b2})
	if final[0] != any(b2) {
		t.Fatal("the other entry is not the identical object")
	}
	got := frames.Items()
	want := [][]delta.Op{
		{{"p", []any{}, 0, 0, []any{a}}, {"p", []any{}, 1, 0, []any{b}}},
		{{"a", []any{1, "state", "output"}, "x"}},
		{{"s", []any{0}, a2}},
		{{"p", []any{}, 0, 1, []any{}}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d frames, want %d", len(got), len(want))
	}
	var applied any = []any{}
	for i, frame := range got {
		nexttest.RequireJSONEqual(t, opsAsAny(frame.Ops), opsAsAny(want[i]))
		// Every frame turns the previous value into the next.
		next, err := delta.ApplyImmutable(applied, frame.Ops)
		must(t, err)
		nexttest.RequireJSONEqual(t, next, frame.Value)
		applied = frame.Value
	}
}

// running.test.ts: running's list re-paths every kind of state op under its entry; a replacement becomes a set
func TestRunningsListRePathsEveryKindOfStateOpUnderItsEntryAReplacementBecomesASet(t *testing.T) {
	batch, scheduler, list := newRunningList(t)
	entry := jsonObject("id", "a", "state", jsonObject("list", []any{float64(1)}, "text", "", "n", float64(0)))
	commitRunning(t, batch, func() { list.add(entry) })
	watch, err := list.Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	after := jsonObject("id", "a", "state", jsonObject("list", []any{float64(2)}, "text", "t", "n", float64(1)))
	ops := []delta.Op{
		{"r", jsonObject("list", []any{}, "text", "", "n", float64(0))},
		{"s", []any{"n"}, float64(1)},
		{"p", []any{"list"}, 0, 0, []any{float64(2)}},
		{"a", []any{"text"}, "t"},
		{"d", []any{"gone"}},
		{"t", []any{"text"}, 1},
		{"m", []any{"list"}, []any{0}},
	}
	commitRunning(t, batch, func() { list.stateChanged(entry, after, ops) })
	scheduler.Wait()
	want := []any{
		[]any{"s", []any{0, "state"}, jsonObject("list", []any{}, "text", "", "n", float64(0))},
		[]any{"s", []any{0, "state", "n"}, float64(1)},
		[]any{"p", []any{0, "state", "list"}, 0, 0, []any{float64(2)}},
		[]any{"a", []any{0, "state", "text"}, "t"},
		[]any{"d", []any{0, "state", "gone"}},
		[]any{"t", []any{0, "state", "text"}, 1},
		[]any{"m", []any{0, "state", "list"}, []any{0}},
	}
	got := frames.Items()
	if len(got) != 1 {
		t.Fatalf("%d frames, want 1", len(got))
	}
	nexttest.RequireJSONEqual(t, opsAsAny(got[0].Ops), want)
}

// running.test.ts: running's list builds no op for a state change while unwatched (cost guard: the ops are not even walked)
func TestRunningsListBuildsNoOpForAStateChangeWhileUnwatched(t *testing.T) {
	batch, _, list := newRunningList(t)
	entry := jsonObject("id", "a", "state", jsonObject("output", ""))
	commitRunning(t, batch, func() { list.add(entry) })
	// An empty op has no verb: building its re-pathed op would index past it and panic, as Pi's iterator throws "walked".
	unwalked := []delta.Op{{}}
	after := jsonObject("id", "a", "state", jsonObject("output", "x"))
	commitRunning(t, batch, func() { list.stateChanged(entry, after, unwalked) })
	nexttest.RequireJSONEqual(t, listValue(t, list), []any{after})
}

// running.test.ts: running's list keeps nothing once ended: reads throw released
func TestRunningsListKeepsNothingOnceEndedReadsReturnReleased(t *testing.T) {
	batch, _, list := newRunningList(t)
	commitRunning(t, batch, func() { list.add(jsonObject("id", "a")) })
	watch, err := list.Watch(background)
	must(t, err)
	list.end()
	<-watch.Closed()
	end, err := watch.Stop()
	must(t, err)
	if end.Kind != chord.WatchEnded {
		t.Fatalf("watch end = %+v, want ended", end)
	}
	if _, err := list.Value(); err == nil || err.Error() != "app.store job app.echo: ended" {
		t.Fatalf("Value() error = %v", err)
	}
}

// running.test.ts: running's list records no ops while unwatched, and marks derives over it at every change
func TestRunningsListRecordsNoOpsWhileUnwatchedAndMarksDerivesOverItAtEveryChange(t *testing.T) {
	batch, _, list := newRunningList(t)
	count := state.NewDerive([]any{list}, func(values []any) (delta.JsonValue, error) {
		return float64(len(values[0].([]any))), nil
	}, batch)
	must(t, count.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(*running), nil }, func(error) {}))
	for want, entry := range []*delta.JsonObject{nil, jsonObject("id", "a"), jsonObject("id", "b")} {
		if entry != nil {
			commitRunning(t, batch, func() { list.add(entry) })
		}
		value, err := count.Value()
		must(t, err)
		if value != float64(want) {
			t.Fatalf("count = %v, want %d", value, want)
		}
	}
}
