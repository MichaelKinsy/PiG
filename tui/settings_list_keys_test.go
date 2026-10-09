package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Upstream SettingsList.handleInput acts on up, down, confirm and cancel only. A list with search gives every other key to its
// search Input and refilters, which returns the selection to the first match (settings-list.ts handleInput, applyFilter; probed
// against pi-tui 1.0.4: Down then PageUp renders "(1/15)" with search and "(2/15)" without). A list without search ignores them.
func TestSettingsListPageAndHomeEndKeysFollowSearchState(t *testing.T) {
	items := make([]SettingItem, 15)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("Item %d", i), CurrentValue: "a", Values: []string{"a", "b"}}
	}
	for name, key := range map[string]string{"PageDown": "\x1b[6~", "PageUp": "\x1b[5~", "Home": "\x1b[H", "End": "\x1b[F"} {
		for _, search := range []bool{true, false} {
			sl := NewSettingsList(items, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: search})
			sl.HandleInput("\x1b[B")
			sl.HandleInput(key)
			want := "(2/15)"
			if search {
				want = "(1/15)"
			}
			if screen := stripANSI(strings.Join(sl.Render(80), "\n")); !strings.Contains(screen, want) {
				t.Errorf("%s with search=%v: want %s:\n%s", name, search, want, screen)
			}
		}
	}
}
