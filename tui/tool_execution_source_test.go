package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

type fixedToolSource struct{ renderers *ToolDefinitionRenderers }

func (s fixedToolSource) ToolRenderers() *ToolDefinitionRenderers { return s.renderers }

// tool-execution.ts:66 `toolDefinition: ToolRenderers | ToolDefinition | undefined`: the bare renderers and a full definition (any source of the
// renderers) draw the card from the constructor's arguments, and undefined, a nil renderers value or a source without renderers leaves it undefined.
func TestToolExecutionConstructorAcceptsEveryToolDefinitionSource(t *testing.T) {
	renderers := &ToolDefinitionRenderers{Call: func(input ToolRenderInput) (Component, bool) {
		return NewText("call " + string(input.Args)), true
	}}
	for _, c := range []struct {
		name   string
		source ToolDefinitionSource
		drawn  bool
	}{
		{"the bare renderers", renderers, true},
		{"a full definition", fixedToolSource{renderers}, true},
		{"undefined", nil, false},
		{"a nil renderers value", (*ToolDefinitionRenderers)(nil), false},
		{"a definition without renderers", fixedToolSource{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			card := NewToolExecutionComponent("custom_tool", "id", json.RawMessage(`{"a":1}`), ToolExecutionOptions{}, c.source, nil, "/")
			if card.HasDefinition() != c.drawn {
				t.Fatalf("HasDefinition = %v, want %v", card.HasDefinition(), c.drawn)
			}
			if got := strings.Contains(strings.Join(card.Render(60), "\n"), `call {"a":1}`); got != c.drawn {
				t.Fatalf("the definition's call renderer drew the card = %v, want %v", got, c.drawn)
			}
		})
	}
}
