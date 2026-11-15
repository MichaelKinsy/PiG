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

// Ports packages/chord/test/state/derive.test.ts: the describe "mapShared" and, new in Pi f6600b58f347, the describe "a
// watched derive read between two changes of its source in one batch" (TestDeriveReadBetweenTwoChangesInOneBatch).
// Every other block runs derives on a ChordHost through self.derive, defineState and references; derive_own_test.go runs
// the same scenarios on Derive itself, and the host variants wait for the host, contracts and documents packages.

type element struct{ n int }

type note struct{ text string }

type noteWithTime struct {
	text string
	at   int
}

func TestMapShared(t *testing.T) {
	t.Run("maps an object element once while it stays the same object; primitives every time", func(t *testing.T) {
		var calls []any
		f := func(x any) int {
			calls = append(calls, x)
			if n, ok := x.(int); ok {
				return n
			}
			return x.(*element).n
		}
		mapped := state.MapShared(f)
		a, b := &element{n: 1}, &element{n: 2}
		if got, want := mapped([]any{a, b, 3}), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
			t.Fatalf("first map = %v, want %v", got, want)
		}
		c := &element{n: 2}
		if got, want := mapped([]any{a, c, 3}), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
			t.Fatalf("second map = %v, want %v", got, want)
		}
		if len(calls) != 5 || calls[0] != any(a) || calls[1] != any(b) || calls[2] != 3 || calls[3] != any(c) || calls[4] != 3 {
			t.Fatalf("calls = %v, want [a b 3 c 3]", calls)
		}
	})

	t.Run("attaches its cleanups to tiny elements allocated right after the cache", func(t *testing.T) {
		// runtime.AddCleanup refuses a cleanup whose closure points into the allocation block of its object; a small
		// pointer-free element shares a 16-byte tiny block with whatever was allocated just before it.
		for n := range 256 {
			mapped := state.MapShared(func(e *element) int { return e.n })
			e := &element{n: n}
			if got := mapped([]*element{e}); len(got) != 1 || got[0] != n {
				t.Fatalf("map = %v, want [%d]", got, n)
			}
		}
	})

	t.Run("keeps no element alive in its cache", func(t *testing.T) {
		mapped := state.MapShared(func(n *note) *note { return &note{text: n.text} })
		refs := func() (weak.Pointer[note], weak.Pointer[note]) {
			n := &note{text: "t"}
			result := mapped([]*note{n})[0]
			return weak.Make(n), weak.Make(result)
		}
		element, result := refs()
		nexttest.ExpectCollected(t, element, result)
		runtime.GC()
		if got := mapped([]*note{{text: "u"}}); len(got) != 1 || got[0].text != "u" {
			t.Fatalf("map after collection = %v", got)
		}
	})

	t.Run("returns the previous result object for an unchanged element", func(t *testing.T) {
		strip := state.MapShared(func(n *noteWithTime) *note { return &note{text: n.text} })
		n := &noteWithTime{text: "t", at: 1}
		first := strip([]*noteWithTime{n})[0]
		second := strip([]*noteWithTime{n, {text: "u", at: 2}})[0]
		if first != second {
			t.Fatal("the unchanged element was mapped again")
		}
	})
}

// cellSource is Pi's `Cell`: a source a test sets inside a batch; nobody watches it (its derives are what the tests
// follow).
type cellSource struct {
	state.Source
	value float64
}

func newCell(value float64, batch *state.Batch) *cellSource {
	c := &cellSource{value: value}
	c.Init(batch, c)
	return c
}

func (c *cellSource) Value() (delta.JsonValue, error) { return c.value, nil }

func (c *cellSource) put(value float64) {
	c.value = value
	c.Changed()
}

func (c *cellSource) Deliver(context.Context) error { return nil }

// cellDerive is one of Pi's `derived(sources, compute)` results: the derive, the value it was taken on with, and what its
// subscriber was advanced with.
type cellDerive struct {
	derive *state.Derive
	taken  delta.JsonValue
	sub    *recorder
}

// cellBatch is Pi's `cell()`: a cell at 1 in a batch counting on a meter, and derived(...).
type cellBatch struct {
	t      *testing.T
	h      *harness
	meter  *nexttest.Meter
	source *cellSource
}

