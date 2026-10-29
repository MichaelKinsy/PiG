package state_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of Derive on documents the test supplies (docSource): the scenarios of derive.test.ts, which upstream runs on
// a ChordHost with defineState documents and references (those tests wait for the host, contracts and documents
// packages).

type tracked struct {
	derive  *state.Derive
	reports *[]error
}

// derived attaches a derive over sources and returns it with the failures its owner was told.
func derived(h *harness, sources []any, compute func(values []any) (delta.JsonValue, error)) tracked {
	reports := &[]error{}
	d := state.NewDerive(sources, compute, h.batch)
	err := d.Attach(func(source any, _ int) (state.DeriveInput, error) {
		return source.(state.DeriveInput), nil
	}, func(err error) { *reports = append(*reports, err) })
	if err != nil {
		panic(err)
	}
	return tracked{derive: d, reports: reports}
}

func intOf(value any) float64 { return value.(*delta.JsonObject).Value("n").(float64) }

// call is a WatchCall with its Ops normalized, nil and empty being the same list.
type call struct {
	Kind    chord.WatchCallKind
	Value   any
	Ops     []delta.Op
	Retired bool
	Error   error
}

func normalized(calls []chord.WatchCall[delta.JsonValue]) []call {
	out := make([]call, len(calls))
	for i, c := range calls {
		out[i] = call{Kind: c.Kind, Value: c.Value, Ops: c.Ops, Retired: c.Retired, Error: c.Error}
		if len(out[i].Ops) == 0 {
			out[i].Ops = []delta.Op{}
		}
	}
	return out
}

func valueCall(value any, ops []delta.Op, retired bool) call {
	return call{Kind: chord.WatchCallValue, Value: value, Ops: ops, Retired: retired}
}

func failedCall(err error) call {
	return call{Kind: chord.WatchCallFailed, Ops: []delta.Op{}, Error: err}
}

func requireCalls(t *testing.T, name string, got *nexttest.Collector[chord.WatchCall[delta.JsonValue]], want ...call) {
	t.Helper()
	if want == nil {
		want = []call{}
	}
	if gotCalls := normalized(got.Items()); !reflect.DeepEqual(gotCalls, want) {
		t.Fatalf("%s calls = %+v, want %+v", name, gotCalls, want)
	}
}

func lastCall(t *testing.T, got *nexttest.Collector[chord.WatchCall[delta.JsonValue]]) call {
	t.Helper()
	calls := normalized(got.Items())
	if len(calls) == 0 {
		t.Fatal("no calls")
	}
	return calls[len(calls)-1]
}

func watchOf(t *testing.T, source interface {
	Watch(context.Context) (chord.WatchHandle[delta.JsonValue], error)
}) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	watch, err := source.Watch(background)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return watch
}

