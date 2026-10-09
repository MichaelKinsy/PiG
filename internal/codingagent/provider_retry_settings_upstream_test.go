package codingagent

import (
	"encoding/json"
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
	got := settings.GetProviderRetrySettings()
	if got.TimeoutMs == nil || *got.TimeoutMs != 30000 || got.MaxRetries == nil || *got.MaxRetries != 2 || got.MaxRetryDelayMs != 45000 {
		t.Fatalf("provider retry settings = %+v, want timeoutMs 30000, maxRetries 2, maxRetryDelayMs 45000", got)
	}
}

// settings-manager.ts:1034-1040: getProviderRetrySettings returns `undefined` for an unset timeoutMs or maxRetries, so an unset value and an explicit 0 differ.
func TestProviderRetrySettingsDistinguishUnsetFromExplicitZero(t *testing.T) {
	unset := NewInMemorySettingsManager(Settings{}).GetProviderRetrySettings()
	if unset.TimeoutMs != nil || unset.MaxRetries != nil || unset.MaxRetryDelayMs != 60000 {
		t.Fatalf("unset = %+v, want nil, nil, 60000", unset)
	}
	var parsed Settings
	if err := json.Unmarshal([]byte(`{"retry":{"provider":{"timeoutMs":0,"maxRetries":0}}}`), &parsed); err != nil {
		t.Fatal(err)
	}
	zero := NewInMemorySettingsManager(parsed).GetProviderRetrySettings()
	if zero.TimeoutMs == nil || *zero.TimeoutMs != 0 || zero.MaxRetries == nil || *zero.MaxRetries != 0 {
		t.Fatalf("explicit zero = %+v, want pointers to 0", zero)
	}
}
