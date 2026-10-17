package tui

import (
	"encoding/json"
	"testing"
)

// Pi packages/coding-agent/src/modes/interactive/components/tool-execution.ts:181-185 setArgsComplete: the card records that its arguments are complete and
// redraws, so a registered tool's renderers run again with context.argsComplete true (getRenderContext, :132) instead of the cached draw.
func TestToolExecutionSetArgsCompleteReachesTheRenderersAndRedraws(t *testing.T) {
	var inputs []ToolRenderInput
	card := newToolCardForTest("probe", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call: func(input ToolRenderInput) (Component, bool) {
			inputs = append(inputs, input)
			return NewPaddedText("call", 0, 0, nil), true
		},
	}, json.RawMessage(`{"a":1}`))

	card.Render(30)
	if len(inputs) == 0 || inputs[len(inputs)-1].ArgsComplete {
		t.Fatalf("before SetArgsComplete the renderer inputs are %+v, want ArgsComplete false", inputs)
	}
	before := len(inputs)
	card.SetArgsComplete()
	card.Render(30)
	if len(inputs) <= before {
		t.Fatalf("the renderer ran %d times after SetArgsComplete (was %d), want a redraw", len(inputs), before)
	}
	if !inputs[len(inputs)-1].ArgsComplete {
		t.Fatalf("after SetArgsComplete the renderer input is %+v, want ArgsComplete true", inputs[len(inputs)-1])
	}
}
