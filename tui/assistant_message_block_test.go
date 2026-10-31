package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAssistantMessageBlock_EmptyRendersNothing: zero height when no content.
func TestAssistantMessageBlock_EmptyRendersNothing(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	if lines := b.Render(80); len(lines) != 0 {
		t.Fatalf("empty block: want 0 lines, got %d", len(lines))
	}
}

// TestAssistantMessageBlock_TextOnly: text without thinking renders via Markdown.
func TestAssistantMessageBlock_TextOnly(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTextDelta("hello world")
	lines := b.Render(80)
	if len(lines) == 0 {
		t.Fatal("text-only: want lines, got none")
	}
	full := strings.Join(lines, "\n")
	if !strings.Contains(full, "hello world") {
		t.Errorf("text not in output: %q", full)
	}
	// No thinking SGR should appear.
	if strings.Contains(full, "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("thinking SGR in text-only output: %q", full)
	}
}

// TestAssistantMessageBlock_ThinkingVisibleThenText: thinking renders before text.
func TestAssistantMessageBlock_ThinkingVisibleThenText(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetThinkingDelta("step 1")
	b.SetTextDelta("answer")
	lines := b.Render(80)

	// lines[0] is the leading spacer (upstream Spacer(1) behaviour).
	if lines[0] != "\x1b]133;A\x07" {
		t.Errorf("first line should be zone start on the leading spacer, got %q", lines[0])
	}
	// lines[1] should contain thinking SGR.
	if !strings.Contains(lines[1], "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("second line should have thinking SGR, got %q", lines[1])
	}
	// Output should also contain the text.
	full := strings.Join(lines, "\n")
	if !strings.Contains(full, "answer") {
		t.Errorf("text not found in output: %q", full)
	}
}

// TestAssistantMessageBlock_ThinkingHiddenShowsStub: hidden thinking shows label.
func TestAssistantMessageBlock_ThinkingHiddenShowsStub(t *testing.T) {
	b := NewAssistantMessageComponent(nil, true, nil, "", nil, nil, "", "") // hidden=true
	b.SetThinkingDelta("deep reasoning")
	b.SetTextDelta("answer")
	lines := b.Render(80)

	// lines[0] is the leading spacer; lines[1] is the hidden stub.
	if lines[0] != "\x1b]133;A\x07" {
		t.Errorf("first line should be zone start on the leading spacer, got %q", lines[0])
	}
	if !strings.Contains(lines[1], thinkingHiddenLabel) {
		t.Errorf("hidden thinking: want label %q, got %q", thinkingHiddenLabel, lines[1])
	}
	if strings.Contains(lines[1], "deep reasoning") {
		t.Errorf("hidden thinking: reasoning text should not appear in stub line")
	}
}

// TestAssistantMessageBlock_SetHideThinkingBlock: toggling changes rendering.
func TestAssistantMessageBlock_SetHideThinkingBlock(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetThinkingDelta("thoughts")

	// lines[0]=spacer, lines[1]=content.
	lines := b.Render(80)
	if !strings.Contains(lines[1], "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("visible: want thinking SGR, got %q", lines[1])
	}

	b.SetHideThinkingBlock(true)
	lines = b.Render(80)
	if !strings.Contains(lines[1], thinkingHiddenLabel) {
		t.Errorf("after SetHideThinkingBlock(true): want stub, got %q", lines[1])
	}

	b.SetHideThinkingBlock(false)
	lines = b.Render(80)
	if !strings.Contains(lines[1], "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("after SetHideThinkingBlock(false): want thinking SGR, got %q", lines[1])
	}
}

// TestAssistantMessageBlock_StreamingDeltas: multiple deltas accumulate correctly.
func TestAssistantMessageBlock_StreamingDeltas(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetThinkingDelta("step ")
	b.SetThinkingDelta("one")
	b.SetTextDelta("part ")
	b.SetTextDelta("two")

	if b.Thinking() != "step one" {
		t.Errorf("thinking: got %q want %q", b.Thinking(), "step one")
	}
	if b.Text() != "part two" {
		t.Errorf("text: got %q want %q", b.Text(), "part two")
	}
}

