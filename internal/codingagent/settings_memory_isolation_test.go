package codingagent

import (
	"fmt"
	"path/filepath"
	"testing"
)

// settings-manager.ts:403-410 clones initial settings before storing them; reload reads independent backing layers.
func TestInMemorySettingsInitialSnapshotsAreIndependent(t *testing.T) {
	for _, size := range []int{0, 1, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			names := make([]string, size)
			for i := range names {
				names[i] = fmt.Sprintf("extension-%d.ts", i)
			}
			initial := Settings{Extensions: names, DefaultThinkingLevel: "high"}
			first := NewInMemorySettingsManager(initial)
			second := NewInMemorySettingsManager(initial)
			if len(names) > 0 {
				names[0] = "mutated-input.ts"
				view := first.GetGlobalSettings()
				view.Extensions[0] = "mutated-snapshot.ts"
			}
			if err := first.SetDefaultThinkingLevel("low"); err != nil {
				t.Fatal(err)
			}
			first.Reload()
			second.Reload()
			for _, sm := range []*SettingsManager{first, second} {
				got := sm.GetGlobalSettings().Extensions
				if got == nil || len(got) != size {
					t.Fatalf("extensions=%v, want non-nil length %d", got, size)
				}
				for i, name := range got {
					if want := fmt.Sprintf("extension-%d.ts", i); name != want {
						t.Fatalf("extensions[%d]=%q, want %q", i, name, want)
					}
				}
				if sm.GlobalPath() != "" || sm.GlobalSettingsPath() != "" {
					t.Fatal("memory storage exposed a settings file path")
				}
			}
			if first.GetDefaultThinkingLevel() != "low" || second.GetDefaultThinkingLevel() != "high" {
				t.Fatal("independent settings managers shared a mutation")
			}
		})
	}
}

// settings-manager.ts:515-537 reloads only the project layer when trust changes and returns immediately when trust is unchanged.
func TestSettingsTrustChangeKeepsGlobalStateAndNoOpOverrides(t *testing.T) {
	sm := writeSettingsLayers(t, `{"theme":"global-before"}`, `{"defaultThinkingLevel":"low"}`)
	sm.SetProjectTrusted(false)
	writeSettingsFixture(t, sm.GlobalPath(), `{"theme":"external-global"}`)
	writeSettingsFixture(t, filepath.Join(sm.CWD(), CONFIG_DIR_NAME, "settings.json"), `{"defaultThinkingLevel":"high"}`)
	sm.SetProjectTrusted(true)
	if sm.Get().Theme != "global-before" || sm.GetDefaultThinkingLevel() != "high" {
		t.Fatalf("trust change refreshed wrong layers: %+v", sm.Get())
	}
	sm.ApplyOverrides(Settings{Theme: "transient"})
	sm.SetProjectTrusted(true)
	if sm.Get().Theme != "transient" {
		t.Fatal("unchanged trust discarded transient overrides")
	}
}
