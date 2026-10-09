package codingagent

import (
	"context"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

// Pi accepts text while startup completes. From mounting the UI it binds handleStartupSubmit (interactive-mode.ts:944-947), and setupEditorSubmitHandler replaces it only after theme detection, the startup header and managed-tool setup (interactive-mode.ts:1017-1028). beginStartupSubmitWindow and endStartupSubmitWindow bound the same window; input read inside it reaches the editor, and Enter keeps the text.
func (m *InteractiveMode) beginStartupSubmitWindow() { m.editor.OnSubmit = m.handleStartupSubmit }

func (m *InteractiveMode) endStartupSubmitWindow() { m.setupEditorSubmitHandler(m.runCtx) }

// handleStartupSubmit mirrors upstream handleStartupSubmit (interactive-mode.ts:3072-3075).
func (m *InteractiveMode) handleStartupSubmit(text string) {
	m.editor.SetText(text)
	m.showStatus("Startup is still in progress")
}

// handleStartupInput gives one early input sequence to the editor, keeping frontend protocol input and terminal theme replies out of it.
func (m *InteractiveMode) handleStartupInput(input inputChunk, render bool) {
	defer input.ticket.settle()
	// pig additive (D91): a frontend's answers to its Open queries arrive
	// during startup.
	if m.frontendInput(string(input.data)) || m.consumeTerminalThemeInput(string(input.data)) {
		return
	}
	m.editor.HandleInput(string(input.data))
	if render && m.tuiInst != nil {
		m.tuiInst.Render()
	}
}

// hiddenSubmitCommand names the commands the submit handler recognizes by exact text and that are not in the slash registry (interactive-mode.ts
// setupEditorSubmitHandler: /arminsayshi and /dementedelves), or returns "".
func hiddenSubmitCommand(text string) string {
	switch text {
	// pig divergence (D87): /pigsayhi is PiG's name for the same easter egg; Pi sends it to the model.
	case "/arminsayshi", "/pigsayhi":
		return "arminsayshi"
	case "/dementedelves":
		return "dementedelves"
	}
	return ""
}

var flushPendingBashComponents = (*InteractiveMode).flushPendingBashBlocks

// setupEditorSubmitHandler installs ordinary submission after managed-tool setup and trims ECMAScript whitespace. Idle prompts wait for the main prompt loop; commands and inputs for an active operation retain their immediate dispatch.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:setupEditorSubmitHandler
func (m *InteractiveMode) setupEditorSubmitHandler(ctx context.Context) {
	m.editor.OnSubmit = func(text string) {
		text = widthx.JSTrim(text)
		if text == "" {
			return
		}
		// Pi recognizes the hidden commands only here, before history, compaction queueing and prompt dispatch.
		switch hiddenSubmitCommand(text) {
		case "arminsayshi":
			m.handleArminSaysHi()
			m.editor.SetText("")
			return
		case "dementedelves":
			m.handleDementedDelves()
			m.editor.SetText("")
			return
		}
		if m.slashRegistry != nil && m.resolvableSlashCommand(text) && !m.isExtensionCommand(text) {
			m.handleSubmit(ctx, text)
			return
		}
		if isUserBashCommand(text) {
			if m.bashCancel == nil {
				m.editor.AddToHistory(text)
			}
			m.handleSubmit(ctx, text)
			return
		}
		if m.isCompacting || m.runStreaming() {
			m.editor.AddToHistory(text)
			m.handleSubmit(ctx, text)
			return
		}
		flushPendingBashComponents(m)
		if m.onInputCallback != nil {
			m.onInputCallback(text)
		} else {
			m.pendingUserInputs = append(m.pendingUserInputs, text)
		}
		m.editor.AddToHistory(text)
	}
}

// getUserInput resolves one queued prompt or installs a one-shot callback. The owner loop selects the channel without blocking editor input while the prompt is pending.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:getUserInput
func (m *InteractiveMode) getUserInput() <-chan string {
	result := make(chan string, 1)
	if len(m.pendingUserInputs) > 0 {
		text := m.pendingUserInputs[0]
		m.pendingUserInputs[0] = ""
		m.pendingUserInputs = m.pendingUserInputs[1:]
		result <- text
	} else {
		m.onInputCallback = func(text string) {
			m.onInputCallback = nil
			result <- text
		}
	}
	return result
}
