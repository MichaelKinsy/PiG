package agent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// steadyToolFixture is a committed transcript that already declares every tool,
// as a session in its steady state does before each turn: the loadout is
// unchanged, so declareToolChanges has nothing to add.
func steadyToolFixture(toolCount, history int) ([]AgentTool, []AgentMessage) {
	return steadyToolFixtureWithProperties(toolCount, history, 6)
}

func steadyToolFixtureWithProperties(toolCount, history, propertyCount int) ([]AgentTool, []AgentMessage) {
	tools := make([]AgentTool, toolCount)
	for i := range tools {
		properties := map[string]any{}
		for p := range propertyCount {
			properties[fmt.Sprintf("field%d", p)] = map[string]any{"type": "string", "description": fmt.Sprintf("argument %d of tool %d, with a description of realistic length", p, i)}
		}
		properties["nested"] = map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
		schema := ai.ToolSchema{
			Name:             fmt.Sprintf("tool_%d", i),
			Description:      "A tool with a description about as long as the built-in ones, which run to a few sentences.",
			Parameters:       map[string]any{"type": "object", "properties": properties, "required": []any{"field0"}},
			PromptGuidelines: []string{"Use this tool when needed."},
		}
		tools[i] = &samplingTool{fakeTool: fakeTool{name: schema.Name}, schema: schema}
	}
	committed := declareToolChanges(tools, nil, nil)
	for i := range history {
		committed = append(committed, AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("message %d", i))}})
	}
	return tools, committed
}

func BenchmarkDeclareToolChangesSteadyState(b *testing.B) {
	tools, committed := steadyToolFixture(12, 40)
	b.ReportAllocs()
	for b.Loop() {
		if got := declareToolChanges(tools, committed, nil); len(got) != 0 {
			b.Fatalf("steady state declared %d messages", len(got))
		}
	}
}
