// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.
package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// headerContainer keeps host spacing outside the replaceable header component, including when a custom header renders no rows.
func (m *InteractiveMode) headerContainer() *tui.Container {
	if !m.opts.LoginVisible {
		return tui.NewContainer(m.extHeader)
	}
	return tui.NewContainer(tui.NewSpacer(1), m.extHeader, tui.NewSpacer(1))
}

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
	// The logo's first line carries the version, its second line the first line of key hints (interactive-mode.ts:977-980).
	logoTop, logoBottom := piLogoLines(theme.ColorMode())
	// pig divergence (D63): the startup version is the composite PiG+Pi release identity.
	withLogo := func(hints string) string {
		return logoTop + " " + themeFg(theme.Dim, "v"+pigversion.Version) + "\n" + logoBottom + " " + hints
	}
	m.toolMu.Lock()
	expanded := m.builtInHeaderExpanded
	m.toolMu.Unlock()
	var instructions string
	if expanded {
		instructions = withLogo(strings.Join([]string{
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
			hint("app.clipboard.pasteImage", "to paste files on macOS, images, or text"),
			rawHint("drop files", "to attach"),
		}, "\n"))
	} else {
		instructions = strings.Join([]string{
			hint("app.interrupt", "interrupt"),
			rawHint(key("app.clear")+"/"+key("app.exit"), "clear/exit"),
			rawHint("/", "commands"),
			rawHint("!", "bash"),
			hint("app.tools.expand", "more"),
		}, themeFg(theme.Muted, " · "))
		instructions = withLogo(instructions) + "\n" + themeFg(theme.Dim, "Press "+key("app.tools.expand")+" to show full startup help and loaded resources.")
	}
	// pig divergence (D2): self-help names PiG rather than the separate Pi executable.
	onboarding := themeFg(theme.Dim, "PiG can explain its own features and look up its docs. Ask it how to use or extend PiG.")
	return tui.NewPaddedText(instructions+"\n\n"+onboarding, 1, 0, nil).Render(width)
}

// Ports packages/coding-agent/src/modes/interactive/components/pi-logo.ts
//
// piLogoLines is the pi logo, 4 cells wide and 2 lines tall, in a terminal color mode.
// It mirrors pi-logo.ts: the brand colors stay fixed across themes and follow the terminal's color mode.
func piLogoLines(mode tui.TerminalColorMode) (top, bottom string) {
	const reset = "\x1b[0m"
	coral, blue, yellow := piLogoColor(228, 138, 122), piLogoColor(79, 142, 179), piLogoColor(234, 182, 93)
	fg := func(color tui.Color) string { return tui.ForegroundAnsi(color, mode) }
	// The fourth cell of the top line is empty, so it is padded to the width of the bottom line.
	top = fg(coral) + tui.BackgroundAnsi(blue, mode) + "▀" + reset + fg(coral) + "▀█" + reset + " "
	bottom = fg(blue) + "█▀" + reset + " " + fg(yellow) + "█" + reset
	return top, bottom
}

func piLogoColor(r, g, b float64) tui.Color {
	color, err := tui.NewRgbColor(r, g, b)
	if err != nil {
		panic(err) // the channels are constants inside 0..255
	}
	return color
}
