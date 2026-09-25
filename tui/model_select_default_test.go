package tui

import (
	"strings"
	"testing"
)

// Pi renders the complete action hint in the dim theme color below the list.
func TestModelSelectorDefaultActionHint(t *testing.T) {
	selector := NewModelSelector("Select model", nil, mkItems("fixture/model"), "")
	want := fg(ActiveTheme().Dim, "  Enter to select · Ctrl+S to set as default · Escape/Ctrl+C to cancel")
	rendered := strings.Join(selector.Render(100), "\n")
	if !strings.Contains(rendered, want) {
		t.Fatalf("missing styled action hint %q in %q", want, rendered)
	}
}

func TestModelSelectorRemappedDefaultAction(t *testing.T) {
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.models.save": {"ctrl+r"}}))
	selector := NewModelSelector("Select model", nil, mkItems("fixture/model"), "")
	if rendered := strings.Join(selector.Render(100), "\n"); !strings.Contains(rendered, "Ctrl+R to set as default") {
		t.Fatalf("hint ignores configured save binding: %q", rendered)
	}
	selector.HandleInput("\x13")
	if selector.Done() {
		t.Fatal("default Ctrl+S remained active after remapping")
	}
	selector.HandleInput("\x12")
	if !selector.Done() || !selector.SelectedAsDefault() {
		t.Fatal("remapped save action did not select as default")
	}
}

func TestModelSelectorEnterDoesNotRequestDefault(t *testing.T) {
	selector := NewModelSelector("Select model", nil, mkItems("fixture/model"), "")
	selector.HandleInput("\r")
	if !selector.Done() || selector.SelectedAsDefault() {
		t.Fatal("Enter must select without persistence")
	}
}

func TestModelSelectorCtrlSSelectsDefault(t *testing.T) {
	for _, key := range []string{"\x13", "\x1b[115;5u"} {
		selector := NewModelSelector("Select model", nil, mkItems("fixture/model"), "")
		selector.HandleInput(key)
		if !selector.Done() || selector.Cancelled() || selector.SelectedFQ() != "fixture/model" {
			t.Fatalf("Ctrl+S %q did not select the default: done=%v selected=%q", key, selector.Done(), selector.SelectedFQ())
		}
		if !selector.SelectedAsDefault() {
			t.Fatal("save action lost persistence intent")
		}
	}
	empty := NewModelSelector("Select model", nil, nil, "")
	empty.HandleInput("\x13")
	if empty.Done() {
		t.Fatal("Ctrl+S must not accept an empty selection")
	}
}
