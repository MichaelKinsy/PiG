package packagemanager

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// .upstream/current/packages/coding-agent/src/core/package-manager.ts:821 `new DefaultPackageManager(options)` stores options.cwd,
// options.agentDir and options.settingsManager; the agent directory is the option, not the settings manager's.
func TestNewPackageManagerStoresItsOptions(t *testing.T) {
	cwd, settingsAgentDir, optionAgentDir := t.TempDir(), t.TempDir(), t.TempDir()
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, settingsAgentDir, true)
	builtins := []string{"alpha", "beta"}
	m := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: optionAgentDir, SettingsManager: sm, BuiltinExtensions: builtins})
	if _, ok := any(m).(*PackageManager); !ok {
		t.Fatalf("NewPackageManager returned %T, want the DefaultPackageManager type", m)
	}
	builtins[0] = "changed"
	if len(m.BuiltinExtensions) != 2 || m.BuiltinExtensions[0] != "alpha" || m.BuiltinExtensions[1] != "beta" {
		t.Fatalf("builtinExtensions = %v, want a copy of the option", m.BuiltinExtensions)
	}
	if m.CWD != cwd || m.SettingsManager != sm {
		t.Fatalf("cwd %q settings %p", m.CWD, m.SettingsManager)
	}
	if got := m.agentDir(); got != optionAgentDir {
		t.Fatalf("agentDir = %q, want the option %q", got, optionAgentDir)
	}
	if got := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm}).agentDir(); got != settingsAgentDir {
		t.Fatalf("default agentDir = %q, want the settings manager's %q", got, settingsAgentDir)
	}
}
