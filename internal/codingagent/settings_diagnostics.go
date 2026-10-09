package codingagent

// settings_diagnostics.go ports upstream core/settings-diagnostics.ts: settings
// load and write failures become startup diagnostics, deduplicated across the
// startup and runtime settings managers.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// AgentSessionRuntimeDiagnostic is one startup diagnostic. Type is "info",
// "warning", or "error". Mirrors upstream AgentSessionRuntimeDiagnostic.
type AgentSessionRuntimeDiagnostic struct {
	Type    string
	Message string
}

// CollectSettingsDiagnostics drains the manager's settings errors as warnings
// naming the settings file, or the scope when no file backs the error.
// Mirrors upstream collectSettingsDiagnostics.
func CollectSettingsDiagnostics(settingsManager *SettingsManager) []AgentSessionRuntimeDiagnostic {
	var diagnostics []AgentSessionRuntimeDiagnostic
	for _, settingsError := range settingsManager.DrainErrors() {
		message := fmt.Sprintf("Invalid %s settings: %v", settingsError.Scope, settingsError.Error)
		if settingsError.Path != "" {
			message = fmt.Sprintf("Invalid settings file %s: %v", settingsError.Path, settingsError.Error)
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{Type: "warning", Message: message})
	}
	return diagnostics
}

// DeduplicateDiagnostics removes duplicate type/message diagnostics while
// preserving their first occurrence. Startup and runtime settings managers can
// report the same file error. Mirrors upstream deduplicateDiagnostics.
func DeduplicateDiagnostics(diagnostics []AgentSessionRuntimeDiagnostic) []AgentSessionRuntimeDiagnostic {
	seen := make(map[AgentSessionRuntimeDiagnostic]bool, len(diagnostics))
	var out []AgentSessionRuntimeDiagnostic
	for _, diagnostic := range diagnostics {
		if seen[diagnostic] {
			continue
		}
		seen[diagnostic] = true
		out = append(out, diagnostic)
	}
	return out
}

// ReportDiagnostics writes diagnostics to stderr for non-interactive modes:
// "Error: " in red, "Warning: " in yellow, and info dimmed, colored only when
// stderr is a terminal. Mirrors upstream main.ts reportDiagnostics.
func ReportDiagnostics(diagnostics []AgentSessionRuntimeDiagnostic) {
	color := term.IsTerminal(int(os.Stderr.Fd()))
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(os.Stderr, formatReportedDiagnostic(diagnostic, color))
	}
}

func formatReportedDiagnostic(diagnostic AgentSessionRuntimeDiagnostic, color bool) string {
	var prefix, open, closeSeq string
	switch diagnostic.Type {
	case "error":
		prefix, open, closeSeq = "Error: ", "\x1b[31m", "\x1b[39m"
	case "warning":
		prefix, open, closeSeq = "Warning: ", "\x1b[33m", "\x1b[39m"
	default:
		open, closeSeq = "\x1b[2m", "\x1b[22m"
	}
	if !color {
		return prefix + diagnostic.Message
	}
	return open + prefix + diagnostic.Message + closeSeq
}

// showStartupDiagnostics renders startup diagnostics in the chat after the
// welcome banner. Mirrors the upstream InteractiveMode.init loop.
func (m *InteractiveMode) showStartupDiagnostics() {
	for _, diagnostic := range m.opts.StartupDiagnostics {
		switch diagnostic.Type {
		case "error":
			m.showError(diagnostic.Message)
		case "warning":
			m.showWarning(diagnostic.Message)
		default:
			m.showStatus(diagnostic.Message)
		}
	}
}

// showModelsJSONError reports a models.json load error as upstream init and handleReloadCommand do.
func (m *InteractiveMode) showModelsJSONError() {
	if m.opts.ModelRegistry == nil {
		return
	}
	if loadError := m.opts.ModelRegistry.LoadError(); loadError != "" {
		m.showError("models.json error: " + loadError)
	}
}

// showInitNotices shows the startup notices in upstream init order: diagnostics, migrated credentials, the models.json error, then the model fallback message, even under quiet startup.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:init
func (m *InteractiveMode) showInitNotices() {
	m.showStartupDiagnostics()
	if len(m.opts.MigratedProviders) > 0 {
		m.showWarning("Migrated credentials to auth.json: " + strings.Join(m.opts.MigratedProviders, ", "))
	}
	m.showModelsJSONError()
	if m.opts.ModelFallbackMessage != "" {
		m.showWarning(m.opts.ModelFallbackMessage)
	}
}
