package state_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// NewDerive takes its sources as []any (the host's Self.derive types them, upstream's `Watchable | NativeWatchable`), so a
// wrong source is not a compile error here: it is refused when Attach resolves it, the resolver's error being Attach's.
// Upstream: native-source.test.ts "types a derive's sources: watchables of either kind, never a native's handle (Fable)".
func TestDeriveRefusesUnresolvedSource(t *testing.T) {
	refusal := errors.New("derive #1: source 1 is not a Chord watchable")
	resolve := func(source any, index int) (state.DeriveInput, error) {
		input, ok := source.(state.DeriveInput)
		if !ok {
			return nil, refusal
		}
		return input, nil
	}

	t.Run("Attach returns the resolver's refusal of a source that is no watchable", func(t *testing.T) {
		h := newHarness(t, nil)
		d := state.NewDerive([]any{state.NewConstant("a"), struct{ value int }{1}}, func([]any) (delta.JsonValue, error) {
			return float64(1), nil
		}, h.batch)
		if err := d.Attach(resolve, func(error) {}); !errors.Is(err, refusal) {
			t.Fatalf("Attach error = %v, want %v", err, refusal)
		}
	})

	t.Run("a refused Attach follows no source: an earlier source keeps no dependent", func(t *testing.T) {
		h := newHarness(t, nil)
		count := newFailable(h.batch, 1)
		d := state.NewDerive([]any{count, struct{ value int }{1}}, func([]any) (delta.JsonValue, error) {
			return float64(1), nil
		}, h.batch)
		if err := d.Attach(resolve, func(error) {}); !errors.Is(err, refusal) {
			t.Fatalf("Attach error = %v, want %v", err, refusal)
		}
		if got := count.DependentSet().Len(); got != 0 {
			t.Fatalf("dependents of the earlier source = %d, want 0", got)
		}
	})
}
