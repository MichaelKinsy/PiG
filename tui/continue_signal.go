package tui

// Ports the ownership rule of packages/tui/src/tui.ts: ownsTerminal and updateSuspendSignalListener (Pi e4f50d76, 3107a58d), applied to SIGCONT.
//
// pig divergence (D661): Pi sets a process.on("SIGTSTP") listener while the TUI owns the terminal, which runs the Ctrl+Z path (stop the TUI, then stop the
// process). A Go program that has caught SIGTSTP cannot return it to the default action (signal.Stop and signal.Reset leave the runtime's handler in
// place and the signal is dropped), so no SIGTSTP listener can follow the terminal's ownership here. The default stop stays the kernel's, and the one
// listener that follows ownership is for SIGCONT, whose default action (continue) is the same whether the runtime catches it or not: SetContinueSignalHandler.

// continueOwnership identifies one owner of the process terminal (a renderer from its start until the last step of its stop, or ProcessTerminal while raw mode
// is armed): false before the first start (Pi's ownsTerminal). Membership is tracked under continueSignal.mu so concurrent begin and end calls on one owner
// cannot leave a stale entry. A renderer whose Terminal is nil (writer-backed test renderers) or not a *ProcessTerminal (for example an extension's remote
// alt-screen bridge) does not own the process terminal and calls neither begin nor end.
// continueOwnership must have non-zero size: Go allows distinct zero-sized struct fields at the end of a struct to alias addresses,
// and continueSignal.owners keys owners by *continueOwnership pointer.
type continueOwnership struct {
	_ byte
}

// beginFor records ownership for a renderer on terminal when that terminal is a *ProcessTerminal.
// Writer-backed renderers (terminal == nil) and non-process terminals (such as an extension's remote bridge) never claim process-terminal SIGCONT ownership.
func (c *continueOwnership) beginFor(terminal Terminal) {
	if !ownsProcessTerminal(terminal) {
		return
	}
	c.begin()
}

// ownsProcessTerminal reports whether terminal is a *ProcessTerminal.
func ownsProcessTerminal(terminal Terminal) bool {
	_, ok := terminal.(*ProcessTerminal)
	return ok
}

// begin marks this owner as holding the process terminal. Starting again keeps the ownership.
func (c *continueOwnership) begin() {
	continueOwnersChanged(c, true)
}

// end releases this owner's hold on the process terminal, as the last step of stop or raw-mode restore.
func (c *continueOwnership) end() {
	continueOwnersChanged(c, false)
}
