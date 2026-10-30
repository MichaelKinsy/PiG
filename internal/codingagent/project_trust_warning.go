package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts:renderProjectTrustWarningIfNeeded.
func (m *InteractiveMode) renderProjectTrustWarningIfNeeded() {
	if m.projectTrusted() {
		return
	}
	// pig additive (D211): a run that withholds an untrusted project's context files also warns when the context files are the only project input.
	var withheld []string
	if m.opts.ContextFilesTrustedOnly {
		withheld = WithheldProjectContextFiles(m.opts.CWD, m.opts.AgentDir)
	}
	needsTrust := HasTrustRequiringProjectResources(m.opts.CWD)
	if !needsTrust && len(withheld) == 0 {
		return
	}
	if !m.chatContainer.IsEmpty() {
		m.chatContainer.Add(tui.NewSpacer(1))
	}
	var message string
	if needsTrust {
		// pig divergence (D2): the warning names PiG's configuration directory and restart command.
		message = "This project is not trusted. Project " + ConfigDirName() + " resources and packages are ignored. Use /trust to save a trust decision, then restart pig."
	}
	if len(withheld) > 0 {
		message = strings.TrimSpace(message + " " + ContextFilesWithheldMessage(withheld))
	}
	m.chatContainer.Add(themedNotice("warning", message, 1))
}

// ContextFilesWithheldMessage says which project context files an untrusted project did not load, and how to load them.
// pig additive (D211).
func ContextFilesWithheldMessage(paths []string) string {
	return "This project is not trusted, so its context files were not loaded: " + strings.Join(paths, ", ") + ". Use /trust to save a trust decision, then restart pig."
}

// maybeSaveImplicitProjectTrustAfterReload remembers the trust of a project that gained trust-requiring resources after a start without them.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:maybeSaveImplicitProjectTrustAfterReload
func (m *InteractiveMode) maybeSaveImplicitProjectTrustAfterReload() bool {
	cwd := m.opts.CWD
	if session := m.currentSession(); session != nil {
		cwd = session.GetCwd()
	}
	if m.autoTrustOnReloadCwd == "" || m.autoTrustOnReloadCwd != cwd {
		return false
	}
	if !m.projectTrusted() || !HasTrustRequiringProjectResources(cwd) {
		return false
	}
	store := NewProjectTrustStore(m.opts.AgentDir)
	decision, err := store.Get(cwd)
	if err == nil && decision != nil {
		m.autoTrustOnReloadCwd = ""
		return false
	}
	if err == nil {
		err = store.Set(cwd, new(true))
	}
	if err != nil {
		m.showWarning("Could not save project trust after reload: " + err.Error())
		return false
	}
	m.autoTrustOnReloadCwd = ""
	return true
}
