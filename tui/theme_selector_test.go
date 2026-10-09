package tui

// pi: packages/coding-agent/src/modes/interactive/components/theme-selector.ts

import (
	"slices"
	"strings"
	"testing"
)

func themeSelectorWith(t *testing.T, current string) (*ThemeSelectorComponent, *[]string, *[]string, *int) {
	t.Helper()
	var selected, previewed []string
	cancels := 0
	c := NewThemeSelectorComponent(current, func(n string) { selected = append(selected, n) }, func() { cancels++ }, func(n string) { previewed = append(previewed, n) })
	return c, &selected, &previewed, &cancels
}

// packages/coding-agent/src/modes/interactive/components/theme-selector.ts:64 getSelectList returns the child list. The list holds every available theme, marks the current one, and starts on it.
func TestThemeSelectorComponentListsThemesAndPreselectsCurrent(t *testing.T) {
	names := ActiveThemeRegistry().Names()
	if len(names) < 2 {
		t.Fatalf("need two themes, have %v", names)
	}
	current := names[len(names)-1]
	c, selected, _, _ := themeSelectorWith(t, current)
	list := c.GetSelectList()
	item, ok := list.SelectedItem()
	if !ok || item.Value != current || item.Description != "(current)" {
		t.Fatalf("selected = %+v %v, want %q marked (current)", item, ok, current)
	}
	if got := len(c.Children()); got != 3 {
		t.Fatalf("children = %d, want border, list, border", got)
	}
	if c.Children()[1] != Component(list) {
		t.Fatal("GetSelectList is not the child list")
	}
	plain := stripANSI(strings.Join(c.Render(80), "\n"))
	if strings.Count(plain, "(current)") != 1 {
		t.Fatalf("exactly one row is marked current:\n%s", plain)
	}
	c.HandleInput("\r")
	if !slices.Equal(*selected, []string{current}) {
		t.Fatalf("confirm selected %v, want [%s]", *selected, current)
	}

	unknown, _, _, _ := themeSelectorWith(t, "no-such-theme")
	if item, _ := unknown.GetSelectList().SelectedItem(); item.Value != names[0] {
		t.Fatalf("an unknown current theme selected %q, want the first %q", item.Value, names[0])
	}
}

// theme-selector.ts: moving the selection previews the theme under the cursor; cancel calls onCancel; the callbacks are
// optional.
func TestThemeSelectorComponentPreviewAndCancel(t *testing.T) {
	names := ActiveThemeRegistry().Names()
	c, selected, previewed, cancels := themeSelectorWith(t, names[0])
	c.HandleInput("\x1b[B")
	if !slices.Equal(*previewed, []string{names[1]}) {
		t.Fatalf("previewed %v, want [%s]", *previewed, names[1])
	}
	if len(*selected) != 0 {
		t.Fatalf("moving selected %v", *selected)
	}
	c.HandleInput("\x1b")
	if *cancels != 1 {
		t.Fatalf("cancel called %d times", *cancels)
	}

	bare := NewThemeSelectorComponent(names[0], nil, nil, nil)
	bare.HandleInput("\x1b[B")
	bare.HandleInput("\r")
	bare.HandleInput("\x1b")
}

// theme.ts getSelectListTheme: selected text is accent, description, scroll info and no-match are muted.
func TestGetSelectListThemeUsesAccentAndMutedColors(t *testing.T) {
	theme := GetSelectListTheme()
	active := ActiveTheme()
	for name, tc := range map[string]struct {
		got  func(string) string
		role string
	}{
		"selectedPrefix": {theme.SelectedPrefix, "accent"}, "selectedText": {theme.SelectedText, "accent"},
		"description": {theme.Description, "muted"}, "scrollInfo": {theme.ScrollInfo, "muted"}, "noMatch": {theme.NoMatch, "muted"},
	} {
		if got, want := tc.got("x"), active.Fg(tc.role, "x"); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
