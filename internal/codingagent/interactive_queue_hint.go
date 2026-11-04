package codingagent

import (
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// Terminal.app sends Option+Up as plain Up (ESC [ A) in its default profile, so the Alt+Up dequeue key of Pi's
// app.message.dequeue never reaches the editor there and plain Up recalls history while the message stays queued (issue #402).
// pig divergence (D228): the two decisions below, Up as dequeue in Terminal.app and the one-time copy tip elsewhere.

const (
	appleTerminalProgram = "Apple_Terminal"
	// dequeueOptionUpKey is the key id Pi binds app.message.dequeue to on every platform but Windows.
	dequeueOptionUpKey = "alt+up"
	// terminalAppMappingHint is the Terminal.app profile setting that makes Option+Up send CSI 1;3A, which pig decodes as alt+up.
	// It never mentions Option-as-Meta: that makes Option+Up send ESC then Up, and ESC aborts the turn.
	terminalAppMappingHint = `If this is Terminal.app, map Option+Up to \033[1;3A in Terminal Settings > Profiles > Keyboard.`
)

// termProgramIs reports whether TERM_PROGRAM names program.
func termProgramIs(program string) bool {
	return os.Getenv("TERM_PROGRAM") == program //perf:ok read when the user presses Up with a queued message and once per tip decision, not per frame; the environment is the terminal's answer and Pi's test sets it per case
}

// sendsModifiedArrows reports whether the terminal is known to send Option+Up as a modified arrow (CSI 1;3A), so its users need no mapping.
func sendsModifiedArrows() bool {
	switch os.Getenv("TERM_PROGRAM") { //perf:ok same cold decision as termProgramIs: evaluated once per queued-message hint
	case "ghostty", "iTerm.app", "WezTerm", "kitty":
		return true
	}
	return os.Getenv("TERM") == "xterm-kitty" //perf:ok same cold decision as termProgramIs
}

// dequeueKeys are the keys bound to app.message.dequeue, in the registry's text form ("alt+up").
func dequeueKeys() []string {
	return tui.GetTUIKeybindings().GetKeys(string(AppMessageDequeue))
}

// queuedMessageCount is how many messages the dequeue action would restore: the agent's queues plus the compaction queue, as restoreQueuedMessagesToEditor counts them.
func (m *InteractiveMode) queuedMessageCount() int {
	if m.agent == nil {
		return len(m.compactionQueue)
	}
	steering, followUps := m.agent.PendingMessages()
	steeringTexts, followUpTexts := collectQueuedTexts(steering, followUps, m.compactionQueue)
	return len(steeringTexts) + len(followUpTexts)
}

// builtInEditor reports whether keys reach the built-in editor. An extension's custom editor, whether it runs in the host or in an extension
// process, keeps Pi's behavior exactly: D228 changes neither its Up key nor its history recall.
// pig divergence (D228): both parts of the divergence are gated on this, the Terminal.app remap and the copy tip.
func (m *InteractiveMode) builtInEditor() bool {
	m.extComponentMu.Lock()
	factory := m.editorFactory
	m.extComponentMu.Unlock()
	return factory == nil && !m.editor.IsRemote()
}

// appleTerminalUpDequeues reports whether a plain Up key runs app.message.dequeue: Terminal.app delivers Option+Up as plain Up, the built-in
// editor is empty, a message is queued, and the action is bound to the key Option+Up stands for (a user who moved it elsewhere keeps Pi's Up).
func (m *InteractiveMode) appleTerminalUpDequeues() bool {
	if !termProgramIs(appleTerminalProgram) || m.editor == nil || !m.builtInEditor() || m.editor.Text() != "" {
		return false
	}
	return slices.Contains(dequeueKeys(), dequeueOptionUpKey) && m.queuedMessageCount() > 0
}

// dequeueHintKeyText is the key text of the queued-message hint: every key bound to app.message.dequeue as keyDisplayText shows it
// (interactive-mode.ts:4736 getAppKeyDisplay), except that in the Terminal.app state the Option+Up binding reads Up, the key that works there.
func (m *InteractiveMode) dequeueHintKeyText() string {
	keys := dequeueKeys()
	if len(keys) == 0 {
		return ""
	}
	// pig divergence (D228): in Terminal.app with an empty editor, Up is the key that dequeues, so the hint names it.
	if m.appleTerminalUpDequeues() {
		keys = slices.Clone(keys)
		for i, key := range keys {
			if key == dequeueOptionUpKey {
				keys[i] = "up"
			}
		}
	}
	return tui.FormatKeyText(strings.Join(keys, "/"), true)
}

// queueHintLine is the "↳ <key> to edit all queued messages" row. It reads the editor and the keybindings when it renders, so the key it
// names follows the editor emptying or filling in the Terminal.app state.
type queueHintLine struct{ m *InteractiveMode }

func (h queueHintLine) Render(width int) []string {
	text := "↳ " + h.m.dequeueHintKeyText() + " to edit all queued messages"
	return tui.NewTruncatedText(tui.ActiveTheme().Fg("dim", text), 1, 0).Render(width)
}

func (queueHintLine) Invalidate() {}

// queueCopyTipText is the one-time line shown when Up recalls history while a message is queued. The mapping hint is for a terminal that may be Terminal.app behind tmux or ssh.
func queueCopyTipText(dequeueKey string, mappingHint bool) string {
	text := "↳ That's a copy. The queued message still sends. " + dequeueKey + " edits the queue."
	if mappingHint {
		text += " " + terminalAppMappingHint
	}
	return text
}

// watchQueuedHistoryRecall returns, for an Up key pressed while a message is queued and the tip not yet shown, a function to call after the
// editor handled the key: it shows the copy tip when the key recalled a history entry (the editor text changed). Otherwise it returns nil.
func (m *InteractiveMode) watchQueuedHistoryRecall(data string) func() {
	if m.queueCopyTipShown || !m.builtInEditor() {
		return nil
	}
	kb := tui.GetTUIKeybindings()
	if !kb.Matches(data, tui.KBEditorCursorUp) && !kb.Matches(data, tui.KBEditorHistoryPrevious) {
		return nil
	}
	if m.queuedMessageCount() == 0 {
		return nil
	}
	before := m.editor.Text()
	return func() {
		dequeueKey := tui.ActionKeyDisplayText(string(AppMessageDequeue))
		if dequeueKey == "" || m.editor.Text() == before {
			return
		}
		m.queueCopyTipShown = true
		// pig divergence (D228): Pi shows nothing here; the tip says the recalled text is a copy of a message that still sends.
		mapping := !termProgramIs(appleTerminalProgram) && !sendsModifiedArrows() && slices.Contains(dequeueKeys(), dequeueOptionUpKey)
		text := queueCopyTipText(dequeueKey, mapping)
		m.setQueueCopyTip(tui.NewPaddedText(tui.ActiveTheme().Fg("dim", text), 1, 0, nil))
	}
}

// setQueueCopyTip puts tip under the editor, after the extension widgets there, or removes it when tip is nil.
func (m *InteractiveMode) setQueueCopyTip(tip tui.Component) {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	m.queueCopyTip = tip
	m.renderWidgetsLocked()
}

// clearQueueCopyTip removes the copy tip while it shows; a later recall shows nothing again, since the tip is once per process run.
func (m *InteractiveMode) clearQueueCopyTip() {
	m.widgetMu.Lock()
	defer m.widgetMu.Unlock()
	if m.queueCopyTip == nil {
		return
	}
	m.queueCopyTip = nil
	m.renderWidgetsLocked()
}
