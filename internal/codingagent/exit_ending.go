package codingagent

// exit_ending.go makes the process's ending leave exactly one exit.log line, whichever paths see it first.
//
// pig additive (D102): exit.log is PiG's own; Pi keeps no such log, so none of this changes what Pi does at the exit
// (exit code, terminal restore, output).
//
// A hung-up terminal is seen by three paths at once: the kernel's SIGHUP (the signal goroutine in coding/cli), the input
// reader's EOF (exitIfDeadTerminal) and a terminal write that fails with EIO (exitOnDeadTerminalWrite). Pi has the same
// three events: its SIGHUP handler starts a graceful shutdown whose terminal writes meet EIO and take
// emergencyTerminalExit (interactive-mode.ts registerSignalHandlers). Pi's JavaScript runs them one after another on one
// thread; Go runs them on three goroutines, each ending in os.Exit. Before this file each path appended its own line,
// and the exit of whichever came first killed the others mid-record: 30 hangups of a real pig in a tmux pane wrote
// three lines (17) or two lines without the signal's (13), never one.
//
// Async contract (the ordering Pi's single thread gave for free):
//
//  1. Claim. RecordEnding claims the record atomically; the first caller owns it, and its reason is the line's cause.
//  2. SIGHUP outranks the dead terminal. The kernel posts SIGHUP before it wakes the reader, but the Go runtime hands
//     the signal to its goroutine later, so the EOF and the EIO write can arrive first. A dead-terminal exit therefore
//     waits for another path's claim (awaitEndingClaim) before claiming itself. The wait ends the moment the signal
//     goroutine claims, which is why it is an event wait and not a sleep. Its hangupGrace bound only keeps a terminal
//     that died without a signal (a session in the background process group, SIGHUP blocked from outside) from never
//     exiting: then the dead terminal is the cause.
//  3. One exit. emergencyTerminalExit runs once; a second caller returns, because the first is exiting.
//  4. Flush before exit. The owner returns from RecordEnding only after the line is appended and closed, and a caller that
//     lost the claim blocks until then, so os.Exit never overtakes the record, whichever path calls it.

import (
	"sync"
	"time"
)

// hangupGrace bounds how long a dead-terminal exit waits for the kernel's SIGHUP to reach the signal goroutine. The
// signal normally arrives within microseconds; this bound is a liveness backstop, not a delay the exit relies on.
const hangupGrace = 500 * time.Millisecond

// processEnding is the process's single exit record. claimed closes when a path owns the record, flushed when its line
// is on disk.
type processEnding struct {
	mu      sync.Mutex
	taken   bool
	claimed chan struct{}
	flushed chan struct{}
}

func newProcessEnding() *processEnding {
	return &processEnding{claimed: make(chan struct{}), flushed: make(chan struct{})}
}

// ending is the process-wide record. Tests that exercise several endings in one process replace it.
var ending = newProcessEnding()

// claim reports whether the caller owns the record. Only the first caller does.
func (e *processEnding) claim() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.taken {
		return false
	}
	e.taken = true
	close(e.claimed)
	return true
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
	defer close(e.flushed)
	RecordExit(agentDir, reason)
	return true
}

// awaitEndingClaim blocks until a path claims the record or hangupGrace passes. A dead-terminal exit calls it before
// claiming, so a SIGHUP that is on its way names the cause (step 2 of the contract).
func awaitEndingClaim() {
	e := ending
	timer := time.NewTimer(hangupGrace)
	defer timer.Stop()
	select {
	case <-e.claimed:
	case <-timer.C:
	}
}
