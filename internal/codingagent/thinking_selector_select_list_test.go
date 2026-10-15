package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: modes/interactive/components/thinking-selector.ts:151 getSelectList() returns the SelectList whose
// getSelectedItem() is the preselected current level; confirm and cancel reach onSelect/onCancel through list.onSelect/onCancel (:108-115).
func TestThinkingSelectorGetSelectListIsTheSelectList(t *testing.T) {
	var picked ai.ThinkingLevel
	cancelled := false
	selector := NewThinkingSelectorComponent(ai.ThinkingLevelMedium,
		[]ai.ThinkingLevel{ai.ThinkingLevelLow, ai.ThinkingLevelMedium, ai.ThinkingLevelHigh},
		func(level ai.ThinkingLevel) { picked = level }, func() { cancelled = true }, nil, "")
	item, ok := selector.GetSelectList().SelectedItem()
	if !ok || item.Value != "medium" {
		t.Fatalf("selected item = %+v ok=%v, want the current level medium", item, ok)
	}
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\r")
	if picked != ai.ThinkingLevelHigh {
		t.Fatalf("picked %q after down+enter, want high", picked)
	}
	selector.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("escape did not reach onCancel")
	}
}
