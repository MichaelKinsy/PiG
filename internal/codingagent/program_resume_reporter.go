// SPDX-License-Identifier: MIT

package codingagent

// Ports packages/coding-agent/src/modes/interactive/program-resume-reporter.ts (Pi PR #12557, commit cf419553da817fefd2f86f170ff3e93eff0bfd28).

import (
	"encoding/json"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// ProgramResumeReporter tells the terminal how to resume the current session after the terminal restarts (OSC 7577).
// Only created when the terminal supplied a token. The terminal holds one command per process; it is replaced when
// the command changes (new, resumed or forked session, first message written) and cleared when the session has no
// file to resume or the user quits. Ending the process through the terminal (SIGHUP, SIGTERM) keeps it, because
// that is when the terminal needs it. It runs on the interactive owner loop.
type ProgramResumeReporter struct {
	token            string
	getTerminal      func() tui.Terminal
	getResumeCommand func() *tui.ProgramResumeCommand
	now              func() time.Time
	pid              func() int
	// reported is the last command sent to the terminal as JSON, or "" when the terminal holds none.
	reported string
}

// NewProgramResumeReporter returns a reporter that authenticates OSC 7577 sequences with token, writes them to
// getTerminal's terminal, and reads the current command from getResumeCommand.
func NewProgramResumeReporter(token string, getTerminal func() tui.Terminal, getResumeCommand func() *tui.ProgramResumeCommand) *ProgramResumeReporter {
	return &ProgramResumeReporter{
		token:            token,
		getTerminal:      getTerminal,
		getResumeCommand: getResumeCommand,
		now:              time.Now,
		pid:              os.Getpid,
	}
}

// Report sends the current command if it changed since the last report.
func (r *ProgramResumeReporter) Report() {
	if r == nil {
		return
	}
	command := r.getResumeCommand()
	encoded := encodeProgramResumeKey(command)
	if encoded != r.reported {
		r.send(command, encoded)
	}
}

// ReportIfNone reports once the session gets a file to resume. No work while a command is reported:
// after that it only changes on a session switch, which calls Report.
func (r *ProgramResumeReporter) ReportIfNone() {
	if r == nil || r.reported != "" {
		return
	}
	r.Report()
}

// ReportAgain reports the current command even if unchanged. A program that ran while pig was suspended
// may have replaced pig's command in a terminal that keeps only the latest one.
func (r *ProgramResumeReporter) ReportAgain() {
	if r == nil {
		return
	}
	command := r.getResumeCommand()
	if command != nil || r.reported != "" {
		r.send(command, encodeProgramResumeKey(command))
	}
}

// Clear removes the command because the user quit.
func (r *ProgramResumeReporter) Clear() {
	if r == nil || r.reported == "" {
		return
	}
	r.send(nil, "")
}

func (r *ProgramResumeReporter) send(command *tui.ProgramResumeCommand, encoded string) {
	terminal := r.getTerminal()
	if terminal == nil {
		return
	}
	sender := tui.ProgramResumeSender{
		Token: r.token,
		Pid:   r.pid(),
		Time:  r.now().Unix(),
	}
	// A command the protocol cannot carry is cleared, so the terminal does not resume an older one in its place.
	var sequence string
	if command != nil {
		if seq, ok := tui.FormatProgramResume(*command, sender); ok {
			sequence = seq
		}
	}
	if sequence == "" {
		sequence = tui.FormatProgramResumeClear(sender)
	}
	r.reported = encoded
	terminal.Write(sequence)
}

func encodeProgramResumeKey(command *tui.ProgramResumeCommand) string {
	if command == nil {
		return ""
	}
	//portlint:allow jsonescape internal dedupe map key compared only against prior keys from this same encoder, never serialized to wire or disk
	b, err := json.Marshal(command)
	if err != nil {
		return ""
	}
	return string(b)
}
