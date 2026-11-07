package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var plainSelectTheme = SelectListTheme{}

func selectListColumn(t *testing.T, line, text string) int {
	t.Helper()
	index := strings.Index(line, text)
	if index < 0 {
		t.Fatalf("%q not in %q", text, line)
	}
	return widthx.VisibleWidth(line[:index])
}

// upstream: packages/tui/test/select-list.test.ts
func TestSelectListUpstream(t *testing.T) {
	long := "very-long-command-name-that-needs-truncation"
	t.Run("normalizes multiline descriptions to single line", func(t *testing.T) {
		list := NewSelectList([]SelectItem{{Value: "test", Label: "test", Description: "Line one\nLine two\nLine three"}}, 5, plainSelectTheme)
		rendered := list.Render(100)
		if len(rendered) == 0 || strings.Contains(rendered[0], "\n") || !strings.Contains(rendered[0], "Line one Line two Line three") {
			t.Fatalf("rendered = %q", rendered)
		}
	})
	t.Run("keeps descriptions aligned when the primary text is truncated", func(t *testing.T) {
		list := NewSelectList([]SelectItem{{Value: "short", Label: "short", Description: "short description"}, {Value: long, Label: long, Description: "long description"}}, 5, plainSelectTheme)
		rendered := list.Render(80)
		if a, b := selectListColumn(t, rendered[0], "short description"), selectListColumn(t, rendered[1], "long description"); a != b {
			t.Fatalf("description columns %d and %d differ", a, b)
		}
	})
	t.Run("uses the configured minimum primary column width", func(t *testing.T) {
		list := NewSelectList([]SelectItem{{Value: "a", Label: "a", Description: "first"}, {Value: "bb", Label: "bb", Description: "second"}}, 5, plainSelectTheme, SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 20})
		rendered := list.Render(80)
		if selectListColumn(t, rendered[0], "first") != 14 || selectListColumn(t, rendered[1], "second") != 14 {
			t.Fatalf("rendered = %q", rendered)
		}
	})
	t.Run("uses the configured maximum primary column width", func(t *testing.T) {
		list := NewSelectList([]SelectItem{{Value: long, Label: long, Description: "first"}, {Value: "short", Label: "short", Description: "second"}}, 5, plainSelectTheme, SelectListLayoutOptions{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 20})
		rendered := list.Render(80)
		if selectListColumn(t, rendered[0], "first") != 22 || selectListColumn(t, rendered[1], "second") != 22 {
			t.Fatalf("rendered = %q", rendered)
		}
	})
	t.Run("allows overriding primary truncation while preserving description alignment", func(t *testing.T) {
		list := NewSelectList([]SelectItem{{Value: long, Label: long, Description: "first"}, {Value: "short", Label: "short", Description: "second"}}, 5, plainSelectTheme, SelectListLayoutOptions{
			MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 12,
			TruncatePrimary: func(c SelectListTruncatePrimaryContext) string {
				if len(c.Text) <= c.MaxWidth {
					return c.Text
				}
				return c.Text[:max(0, c.MaxWidth-1)] + "…"
			},
		})
		rendered := list.Render(80)
		if !strings.Contains(rendered[0], "…") || selectListColumn(t, rendered[0], "first") != selectListColumn(t, rendered[1], "second") {
			t.Fatalf("rendered = %q", rendered)
		}
	})
}

