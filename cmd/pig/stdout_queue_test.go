package main

import (
	"context"
	"errors"
	"io"
	"slices"
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, line := range []string{"a\n", "b\n", "c\n"} {
			_, _ = q.Write([]byte(line))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
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
	if !slices.Equal(w.written, []string{"a\n", "b\n", "c\n"}) {
		t.Fatalf("written = %q, want ordered chunks", w.written)
	}
}

// The tail's catch exits once; later chunks are not written.
func TestStdoutQueueReportsWriteFailureOnce(t *testing.T) {
	w := &gatedWriter{release: make(chan struct{}), err: errors.New("EPIPE")}
	close(w.release)
	var reports int
	var mu sync.Mutex
	q := newStdoutQueue(w, func(error) { mu.Lock(); reports++; mu.Unlock() })
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
		done <- runPrintMode(ctx, host, printModeOptions{Mode: "json", InitialMessage: "probe", stdout: stdout, stderr: io.Discard})
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
