package parity

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func renderSettingsListToTUI(t *testing.T, rows, cols int, sl *tui.SettingsList) func(*bytes.Buffer) {
	t.Helper()
	return func(w *bytes.Buffer) {
		ti := tui.NewWithOutput(w, cols, rows)
		ti.Add(sl)
		ti.Render()
	}
}

func testSettingItems() []tui.SettingItem {
	return []tui.SettingItem{
		{ID: "auto-compact", Label: "Auto-compact", Description: "Automatically compact context when it gets too large", CurrentValue: "false", Values: []string{"false", "true"}},
		{ID: "show-images", Label: "Show images", Description: "Render inline images in capable terminals", CurrentValue: "true", Values: []string{"true", "false"}},
		{ID: "theme", Label: "Theme", Description: "Color theme: auto, dark, or light", CurrentValue: "auto", Values: []string{"auto", "dark", "light"}},
		{ID: "thinking", Label: "Thinking", Description: "Adjust the reasoning level for the current session", CurrentValue: "off", Values: []string{"off", "low", "medium"}},
		{ID: "transport", Label: "Transport", Description: "HTTP transport protocol for LLM streaming", CurrentValue: "sse", Values: []string{"sse", "websocket", "websocket-cached", "auto"}},
	}
}

func TestParitySettingsList_GoldenVisibleState(t *testing.T) {
	sl := tui.NewSettingsList(testSettingItems())
	AssertGolden(t, "settings-list-visible", 14, 54, renderSettingsListToTUI(t, 14, 54, sl))
}

func TestParitySettingsList_GoldenFilteredState(t *testing.T) {
	sl := tui.NewSettingsList(testSettingItems())
	for _, ch := range "theme" {
		sl.HandleInput(string(ch))
	}
	AssertGolden(t, "settings-list-filtered-theme", 10, 54, renderSettingsListToTUI(t, 10, 54, sl))
}
