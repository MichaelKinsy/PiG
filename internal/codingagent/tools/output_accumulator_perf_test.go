package tools

import (
	"os"
	"strings"
	"testing"
)

// truncateHead and truncateTail return a substring of their input, so cutting a large output builds no per-line strings and joins nothing.
func TestToolTruncationCutsWithoutCopying(t *testing.T) {
	content := strings.Repeat("a line of command output\n", 8000)
	for name, cut := range map[string]func(){
		"tail": func() { _ = TruncateTail(content, TruncationOptions{}) },
		"head": func() { _ = TruncateHead(content, TruncationOptions{}) },
	} {
		if allocs := testing.AllocsPerRun(20, cut); allocs != 0 {
			t.Errorf("%s truncation of 8000 lines: %v allocations, want 0", name, allocs)
		}
	}
}

// A chunk appended to the accumulator costs no allocation of its own: it is decoded straight into the tail, and kept raw bytes (before the full-output
// file exists) go into one growing buffer. Over 1000 chunks, past the 50KB limit into the file, the amortized growth and the file's setup stay far below
// one allocation per chunk.
func TestOutputAccumulatorAppendAllocsPerChunk(t *testing.T) {
	chunk := []byte(strings.Repeat("x", 99) + "\n")
	const chunks = 1000
	var paths []string
	allocs := testing.AllocsPerRun(5, func() {
		accumulator := NewOutputAccumulator("pi-allocs-test")
		for range chunks {
			_ = accumulator.Append(chunk)
		}
		_ = accumulator.CloseTempFile()
		paths = append(paths, accumulator.tempFilePath)
	})
	for _, path := range paths {
		_ = os.Remove(path)
	}
	if perChunk := allocs / chunks; perChunk > 0.1 {
		t.Fatalf("%.2f allocations per appended chunk, want at most 0.1", perChunk)
	}
}

// A snapshot copies only the kept text, once, and a snapshot of unchanged output (the final update, then the result) copies nothing.
func TestOutputAccumulatorSnapshotCopiesOnlyTheCut(t *testing.T) {
	accumulator := NewOutputAccumulator("pi-allocs-test")
	for range 4000 {
		_ = accumulator.Append([]byte("a line of command output\n"))
	}
	defer func() {
		_ = accumulator.CloseTempFile()
		_ = os.Remove(accumulator.tempFilePath)
	}()
	_ = accumulator.Snapshot(true)
	chunk := []byte("one more line\n")
	if allocs := testing.AllocsPerRun(20, func() {
		_ = accumulator.Append(chunk)
		_ = accumulator.Snapshot(true)
	}); allocs > 1 {
		t.Errorf("append and snapshot: %v allocations, want at most 1 (the kept text)", allocs)
	}
	first := accumulator.Snapshot(true)
	if allocs := testing.AllocsPerRun(20, func() { _ = accumulator.Snapshot(true) }); allocs != 0 {
		t.Errorf("snapshot of unchanged output: %v allocations, want 0", allocs)
	}
	if again := accumulator.Snapshot(true); again != first {
		t.Errorf("snapshot of unchanged output changed: %+v, then %+v", first.Truncation, again.Truncation)
	}
}
