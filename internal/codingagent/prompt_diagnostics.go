package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) loadPromptTemplates() {
	result := LoadPromptTemplates("", "", m.opts.PromptPaths...)
	m.promptTemplates = result.Templates
	m.promptDiagnostics = result.Diagnostics
	m.publishSlashCommandCatalog()
}

func (m *InteractiveMode) showPromptDiagnostics() {
	if len(m.promptDiagnostics) == 0 {
		return
	}
	theme := tui.ActiveTheme()
	var lines strings.Builder
	lines.WriteString(theme.FgText("warning", "[Prompt conflicts]"))
	for _, diagnostic := range m.promptDiagnostics {
		lines.WriteByte('\n')
		lines.WriteString(theme.FgText("warning", "  "+diagnostic.Path))
		lines.WriteByte('\n')
		lines.WriteString(theme.FgText("warning", "    "+diagnostic.Message))
	}
	m.appendChatBlock(tui.NewText(lines.String()))
	m.tuiInst.Render()
}
