package tui

import (
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"
)

// A running `!cmd` block shows the tail window of its output (truncateTail's line and byte limits, as Pi's updateDisplay
// does), so streaming a large output must keep about the window, not every byte the command printed: the block's
// strings.Builder holds the whole stream next to the TailWindow (TO-T2 rest).
func TestBashExecutionStreamingRetainsTheTailNotTheWholeOutput(t *testing.T) {
	line := strings.Repeat("0123456789abcdef", 4) + "\n"
	chunk := strings.Repeat(line, 1024) // 65 KB
	const chunks = 512                  // 33 MB streamed
	live := func() uint64 {
		runtime.GC()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	before := live()
	block := NewBashExecutionComponent("yes", nil, false, 1)
	for range chunks {
		block.AppendOutput(chunk)
	}
	retained := int64(live()) - int64(before)
	runtime.KeepAlive(block)
	runtime.KeepAlive(chunk)
	const limit = 4 << 20
	t.Logf("streamed %d MB, block retains %.1f MB", chunks*len(chunk)>>20, float64(retained)/(1<<20))
	if retained > limit {
		t.Fatalf("a running bash block retains %.1f MB after streaming %d MB, want at most %d MB (the tail window plus slack)", float64(retained)/(1<<20), chunks*len(chunk)>>20, limit>>20)
	}
}