func mustSet(t *testing.T, doc *docSource, value delta.JsonValue) {
	t.Helper()
	if err := doc.set(value); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func mustValue(t *testing.T, source interface {
	Value() (delta.JsonValue, error)
}) delta.JsonValue {
	t.Helper()
	value, err := source.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	return value
}

func TestDeriveAcrossSources(t *testing.T) {
	t.Run("update in the batch of the commit that changed their source's source", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		double := derived(h, []any{doc}, func(values []any) (delta.JsonValue, error) { return intOf(values[0]) * 2, nil })
		viewer := derived(h, []any{double.derive}, func(values []any) (delta.JsonValue, error) {
			return fmt.Sprintf("double %v", values[0]), nil
		})
		frames := nexttest.FramesOf(watchOf(t, viewer.derive))
		mustSet(t, doc, num(2))
		h.delivered()
		want := []nexttest.Frame[delta.JsonValue]{{Value: "double 4", Ops: ops(op("r", "double 4"))}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
	})
}

func TestDerivePullModel(t *testing.T) {
	t.Run("computes a diamond's derives once each per batch", func(t *testing.T) {
		meter := nexttest.Metered(t)
		h := newHarness(t, meter.Telemetry.Metrics())
		doc := newDoc(h.batch, num(0))
		b := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]) + 1, nil })
		c := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]) + 2, nil })
		d := derived(h, []any{b.derive, c.derive}, func(v []any) (delta.JsonValue, error) {
			return v[0].(float64) + v[1].(float64), nil
		})
		frames := nexttest.FramesOf(watchOf(t, d.derive))
		before := meter.Counts()["chord.derive.computes"]
		mustSet(t, doc, num(1))
		if got := meter.Counts()["chord.derive.computes"] - before; got != 3 {
			t.Fatalf("computes in the commit = %v, want 3", got)
		}
		h.delivered()
		got := frames.Items()
		if len(got) != 1 || got[0].Value != float64(5) {
			t.Fatalf("frames = %+v, want one with 5", got)
		}
	})

	t.Run("leaves unwatched derives dirty until read; a one-off read does not make them eager", func(t *testing.T) {
		meter := nexttest.Metered(t)
		h := newHarness(t, meter.Telemetry.Metrics())
		doc := newDoc(h.batch, num(0))
		double := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]) * 2, nil })
		computes := func() float64 { return meter.Counts()["chord.derive.computes"] }
		before := computes()
		mustSet(t, doc, num(1))
		if computes() != before {
			t.Fatal("a commit computed an unwatched derive")
		}
		for i := 0; i < 2; i++ {
			if got := mustValue(t, double.derive); got != float64(2) {
				t.Fatalf("value = %v, want 2", got)
			}
		}
		if computes() != before+1 {
			t.Fatalf("computes = %v, want %v", computes(), before+1)
		}
		mustSet(t, doc, num(2))
		if computes() != before+1 {
			t.Fatal("a second commit computed an unwatched derive")
		}
	})

	t.Run("diffs a watched derive's old and new value (counted), and sends no frame when they are equal", func(t *testing.T) {
		meter := nexttest.Metered(t)
		h := newHarness(t, meter.Telemetry.Metrics())
		doc := newDoc(h.batch, num(0))
		even := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) {
			return delta.JsonObjectOf("even", float64(int(intOf(v[0]))%2)), nil
		})
		frames := nexttest.FramesOf(watchOf(t, even.derive))
		mustSet(t, doc, num(2))
		mustSet(t, doc, num(3))
		h.delivered()
		want := []nexttest.Frame[delta.JsonValue]{{Value: delta.JsonObjectOf("even", float64(1)), Ops: ops(op("s", delta.Path{"even"}, float64(1)))}}
		if got := frames.Items(); !reflect.DeepEqual(got, want) {
			t.Fatalf("frames = %+v, want %+v", got, want)
		}
		if got := meter.Counts()["chord.derive.diffs"]; got != 2 {
			t.Fatalf("diffs = %v, want 2", got)
		}
	})
}

// failingDerives is `app.failing`: t throws while n is 1 or negative, u is t + 1, v is n.
type failingDerives struct {
	h    *harness
	doc  *docSource
	t, u tracked
	v    tracked
}

func newFailing(t *testing.T) *failingDerives {
	h := newHarness(t, nil)
	doc := newDoc(h.batch, num(0))
	tt := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) {
		if n := intOf(v[0]); n == 1 || n < 0 {
			return nil, errThrew
		}
		return intOf(v[0]), nil
	})
	return &failingDerives{
		h: h, doc: doc, t: tt,
		u: derived(h, []any{tt.derive}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) + 1, nil }),
		v: derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]), nil }),
	}
}

func (f *failingDerives) set(t *testing.T, n float64) { t.Helper(); mustSet(t, f.doc, num(n)) }

func requireThrows(t *testing.T, name string, source *state.Derive, want error) {
	t.Helper()
	if _, err := source.Value(); err != want {
		t.Fatalf("%s.Value error = %v, want %v", name, err, want)
	}
}

