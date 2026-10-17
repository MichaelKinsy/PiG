package tools

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// Pi output-accumulator.ts:253 writes every chunk to the full-output stream and hands the path out in snapshots. The Go accumulator buffers those writes (a
// syscall per small chunk was most of an Append), so the file must hold every byte appended so far whenever its path is handed out, when the full output is
// read, and after the file closes; the buffered bytes are never lost or reordered.
func TestOutputAccumulatorBufferedFileHoldsEveryByteWhenItsPathIsHandedOut(t *testing.T) {
	accumulator := newOutputAccumulator(1<<30, 1<<10, "pi-buffer-test")
	var want bytes.Buffer
	chunk := []byte(strings.Repeat("a line of shell output\n", 3))
	for i := range 400 {
		piece := append([]byte(nil), chunk...)
		piece[0] = byte('A' + i%26)
		want.Write(piece)
		if err := accumulator.Append(piece); err != nil {
			t.Fatal(err)
		}
		if i%97 == 0 && want.Len() > 1<<10 {
			path := accumulator.Snapshot(true).FullOutputPath
			if path == "" {
				t.Fatal("a snapshot of truncated output has no full-output path")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("after %d chunks the file holds %d bytes (err %v), want the %d appended so far", i+1, len(got), err, want.Len())
			}
		}
	}
	accumulator.Finish()
	full, err := accumulator.ReadFullOutput(1 << 30)
	if err != nil || full.Content != want.String() {
		t.Fatalf("ReadFullOutput before the file closes returned %d bytes (err %v), want %d", len(full.Content), err, want.Len())
	}
	path := accumulator.Snapshot(false).FullOutputPath
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("after CloseTempFile the file holds %d bytes (err %v), want %d", len(got), err, want.Len())
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

// Small chunks are coalesced: appends to a full tail leave their bytes in the writer's buffer until something needs the file, so a chunk costs a copy and
// not a write syscall.
func TestOutputAccumulatorCoalescesSmallChunkWrites(t *testing.T) {
	accumulator := newOutputAccumulator(1<<30, 1<<10, "pi-buffer-test")
	chunk := []byte(strings.Repeat("x", 71) + "\n")
	for range 100 {
		_ = accumulator.Append(chunk)
	}
	if accumulator.tempWriter == nil || accumulator.tempWriter.Buffered() == 0 {
		t.Fatal("chunks appended past the limit were written through one by one")
	}
	path := accumulator.Snapshot(true).FullOutputPath
	if accumulator.tempWriter.Buffered() != 0 {
		t.Fatalf("%d bytes stay buffered after the path was handed out", accumulator.tempWriter.Buffered())
	}
	_ = accumulator.CloseTempFile()
	_ = os.Remove(path)
}

// Closing the file flushes what is still buffered: output appended after the last snapshot is in the file once CloseTempFile returns, with nothing between
// the last append and the close that would flush it.
func TestOutputAccumulatorCloseWritesTheBufferedTail(t *testing.T) {
	accumulator := newOutputAccumulator(1<<30, 1<<10, "pi-buffer-test")
	var want bytes.Buffer
	for i := range 60 {
		piece := []byte(strings.Repeat("y", 40) + string(rune('a'+i%26)) + "\n")
		want.Write(piece)
		_ = accumulator.Append(piece)
	}
	accumulator.Finish()
	path := accumulator.tempFilePath
	if path == "" {
		t.Fatal("output past the limit has no file")
	}
	if err := accumulator.CloseTempFile(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("the closed file holds %d bytes (err %v), want %d", len(got), err, want.Len())
	}
}