func newCellBatch(t *testing.T) *cellBatch {
	t.Helper()
	meter := nexttest.Metered(t)
	h := newHarness(t, meter.Telemetry.Metrics())
	return &cellBatch{t: t, h: h, meter: meter, source: newCell(1, h.batch)}
}

func (c *cellBatch) derived(sources []any, compute func(values []any) (delta.JsonValue, error)) cellDerive {
	c.t.Helper()
	derive := state.NewDerive(sources, compute, c.h.batch)
	if err := derive.Attach(func(source any, _ int) (state.DeriveInput, error) { return source.(state.DeriveInput), nil }, func(error) {}); err != nil {
		c.t.Fatalf("Attach: %v", err)
	}
	// As a subscriber takes it on: the current value first.
	taken := mustValue(c.t, derive)
	sub := &recorder{}
	derive.Subscribe(sub)
	return cellDerive{derive: derive, taken: taken, sub: sub}
}

func (c *cellBatch) commit(work func()) {
	c.t.Helper()
	if err := c.h.batch.Commit(func() error { work(); return nil }, background); err != nil {
		c.t.Fatalf("Commit: %v", err)
	}
}

func (c *cellBatch) requireNoErrors() {
	c.t.Helper()
	if got := c.h.errors.all(); len(got) != 0 {
		c.t.Fatalf("batch errors = %+v, want none", got)
	}
}

// opsOf is Pi's `opsOf`: the ops of the value call call.
func opsOf(t *testing.T, call chord.WatchCall[delta.JsonValue]) []delta.Op {
	t.Helper()
	if call.Kind != chord.WatchCallValue {
		t.Fatalf("call = %+v, want a value call", call)
	}
	return call.Ops
}

func nBig(n float64, big bool) *delta.JsonObject { return delta.JsonObjectOf("n", n, "big", big) }

