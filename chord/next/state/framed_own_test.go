package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of FramedSource (packages/chord/src/state/framed.ts), which Pi covers only through its readers
// (test/host/query-reader.test.ts, which waits for the store's read engine): a subclass that feeds it with Set and
// FailWith, as a live query feeds its rows.

type framedOwner struct{ reports []error }

func (o *framedOwner) Label() string     { return "app.rows" }
func (o *framedOwner) Report(err error)  { o.reports = append(o.reports, err) }
func (o *framedOwner) reported() []error { return o.reports }

type rowsSource struct{ state.FramedSource }

func newRows(h *harness, owner state.FramedOwner) *rowsSource {
	rows := &rowsSource{}
	rows.Init(h.batch, owner)
	return rows
}

func (rows *rowsSource) feed(t *testing.T, h *harness, step func()) {
	t.Helper()
	if err := h.batch.Within(func() error { step(); return nil }, background); err != nil {
		t.Fatalf("Within: %v", err)
	}
}

func TestFramedSource(t *testing.T) {
	t.Run("read before its first value is not_ready, naming its owner; then it has the value it was given", func(t *testing.T) {
		h := newHarness(t, nil)
		rows := newRows(h, &framedOwner{})
		_, err := rows.Value()
		var chordErr *data.ChordError
		if !errors.As(err, &chordErr) || chordErr.Code != data.CodeNotReady || chordErr.Message != "app.rows: read before its first value" {
			t.Fatalf("Value error = %v, want not_ready naming app.rows", err)
		}
		if rows.Ready() {
			t.Fatal("Ready before a value")
		}
		rows.feed(t, h, func() { rows.Set(num(1)) })
		if value, err := rows.Value(); err != nil || !reflect.DeepEqual(value, any(num(1))) || !rows.Ready() {
			t.Fatalf("Value = %v, %v, ready %v", value, err, rows.Ready())
		}
	})

	t.Run("a watch gets one frame per batch as the diff from the value it had, none when equal (its owner sets once per batch)", func(t *testing.T) {
		h := newHarness(t, nil)
		rows := newRows(h, &framedOwner{})
		rows.feed(t, h, func() { rows.Set(num(0)) })
		watch := watchOf(t, rows)
		frames := nexttest.FramesOf(watch)
		rows.feed(t, h, func() { rows.Set(num(2)) })
		h.delivered()
		rows.feed(t, h, func() { rows.Set(num(2)) })
		h.delivered()
		want := []nexttest.Frame[delta.JsonValue]{{Value: num(2), Ops: setOps(2)}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
	})

	t.Run("a failure gives its watches one failure call and its owner one report per episode; the next value replaces it whole", func(t *testing.T) {
		h := newHarness(t, nil)
		owner := &framedOwner{}
		rows := newRows(h, owner)
		rows.feed(t, h, func() { rows.Set(num(1)) })
		watch := watchOf(t, rows)
		calls := nexttest.ChangesOf(watch)
		first, second := errors.New("first"), errors.New("second")
		rows.feed(t, h, func() { rows.FailWith(first) })
		h.delivered()
		if failure := rows.Failure(); !errors.Is(failure, first) {
			t.Fatalf("Failure = %v", failure)
		}
		if _, err := rows.Value(); !errors.Is(err, first) {
			t.Fatalf("Value error = %v, want first", err)
		}
		rows.feed(t, h, func() { rows.FailWith(second) })
		h.delivered()
		if got := owner.reported(); len(got) != 1 || !errors.Is(got[0], first) {
			t.Fatalf("reports = %v, want only the first of the episode", got)
		}
		if start := rows.Start(); !start.HasLast || !reflect.DeepEqual(start.Last, any(num(1))) || !errors.Is(start.Error, second) {
			t.Fatalf("Start after a second failure = %+v, want the last good value and the second failure", start)
		}
		rows.feed(t, h, func() { rows.Set(num(2)) })
		h.delivered()
		requireCalls(t, "watch", calls, failedCall(first), valueCall(num(2), ops(op("r", num(2))), false))
		rows.feed(t, h, func() { rows.FailWith(first) })
		h.delivered()
		if got := owner.reported(); len(got) != 2 {
			t.Fatalf("reports = %v, want a second episode reported", got)
		}
	})

	t.Run("a watch opened while it is failed starts at its last good value and gets the failure", func(t *testing.T) {
		h := newHarness(t, nil)
		rows := newRows(h, &framedOwner{})
		boom := errors.New("boom")
		rows.feed(t, h, func() { rows.Set(num(5)) })
		rows.feed(t, h, func() { rows.FailWith(boom) })
		start := rows.Start()
		if start.Kind != state.StartFailed || !errors.Is(start.Error, boom) || !start.HasLast || !reflect.DeepEqual(start.Last, any(num(5))) {
			t.Fatalf("Start = %+v", start)
		}
		watch := watchOf(t, rows)
		calls := nexttest.ChangesOf(watch)
		h.delivered()
		requireCalls(t, "watch", calls, failedCall(boom))
	})

	t.Run("a failure before its first value leaves a watch nothing to start at; Quiet leaves that failure unreported", func(t *testing.T) {
		h := newHarness(t, nil)
		owner := &framedOwner{}
		rows := newRows(h, owner)
		boom := errors.New("boom")
		rows.Quiet()
		rows.feed(t, h, func() { rows.FailWith(boom) })
		if got := owner.reported(); len(got) != 0 {
			t.Fatalf("reports = %v, want none after Quiet", got)
		}
		if _, err := rows.Watch(background); !errors.Is(err, boom) {
			t.Fatalf("Watch error = %v, want boom", err)
		}
	})

	t.Run("ended by its owner: reads throw the end's error, watches end ended, and nothing more is taken", func(t *testing.T) {
		h := newHarness(t, nil)
		rows := newRows(h, &framedOwner{})
		rows.feed(t, h, func() { rows.Set(num(1)) })
		watch := watchOf(t, rows)
		gone := errors.New("object ended")
		rows.End(gone)
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("end = %+v, want ended", end)
		}
		if _, err := rows.Value(); !errors.Is(err, gone) || !rows.Detached() || rows.Ready() {
			t.Fatalf("Value error = %v, detached %v, ready %v", err, rows.Detached(), rows.Ready())
		}
		rows.feed(t, h, func() { rows.Set(num(2)); rows.FailWith(errors.New("late")) })
		if _, err := rows.Value(); !errors.Is(err, gone) {
			t.Fatalf("Value error after Set = %v, want the end's", err)
		}
	})
}
