package cli

import (
	"context"
	"errors"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type gatedWriter struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	written []string
	err     error
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	if w.entered != nil {
		w.once.Do(func() { close(w.entered) })
	}
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.written = append(w.written, string(p))
	return len(p), nil
}

// output-guard.ts writeRawStdout returns before the pipe accepts the chunk; a stopped reader must not block the Session listener that serialized it.
func TestStdoutQueueWriteDoesNotBlockOnStoppedReader(t *testing.T) {
	w := &gatedWriter{release: make(chan struct{})}
	q := newStdoutQueue(w, nil)
	defer q.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, line := range []string{"a\n", "b\n", "c\n"} {
			_, _ = q.Write([]byte(line))
		}
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Write blocked on a stopped stdout reader")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if q.Wait(ctx) {
		t.Fatal("backpressure wait reported drained while the reader is stopped")
	}
	close(w.release)
	if !q.Wait(t.Context()) {
		t.Fatal("backpressure wait did not drain")
	}
	if got := strings.Join(w.written, ""); got != "a\nb\nc\n" {
		t.Fatalf("written = %q, want the chunks in order", w.written)
	}
}

// The tail's catch exits once; later chunks are not written.
func TestStdoutQueueReportsWriteFailureOnce(t *testing.T) {
	w := &gatedWriter{release: make(chan struct{}), err: errors.New("EPIPE")}
	close(w.release)
	var reports int
	var mu sync.Mutex
	q := newStdoutQueue(w, func(error) { mu.Lock(); reports++; mu.Unlock() })
	defer q.Close()
	_, _ = q.Write([]byte("a\n"))
	q.Wait(t.Context())
	_, _ = q.Write([]byte("b\n"))
	q.Wait(t.Context())
	mu.Lock()
	defer mu.Unlock()
	if reports != 1 || len(w.written) != 0 {
		t.Fatalf("reports = %d written = %q", reports, w.written)
	}
}

var _ io.Writer = (*stdoutQueue)(nil)

// print-mode.ts:51-63 disposes and exits on SIGTERM without flushing raw stdout. A JSON run whose stdout reader stopped must still end on termination instead of holding the Session worker in a pipe write.
func TestJSONModeTerminationDoesNotWaitForStoppedStdoutReader(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	host := printModeTestHost(t, provider)
	stdout := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{})}
	defer close(stdout.release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- startPrintModeErr(ctx, host, printModeOptions{Mode: "json", InitialMessage: "probe", stdout: stdout, stderr: io.Discard})
	}()
	select {
	case <-stdout.entered:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("JSON mode never wrote to stdout")
	}
	previous := receivedTerminationSignal.Swap(int32(syscall.SIGTERM))
	defer receivedTerminationSignal.Store(previous)
	cancel()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("JSON mode did not return after termination while stdout was blocked")
	}
}

// countingWriter records each Write call and the goroutine that made it.
type countingWriter struct {
	mu         sync.Mutex
	writes     []string
	goroutines map[string]struct{}
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, string(p))
	if w.goroutines == nil {
		w.goroutines = map[string]struct{}{}
	}
	var stack [64]byte
	header, _, _ := strings.Cut(string(stack[:runtime.Stack(stack[:], false)]), " [")
	w.goroutines[header] = struct{}{}
	return len(p), nil
}

func (w *countingWriter) snapshot() (writes []string, goroutines int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.writes), len(w.goroutines)
}

// Every chunk pending when the writer takes its turn is one write of their concatenation; the byte stream is the chunks in order.
func TestStdoutQueueCoalescesPendingChunksIntoOneWrite(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{})}
	q := newStdoutQueue(gate, nil)
	defer q.Close()
	_, _ = q.Write([]byte("first\n"))
	<-gate.entered
	// The writer is inside its first write: these three are pending together.
	for _, line := range []string{"a\n", "b\n", "c\n"} {
		_, _ = q.Write([]byte(line))
	}
	close(gate.release)
	if !q.Wait(t.Context()) {
		t.Fatal("tail did not drain")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if want := []string{"first\n", "a\nb\nc\n"}; !slices.Equal(gate.written, want) {
		t.Fatalf("writes = %q, want %q", gate.written, want)
	}
}

