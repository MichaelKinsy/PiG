package tui

import (
	"fmt"
	"slices"
	"testing"
)

// SelectList.render uses the muted foreground without row padding for both
// noMatch and scrollInfo; settings-submenu.ts embeds those rows unchanged.
func TestSelectSubmenuEmptyAndScrollingRowsMatchUpstream(t *testing.T) {
	muted := ActiveTheme().Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}
	t.Run("empty search", func(t *testing.T) {
		menu := NewSelectSubmenu("Models", "", []SelectItem{{Value: "one", Label: "One"}}, "", SelectSubmenuOptions{Searchable: true})
		menu.HandleInput("absent")
		want := muted + "  No matching commands\x1b[39m"
		if rows := menu.Render(80); !slices.Contains(rows, want) {
			t.Fatalf("no-match row must be unpadded muted foreground %q; got %q", want, rows)
		}
	})
	t.Run("scrolling catalog", func(t *testing.T) {
		items := make([]SelectItem, 12)
		for i := range items {
			items[i] = SelectItem{Value: fmt.Sprint(i), Label: fmt.Sprintf("model-%d", i)}
		}
		menu := NewSelectSubmenu("Models", "", items, "")
		want := muted + "  (1/12)\x1b[39m"
		if rows := menu.Render(80); !slices.Contains(rows, want) {
			t.Fatalf("scroll row must be unpadded muted foreground %q; got %q", want, rows)
		}
	})
}
