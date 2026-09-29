package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestUpstreamSettingsList(t *testing.T) {
	type change struct{ id, value string }
	newList := func() *SettingsList {
		return NewSettingsListWithOptions([]SettingItem{{ID: "tui-mode", Label: "TUI mode", CurrentValue: "regular", Values: []string{"regular", "fullscreen"}}}, 10, true)
	}
	// The Go caller consumes ChangedID/ChangedValue and Reset after each input, instead of receiving the upstream onChange callback.
	input := func(list *SettingsList, data string, changes *[]change) {
		list.HandleInput(data)
		if list.ChangedID != "" {
			*changes = append(*changes, change{list.ChangedID, list.ChangedValue})
			list.Reset()
		}
	}
	// .upstream/v0.87.1/packages/tui/test/settings-list.test.ts:23
	t.Run("includes spaces in an active search instead of changing the selected setting", func(t *testing.T) {
		list := newList()
		var changes []change
		for _, r := range "TUI mode" {
			input(list, string(r), &changes)
		}
		if len(changes) != 0 {
			t.Fatalf("changes = %v, want none", changes)
		}
		lines := list.Render(80)
		if len(lines) == 0 || !strings.Contains(lines[0], "TUI mode") {
			t.Fatalf("search row = %q", lines)
		}
		input(list, "\r", &changes)
		if !slices.Equal(changes, []change{{"tui-mode", "fullscreen"}}) {
			t.Fatal(changes)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/settings-list.test.ts:43
	t.Run("keeps Space as a change shortcut before a search query is entered", func(t *testing.T) {
		list := newList()
		var changes []change
		input(list, " ", &changes)
		if !slices.Equal(changes, []change{{"tui-mode", "fullscreen"}}) {
			t.Fatal(changes)
		}
	})
}
