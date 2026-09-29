package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Upstream SettingsList.handleInput acts on up, down, confirm and cancel only
// and gives every other key to its search input (settings-list.ts:222-250), so
// PageUp, PageDown, Home and End leave the selection where it is.
func TestSettingsListPageAndHomeEndKeysKeepSelection(t *testing.T) {
	items := make([]SettingItem, 15)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("Item %d", i), CurrentValue: "a", Values: []string{"a", "b"}}
	}
	for name, key := range map[string]string{"PageDown": "\x1b[6~", "PageUp": "\x1b[5~", "Home": "\x1b[H", "End": "\x1b[F"} {
		sl := NewSettingsList(items)
		sl.HandleInput("\x1b[B")
		sl.HandleInput(key)
		if screen := stripANSI(strings.Join(sl.Render(80), "\n")); !strings.Contains(screen, "(2/15)") {
			t.Errorf("%s moved the selection:\n%s", name, screen)
		}
	}
}
