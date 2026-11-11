package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of the Batch on its own: the host-based upstream tests (batch.test.ts "reentrancy" and "delivery") exercise
// the same behavior through a ChordHost, whose package is not ported yet.

type listed struct {
	name  string
	order *[]string
	err   error
}

func (source *listed) Deliver(context.Context) error {
	*source.order = append(*source.order, source.name)
	return source.err
}

func requireReentrant(t *testing.T, what string, err error) {
	t.Helper()
	chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
	if !ok || chordErr.Reason != data.ReasonReentrant {
		t.Fatalf("%s: err = %v, want invalid/reentrant", what, err)
	}
}

func TestBatchReentrancy(t *testing.T) {
	t.Run("refuses watch-like calls inside a compute and an adoption, not a change inside an adoption", func(t *testing.T) {
		batch := state.NewBatch(func(state.BatchError) {}, nil)
		if batch.Running() {
			t.Fatal("a new batch runs")
		}
		if err := batch.AssertOutside("watch"); err != nil {
			t.Fatalf("outside: %v", err)
		}
		if err := batch.Commit(func() error {
			if !batch.Running() {
				t.Error("Running is false inside an adoption")
			}
			requireReentrant(t, "assertOutside in a commit", batch.AssertOutside("watch"))
			if err := batch.AssertNotComputing("writable.set"); err != nil {
				t.Errorf("a change inside an adoption is refused: %v", err)
			}
			return nil
		}, background); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if err := batch.Compute(func() error {
			requireReentrant(t, "assertOutside in a compute", batch.AssertOutside("watch"))
			requireReentrant(t, "assertNotComputing in a compute", batch.AssertNotComputing("writable.set"))
			// Nests: an inner compute leaves the flags as the outer one had them.
			if err := batch.Compute(func() error { return nil }); err != nil {
				t.Errorf("inner compute: %v", err)
			}
			requireReentrant(t, "after the inner compute", batch.AssertNotComputing("writable.set"))
			return nil
		}); err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if batch.Running() {
			t.Fatal("the batch still runs after the compute")
		}
	})

	t.Run("clears the flags after a compute that fails or panics", func(t *testing.T) {
		batch := state.NewBatch(func(state.BatchError) {}, nil)
		boom := errors.New("compute threw")
		if err := batch.Compute(func() error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("Compute = %v, want the compute's error", err)
		}
		if err := batch.AssertNotComputing("writable.set"); err != nil || batch.Running() {
			t.Fatalf("flags stay set after a failing compute: %v, running %v", err, batch.Running())
		}
		func() {
			defer func() { _ = recover() }()
			_ = batch.Compute(func() error { panic("boom") })
		}()
		if err := batch.AssertNotComputing("writable.set"); err != nil || batch.Running() {
			t.Fatalf("flags stay set after a panicking compute: %v, running %v", err, batch.Running())
		}
		if err := batch.Run(func() error { return boom }); !errors.Is(err, boom) || batch.Running() {
			t.Fatalf("Run = %v, running %v", err, batch.Running())
		}
	})
}