// One writer goroutine serves every chunk: 2000 waited-for lines are written by one goroutine, Close ends it, and a chunk after Close starts another.
func TestStdoutQueueWriterIsOneGoroutineEndedByClose(t *testing.T) {
	w := &countingWriter{}
	q := newStdoutQueue(w, nil)
	_, _ = q.Write([]byte("x\n"))
	for range 2000 {
		_, _ = q.Write([]byte("y\n"))
		if !q.Wait(t.Context()) {
			t.Fatal("tail did not drain")
		}
	}
	if _, goroutines := w.snapshot(); goroutines != 1 {
		t.Fatalf("%d goroutines wrote 2001 lines, want one writer", goroutines)
	}
	q.Close()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		q.mu.Lock()
		running := q.running
		q.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer still running after Close")
		}
		time.Sleep(time.Millisecond)
	}
	_, _ = q.Write([]byte("z\n"))
	if !q.Wait(t.Context()) {
		t.Fatal("tail did not drain after Close")
	}
	q.Close()
	writes, goroutines := w.snapshot()
	if goroutines != 2 {
		t.Fatalf("%d writer goroutines across Close, want 2", goroutines)
	}
	if got, want := strings.Join(writes, ""), "x\n"+strings.Repeat("y\n", 2000)+"z\n"; got != want {
		t.Fatalf("byte stream differs: %d bytes, want %d", len(got), len(want))
	}
}

// Close with chunks pending writes them before the writer exits.
func TestStdoutQueueCloseWritesPendingChunks(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{})}
	q := newStdoutQueue(gate, nil)
	_, _ = q.Write([]byte("a\n"))
	<-gate.entered
	_, _ = q.Write([]byte("b\n"))
	q.Close()
	close(gate.release)
	if !q.Wait(t.Context()) {
		t.Fatal("tail did not drain")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if got := strings.Join(gate.written, ""); got != "a\nb\n" {
		t.Fatalf("written = %q", gate.written)
	}
}

// A chunk the writer has taken but the pipe has not accepted is still pending: the backpressure wait does not resolve while that write is in flight, even with nothing queued behind it.
func TestStdoutQueueWaitHoldsWhileAWriteIsInFlight(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{})}
	q := newStdoutQueue(gate, nil)
	defer q.Close()
	_, _ = q.Write([]byte("a\n"))
	<-gate.entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if q.Wait(ctx) {
		t.Fatal("backpressure wait resolved while the only chunk was still being written")
	}
	close(gate.release)
	if !q.Wait(t.Context()) {
		t.Fatal("tail did not drain")
	}
}

// Close does not wait for the writer: one blocked on a stopped reader neither holds Close nor outlives its write.
func TestStdoutQueueCloseDoesNotWaitForABlockedWriter(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{})}
	q := newStdoutQueue(gate, nil)
	_, _ = q.Write([]byte("a\n"))
	<-gate.entered
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		q.Close()
	}()
	select {
	case <-closed:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Close waited for a writer blocked on a stopped reader")
	}
	close(gate.release)
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		q.mu.Lock()
		running := q.running
		q.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("writer still running after its write returned")
		}
		time.Sleep(time.Millisecond)
	}
}

// A failure with chunks queued behind the failed write reports once, drops them, and ignores every later chunk; the wait resolves instead of holding for bytes that will never go out.
func TestStdoutQueueFailureDropsPendingChunksAndReportsOnce(t *testing.T) {
	gate := &gatedWriter{release: make(chan struct{}), entered: make(chan struct{}), err: errors.New("EPIPE")}
	var reports int
	var mu sync.Mutex
	q := newStdoutQueue(gate, func(error) { mu.Lock(); reports++; mu.Unlock() })
	defer q.Close()
	_, _ = q.Write([]byte("a\n"))
	<-gate.entered
	for _, line := range []string{"b\n", "c\n", "d\n"} {
		_, _ = q.Write([]byte(line))
	}
	close(gate.release)
	bounded, stop := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer stop()
	if !q.Wait(bounded) {
		t.Fatal("wait did not resolve after the failure")
	}
	if n, err := q.Write([]byte("e\n")); n != 2 || err != nil {
		t.Fatalf("Write after failure = %d, %v; want it accepted and dropped", n, err)
	}
	if !q.Wait(bounded) {
		t.Fatal("wait did not resolve after a chunk written once output had failed")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if reports != 1 || len(gate.written) != 0 {
		t.Fatalf("reports = %d, written = %q; want one report and nothing written", reports, gate.written)
	}
}
