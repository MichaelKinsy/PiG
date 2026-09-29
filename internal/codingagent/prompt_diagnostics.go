package codingagent

func (m *InteractiveMode) loadPromptTemplates() {
	result := LoadPromptTemplates("", "", m.opts.PromptPaths...)
	m.promptTemplates = result.Templates
	m.promptDiagnostics = result.Diagnostics
	m.publishSlashCommandCatalog()
}
