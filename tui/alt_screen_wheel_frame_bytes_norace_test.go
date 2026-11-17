//go:build !race

package tui

import "testing"

// The bytes a wheel frame allocates are flat over the length of the transcript: the mouse path used to copy the children and their heights for
// every event (O(children): 60 KB at 1k rows, 138 KB at 26k and 359 KB at 104k before, 56 to 57 KB at each after; BenchmarkAltScreenWheelScroll).
// The race detector's own allocations make the figure meaningless, so the test is built without it.
func TestAltScreenWheelFrameBytesDoNotGrowWithTheTranscript(t *testing.T) {
	small := wheelFrameBytes(wheelTranscripts[0].messages, 200)
	large := wheelFrameBytes(wheelTranscripts[2].messages, 200)
	t.Logf("wheel frame bytes: 1k rows %d, 104k rows %d", small, large)
	if large > small*5/4+4096 {
		t.Fatalf("a wheel frame allocates %d bytes at 104k rows against %d at 1k rows: it grows with the transcript", large, small)
	}
}