func TestDeriveFailures(t *testing.T) {
	t.Run("give the throwing derive's watches and those of the derives over it one failure call; they heal on a later change", func(t *testing.T) {
		f := newFailing(t)
		ts, us := nexttest.ChangesOf(watchOf(t, f.t.derive)), nexttest.ChangesOf(watchOf(t, f.u.derive))
		frames := nexttest.FramesOf(watchOf(t, f.v.derive))
		f.set(t, 1)
		f.h.delivered()
		requireCalls(t, "t", ts, failedCall(errThrew))
		requireCalls(t, "u", us, failedCall(errThrew))
		requireThrows(t, "t", f.t.derive, errThrew)
		requireThrows(t, "u", f.u.derive, errThrew)
		if got := mustValue(t, f.v.derive); got != float64(1) {
			t.Fatalf("v = %v, want 1", got)
		}
		f.set(t, 2)
		f.h.delivered()
		// Open all along; the next value replaces the root.
		if got, want := lastCall(t, ts), valueCall(float64(2), ops(op("r", float64(2))), false); !reflect.DeepEqual(got, want) {
			t.Fatalf("t last = %+v, want %+v", got, want)
		}
		if got, want := lastCall(t, us), valueCall(float64(3), ops(op("r", float64(3))), false); !reflect.DeepEqual(got, want) {
			t.Fatalf("u last = %+v, want %+v", got, want)
		}
		var values []any
		for _, frame := range frames.Items() {
			values = append(values, frame.Value)
		}
		if want := []any{float64(1), float64(2)}; !reflect.DeepEqual(values, want) {
			t.Fatalf("v frames = %v, want %v", values, want)
		}
		// The throwing derive's owner reports it, once; the derive over it (its source failed) does not.
		if got := *f.t.reports; len(got) != 1 || got[0] != errThrew {
			t.Fatalf("t reports = %v, want one %v", got, errThrew)
		}
		if got := *f.u.reports; len(got) != 0 {
			t.Fatalf("u reports = %v, want none", got)
		}
	})

	t.Run("give one failure call per episode: a recompute that throws again is quiet; a heal ends the episode", func(t *testing.T) {
		f := newFailing(t)
		ts, us := nexttest.ChangesOf(watchOf(t, f.t.derive)), nexttest.ChangesOf(watchOf(t, f.u.derive))
		for _, n := range []float64{1, -1, -2, 3, -3} {
			f.set(t, n)
			f.h.delivered()
		}
		requireCalls(t, "t", ts, failedCall(errThrew), valueCall(float64(3), ops(op("r", float64(3))), false), failedCall(errThrew))
		requireCalls(t, "u", us, failedCall(errThrew), valueCall(float64(4), ops(op("r", float64(4))), false), failedCall(errThrew))
	})

	t.Run("open a new watch on a failed derive at its last good value, the failure its first call, then the heal", func(t *testing.T) {
		f := newFailing(t)
		f.set(t, 5)
		// Lazy: the last good value is the last one computed.
		if got := mustValue(t, f.u.derive); got != float64(6) {
			t.Fatalf("u = %v, want 6", got)
		}
		f.set(t, 1)
		tw, uw := watchOf(t, f.t.derive), watchOf(t, f.u.derive)
		if tw.Value() != float64(5) || uw.Value() != float64(6) {
			t.Fatalf("watches start at %v, %v; want 5, 6", tw.Value(), uw.Value())
		}
		ts, us := nexttest.ChangesOf(tw), nexttest.ChangesOf(uw)
		f.h.delivered()
		f.set(t, -1)
		f.set(t, 7)
		f.h.delivered()
		requireCalls(t, "t", ts, failedCall(errThrew), valueCall(float64(7), ops(op("r", float64(7))), false))
		requireCalls(t, "u", us, failedCall(errThrew), valueCall(float64(8), ops(op("r", float64(8))), false))
	})

	t.Run("report once per failure episode, also when nothing watches (a lazy derive fails when read)", func(t *testing.T) {
		f := newFailing(t)
		f.set(t, 1)
		requireThrows(t, "t", f.t.derive, errThrew)
		requireThrows(t, "t", f.t.derive, errThrew)
		f.set(t, -1)
		requireThrows(t, "t", f.t.derive, errThrew)
		if n := len(*f.t.reports); n != 1 {
			t.Fatalf("reports = %d, want 1", n)
		}
		f.set(t, 2)
		if got := mustValue(t, f.t.derive); got != float64(2) {
			t.Fatalf("t = %v, want 2", got)
		}
		f.set(t, 1)
		requireThrows(t, "t", f.t.derive, errThrew)
		if n := len(*f.t.reports); n != 2 {
			t.Fatalf("reports = %d, want 2", n)
		}
	})

	t.Run("fold a failure and a value for a busy listener: the value replaces the failure, as a root replacement", func(t *testing.T) {
		f := newFailing(t)
		watch := watchOf(t, f.t.derive)
		var calls nexttest.Collector[chord.WatchCall[delta.JsonValue]]
		gate := make(chan struct{})
		watch.Start(func(_ context.Context, c chord.WatchCall[delta.JsonValue]) error {
			if calls.Append(c) == 1 {
				<-gate
			}
			return nil
		})
		f.set(t, 5)
		if err := f.h.scheduler.AwaitEntered(background); err != nil {
			t.Fatal(err)
		}
		f.set(t, 1)
		f.set(t, 6)
		close(gate)
		f.h.delivered()
		requireCalls(t, "t", &calls, valueCall(float64(5), ops(op("r", float64(5))), false), valueCall(float64(6), ops(op("r", float64(6))), false))
	})
}

