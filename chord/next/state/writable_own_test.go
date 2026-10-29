package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go tests of WritableSource on a Batch: the scenarios of writable.test.ts "writable", which upstream runs on a ChordHost
// whose objects own the writables (those tests wait for the host, contracts and registries packages).

// attempt is the WritableOwner of a test: its context and the check that its attempt still runs.
type attempt struct {
	ctx    context.Context
	sealed error
}

func (a *attempt) Context() context.Context { return a.ctx }
func (a *attempt) AssertUsable() error      { return a.sealed }

// counterOf is `app.counter`: a writable number and its double as a derive.
type counterOf struct {
	h        *harness
	owner    *attempt
	writable *state.WritableSource[float64]
	doubled  tracked
}

func newCounter(t *testing.T, metrics func(testing.TB) *nexttest.Meter) (*counterOf, *nexttest.Meter) {
	var meter *nexttest.Meter
	var h *harness
	if metrics != nil {
		meter = metrics(t)
		h = newHarness(t, meter.Telemetry.Metrics())
	} else {
		h = newHarness(t, nil)
	}
	owner := &attempt{ctx: background}
	writable := state.NewWritableSource[float64](1, owner, h.batch)
	doubled := derived(h, []any{writable}, func(v []any) (delta.JsonValue, error) { return v[0].(float64) * 2, nil })
	return &counterOf{h: h, owner: owner, writable: writable, doubled: doubled}, meter
}

func (c *counterOf) set(t *testing.T, value float64) {
	t.Helper()
	if err := c.writable.Set(value); err != nil {
		t.Fatalf("Set(%v): %v", value, err)
	}
}

func doubledOf(watch chord.WatchHandle[delta.JsonValue]) *nexttest.Collector[float64] {
	values := &nexttest.Collector[float64]{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			values.Append(call.Value.(float64))
		}
		return nil
	})
	return values
}

