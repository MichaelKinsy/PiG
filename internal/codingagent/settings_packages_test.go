package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Pi settings-manager.ts:1105-1115 retains an assigned [] instead of deleting packages; JSON.stringify at :664 adds no trailing newline.
func TestSettingsPackagesPreserveEmpty(t *testing.T) {
	for _, local := range []bool{false, true} {
		cwd, agentDir := t.TempDir(), t.TempDir()
		sm := NewSettingsManager(cwd, agentDir)
		path := filepath.Join(agentDir, "settings.json")
		set := sm.SetPackages
		if local {
			path = filepath.Join(cwd, ".pig", "settings.json")
			set = sm.SetProjectPackages
		}
		for _, packages := range [][]PackageSource{{{Source: "npm:fixture"}}, {}, nil} {
			if err := set(packages); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := "{}"
			if packages != nil {
				value, err := json.MarshalIndent(struct {
					Packages []PackageSource `json:"packages"`
				}{packages}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				expected = string(value)
			}
			if string(data) != expected {
				t.Fatalf("local=%v packages=%#v: got %q want %q", local, packages, data, expected)
			}
			sm.Reload()
			got := sm.GetGlobalSettings().Packages
			if local {
				got = sm.GetProjectSettings().Packages
			}
			if (got == nil) != (packages == nil) || len(got) != len(packages) {
				t.Fatalf("reload lost presence: got %#v want %#v", got, packages)
			}
		}
	}
}