func TestDeriveDetach(t *testing.T) {
	t.Run("a detached derive is not held by its sources; its watches end ended and reads throw its object's error", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		d := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]), nil })
		if doc.DependentSet().Len() != 1 {
			t.Fatalf("dependents = %d, want 1", doc.DependentSet().Len())
		}
		watch := watchOf(t, d.derive)
		ended := errors.New("app.viewer: ended")
		d.derive.Detach(ended)
		if doc.DependentSet().Len() != 0 {
			t.Fatalf("dependents after detach = %d, want 0", doc.DependentSet().Len())
		}
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("end = %+v, want ended", end)
		}
		requireThrows(t, "detached", d.derive, ended)
		// A change reaches nobody.
		mustSet(t, doc, num(1))
		requireThrows(t, "detached after a change", d.derive, ended)
	})
}

func TestDeriveComputeIsPure(t *testing.T) {
	t.Run("refuses watch-like calls and changes inside a compute, and clears the flag after a throwing one", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		var attempts []error
		d := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) {
			if intOf(v[0]) > 0 {
				attempts = append(attempts, h.batch.AssertOutside("watch"), h.batch.AssertNotComputing("writable.set"))
			}
			if intOf(v[0]) > 1 {
				return nil, errors.New("compute threw")
			}
			return intOf(v[0]), nil
		})
		mustSet(t, doc, num(1))
		mustValue(t, d.derive)
		if len(attempts) != 2 {
			t.Fatalf("attempts = %v", attempts)
		}
		requireReentrant(t, "watch in a compute", attempts[0])
		requireReentrant(t, "set in a compute", attempts[1])
		mustSet(t, doc, num(2))
		if _, err := d.derive.Value(); err == nil || err.Error() != "compute threw" {
			t.Fatalf("Value error = %v, want compute threw", err)
		}
		if err := h.batch.AssertOutside("watch"); err != nil || h.batch.Running() {
			t.Fatalf("flags stay set after a throwing compute: %v", err)
		}
		mustSet(t, doc, num(3))
	})
}

// spy forwards a derive's marks and counts the retirement walks that reach it.
type spy struct {
	target state.Dependent
	calls  *int
}

func (s *spy) Invalidate() { s.target.Invalidate() }
func (s *spy) SourceRetired(visited map[state.Dependent]struct{}) {
	*s.calls++
	s.target.SourceRetired(visited)
}

// shadow hides a source's dependents from the derive reading it, so spies can stand between them.
type shadow struct {
	inner      state.DeriveInput
	dependents state.DependentSet
}

func (s *shadow) DeriveValue() (any, error)         { return s.inner.DeriveValue() }
func (s *shadow) DependentSet() *state.DependentSet { return &s.dependents }

