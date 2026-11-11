package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// An async transform completes on a worker goroutine, which may not rebuild the block: Pi mutates a component only on its event loop. As UserMessageComponent.SetAsyncMarkdownTransform does, the block takes the completion by marking itself dirty (an atomic flag, which Invalidate on any goroutine is for) so the parent container renders it again and shows the rewrite, and calls the caller's hook.
func TestAssistantMessageAsyncCompletionMarksBlockDirty(t *testing.T) {
	var workers sync.WaitGroup
	var completed atomic.Int32
	transform := func(prefix string) *AsyncMarkdownTransform {
		return &AsyncMarkdownTransform{
			Context: t.Context(), Start: workers.Go,
			Invalidate: func() { completed.Add(1) },
			Prepare: func(markdown string, _ int) func(context.Context) string {
				return func(context.Context) string { return prefix + markdown }
			},
		}
	}
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	block.SetAsyncMarkdownTransforms(transform("text:"), transform("thinking:"))
	block.SetContent([]AssistantSegment{{Thinking: true, Text: "pondering"}, {Text: "answer"}})
	chat := NewContainer()
	chat.Add(block)
	chat.Render(80) // starts both generations; nothing is painted until each completes
	workers.Wait()
	if got := completed.Load(); got != 2 {
		t.Fatalf("the caller's invalidate hook ran %d times, want once per completed generation (2)", got)
	}
	if !block.IsDirty() {
		t.Error("a completed async transform did not mark the block dirty: its parent container serves the lines it cached before the rewrite")
	}
	got := strings.Join(chat.Render(80), "\n")
	for _, want := range []string{"text:answer", "thinking:pondering"} {
		if !strings.Contains(got, want) {
			t.Errorf("render after the completed transforms lacks %q: %q", want, got)
		}
	}
}
