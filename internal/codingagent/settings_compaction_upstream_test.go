// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func readCompactionSettings(sm *SettingsManager, provider, id string) (CompactionConfig, error) {
	return sm.GetModelCompactionSettings(provider, id)
}
func compactionConfigForTest(t *testing.T, sm *SettingsManager, model ...string) CompactionConfig {
	t.Helper()
	provider, id := "", ""
	if len(model) == 2 {
		provider, id = model[0], model[1]
	}
	config, err := sm.GetModelCompactionSettings(provider, id)
	if err != nil {
		t.Fatal(err)
	}
	return config
}
func compactionSettingsMemory(t *testing.T, source string) *SettingsManager {
	t.Helper()
	var settings Settings
	if err := json.Unmarshal([]byte(source), &settings); err != nil {
		t.Fatal(err)
	}
	return &SettingsManager{global: settings, merged: cloneSettings(settings)}
}
func applyCompactionJSON(t *testing.T, sm *SettingsManager, source string) {
	t.Helper()
	var settings Settings
	if err := json.Unmarshal([]byte(source), &settings); err != nil {
		t.Fatal(err)
	}
	sm.ApplyOverrides(settings)
}
func assertCompactionSettings(t *testing.T, sm *SettingsManager, provider, id string, want CompactionConfig) {
	t.Helper()
	got, err := readCompactionSettings(sm, provider, id)
	if err != nil || got != want {
		t.Fatalf("settings(%q,%q)=%+v,%v; want %+v", provider, id, got, err, want)
	}
	reserve, reserveErr := sm.GetCompactionReserveTokens(provider, id)
	recent, recentErr := sm.GetCompactionKeepRecentTokens(provider, id)
	if reserveErr != nil || recentErr != nil || reserve != want.ReserveTokens || recent != want.KeepRecentTokens {
		t.Fatalf("individual getters=%d,%v / %d,%v", reserve, reserveErr, recent, recentErr)
	}
}
func assertCompactionError(t *testing.T, sm *SettingsManager, provider, id, want string, prefix bool) {
	t.Helper()
	_, err := readCompactionSettings(sm, provider, id)
	if err == nil || (!prefix && err.Error() != want) || (prefix && !strings.HasPrefix(err.Error(), want)) {
		t.Fatalf("error=%v; want %q", err, want)
	}
	fmt.Printf("COMPACTION_SETTING %s\n", err)
}

