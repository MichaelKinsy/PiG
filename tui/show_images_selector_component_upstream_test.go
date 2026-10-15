package tui

import "testing"

// upstream: packages/coding-agent/src/modes/interactive/components/show-images-selector.ts:19-52. ShowImagesSelectorComponent is a Container whose children
// are a DynamicBorder, the SelectList (getSelectList, :47) and a DynamicBorder (:24, :42, :44), in that order. The list holds yes then no (:21-24),
// preselects index 0 for true and 1 for false (:31), and onSelect reports `item.value === "yes"` (:34).
// mutation-checked: swapping the preselection, mapping "no" to true, dropping the bottom border and putting the list before the top border each fail it.
func TestShowImagesSelectorComponentStructureAndSelectionUpstream(t *testing.T) {
	for _, current := range []bool{true, false} {
		var shown []bool
		var selector *ShowImagesSelectorComponent = NewShowImagesSelectorComponent(current, func(show bool) { shown = append(shown, show) }, func() {})

		children := selector.Children()
		if len(children) != 3 {
			t.Fatalf("current=%v: %d children, want border, list, border", current, len(children))
		}
		if _, ok := children[0].(*DynamicBorder); !ok {
			t.Errorf("current=%v: first child is %T, want the top *DynamicBorder", current, children[0])
		}
		if children[1] != Component(selector.GetSelectList()) {
			t.Errorf("current=%v: second child is not the select list", current)
		}
		if _, ok := children[2].(*DynamicBorder); !ok {
			t.Errorf("current=%v: last child is %T, want the bottom *DynamicBorder", current, children[2])
		}

		wantValue := "no"
		if current {
			wantValue = "yes"
		}
		if item, ok := selector.GetSelectList().SelectedItem(); !ok || item.Value != wantValue {
			t.Errorf("current=%v: preselected %+v, want %q", current, item, wantValue)
		}

		// Enter on yes then on no reports true then false whatever was preselected.
		list := selector.GetSelectList()
		list.SetSelectedIndex(0)
		selector.HandleInput("\r")
		list.SetSelectedIndex(1)
		selector.HandleInput("\r")
		if len(shown) != 2 || shown[0] != true || shown[1] != false {
			t.Errorf("current=%v: onSelect reported %v, want [true false]", current, shown)
		}
	}
}
