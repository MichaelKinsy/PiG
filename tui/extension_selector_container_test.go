package tui

import (
	"strings"
	"testing"
)

// upstream: extension-selector.ts extends Container: the children are border, spacer, title, spacer, list, spacer, hint, spacer, border (a description adds a spacer and a text), and added children render with them.
func TestExtensionSelectorIsAContainerOfUpstreamChildren(t *testing.T) {
	e := NewExtensionSelectorComponent("Pick", []string{"one", "two"}, nil, nil)
	if e.ChildCount() != 9 {
		t.Fatalf("children = %d, want 9", e.ChildCount())
	}
	e.SetDescription("explain")
	if e.ChildCount() != 11 {
		t.Fatalf("children with a description = %d, want 11", e.ChildCount())
	}
	extra := NewText("EXTRA-ROW")
	e.Add(extra)
	if !strings.Contains(strings.Join(e.Render(60), "\n"), "EXTRA-ROW") {
		t.Fatal("an added child did not render")
	}
	e.Remove(extra)
	if strings.Contains(strings.Join(e.Render(60), "\n"), "EXTRA-ROW") {
		t.Fatal("a removed child still renders")
	}
}

// upstream: updateList clears the list container and adds one Text per option, so the arrow follows the selection; the countdown tick rewrites the title.
func TestExtensionSelectorListFollowsSelectionAndCountdown(t *testing.T) {
	e := NewExtensionSelectorComponent("Pick", []string{"one", "two"}, nil, nil)
	e.HandleInput("j")
	rendered := stripUserMessageSelectorANSILines(e.Render(60))
	var arrow []string
	for _, line := range rendered {
		if strings.Contains(line, "→") {
			arrow = append(arrow, line)
		}
	}
	if len(arrow) != 1 || !strings.Contains(arrow[0], "two") {
		t.Fatalf("arrow rows = %q", arrow)
	}
	e.SetCountdown(7)
	if !strings.Contains(strings.Join(stripUserMessageSelectorANSILines(e.Render(60)), "\n"), "Pick (7s)") {
		t.Fatal("countdown title not rendered")
	}
}
