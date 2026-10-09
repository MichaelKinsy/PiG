package codingagent

import "slices"

func (m *InteractiveMode) loadPromptTemplates() {
	// resource-loader.ts updatePromptsFromPaths: the session's cwd and agent directory classify each template's sourceInfo; the paths already include the default directories.
	loaded := LoadPromptTemplatesFromOptions(LoadPromptTemplatesOptions{Cwd: m.opts.CWD, AgentDir: m.opts.AgentDir, PromptPaths: m.opts.PromptPaths})
	templates, collisions := DedupePromptTemplates(loaded.Templates)
	m.promptTemplates = templates
	m.promptDiagnostics = slices.Concat(loaded.Diagnostics, collisions)
	m.publishSlashCommandCatalog()
}
