package host

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Go-only (no Pi case): the test helper receive is `await promise` on the test's own goroutine. When the promise never resolves
// the test stops there with the guard's message alone. It does not go on with the zero value of a signal that never came:
// TestObjectRetriesMakeAGetRightAfterAFailureWaitForTheScheduledRetry did, dereferenced a nil *Reference after the guard, and
// failed with a nil pointer dereference that named the wrong thing.

// recordingTB is a testing.TB that records what a helper reports and stops the goroutine at Fatal, as testing.T does.
type recordingTB struct {
	testing.TB
	mu       sync.Mutex
	messages []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Error(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, fmt.Sprint(args...))
}

func (r *recordingTB) Fatal(args ...any) {
	r.Error(args...)
	runtime.Goexit()
}

func TestReceiveStopsTheTestAtTheGuardAndGoesOnWithNoZeroValue(t *testing.T) {
	recorder := &recordingTB{}
	never := make(chan *Reference)
	done := make(chan struct{})
	wentOn := false
	go func() {
		defer close(done)
		_ = receiveWithin(recorder, never, 10*time.Millisecond)
		wentOn = true
	}()
	receive(t, done)
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if wentOn || len(recorder.messages) != 1 || recorder.messages[0] != "the awaited signal never came" {
		t.Fatalf("went on %v, messages %q, want the test stopped with the guard's message only", wentOn, recorder.messages)
	}
}

func TestReceiveReturnsTheValueThatCame(t *testing.T) {
	recorder := &recordingTB{}
	came := make(chan int, 1)
	came <- 7
	if got := receiveWithin(recorder, came, time.Minute); got != 7 || len(recorder.messages) != 0 {
		t.Fatalf("got %d, messages %q, want 7 and none", got, recorder.messages)
	}
}
