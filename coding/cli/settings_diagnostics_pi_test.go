package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/src/core/settings-diagnostics.ts

// Ports test/settings-diagnostics.test.ts "includes the settings file path for file-backed storage" through the command line's own wiring: a settings.json
// holding "{" is reported by the startup settings manager (main.ts:684) and again by the runtime's (main.ts:811), and the build's diagnostics hold the one
// warning that names the file (main.ts:887 deduplicateDiagnostics).
func TestStartupBuildReportsAnInvalidSettingsFileOnceWithItsPath(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", agentDir)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs([]string{"--model", "test-faux/faux-1", "--print"})
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
		settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
		startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
		trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
	}
	build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd, Startup: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
	var settingsWarnings []codingagent.AgentSessionRuntimeDiagnostic
	for _, diagnostic := range build.StartupDiagnostics {
		if strings.Contains(diagnostic.Message, "Invalid settings file") {
			settingsWarnings = append(settingsWarnings, diagnostic)
		}
	}
	if len(settingsWarnings) != 1 {
		t.Fatalf("settings diagnostics = %+v, want exactly one", build.StartupDiagnostics)
	}
	if settingsWarnings[0].Type != "warning" {
		t.Fatalf("type = %q, want warning", settingsWarnings[0].Type)
	}
	if want := "Invalid settings file " + settingsPath + ":"; !strings.Contains(settingsWarnings[0].Message, want) {
		t.Fatalf("message = %q, want it to contain %q", settingsWarnings[0].Message, want)
	}
}
