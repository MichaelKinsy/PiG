package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/state/framed.test.ts (Pi f6600b58f347; new in 36c709e720b6): one frame per batch however
// often the batch set the source, of the state the batch left, diffed from the value its watches had before the batch;
// failures and recoveries within one batch; its dependents marked by every change; nothing listed while unwatched.
// Subtest names are Pi's `it` texts; the two tests are Pi's two describe blocks.
//
// Go forms: Pi's `Fed extends FramedSource` overrides `deliver` to count; Go's batch lists the Source's impl, so
// fedSource points the embedded Source at itself again after FramedSource.Init (Source.Init's impl is the type that
// embeds it). Pi's `batch.commit(fn, context)` is Batch.Commit; its `Subscriber` is a pointer recording `advance`.

// fedSource is a framed source a test feeds, counting its deliveries.
type fedSource struct {
	state.FramedSource
	deliveries int
}

func (f *fedSource) Deliver(ctx context.Context) error {
	f.deliveries++
	return f.FramedSource.Deliver(ctx)
}

// The tests' Pi Subscriber is watchable_own_test.go's recorder: the calls it was advanced with, none folded.

// fed is Pi's `fed(initial)`: a fed source with the value initial (nil: none), its batch's harness, what its owner was
// told, and a subscriber that subscribe takes on.
type fed struct {
	h      *harness
	source *fedSource
	owner  *framedOwner
	sub    *recorder
}

func newFed(t *testing.T, initial delta.JsonValue) *fed {
	t.Helper()
	h := newHarness(t, nil)
	f := &fed{h: h, source: &fedSource{}, owner: &framedOwner{}, sub: &recorder{}}
	f.source.Init(h.batch, f.owner)
	f.source.Source.Init(h.batch, f.source)
	if initial != nil {
		f.commit(t, func() { f.source.Set(initial) })
	}
	return f
}

