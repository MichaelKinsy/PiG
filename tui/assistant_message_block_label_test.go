package tui

// pi: packages/coding-agent/src/modes/interactive/components/assistant-message.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// assistant-message.ts setHiddenThinkingLabel: the label stands in for a hidden thinking run and the component re-renders with it; a visible run is unaffected.
// Pi: packages/coding-agent/src/modes/interactive/components/assistant-message.ts:58 (AssistantMessageComponent.setHideThinkingBlock); packages/coding-agent/src/modes/interactive/components/assistant-message.ts:66 (AssistantMessageComponent.setHiddenThinkingLabel).
func TestAssistantMessageComponentSetHiddenThinkingLabel(t *testing.T) {
	block := NewAssistantMessageComponent(nil, true, nil, "", nil, nil)
	block.SetContent([]AssistantSegment{{Thinking: true, Text: "SECRET"}, {Text: "answer"}})
	text := func() string { return widthx.StripAnsi(strings.Join(block.Render(40), "\n")) }
	if got := text(); !strings.Contains(got, "Thinking...") || strings.Contains(got, "SECRET") {
		t.Fatalf("default hidden label:\n%s", got)
	}
	block.SetHiddenThinkingLabel("Mulling it over")
	if got := text(); !strings.Contains(got, "Mulling it over") || strings.Contains(got, "Thinking...") {
		t.Fatalf("custom hidden label:\n%s", got)
	}
	block.SetHideThinkingBlock(false)
	if got := text(); !strings.Contains(got, "SECRET") || strings.Contains(got, "Mulling it over") {
		t.Fatalf("a visible run shows its text:\n%s", got)
	}
}
