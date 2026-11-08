package cli

import (
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi starts shutdown() for stdin's end in an event-loop iteration after the line, so the microtasks of earlier commands and of a published agent_end finish first (rpc-mode.ts:355-360,726-742,802-805). Stdin end therefore settles before it starts the shutdown, and a settle that reports the mode ended abandons the shutdown.
func TestRPCShutdownInputEndSettlesBeforeShutdownStarts(t *testing.T) {
	build := func(order *[]string, settle func() bool) *rpcShutdown {
		step := func(name string) func() { return func() { *order = append(*order, name) } }
		s := newRPCShutdown(step("detach"), step("dispose"), step("flush"), func(int) { *order = append(*order, "exit") })
		s.settle = settle
		return s
	}
	var order []string
	build(&order, func() bool { order = append(order, "settle"); return true }).inputEnd()
	if want := []string{"settle", "detach", "dispose", "flush", "exit"}; !slices.Equal(order, want) {
		t.Fatalf("stdin end ran %v, want %v", order, want)
	}
	order = nil
	build(&order, func() bool { order = append(order, "settle"); return false }).inputEnd()
	if want := []string{"settle"}; !slices.Equal(order, want) {
		t.Fatalf("an abandoned settle ran %v, want %v", order, want)
	}
}

// A repeated signal during a stdin-end dispose exits 128+SIGTERM at once (Pi main ace560c30). Pig's exit stops the extension processes before it ends the process, which completes the pending dispose, and the dispose's own exit (0) then runs beside the signal's. The first exit decides the status. The fake exit forces that interleaving: it completes the dispose and waits for the stdin end's shutdown to finish before it records its own status.
func TestRPCShutdownRepeatedSignalKeepsItsExitStatus(t *testing.T) {
	var mu sync.Mutex
	var exits []int
	release := make(chan struct{})
	disposing := make(chan struct{})
	inputEnded := make(chan struct{})
	s := newRPCShutdown(func() {}, func() { close(disposing); <-release }, func() {}, func(code int) {
		if code == 128+int(syscall.SIGTERM) {
			close(release)
			select {
			case <-inputEnded:
			case <-time.After(testbudget.Wait(t)):
				t.Error("the stdin end's shutdown did not finish after its dispose completed")
			}
		}
		mu.Lock()
		exits = append(exits, code)
		mu.Unlock()
	})
	go func() { s.inputEnd(); close(inputEnded) }()
	<-disposing
	s.repeatedSignal(syscall.SIGTERM)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(exits, []int{143}) {
		t.Fatalf("exits = %v, want only the repeated signal's [143]", exits)
	}
}

// Pi's repeated signal calls process.exit at once (rpc-mode.ts signalCount > 1). An exit that is already waiting for stdout does not hold it back: the repeated signal ends the process through its own exit, which does not wait for stdout either.
func TestRPCShutdownRepeatedSignalExitsWhileAnotherExitFlushes(t *testing.T) {
	flushing := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var exits, aborts []int
	s := newRPCShutdown(func() {}, func() {}, func() { close(flushing); <-release }, func(code int) {
		mu.Lock()
		exits = append(exits, code)
		mu.Unlock()
	})
	s.abort = func(code int) {
		mu.Lock()
		aborts = append(aborts, code)
		mu.Unlock()
	}
	done := make(chan struct{})
	go func() { s.inputEnd(); close(done) }()
	<-flushing
	s.repeatedSignal(syscall.SIGHUP)
	close(release)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(aborts, []int{129}) || len(exits) != 0 {
		t.Fatalf("aborts = %v, exits = %v, want the repeated signal's abort [129] and no exit after it", aborts, exits)
	}
}
