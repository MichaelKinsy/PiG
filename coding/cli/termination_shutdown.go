package cli

import (
	"os"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

var (
	terminationShutdownMu   sync.Mutex
	terminationShutdownFunc func()
)

// setTerminationShutdownHook registers the shutdown to run when a termination
// signal arrives, before the root context is cancelled. Passing nil clears it.
//
// The root context owns the extension subprocesses, so cancelling it kills the
// processes that would otherwise receive session_shutdown. Emitting through
// this hook first is what lets extensions run cleanup on SIGTERM, matching
// upstream's shutdown({fromSignal: true}), which disposes the runtime before
// touching anything else.
func setTerminationShutdownHook(fn func()) {
	terminationShutdownMu.Lock()
	defer terminationShutdownMu.Unlock()
	terminationShutdownFunc = fn
}

// runTerminationShutdownHook runs the registered hook, if any, to completion.
// Upstream's signal shutdown awaits runtimeHost.dispose() with no deadline.
func runTerminationShutdownHook() {
	terminationShutdownMu.Lock()
	fn := terminationShutdownFunc
	terminationShutdownMu.Unlock()
	if fn != nil {
		fn()
	}
}

var (
	terminationExitRecordOnce sync.Once
	terminationPreludeFunc    func()
	terminationRecordFunc     func(os.Signal) bool
)

// setTerminationRecord registers a mode's decision whether the first termination signal ends the process and so owns the exit.log line (D102). RPC mode decides that a signal during a shutdown that another trigger started does not: that shutdown ends the process, and its exit writes the line that says so. Passing nil clears it; with none registered, the first signal is recorded.
func setTerminationRecord(fn func(os.Signal) bool) {
	terminationShutdownMu.Lock()
	defer terminationShutdownMu.Unlock()
	terminationRecordFunc = fn
}

// setTerminationPrelude registers the work a mode needs done the moment a termination signal arrives, before either
// signal handler kills tracked children or disposes anything. Passing nil clears it.
func setTerminationPrelude(fn func()) {
	terminationShutdownMu.Lock()
	defer terminationShutdownMu.Unlock()
	terminationPreludeFunc = fn
}

// noteTerminationSignal runs the termination prelude and records the exit once, then marks the process as ending by
// sig. Main's handler and print mode's handler both receive the signal, and print mode's handler exits the process
// itself, so each calls this before any other work: whichever runs first, the other waits here until the prelude and
// the record are done.
func noteTerminationSignal(sig os.Signal) (syscall.Signal, bool) {
	terminationExitRecordOnce.Do(func() {
		terminationShutdownMu.Lock()
		prelude := terminationPreludeFunc
		decide := terminationRecordFunc
		terminationShutdownMu.Unlock()
		if prelude != nil {
			prelude()
		}
		if decide != nil && !decide(sig) {
			return
		}
		// pig additive (D102): Pi exits on SIGTERM and SIGHUP without a trace. RecordEnding claims the process's one
		// exit.log line for the first cause observed, and a later ending adds none (internal/codingagent/exit_ending.go).
		codingagent.RecordEnding(codingagent.AgentDir(), "received "+codingagent.SignalName(sig))
	})
	sysSig, ok := sig.(syscall.Signal)
	if ok {
		receivedTerminationSignal.Store(int32(sysSig))
	}
	return sysSig, ok
}
