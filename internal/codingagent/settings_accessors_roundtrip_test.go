package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/settings-manager.ts: every getXxx reads the key its setter writes, with the documented default when the key is absent,
// and every setXxx persists exactly that key to the global (or, for the setProject* path setters, the project) settings file.
// mutation-checked: each accessor below was mutated to return its zero value or to skip its write and this test failed.

func settingsAccessorsManager(t *testing.T, content string, trusted bool) (*SettingsManager, string, string) {
	t.Helper()
	agentDir, cwd := t.TempDir(), t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewSettingsManagerWithProjectTrust(cwd, agentDir, trusted), agentDir, cwd
}

func settingsAccessorsFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func expectSetting(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

func TestSettingsManagerGettersReadTheirKeysAndDefaultWhenAbsent(t *testing.T) {
	t.Setenv("PI_HARDWARE_CURSOR", "")
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	empty, _, _ := settingsAccessorsManager(t, "", false)
	expectSetting(t, "default LastChangelogVersion", empty.GetLastChangelogVersion(), "")
	expectSetting(t, "default DefaultProvider", empty.GetDefaultProvider(), "")
	expectSetting(t, "default DefaultThinkingLevel", empty.GetDefaultThinkingLevel(), ai.ThinkingLevel(""))
	expectSetting(t, "default Transport", empty.GetTransport(), "auto")
	expectSetting(t, "default SteeringMode", empty.GetSteeringMode(), "one-at-a-time")
	expectSetting(t, "default FollowUpMode", empty.GetFollowUpMode(), "one-at-a-time")
	expectSetting(t, "default HideThinkingBlock", empty.GetHideThinkingBlock(), false)
	expectSetting(t, "default QuietStartup", empty.GetQuietStartup(), QuietStartupFalse)
	expectSetting(t, "default NpmCommand", empty.GetNpmCommand(), []string(nil))
	expectSetting(t, "default CollapseChangelog", empty.GetCollapseChangelog(), false)
	expectSetting(t, "default ShowCacheMissNotices", empty.GetShowCacheMissNotices(), false)
	expectSetting(t, "default EnableInstallTelemetry", empty.GetEnableInstallTelemetry(), true)
	expectSetting(t, "default TrackingID", empty.GetTrackingID(), "")
	expectSetting(t, "default SkillPaths", empty.GetSkillPaths(), []string{})
	expectSetting(t, "default PromptTemplatePaths", empty.GetPromptTemplatePaths(), []string{})
	expectSetting(t, "default ThemePaths", empty.GetThemePaths(), []string{})
	expectSetting(t, "default EnableSkillCommands", empty.GetEnableSkillCommands(), true)
	expectSetting(t, "default ShowImages", empty.GetShowImages(), true)
	expectSetting(t, "default ImageWidthCells", empty.GetImageWidthCells(), 60)
	expectSetting(t, "default ImageAutoResize", empty.GetImageAutoResize(), true)
	expectSetting(t, "default BlockImages", empty.GetBlockImages(), false)
	expectSetting(t, "default ClearOnShrink", empty.GetClearOnShrink(), false)
	expectSetting(t, "default ShowTerminalProgress", empty.GetShowTerminalProgress(), false)
	expectSetting(t, "default ShowHardwareCursor", empty.GetShowHardwareCursor(), false)
	expectSetting(t, "default EditorPaddingX", empty.GetEditorPaddingX(), 0)
	expectSetting(t, "default AutocompleteMaxVisible", empty.GetAutocompleteMaxVisible(), 5)
	expectSetting(t, "default CodeBlockIndent", empty.GetCodeBlockIndent(), "  ")
	expectSetting(t, "default DoubleEscapeAction", empty.GetDoubleEscapeAction(), "tree")
	expectSetting(t, "default TreeFilterMode", empty.GetTreeFilterMode(), "default")
	expectSetting(t, "default ThinkingBudgets", empty.GetThinkingBudgets(), (*ThinkingBudgetsSettings)(nil))
	expectSetting(t, "default ModelThinkingLevel", empty.GetModelThinkingLevel("a", "b"), ai.ThinkingLevel(""))
	expectSetting(t, "default AllModelThinkingLevels", empty.GetAllModelThinkingLevels(), map[string]ai.ThinkingLevel{})
	expectSetting(t, "default Warnings", empty.GetWarnings(), WarningSettings{AnthropicExtraUsage: true, anthropicExtraUsageSet: true})
	expectSetting(t, "default BranchSummarySettings", empty.GetBranchSummarySettings().ReserveTokens, 16384)
	expectSetting(t, "default BranchSummarySkipPrompt", empty.GetBranchSummarySkipPrompt(), false)
	expectSetting(t, "default ProviderRetryMaxDelay", empty.GetProviderRetrySettings().MaxRetryDelayMs, 60000)
	if timeout, err := empty.GetWebSocketConnectTimeoutMs(); err != nil || timeout != nil {
		t.Errorf("default WebSocketConnectTimeoutMs = %v, %v", timeout, err)
	}
	compaction, err := empty.GetCompactionSettings()
	expectSetting(t, "default CompactionSettings", []any{compaction, err}, []any{CompactionConfig{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}, error(nil)})
	expectSetting(t, "GetSettings of an empty file", empty.GetSettings().DefaultProvider, "")

	set, _, _ := settingsAccessorsManager(t, `{
	  "lastChangelogVersion":"1.2.3","defaultProvider":"prov","defaultThinkingLevel":"high","transport":"sse",
	  "steeringMode":"all","followUpMode":"all","hideThinkingBlock":true,"quietStartup":"header","npmCommand":["pnpm","x"],
	  "collapseChangelog":true,"showCacheMissNotices":true,"enableInstallTelemetry":false,"trackingId":"track-1",
	  "skills":["s1"],"prompts":["p1"],"themes":["t1"],"enableSkillCommands":false,
	  "terminal":{"showImages":false,"imageWidthCells":40,"clearOnShrink":true,"showTerminalProgress":true},
	  "images":{"autoResize":false,"blockImages":true},
	  "modelThinkingLevels":{"a/b":"low","c/d":"high"},"doubleEscapeAction":"fork","treeFilterMode":"user-only",
	  "thinkingBudgets":{"low":1234},"editorPaddingX":2,"autocompleteMaxVisible":9,"showHardwareCursor":true,
	  "markdown":{"codeBlockIndent":"    "},"warnings":{"anthropicExtraUsage":false},"websocketConnectTimeoutMs":1500,
	  "compaction":{"reserveTokens":1111,"keepRecentTokens":2222},"branchSummary":{"reserveTokens":333,"skipPrompt":true},
	  "retry":{"provider":{"timeoutMs":5,"maxRetries":2,"maxRetryDelayMs":7}}}`, false)
	expectSetting(t, "LastChangelogVersion", set.GetLastChangelogVersion(), "1.2.3")
	expectSetting(t, "DefaultProvider", set.GetDefaultProvider(), "prov")
	expectSetting(t, "DefaultThinkingLevel", set.GetDefaultThinkingLevel(), ai.ThinkingLevel("high"))
	expectSetting(t, "Transport", set.GetTransport(), "sse")
	expectSetting(t, "SteeringMode", set.GetSteeringMode(), "all")
	expectSetting(t, "FollowUpMode", set.GetFollowUpMode(), "all")
	expectSetting(t, "HideThinkingBlock", set.GetHideThinkingBlock(), true)
	expectSetting(t, "QuietStartup", set.GetQuietStartup(), QuietStartupHeader)
	expectSetting(t, "NpmCommand", set.GetNpmCommand(), []string{"pnpm", "x"})
	expectSetting(t, "CollapseChangelog", set.GetCollapseChangelog(), true)
	expectSetting(t, "ShowCacheMissNotices", set.GetShowCacheMissNotices(), true)
	expectSetting(t, "EnableInstallTelemetry", set.GetEnableInstallTelemetry(), false)
	expectSetting(t, "TrackingID", set.GetTrackingID(), "track-1")
	expectSetting(t, "SkillPaths", set.GetSkillPaths(), []string{"s1"})
	expectSetting(t, "PromptTemplatePaths", set.GetPromptTemplatePaths(), []string{"p1"})
	expectSetting(t, "ThemePaths", set.GetThemePaths(), []string{"t1"})
	expectSetting(t, "EnableSkillCommands", set.GetEnableSkillCommands(), false)
	expectSetting(t, "ShowImages", set.GetShowImages(), false)
	expectSetting(t, "ImageWidthCells", set.GetImageWidthCells(), 40)
	expectSetting(t, "ImageAutoResize", set.GetImageAutoResize(), false)
	expectSetting(t, "BlockImages", set.GetBlockImages(), true)
	expectSetting(t, "ClearOnShrink", set.GetClearOnShrink(), true)
	expectSetting(t, "ShowTerminalProgress", set.GetShowTerminalProgress(), true)
	expectSetting(t, "ShowHardwareCursor", set.GetShowHardwareCursor(), true)
	expectSetting(t, "EditorPaddingX", set.GetEditorPaddingX(), 2)
	expectSetting(t, "AutocompleteMaxVisible", set.GetAutocompleteMaxVisible(), 9)
	expectSetting(t, "CodeBlockIndent", set.GetCodeBlockIndent(), "    ")
	expectSetting(t, "DoubleEscapeAction", set.GetDoubleEscapeAction(), "fork")
	expectSetting(t, "TreeFilterMode", set.GetTreeFilterMode(), "user-only")
	if budgets := set.GetThinkingBudgets(); budgets == nil || budgets.Low == nil || *budgets.Low != 1234 {
		t.Errorf("ThinkingBudgets = %+v", budgets)
	}
	expectSetting(t, "ModelThinkingLevel", set.GetModelThinkingLevel("c", "d"), ai.ThinkingLevel("high"))
	expectSetting(t, "AllModelThinkingLevels", set.GetAllModelThinkingLevels(), map[string]ai.ThinkingLevel{"a/b": "low", "c/d": "high"})
	expectSetting(t, "Warnings", set.GetWarnings(), WarningSettings{AnthropicExtraUsage: false, anthropicExtraUsageSet: true})
	branch := set.GetBranchSummarySettings()
	expectSetting(t, "BranchSummarySettings", []any{branch.ReserveTokens, branch.SkipPrompt}, []any{333, true})
	expectSetting(t, "BranchSummarySkipPrompt", set.GetBranchSummarySkipPrompt(), true)
	retry := set.GetProviderRetrySettings()
	if retry.TimeoutMs == nil || *retry.TimeoutMs != 5 || retry.MaxRetries == nil || *retry.MaxRetries != 2 || retry.MaxRetryDelayMs != 7 {
		t.Errorf("ProviderRetrySettings = %+v", retry)
	}
	if timeout, err := set.GetWebSocketConnectTimeoutMs(); err != nil || timeout == nil || *timeout != 1500 {
		t.Errorf("WebSocketConnectTimeoutMs = %v, %v", timeout, err)
	}
	compaction, err = set.GetCompactionSettings()
	expectSetting(t, "CompactionSettings", []any{compaction, err}, []any{CompactionConfig{Enabled: true, ReserveTokens: 1111, KeepRecentTokens: 2222}, error(nil)})
	reserve, err := set.GetCompactionReserveTokens()
	expectSetting(t, "CompactionReserveTokens", []any{reserve, err}, []any{1111, error(nil)})
	keep, err := set.GetCompactionKeepRecentTokens()
	expectSetting(t, "CompactionKeepRecentTokens", []any{keep, err}, []any{2222, error(nil)})
	expectSetting(t, "GetSettings", set.GetSettings().TrackingID, "track-1")

	invalid, _, _ := settingsAccessorsManager(t, `{"treeFilterMode":"bogus","doubleEscapeAction":""}`, false)
	expectSetting(t, "an unknown TreeFilterMode", invalid.GetTreeFilterMode(), "default")
}

func TestSettingsManagerSettersPersistTheirKeyAndTheGetterReadsItBack(t *testing.T) {
	manager, agentDir, _ := settingsAccessorsManager(t, "", true)
	global := filepath.Join(agentDir, "settings.json")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(manager.SetDefaultProvider("prov"))
	must(manager.SetDefaultModel("mod"))
	must(manager.SetDefaultProjectTrust(DefaultProjectTrustAlways))
	must(manager.SetDoubleEscapeAction("fork"))
	must(manager.SetEnableAnalytics(true))
	must(manager.SetEnableInstallTelemetry(false))
	must(manager.SetEnableSkillCommands(false))
	must(manager.SetHideThinkingBlock(true))
	must(manager.SetHttpIdleTimeoutMs(4000))
	must(manager.SetImageAutoResize(false))
	must(manager.SetBlockImages(true))
	must(manager.SetClearOnShrink(true))
	must(manager.SetCollapseChangelog(true))
	must(manager.SetAutocompleteMaxVisible(50))
	must(manager.SetModelThinkingLevel("p", "m", "high"))
	must(manager.SetPackages([]PackageSource{{Source: "npm:pkg"}}))
	must(manager.SetPromptTemplatePaths([]string{"p1"}))
	must(manager.SetSkillPaths([]string{"s1"}))
	must(manager.SetThemePaths([]string{"t1"}))
	must(manager.SetQuietStartup(QuietStartupTrue))
	must(manager.SetRetryEnabled(false))
	must(manager.SetShowCacheMissNotices(true))
	must(manager.SetShowHardwareCursor(true))
	must(manager.SetShowTerminalProgress(true))
	must(manager.SetTreeFilterMode("labeled-only"))
	must(manager.SetWarnings(WarningSettings{AnthropicExtraUsage: false}))

	file := settingsAccessorsFile(t, global)
	for key, want := range map[string]any{
		"defaultProvider": "prov", "defaultModel": "mod", "defaultProjectTrust": "always", "doubleEscapeAction": "fork",
		"enableAnalytics": true, "enableInstallTelemetry": false, "enableSkillCommands": false, "hideThinkingBlock": true,
		"httpIdleTimeoutMs": float64(4000), "collapseChangelog": true, "autocompleteMaxVisible": float64(20),
		"modelThinkingLevels": map[string]any{"p/m": "high"}, "prompts": []any{"p1"}, "skills": []any{"s1"}, "themes": []any{"t1"},
		"quietStartup": true, "showCacheMissNotices": true, "showHardwareCursor": true, "treeFilterMode": "labeled-only",
		"retry": map[string]any{"enabled": false}, "warnings": map[string]any{"anthropicExtraUsage": false},
		"packages": []any{"npm:pkg"},
	} {
		expectSetting(t, "persisted "+key, file[key], want)
	}
	if id, _ := file["trackingId"].(string); id == "" {
		t.Errorf("the first analytics opt-in wrote no trackingId: %v", file["trackingId"])
	}
	expectSetting(t, "persisted images", file["images"], map[string]any{"autoResize": false, "blockImages": true})
	if terminal, _ := file["terminal"].(map[string]any); terminal["clearOnShrink"] != true || terminal["showTerminalProgress"] != true {
		t.Errorf("persisted terminal = %v", file["terminal"])
	}

	reloaded := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, true)
	expectSetting(t, "reloaded DefaultProvider", reloaded.GetDefaultProvider(), "prov")
	expectSetting(t, "reloaded DefaultProjectTrust", reloaded.GetDefaultProjectTrust(), DefaultProjectTrustAlways)
	expectSetting(t, "reloaded DoubleEscapeAction", reloaded.GetDoubleEscapeAction(), "fork")
	expectSetting(t, "reloaded TrackingID", reloaded.GetTrackingID(), file["trackingId"])
	expectSetting(t, "reloaded EnableInstallTelemetry", reloaded.GetEnableInstallTelemetry(), false)
	expectSetting(t, "reloaded AutocompleteMaxVisible", reloaded.GetAutocompleteMaxVisible(), 20)
	expectSetting(t, "reloaded ModelThinkingLevel", reloaded.GetModelThinkingLevel("p", "m"), ai.ThinkingLevel("high"))
	expectSetting(t, "reloaded TreeFilterMode", reloaded.GetTreeFilterMode(), "labeled-only")
	expectSetting(t, "reloaded Warnings", reloaded.GetWarnings(), WarningSettings{AnthropicExtraUsage: false, anthropicExtraUsageSet: true})
	expectSetting(t, "reloaded QuietStartup", reloaded.GetQuietStartup(), QuietStartupTrue)

	must(manager.SetEnableAnalytics(false))
	must(manager.RemoveModelThinkingLevel("p", "m"))
	must(manager.RemoveModelThinkingLevel("never", "set"))
	file = settingsAccessorsFile(t, global)
	if _, has := file["modelThinkingLevels"]; has {
		t.Errorf("RemoveModelThinkingLevel of the last entry left %v", file["modelThinkingLevels"])
	}
	expectSetting(t, "a later opt-out keeps the tracking id", file["trackingId"] != nil && file["enableAnalytics"] == false, true)
}

func TestSettingsManagerProjectPathSettersWriteTheProjectFile(t *testing.T) {
	manager, agentDir, cwd := settingsAccessorsManager(t, "", true)
	for _, err := range []error{
		manager.SetProjectExtensionPaths([]string{"e1"}),
		manager.SetProjectSkillPaths([]string{"s1"}),
		manager.SetProjectPromptTemplatePaths([]string{"p1"}),
		manager.SetProjectThemePaths([]string{"t1"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	file := settingsAccessorsFile(t, filepath.Join(ProjectConfigDir(cwd), "settings.json"))
	for key, want := range map[string]any{"extensions": []any{"e1"}, "skills": []any{"s1"}, "prompts": []any{"p1"}, "themes": []any{"t1"}} {
		expectSetting(t, "project "+key, file[key], want)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "settings.json")); err == nil {
		t.Errorf("a project path setter wrote the global settings file")
	}
}
