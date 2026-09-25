package tui

import (
	"strings"
	"testing"
)

// TestAssistantMessageBlock_EmptyRendersNothing: zero height when no content.
func TestAssistantMessageBlock_EmptyRendersNothing(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	if lines := b.Render(80); len(lines) != 0 {
		t.Fatalf("empty block: want 0 lines, got %d", len(lines))
	}
}

// TestAssistantMessageBlock_TextOnly: text without thinking renders via Markdown.
func TestAssistantMessageBlock_TextOnly(t *testing.T) {
	b := NewAssistantMessageBlock(false)
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
	b := NewAssistantMessageBlock(false)
	b.SetThinkingDelta("step 1")
	b.SetTextDelta("answer")
	lines := b.Render(80)

	// lines[0] is the leading spacer (upstream Spacer(1) behaviour).
	if lines[0] != "" {
		t.Errorf("first line should be leading spacer, got %q", lines[0])
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
	b := NewAssistantMessageBlock(true) // hidden=true
	b.SetThinkingDelta("deep reasoning")
	b.SetTextDelta("answer")
	lines := b.Render(80)

	// lines[0] is the leading spacer; lines[1] is the hidden stub.
	if lines[0] != "" {
		t.Errorf("first line should be leading spacer, got %q", lines[0])
	}
	if !strings.Contains(lines[1], thinkingHiddenLabel) {
		t.Errorf("hidden thinking: want label %q, got %q", thinkingHiddenLabel, lines[1])
	}
	if strings.Contains(lines[1], "deep reasoning") {
		t.Errorf("hidden thinking: reasoning text should not appear in stub line")
	}
}

// TestAssistantMessageBlock_SetHiddenThinking: toggling changes rendering.
func TestAssistantMessageBlock_SetHiddenThinking(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	b.SetThinkingDelta("thoughts")

	// lines[0]=spacer, lines[1]=content.
	lines := b.Render(80)
	if !strings.Contains(lines[1], "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("visible: want thinking SGR, got %q", lines[1])
	}

	b.SetHiddenThinking(true)
	lines = b.Render(80)
	if !strings.Contains(lines[1], thinkingHiddenLabel) {
		t.Errorf("after SetHiddenThinking(true): want stub, got %q", lines[1])
	}

	b.SetHiddenThinking(false)
	lines = b.Render(80)
	if !strings.Contains(lines[1], "\x1b[3m"+ActiveTheme().ThinkingText) {
		t.Errorf("after SetHiddenThinking(false): want thinking SGR, got %q", lines[1])
	}
}

// TestAssistantMessageBlock_StreamingDeltas: multiple deltas accumulate correctly.
func TestAssistantMessageBlock_StreamingDeltas(t *testing.T) {
	b := NewAssistantMessageBlock(false)
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
	b := NewAssistantMessageBlock(false)
	b.SetThinkingDelta("🚀🚀🚀🚀") // 4 × 2col = 8 cols total
	lines := b.Render(4)
	// AssistantMessageBlock renders markdown/thinking with upstream-style
	// horizontal padding of 1 column on each side. At width=4 that leaves 2 content
	// columns, so each 2-column emoji wraps one-per-line: spacer + 4 rows.
	if len(lines) != 5 {
		t.Fatalf("emoji wrap: got %d lines want 5 (spacer+4 content with pad) (content=%v)", len(lines), lines)
	}
}

func TestAssistantMessageBlock_CJKWrapUsesBothHorizontalPaddingColumns(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	b.SetTextDelta("日本語テスト hello world 你好世界 test")
	lines := b.Render(32)
	want := []string{"", " 日本語テスト hello world 你好", " 世界 test"}
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
	b := NewAssistantMessageBlock(false)
	b.SetTerminalError("error", "GitHub Copilot credentials expired or were revoked. Run `pig login` and retry.")
	lines := b.Render(52)
	if len(lines) < 2 {
		t.Fatalf("error block: got %d lines, want spacer + error text: %#v", len(lines), lines)
	}
	if lines[0] != "" {
		t.Fatalf("first line should be leading spacer, got %q", lines[0])
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
	b := NewAssistantMessageBlock(false)
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
	b := NewAssistantMessageBlock(false)
	b.SetHasToolCalls(true)
	b.SetTerminalError("aborted", "")
	lines := b.Render(80)
	full := strings.Join(lines, "\n")
	if strings.Contains(full, "Operation aborted") {
		t.Fatalf("abort with tool calls should be suppressed, got %q", full)
	}
}

func TestAssistantMessageBlock_LengthStopRendersIncompleteResponseError(t *testing.T) {
	b := NewAssistantMessageBlock(false)
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
