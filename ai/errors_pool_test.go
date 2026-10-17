package ai

import (
	"math"
	"strings"
	"testing"
)

// A large value does not stay pinned in the SafeJsonStringify pool: a state whose buffers grew past 64 KiB is dropped on Put (golang/go#23199),
// so one 1 MiB tool argument does not keep megabytes alive per pooled state.
func TestSafeJsonStringifyPoolDropsLargeBuffers(t *testing.T) {
	payload := map[string]any{"content": strings.Repeat("x", 1<<20), "n": math.Copysign(0, -1)}
	if got := SafeJsonStringify(payload); len(got) < 1<<20 {
		t.Fatalf("SafeJsonStringify wrote %d bytes", len(got))
	}
	state := safeJSONEncoders.Get().(*safeJSONEncoder)
	defer safeJSONEncoders.Put(state)
	if capacity := max(state.buffer.Cap(), cap(state.canonical)); capacity > 64<<10 {
		t.Fatalf("the pooled SafeJsonStringify state keeps a %d-byte buffer after a 1 MiB value, want at most 64 KiB", capacity)
	}
}
