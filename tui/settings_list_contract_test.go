package tui

import (
	"fmt"
	"testing"
)

// Boundary obligations of SettingsList.HandleInput (packages/tui/src/components/settings-list.ts handleInput): the action order
// up, down, selection page keys, confirm or space, cancel, then the search Input, with every action read through the keybinding
// manager and a submenu owning all input while it is open.

func settingsContractItems(n int) []SettingItem {
	items := make([]SettingItem, n)
	for i := range items {
		items[i] = SettingItem{ID: fmt.Sprintf("s%d", i), Label: fmt.Sprintf("Setting %d", i), CurrentValue: "off", Values: []string{"off", "on"}}
	}
	return items
}

// A key bound to both a selection page action and confirm runs the page action (the page branch precedes confirm), and a key
// bound to down and a page action runs down.
func TestSettingsListPageKeysRankBetweenDownAndConfirm(t *testing.T) {
	t.Run("page before confirm", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectPageDown: {"ctrl+s"}, KBSelectConfirm: {"ctrl+s"}})
		list := NewSettingsList(settingsContractItems(12), 5, GetSettingsListTheme(), func(string, string) {}, func() {}, SettingsListOptions{})
		list.HandleInput("\x13")
		if list.cursor != 5 || list.Items()[0].CurrentValue != "off" || list.Items()[5].CurrentValue != "off" {
			t.Fatalf("ctrl+s bound to page down and confirm: cursor %d, values %q %q (the page key must win)", list.cursor, list.Items()[0].CurrentValue, list.Items()[5].CurrentValue)
		}
	})
	t.Run("down before page", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectDown: {"ctrl+s"}, KBSelectPageDown: {"ctrl+s"}})
		list := NewSettingsList(settingsContractItems(12), 5, GetSettingsListTheme(), nil, nil, SettingsListOptions{})
		list.HandleInput("\x13")
		if list.cursor != 1 {
			t.Fatalf("ctrl+s bound to down and page down: cursor %d, want 1 (down must win)", list.cursor)
		}
	})
	t.Run("up before page", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectUp: {"ctrl+s"}, KBSelectPageUp: {"ctrl+s"}})
		list := NewSettingsList(settingsContractItems(12), 5, GetSettingsListTheme(), nil, nil, SettingsListOptions{})
		list.HandleInput("\x13")
		if list.cursor != 11 {
			t.Fatalf("ctrl+s bound to up and page up: cursor %d, want 11 (up must win and wrap)", list.cursor)
		}
	})
}

// The page keys move by one visible page, clamp to both ends without wrapping, and never move an empty list.
func TestSettingsListPageKeysClampAndIgnoreEmptyLists(t *testing.T) {
	list := NewSettingsList(settingsContractItems(12), 5, GetSettingsListTheme(), nil, nil, SettingsListOptions{})
	for i, want := range []int{5, 10, 11} {
		list.HandleInput("\x1b[6~")
		if list.cursor != want {
			t.Fatalf("page down %d: cursor %d, want %d", i+1, list.cursor, want)
		}
	}
	for i, want := range []int{6, 1, 0, 0} {
		list.HandleInput("\x1b[5~")
		if list.cursor != want {
			t.Fatalf("page up %d: cursor %d, want %d", i+1, list.cursor, want)
		}
	}
	empty := NewSettingsList(nil, 5, GetSettingsListTheme(), nil, nil, SettingsListOptions{})
	empty.HandleInput("\x1b[6~")
	empty.HandleInput("\x1b[5~")
	if empty.cursor != 0 || empty.Done() {
		t.Fatalf("page keys moved an empty list: cursor %d done %v", empty.cursor, empty.Done())
	}
}

// With search on, a remapped cancel key cancels and the default Escape stops cancelling: it goes to the search Input like any
// other key.
func TestSettingsListCancelFollowsTheKeybindingManager(t *testing.T) {
	withBindings(t, map[string][]string{KBSelectCancel: {"ctrl+s"}})
	cancels := 0
	list := NewSettingsList(settingsContractItems(3), 10, GetSettingsListTheme(), func(string, string) {}, func() { cancels++ }, SettingsListOptions{EnableSearch: true})
	list.HandleInput("\x1b")
	if cancels != 0 {
		t.Fatalf("the default Escape cancelled after cancel was remapped to ctrl+s")
	}
	list.HandleInput("\x13")
	if cancels != 1 {
		t.Fatalf("ctrl+s bound to cancel fired onCancel %d times, want 1", cancels)
	}
}

// Up and down act on the filtered rows: a single match neither wraps nor moves, and after the filter empties both keys are
// ignored.
func TestSettingsListNavigationActsOnTheFilteredRows(t *testing.T) {
	list := NewSettingsList(settingsContractItems(12), 5, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	for _, key := range []string{"1", "1"} {
		list.HandleInput(key)
	}
	if len(list.filtered) != 1 {
		t.Fatalf("query 11 left %d rows, want 1", len(list.filtered))
	}
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[A")
	if list.cursor != 0 {
		t.Fatalf("a single filtered row moved to %d", list.cursor)
	}
	list.HandleInput("z")
	if len(list.filtered) != 0 {
		t.Fatalf("query 11z left %d rows, want 0", len(list.filtered))
	}
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[A")
	list.HandleInput("\x1b[6~")
	list.HandleInput("\r")
	if list.cursor != 0 || list.Done() {
		t.Fatalf("an empty filtered list reacted: cursor %d done %v", list.cursor, list.Done())
	}
}

// While a submenu is open it owns every key, including up, down and the page keys; a submenu without an input handler swallows
// them. After it closes the search text and the selection survive and the list handles keys again.
func TestSettingsListSubmenuOwnsInputAndTheListStateSurvivesIt(t *testing.T) {
	var done func(*string, *SubmenuDoneOptions)
	items := settingsContractItems(3)
	items[1].Submenu = func(_ string, d func(*string, *SubmenuDoneOptions)) Component {
		done = d
		return NewText("plain")
	}
	list := NewSettingsList(items, 10, GetSettingsListTheme(), func(string, string) {}, func() {}, SettingsListOptions{EnableSearch: true})
	list.HandleInput("S")
	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	if done == nil || list.cursor != 1 {
		t.Fatalf("enter did not open the submenu of the second row: cursor %d", list.cursor)
	}
	for _, key := range []string{"\x1b[B", "\x1b[A", "\x1b[6~", "x", "\r"} {
		list.HandleInput(key)
	}
	if list.cursor != 1 || list.searchInput.GetValue() != "S" || list.Done() {
		t.Fatalf("an inert submenu let the list act: cursor %d search %q", list.cursor, list.searchInput.GetValue())
	}
	done(nil, nil)
	if list.cursor != 1 || list.searchInput.GetValue() != "S" {
		t.Fatalf("closing the submenu changed the list: cursor %d search %q", list.cursor, list.searchInput.GetValue())
	}
	list.HandleInput("\x1b[B")
	if list.cursor != 2 {
		t.Fatalf("down after the submenu closed went to %d, want 2", list.cursor)
	}
}
