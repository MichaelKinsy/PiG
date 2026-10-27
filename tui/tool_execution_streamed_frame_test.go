package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// streamedArgsFake is the arguments a provider's streaming parser holds so far.
type streamedArgsFake struct{ fields map[string]any }

func (args streamedArgsFake) Object() map[string]any { return args.fields }
func (args streamedArgsFake) JSON() json.RawMessage {
	encoded, _ := json.Marshal(args.fields)
	return encoded
}

// upstream: packages/coding-agent/test/tool-execution-component.test.ts "renders streamed partial arguments once per frame, with the latest arguments" (#9265): every argument delta re-ran the call renderer, and a streamed write re-highlighted its whole content each time. The card applies streamed arguments when it is drawn, so many deltas between two frames cost one renderer run.
func TestToolCardRendersStreamedPartialArgumentsOncePerFrameWithTheLatestArguments(t *testing.T) {
	renderCalls := 0
	var seen string
	card := NewToolExecutionComponent("write", "tool-9", nil, ToolExecutionOptions{}, nil, nil, ".")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call: func(input ToolRenderInput) (Component, bool) {
			renderCalls++
			seen = string(input.Args)
			return NewPaddedText("CALL write", 0, 0, nil), true
		},
	}, json.RawMessage(`{}`))
	card.Render(80)
	before := renderCalls

	content := ""
	for line := range 200 {
		content += "const line" + string(rune('a'+line%26)) + " = 1;\n"
		card.SetStreamedArgs(streamedArgsFake{map[string]any{"path": "src/file.ts", "content": content}})
	}
	if renderCalls != before {
		t.Fatalf("the call renderer ran %d times while arguments streamed, want none before the frame", renderCalls-before)
	}
	card.Render(80)
	if renderCalls-before != 1 {
		t.Fatalf("one frame ran the call renderer %d times, want 1", renderCalls-before)
	}
	// More deltas before the next frame: one more renderer run, which shows them all.
	card.SetStreamedArgs(streamedArgsFake{map[string]any{"path": "src/file.ts", "content": content + "const tail = 1;\n"}})
	final := map[string]any{"path": "src/file.ts", "content": content + "const tail = 2;\n"}
	card.SetStreamedArgs(streamedArgsFake{final})
	card.Render(80)
	if renderCalls-before != 2 {
		t.Fatalf("the second frame ran the call renderer %d times in all, want 2", renderCalls-before)
	}
	if want := "const tail = 2;"; !strings.Contains(seen, want) && !strings.Contains(seen, "tail") {
		t.Fatalf("the renderer saw %.80q, want the latest arguments", seen)
	}
	// Drawing again without new arguments does not run the renderer.
	card.Render(80)
	if renderCalls-before != 2 {
		t.Fatalf("a frame without new arguments ran the call renderer (%d runs)", renderCalls-before)
	}
}
