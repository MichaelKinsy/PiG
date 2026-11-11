// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// getModelScopeNotice is the "Model scope: ..." notice: the scoped model ids with their thinking levels in dim, and the cycle key in muted. It is empty when no model is scoped or the startup details are hidden. The main-screen renderer prints it before the terminal UI starts; fullscreen shows it in the header.
func (m *InteractiveMode) getModelScopeNotice() string {
	scoped := m.extensionScopedModels()
	if len(scoped) == 0 || !m.shouldShowStartupDetails() {
		return ""
	}
	entries := make([]string, len(scoped))
	for i, entry := range scoped {
		entries[i] = entry.Model.ID
		if entry.ThinkingLevel != "" {
			entries[i] += ":" + string(entry.ThinkingLevel)
		}
	}
	theme := tui.ActiveTheme()
	var cycleHint string
	keybindings := m.keybindings
	if keybindings == nil {
		keybindings = NewKeybindingsManager(m.opts.AgentDir)
	}
	if keys := keybindings.GetKeys("app.model.cycleForward"); len(keys) > 0 {
		cycleHint = theme.Fg("muted", " ("+tui.FormatKeyText(strings.Join(keys, "/"), true)+" to cycle)")
	}
	return theme.Fg("dim", "Model scope: "+strings.Join(entries, ", ")+cycleHint)
}

// showFullscreenModelScopeNotice adds the model scope notice and a spacer to the header container. The alternate screen would leave stdout text in the shell after exit, so fullscreen shows the notice here instead of printing it. Nothing is added without a notice.
func (m *InteractiveMode) showFullscreenModelScopeNotice(header *tui.Container) {
	if m.getModelScopeNotice() == "" {
		return
	}
	header.Add(tui.NewThemedText(m.getModelScopeNotice, 1, 0))
	header.Add(tui.NewSpacer(1))
}
