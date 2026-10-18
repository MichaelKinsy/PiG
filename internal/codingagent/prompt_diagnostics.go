package codingagent

import (
	"os"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Ports packages/coding-agent/src/core/prompt-templates.ts
// Ports packages/coding-agent/src/utils/frontmatter.ts
func (m *InteractiveMode) loadPromptTemplates() {
	// resource-loader.ts updatePromptsFromPaths: the session's cwd and agent directory classify each template's sourceInfo; the paths already include the default directories.
	loaded := LoadPromptTemplatesFromOptions(LoadPromptTemplatesOptions{Cwd: m.opts.CWD, AgentDir: m.opts.AgentDir, PromptPaths: m.opts.PromptPaths})
	templates, collisions := DedupePromptTemplates(loaded.Templates)
	m.promptTemplates = templates
	m.promptDiagnostics = slices.Concat(loaded.Diagnostics, collisions)
	m.publishSlashCommandCatalog()
}

// reportMissingPromptTemplatePaths is the check resource-loader.ts reload makes after loading the prompts (:615-626): each
// --prompt-template path, trimmed and resolved against the Session's cwd as resolveResourcePath does (:1035-1036), that does not exist
// is an error diagnostic, unless the loader already reported the path. An extension's resources_discover reloads the prompts without
// it.
func (m *InteractiveMode) reportMissingPromptTemplatePaths() {
	for _, raw := range m.opts.CLIPromptTemplatePaths {
		path, err := resolvepath.ResolveTrimmed(raw, m.opts.CWD)
		if err != nil {
			path = raw
		}
		if nodeExists(path) || slices.ContainsFunc(m.promptDiagnostics, func(d extension.ResourceDiagnostic) bool { return d.Path == path }) {
			continue
		}
		m.promptDiagnostics = append(m.promptDiagnostics, extension.ResourceDiagnostic{Type: extension.DiagnosticError, Message: "Prompt template path does not exist", Path: path})
	}
}

// nodeExists is fs.existsSync: Node stats the namespaced form of the path, so on Windows "x." or "x " is not the file "x".
func nodeExists(path string) bool {
	namespaced, err := nodepath.ToNamespacedPath(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(namespaced)
	return err == nil
}
