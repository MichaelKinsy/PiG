package codingagent

// settings_diagnostics.go ports upstream core/settings-diagnostics.ts: settings
// load and write failures become startup diagnostics, deduplicated across the
// startup and runtime settings managers.

import (
	"fmt"
	"strings"
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

// showRuntimeDiagnostics renders runtime diagnostics in the chat: an error as an error, a warning as a warning, anything else as a status line. Startup shows its diagnostics after the welcome banner, and the rebind callback shows a replacement's before the replacing command's own message.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts showRuntimeDiagnostics (c47ad0432bb8); it runs on the owner.
func (m *InteractiveMode) showRuntimeDiagnostics(diagnostics []AgentSessionRuntimeDiagnostic) {
	for _, diagnostic := range diagnostics {
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

// showInitNotices shows the startup notices in upstream init order: diagnostics, migrated credentials, the models.json error, then the model fallback message, even under quiet startup. PiG's frame pacing notice follows the diagnostics (showFramePacingNotice, D625).
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:init
func (m *InteractiveMode) showInitNotices() {
	m.showRuntimeDiagnostics(m.opts.StartupDiagnostics)
	m.showFramePacingNotice()
	if len(m.opts.MigratedProviders) > 0 {
		m.showWarning("Migrated credentials to auth.json: " + strings.Join(m.opts.MigratedProviders, ", "))
	}
	m.showModelsJSONError()
	if m.opts.ModelFallbackMessage != "" {
		m.showWarning(m.opts.ModelFallbackMessage)
	}
}