// mutation-checked: zeroing the results of SelectList.SelectedItem fails it
// Pi: packages/tui/src/components/editor.ts:769 (getSelectedItem)
// packages/tui/src/components/select-list.ts:269 `getSelectedItem()` returns the selected item, or null when the filter leaves none.
// packages/tui/src/components/select-list.ts:269 (SelectList.getSelectedItem): the selected item follows the cursor and the filter.
func TestSelectListBehavior(t *testing.T) {
	items := []SelectItem{{Value: "alpha", Label: "alpha"}, {Value: "Alps", Label: "Alps"}, {Value: "beta", Label: "beta"}, {Value: "gamma", Label: "gamma"}}
	var selected, changed []string
	cancelled := 0
	list := NewSelectList(items, 2, SelectListTheme{NoMatch: func(s string) string { return "<" + s + ">" }})
	list.OnSelect = func(item SelectItem) { selected = append(selected, item.Value) }
	list.OnSelectionChange = func(item SelectItem) { changed = append(changed, item.Value) }
	list.OnCancel = func() { cancelled++ }

	// Arrow keys wrap at both ends and report each change; Enter and Escape reach their callbacks.
	list.HandleInput("\x1b[A")
	list.HandleInput("\x1b[B")
	list.HandleInput("\r")
	list.HandleInput("\x1b")
	if strings.Join(changed, ",") != "gamma,alpha" || strings.Join(selected, ",") != "alpha" || cancelled != 1 {
		t.Fatalf("changed=%v selected=%v cancelled=%d", changed, selected, cancelled)
	}

	// A scrolling list shows a (selected/total) line and centers the window on the selection.
	list.SetSelectedIndex(2)
	rendered := list.Render(40)
	if len(rendered) != 3 || !strings.Contains(rendered[0], "Alps") || !strings.Contains(rendered[1], "→ beta") || strings.TrimSpace(rendered[2]) != "(3/4)" {
		t.Fatalf("rendered = %q", rendered)
	}
	list.SetSelectedIndex(99)
	if item, _ := list.SelectedItem(); item.Value != "gamma" {
		t.Fatalf("an out-of-range index must clamp to the last item, got %q", item.Value)
	}

	// The filter is a case-insensitive value prefix, resets the selection and a miss shows the no-match line.
	list.SetFilter("AL")
	if item, ok := list.SelectedItem(); !ok || item.Value != "alpha" || len(list.filteredItems) != 2 {
		t.Fatalf("filtered = %+v", list.filteredItems)
	}
	list.SetFilter("zzz")
	if got := list.Render(40); len(got) != 1 || got[0] != "<  No matching commands>" {
		t.Fatalf("no match rendered %q", got)
	}
	list.HandleInput("\r") // nothing selected: no callback
	if len(selected) != 1 {
		t.Fatalf("confirming an empty list selected %v", selected)
	}
}

// upstream: packages/tui/test/mouse-components.test.ts:96 (a press and a click select and activate a row) and
// mouse-components.test.ts:140 (hover is ignored, and the wheel moves getSelectedItem).
func TestSelectListMouse(t *testing.T) {
	items := []SelectItem{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}, {Value: "c", Label: "c"}}
	list := NewSelectList(items, 3, SelectListTheme{})
	var picked string
	list.OnSelect = func(item SelectItem) { picked = item.Value }
	press := list.HandleMouse(TuiMouseEvent{Type: MousePress, Button: MouseButtonLeft, Y: 2})
	if press == nil || !press.Handled || !press.Focus {
		t.Fatalf("press = %+v", press)
	}
	// A click confirms the row that was pressed, even when the pointer moved before release.
	if click := list.HandleMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, Y: 0}); click == nil || !click.Handled || picked != "c" {
		t.Fatalf("click = %+v picked=%q", click, picked)
	}
	// Hover never changes the selection; a row outside the window is not handled.
	if list.HandleMouse(TuiMouseEvent{Type: MouseMove, Y: 0}) != nil {
		t.Fatal("hover must not be handled")
	}
	if list.HandleMouse(TuiMouseEvent{Type: MousePress, Button: MouseButtonLeft, Y: 9}) != nil {
		t.Fatal("a press outside the rows must not be handled")
	}
	wheel := list.HandleMouse(TuiMouseEvent{Type: MouseWheel, WheelDelta: -1})
	if item, _ := list.SelectedItem(); wheel == nil || !wheel.Handled || wheel.Render == nil || !*wheel.Render || item.Value != "b" {
		t.Fatalf("wheel = %+v selected=%q", wheel, item.Value)
	}
}

// upstream: packages/tui/src/components/select-list.ts:19 declares selectedPrefix on SelectListTheme and renderItem (select-list.ts:193)
// never calls it: the selected row's "→ " prefix is styled only through selectedText.
func TestSelectListThemeSelectedPrefixIsNeverApplied(t *testing.T) {
	prefixCalls := 0
	theme := SelectListTheme{
		SelectedPrefix: func(text string) string { prefixCalls++; return "<P>" + text },
		SelectedText:   func(text string) string { return "<S>" + text },
	}
	list := NewSelectList([]SelectItem{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}}, 5, theme)
	rendered := list.Render(40)
	if prefixCalls != 0 {
		t.Fatalf("SelectedPrefix was called %d times; upstream never applies it", prefixCalls)
	}
	if strings.Contains(strings.Join(rendered, "\n"), "<P>") || !strings.HasPrefix(rendered[0], "<S>→ a") {
		t.Fatalf("rendered = %q", rendered)
	}
}
