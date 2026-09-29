package tui

import (
	"strings"
	"testing"
)

func TestModelSelectorUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-selector.test.ts:29
	t.Run("keeps the current model marked while browsing", func(t *testing.T) {
		selector := NewModelSelector("Select model", nil, []ModelSelectorItem{{Provider: "test", ID: "current-model", Name: "Current Model"}, {Provider: "test", ID: "browsed-model", Name: "Browsed Model"}}, "test/current-model")
		row := func(id string) string {
			for _, line := range selector.Render(120) {
				plain := stripANSI(line)
				if strings.Contains(plain, id+" [") {
					return strings.TrimRight(plain, " ")
				}
			}
			return ""
		}
		if got := row("current-model"); got != "→ ✓ current-model [test]" {
			t.Fatal(got)
		}
		selector.HandleInput("\x1b[B")
		if got := row("current-model"); got != "  ✓ current-model [test]" {
			t.Fatal(got)
		}
		if got := row("browsed-model"); got != "→   browsed-model [test]" {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-selector.test.ts:60
	t.Run("uses the configured save binding", func(t *testing.T) {
		old := GetTUIKeybindings()
		t.Cleanup(func() { SetTUIKeybindings(old) })
		SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.models.save": {"ctrl+r"}}))
		selector := NewModelSelector("Select model", nil, []ModelSelectorItem{{Provider: "test", ID: "current-model"}}, "test/current-model")
		if rendered := stripANSI(strings.Join(selector.Render(120), "\n")); !strings.Contains(rendered, "Ctrl+R to set as default") {
			t.Fatal(rendered)
		}
		selector.HandleInput("\x13")
		if selector.SelectedAsDefault() || selector.Done() {
			t.Fatal("default binding still active")
		}
		selector.HandleInput("\x12")
		if !selector.SelectedAsDefault() || selector.SelectedFQ() != "test/current-model" {
			t.Fatalf("selected=%s persist=%v", selector.SelectedFQ(), selector.SelectedAsDefault())
		}
	})
}
