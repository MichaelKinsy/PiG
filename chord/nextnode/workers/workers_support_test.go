package workers

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func jsonObject(pairs ...any) *delta.JsonObject { return delta.JsonObjectOf(pairs...) }

// until waits until ready() holds, polling every 10 ms, for at most ms (test/next-node/workers/support.ts `until`).
func until(t testing.TB, ready func() bool, ms ...int) {
	t.Helper()
	limit := 10_000
	if len(ms) > 0 {
		limit = ms[0]
	}
	deadline := time.Now().Add(time.Duration(limit) * time.Millisecond)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("until: timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// expectCollected expects every referent to be collected once nothing else holds it (test/support.ts
// `expectCollected`).
func expectCollected[T any](t testing.TB, refs ...weak.Pointer[T]) {
	t.Helper()
	for range 10 {
		runtime.GC()
		held := false
		for _, ref := range refs {
			held = held || ref.Value() != nil
		}
		if !held {
			return
		}
		time.Sleep(time.Millisecond)
	}
	for index, ref := range refs {
		if ref.Value() != nil {
			t.Errorf("referent %d was not collected", index)
		}
	}
}

// loadHost is `await host.load([], context)`: it fails the test when the load fails.
func loadHost(t testing.TB, h host.Host) {
	t.Helper()
	if _, err := h.Load(nil, background); err != nil {
		t.Fatal(err)
	}
}
