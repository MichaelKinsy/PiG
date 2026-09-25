package tui

import (
	"strings"
	"testing"
)

func TestSelectSubmenuSearchMatchesDescriptionsAndResetsSelection(t *testing.T) {
	items := []SelectItem{{Value: "one", Label: "First", Description: "Deep reasoning"}, {Value: "two", Label: "Second", Description: "Brief reasoning"}}
	menu := NewSelectSubmenu("Models", "Choose a model", items, "two", SelectSubmenuOptions{Searchable: true, MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 46})
	menu.HandleInput("dp rs")
	if got := menu.CurrentValue(); got != "one" {
		t.Fatalf("fuzzy description match = %q", got)
	}
	menu.HandleInput("\x15")
	if got := menu.CurrentValue(); got != "one" {
		t.Fatalf("clearing search did not reset to first row: %q", got)
	}
	menu.HandleInput("\x1b[A")
	if got := menu.CurrentValue(); got != "two" {
		t.Fatalf("up did not wrap: %q", got)
	}
	if got := strings.Join(menu.Render(100), "\n"); !strings.Contains(got, "> ") || !strings.Contains(got, "Type to filter · Enter to select · Esc to go back") {
		t.Fatalf("search UI = %s", got)
	}
	menu.HandleInput("no-match")
	menu.HandleInput("\r")
	if menu.Done() {
		t.Fatal("empty search confirmed a selection")
	}
	menu.HandleInput("\x1b")
	if !menu.Cancelled() {
		t.Fatal("empty search did not cancel")
	}
}

func TestSettingsListSubmenuReturnsWithoutLosingFilter(t *testing.T) {
	var finish func(*string)
	list := NewSettingsList([]SettingItem{{ID: "model-thinking", Label: "Default thinking", CurrentValue: "none", Submenu: func(current string, done func(*string)) Component {
		if current != "none" {
			t.Fatalf("current = %q", current)
		}
		finish = done
		return NewSelectSubmenu("Models", "Choose", nil, "")
	}}})
	list.HandleInput("Default")
	list.HandleInput("\r")
	if finish == nil {
		t.Fatal("Enter on a submenu-only row was ignored")
	}
	value := "1 configured"
	finish(&value)
	if list.Done() {
		t.Fatal("closing a submenu closed settings")
	}
	if got := strings.Join(list.Render(100), "\n"); !strings.Contains(got, "> Default") || !strings.Contains(got, "1 configured") {
		t.Fatalf("return state = %s", got)
	}
}
