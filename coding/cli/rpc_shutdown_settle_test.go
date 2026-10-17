package cli

import (
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
)

// Pi starts shutdown() for stdin's end in an event-loop iteration after the line, so the microtasks of earlier commands and of a published agent_end finish first (rpc-mode.ts:355-360,726-742,802-805). Stdin end therefore settles before it removes the signal handlers, and a settle that reports the mode ended abandons the shutdown.
func TestRPCShutdownInputEndSettlesBeforeShutdownStarts(t *testing.T) {
	build := func(order *[]string, settle func() bool) *rpcShutdown {
		step := func(name string) func() { return func() { *order = append(*order, name) } }
		s := newRPCShutdown(step("remove"), step("detach"), step("dispose"), step("flush"), func(int) { *order = append(*order, "exit") }, func(os.Signal) {})
		s.settle = settle
		return s
	}
	var order []string
	build(&order, func() bool { order = append(order, "settle"); return true }).inputEnd()
	if want := []string{"settle", "remove", "detach", "dispose", "flush", "exit"}; !slices.Equal(order, want) {
		t.Fatalf("stdin end ran %v, want %v", order, want)
	}
	order = nil
	build(&order, func() bool { order = append(order, "settle"); return false }).inputEnd()
	if want := []string{"settle"}; !slices.Equal(order, want) {
		t.Fatalf("an abandoned settle ran %v, want %v", order, want)
	}
}

// Node's default action for a termination signal ends the process before any exit can run. Pig stops the extension processes first, which completes a pending dispose and reports a drained event loop, so neither may exit 0 while the signal is on its way: the process ends by the signal (rpc-mode.ts:728-744 removes the handlers, and the default action follows). Before the fix the dispose that the kill completed exited 0 (macOS cli-7 TestRPCInputEndAfterCommandReplacementKeepsPendingQuitShutdownQuiet: wait status 0, want SIGTERM).
func TestRPCShutdownEndsByTheSignalNotByExit(t *testing.T) {
	var mu sync.Mutex
	var exits []int
	var flushes int
	release := make(chan struct{})
	disposing := make(chan struct{})
	inputEnded := make(chan struct{})
	var s *rpcShutdown
	s = newRPCShutdown(func() {}, func() {}, func() { close(disposing); <-release }, func() { mu.Lock(); flushes++; mu.Unlock() }, func(code int) { mu.Lock(); exits = append(exits, code); mu.Unlock() }, func(os.Signal) {
		// The extension processes die before the signal does: the pending dispose returns and their closed connections report the drain.
		close(release)
		s.drained()
		<-inputEnded
	})
	go func() { s.inputEnd(); close(inputEnded) }()
	<-disposing
	s.signal(syscall.SIGTERM)
	<-inputEnded
	mu.Lock()
	defer mu.Unlock()
	if len(exits) != 0 || flushes != 0 {
		t.Fatalf("exits = %v, flushes = %d after the termination began; want none", exits, flushes)
	}
	// Without a termination in flight the drain exits 0.
	s.dying.Store(false)
	mu.Unlock()
	s.drained()
	mu.Lock()
	if !slices.Equal(exits, []int{0}) {
		t.Fatalf("a drain exited %v, want [0]", exits)
	}
}

// A termination signal while shutdown() awaits flushRawStdout takes its default action, because shutdown() removed the handlers first (rpc-mode.ts:732-742): the process ends by the signal, not by the process.exit(0) that follows the flush.
func TestRPCShutdownSignalDuringTheFlushEndsByTheSignal(t *testing.T) {
	var mu sync.Mutex
	var exits []int
	flushing := make(chan struct{})
	release := make(chan struct{})
	inputEnded := make(chan struct{})
	var died []os.Signal
	s := newRPCShutdown(func() {}, func() {}, func() {}, func() { close(flushing); <-release }, func(code int) { mu.Lock(); exits = append(exits, code); mu.Unlock() }, func(sig os.Signal) {
		mu.Lock()
		died = append(died, sig)
		mu.Unlock()
		// Stopping the extension processes lets the flush finish before the signal ends the process.
		close(release)
		<-inputEnded
	})
	go func() { s.inputEnd(); close(inputEnded) }()
	<-flushing
	s.forceDie(syscall.SIGTERM)
	mu.Lock()
	defer mu.Unlock()
	if len(exits) != 0 || !slices.Equal(died, []os.Signal{syscall.SIGTERM}) {
		t.Fatalf("exits = %v, died = %v after a signal during the flush; want no exit and [SIGTERM]", exits, died)
	}
}
