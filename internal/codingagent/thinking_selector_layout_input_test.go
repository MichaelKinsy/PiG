package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/thinking-selector.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// thinking-selector.ts:18-21 THINKING_SELECT_LIST_LAYOUT {minPrimaryColumnWidth: 12, maxPrimaryColumnWidth: 32}: the label column is at least 12 wide, so the
// descriptions of "✓ off" (5 columns) and "  minimal" (9 columns) start in the same column, 14 after the 2-column cursor gutter. (Every real level label is shorter than 12, so
// the 32-column maximum has no row to act on: the level descriptions are keyed by Pi's seven level names.)
// mutation-checked: the mutant "layout min width 10" fails it.
func TestThinkingSelectorLabelColumnIsAtLeastTwelveWide(t *testing.T) {
	useKeybindings(t, nil)
	selector := NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "minimal"}, func(ai.ThinkingLevel) {}, func() {}, nil, "")
	var rows []string
	for _, line := range selector.GetSelectList().Render(100) {
		rows = append(rows, stripANSI(line))
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %q, want 2", rows)
	}
	for i, description := range []string{"No reasoning", "Very brief reasoning (~1k tokens)"} {
		column := strings.Index(rows[i], description)
		if column < 0 || len([]rune(rows[i][:column])) != 14 {
			t.Fatalf("row %q: description %q starts at column %d, want 14", rows[i], description, len([]rune(rows[i][:max(column, 0)])))
		}
	}
}

// thinking-selector.ts:129-135 handleInput: the save binding hands the highlighted level to onSelectAsDefault and returns, so the key is not typed into the search input.
// The binding is moved to a printable key so that typing it would filter the list.
// mutation-checked: the mutant "save falls through to the search input" fails it.
func TestThinkingSelectorSaveKeyIsNotTypedIntoTheSearchInput(t *testing.T) {
	useKeybindings(t, map[string][]KeyID{"app.thinking.save": {"x"}})
	var saved []ai.ThinkingLevel
	selector := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"off", "medium", "xhigh"}, func(ai.ThinkingLevel) {}, func() {},
		func(level ai.ThinkingLevel) { saved = append(saved, level) }, "")
	selector.HandleInput("x")
	if len(saved) != 1 || saved[0] != "medium" {
		t.Fatalf("saved = %v, want [medium]", saved)
	}
	rendered := stripANSI(strings.Join(selector.GetSelectList().Render(100), "\n"))
	for _, level := range []string{"off", "medium", "xhigh"} {
		if !strings.Contains(rendered, level) {
			t.Fatalf("the save key filtered the list (lost %s):\n%s", level, rendered)
		}
	}
}

// thinking-selector.ts:137-145 handleInput: Enter, Escape, Up and Down go to the list and return; they are not also typed into the search input, whose onSubmit would select again.
// mutation-checked: the mutant "navigation keys also reach the search input" fails it.
func TestThinkingSelectorConfirmSelectsOnce(t *testing.T) {
	useKeybindings(t, nil)
	var selected []ai.ThinkingLevel
	cancelled := 0
	selector := NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low"}, func(level ai.ThinkingLevel) { selected = append(selected, level) }, func() { cancelled++ }, nil, "")
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\r")
	if len(selected) != 1 || selected[0] != "low" {
		t.Fatalf("selected = %v after Down and Enter, want [low] once", selected)
	}
	selector.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("cancelled %d times after Escape, want once", cancelled)
	}
}
