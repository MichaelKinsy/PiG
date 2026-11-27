package codingagent

import (
	"github.com/MichaelKinsy/PiG/internal/installchange"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/tui"
	"os"
)

// installChangeCause states what changed. A replaced or removed executable uses Pi's sentence for a removed install
// (interactive-mode.ts maybeShowInstallChangeWarning, kind "removed"): PiG reads no version from the replaced file, so
// it cannot use Pi's "was updated to <version>" sentence.
// pig additive (D95): a pruned extension cell or runtime file has no Pi counterpart and names the extension files.
func installChangeCause(change *installchange.Change) string {
	if change.Kind == installchange.FilesPruned {
		return "The " + AppName + " extension files this session runs from were removed or replaced"
	}
	return "The " + AppName + " installation this session runs from was removed or replaced"
}

// resumeHintCommand returns the command that resumes the current session, or "" when the session does not persist or stdout is not a terminal.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts formatResumeCommand(this.sessionManager).
func (m *InteractiveMode) resumeCommandSessionState() (resumeCommandSession, string, bool) {
	session := m.currentSession()
	if session == nil {
		return resumeCommandSession{}, "", false
	}
	resolvedDir, _ := nodepath.Resolve(m.opts.SessionDir)
	defaultDir, _ := nodepath.Resolve(defaultSessionDir(session.CWD()))
	return resumeCommandSession{
		programName:           m.opts.ResumeProgramName,
		persisted:             session.Path() != "",
		sessionFile:           session.Path(),
		sessionID:             session.ID(),
		sessionDir:            m.opts.SessionDir,
		usesDefaultSessionDir: m.opts.SessionDir == "" || resolvedDir == defaultDir,
	}, session.CWD(), true
}

func (m *InteractiveMode) resumeHintCommand() string {
	state, _, ok := m.resumeCommandSessionState()
	if !ok {
		return ""
	}
	return formatResumeCommand(state, stdoutIsTTY())
}

// programResumeCommand returns the OSC 7577 resume command for the active session, or nil while the session has no file.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts getProgramResumeCommand (Pi cf419553da).
func (m *InteractiveMode) programResumeCommand() *tui.ProgramResumeCommand {
	state, cwd, ok := m.resumeCommandSessionState()
	if !ok {
		return nil
	}
	return getProgramResumeCommand(state, cwd, m.opts.ProgramResumeFlags)
}

// getProgramResumeCommand returns the OSC 7577 resume command: the exit resume args plus the restriction and
// resource flags pi was started with, run in the session's directory. Returns nil when there is no session file,
// or when flags were typed in another directory than the session's.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts getProgramResumeCommand (Pi 145785f0d).
func getProgramResumeCommand(state resumeCommandSession, cwd string, resumeFlags []string) *tui.ProgramResumeCommand {
	argv := getResumeArgs(state)
	if len(argv) == 0 {
		return nil
	}
	if len(resumeFlags) > 0 {
		procCwd, err := os.Getwd()
		if err != nil || !pathsEqual(cwd, procCwd) {
			return nil
		}
		argv = append(argv, resumeFlags...)
	}
	return &tui.ProgramResumeCommand{Argv: argv, Cwd: cwd}
}

// maybeShowInstallChangeWarning checks, after an error, whether an update or a cache prune replaced or removed files this
// process runs from, and warns once. Code started on demand then fails until restart. It returns true once the install changed.
// The check runs only when an error is shown, never on a timer, and stats the executable and the cell files this process recorded.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts maybeShowInstallChangeWarning (#10439).
// pig additive (D95): the change is detected from the executable and cache files, not from a package.json.
func (m *InteractiveMode) maybeShowInstallChangeWarning() bool {
	if m.installChangeWarningShown {
		return true
	}
	change := m.installChanges.Detect()
	if change == nil {
		return false
	}
	m.installChangeWarningShown = true
	restart := "Restart " + AppName + "."
	if command := m.resumeHintCommand(); command != "" {
		restart = "Restart with `" + command + "` to continue this session."
	}
	m.showWarning(installChangeCause(change) + ". Features that load code on demand can fail until restart. " + restart)
	return true
}
