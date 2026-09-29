package tui

import (
	"strings"
	"testing"
)

func BenchmarkToolDefinitionRender(b *testing.B) {
	card := NewToolExecutionComponent("custom_tool", "")
	card.SetDefinition(&ToolDefinitionRenderers{Call: func(ToolRenderInput) (Component, bool) { return NewText("call"), true }, Result: func(ToolRenderInput) (Component, bool) { return NewText(strings.Repeat("result line\n", 10)), true }}, nil)
	card.SetResult("result", false, 0)
	card.Render(120)
	b.ReportAllocs()
	for b.Loop() {
		card.Render(120)
	}
}
