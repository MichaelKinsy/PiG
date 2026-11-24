package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Upstream SettingsList.handleInput acts on up, down, confirm and cancel only. A list with search gives every other key to its
// search Input, which refilters only when the query changed (Pi 5bcf370a5, #12334), so Home and End keep the selection with search
// as without it: "(2/15)" after Down. A list without search ignores them.
func TestSettingsListPageAndHomeEndKeysFollowSearchState(t *testing.T) {
	items := make([]SettingItem, 15)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("Item %d", i), CurrentValue: "a", Values: []string{"a", "b"}}
	}
	// Home and End are not selection keys in settings-list.ts; the page keys are covered by TestSettingsListPageKeysMoveByAPage.
	for name, key := range map[string]string{"Home": "\x1b[H", "End": "\x1b[F"} {
		for _, search := range []bool{true, false} {
			sl := NewSettingsList(items, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: search})
			sl.HandleInput("\x1b[B")
			sl.HandleInput(key)
			want := "(2/15)"
			if screen := stripANSI(strings.Join(sl.Render(80), "\n")); !strings.Contains(screen, want) {
				t.Errorf("%s with search=%v: want %s:\n%s", name, search, want, screen)
			}
		}
	}
}
