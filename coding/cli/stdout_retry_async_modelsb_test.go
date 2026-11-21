package cli

// pi: packages/coding-agent/src/core/output-guard.ts

import (
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A reader that closed its end fails the queued write with EPIPE: the retry writer does not loop on it, the queue reports the failure once (the exit-1 path of print, json and rpc mode) and the backpressure wait ends.
func TestStdoutQueueOverRawWriterReportsAClosedReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	t.Cleanup(func() { _ = w.Close() })
	var reports atomic.Int32
	var errs atomic.Value
	q := newStdoutQueue(codingagent.NewRetryingWriter(w, func(time.Duration) { t.Error("EPIPE was retried") }), func(err error) {
		reports.Add(1)
		errs.Store(err)
	})
	defer q.Close()
	_, _ = q.Write([]byte("frame\n"))
	if !q.Wait(t.Context()) {
		t.Fatal("backpressure wait did not end after the failed write")
	}
	_, _ = q.Write([]byte("later\n"))
	q.Wait(t.Context())
	if reports.Load() != 1 {
		t.Fatalf("failure reported %d times, want once", reports.Load())
	}
	if err, _ := errs.Load().(error); err == nil || !isClosedPipeWriteError(err) {
		t.Fatalf("reported %v, want closed-pipe write error", err)
	}
}

type eagainOnce struct {
	calls atomic.Int32
	out   chan string
}

func (e *eagainOnce) Write(p []byte) (int, error) {
	if e.calls.Add(1) == 1 {
		return 0, syscall.EAGAIN
	}
	e.out <- string(p)
	return len(p), nil
}

// The queue's backpressure wait holds across the 10 ms retry pause: it returns only after the chunk behind the EAGAIN is written, and the chunk is not reported as a failure.
func TestStdoutQueueWaitCoversTheRetryPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := &eagainOnce{out: make(chan string, 4)}
		entered, release := make(chan struct{}), make(chan struct{})
		var failed atomic.Bool
		failedCh := make(chan struct{})
		q := newStdoutQueue(codingagent.NewRetryingWriter(e, func(time.Duration) { close(entered); <-release }), func(error) { failed.Store(true); close(failedCh) })
		defer q.Close()
		releaseOnce := sync.OnceFunc(func() { close(release) })
		defer releaseOnce()
		_, _ = q.Write([]byte("one\n"))
		select {
		case <-entered:
		case <-failedCh:
			t.Fatal("the EAGAIN write was reported as a failure instead of being retried")
		}
		waited := make(chan bool, 1)
		go func() { waited <- q.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case <-waited:
			t.Fatal("backpressure wait returned while the write was in its retry pause")
		default:
		}
		releaseOnce()
		if !<-waited {
			t.Fatal("wait did not drain")
		}
		if got := <-e.out; got != "one\n" || failed.Load() {
			t.Fatalf("got %q failed=%v", got, failed.Load())
		}
	})
}
