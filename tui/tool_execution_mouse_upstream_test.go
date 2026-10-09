package tui

import (
	"strings"
	"testing"
)

func toolMouseCard(self bool) *ToolExecutionComponent {
	card := newToolCardForTest("read", "")
	card.SetDefinition(&ToolDefinitionRenderers{Self: self, Call: func(ToolRenderInput) (Component, bool) { return NewText("read notes.txt"), true }, Result: func(input ToolRenderInput) (Component, bool) {
		text := ""
		if input.Expanded {
			text = "hidden content"
		}
		return NewText(text), true
	}}, nil)
	return card
}

func TestToolResultClickThroughTerminal(t *testing.T) {
	for _, self := range []bool{false, true} {
		card := toolMouseCard(self)
		card.SetResult("hidden content", false, 0)
		h := newAltHarness(t, 120, 30, TuiAltScreenOptions{})
		h.tui.Add(card)
		h.start()
		if h.viewportHas("hidden content") {
			t.Fatal("collapsed result already visible")
		}
		if self {
			h.send("\x1b[<0;3;2M", "\x1b[<0;3;2m")
		} else {
			h.send("\x1b[<0;3;3M", "\x1b[<0;3;3m")
		}
		if !h.viewportHas("hidden content") {
			t.Fatalf("click did not expand self=%v: %q", self, h.viewport())
		}
	}
}

// tool-execution.ts createResultRegion requires a result and leaves padding, image rows and nested component handlers to their own owners.
// Pi: packages/coding-agent/src/modes/interactive/components/tool-execution.ts:120 (ToolExecutionComponent.invalidate).
func TestToolResultMouseRegions(t *testing.T) {
	card := toolMouseCard(false)
	lines := card.Render(120)
	click := func(x, y int) *TuiMouseDispatchResult {
		event := componentMouseEvent(MouseClick, x, y)
		event.Width = 120
		event.Height = len(lines)
		return DispatchMouseEvent(card, event)
	}
	if click(2, 2) != nil {
		t.Fatal("pending call toggled before any result")
	}
	card.SetResult("hidden content", false, 0)
	lines = card.Render(120)
	for _, point := range [][2]int{{2, 0}, {2, 1}, {0, 2}, {119, 2}, {2, len(lines) - 1}} {
		if click(point[0], point[1]) != nil {
			t.Fatalf("padding/spacer click handled: %v", point)
		}
	}
	if result := click(2, 2); result == nil || !result.Handled {
		t.Fatal("call content click was not handled")
	}
	if !strings.Contains(strings.Join(card.Render(120), "\n"), "hidden content") {
		t.Fatal("content did not expand")
	}

	withImageCapabilities(t, ImageProtocolKitty)
	card.ImageBlocks = []ImageBlock{{Data: "final-png", MIMEType: "image/png"}}
	card.Invalidate()
	lines = card.Render(120)
	if click(2, len(lines)-1) != nil {
		t.Fatal("image row toggled the result")
	}
}

func TestToolMouseDelegatesNestedHandlersBeforeExpansion(t *testing.T) {
	card := newToolCardForTest("custom", "")
	calls := 0
	card.SetDefinition(&ToolDefinitionRenderers{Call: func(ToolRenderInput) (Component, bool) {
		return NewMouseRegion(NewText("interactive"), func(event TuiMouseEvent) *TuiMouseEventResult {
			if event.Type != MouseClick {
				return nil
			}
			calls++
			return &TuiMouseEventResult{Handled: true}
		}), true
	}}, nil)
	card.SetResult("result", false, 0)
	lines := card.Render(120)
	event := componentMouseEvent(MouseClick, 2, 2)
	event.Width = 120
	event.Height = len(lines)
	if result := DispatchMouseEvent(card, event); result == nil || !result.Handled {
		t.Fatal("nested handler not called")
	}
	if calls != 1 || !card.Collapsed {
		t.Fatalf("nested calls=%d collapsed=%t", calls, card.Collapsed)
	}
}
