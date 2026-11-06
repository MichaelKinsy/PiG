package tui

import (
	"reflect"
	"testing"
)

// SelectSubmenuComponent.HandleInput (settings-submenu.ts SelectSubmenu.handleInput): the selection page keys read through the keybinding
// manager go to the select list of a searchable submenu instead of its search Input, and report the moved selection once; an empty
// or single-item list never reports a page move.
func TestSelectSubmenuPageKeysFollowTheKeybindingManager(t *testing.T) {
	withBindings(t, map[string][]string{KBSelectPageDown: {"ctrl+v"}, KBSelectPageUp: {"alt+v"}})
	items := make([]SelectItem, 25)
	for i := range items {
		items[i] = SelectItem{Value: string(rune('a' + i)), Label: "Item " + string(rune('a'+i))}
	}
	var seen []string
	menu := NewSelectSubmenu("Pick", "", items, "a", SelectSubmenuOptions{Searchable: true})
	menu.OnSelectionChange = func(value string) { seen = append(seen, value) }
	menu.HandleInput("\x16")
	menu.HandleInput("\x16")
	menu.HandleInput("\x16")
	menu.HandleInput("\x1bv")
	if want := []string{"k", "u", "y", "o"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("page moves reported %v, want %v", seen, want)
	}
	if menu.searchInput.GetValue() != "" {
		t.Fatalf("a page key edited the search: %q", menu.searchInput.GetValue())
	}
	// The default PageDown is no longer a page key: it edits nothing here and moves nothing.
	before := len(seen)
	menu.HandleInput("\x1b[6~")
	if len(seen) != before {
		t.Fatalf("the unbound default page key moved the selection: %v", seen)
	}
	single := NewSelectSubmenu("Pick", "", items[:1], "a", SelectSubmenuOptions{Searchable: true})
	var moves []string
	single.OnSelectionChange = func(value string) { moves = append(moves, value) }
	single.HandleInput("\x16")
	single.HandleInput("\x1bv")
	if len(moves) != 0 {
		t.Fatalf("a single item list reported page moves %v", moves)
	}
}
