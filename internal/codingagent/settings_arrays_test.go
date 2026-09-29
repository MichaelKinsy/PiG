package codingagent

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Settings arrays replace prior arrays, including when the replacement is empty (settings-manager.ts deepMergeSettings/save).
func TestSettingsExplicitEmptyArraysPersist(t *testing.T) {
	const initial = `{"packages":["npm:example"],"extensions":["ext.ts"],"skills":["skill"],"prompts":["prompt"],"themes":["theme"],"enabledModels":["model"],"defaultTools":["read"],"npmCommand":["npm"]}`
	const empty = `{"packages":[],"extensions":[],"skills":[],"prompts":[],"themes":[],"enabledModels":[],"defaultTools":[],"npmCommand":[]}`
	sm := writeSettingsLayers(t, initial, `{}`)
	settingsOK(t, sm.UpdateGlobal(func(s *Settings) {
		s.Packages = []PackageSource{}
		s.Extensions = []string{}
		s.Skills = []string{}
		s.Prompts = []string{}
		s.Themes = []string{}
		s.EnabledModels = []string{}
		s.DefaultTools = []string{}
		s.NpmCommand = []string{}
	}))
	settingsOK(t, sm.Flush())
	sm.Reload()
	assertSettingsFileJSON(t, sm.GlobalPath(), empty)
	settingsOK(t, sm.SetTheme("dark"))
	sm.Reload()
	raw, err := json.Marshal(sm.GetGlobalSettings())
	settingsOK(t, err)
	var value any
	settingsOK(t, json.Unmarshal(raw, &value))
	raw, err = json.Marshal(value)
	settingsOK(t, err)
	fmt.Printf("SETTINGS_ARRAYS %s\n", raw)
	// Default list getters return arrays, but missing defaultTools remains undefined.
	absent := NewInMemorySettingsManager(Settings{})
	raw, err = json.Marshal([]any{absent.GetPackages(), absent.GetExtensionPaths(), absent.GetSkillPaths(), absent.GetPromptTemplatePaths(), absent.GetThemePaths(), absent.GetDefaultTools(), absent.DrainErrors()})
	settingsOK(t, err)
	fmt.Printf("SETTINGS_ARRAY_DEFAULTS %s\n", raw)
}

func BenchmarkSettingsMemoryReload(b *testing.B) {
	for _, size := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			settings := Settings{Extensions: make([]string, size)}
			for i := range settings.Extensions {
				settings.Extensions[i] = fmt.Sprintf("extension-%d.ts", i)
			}
			sm := NewInMemorySettingsManager(settings)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sm.Reload()
				_ = sm.Get()
			}
		})
	}
}
