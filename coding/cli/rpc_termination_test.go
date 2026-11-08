package cli

import (
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type terminationHarness struct {
	mu      sync.Mutex
	steps   []string
	records []string
	done    chan struct{}
	exit    *rpcExit
	s       *rpcShutdown
	term    *rpcTermination
}

func (h *terminationHarness) step(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, name)
}

func (h *terminationHarness) snapshot() (steps, records []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.steps), slices.Clone(h.records)
}

func newTerminationHarness() *terminationHarness {
	h := &terminationHarness{done: make(chan struct{})}
	h.s = newRPCShutdown(func() { h.step("detach") }, func() { h.step("dispose") }, func() {}, func(code int) { h.step("exit") })
	h.exit = newRPCExit(func() {}, func() {}, func() { h.step("cleanup") }, func(int, bool) { h.step("record") }, func(int) { h.step("end") })
	h.term = &rpcTermination{
		shutdown: h.s, exit: h.exit, release: func() { h.step("release") }, done: h.done,
		received: func() syscall.Signal { return syscall.SIGTERM },
		recordEnding: func(reason string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.records = append(h.records, reason)
		},
	}
	return h
}

// A signal claims the shutdown when it is recorded, and only the termination hook runs it. The mode registers the hook before the record and clears it after, but a signal can still be recorded while the hook is gone, at the teardown; the mode's finish then disposes, once, so session_shutdown still runs.
func TestRPCFinishRunsADisposeASignalClaimedButNeverRan(t *testing.T) {
	h := newTerminationHarness()
	if !h.term.record(syscall.SIGTERM) {
		t.Fatal("the first signal did not claim the shutdown")
	}
	h.s.finish()
	h.s.finish()
	if steps, _ := h.snapshot(); !slices.Equal(steps, []string{"detach", "dispose"}) {
		t.Fatalf("steps = %v, want one detach and one dispose", steps)
	}
}

// The window at startup: a signal arrives after the hook is registered and before the record is. Nothing decided it, so the hook claims the shutdown and runs it.
func TestRPCSignalBeforeTheRecordIsRegisteredStillRunsTheShutdown(t *testing.T) {
	h := newTerminationHarness()
	h.term.hook()
	if steps, _ := h.snapshot(); !slices.Equal(steps, []string{"detach", "dispose", "exit"}) {
		t.Fatalf("steps = %v, want the shutdown the hook claimed", steps)
	}
}

// A single signal during a started shutdown is ignored, and the stdout write failure that ends the mode then ends the process with the signal's status and no exit. Its exit.log line names the ignored signal (D102).
func TestRPCIgnoredSignalIsRecordedWhenTheModeEndsTheProcess(t *testing.T) {
	h := newTerminationHarness()
	h.s.claim() // a stdin-end shutdown is running
	if h.term.record(syscall.SIGTERM) {
		t.Fatal("a signal during a started shutdown claimed it")
	}
	hooked := make(chan struct{})
	go func() { h.term.hook(); close(hooked) }()
	close(h.done)
	select {
	case <-hooked:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the hook did not return once the mode ended")
	}
	steps, records := h.snapshot()
	if !slices.Equal(steps, []string{"release"}) {
		t.Fatalf("steps = %v, want only the hold's release", steps)
	}
	if want := []string{"received SIGTERM during shutdown, which the mode's end cut off (exit 143)"}; !slices.Equal(records, want) {
		t.Fatalf("records = %v, want %v", records, want)
	}
	// The mode owns the end: a normal exit of the running shutdown returns and runs nothing, while a repeated signal still forces its own.
	h.exit.run(0, false, false)
	if steps, _ := h.snapshot(); slices.Contains(steps, "end") {
		t.Fatalf("steps = %v, want no exit after the mode ended the process", steps)
	}
	h.exit.run(143, false, true)
	if steps, _ := h.snapshot(); !slices.Equal(steps[1:], []string{"cleanup", "record", "end"}) {
		t.Fatalf("steps = %v, want the repeated signal's exit", steps)
	}
}

// When an exit already owns the end, the mode's end adds no line: that exit records itself.
func TestRPCIgnoredSignalIsNotRecordedByTheModeWhenAnExitOwnsTheEnd(t *testing.T) {
	h := newTerminationHarness()
	h.s.claim()
	h.term.record(syscall.SIGTERM)
	h.exit.run(0, false, false)
	close(h.done)
	h.term.hook()
	if _, records := h.snapshot(); len(records) != 0 {
		t.Fatalf("records = %v, want none from the hook", records)
	}
}

// The mode can return at the instant the first signal is recorded: finish then runs the dispose the signal claimed, and the signal's own run loses that race. Its exit must wait for the dispose that is running, or the process ends with the signal's status under the session_shutdown handlers. finish itself does not wait for a dispose that is running.
func TestRPCSignalWaitsForTheDisposeFinishStarted(t *testing.T) {
	h := newTerminationHarness()
	disposeStarted := make(chan struct{})
	releaseDispose := make(chan struct{})
	h.s = newRPCShutdown(func() { h.step("detach") }, func() {
		h.step("dispose:start")
		close(disposeStarted)
		<-releaseDispose
		h.step("dispose:end")
	}, func() {}, func(int) { h.step("exit") })
	h.term.shutdown = h.s
	if !h.term.record(syscall.SIGTERM) {
		t.Fatal("the first signal did not claim the shutdown")
	}
	finished := make(chan struct{})
	go func() { h.s.finish(); close(finished) }()
	<-disposeStarted
	// The hook runs now: the signal's run finds the dispose started by finish.
	hooked := make(chan struct{})
	go func() { h.term.hook(); close(hooked) }()
	select {
	case <-hooked:
		t.Fatal("the signal exited while the dispose that finish started was still running")
	case <-time.After(150 * time.Millisecond):
	}
	if steps, _ := h.snapshot(); slices.Contains(steps, "exit") {
		t.Fatalf("steps = %v, want no exit before the dispose ends", steps)
	}
	close(releaseDispose)
	for _, done := range []chan struct{}{finished, hooked} {
		select {
		case <-done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("a caller did not return after the dispose ended")
		}
	}
	if steps, _ := h.snapshot(); !slices.Equal(steps, []string{"detach", "dispose:start", "dispose:end", "exit"}) {
		t.Fatalf("steps = %v, want the exit after the dispose ended", steps)
	}
}
