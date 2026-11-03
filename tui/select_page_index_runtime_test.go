package tui

import (
	"testing"
)

// tui.ts routes input to the focused component after the viewport keys; keybindings.ts getSelectPageIndex decides how far a
// tui.select.pageUp or tui.select.pageDown key moves a selection. This drives the focused selector through the alt-screen
// runtime input path (HandleViewportInput, then the focused component), as the interactive mode delivers a terminal key, and
// checks every step against GetSelectPageIndex and against the page arithmetic written out.
func TestGetSelectPageIndexMovesAFocusedSelectorThroughTheRuntimeInputPath(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 30, 10, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	list := NewSelectList(pageTestItems(23), 5, SelectListTheme{})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, list, new(7)))
	h.tui.SetFocus(list)
	h.start()
	top := transcript.ScrollTop()

	kb := GetTUIKeybindings()
	steps := []struct {
		key  string
		want int
	}{
		{testPageDownKey, 5}, {testPageDownKey, 10}, {testPageDownKey, 15}, {testPageDownKey, 20},
		{testPageDownKey, 22}, // clamped to the last item, never wrapped
		{testPageDownKey, 22},
		{testPageUpKey, 17}, {testPageUpKey, 12}, {testPageUpKey, 7}, {testPageUpKey, 2},
		{testPageUpKey, 0}, // clamped to the first item
		{testPageUpKey, 0},
		{"x", 0}, // a key that is not a page key leaves the selection where it was
	}
	for _, step := range steps {
		before := list.SelectedIndex()
		expected, isPage := GetSelectPageIndex(kb, step.key, before, 23, 5)
		h.send(step.key)
		if got := list.SelectedIndex(); got != step.want {
			t.Fatalf("after %q from %d the selection is %d, want %d", step.key, before, got, step.want)
		}
		if isPage && expected != step.want {
			t.Fatalf("GetSelectPageIndex(%q, %d, 23, 5) = %d, want %d", step.key, before, expected, step.want)
		}
		if !isPage && step.key != "x" {
			t.Fatalf("GetSelectPageIndex did not recognise page key %q", step.key)
		}
	}
	if transcript.ScrollTop() != top {
		t.Fatalf("the transcript scrolled from %d to %d while a focused list took the page keys", top, transcript.ScrollTop())
	}
}

// getSelectPageIndex reads the registry when a key arrives: after a keybindings reload that moves the page keys, the new keys page
// the focused selector and the old ones do not.
func TestGetSelectPageIndexFollowsRemappedPageKeysThroughTheRuntimeInputPath(t *testing.T) {
	useAltScreenBindings(t, map[string][]string{"tui.select.pageUp": {"alt+v"}, "tui.select.pageDown": {"ctrl+v"}})
	h := newAltHarness(t, 30, 10, TuiAltScreenOptions{})
	transcript := NewScrollView(NewText(numberedLines(40)), ScrollViewOptions{Follow: "end", Primary: true})
	list := NewSelectList(pageTestItems(12), 5, SelectListTheme{})
	h.tui.SetLayoutRoot(transcriptOverDock(transcript, list, new(7)))
	h.tui.SetFocus(list)
	h.start()

	kb := GetTUIKeybindings()
	if index, ok := GetSelectPageIndex(kb, "\x16", 0, 12, 5); !ok || index != 5 {
		t.Fatalf("GetSelectPageIndex(ctrl+v) = %d, %v, want 5, true", index, ok)
	}
	if index, ok := GetSelectPageIndex(kb, testPageDownKey, 0, 12, 5); ok || index != 0 {
		t.Fatalf("GetSelectPageIndex(PageDown) = %d, %v, want 0, false once PageDown is not a selection page key", index, ok)
	}
	for _, step := range []struct {
		key  string
		want int
	}{{"\x16", 5}, {"\x16", 10}, {"\x16", 11}, {"\x1bv", 6}} {
		h.send(step.key)
		if got := list.SelectedIndex(); got != step.want {
			t.Fatalf("after %q the selection is %d, want %d", step.key, got, step.want)
		}
	}
}
