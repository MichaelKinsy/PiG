package codingagent

import (
	"github.com/MichaelKinsy/PiG/internal/installchange"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
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
func (m *InteractiveMode) resumeHintCommand() string {
	session := m.currentSession()
	if session == nil {
		return ""
	}
	resolvedDir, _ := nodepath.Resolve(m.opts.SessionDir)
	defaultDir, _ := nodepath.Resolve(defaultSessionDir(session.CWD()))
	return formatResumeCommand(resumeCommandSession{
		persisted:             session.Path() != "",
		sessionFile:           session.Path(),
		sessionID:             session.ID(),
		sessionDir:            m.opts.SessionDir,
		usesDefaultSessionDir: m.opts.SessionDir == "" || resolvedDir == defaultDir,
	}, stdoutIsTTY())
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
