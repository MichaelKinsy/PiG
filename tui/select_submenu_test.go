package tui

import (
	"strings"
	"testing"
)

func TestSelectSubmenuRendersTitleDescriptionAndHint(t *testing.T) {
	sel := NewSelectSubmenu(
		"Thinking Level",
		"Select reasoning depth for thinking-capable models",
		[]SelectItem{{Value: "off", Label: "off", Description: "No reasoning"}, {Value: "high", Label: "high", Description: "Deep reasoning (~16k tokens)"}},
		"off",
	)
	lines := sel.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Thinking Level") {
		t.Fatalf("missing title: %s", joined)
	}
	if !strings.Contains(joined, "Select reasoning depth") {
		t.Fatalf("missing description: %s", joined)
	}
	if !strings.Contains(joined, "Enter to select") {
		t.Fatalf("missing hint: %s", joined)
	}
	if strings.Contains(joined, "filter:") {
		t.Fatalf("submenu should not render filter input: %s", joined)
	}
}

func TestSelectSubmenuPreselectsCurrentValue(t *testing.T) {
	sel := NewSelectSubmenu(
		"Theme",
		"Select color theme",
		[]SelectItem{{Value: "dark", Label: "dark"}, {Value: "light", Label: "light"}},
		"light",
	)
	if got := sel.CurrentValue(); got != "light" {
		t.Fatalf("CurrentValue = %q, want light", got)
	}
}

func TestSelectSubmenuDownWrapsLastToFirst(t *testing.T) {
	items := []SelectItem{{Value: "auto", Label: "Automatic"}, {Value: "dark", Label: "dark"}, {Value: "light", Label: "light"}}
	selector := NewSelectSubmenu("Theme", "", items, "dark")
	selector.HandleInput("\x1b[B")
	if got := selector.CurrentValue(); got != "light" {
		t.Fatalf("first Down selected %q", got)
	}
	selector.HandleInput("\x1b[B")
	if got := selector.CurrentValue(); got != "auto" {
		t.Fatalf("wrapped Down selected %q", got)
	}
}
