package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Upstream settings-manager.ts: getSteeringMode and getFollowUpMode are `setting || "one-at-a-time"`,
// getEnableSkillCommands is `?? true`, getShowCacheMissNotices is `?? false`, getThinkingBudgets returns the stored object or undefined.
// Pi: packages/coding-agent/src/core/settings-manager.ts:1050 (SettingsManager.getShowCacheMissNotices); packages/coding-agent/src/core/settings-manager.ts:1264 (SettingsManager.getEnableSkillCommands); packages/coding-agent/src/core/settings-manager.ts:1274 (SettingsManager.getThinkingBudgets); packages/coding-agent/src/core/settings-manager.ts:830 (SettingsManager.getSteeringMode); packages/coding-agent/src/core/settings-manager.ts:840 (SettingsManager.getFollowUpMode).
func TestSettingsManagerSimpleGettersMatchUpstreamDefaultsAndStoredValues(t *testing.T) {
	load := func(content string) *SettingsManager {
		t.Helper()
		agentDir, cwd := t.TempDir(), t.TempDir()
		if content != "" {
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return NewSettingsManager(cwd, agentDir)
	}
	defaults := load("")
	if defaults.GetSteeringMode() != "one-at-a-time" || defaults.GetFollowUpMode() != "one-at-a-time" || !defaults.GetEnableSkillCommands() || defaults.GetShowCacheMissNotices() || defaults.GetThinkingBudgets() != nil {
		t.Fatalf("defaults: steering=%q followUp=%q skills=%v cacheMiss=%v budgets=%v", defaults.GetSteeringMode(), defaults.GetFollowUpMode(), defaults.GetEnableSkillCommands(), defaults.GetShowCacheMissNotices(), defaults.GetThinkingBudgets())
	}
	set := load(`{"steeringMode":"all","followUpMode":"all","enableSkillCommands":false,"showCacheMissNotices":true,"thinkingBudgets":{"low":1234,"high":0}}`)
	if set.GetSteeringMode() != "all" || set.GetFollowUpMode() != "all" || set.GetEnableSkillCommands() || !set.GetShowCacheMissNotices() {
		t.Fatalf("stored: steering=%q followUp=%q skills=%v cacheMiss=%v", set.GetSteeringMode(), set.GetFollowUpMode(), set.GetEnableSkillCommands(), set.GetShowCacheMissNotices())
	}
	budgets := set.GetThinkingBudgets()
	if budgets == nil || budgets.Low == nil || *budgets.Low != 1234 || budgets.High == nil || *budgets.High != 0 || budgets.Minimal != nil || budgets.Medium != nil {
		t.Fatalf("thinking budgets = %+v", budgets)
	}
	// An empty mode string is falsy, so the default applies.
	empty := load(`{"steeringMode":"","followUpMode":""}`)
	if empty.GetSteeringMode() != "one-at-a-time" || empty.GetFollowUpMode() != "one-at-a-time" {
		t.Fatalf("empty modes: %q %q", empty.GetSteeringMode(), empty.GetFollowUpMode())
	}
}
