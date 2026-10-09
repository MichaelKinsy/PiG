package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// tool-execution.ts constructor(toolName, toolCallId, args, options, toolDefinition, ui, cwd): the card draws its header from args and cwd,
// hands toolCallId to the renderers' context, keeps the options, draws a given definition, and asks ui for a render when the image
// transcoder registers.
func TestToolExecutionConstructorArguments(t *testing.T) {
	t.Run("the header comes from the args and cwd", func(t *testing.T) {
		c := NewToolExecutionComponent("read", "call-1", json.RawMessage(`{"path":"/work/a.txt"}`), ToolExecutionOptions{}, nil, nil, "/work")
		got := stripANSI(strings.Join(c.Render(80), "\n"))
		if !strings.Contains(got, "read") || !strings.Contains(got, "a.txt") {
			t.Fatalf("header = %q", got)
		}
		if c.Cwd != "/work" || c.ToolCallID != "call-1" || c.Name != "read" {
			t.Fatalf("cwd=%q id=%q name=%q", c.Cwd, c.ToolCallID, c.Name)
		}
	})
	t.Run("the options apply", func(t *testing.T) {
		pad, cells, hide := 3, 20, false
		c := NewToolExecutionComponent("tool", "id", nil, ToolExecutionOptions{OutputPad: &pad, ImageWidthCells: &cells, ShowImages: &hide}, nil, nil, "")
		if c.outputPad != 3 || c.ImageWidthCells != 20 || c.ShowImages {
			t.Fatalf("pad=%d cells=%d show=%v", c.outputPad, c.ImageWidthCells, c.ShowImages)
		}
	})
	t.Run("a definition receives the args and the tool call id", func(t *testing.T) {
		var got ToolRenderInput
		definition := &ToolDefinitionRenderers{Call: func(input ToolRenderInput) (Component, bool) {
			got = input
			return NewText("custom call"), true
		}}
		c := NewToolExecutionComponent("custom", "call-9", json.RawMessage(`{"a":1}`), ToolExecutionOptions{}, definition, nil, "/w")
		if rendered := stripANSI(strings.Join(c.Render(60), "\n")); !strings.Contains(rendered, "custom call") {
			t.Fatalf("definition not drawn: %q", rendered)
		}
		if got.ToolCallID != "call-9" || string(got.Args) != `{"a":1}` {
			t.Fatalf("render input = %+v", got)
		}
		if !c.HasDefinition() {
			t.Fatal("the card does not report its definition")
		}
	})
	t.Run("the ui is asked for a render when the transcoder registers", func(t *testing.T) {
		withImageCapabilities(t, ImageProtocolKitty)
		ui := &renderCountingTUI{}
		SetImageTranscoderLoader(func(onRegistered func()) { onRegistered() })
		t.Cleanup(func() { SetImageTranscoderLoader(nil) })
		c := NewToolExecutionComponent("tool", "id", nil, ToolExecutionOptions{}, nil, ui, "")
		c.ImageBlocks = []ImageBlock{{Data: "jpeg", MIMEType: "image/jpeg"}}
		c.SetResult("", false, 0)
		c.Render(60)
		if ui.requests.Load() < 1 {
			t.Fatal("registering the transcoder did not ask the ui for a render")
		}
	})
}

type toolDefinitionHolder struct{ renderers *ToolDefinitionRenderers }

func (h toolDefinitionHolder) ToolRenderers() *ToolDefinitionRenderers { return h.renderers }

// tool-execution.ts:66 `toolDefinition: ToolRenderers | ToolDefinition | undefined`: the card draws the renderers whether it is given them bare or
// carried by a full tool definition, and a nil source or one without renderers is undefined.
func TestNewToolExecutionComponentAcceptsAnyToolDefinitionSource(t *testing.T) {
	renderers := &ToolDefinitionRenderers{Call: func(ToolRenderInput) (Component, bool) { return NewText("custom call"), true }}
	for name, source := range map[string]ToolDefinitionSource{"bare renderers": renderers, "a tool definition": toolDefinitionHolder{renderers}} {
		t.Run(name, func(t *testing.T) {
			c := NewToolExecutionComponent("custom", "id", nil, ToolExecutionOptions{}, source, nil, "/w")
			if !c.HasDefinition() || !strings.Contains(stripANSI(strings.Join(c.Render(60), "\n")), "custom call") {
				t.Fatalf("definition not drawn: has=%v", c.HasDefinition())
			}
		})
	}
	for name, source := range map[string]ToolDefinitionSource{"nil": nil, "no renderers": toolDefinitionHolder{}, "typed nil renderers": (*ToolDefinitionRenderers)(nil)} {
		t.Run("undefined/"+name, func(t *testing.T) {
			if c := NewToolExecutionComponent("custom", "id", nil, ToolExecutionOptions{}, source, nil, "/w"); c.HasDefinition() {
				t.Fatal("an undefined definition produced a definition card")
			}
		})
	}
}
