package tools

import (
	"os"
	"strings"
	"testing"
)

// output-accumulator.ts closeTempFile (:121) awaits the full-output stream's finish and rejects when the stream failed, so bash.ts never reads a file
// whose tail was lost without being told. CloseTempFile is the awaited step: it returns the flush or close failure of the file, and it releases the file so
// a repeated call (bash.ts closes once per run) has nothing left to fail on.
func TestOutputAccumulatorCloseTempFileReportsAFailedFile(t *testing.T) {
	accumulator := newOutputAccumulator(1<<30, 1<<10, "pi-async-test")
	if err := accumulator.Append([]byte(strings.Repeat("z", 2<<10))); err != nil {
		t.Fatal(err)
	}
	path := accumulator.Snapshot(true).FullOutputPath
	if path == "" {
		t.Fatal("truncated output has no full-output path")
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := accumulator.Append([]byte("buffered tail\n")); err != nil {
		t.Fatal(err)
	}
	if accumulator.tempWriter.Buffered() == 0 {
		t.Fatal("the tail was written through; the test needs bytes still buffered when the file fails")
	}
	// The stream fails under the accumulator: the descriptor is closed before the buffered tail is flushed.
	if err := accumulator.tempFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.CloseTempFile(); err == nil {
		t.Fatal("CloseTempFile resolved although the buffered tail could not be written")
	}
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatalf("a second CloseTempFile has no file left to fail on, got %v", err)
	}
}