func TestWritable(t *testing.T) {
	t.Run("a new value (by identity) is a new frame; the same value notifies nobody and costs one comparison", func(t *testing.T) {
		c, meter := newCounter(t, nexttest.Metered)
		calls := nativeCalls(nativeWatchOf[float64](t, c.writable))
		doubled := doubledOf(watchOf(t, c.doubled.derive))
		computes := meter.Counts()["chord.derive.computes"]
		c.set(t, 1)
		c.h.delivered()
		requireNative(t, calls)
		if meter.Counts()["chord.derive.computes"] != computes {
			t.Fatal("setting the same value computed a derive")
		}
		// Both sets land in one turn, as the commits of one microtask turn do upstream.
		release := c.h.scheduler.Hold()
		c.set(t, 2)
		c.set(t, 3)
		release()
		c.h.delivered()
		requireNative(t, calls, valueN(2), valueN(3))
		// A JSON watch folds what came while it waited: the derive's last value.
		if got := doubled.Items(); !reflect.DeepEqual(got, []float64{6}) {
			t.Fatalf("doubled = %v, want [6]", got)
		}
		if c.writable.Value() != 3 {
			t.Fatalf("Value = %v, want 3", c.writable.Value())
		}
	})

	t.Run("inside a batch, sets mark it there: one frame with the last value, the derives over it computed once", func(t *testing.T) {
		c, meter := newCounter(t, nexttest.Metered)
		calls := nativeCalls(nativeWatchOf[float64](t, c.writable))
		doubled := doubledOf(watchOf(t, c.doubled.derive))
		computes := meter.Counts()["chord.derive.computes"]
		if err := c.h.batch.Commit(func() error {
			if err := c.writable.Set(5); err != nil {
				return err
			}
			return c.writable.Set(6)
		}, background); err != nil {
			t.Fatal(err)
		}
		c.h.delivered()
		requireNative(t, calls, valueN(6))
		if got := doubled.Items(); !reflect.DeepEqual(got, []float64{12}) {
			t.Fatalf("doubled = %v, want [12]", got)
		}
		if got := meter.Counts()["chord.derive.computes"]; got != computes+1 {
			t.Fatalf("computes = %v, want %v", got, computes+1)
		}
	})

	t.Run("refuses a set inside a derive compute (invalid/reentrant)", func(t *testing.T) {
		h := newHarness(t, nil)
		own := state.NewWritableSource[float64](0, &attempt{ctx: background}, h.batch)
		var attempted error
		bad := derived(h, []any{own}, func(v []any) (delta.JsonValue, error) {
			attempted = own.Set(v[0].(float64) + 1)
			return v[0], attempted
		})
		_, err := bad.derive.Value()
		requireReentrant(t, "set in a compute", attempted)
		requireReentrant(t, "the derive's failure", err)
		if own.Value() != 0 {
			t.Fatalf("a refused set changed the value to %v", own.Value())
		}
		// A set in a batch's adoption lands in that batch.
		if err := h.batch.Commit(func() error { return own.Set(2) }, background); err != nil || own.Value() != 2 {
			t.Fatalf("Set in a commit: %v, value %v", err, own.Value())
		}
	})

	t.Run("is the attempt's: Set after its end returns its error, its watches end ended, its value stays readable", func(t *testing.T) {
		c, _ := newCounter(t, nil)
		watch := nativeWatchOf[float64](t, c.writable)
		ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.restartable(1): ended"}, nil)
		c.writable.Detach(ended)
		<-watch.Closed()
		if end := watch.End(); end != (chord.WatchEnd{Kind: chord.WatchEnded}) {
			t.Fatalf("end = %+v, want ended", end)
		}
		if err := c.writable.Set(2); !errors.Is(err, ended) {
			t.Fatalf("Set after the end = %v, want %v", err, ended)
		}
		if c.writable.Value() != 1 {
			t.Fatalf("Value = %v, want 1", c.writable.Value())
		}
		// A set of the same value after its attempt ended throws released too.
		if err := c.writable.Set(1); !errors.Is(err, ended) {
			t.Fatalf("Set of the same value after the end = %v, want %v", err, ended)
		}
	})

	t.Run("is the attempt's until it is sealed: Set returns what AssertUsable refuses, even for the same value", func(t *testing.T) {
		c, _ := newCounter(t, nil)
		c.owner.sealed = errors.New("sealed")
		if err := c.writable.Set(1); err != c.owner.sealed {
			t.Fatalf("Set = %v, want the seal's error", err)
		}
		if err := c.writable.Set(2); err != c.owner.sealed || c.writable.Value() != 1 {
			t.Fatalf("Set = %v, value %v", err, c.writable.Value())
		}
	})

	t.Run("is a derive source", func(t *testing.T) {
		c, _ := newCounter(t, nil)
		c.set(t, 4)
		if got := mustValue(t, c.doubled.derive); got != float64(8) {
			t.Fatalf("doubled = %v, want 8", got)
		}
	})

	t.Run("is collected with its attempt", func(t *testing.T) {
		h := newHarness(t, nil)
		ref := func() weak.Pointer[state.WritableSource[[]int]] {
			owner := &attempt{ctx: background}
			writable := state.NewWritableSource(make([]int, 1000), owner, h.batch)
			writable.Detach(errors.New("ended"))
			return weak.Make(writable)
		}()
		nexttest.ExpectCollected(t, ref)
	})

	t.Run("a writable an app keeps after its host closed keeps no host; its observers' abort listeners go", func(t *testing.T) {
		c, _ := newCounter(t, nil)
		cancellable, cancel := chordctx.WithCancel(background)
		o := newObserver(c.h.batch)
		if _, err := c.writable.Observe(o, cancellable); err != nil {
			t.Fatal(err)
		}
		c.writable.Detach(errors.New("closed"))
		if len(o.ends) != 1 || o.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("observer ends = %+v, want ended", o.ends)
		}
		if c.writable.Batch() != nil {
			t.Fatal("a detached writable keeps its host's batch")
		}
		// The abort listener was removed: aborting afterwards ends nothing again.
		cancel(nil)
		c.h.delivered()
		if len(o.ends) != 1 {
			t.Fatalf("observer ends after the abort = %+v, want only ended", o.ends)
		}
		if c.writable.Value() != 1 {
			t.Fatalf("Value = %v, want 1", c.writable.Value())
		}
	})

	t.Run("a late callback's Set after the host closed returns the released error", func(t *testing.T) {
		c, _ := newCounter(t, nil)
		released := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "app.counter: ended"}, nil)
		c.writable.Detach(released)
		err := c.writable.Set(9)
		if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok || chordErr != released {
			t.Fatalf("Set = %v, want released", err)
		}
	})
}

func TestIdentical(t *testing.T) {
	type pair struct{ a, b int }
	slice := make([]int, 2)
	mapped := map[string]int{}
	number := 3
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"equal numbers", float64(1), float64(1), true},
		{"NaN", nanValue(), nanValue(), false},
		{"equal strings", "a", "a", true},
		{"other strings", "a", "b", false},
		{"same pointer", &number, &number, true},
		{"other pointer", &number, new(int), false},
		{"same slice", slice, slice, true},
		{"other slice", slice, make([]int, 2), false},
		{"shorter view of a slice", slice, slice[:1], false},
		{"same map", mapped, mapped, true},
		{"other map", mapped, map[string]int{}, false},
		{"equal comparable structs", pair{1, 2}, pair{1, 2}, true},
		{"nil and nil", nil, nil, true},
		{"nil and value", nil, float64(1), false},
		{"uncomparable structs are no identity", struct{ s []int }{slice}, struct{ s []int }{slice}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.Identical(tc.a, tc.b); got != tc.want {
				t.Fatalf("Identical(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
	t.Run("functions are identical by closure", func(t *testing.T) {
		closure := func(n int) func() int { return func() int { return n } }
		f, g := closure(1), closure(1)
		if !state.Identical(f, f) || state.Identical(f, g) {
			t.Fatal("closures are compared by code instead of by closure")
		}
	})
}