func TestDeriveRetiredFlag(t *testing.T) {
	list := func(items ...any) *delta.JsonObject { return delta.JsonObjectOf("items", append([]any{}, items...)) }
	count := func(v any) int { return len(v.(*delta.JsonObject).Value("items").([]any)) }

	t.Run("a derive over a retired source publishes retired: true although its value and ops are unchanged (rule 21)", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, list())
		many := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return count(v[0]) > 1, nil })
		twice := derived(h, []any{many.derive}, func(v []any) (delta.JsonValue, error) { return !v[0].(bool), nil })
		mustSet(t, doc, list("a"))
		manys, twices := nexttest.ChangesOf(watchOf(t, many.derive)), nexttest.ChangesOf(watchOf(t, twice.derive))
		if err := doc.retire(list()); err != nil {
			t.Fatal(err)
		}
		h.delivered()
		requireCalls(t, "many", manys, valueCall(false, ops(), true))
		requireCalls(t, "twice", twices, valueCall(true, ops(), true))
		mustSet(t, doc, list("a", "b"))
		h.delivered()
		if got, want := lastCall(t, manys), valueCall(true, ops(op("r", true)), false); !reflect.DeepEqual(got, want) {
			t.Fatalf("many last = %+v, want %+v", got, want)
		}
	})

	t.Run("a retirement while the derive is failed travels with its next good value", func(t *testing.T) {
		h := newHarness(t, nil)
		numDoc, gate := newDoc(h.batch, num(0)), newDoc(h.batch, delta.JsonObjectOf("on", float64(0)))
		n := derived(h, []any{numDoc, gate}, func(v []any) (delta.JsonValue, error) {
			if v[1].(*delta.JsonObject).Value("on") == float64(1) && intOf(v[0]) == 0 {
				return nil, errThrew
			}
			return intOf(v[0]), nil
		})
		mustSet(t, numDoc, num(1))
		mustSet(t, gate, delta.JsonObjectOf("on", float64(1)))
		calls := nexttest.ChangesOf(watchOf(t, n.derive))
		// The retirement makes n 0 again, which throws.
		if err := numDoc.retire(num(0)); err != nil {
			t.Fatal(err)
		}
		mustSet(t, numDoc, num(2))
		h.delivered()
		requireCalls(t, "n", calls, failedCall(errThrew), valueCall(float64(2), ops(op("r", float64(2))), true))
	})

	t.Run("a retirement visits each derive of a diamond once, not once per path (40 derives)", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, list())
		var visits int
		var spied []*shadow
		attach := func(sources []any, compute func([]any) (delta.JsonValue, error)) *state.Derive {
			d := state.NewDerive(sources, compute, h.batch)
			if err := d.Attach(func(source any, _ int) (state.DeriveInput, error) {
				s := &shadow{inner: source.(state.DeriveInput)}
				spied = append(spied, s)
				return s, nil
			}, func(error) {}); err != nil {
				t.Fatal(err)
			}
			return d
		}
		previous := attach([]any{doc}, func(v []any) (delta.JsonValue, error) { return float64(count(v[0])), nil })
		// 13 layers of two derives joined by a third: 2^13 paths from the top to the bottom.
		for layer := 0; layer < 13; layer++ {
			left := attach([]any{previous}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) + 1, nil })
			right := attach([]any{previous}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) + 2, nil })
			previous = attach([]any{left, right}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) + v[1].(float64), nil })
		}
		// Every derive reads its sources through a shadow: the spies take the derives' places in the sources' dependents.
		for _, s := range spied {
			for _, dependent := range s.dependents.Snapshot() {
				s.inner.DependentSet().Add(&spy{target: dependent, calls: &visits})
			}
		}
		mustSet(t, doc, list("a"))
		frames := nexttest.ChangesOf(watchOf(t, previous))
		if err := doc.retire(list()); err != nil {
			t.Fatal(err)
		}
		h.delivered()
		if visits >= 100 {
			t.Fatalf("a retirement made %d visits, want fewer than 100", visits)
		}
		if got := lastCall(t, frames); !got.Retired {
			t.Fatalf("last call = %+v, want retired", got)
		}
	})
}

func TestDeriveGC(t *testing.T) {
	t.Run("a detached derive is not held by its former sources", func(t *testing.T) {
		h := newHarness(t, nil)
		doc := newDoc(h.batch, num(0))
		ref := func() weak.Pointer[state.Derive] {
			label := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) { return intOf(v[0]), nil })
			label.derive.Detach(errors.New("app.viewer: ended"))
			return weak.Make(label.derive)
		}()
		mustSet(t, doc, num(1))
		nexttest.ExpectCollected(t, ref)
	})

	t.Run("a watch open on a failed derive keeps nothing more: it ends with its object, and a retained one holds no source", func(t *testing.T) {
		h := newHarness(t, nil)
		var derive weak.Pointer[state.Derive]
		var document weak.Pointer[docSource]
		watch, calls := func() (chord.WatchHandle[delta.JsonValue], *nexttest.Collector[chord.WatchCall[delta.JsonValue]]) {
			doc := newDoc(h.batch, num(0))
			failing := derived(h, []any{doc}, func(v []any) (delta.JsonValue, error) {
				if intOf(v[0]) == 1 {
					return nil, errThrew
				}
				return intOf(v[0]), nil
			})
			derive, document = weak.Make(failing.derive), weak.Make(doc)
			watch := watchOf(t, failing.derive)
			calls := nexttest.ChangesOf(watch)
			mustSet(t, doc, num(1))
			h.delivered()
			failing.derive.Detach(errors.New("app.paged(1): ended"))
			return watch, calls
		}()
		requireCalls(t, "watch", calls, failedCall(errThrew))
		<-watch.Closed()
		if end := watch.End(); end.Kind != chord.WatchEnded {
			t.Fatalf("end = %+v, want ended", end)
		}
		// The watch stays referenced here.
		nexttest.ExpectCollected(t, derive)
		nexttest.ExpectCollected(t, document)
		runtime.KeepAlive(watch)
	})
}
