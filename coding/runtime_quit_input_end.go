package coding

import "sync"

// EmitQuitShutdownAfterInputEnd is EmitQuitShutdown for a mode whose input ended before its dispose. Pi's interactive quit stops the TUI and then awaits runtimeHost.dispose() (interactive-mode.ts shutdown): stop() takes stdin out of the event loop, so while the dispose awaits the aborted run (agent-session-runtime.ts dispose awaits session.abort() first) or a session_shutdown handler, Pi's process exits 0 as soon as nothing else keeps the loop alive, and the dispose never returns: no session_shutdown follows, and nothing the mode prints after a dispose.
//
// pig additive (D19): the extensions run in processes of their own, so the mode tells their host that input ended (ExtensionHost.EndInput) and the runtimes report the drained loop (the handler of a drained loop: every Node process reported its own, and no other runtime has a command in flight; a quit yield, Pi's RPC exit at the stdout flush, is not among its reports). drained is the mode's exit. It runs at most once, for a report that arrives before the dispose returned, and it must end the process; the dispose then does not return here either. A report after the dispose returned does nothing: the mode is on its way to its own exit. A runtime that cannot report an idle loop (Go, Python, Rust, Lisp) reports nothing, and the dispose waits for its author, as Pi's waits for an author whose loop is alive.
func (rt *Runtime) EmitQuitShutdownAfterInputEnd(drained func()) {
	if rt.emitQuitShutdownAfterInputEnd(drained) {
		select {}
	}
}

// emitQuitShutdownAfterInputEnd is EmitQuitShutdownAfterInputEnd; it reports whether the exit started, in which case the caller must not go on.
func (rt *Runtime) emitQuitShutdownAfterInputEnd(drained func()) bool {
	session := rt.Session()
	if session == nil || session.ExtensionHost() == nil {
		rt.settleBeforeQuitDispose()
		rt.EmitQuitShutdown()
		return false
	}
	dispose := &inputEndedDispose{drained: drained}
	host := session.ExtensionHost()
	host.SetRuntimeLoopDrainHandler(dispose.report)
	host.EndInput()
	rt.settleBeforeQuitDispose()
	rt.EmitQuitShutdown()
	return dispose.finish()
}

// inputEndedDispose decides between the two ends of a dispose whose input ended: it returns, or the extension runtimes report a drained loop first and the process exits.
type inputEndedDispose struct {
	mu       sync.Mutex
	returned bool
	exiting  bool
	drained  func()
}

// report is the extension host's drain handler.
func (d *inputEndedDispose) report() {
	d.mu.Lock()
	if d.returned || d.exiting {
		d.mu.Unlock()
		return
	}
	d.exiting = true
	d.mu.Unlock()
	d.drained()
}

// finish marks the dispose returned and reports whether the exit had started before: ending the extension processes inside the exit completes the pending dispose, whose caller would otherwise go on beside the exit.
func (d *inputEndedDispose) finish() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.exiting {
		return true
	}
	d.returned = true
	return false
}

// SetBeforeQuitDispose installs the mode's settle of its active run. EmitQuitShutdownAfterInputEnd runs it on its caller's goroutine once the extension runtimes heard that input ended and before the dispose awaits the Session's abort: an interactive run ends on the mode's owner loop (its event flush and its turn's cleanup), and that dispose runs on the same loop, so the mode aborts the run and serves its loop until the run has settled, agent_settled included, as Pi's loop turns under `await this.session.abort()`. Configure it before the mode runs.
func (rt *Runtime) SetBeforeQuitDispose(settle func()) {
	rt.replacement.quitSettle = settle
}

func (rt *Runtime) settleBeforeQuitDispose() {
	if settle := rt.replacement.quitSettle; settle != nil {
		settle()
	}
}