func (f *fed) commit(t *testing.T, work func()) {
	t.Helper()
	if err := f.h.batch.Commit(func() error { work(); return nil }, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func (f *fed) subscribe() { f.source.Subscribe(f.sub) }

func (f *fed) requireNoErrors(t *testing.T) {
	t.Helper()
	if got := f.h.errors.all(); len(got) != 0 {
		t.Fatalf("batch errors = %+v, want none", got)
	}
}

func (f *fed) requireCalls(t *testing.T, want ...call) {
	t.Helper()
	if want == nil {
		want = []call{}
	}
	if got := normalized(f.sub.calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
}

func abc(a, b, c float64) *delta.JsonObject { return delta.JsonObjectOf("a", a, "b", b, "c", c) }

// pi: packages/chord/src/state/framed.ts
func TestFramedSourceSetSeveralTimesInOneBatch(t *testing.T) {
	t.Run("delivers one frame: its last value, diffed from the value before the batch", func(t *testing.T) {
		before := abc(1, 1, 1)
		f := newFed(t, before)
		f.subscribe()
		f.commit(t, func() {
			f.source.Set(abc(2, 1, 1))
			f.source.Set(abc(2, 3, 1))
			f.source.Set(abc(2, 3, 4))
		})
		f.requireNoErrors(t)
		if len(f.sub.calls) != 1 {
			t.Fatalf("calls = %d, want 1: %+v", len(f.sub.calls), f.sub.calls)
		}
		got := f.sub.calls[0]
		if got.Kind != chord.WatchCallValue || !reflect.DeepEqual(got.Value, any(abc(2, 3, 4))) {
			t.Fatalf("call = %+v, want the value {a:2 b:3 c:4}", got)
		}
		// The ops apply to what the subscriber had, not to a value in between.
		if applied, err := delta.ApplyImmutable(before, got.Ops); err != nil || !reflect.DeepEqual(applied, got.Value) {
			t.Fatalf("ApplyImmutable(before, ops) = %v, %v; want %v", applied, err, got.Value)
		}
		wantOps := ops(op("s", delta.Path{"a"}, float64(2)), op("s", delta.Path{"b"}, float64(3)), op("s", delta.Path{"c"}, float64(4)))
		if !reflect.DeepEqual(got.Ops, wantOps) {
			t.Fatalf("ops = %v, want %v", got.Ops, wantOps)
		}
		// Listed once in the batch (the cost: one delivery, one diff).
		if f.source.deliveries != 1 {
			t.Fatalf("deliveries = %d, want 1", f.source.deliveries)
		}
		// The next batch starts from the last value.
		f.commit(t, func() { f.source.Set(abc(2, 4, 4)) })
		if len(f.sub.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(f.sub.calls))
		}
		if next := normalized(f.sub.calls)[1]; next.Kind != chord.WatchCallValue || !reflect.DeepEqual(next.Ops, ops(op("s", delta.Path{"b"}, float64(4)))) {
			t.Fatalf("second call = %+v, want ops [[s [b] 4]]", next)
		}
	})

	t.Run("delivers nothing when the batch ends at the value it started from", func(t *testing.T) {
		f := newFed(t, num(1))
		f.subscribe()
		f.commit(t, func() {
			f.source.Set(num(2))
			f.source.Set(num(1))
		})
		f.requireCalls(t)
		if f.source.deliveries != 1 {
			t.Fatalf("deliveries = %d, want 1", f.source.deliveries)
		}
	})

	t.Run("marks its dependents with every change: a derive read between two sets reads again after the second", func(t *testing.T) {
		f := newFed(t, float64(1))
		double := state.NewDerive([]any{f.source}, func(values []any) (delta.JsonValue, error) { return values[0].(float64) * 2, nil }, f.h.batch)
		if err := double.Attach(func(any, int) (state.DeriveInput, error) { return f.source, nil }, func(error) {}); err != nil {
			t.Fatal(err)
		}
		if got := mustValue(t, double); got != any(float64(2)) {
			t.Fatalf("double = %v, want 2", got)
		}
		var read []any
		f.commit(t, func() {
			f.source.Set(float64(2))
			read = append(read, mustValue(t, double))
			f.source.Set(float64(3))
			read = append(read, mustValue(t, double))
		})
		if want := []any{float64(4), float64(6)}; !reflect.DeepEqual(read, want) {
			t.Fatalf("read = %v, want %v", read, want)
		}
		if got := mustValue(t, double); got != any(float64(6)) {
			t.Fatalf("double after = %v, want 6", got)
		}
	})

	t.Run("tells a dependent of a change after it took the value: one that reads at once reads the new one", func(t *testing.T) {
		f := newFed(t, float64(1))
		var read []any
		f.source.DependentSet().Add(&invalidatingDependent{invalidate: func() { read = append(read, mustValue(t, f.source)) }})
		f.commit(t, func() {
			f.source.Set(float64(2))
			f.source.Set(float64(3))
		})
		if want := []any{float64(2), float64(3)}; !reflect.DeepEqual(read, want) {
			t.Fatalf("read = %v, want %v", read, want)
		}
	})

	t.Run("lists nothing while nobody watches it; a subscriber taken on between two sets gets the diff from what it took", func(t *testing.T) {
		f := newFed(t, num(1))
		f.commit(t, func() {
			f.source.Set(num(2))
			f.source.Set(num(3))
		})
		if f.source.deliveries != 0 {
			t.Fatalf("deliveries = %d, want 0", f.source.deliveries)
		}
		if got := mustValue(t, f.source); !reflect.DeepEqual(got, any(num(3))) {
			t.Fatalf("value = %v, want {n:3}", got)
		}
		var taken delta.JsonValue
		f.commit(t, func() {
			f.source.Set(num(4))
			taken = mustValue(t, f.source)
			f.subscribe()
			f.source.Set(num(5))
			f.source.Set(num(6))
		})
		if len(f.sub.calls) != 1 {
			t.Fatalf("calls = %d, want 1: %+v", len(f.sub.calls), f.sub.calls)
		}
		got := f.sub.calls[0]
		if got.Kind != chord.WatchCallValue {
			t.Fatalf("call = %+v, want a value call", got)
		}
		if applied, err := delta.ApplyImmutable(taken, got.Ops); err != nil || !reflect.DeepEqual(applied, any(num(6))) {
			t.Fatalf("ApplyImmutable(taken, ops) = %v, %v; want {n:6}", applied, err)
		}
	})
}

// invalidatingDependent is a Dependent that runs invalidate when it is marked (Pi's `{ invalidate, sourceRetired }`).
type invalidatingDependent struct{ invalidate func() }

func (d *invalidatingDependent) Invalidate()                                { d.invalidate() }
func (d *invalidatingDependent) SourceRetired(map[state.Dependent]struct{}) {}

func TestFramedSourceFailingAndRecoveringInOneBatch(t *testing.T) {
	t.Run("a failure and the value after it are one frame, the diff from the value before; no failure call", func(t *testing.T) {
		f := newFed(t, num(1))
		f.subscribe()
		f.commit(t, func() {
			f.source.FailWith(errors.New("a page in between"))
			f.source.Set(num(2))
		})
		f.requireCalls(t, valueCall(num(2), ops(op("s", delta.Path{"n"}, float64(2))), false))
		// The episode was the owner's to hear of.
		if len(f.owner.reported()) != 1 {
			t.Fatalf("reports = %v, want 1", f.owner.reported())
		}
	})

	t.Run("a value and the failure after it, or two failures, are one failure call and one report", func(t *testing.T) {
		f := newFed(t, num(1))
		f.subscribe()
		first, last := errors.New("first"), errors.New("last")
		f.commit(t, func() {
			f.source.Set(num(2))
			f.source.FailWith(first)
			f.source.FailWith(last)
		})
		f.requireCalls(t, failedCall(last))
		if len(f.owner.reported()) != 1 {
			t.Fatalf("reports = %v, want 1", f.owner.reported())
		}
		if start := f.source.Start(); !errors.Is(f.source.Failure(), last) || !start.HasLast || !reflect.DeepEqual(start.Last, any(num(2))) {
			t.Fatalf("failure = %v, start = %+v; want last and the last good value {n:2}", f.source.Failure(), start)
		}
		// Still failed in a later batch: nothing new for the watches; the next value replaces the value whole.
		f.commit(t, func() { f.source.FailWith(errors.New("again")) })
		if len(f.sub.calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(f.sub.calls))
		}
		f.commit(t, func() {
			f.source.FailWith(errors.New("once more"))
			f.source.Set(num(3))
			f.source.Set(num(4))
		})
		if got, want := normalized(f.sub.calls[1:]), []call{valueCall(num(4), ops(op("r", num(4))), false)}; !reflect.DeepEqual(got, want) {
			t.Fatalf("calls after = %+v, want %+v", got, want)
		}
	})

	t.Run("an end in the batch that set it delivers nothing", func(t *testing.T) {
		f := newFed(t, num(1))
		f.subscribe()
		f.commit(t, func() {
			f.source.Set(num(2))
			f.source.End(errors.New("ended"))
			f.source.Set(num(3))
		})
		f.requireCalls(t)
		f.requireNoErrors(t)
		if !f.source.Detached() {
			t.Fatal("not detached")
		}
	})
}