func TestSettingsManagerCompactionUpstream(t *testing.T) {
	defaults := CompactionConfig{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:10
	t.Run("uses defaults without compaction settings", func(t *testing.T) {
		sm := compactionSettingsMemory(t, `{}`)
		assertCompactionSettings(t, sm, "", "", defaults)
		assertCompactionSettings(t, sm, "provider", "family/model", defaults)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:16
	t.Run("resolves each field independently and keeps individual getters consistent", func(t *testing.T) {
		sm := compactionSettingsMemory(t, `{"compaction":{"reserveTokens":8192,"keepRecentTokens":10000,"modelOverrides":{"provider/family/model":{"reserveTokens":400000}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 10000})
		assertCompactionSettings(t, sm, "", "", CompactionConfig{true, 8192, 10000})
		applyCompactionJSON(t, sm, `{"compaction":{"modelOverrides":{"provider/family/model":{"keepRecentTokens":30000}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 30000})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:38
	t.Run("falls back to built-in defaults for missing fields", func(t *testing.T) {
		sm := compactionSettingsMemory(t, `{"compaction":{"modelOverrides":{"provider/family/model":{"keepRecentTokens":1024}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 16384, 1024})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:45
	t.Run("matches exact provider model IDs including IDs containing slashes", func(t *testing.T) {
		sm := compactionSettingsMemory(t, `{"compaction":{"modelOverrides":{"provider/family/model":{"reserveTokens":400000},"provider/*":{"reserveTokens":1},"family/model":{"reserveTokens":2}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 20000})
		for _, model := range [][2]string{{"other", "family/model"}, {"provider", "other"}, {"provider", "family/Model"}} {
			assertCompactionSettings(t, sm, model[0], model[1], defaults)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:65
	t.Run("merges project model overrides per field before resolving fallbacks", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"compaction":{"reserveTokens":8192,"modelOverrides":{"provider/family/model":{"reserveTokens":400000,"keepRecentTokens":30000},"provider/other":{"keepRecentTokens":4096}}}}`, `{"compaction":{"reserveTokens":1024,"modelOverrides":{"provider/family/model":{"keepRecentTokens":2000}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 2000})
		assertCompactionSettings(t, sm, "provider", "other", CompactionConfig{true, 1024, 4096})
		sm.Load()
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 2000})
		sm.SetProjectTrusted(false)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 30000})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:100
	t.Run("keeps enabled global and preserves overrides when saving the toggle", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"compaction":{"modelOverrides":{"provider/family/model":{"enabled":false,"reserveTokens":400000}}}}`, "")
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 400000, 20000})
		if err := sm.SetCompactionEnabled(false); err != nil {
			t.Fatal(err)
		}
		if err := sm.Flush(); err != nil {
			t.Fatal(err)
		}
		sm.Load()
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{false, 400000, 20000})
	})
	invalid := []struct{ json, text string }{{"null", "null"}, {"-1", "-1"}, {"1.5", "1.5"}, {`"400000"`, "400000"}, {"true", "true"}, {"{}", "[object Object]"}, {"[]", ""}, {"9007199254740992", "9007199254740992"}}
	for _, field := range []string{"reserveTokens", "keepRecentTokens"} {
		// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:116
		t.Run("model override "+field+" reports invalid token values", func(t *testing.T) {
			for _, value := range invalid {
				t.Run(value.json, func(t *testing.T) {
					sm := writeSettingsLayers(t, fmt.Sprintf(`{"compaction":{"modelOverrides":{"provider/family/model":{"%s":%s}}}}`, field, value.json), "")
					assertCompactionError(t, sm, "provider", "family/model", fmt.Sprintf(`Invalid compaction.modelOverrides["provider/family/model"].%s setting: %s. Expected a non-negative safe integer.`, field, value.text), false)
					assertCompactionSettings(t, sm, "", "", defaults)
					assertCompactionSettings(t, sm, "other", "family/model", defaults)
				})
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:134
		t.Run("model override "+field+" reports non-finite runtime values", func(t *testing.T) {
			for _, value := range []struct {
				number float64
				text   string
			}{{math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"}} {
				t.Run(value.text, func(t *testing.T) {
					sm := compactionSettingsMemory(t, `{}`)
					override := CompactionModelOverride{}
					if field == "reserveTokens" {
						override.ReserveTokens = new(value.number)
					} else {
						override.KeepRecentTokens = new(value.number)
					}
					sm.ApplyOverrides(Settings{Compaction: &CompactionSettingsJSON{ModelOverrides: map[string]CompactionModelOverride{"provider/family/model": override}}})
					assertCompactionError(t, sm, "provider", "family/model", fmt.Sprintf(`Invalid compaction.modelOverrides["provider/family/model"].%s setting: %s`, field, value.text), true)
				})
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:144
		t.Run("ordinary compaction "+field+" reports invalid values even when valid override exists", func(t *testing.T) {
			for _, value := range invalid {
				t.Run(value.json, func(t *testing.T) {
					sm := writeSettingsLayers(t, fmt.Sprintf(`{"compaction":{"%s":%s,"modelOverrides":{"provider/family/model":{"%s":4096}}}}`, field, value.json, field), "")
					want := fmt.Sprintf("Invalid compaction.%s setting: %s. Expected a non-negative safe integer.", field, value.text)
					assertCompactionError(t, sm, "", "", want, false)
					assertCompactionError(t, sm, "provider", "family/model", want, false)
				})
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:163
		t.Run("ordinary compaction "+field+" reports non-finite runtime values", func(t *testing.T) {
			for _, value := range []struct {
				number float64
				text   string
			}{{math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"}} {
				t.Run(value.text, func(t *testing.T) {
					sm := compactionSettingsMemory(t, `{}`)
					settings := &CompactionSettingsJSON{}
					if field == "reserveTokens" {
						settings.ReserveTokens = new(value.number)
					} else {
						settings.KeepRecentTokens = new(value.number)
					}
					sm.ApplyOverrides(Settings{Compaction: settings})
					assertCompactionError(t, sm, "", "", fmt.Sprintf("Invalid compaction.%s setting: %s", field, value.text), true)
				})
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:170
	t.Run("reports malformed model entries", func(t *testing.T) {
		for _, value := range []struct{ json, text string }{{"null", "null"}, {"false", "false"}, {"42", "42"}, {`"invalid"`, "invalid"}, {"[]", ""}} {
			t.Run(value.json, func(t *testing.T) {
				sm := writeSettingsLayers(t, fmt.Sprintf(`{"compaction":{"modelOverrides":{"provider/family/model":%s}}}`, value.json), "")
				assertCompactionError(t, sm, "provider", "family/model", fmt.Sprintf(`Invalid compaction.modelOverrides["provider/family/model"] setting: %s. Expected an object.`, value.text), false)
			})
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager-compaction.test.ts:178
	t.Run("accepts zero in ordinary settings and model overrides", func(t *testing.T) {
		sm := compactionSettingsMemory(t, `{"compaction":{"reserveTokens":0,"keepRecentTokens":0}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 0, 0})
		applyCompactionJSON(t, sm, `{"compaction":{"reserveTokens":1000,"keepRecentTokens":1000,"modelOverrides":{"provider/family/model":{"reserveTokens":0,"keepRecentTokens":0}}}}`)
		assertCompactionSettings(t, sm, "provider", "family/model", CompactionConfig{true, 0, 0})
	})
}
