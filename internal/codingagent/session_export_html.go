//go:build !pig_strip_export_html

package codingagent

// session_export_html.go holds the session-level HTML export of upstream
// core/export-html/index.ts. It and its internal/codingagent/export import
// (the HTML template, CSS, JS and vendored marked/highlight.js embeds) are the
// boundary a Piglet Binary compiles out with the pig_strip_export_html tag;
// session_export_html_stripped.go replaces them.

import (
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/export"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// ExportToolRenderers is upstream AgentSession's getToolRenderers for HTML
// exports: the resolvers of runner's extensions in load order, then the tools
// they registered. A nil runner draws no tool through renderers.
// upstream: packages/coding-agent/src/core/agent-session.ts:exportToHtml (getToolRenderers)
func ExportToolRenderers(runner *inproc.Runner) func(name string) *extension.ToolRenderers {
	// pig additive (D92): a Piglet that strips export-html draws no tool renderers, as its Binary does.
	if runner == nil || pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExportHTML) {
		return nil
	}
	registered := export.ToolRenderersOf(runner.Tools())
	return func(name string) *extension.ToolRenderers {
		return runner.ResolveToolRenderers(name, func() *extension.ToolRenderers { return registered(name) })
	}
}

// ExportSessionToHTML writes the session file as HTML the way upstream
// exportSessionToHtml does and returns the path written. An in-memory session
// (empty sessionFile) and a session whose file is not written yet fail with
// upstream's messages. outputPath is normalized as upstream normalizePath does
// and written relative to the process working directory without creating its
// parent; an empty outputPath becomes pig-session-<session basename>.html.
// themeName is the export theme [ExportThemeName] chose (empty exports with the active theme).
// state is the live agent state upstream passes to exportSessionToHtml: the
// export embeds its system prompt and active tool schemas. A Piglet that
// strips export-html gets the stripped error before any of these checks.
func ExportSessionToHTML(sessionFile, outputPath string, getToolRenderers func(name string) *extension.ToolRenderers, cwd string, state ShareState, themeName string) (string, error) {
	// pig additive (D92): a Piglet that strips export-html fails as its Binary does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExportHTML) {
		return "", pigstrip.Error("HTML export", pigstrip.ListFeatures, pigstrip.ExportHTML)
	}
	if sessionFile == "" {
		return "", errors.New("Cannot export in-memory session to HTML")
	}
	if _, err := os.Stat(sessionFile); err != nil {
		return "", errors.New("Nothing to export yet - start a conversation first")
	}
	outputPath, err := normalizeSettingsPath(outputPath)
	if err != nil {
		return "", err
	}
	agentState := export.AgentState{SystemPrompt: state.SystemPrompt, Tools: make([]export.ToolSchema, len(state.Tools))}
	for i, tool := range state.Tools {
		agentState.Tools[i] = export.ToolSchema(tool)
	}
	return export.ExportFromFileWithTools(sessionFile, outputPath, getToolRenderers, cwd, &agentState, themeName)
}

// ExportFileToHTML is the CLI's --export: exportFromFile (core/export-html/index.ts) opens the session file as SessionManager.open does, so a
// missing file, a directory, a file that is not a session and an empty file (which becomes a new session) answer as Pi's, then writes the HTML.
// The input and the output path are resolved and normalized like Pi's resolvePath and normalizePath; an empty outputPath becomes
// <app>-session-<input basename>.html in the working directory.
func ExportFileToHTML(inputPath, outputPath string) (string, error) {
	resolved, err := ResolvePath(inputPath, "")
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("File not found: %s", resolved)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if _, err := NewSessionManager(cwd).Open(resolved); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	data, err := export.FromJSONL(raw)
	if err != nil {
		return "", err
	}
	if outputPath != "" {
		if outputPath, err = normalizeSettingsPath(outputPath); err != nil {
			return "", err
		}
	}
	return export.WriteHTML(data, resolved, outputPath)
}
