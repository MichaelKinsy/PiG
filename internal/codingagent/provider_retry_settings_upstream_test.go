package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7572-provider-retry-settings-merge.test.ts:5
func TestProviderRetrySettingsPreservesGlobalSettingsNotOverriddenByProject(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"retry":{"provider":{"timeoutMs":30000,"maxRetryDelayMs":45000}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), []byte(`{"retry":{"provider":{"maxRetries":2}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	want := ProviderRetryConfig{TimeoutMs: 30000, MaxRetries: 2, MaxRetryDelayMs: 45000}
	if got := settings.GetProviderRetrySettings(); got != want {
		t.Fatalf("provider retry settings = %+v, want %+v", got, want)
	}
}
