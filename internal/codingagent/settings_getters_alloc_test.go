package codingagent

import (
	"bytes"
	"encoding/json"
	"testing"
)

// settingsWithEveryKind is merged settings that hold slices, maps and pointers, so a deep copy of them allocates.
func settingsWithEveryKind(t testing.TB) Settings {
	t.Helper()
	var s Settings
	if err := json.Unmarshal([]byte(`{
		"defaultProvider":"anthropic","defaultModel":"m","transport":"sse","steeringMode":"all","codeBlockIndent":"  ",
		"packages":["npm:a",{"source":"npm:b","extensions":["x"]}],"extensions":["e1","e2"],"skills":["s"],"prompts":["p"],"themes":["t"],
		"enabledModels":["a/b","c/d"],"defaultTools":["read","+bash"],"modelThinkingLevels":{"a/b":"high"},
		"compaction":{"enabled":true,"reserveTokens":1000},"branchSummary":{"reserveTokens":2000},"retry":{"enabled":true,"maxRetries":5,"provider":{"maxRetryDelayMs":5}},
		"showImages":true,"imageAutoResize":false,"editorPaddingX":2,"outputPad":1,"autocompleteMaxVisible":7,"clearOnShrink":true,
		"enableSkillCommands":false,"thinkingBudgets":{"low":10},"npmCommand":["pnpm"],"markdown":{"mermaid":"off","codeBlockIndent":"  "},
		"warnings":{"anthropicExtraUsage":false},"terminal":{"showTerminalProgress":true}
	}`), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The scalar getters read the merged settings under the read lock; they do not deep-copy all of Settings per call (AS-F8).
func TestSettingsScalarGettersDoNotCopySettings(t *testing.T) {
	// GetExternalEditorCommand reads VISUAL and EDITOR as Pi reads process.env. On Windows a set variable allocates its UTF-16 to string
	// conversion (os.Getenv), while an unset or empty one does not (env_lookup_windows.go); a runner that sets either would count as an allocation.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	sm := NewInMemorySettingsManager(settingsWithEveryKind(t))
	getters := map[string]func(){
		"GetBlockImages":                func() { _ = sm.GetBlockImages() },
		"GetRetrySettings":              func() { _ = sm.GetRetrySettings() },
		"GetRetryEnabled":               func() { _ = sm.GetRetryEnabled() },
		"GetCodeBlockIndent":            func() { _ = sm.GetCodeBlockIndent() },
		"GetSteeringMode":               func() { _ = sm.GetSteeringMode() },
		"GetFollowUpMode":               func() { _ = sm.GetFollowUpMode() },
		"GetTransport":                  func() { _ = sm.GetTransport() },
		"GetHideThinkingBlock":          func() { _ = sm.GetHideThinkingBlock() },
		"GetShowCacheMissNotices":       func() { _ = sm.GetShowCacheMissNotices() },
		"GetShowImages":                 func() { _ = sm.GetShowImages() },
		"GetImageWidthCells":            func() { _ = sm.GetImageWidthCells() },
		"GetImageAutoResize":            func() { _ = sm.GetImageAutoResize() },
		"GetClearOnShrink":              func() { _ = sm.GetClearOnShrink() },
		"GetShowTerminalProgress":       func() { _ = sm.GetShowTerminalProgress() },
		"GetShowHardwareCursor":         func() { _ = sm.GetShowHardwareCursor() },
		"GetEditorPaddingX":             func() { _ = sm.GetEditorPaddingX() },
		"GetOutputPad":                  func() { _ = sm.GetOutputPad() },
		"GetAutocompleteMaxVisible":     func() { _ = sm.GetAutocompleteMaxVisible() },
		"GetTuiMode":                    func() { _ = sm.GetTuiMode() },
		"GetFullscreenExitOutput":       func() { _ = sm.GetFullscreenExitOutput() },
		"GetFullscreenCopyOnSelect":     func() { _ = sm.GetFullscreenCopyOnSelect() },
		"GetFullscreenScrollbar":        func() { _ = sm.GetFullscreenScrollbar() },
		"GetFullscreenWheelScrollLines": func() { _ = sm.GetFullscreenWheelScrollLines() },
		"GetDoubleEscapeAction":         func() { _ = sm.GetDoubleEscapeAction() },
		"GetDefaultProjectTrust":        func() { _ = sm.GetDefaultProjectTrust() },
		"GetCacheWarmingMode":           func() { _ = sm.GetCacheWarmingMode() },
		"GetLastChangelogVersion":       func() { _ = sm.GetLastChangelogVersion() },
		"GetMermaidRenderingMode":       func() { _ = sm.GetMermaidRenderingMode() },
		"GetEnableSkillCommands":        func() { _ = sm.GetEnableSkillCommands() },
		"GetCollapseChangelog":          func() { _ = sm.GetCollapseChangelog() },
		"GetTreeFilterMode":             func() { _ = sm.GetTreeFilterMode() },
		"GetBranchSummarySettings":      func() { _ = sm.GetBranchSummarySettings() },
		"GetHttpIdleTimeoutMs":          func() { _, _ = sm.GetHttpIdleTimeoutMs() },
		"GetProviderRequestTimeoutMs":   func() { _, _ = sm.GetProviderRequestTimeoutMs() },
		"GetDefaultProvider":            func() { _ = sm.GetDefaultProvider() },
		"GetDefaultModel":               func() { _ = sm.GetDefaultModel() },
		"GetTheme":                      func() { _ = sm.GetTheme() },
		"GetDefaultThinkingLevel":       func() { _ = sm.GetDefaultThinkingLevel() },
		"GetQuietStartup":               func() { _ = sm.GetQuietStartup() },
		"GetShellCommandPrefix":         func() { _ = sm.GetShellCommandPrefix() },
		"GetEnableAnalytics":            func() { _ = sm.GetEnableAnalytics() },
		"GetTrackingID":                 func() { _ = sm.GetTrackingID() },
		"GetEnableInstallTelemetry":     func() { _ = sm.GetEnableInstallTelemetry() },
		"GetModelThinkingLevel":         func() { _ = sm.GetModelThinkingLevel("a", "b") },
		"GetWarnings":                   func() { _ = sm.GetWarnings() },
		"GetExternalEditorCommand":      func() { _ = sm.GetExternalEditorCommand() },
	}
	for name, get := range getters {
		if allocs := testing.AllocsPerRun(100, get); allocs != 0 {
			t.Errorf("%s: %v allocs per call, want 0", name, allocs)
		}
	}
}

// A getter that returns a slice, map or pointer returns a copy: a change through it does not reach the manager.
func TestSettingsReferenceGettersReturnCopies(t *testing.T) {
	sm := NewInMemorySettingsManager(settingsWithEveryKind(t))
	sm.GetExtensionPaths()[0] = "changed"
	sm.GetSkillPaths()[0] = "changed"
	sm.GetPromptTemplatePaths()[0] = "changed"
	sm.GetThemePaths()[0] = "changed"
	sm.GetEnabledModels()[0] = "changed"
	sm.GetNpmCommand()[0] = "changed"
	sm.GetPackages()[0].Source = "changed"
	sm.GetDefaultTools()[0] = "changed"
	if budgets := sm.GetThinkingBudgets(); budgets != nil && budgets.Low != nil {
		*budgets.Low = 99
	}
	sm.GetAllModelThinkingLevels()["a/b"] = "changed"
	if timeout, _ := sm.GetWebSocketConnectTimeoutMs(); timeout != nil {
		*timeout = 99
	}
	if theme := sm.GetThemeSetting(); theme != nil {
		*theme = "changed"
	}
	want := settingsWithEveryKind(t)
	got := sm.Get()
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("a getter's result reached the manager:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}
