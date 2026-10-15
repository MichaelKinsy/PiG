package tui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// bash-execution.ts updateDisplay shows only truncateTail(output) (the last 2000 lines, 50 KB), so the block's per-frame work must follow what it shows, not
// how much output has streamed in. Re-truncating the whole output on every frame made a 10 MB stream cost milliseconds and megabytes per frame. A render
// after a megabyte-scale stream allocates in proportion to the window.
//
// mutation-checked: re-truncating the whole accumulated output in updateDisplay allocates the line index of the whole output and fails it.
func TestBashExecutionRenderCostDoesNotGrowWithTheStreamedOutput(t *testing.T) {
	block := NewBashExecutionComponent("yes", nil, false, 1)
	streamed := 0
	for i := range 100_000 {
		chunk := fmt.Sprintf("line %06d of the streamed output\n", i)
		streamed += len(chunk)
		block.AppendOutput(chunk)
	}
	if streamed < 3_000_000 {
		t.Fatalf("streamed %d bytes, want a multi-megabyte stream", streamed)
	}
	block.Render(100)
	block.AppendOutput("one more line\n")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	lines := block.Render(100)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512*1024 {
		t.Fatalf("one render after %d bytes of output allocated %d bytes, want it to follow the 50 KB window", streamed, allocated)
	}
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "one more line") {
		t.Fatalf("the running render lost the tail:\n%s", text)
	}
	code := 0
	block.SetComplete(&code, false, nil, "")
	if text := strings.Join(block.Render(100), "\n"); !strings.Contains(text, "one more line") || !strings.Contains(text, "more lines") {
		t.Fatalf("the completed render lost the tail or the hidden-lines row:\n%s", text)
	}
}