// TestAssistantMessageBlock_EmojiNoStripe: wide chars don't produce extra columns.
// Regression for the emoji bg-paint stripe bug (AGENTS.md column-aware rule).
func TestAssistantMessageBlock_EmojiNoStripe(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetThinkingDelta("🚀🚀🚀🚀") // 4 × 2col = 8 cols total
	lines := b.Render(4)
	// AssistantMessageComponent renders markdown/thinking with upstream-style
	// horizontal padding of 1 column on each side. At width=4 that leaves 2 content
	// columns, so each 2-column emoji wraps one-per-line: spacer + 4 rows.
	if len(lines) != 5 {
		t.Fatalf("emoji wrap: got %d lines want 5 (spacer+4 content with pad) (content=%v)", len(lines), lines)
	}
}

func TestAssistantMessageBlock_CJKWrapUsesBothHorizontalPaddingColumns(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTextDelta("日本語テスト hello world 你好世界 test")
	lines := b.Render(32)
	want := []string{"", " 日本語テスト hello world 你好", " 世界 test"}
	// Pi's Markdown constructor pads its output, including both horizontal margins, to the requested width.
	for i := 1; i < len(want); i++ {
		want[i] += strings.Repeat(" ", 32-lineDisplayWidth(want[i]))
	}
	if len(lines) != len(want) {
		t.Fatalf("CJK line count = %d, want %d: %#v", len(lines), len(want), lines)
	}
	for i := range want {
		if got := stripANSI(lines[i]); got != want[i] {
			t.Errorf("line %d = %q, want %q", i, got, want[i])
		}
	}
}

func TestAssistantMessageBlock_ErrorRendersInsideAssistantBlock(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTerminalError("error", "GitHub Copilot credentials expired or were revoked. Run `pig login` and retry.")
	lines := b.Render(52)
	if len(lines) < 2 {
		t.Fatalf("error block: got %d lines, want spacer + error text: %#v", len(lines), lines)
	}
	if lines[0] != "\x1b]133;A\x07" {
		t.Fatalf("first line should be zone start on the leading spacer, got %q", lines[0])
	}
	full := strings.Join(lines, "\n")
	if !strings.Contains(full, "Error: GitHub Copilot credentials expired") {
		t.Fatalf("error text not rendered: %q", full)
	}
	if !strings.Contains(full, ActiveTheme().Error) {
		t.Fatalf("error style missing: %q", full)
	}
}

// TestAssistantMessageBlock_AbortRendersOperationAborted: user Ctrl+C shows
// "Operation aborted" inside the assistant block, matching upstream
// assistant-message.ts:129-133.
func TestAssistantMessageBlock_AbortRendersOperationAborted(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTextDelta("partial ")
	b.SetTerminalError("aborted", "")
	lines := b.Render(80)
	full := strings.Join(lines, "\n")
	if !strings.Contains(full, "Operation aborted") {
		t.Fatalf("abort: want 'Operation aborted', got %q", full)
	}
	// Should NOT show "Error:" prefix for aborts.
	if strings.Contains(full, "Error:") {
		t.Fatalf("abort should not have Error: prefix, got %q", full)
	}
}

// TestAssistantMessageBlock_AbortSuppressedWithToolCalls: when tool calls are
// present, the abort message is suppressed (tools show their own errors).
func TestAssistantMessageBlock_AbortSuppressedWithToolCalls(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetHasToolCalls(true)
	b.SetTerminalError("aborted", "")
	lines := b.Render(80)
	full := strings.Join(lines, "\n")
	if strings.Contains(full, "Operation aborted") {
		t.Fatalf("abort with tool calls should be suppressed, got %q", full)
	}
}

func TestAssistantMessageBlock_LengthStopRendersIncompleteResponseError(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTextDelta("partial answer")
	b.SetHasToolCalls(true)
	b.SetTerminalError("length", "")
	lines := b.Render(80)
	full := strings.Join(lines, "\n")
	for _, want := range []string{
		"Response was truncated before completion.",
	} {
		if !strings.Contains(full, want) {
			t.Fatalf("length stop message missing %q: %q", want, full)
		}
	}
}

