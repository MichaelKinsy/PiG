package tui

// pi: packages/coding-agent/src/modes/interactive/components/show-images-selector.ts

import (
	"strings"
	"testing"
)

func TestShowImagesSelector_Render(t *testing.T) {
	sel := NewShowImagesSelectorComponent(true, nil, nil)
	joined := stripANSI(strings.Join(sel.Render(80), "\n"))
	if strings.Contains(joined, "filter:") {
		t.Fatalf("non-searchable selector rendered a filter input:\n%s", joined)
	}
	if !strings.Contains(joined, "Yes         Show images inline in terminal") {
		t.Fatalf("render missing yes option and description column:\n%s", joined)
	}
	if !strings.Contains(joined, "No          Show text placeholder instead") {
		t.Fatalf("render missing no option and muted description column:\n%s", joined)
	}
}

// packages/coding-agent/src/modes/interactive/components/show-images-selector.ts:47 `getSelectList()` returns the list the
// selector shows, preselected on the current value, and its selection decides what onSelect receives (:26-28).
func TestShowImagesSelector_GetSelectListIsTheSelectionOwner(t *testing.T) {
	var shown []bool
	sel := NewShowImagesSelectorComponent(false, func(show bool) { shown = append(shown, show) }, nil)
	item, ok := sel.GetSelectList().SelectedItem()
	if !ok || item.Value != "no" {
		t.Fatalf("pre-selected item = %+v, %v; want no", item, ok)
	}
	if sel.Children()[1] != Component(sel.GetSelectList()) {
		t.Fatal("GetSelectList is not the child list")
	}
	sel.GetSelectList().SetSelectedIndex(0)
	sel.HandleInput("\r")
	if len(shown) != 1 || !shown[0] {
		t.Fatalf("onSelect calls = %v, want [true] after moving the list's selection to yes", shown)
	}
}

// ShowImagesSelectorComponent extends Container (show-images-selector.ts:19): border, list, border are its children, and
// a keypress on the list is visible in the next render.
func TestShowImagesSelectorIsAContainerAndRendersListChanges(t *testing.T) {
	c := NewShowImagesSelectorComponent(true, nil, nil)
	if got := len(c.Children()); got != 3 {
		t.Fatalf("children = %d, want 3", got)
	}
	before := strings.Join(c.Render(60), "\n")
	c.HandleInput("\x1b[B")
	if after := strings.Join(c.Render(60), "\n"); after == before {
		t.Fatal("render did not change after moving the cursor")
	}
}
