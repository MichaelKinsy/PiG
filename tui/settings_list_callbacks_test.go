package tui

import (
	"slices"
	"strings"
	"testing"
)

// packages/tui/src/components/settings-list.ts:215-262: a cycled value and a value a submenu returns reach onChange after the row shows it; Esc on the main list reaches onCancel.
func TestSettingsListCallbacksRunLikeUpstreamOnChangeAndOnCancel(t *testing.T) {
	var finish func(*string, *SubmenuDoneOptions)
	var changes []string
	cancels := 0
	list := NewSettingsList([]SettingItem{
		{ID: "mode", Label: "Mode", CurrentValue: "a", Values: []string{"a", "b"}},
		{ID: "menu", Label: "Menu", CurrentValue: "none", Submenu: func(_ string, done func(*string, *SubmenuDoneOptions)) Component {
			finish = done
			return NewSelectSubmenu("Menu", "", nil, "")
		}},
	}, 10, GetSettingsListTheme(), func(id, value string) { changes = append(changes, id+"="+value) }, func() { cancels++ }, SettingsListOptions{EnableSearch: true})

	list.HandleInput("\r")
	if list.Done() || list.ChangedID != "" {
		t.Fatalf("a callback list also recorded the change for a caller loop: done=%v id=%q", list.Done(), list.ChangedID)
	}
	list.SelectItem("menu")
	list.HandleInput("\r")
	value := "2 configured"
	finish(&value, nil)
	finish = nil
	list.HandleInput("\x1b")
	if want := []string{"mode=b", "menu=2 configured"}; !slices.Equal(changes, want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	if cancels != 1 || list.Cancelled() || list.Done() {
		t.Fatalf("cancels=%d cancelled=%v done=%v", cancels, list.Cancelled(), list.Done())
	}
}

// settings-list.ts:84-91: selectItem moves the cursor to a displayed item and ignores any other id.
func TestSettingsListSelectItemMovesTheCursorOnlyToDisplayedItems(t *testing.T) {
	list := NewSettingsList([]SettingItem{
		{ID: "one", Label: "One", CurrentValue: "x", Values: []string{"x", "y"}},
		{ID: "two", Label: "Two", CurrentValue: "x", Values: []string{"x", "y"}},
	}, 10, GetSettingsListTheme(), func(string, string) {}, func() {}, SettingsListOptions{EnableSearch: true})

	list.SelectItem("two")
	if got := stripANSI(strings.Join(list.Render(80), "\n")); !strings.Contains(got, "→ Two") {
		t.Fatalf("selectItem(two): %s", got)
	}
	list.SelectItem("absent")
	if got := stripANSI(strings.Join(list.Render(80), "\n")); !strings.Contains(got, "→ Two") {
		t.Fatalf("selectItem(absent) moved the cursor: %s", got)
	}
}

// settings-list.ts:55-70: the constructor takes the rows, maxVisible, the theme, onChange, onCancel and the options in that order;
// enableSearch defaults to false (settings-list.ts:34-36, :69).
func TestNewSettingsListTakesPisConstructorArguments(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Alpha", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "b", Label: "Beta", CurrentValue: "x", Values: []string{"x", "y"}},
		{ID: "c", Label: "Gamma", CurrentValue: "p", Values: []string{"p", "q"}},
	}
	theme := GetSettingsListTheme()
	theme.Label = func(text string, _ bool) string { return "<" + text + ">" }
	var changes []string
	cancels := 0
	onChange := func(id, value string) { changes = append(changes, id+"="+value) }
	onCancel := func() { cancels++ }

	list := NewSettingsList(items, 2, theme, onChange, onCancel, SettingsListOptions{})
	rendered := strings.Join(list.Render(80), "\n")
	if !strings.Contains(rendered, "<Alpha") || !strings.Contains(rendered, "<Beta") {
		t.Fatalf("the injected theme did not style the rows:\n%s", rendered)
	}
	if strings.Contains(rendered, "Gamma") {
		t.Fatalf("maxVisible 2 showed a third row:\n%s", rendered)
	}
	list.HandleInput("\r")
	list.HandleInput("\x1b")
	if !slices.Equal(changes, []string{"a=off"}) || cancels != 1 {
		t.Fatalf("changes=%v cancels=%d, want [a=off] and 1", changes, cancels)
	}
	list.HandleInput("g")
	if after := strings.Join(list.Render(80), "\n"); !strings.Contains(after, "<Alpha") {
		t.Fatalf("typing filtered a list built without enableSearch:\n%s", after)
	}

	searching := NewSettingsList(items, 10, theme, onChange, onCancel, SettingsListOptions{EnableSearch: true})
	searching.HandleInput("g")
	if after := strings.Join(searching.Render(80), "\n"); strings.Contains(after, "Alpha") || !strings.Contains(after, "<Gamma") {
		t.Fatalf("enableSearch did not filter to the matching row:\n%s", after)
	}
}
