package tui

import (
	"bytes"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/src/modes/interactive/components/assistant-message.ts:164
func TestAssistantThinkingClicksThroughTerminal(t *testing.T) {
	h := newAltHarness(t, 80, 24, TuiAltScreenOptions{})
	block := NewAssistantMessageBlock(false)
	content := []AssistantSegment{{Thinking: true, Text: "first reasoning"}, {Text: "answer"}, {Thinking: true, Text: "second reasoning"}}
	block.SetContent(content)
	h.tui.Add(block)
	h.start()
	// Pi's alternate screen consumes zone marks internally rather than writing them (tui-alt-screen.ts:1670).
	var output bytes.Buffer
	regular := NewWithOutput(&output, 80, 24)
	zoneBlock := NewAssistantMessageBlock(false)
	zoneBlock.SetContent(content)
	regular.Add(zoneBlock)
	regular.Render()
	if !strings.Contains(output.String(), "\x1b]133;A\x07") || !strings.Contains(output.String(), "\x1b]133;B\x07\x1b]133;C\x07") {
		t.Fatal("assistant zone markers did not reach the Main Screen terminal")
	}
	h.send("\x1b[<0;2;2M", "\x1b[<0;2;2m")
	if h.viewportHas("first reasoning") || !h.viewportHas("Thinking...") || !h.viewportHas("second reasoning") {
		t.Fatalf("terminal thinking visibility = %q", h.viewport())
	}
	block.SetContent(content)
	h.render()
	if h.viewportHas("first reasoning") {
		t.Fatal("streaming update lost the individual override")
	}
	block.SetHiddenThinking(true)
	block.SetHiddenThinking(false)
	h.render()
	if !h.viewportHas("first reasoning") || !h.viewportHas("second reasoning") {
		t.Fatal("global thinking toggle did not clear individual overrides")
	}
	for _, event := range []TuiMouseEvent{
		componentMouseEvent(MouseClick, 1, 0),
		componentMouseEvent(MouseClick, 1, 3),
		componentMouseEvent(MousePress, 1, 1),
	} {
		if result := DispatchMouseEvent(block, event); result != nil {
			t.Fatalf("non-thinking click handled: %+v", event)
		}
	}
}

func BenchmarkAssistantThinkingClick(b *testing.B) {
	block := NewAssistantMessageBlock(false)
	block.SetContent([]AssistantSegment{{Thinking: true, Text: strings.Repeat("reasoning ", 10000)}})
	block.Render(80)
	event := componentMouseEvent(MouseClick, 1, 1)
	b.ReportAllocs()
	for b.Loop() {
		DispatchMouseEvent(block, event)
	}
}