func TestDeriveReadBetweenTwoChangesInOneBatch(t *testing.T) {
	t.Run("delivers one frame: its last value, diffed from the value before the batch (one diff)", func(t *testing.T) {
		c := newCellBatch(t)
		// `big` changes with the first change only: a diff from the value read in between would not carry it.
		d := c.derived([]any{c.source}, func(values []any) (delta.JsonValue, error) {
			n := values[0].(float64)
			return nBig(n, n >= 2), nil
		})
		before := c.meter.Counts()["chord.derive.diffs"]
		var read []any
		c.commit(func() {
			c.source.put(2)
			// As a reference binding in the same commit reads the member it binds to.
			read = append(read, mustValue(t, d.derive))
			c.source.put(3)
		})
		if want := []any{nBig(2, true)}; !reflect.DeepEqual(read, want) {
			t.Fatalf("read = %v, want %v", read, want)
		}
		c.requireNoErrors()
		if len(d.sub.calls) != 1 || d.sub.calls[0].Kind != chord.WatchCallValue || !reflect.DeepEqual(d.sub.calls[0].Value, any(nBig(3, true))) {
			t.Fatalf("calls = %+v, want one value call {n:3 big:true}", d.sub.calls)
		}
		// The ops apply to what the subscriber had, not to the value read in between.
		if applied, err := delta.ApplyImmutable(d.taken, opsOf(t, d.sub.calls[0])); err != nil || !reflect.DeepEqual(applied, any(nBig(3, true))) {
			t.Fatalf("ApplyImmutable(taken, ops) = %v, %v; want {n:3 big:true}", applied, err)
		}
		if got := c.meter.Counts()["chord.derive.diffs"] - before; got != 1 {
			t.Fatalf("diffs = %v, want 1", got)
		}
		// The next batch starts from the last value.
		c.commit(func() { c.source.put(4) })
		if len(d.sub.calls) != 2 {
			t.Fatalf("calls = %d, want 2", len(d.sub.calls))
		}
		if got, want := opsOf(t, d.sub.calls[1]), ops(op("s", delta.Path{"n"}, float64(4))); !reflect.DeepEqual(got, want) {
			t.Fatalf("second ops = %v, want %v", got, want)
		}
	})

	t.Run("delivers nothing when the batch ends at the value it started from", func(t *testing.T) {
		c := newCellBatch(t)
		d := c.derived([]any{c.source}, func(values []any) (delta.JsonValue, error) { return num(values[0].(float64)), nil })
		c.commit(func() {
			c.source.put(2)
			mustValue(t, d.derive)
			c.source.put(1)
		})
		if len(d.sub.calls) != 0 {
			t.Fatalf("calls = %+v, want none", d.sub.calls)
		}
	})

	t.Run("a derive over it is one frame too, read or not", func(t *testing.T) {
		c := newCellBatch(t)
		inner := c.derived([]any{c.source}, func(values []any) (delta.JsonValue, error) { return num(values[0].(float64)), nil })
		outer := c.derived([]any{inner.derive}, func(values []any) (delta.JsonValue, error) {
			return delta.JsonObjectOf("shown", fmt.Sprintf("n=%v", intOf(values[0]))), nil
		})
		c.commit(func() {
			c.source.put(2)
			mustValue(t, outer.derive)
			c.source.put(3)
			mustValue(t, inner.derive)
			c.source.put(4)
		})
		if len(inner.sub.calls) != 1 || len(outer.sub.calls) != 1 {
			t.Fatalf("calls: inner %d, outer %d; want 1 each", len(inner.sub.calls), len(outer.sub.calls))
		}
		if applied, err := delta.ApplyImmutable(inner.taken, opsOf(t, inner.sub.calls[0])); err != nil || !reflect.DeepEqual(applied, any(num(4))) {
			t.Fatalf("inner applied = %v, %v; want {n:4}", applied, err)
		}
		if applied, err := delta.ApplyImmutable(outer.taken, opsOf(t, outer.sub.calls[0])); err != nil || !reflect.DeepEqual(applied, any(delta.JsonObjectOf("shown", "n=4"))) {
			t.Fatalf("outer applied = %v, %v; want {shown:n=4}", applied, err)
		}
	})

	t.Run("a failure read in between and the value after it are one frame, the diff from the value before; no failure call", func(t *testing.T) {
		c := newCellBatch(t)
		two := errors.New("two")
		d := c.derived([]any{c.source}, func(values []any) (delta.JsonValue, error) {
			if values[0].(float64) == 2 {
				return nil, two
			}
			return num(values[0].(float64)), nil
		})
		c.commit(func() {
			c.source.put(2)
			if _, err := d.derive.Value(); !errors.Is(err, two) {
				t.Errorf("read in between error = %v, want two", err)
			}
			c.source.put(3)
		})
		if len(d.sub.calls) != 1 {
			t.Fatalf("calls = %d, want 1: %+v", len(d.sub.calls), d.sub.calls)
		}
		// Its watches had a value: a diff, not a replacement of the whole.
		if got, want := opsOf(t, d.sub.calls[0]), ops(op("s", delta.Path{"n"}, float64(3))); !reflect.DeepEqual(got, want) {
			t.Fatalf("ops = %v, want %v", got, want)
		}
		if applied, err := delta.ApplyImmutable(d.taken, opsOf(t, d.sub.calls[0])); err != nil || !reflect.DeepEqual(applied, any(num(3))) {
			t.Fatalf("applied = %v, %v; want {n:3}", applied, err)
		}
	})

	t.Run("a value read in between and the failure after it are one failure call", func(t *testing.T) {
		c := newCellBatch(t)
		three := errors.New("three")
		d := c.derived([]any{c.source}, func(values []any) (delta.JsonValue, error) {
			if values[0].(float64) == 3 {
				return nil, three
			}
			return num(values[0].(float64)), nil
		})
		c.commit(func() {
			c.source.put(2)
			mustValue(t, d.derive)
			c.source.put(3)
		})
		if len(d.sub.calls) != 1 || d.sub.calls[0].Kind != chord.WatchCallFailed || !errors.Is(d.sub.calls[0].Error, three) {
			t.Fatalf("calls = %+v, want one failure call 'three'", d.sub.calls)
		}
		// Healed in a later batch: the value whole.
		c.commit(func() { c.source.put(4) })
		if got, want := normalized(d.sub.calls[1:]), []call{valueCall(num(4), ops(op("r", num(4))), false)}; !reflect.DeepEqual(got, want) {
			t.Fatalf("calls after = %+v, want %+v", got, want)
		}
	})
}
