package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports interactive-mode.ts setHiddenThinkingLabel (2373-2383) and its reset on session replacement (2473): the extension's label
// replaces "Thinking..." on every assistant block present and every block created later. No label restores the default and an empty label is an empty one
// (`label ?? this.defaultHiddenThinkingLabel`, :2383).
func TestExtensionHiddenThinkingLabelReachesAssistantBlocks(t *testing.T) {
	m := autocompleteModeProbe(t)
	m.hideThinking = true
	ui := &ExtUIContext{m: m}
	setLabel := func(label string) {
		autocompleteOwnerCall(t, m, func() error { ui.SetHiddenThinkingLabel(label); return nil })
		runPostedOwnerTasks(m)
	}
	clearLabel := func() {
		autocompleteOwnerCall(t, m, func() error { ui.ClearHiddenThinkingLabel(); return nil })
		runPostedOwnerTasks(m)
	}
	collapsed := func(block *tui.AssistantMessageComponent) string {
		return strings.Join(block.Render(60), "\n")
	}
	newBlock := func() *tui.AssistantMessageComponent {
		block := m.newAssistantMessageBlock()
		block.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "a long chain of reasoning"}, {Text: "answer"}})
		m.assistantBlocks = append(m.assistantBlocks, block)
		return block
	}
	before := newBlock()
	if got := collapsed(before); !strings.Contains(got, "Thinking...") {
		t.Fatalf("default block = %q", got)
	}
	setLabel("Pondering")
	after := newBlock()
	for name, block := range map[string]*tui.AssistantMessageComponent{"existing": before, "new": after} {
		got := collapsed(block)
		if !strings.Contains(got, "Pondering") || strings.Contains(got, "Thinking...") || strings.Contains(got, "chain of reasoning") {
			t.Errorf("%s block after the extension label = %q", name, got)
		}
	}
	setLabel("")
	if got := collapsed(before); strings.Contains(got, "Thinking...") || strings.Contains(got, "Pondering") || strings.Contains(got, "chain of reasoning") || !strings.Contains(got, "answer") {
		t.Errorf("an empty label must show an empty label, not the default or the thinking text: %q", got)
	}
	if got := collapsed(m.newAssistantMessageBlockWithThinking(t)); strings.Contains(got, "Thinking...") {
		t.Errorf("a block created after an empty label shows the default: %q", got)
	}
	clearLabel()
	if got := collapsed(before); !strings.Contains(got, "Thinking...") || strings.Contains(got, "Pondering") {
		t.Errorf("clearing did not restore the default: %q", got)
	}
	setLabel("Again")
	autocompleteOwnerCall(t, m, func() error { m.resetExtensionUIForReplacement(); return nil })
	runPostedOwnerTasks(m)
	if got := collapsed(after); !strings.Contains(got, "Thinking...") || strings.Contains(got, "Again") {
		t.Errorf("session replacement did not reset the label: %q", got)
	}
}

// runPostedOwnerTasks runs the tasks already posted to the owner loop: a caller that posts without waiting may return before the owner took the task.
func runPostedOwnerTasks(m *InteractiveMode) {
	for {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		default:
			return
		}
	}
}

// newAssistantMessageBlockWithThinking returns a registered assistant block holding one thinking run and an answer.
func (m *InteractiveMode) newAssistantMessageBlockWithThinking(t *testing.T) *tui.AssistantMessageComponent {
	t.Helper()
	block := m.newAssistantMessageBlock()
	block.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "a long chain of reasoning"}, {Text: "answer"}})
	m.assistantBlocks = append(m.assistantBlocks, block)
	return block
}
