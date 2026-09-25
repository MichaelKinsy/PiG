package tui

import (
	"strings"
	"testing"
)

// Alternating thinking/text snapshots exercise the same SetContent + Render
// path used by interactive message_update, at a representative response size.
func BenchmarkAssistantMessageStreaming(b *testing.B) {
	block := NewAssistantMessageBlock(false)
	content := []AssistantSegment{
		{Text: strings.Repeat("intro ", 128)},
		{Thinking: true, Text: strings.Repeat("reasoning ", 512)},
		{Text: strings.Repeat("answer ", 512)},
		{Thinking: true, Text: "checking the final result"},
		{Text: "final result"},
	}
	b.ReportAllocs()
	for b.Loop() {
		block.SetContent(content)
		block.Render(100)
	}
}
