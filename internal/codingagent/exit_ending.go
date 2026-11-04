package codingagent

// exit_ending.go makes the process's ending leave exactly one exit.log line, whichever paths see it first.
//
// pig additive (D102): exit.log is PiG's own; Pi keeps no such log, so none of this changes what Pi does at the exit
// (exit code, terminal restore, output, timing).
//
// A hung-up terminal is seen by three paths at once: the kernel's SIGHUP (the signal goroutine in coding/cli), the input
// reader's EOF (exitIfDeadTerminal) and a terminal write that fails with EIO (exitOnDeadTerminalWrite). Pi has the same
// three events: its SIGHUP handler starts a graceful shutdown whose terminal writes meet EIO and take
// emergencyTerminalExit (interactive-mode.ts registerSignalHandlers), and a stdout or stderr error takes it at once.
// Pi's JavaScript runs them one after another on one thread, in the order libuv returns them, and the first to reach
// process.exit ends the process. Go runs them on three goroutines, each ending in os.Exit. Before this file each path
// appended its own line, and the exit of whichever came first killed the others mid-record: 30 hangups of a real pig in
// a tmux pane wrote three lines (17) or two lines without the signal's (13), never one.
//
// Async contract (the ordering Pi's single thread gave for free):
//
//  1. Claim. RecordEnding claims the record atomically; the first caller owns it, and its reason is the line's cause.
//     The cause is the first one observed: a pig that leads its session sees the kernel's SIGHUP first, a pig started
//     from a shell sees the dead terminal first, because the shell forwards the hangup later. No path waits for another
//     to name the cause, so the exit is as prompt as Pi's.
//  2. One emergency exit. emergencyTerminalExit runs once. Pi's is `never`, so a second caller never returns either: it
//     blocks behind the first, which ends the process with 129. Every caller was written for a call that does not
//     return (a crash on a dead terminal, Run's unwinding to exit 1).
//  3. The emergency exit outranks the other exits. A path about to call os.Exit (BeforeExit) blocks behind a running
//     emergency exit, so the status is Pi's 129, and otherwise waits for the line that is in flight, so os.Exit never
//     overtakes the record.
//  4. Flush before exit. The owner returns from RecordEnding only after the line is appended and closed, and a caller
//     that lost the claim blocks until then.

import "sync"

// processEnding is the process's single exit record. flushed closes once the owner's line is on disk.
type processEnding struct {
	mu        sync.Mutex
	taken     bool
	flushOnce sync.Once
	flushed   chan struct{}
	// hold, when non-nil, keeps the owner from writing until it is closed. Tests use it to hold the owner inside its claim. It is a
	// channel and not a function, so no dynamic call hides the write from the static analyzers (a replaceable write function made
	// gosec's taint analysis (G704) connect the install-telemetry request URL to it).
	hold <-chan struct{}
}

func newProcessEnding() *processEnding {
	return &processEnding{flushed: make(chan struct{})}
}

// ending is the process-wide record. Tests that exercise several endings in one process replace it.
var ending = newProcessEnding()

// flush marks the owner's line as on disk. It releases every waiter, once.
func (e *processEnding) flush() {
	e.flushOnce.Do(func() { close(e.flushed) })
}

// claim reports whether the caller owns the record. Only the first caller does.
func (e *processEnding) claim() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return false
	}
	e.taken = true
	return true
}

// claimed reports whether some path owns the record.
func (e *processEnding) claimed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.taken
}

// RecordEnding appends the one exit.log line for the ending that ends this process and reports whether reason is it. A
// caller that does not own the record blocks until the owner's line is on disk, so returning means the record is
// complete either way and the caller may exit.
func RecordEnding(agentDir, reason string) bool {
	e := ending
	if !e.claim() {
		<-e.flushed
		return false
	}
	defer e.flush()
	if e.hold != nil {
		<-e.hold
	}
	RecordExit(agentDir, reason)
	return true
}

// BeforeExit is called by a path that is about to end the process with os.Exit and did not claim the record through
// RecordEnding, or that lost the claim. It never returns while an emergency exit is running, because that exit ends the
// process with 129 as Pi's does (step 3 of the contract); otherwise it returns once the line another path claimed is on
// disk. With no ending in flight it returns at once.
func BeforeExit() {
	if emergencyExiting.Load() {
		select {}
	}
	if e := ending; e.claimed() {
		<-e.flushed
	}
}
