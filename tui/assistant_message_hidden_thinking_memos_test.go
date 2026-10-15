package tui

import (
	"strings"
	"testing"
)

func markdownKeepsStreamingMemos(m *Markdown) bool {
	return m.wraps != nil || m.nextWraps != nil || m.inlines != nil || m.nextInlines != nil || m.resume != nil
}

// A thinking run hidden while it streams shows the hidden-thinking label, so its Markdown never renders again: a delta that
// arrived after the hide stays undrawn, and FinishStreaming, which keeps the memos of an undrawn text for its next render,
// would keep them for the session. A finished block's hidden runs keep no streaming memos, whether hidden by Ctrl+T or by a
// click on the run.
func TestFinishedBlockKeepsNoMemosForAHiddenThinkingRun(t *testing.T) {
	thinking := strings.Repeat("Considering **the** `options` and [one link](https://example.com) here.\n\n", 40)
	for _, hide := range []struct {
		name string
		hide func(b *AssistantMessageComponent)
	}{
		{"Ctrl+T", func(b *AssistantMessageComponent) { b.SetHideThinkingBlock(true) }},
		{"click", func(b *AssistantMessageComponent) {
			b.thinkingRuns[0].region.onMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft})
		}},
	} {
		t.Run(hide.name, func(t *testing.T) {
			block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			for n := 0; n < len(thinking); n += len(thinking) / 20 {
				block.SetThinkingDelta(thinking[n:min(n+len(thinking)/20, len(thinking))])
				block.Render(100)
			}
			md := block.segments[0].md
			if !markdownKeepsStreamingMemos(md) {
				t.Fatal("a visible streamed thinking run keeps memos for its next delta")
			}
			hide.hide(block)
			block.Render(100)
			block.SetThinkingDelta(" one more delta")
			block.Render(100)
			block.FinishStreaming()
			block.Render(100)
			if markdownKeepsStreamingMemos(md) {
				t.Fatal("the finished block's hidden thinking run keeps its streaming memos")
			}
		})
	}
}
