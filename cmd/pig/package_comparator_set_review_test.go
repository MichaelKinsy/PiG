package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Review GF03-REVIEW-002: Pi package-manager.ts:1446-1457,1473-1478 retains installed 1.0.0 for a matching comparator set. Offline mode proves source parsing cannot misclassify an installed package as missing and cannot invoke an installer.
func TestEnsureConfiguredNPMComparatorSetFindsInstalledPackage(t *testing.T) {
	for _, name := range []string{"example", "@scope/example"} {
		for _, local := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/user", true: "/project"}[local], func(t *testing.T) {
				cwd, agentDir := t.TempDir(), t.TempDir()
				t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
				t.Setenv("PIG_OFFLINE", "1")
				t.Setenv("PI_OFFLINE", "1")
				sm := codingagent.NewSettingsManager(cwd, agentDir)
				root := filepath.Join(agentDir, "npm")
				setPackages := sm.SetPackages
				if local {
					root = filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "npm")
					setPackages = sm.SetProjectPackages
				}
				const selector = ">=1.0.0 <2.0.0"
				if err := setPackages([]codingagent.PackageSource{{Source: "npm:" + name + "@" + selector}}); err != nil {
					t.Fatal(err)
				}
				installed := filepath.Join(root, "node_modules", filepath.FromSlash(name))
				if err := os.MkdirAll(installed, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(installed, "package.json"), []byte(`{"version":"1.0.0"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				reinstalled, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, sm.AgentDir(), sm)
				if len(reinstalled) != 0 || len(missing) != 0 {
					t.Fatalf("matching installed package lost: reinstalled=%v missing=%v", reinstalled, missing)
				}
			})
		}
	}
}
