package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// renderBuiltInHeader renders the current startup help expansion. Verbose seeds this state only at initialization; tool toggles and header restoration subsequently select it.
func (m *InteractiveMode) renderBuiltInHeader(width int) []string {
	theme := tui.ActiveTheme()
	var bindings *tui.TUIKeybindingsManager
	if m.keybindings != nil {
		bindings = m.keybindings.merged
	} else {
		bindings = tui.NewKeybindingsManager(keybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil)
	}
	key := func(action string) string {
		return tui.FormatKeyText(strings.Join(bindings.GetKeys(action), "/"), false)
	}
	rawHint := func(key, description string) string {
		return themeFg(theme.Dim, key) + themeFg(theme.Muted, " "+description)
	}
	hint := func(action, description string) string { return rawHint(key(action), description) }
	// pig divergence (D2): the command identity in the logo is pig.
	logo := "\x1b[1m" + themeFg(theme.Accent, "pig") + "\x1b[22m"
	// pig divergence (D63): the startup version is the composite PiG+Pi release identity.
	logo += themeFg(theme.Dim, " v"+pigversion.Version)
	m.toolMu.Lock()
	expanded := m.builtInHeaderExpanded
	m.toolMu.Unlock()
	var instructions string
	if expanded {
		instructions = strings.Join([]string{
			hint("app.interrupt", "to interrupt"),
			hint("app.clear", "to clear"),
			rawHint(key("app.clear")+" twice", "to exit"),
			hint("app.exit", "to exit (empty)"),
			hint("app.suspend", "to suspend"),
			hint("tui.editor.deleteToLineEnd", "to delete to end"),
			hint("app.thinking.cycle", "to cycle thinking level"),
			rawHint(key("app.model.cycleForward")+"/"+key("app.model.cycleBackward"), "to cycle models"),
			hint("app.model.select", "to select model"),
			hint("app.tools.expand", "to expand tools"),
			hint("app.thinking.toggle", "to expand thinking"),
			hint("app.editor.external", "for external editor"),
			rawHint("/", "for commands"),
			rawHint("!", "to run bash"),
			rawHint("!!", "to run bash (no context)"),
			hint("app.message.followUp", "to queue follow-up"),
			hint("app.message.dequeue", "to edit all queued messages"),
			hint("app.clipboard.pasteImage", "to paste image (with text fallback)"),
			rawHint("drop files", "to attach"),
		}, "\n")
	} else {
		instructions = strings.Join([]string{
			hint("app.interrupt", "interrupt"),
			rawHint(key("app.clear")+"/"+key("app.exit"), "clear/exit"),
			rawHint("/", "commands"),
			rawHint("!", "bash"),
			hint("app.tools.expand", "more"),
		}, themeFg(theme.Muted, " · "))
		instructions += "\n" + themeFg(theme.Dim, "Press "+key("app.tools.expand")+" to show full startup help and loaded resources.")
	}
	// pig divergence (D2): self-help names PiG rather than the separate Pi executable.
	onboarding := themeFg(theme.Dim, "PiG can explain its own features and look up its docs. Ask it how to use or extend PiG.")
	out := []string{""}
	out = append(out, tui.NewPaddedText(logo+"\n"+instructions+"\n\n"+onboarding, 1, 0, nil).Render(width)...)
	return append(out, "")
}