func TestBatchDelivery(t *testing.T) {
	t.Run("reports a delivery that fails and still delivers the later frames of the commit", func(t *testing.T) {
		h := newHarness(t, nil)
		var order []string
		bad := &listed{name: "bad", order: &order, err: errors.New("diff threw")}
		other := &listed{name: "other", order: &order}
		err := h.batch.Commit(func() error {
			order = append(order, "adopted")
			h.batch.Changed(bad)
			h.batch.Changed(other)
			return nil
		}, background)
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if want := []string{"adopted", "bad", "other"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		got := h.errors.all()
		if len(got) != 1 || got[0].Kind != state.BatchDeliveryFailed || got[0].Error.Error() != "diff threw" {
			t.Fatalf("reported = %+v, want one delivery_failed", got)
		}
	})

	t.Run("an adoption that fails ends the commit: nothing is delivered, the lists are cleared and the error returned", func(t *testing.T) {
		h := newHarness(t, nil)
		var order []string
		boom := errors.New("adopt threw")
		err := h.batch.Commit(func() error {
			h.batch.Changed(&listed{name: "a", order: &order})
			h.batch.Flushing(func(context.Context) error { order = append(order, "flush"); return nil })
			return boom
		}, background)
		if !errors.Is(err, boom) {
			t.Fatalf("Commit = %v, want %v", err, boom)
		}
		if len(order) != 0 {
			t.Fatalf("delivered %v after a failed adoption", order)
		}
		if err := h.batch.Commit(func() error { return nil }, background); err != nil || len(order) != 0 {
			t.Fatalf("a later commit delivers the old lists: %v %v", err, order)
		}
	})

	t.Run("a listener's error is reported as listener_error by ReportListener", func(t *testing.T) {
		h := newHarness(t, nil)
		boom := errors.New("listener threw")
		h.batch.ReportListener(boom)
		got := h.errors.all()
		if len(got) != 1 || got[0].Kind != state.BatchListenerError || !errors.Is(got[0].Error, boom) {
			t.Fatalf("reported = %+v", got)
		}
	})
}

func TestBatchWithin(t *testing.T) {
	t.Run("outside a commit it is a commit of its own; inside one it adopts there", func(t *testing.T) {
		h := newHarness(t, nil)
		var order []string
		source := &listed{name: "source", order: &order}
		if err := h.batch.Within(func() error {
			h.batch.Changed(source)
			order = append(order, "adopted")
			return nil
		}, background); err != nil {
			t.Fatalf("Within: %v", err)
		}
		if want := []string{"adopted", "source"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("outside: order = %v, want %v", order, want)
		}
		order = nil
		if err := h.batch.Commit(func() error {
			return h.batch.Within(func() error {
				h.batch.Changed(source)
				order = append(order, "inner")
				return nil
			}, background)
		}, background); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if want := []string{"inner", "source"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("inside: order = %v, want %v", order, want)
		}
	})

	t.Run("while the flushes send, it waits for the next commit, run right after", func(t *testing.T) {
		h := newHarness(t, nil)
		var order []string
		source := &listed{name: "source", order: &order}
		err := h.batch.Commit(func() error {
			h.batch.Flushing(func(context.Context) error {
				order = append(order, "send")
				// A link closing while it sends: its departure lands in the next batch.
				return h.batch.Within(func() error {
					h.batch.Changed(source)
					order = append(order, "departure")
					return nil
				}, background)
			})
			return nil
		}, background)
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if want := []string{"send", "departure", "source"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
	})
}

func TestBatchSoon(t *testing.T) {
	t.Run("collects the changes until the microtask into one commit, in the first one's context", func(t *testing.T) {
		h := newHarness(t, nil)
		var order []string
		first := context.WithValue(background, soonKey, "first")
		second := context.WithValue(background, soonKey, "second")
		var seen string
		source := &ctxSource{seen: &seen, order: &order}
		h.batch.Soon(func() { order = append(order, "a"); h.batch.Changed(source) }, first)
		h.batch.Soon(func() { order = append(order, "b") }, second)
		if h.micro.pending() != 1 {
			t.Fatalf("%d microtasks queued, want 1", h.micro.pending())
		}
		if len(order) != 0 {
			t.Fatalf("adopted before the microtask: %v", order)
		}
		h.micro.run()
		if want := []string{"a", "b", "delivered"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		if seen != "first" {
			t.Fatalf("delivery context = %q, want the first Soon's", seen)
		}
		// Another Soon queues a new microtask.
		h.batch.Soon(func() { order = append(order, "c") }, background)
		if h.micro.pending() != 1 {
			t.Fatalf("%d microtasks queued after the drain, want 1", h.micro.pending())
		}
		h.micro.run()
		if got := order[len(order)-1]; got != "c" {
			t.Fatalf("order = %v", order)
		}
	})
}

type soonCtxKey struct{}

var soonKey = soonCtxKey{}

type ctxSource struct {
	seen  *string
	order *[]string
}

func (source *ctxSource) Deliver(ctx context.Context) error {
	*source.order = append(*source.order, "delivered")
	*source.seen, _ = ctx.Value(soonKey).(string)
	return nil
}

func TestBatchCount(t *testing.T) {
	t.Run("counts derive and registry work on the host's metrics; without metrics it counts nothing", func(t *testing.T) {
		meter := nexttest.Metered(t)
		h := newHarness(t, meter.Telemetry.Metrics())
		h.batch.Count(state.CounterDeriveComputes)
		h.batch.Count(state.CounterDeriveComputes)
		h.batch.Count(state.CounterDeriveDiffs)
		h.batch.Count(state.CounterRegistryComposes)
		h.batch.Count(state.CounterRegistryAdapts)
		h.batch.Count(state.CounterContractAdapts)
		want := map[string]float64{
			"chord.derive.computes":   2,
			"chord.derive.diffs":      1,
			"chord.registry.composes": 1,
			"chord.registry.adapts":   1,
			"chord.contract.adapts":   1,
		}
		got := meter.Counts()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("counts = %v, want %v", got, want)
		}
		newHarness(t, nil).batch.Count(state.CounterDeriveComputes)
	})
}
