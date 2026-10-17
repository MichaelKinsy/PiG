package node

import (
	"bytes"
	"context"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// abortAfterContext reports no cancellation for its first failFrom-1 Err calls and cancellation from the failFrom-th on, so a test can cancel a
// BinaryReader call after its first check (before the read) and before its second (after the read), the two abortResult sites of node.ts:477 and :489.
type abortAfterContext struct {
	context.Context
	failFrom int
	calls    int
}

func (c *abortAfterContext) Err() error {
	c.calls++
	if c.calls >= c.failFrom {
		return context.Canceled
	}
	return nil
}

// upstream: packages/durable/src/env/node.ts NodeBinaryReader.read (:476-506): Number.isSafeInteger bounds offset and length, so 2^53-1 is accepted (it reads
// nothing at that offset, or the whole file for that length) and 2^53 is invalid; a read at or past the end is an empty Uint8Array, never undefined.
// mutation-checked: `offset >= maxSafeInteger` and `length >= maxSafeInteger` each fail it.
func TestBinaryReaderReadAcceptsTheLargestSafeIntegerAndRefusesTheNext(t *testing.T) {
	const largestSafe = int64(1)<<53 - 1
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "data.txt", []byte("hello")))
	reader := must(env.OpenBinaryReader(background, "data.txt", nil))
	defer func() { _ = reader.Close(background) }()

	atEnd, err := reader.Read(background, largestSafe, 1)
	if err != nil || atEnd == nil || len(atEnd) != 0 {
		t.Fatalf("Read(2^53-1, 1) = %v, %v, want an empty non-nil result", atEnd, err)
	}
	if past, err := reader.Read(background, 5, 3); err != nil || past == nil || len(past) != 0 {
		t.Fatalf("Read at the end = %v, %v, want an empty non-nil result", past, err)
	}
	whole, err := reader.Read(background, 0, largestSafe)
	if err != nil || !bytes.Equal(whole, []byte("hello")) {
		t.Fatalf("Read(0, 2^53-1) = %q, %v, want the whole file without allocating the length", whole, err)
	}
	for _, c := range [][2]int64{{largestSafe + 1, 0}, {0, largestSafe + 1}} {
		if _, err := reader.Read(background, c[0], c[1]); !isFileError(err, durableenv.FileErrorInvalid) {
			t.Errorf("Read(%d, %d) error = %v, want an invalid FileError", c[0], c[1], err)
		}
	}
}

// upstream: packages/durable/src/env/node.ts:55 BINARY_READ_CHUNK and :487-497: a read is made in chunks of at most 1 MiB so a huge length allocates only as much
// as the file yields, and the chunks are joined in order (:499-505). A file of two and a half chunks plus a byte is read whole, with each chunk's bytes at their place.
// mutation-checked: allocating `length-total` per chunk panics on the 2^53-1 length of the sibling test; dropping the append of later chunks fails this one.
func TestBinaryReaderReadJoinsChunksInOrder(t *testing.T) {
	const chunk = 1024 * 1024
	data := make([]byte, 2*chunk+chunk/2+1)
	for i := range data {
		data[i] = byte(i % 251)
	}
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "big.bin", data))
	reader := must(env.OpenBinaryReader(background, "big.bin", nil))
	defer func() { _ = reader.Close(background) }()
	if got := must(reader.Read(background, 0, int64(len(data))+10)); !bytes.Equal(got, data) {
		t.Fatalf("Read(0, size+10) returned %d bytes that differ from the file's %d", len(got), len(data))
	}
	if got := must(reader.Read(background, chunk-3, 8)); !bytes.Equal(got, data[chunk-3:chunk+5]) {
		t.Fatalf("a read across a chunk boundary = %v, want %v", got, data[chunk-3:chunk+5])
	}
}

// upstream: packages/durable/src/env/node.ts:477 and :489 (read), :512 and :525 (scanLines): the abort signal is checked before the call and again after
// each file read, and an abort found after the read returns the aborted error, not the bytes or the scan.
// mutation-checked: removing the after-read check in Read, and in ScanLines, each fail it.
func TestBinaryReaderCallsReportAnAbortRaisedDuringTheRead(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "data.txt", []byte("one\ntwo\n")))
	reader := must(env.OpenBinaryReader(background, "data.txt", nil))
	defer func() { _ = reader.Close(background) }()

	for _, failFrom := range []int{1, 2} {
		ctx := &abortAfterContext{Context: background, failFrom: failFrom}
		if got, err := reader.Read(ctx, 0, 4); !isFileError(err, durableenv.FileErrorAborted) || got != nil {
			t.Errorf("Read aborted at check %d = %q, %v, want the aborted FileError and no bytes", failFrom, got, err)
		}
		ctx = &abortAfterContext{Context: background, failFrom: failFrom}
		if scan, err := reader.ScanLines(ctx, durableenv.ScanLinesOptions{StartLine: 0}); !isFileError(err, durableenv.FileErrorAborted) || scan != (durableenv.LineScan{}) {
			t.Errorf("ScanLines aborted at check %d = %+v, %v, want the aborted FileError and no scan", failFrom, scan, err)
		}
	}
	if got := must(reader.Read(&abortAfterContext{Context: background, failFrom: 1 << 30}, 0, 3)); string(got) != "one" {
		t.Fatalf("a call that is never aborted read %q, want one", got)
	}
}
