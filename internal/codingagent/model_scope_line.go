// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// modelScopeLine is the "Model scope: ..." line init() prints with console.log before the terminal UI starts (interactive-mode.ts:950-963): the scoped model ids with their thinking levels in dim, and the cycle key in muted. It is empty when no model is scoped or the startup details are hidden.
func (m *InteractiveMode) modelScopeLine() string {
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
