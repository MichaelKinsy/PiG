package codingagent

// Ports packages/coding-agent/src/modes/interactive/program-status-reporter.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// BlockedStatus is what a dialog waits for: the kind of dialog and the line the terminal shows.
type BlockedStatus struct {
	Kind    tui.ProgramStatusKind
	Message string
}

// blockedDialog is one open dialog, keyed by its source.
type blockedDialog struct {
	source string
	status BlockedStatus
}

// firstLine is upstream's `text?.split(/\r?\n/, 1)[0]?.trim() || "Error"`.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.TrimSuffix(line, "\r")
	if line = widthx.JSTrim(line); line != "" {
		return line
	}
	return "Error"
}

// ProgramStatusReporter reports interactive-mode state to the terminal (OSC 7501): `working` during agent runs and
// compaction, `blocked` while a dialog waits for the user, then `done`, `error`, or `idle` once the run settles.
// Messages are limited to the session name, dialog titles, and the first line of errors; prompts and assistant output
// are never reported. It runs on the interactive owner loop.
type ProgramStatusReporter struct {
	getTerminal    func() tui.Terminal
	getSessionName func() string
	runActive      bool
	compacting     bool
	// runResult is the outcome of the current run, reported once it settles.
	runResult tui.ProgramStatus
	// restingStatus is the status while no run is active.
	restingStatus tui.ProgramStatus
	// blocked holds the open dialogs by source, in the order they opened. The most recent one is reported.
	blocked    []blockedDialog
	lastReport *tui.ProgramStatus
}

// NewProgramStatusReporter returns a reporter that writes to getTerminal's terminal and names runs after getSessionName.
func NewProgramStatusReporter(getTerminal func() tui.Terminal, getSessionName func() string) *ProgramStatusReporter {
	return &ProgramStatusReporter{
		getTerminal: getTerminal, getSessionName: getSessionName,
		runResult: tui.ProgramStatus{State: tui.ProgramStateDone}, restingStatus: tui.ProgramStatus{State: tui.ProgramStateIdle},
	}
}

// HandleEvent updates the status from a Session event.
func (r *ProgramStatusReporter) HandleEvent(event agent.AgentEvent) {
	switch event := event.(type) {
	case agent.AgentStartEvent:
		r.runActive = true
		r.runResult = tui.ProgramStatus{State: tui.ProgramStateDone}
	case agent.MessageEndEvent:
		// The latest response decides the outcome, so a retried error is replaced by its successful retry.
		if event.Message.Assistant == nil {
			return
		}
		if event.Message.Assistant.StopReason == ai.StopReasonError {
			r.runResult = tui.ProgramStatus{State: tui.ProgramStateError, Message: firstLine(event.Message.Assistant.ErrorMessage)}
		} else {
			r.runResult = tui.ProgramStatus{State: tui.ProgramStateDone}
		}
	case agent.CompactionStartEvent:
		r.compacting = true
	case agent.CompactionEndEvent:
		r.compacting = false
		switch {
		case r.runActive:
			// A failed recovery compaction ends the run unless a later response succeeds.
			if event.Aborted {
				r.runResult = tui.ProgramStatus{State: tui.ProgramStateIdle}
			} else if event.ErrorMessage != "" {
				r.runResult = tui.ProgramStatus{State: tui.ProgramStateError, Message: firstLine(event.ErrorMessage)}
			}
		case event.Aborted:
			r.restingStatus = tui.ProgramStatus{State: tui.ProgramStateIdle}
		case event.Reason == "manual":
			if event.ErrorMessage != "" {
				r.restingStatus = tui.ProgramStatus{State: tui.ProgramStateError, Message: firstLine(event.ErrorMessage)}
			} else {
				r.restingStatus = tui.ProgramStatus{State: tui.ProgramStateDone}
			}
		}
	case agent.AgentSettledEvent:
		r.runActive = false
		if event.Aborted {
			r.restingStatus = tui.ProgramStatus{State: tui.ProgramStateIdle}
		} else {
			r.restingStatus = r.runResult
		}
	case agent.SessionInfoChangedEvent:
		// The session name is part of working and done reports.
	default:
		return
	}
	r.Report()
}

// SetBlocked reports `blocked` for a dialog until it is cleared with a nil status. Reopening a source replaces it.
func (r *ProgramStatusReporter) SetBlocked(source string, status *BlockedStatus) {
	r.blocked = withoutDialog(r.blocked, source)
	if status != nil {
		r.blocked = append(r.blocked, blockedDialog{source: source, status: *status})
	}
	r.Report()
}

// withoutDialog is `blocked.delete(source)`: the later set appends, so a reopened source moves to the end.
func withoutDialog(dialogs []blockedDialog, source string) []blockedDialog {
	kept := make([]blockedDialog, 0, len(dialogs))
	for _, dialog := range dialogs {
		if dialog.source != source {
			kept = append(kept, dialog)
		}
	}
	return kept
}

// Reset forgets the previous session's run, for example after switching sessions.
func (r *ProgramStatusReporter) Reset() {
	r.runActive = false
	r.compacting = false
	r.runResult = tui.ProgramStatus{State: tui.ProgramStateDone}
	r.restingStatus = tui.ProgramStatus{State: tui.ProgramStateIdle}
	r.Report()
}

// Report sends the current status unless it is the one last sent.
func (r *ProgramStatusReporter) Report() {
	if r == nil {
		return
	}
	status := r.currentStatus()
	status.App = AppName
	if r.lastReport != nil && *r.lastReport == status {
		return
	}
	terminal := r.getTerminal()
	if terminal == nil {
		return
	}
	r.lastReport = &status
	terminal.SetProgramStatus(status)
}

// pig additive (D91): PiG can replace the terminal mid-run, when a frontend session stops drawing; Pi's renderer keeps one terminal.

// Resend sends the current status to the terminal even if it is the one last sent, for a terminal that replaced the one it was sent to.
func (r *ProgramStatusReporter) Resend() {
	if r == nil {
		return
	}
	r.lastReport = nil
	r.Report()
}

func (r *ProgramStatusReporter) currentStatus() tui.ProgramStatus {
	if len(r.blocked) > 0 {
		last := r.blocked[len(r.blocked)-1].status
		return tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: last.Kind, Message: last.Message}
	}
	if r.compacting {
		return tui.ProgramStatus{State: tui.ProgramStateWorking, Message: "Compacting context"}
	}
	status := r.restingStatus
	if r.runActive {
		status = tui.ProgramStatus{State: tui.ProgramStateWorking}
	}
	if status.State == tui.ProgramStateWorking || status.State == tui.ProgramStateDone {
		status.Message = r.getSessionName()
	}
	return status
}

// programStatusReporter is the mode's reporter, created on first use. It runs on the owner loop.
// upstream: interactive-mode.ts `programStatus`, a field initialised with the terminal and session name getters.
func (m *InteractiveMode) programStatusReporter() *ProgramStatusReporter {
	if m.programStatus == nil {
		m.programStatus = NewProgramStatusReporter(
			func() tui.Terminal {
				if m.tuiInst == nil {
					return nil
				}
				return m.tuiInst.Terminal()
			},
			func() string {
				if session := m.currentSession(); session != nil {
					return session.GetSessionName()
				}
				return ""
			},
		)
	}
	return m.programStatus
}

// extensionDialogStatusSource is the program status source of extension dialogs, which share the editor slot.
const extensionDialogStatusSource = "extension-dialog"
