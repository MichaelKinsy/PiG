package state_test

import (
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/state/watchable.test.ts, the describe "constant". The blocks "watches" and "subscribers"
// run on a ChordHost with a defineState counter (c.num.watch, resolveWatchable); watchable_own_test.go runs them on a
// Source, and the host variants wait for the host, contracts and documents packages.

func TestConstant(t *testing.T) {
	t.Run("never changes; its watches end only by stop or cancellation and hold nothing after", func(t *testing.T) {
		start := state.NewConstant(delta.JsonObjectOf("id", "S1"))
		value, err := start.Value()
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		if got := value.(*delta.JsonObject); got.Value("id") != "S1" {
			t.Fatalf("value = %v", got)
		}
		ref := func() weak.Pointer[chord.Watch[delta.JsonValue]] {
			watch, err := start.OpenWatch(background)
			if err != nil {
				t.Fatalf("OpenWatch: %v", err)
			}
			if watch.Value() != value {
				t.Fatal("the watch does not start at the constant's value")
			}
			end, err := watch.Stop()
			if err != nil || end != (chord.WatchEnd{Kind: chord.WatchStopped}) {
				t.Fatalf("Stop = %+v, %v; want stopped", end, err)
			}
			return weak.Make(watch)
		}()
		nexttest.ExpectCollected(t, ref)
	})
}
