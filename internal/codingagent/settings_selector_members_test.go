package codingagent

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// packages/coding-agent/src/modes/interactive/components/settings-selector.ts (SettingsConfig, SettingsCallbacks): the
// selector shows every SettingsConfig value as its row's current value and reports each change of a cycling row through
// the matching SettingsCallbacks member with the new value.
// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:104 (SettingsCallbacks.onAutoCompactChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:105 (SettingsCallbacks.onShowImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:106 (SettingsCallbacks.onImageWidthCellsChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:107 (SettingsCallbacks.onAutoResizeImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:108 (SettingsCallbacks.onBlockImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:109 (SettingsCallbacks.onEnableSkillCommandsChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:110 (SettingsCallbacks.onSteeringModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:111 (SettingsCallbacks.onFollowUpModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:112 (SettingsCallbacks.onTransportChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:119 (SettingsCallbacks.onHideThinkingBlockChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:121 (SettingsCallbacks.onShowCacheMissNoticesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:122 (SettingsCallbacks.onCollapseChangelogChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:123 (SettingsCallbacks.onEnableInstallTelemetryChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:124 (SettingsCallbacks.onDoubleEscapeActionChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:125 (SettingsCallbacks.onTreeFilterModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:126 (SettingsCallbacks.onShowHardwareCursorChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:127 (SettingsCallbacks.onEditorPaddingXChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:129 (SettingsCallbacks.onAutocompleteMaxVisibleChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:131 (SettingsCallbacks.onDefaultProjectTrustChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:132 (SettingsCallbacks.onClearOnShrinkChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:133 (SettingsCallbacks.onShowTerminalProgressChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:134 (SettingsCallbacks.onTuiModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:60 (SettingsConfig.autoCompact); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:64 (SettingsConfig.showImages); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:65 (SettingsConfig.imageWidthCells); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:66 (SettingsConfig.autoResizeImages); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:67 (SettingsConfig.blockImages); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:68 (SettingsConfig.enableSkillCommands); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:69 (SettingsConfig.steeringMode); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:70 (SettingsConfig.followUpMode); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:71 (SettingsConfig.transport); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:75 (SettingsConfig.availableThinkingLevels); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:80 (SettingsConfig.hideThinkingBlock); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:82 (SettingsConfig.showCacheMissNotices); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:83 (SettingsConfig.collapseChangelog); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:84 (SettingsConfig.enableInstallTelemetry); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:85 (SettingsConfig.doubleEscapeAction); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:86 (SettingsConfig.treeFilterMode); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:87 (SettingsConfig.showHardwareCursor); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:88 (SettingsConfig.editorPaddingX); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:90 (SettingsConfig.autocompleteMaxVisible); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:92 (SettingsConfig.defaultProjectTrust); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:93 (SettingsConfig.clearOnShrink); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:94 (SettingsConfig.showTerminalProgress).
// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:104 (SettingsCallbacks.onAutoCompactChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:105 (SettingsCallbacks.onShowImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:106 (SettingsCallbacks.onImageWidthCellsChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:107 (SettingsCallbacks.onAutoResizeImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:108 (SettingsCallbacks.onBlockImagesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:109 (SettingsCallbacks.onEnableSkillCommandsChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:110 (SettingsCallbacks.onSteeringModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:111 (SettingsCallbacks.onFollowUpModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:112 (SettingsCallbacks.onTransportChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:119 (SettingsCallbacks.onHideThinkingBlockChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:121 (SettingsCallbacks.onShowCacheMissNoticesChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:122 (SettingsCallbacks.onCollapseChangelogChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:123 (SettingsCallbacks.onEnableInstallTelemetryChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:124 (SettingsCallbacks.onDoubleEscapeActionChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:125 (SettingsCallbacks.onTreeFilterModeChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:126 (SettingsCallbacks.onShowHardwareCursorChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:127 (SettingsCallbacks.onEditorPaddingXChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:129 (SettingsCallbacks.onAutocompleteMaxVisibleChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:131 (SettingsCallbacks.onDefaultProjectTrustChange).
func TestSettingsSelectorReportsEveryCyclingRowThroughItsCallback(t *testing.T) {
	initSettingsSelectorTheme(t)
	// The image rows exist only on a terminal with image support (settings-selector.ts supportsImages), so the test fixes the capabilities
	// instead of reading the ones of the terminal it runs in.
	previousCapabilities := tui.GetCapabilities()
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	t.Cleanup(func() { tui.SetCapabilities(previousCapabilities) })
	fired := map[string]string{}
	record := func(name string) func(value any) {
		return func(value any) { fired[name] = fmt.Sprint(value) }
	}
	config := SettingsConfig{
		AutoCompact: true, ShowImages: true, ImageWidthCells: 60, AutoResizeImages: true, BlockImages: false,
		EnableSkillCommands: true, SteeringMode: "one-at-a-time", FollowUpMode: "one-at-a-time", Transport: "sse",
		HttpIdleTimeoutMs: 60000, ThinkingLevel: "medium", AvailableThinkingLevels: []string{"off", "low", "medium", "high"},
		HideThinkingBlock: false, ShowCacheMissNotices: true, CollapseChangelog: false, EnableInstallTelemetry: true,
		DoubleEscapeAction: "tree", TreeFilterMode: "default", ShowHardwareCursor: false, EditorPaddingX: 0,
		AutocompleteMaxVisible: 5, DefaultProjectTrust: "ask", ClearOnShrink: false, ShowTerminalProgress: false,
		TuiMode: "regular", DefaultModel: "not set",
	}
	callbacks := SettingsCallbacks{
		OnAutoCompactChange:            func(v bool) { record("AutoCompact")(v) },
		OnShowImagesChange:             func(v bool) { record("ShowImages")(v) },
		OnImageWidthCellsChange:        func(v int) { record("ImageWidthCells")(v) },
		OnAutoResizeImagesChange:       func(v bool) { record("AutoResizeImages")(v) },
		OnBlockImagesChange:            func(v bool) { record("BlockImages")(v) },
		OnEnableSkillCommandsChange:    func(v bool) { record("EnableSkillCommands")(v) },
		OnSteeringModeChange:           func(v string) { record("SteeringMode")(v) },
		OnFollowUpModeChange:           func(v string) { record("FollowUpMode")(v) },
		OnTransportChange:              func(v string) { record("Transport")(v) },
		OnHttpIdleTimeoutMsChange:      func(v int) { record("HttpIdleTimeoutMs")(v) },
		OnHideThinkingBlockChange:      func(v bool) { record("HideThinkingBlock")(v) },
		OnShowCacheMissNoticesChange:   func(v bool) { record("ShowCacheMissNotices")(v) },
		OnCollapseChangelogChange:      func(v bool) { record("CollapseChangelog")(v) },
		OnEnableInstallTelemetryChange: func(v bool) { record("EnableInstallTelemetry")(v) },
		OnDoubleEscapeActionChange:     func(v string) { record("DoubleEscapeAction")(v) },
		OnTreeFilterModeChange:         func(v string) { record("TreeFilterMode")(v) },
		OnShowHardwareCursorChange:     func(v bool) { record("ShowHardwareCursor")(v) },
		OnEditorPaddingXChange:         func(v int) { record("EditorPaddingX")(v) },
		OnAutocompleteMaxVisibleChange: func(v int) { record("AutocompleteMaxVisible")(v) },
		OnDefaultProjectTrustChange:    func(v string) { record("DefaultProjectTrust")(v) },
		OnClearOnShrinkChange:          func(v bool) { record("ClearOnShrink")(v) },
		OnShowTerminalProgressChange:   func(v bool) { record("ShowTerminalProgress")(v) },
		OnTuiModeChange:                func(v string) { record("TuiMode")(v) },
	}
	list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
	for _, item := range list.Items() {
		if item.Submenu != nil || len(item.Values) < 2 {
			continue
		}
		list.SelectItem(item.ID)
		list.HandleInput("\r")
	}
	want := map[string]string{
		"AutoCompact": "false", "AutoResizeImages": "false", "AutocompleteMaxVisible": "7", "BlockImages": "true",
		"ClearOnShrink": "true", "CollapseChangelog": "true", "DefaultProjectTrust": "always", "DoubleEscapeAction": "fork",
		"EditorPaddingX": "1", "EnableInstallTelemetry": "false", "EnableSkillCommands": "false", "FollowUpMode": "all",
		"HideThinkingBlock": "true", "HttpIdleTimeoutMs": "120000", "ImageWidthCells": "80", "ShowCacheMissNotices": "false",
		"ShowHardwareCursor": "true", "ShowImages": "false", "ShowTerminalProgress": "true", "SteeringMode": "all",
		"Transport": "websocket", "TreeFilterMode": "no-tools", "TuiMode": "fullscreen",
	}
	for name, value := range want {
		if got, ok := fired[name]; !ok || got != value {
			t.Errorf("%s callback = %q (fired %v), want %q", name, got, ok, value)
		}
	}
	if len(fired) != len(want) {
		t.Errorf("fired %d callbacks, want %d: %v", len(fired), len(want), fired)
	}
}

// A selection change in the settings list reaches the screen through every container above the selector. A parent container consumes the selector's dirty flag before it renders the selector, so the flag must not take the list's invalidation with it: the selector's own container still has to see the list dirty and render it again.
func TestSettingsSelectorInAContainerRedrawsAfterInput(t *testing.T) {
	initSettingsSelectorTheme(t)
	selector := NewSettingsSelectorComponent(SettingsConfig{AutoCompact: true, ShowImages: true, SteeringMode: "all", FollowUpMode: "all", Transport: "sse", DoubleEscapeAction: "tree", TreeFilterMode: "default", DefaultProjectTrust: "ask", TuiMode: "regular"}, SettingsCallbacks{})
	parent := tui.NewContainer(selector)
	before := slices.Clone(parent.Render(80))
	selector.GetSettingsList().HandleInput("\x1b[B")
	after := slices.Clone(parent.Render(80))
	if slices.Equal(before, after) {
		t.Fatalf("moving the selection left the frame unchanged:\n%q", after)
	}
	if again := parent.Render(80); !slices.Equal(again, after) {
		t.Fatalf("an unchanged selector rendered differently:\n%q\nwant\n%q", again, after)
	}
}
