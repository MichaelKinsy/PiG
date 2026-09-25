package main

import "sync"

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
