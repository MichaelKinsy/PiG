package coding

// pi: packages/coding-agent/src/core/agent-session-services.ts

import (
	"os"
	"path/filepath"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Upstream AgentSessionServices (core/agent-session-services.ts) carries cwd, agentDir, modelRuntime and settingsManager; AgentSessionServices exposes them as CWD, AgentDir, ModelRuntime and SettingsManager.
func TestServicesCarryTheirCwdAgentDirModelRuntimeAndSettingsManager(t *testing.T) {
	home := withTempHome(t)
	cwd := t.TempDir()
	agentDir := filepath.Join(home, "custom-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"agent-dir-theme"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)

	if got := services.CWD(); got != cwd {
		t.Errorf("CWD = %q, want %q", got, cwd)
	}
	if got := services.AgentDir(); got != agentDir {
		t.Errorf("AgentDir = %q, want %q", got, agentDir)
	}
	runtime := services.ModelRuntime()
	if runtime == nil || runtime != services.ModelRuntime() {
		t.Errorf("ModelRuntime = %p then %p, want one shared non-nil runtime", runtime, services.ModelRuntime())
	}
	manager := services.SettingsManager()
	if manager == nil {
		t.Fatal("SettingsManager is nil")
	}
	if got := manager.GetTheme(); got != "agent-dir-theme" {
		t.Errorf("SettingsManager theme = %q, want the settings.json of AgentDir", got)
	}
	if manager != services.SettingsManager() {
		t.Error("SettingsManager must be the one live manager, not a copy")
	}
}

// Upstream createAgentSessionServices passes a caller-supplied settingsManager through unchanged.
func TestServicesReturnTheCallerSuppliedSettingsManager(t *testing.T) {
	home := withTempHome(t)
	supplied := icodingagent.NewSettingsManager(t.TempDir(), filepath.Join(home, "supplied-agent"))
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: filepath.Join(home, "other-agent"), SettingsManager: supplied})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if services.SettingsManager() != supplied {
		t.Error("SettingsManager did not return the supplied manager")
	}
}
