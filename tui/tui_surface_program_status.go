package tui

import (
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// pig additive (D91): the frontend session is the terminal the TUI is built
// on, so Pi's program status (program-status-reporter.ts report, which calls
// the TUI terminal's setProgramStatus) goes to the session's ProgramStatus
// hook instead of stdout. As for Pi's terminals that are not a process
// (virtual-terminal.ts), there is no support query: every report is
// forwarded. Stop and Suspend clear a status that shows and Start and Resume
// report it again, as ProcessTerminal stop and start do (terminal.ts:467-476,
// :299).

// surfaceTerminal is the Terminal of a TuiSurface: the renderer's terminal,
// with the program status reported to the session.
type surfaceTerminal struct {
	Terminal

	// session is the session's status hook, nil when it has none.
	session frontend.ProgramStatusSession

	mu sync.Mutex
	// status is the latest status, kept while paused so resume reports it
	// again; nil after a clear.
	status *ProgramStatus
	// paused is whether the surface is stopped or suspended.
	paused bool
}

func newSurfaceTerminal(session frontend.Session) *surfaceTerminal {
	hook, _ := session.(frontend.ProgramStatusSession)
	return &surfaceTerminal{session: hook}
}

// Terminal returns the terminal the session draws on (tui.ts TuiBase.terminal):
// the renderer's terminal, whose SetProgramStatus reports to the session. It
// takes no lock, as a component such as the editor asks for it while it
// renders.
func (t *TuiSurface) Terminal() Terminal {
	return t.statusTerminal
}

// SetProgramStatus implements [Terminal]: it records status and reports it
// to the session unless the surface is stopped or suspended.
func (t *surfaceTerminal) SetProgramStatus(status ProgramStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if status.State == ProgramStateClear {
		t.status = nil
	} else {
		t.status = &status
	}
	if !t.paused {
		t.report(status)
	}
}

// pause clears a status that shows before the session stops or lends the
// terminal; reports wait for resume.
func (t *surfaceTerminal) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.paused && t.status != nil {
		t.report(ProgramStatus{State: ProgramStateClear})
	}
	t.paused = true
}

// resume reports the latest status again once the session draws again.
func (t *surfaceTerminal) resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paused && t.status != nil {
		t.report(*t.status)
	}
	t.paused = false
}

func (t *surfaceTerminal) report(status ProgramStatus) {
	if t.session != nil {
		t.session.ProgramStatus(FormatProgramStatus(status))
	}
}