// TestAssistantMessageBlock_ConcurrentMutationsAndRender is the race regression:
// extension view builders, repaint callbacks and MouseRegion clicks mutate the
// block off the owner goroutine, and an unsynchronized updateContent pair let
// one caller's clear() write {0x0, 0x0} nil Components into the child list
// another caller was handing to SetChildren (the renderChildLocked
// nil-dispatch panic). Under -race every mutator and Render must serialize.
func TestAssistantMessageBlock_ConcurrentMutationsAndRender(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	msg := AssistantMessage{Content: []AssistantContentBlock{
		{Type: "thinking", Thinking: "racing thought"},
		{Type: "text", Text: "racing answer"},
	}}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				switch (worker + i) % 6 {
				case 0:
					b.SetHiddenThinkingLabel("")
				case 1:
					b.SetHiddenThinkingLabel("Thinking...")
				case 2:
					b.SetOutputPad(i % 2)
				case 3:
					b.SetHideThinkingBlock(i%2 == 0)
				case 4:
					b.UpdateContent(msg, i%2 == 0)
				default:
					// The frontend-node path reads b.segments, the
					// visibility overrides and the terminal state; it
					// must serialize under b.mu like every other read.
					TranscriptNodes(b, 80+i%40, i%2 == 0, nil)
				}
			}
		}(worker)
	}
	for renderer := 0; renderer < 4; renderer++ {
		wg.Add(1)
		go func(renderer int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if lines := b.Render(80); lines == nil {
					t.Error("Render(80) returned nil slice")
				}
				// The event width alternates 60/100, never the rendered 80,
				// so HandleMouse always misses the layout cache and must
				// measure the children at the event width under b.mu.
				event := TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft,
					X: i % 5, Y: i % 3, Width: 60 + (i%2)*40, Height: 40}
				b.HandleMouse(event)
			}
		}(renderer)
	}
	// The getters read the thinking/text pair under snapMu.RLock; racing them
	// against growing writes catches a getter that bypasses the lock and
	// reads a field the delta writers are updating.
	for reader := 0; reader < 2; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = b.Text()
				_ = b.Thinking()
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b.SetTextDelta(strings.Repeat("x", i%97))
				b.SetThinkingDelta(strings.Repeat("y", i%89))
			}
		}()
	}
	wg.Wait()
}

// TestAssistantMessageBlock_StaleThinkingRegionClickIgnored is the stale
// region regression: a click on a MouseRegion a later updateContent replaced
// at the same run index must report the click handled but not toggle the
// replacement run's visibility.
func TestAssistantMessageBlock_StaleThinkingRegionClickIgnored(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetThinkingDelta("first thought")
	b.SetTextDelta("answer")
	b.Render(80)
	b.mu.Lock()
	staleRegion := b.thinkingRuns[0].region
	b.mu.Unlock()

	// Reorder so the thinking segment rebuilds a different Markdown: the
	// update replaces thinkingRuns[0].region while the run index survives.
	b.SetContent([]AssistantSegment{
		{Text: "new answer"},
		{Thinking: true, Text: "second thought"},
	})
	before := b.Render(80)

	result := staleRegion.HandleMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft,
		X: 1, Y: 0, Width: 80, Height: 1})
	if result == nil || !result.Handled {
		t.Fatalf("stale region click: want Handled result, got %+v", result)
	}
	if got := b.Render(80); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("stale region click mutated the block:\nbefore=%q\nafter=%q", before, got)
	}
	if len(b.thinkingVisibilityOverrides) != 0 {
		t.Fatalf("stale region click wrote visibility overrides: %v", b.thinkingVisibilityOverrides)
	}
}

// TestAssistantMessageBlock_TransformStateGetterNoDeadlock is the
// re-entrancy regression: a TransformState that calls a block getter back
// (the pattern SetMarkdownTransformState(b.Text) registers) runs while
// Render holds b.mu. The getters take snapMu.RLock instead of b.mu, so the
// render must return instead of deadlocking.
func TestAssistantMessageBlock_TransformStateGetterNoDeadlock(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	b.SetTextDelta("answer text")
	b.SetThinkingDelta("reason text")
	b.SetMarkdownTransformState(b.Text)
	b.SetThinkingMarkdownTransform(func(markdown string, width int) string {
		return b.Thinking() + markdown
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Render(80)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Render deadlocked: TransformState/Transform callback re-entered b.mu")
	}
}
