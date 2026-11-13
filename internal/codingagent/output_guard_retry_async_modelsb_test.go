package codingagent

// pi: packages/coding-agent/src/core/output-guard.ts

import (
	"bytes"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// scriptedWriter answers each Write with the next step: it accepts accept bytes (all when accept < 0) and returns err.
type scriptedWriter struct {
	steps []writeStep
	got   bytes.Buffer
	calls int
}

type writeStep struct {
	accept int
	err    error
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	step := w.steps[w.calls]
	w.calls++
	n := step.accept
	if n < 0 || n > len(p) {
		n = len(p)
	}
	w.got.Write(p[:n])
	return n, step.err
}

// output-guard.ts:20-45: ENOBUFS, EAGAIN and EWOULDBLOCK are retried after 10 ms without limit; nothing is lost or repeated.
func TestRawStdoutRetriesTransientErrorsAfterTenMilliseconds(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOBUFS, syscall.EAGAIN, syscall.EWOULDBLOCK} {
		w := &scriptedWriter{steps: []writeStep{{0, errno}, {0, &os.PathError{Op: "write", Path: "/dev/stdout", Err: errno}}, {-1, nil}}}
		var waits []time.Duration
		n, err := NewRetryingWriter(w, func(d time.Duration) { waits = append(waits, d) }).Write([]byte("chunk"))
		if err != nil || n != 5 || w.got.String() != "chunk" {
			t.Fatalf("%v: n=%d err=%v wrote %q", errno, n, err, w.got.String())
		}
		if !slices.Equal(waits, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}) {
			t.Fatalf("%v: waits = %v, want two 10ms pauses", errno, waits)
		}
	}
}

// A call that accepted part of the chunk before EAGAIN resumes with the rest: no byte is lost or written twice.
func TestRawStdoutRetryResumesAfterAPartialWrite(t *testing.T) {
	w := &scriptedWriter{steps: []writeStep{{3, syscall.EAGAIN}, {2, syscall.ENOBUFS}, {-1, nil}}}
	n, err := NewRetryingWriter(w, func(time.Duration) {}).Write([]byte("abcdefgh"))
	if err != nil || n != 8 || w.got.String() != "abcdefgh" {
		t.Fatalf("n=%d err=%v wrote %q", n, err, w.got.String())
	}
}

// Any other error is not retried and reaches the caller (the queue's exit-1 hook).
func TestRawStdoutDoesNotRetryOtherErrors(t *testing.T) {
	boom := syscall.EPIPE
	w := &scriptedWriter{steps: []writeStep{{0, boom}}}
	waited := false
	_, err := NewRetryingWriter(w, func(time.Duration) { waited = true }).Write([]byte("x"))
	if !errors.Is(err, boom) || waited || w.calls != 1 {
		t.Fatalf("err=%v waited=%v calls=%d, want one attempt that returns EPIPE", err, waited, w.calls)
	}
}

// The pause really is the wait hook's: while it blocks the chunk is not written, and the retry follows its return.
func TestRawStdoutRetryHoldsTheChunkUntilThePauseEnds(t *testing.T) {
	w := &scriptedWriter{steps: []writeStep{{0, syscall.EAGAIN}, {-1, nil}}}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = NewRetryingWriter(w, func(time.Duration) { close(entered); <-release }).Write([]byte("late"))
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatal("the write returned without pausing for the retry")
	}
	if w.calls != 1 {
		t.Fatalf("calls during the pause = %d, want 1", w.calls)
	}
	close(release)
	<-done
	if w.got.String() != "late" || w.calls != 2 {
		t.Fatalf("wrote %q in %d calls", w.got.String(), w.calls)
	}
}

// RawStdoutWriter reaches the real stdout file: two chunks larger than the pipe buffer written while the reader is stalled arrive complete and in order.
func TestRawStdoutWriterKeepsOrderAcrossAStalledReader(t *testing.T) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })
	first := strings.Repeat("A", 1<<20)
	second := strings.Repeat("B", 1<<20)
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = WriteRawStdout(first)
		_, _ = RawStdoutWriter().Write([]byte(second))
		_ = w.Close()
	}()
	// The first chunk exceeds the pipe buffer, so its write cannot finish before the reader below starts.
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	<-written
	if string(got) != first+second {
		t.Fatalf("read %d bytes, want %d in order", len(got), len(first)+len(second))
	}
}
