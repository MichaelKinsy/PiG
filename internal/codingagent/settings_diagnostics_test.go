package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Mirrors upstream test/settings-diagnostics.test.ts.

func TestCollectSettingsDiagnosticsIncludesTheSettingsFilePath(t *testing.T) {
	tempDir := t.TempDir()
	agentDir := filepath.Join(tempDir, "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	diagnostics := CollectSettingsDiagnostics(NewSettingsManager(tempDir, agentDir))
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one", diagnostics)
	}
	if diagnostics[0].Type != "warning" {
		t.Fatalf("type = %q, want warning", diagnostics[0].Type)
	}
	if want := "Invalid settings file " + settingsPath + ":"; !strings.Contains(diagnostics[0].Message, want) {
		t.Fatalf("message = %q, want it to contain %q", diagnostics[0].Message, want)
	}
}

func TestCollectSettingsDiagnosticsFallsBackToTheScopeWithoutAPath(t *testing.T) {
	sm := &SettingsManager{errors: []SettingsError{{Scope: "global", Error: errors.New("backend failed")}}}
	got := CollectSettingsDiagnostics(sm)
	want := []AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "Invalid global settings: backend failed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostics = %+v, want %+v", got, want)
	}
}

func TestDeduplicateDiagnosticsByTypeAndMessage(t *testing.T) {
	warning := AgentSessionRuntimeDiagnostic{Type: "warning", Message: "Invalid settings file /tmp/settings.json"}
	errorDiagnostic := AgentSessionRuntimeDiagnostic{Type: "error", Message: warning.Message}
	got := DeduplicateDiagnostics([]AgentSessionRuntimeDiagnostic{warning, warning, errorDiagnostic})
	if want := []AgentSessionRuntimeDiagnostic{warning, errorDiagnostic}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated = %+v, want %+v", got, want)
	}
}

// Mirrors upstream main.ts reportDiagnostics prefixes and chalk colors.
func TestFormatReportedDiagnostic(t *testing.T) {
	cases := []struct {
		diagnostic AgentSessionRuntimeDiagnostic
		plain      string
		colored    string
	}{
		{AgentSessionRuntimeDiagnostic{Type: "error", Message: "boom"}, "Error: boom", "\x1b[31mError: boom\x1b[39m"},
		{AgentSessionRuntimeDiagnostic{Type: "warning", Message: "careful"}, "Warning: careful", "\x1b[33mWarning: careful\x1b[39m"},
		{AgentSessionRuntimeDiagnostic{Type: "info", Message: "note"}, "note", "\x1b[2mnote\x1b[22m"},
	}
	for _, tc := range cases {
		if got := formatReportedDiagnostic(tc.diagnostic, false); got != tc.plain {
			t.Errorf("plain %s = %q, want %q", tc.diagnostic.Type, got, tc.plain)
		}
		if got := formatReportedDiagnostic(tc.diagnostic, true); got != tc.colored {
			t.Errorf("colored %s = %q, want %q", tc.diagnostic.Type, got, tc.colored)
		}
	}
}

// Upstream InteractiveMode.init routes each startup diagnostic by type.
func TestShowStartupDiagnosticsRendersEachType(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.opts.StartupDiagnostics = []AgentSessionRuntimeDiagnostic{
		{Type: "warning", Message: "Invalid settings file /x/settings.json: bad"},
		{Type: "error", Message: "broken"},
		{Type: "info", Message: "fyi"},
	}
	m.showStartupDiagnostics()
	rendered := stripANSITest(strings.Join(m.chatContainer.Render(100), "\n"))
	for _, want := range []string{"Invalid settings file /x/settings.json: bad", "Error: broken", "fyi"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("chat %q missing %q", rendered, want)
		}
	}
}
