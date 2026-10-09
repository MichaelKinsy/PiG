package tui

// pi: packages/coding-agent/src/modes/interactive/components/settings-submenu.ts

// pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts

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
	var finish func(*string, *SubmenuDoneOptions)
	list := NewSettingsList([]SettingItem{{ID: "model-thinking", Label: "Default thinking", CurrentValue: "none", Submenu: func(current string, done func(*string, *SubmenuDoneOptions)) Component {
		if current != "none" {
			t.Fatalf("current = %q", current)
		}
		finish = done
		return NewSelectSubmenu("Models", "Choose", nil, "")
	}}}, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})

	list.HandleInput("Default")
	list.HandleInput("\r")
	if finish == nil {
		t.Fatal("Enter on a submenu-only row was ignored")
	}
	value := "1 configured"
	finish(&value, nil)
	if list.Done() {
		t.Fatal("closing a submenu closed settings")
	}
	if got := strings.Join(list.Render(100), "\n"); !strings.Contains(got, "> Default") || !strings.Contains(got, "1 configured") {
		t.Fatalf("return state = %s", got)
	}
}

// settings-list.ts SettingItem.submenu done(selectedValue, { navigateTo }): closing the submenu with navigateTo moves the cursor to
// that item and opens its submenu (closeSubmenu: selectItem(id); activateItem()). An unknown id leaves the cursor where it is, so
// activateItem reopens the current item's submenu, and an empty navigateTo does not navigate.
func TestSettingsListSubmenuNavigateTo(t *testing.T) {
	var finishFirst func(*string, *SubmenuDoneOptions)
	opened := ""
	list := NewSettingsList([]SettingItem{
		{ID: "first", Label: "First", CurrentValue: "a", Submenu: func(_ string, done func(*string, *SubmenuDoneOptions)) Component {
			finishFirst = done
			opened = "first"
			return NewText("first menu")
		}},
		{ID: "second", Label: "Second", CurrentValue: "b", Submenu: func(_ string, _ func(*string, *SubmenuDoneOptions)) Component {
			opened = "second"
			return NewText("second menu")
		}},
	}, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})

	list.HandleInput("\r")
	if opened != "first" {
		t.Fatalf("Enter opened %q, want first", opened)
	}
	finishFirst(nil, &SubmenuDoneOptions{NavigateTo: "missing"})
	if opened != "first" {
		t.Fatalf("unknown navigateTo opened %q, want the current item's submenu", opened)
	}
	finishFirst(nil, &SubmenuDoneOptions{})
	if got := strings.Join(list.Render(60), "\n"); strings.Contains(got, "menu") {
		t.Fatalf("empty navigateTo left a submenu open:\n%s", got)
	}
	list.HandleInput("\r")
	if opened != "first" {
		t.Fatalf("empty navigateTo navigated to %q", opened)
	}
	list.HandleInput("\r")
	value := "z"
	finishFirst(&value, &SubmenuDoneOptions{NavigateTo: "second"})
	if opened != "second" {
		t.Fatalf("navigateTo second opened %q", opened)
	}
	if got := strings.Join(list.Render(60), "\n"); !strings.Contains(got, "second menu") {
		t.Fatalf("second submenu is not rendered:\n%s", got)
	}
}
