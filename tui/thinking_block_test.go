package tui

import (
	"strings"
	"testing"
)

// TestThinkingBlock_EmptyInvisible: zero-height when no content received yet.
func TestThinkingBlock_EmptyInvisible(t *testing.T) {
	b := NewThinkingBlock(false)
	if lines := b.Render(80); len(lines) != 0 {
		t.Fatalf("empty thinking block: got %d lines, want 0", len(lines))
	}
	b2 := NewThinkingBlock(true)
	if lines := b2.Render(80); len(lines) != 0 {
		t.Fatalf("empty hidden thinking block: got %d lines, want 0", len(lines))
	}
}

// TestThinkingBlock_HiddenLabel: non-empty + hidden → single "Thinking..." line.
func TestThinkingBlock_HiddenLabel(t *testing.T) {
	b := NewThinkingBlock(true)
	b.SetContent("some reasoning here")
	lines := b.Render(80)
	if len(lines) != 1 {
		t.Fatalf("hidden: got %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], thinkingHiddenLabel) {
		t.Errorf("hidden line %q does not contain %q", lines[0], thinkingHiddenLabel)
	}
	if !strings.Contains(lines[0], "\033[2m") {
		t.Errorf("hidden line should have dim SGR, got %q", lines[0])
	}
}

// TestThinkingBlock_VisibleContent: non-empty + visible → content lines with SGR.
func TestThinkingBlock_VisibleContent(t *testing.T) {
	b := NewThinkingBlock(false)
	b.SetContent("step one\nstep two")
	lines := b.Render(80)
	if len(lines) < 2 {
		t.Fatalf("visible: got %d lines, want >=2", len(lines))
	}
	for _, l := range lines {
		if !strings.Contains(l, "\033[90m") {
			t.Errorf("visible line %q missing gray SGR", l)
		}
		if !strings.Contains(l, "\033[3m") {
			t.Errorf("visible line %q missing italic SGR", l)
		}
	}
}

// TestThinkingBlock_SetHidden: toggle changes rendering.
func TestThinkingBlock_SetHidden(t *testing.T) {
	b := NewThinkingBlock(false)
	b.SetContent("reasoning")
	lines := b.Render(80)
	if len(lines) == 0 {
		t.Fatal("visible: want at least one line")
	}
	if strings.Contains(lines[0], thinkingHiddenLabel) {
		t.Error("visible mode should not show hidden label")
	}

	b.SetHidden(true)
	lines = b.Render(80)
	if len(lines) != 1 || !strings.Contains(lines[0], thinkingHiddenLabel) {
		t.Errorf("after SetHidden(true): want label, got %v", lines)
	}

	b.SetHidden(false)
	lines = b.Render(80)
	if strings.Contains(lines[0], thinkingHiddenLabel) {
		t.Error("after SetHidden(false): should not show label")
	}
}

// TestThinkingBlock_WrapLongLine: lines wider than width get split correctly.
func TestThinkingBlock_WrapLongLine(t *testing.T) {
	b := NewThinkingBlock(false)
	// 20 'a' chars: longer than width=8
	b.SetContent(strings.Repeat("a", 20))
	lines := b.Render(8)
	for _, l := range lines {
		// Strip SGR to measure actual content width.
		stripped := stripANSI(l)
		if len(stripped) > 8 {
			t.Errorf("line %q wider than 8 chars after stripping SGR", stripped)
		}
	}
}

// TestThinkingBlock_EmojiNoStripe: emoji (2-col) must not cause padding overflow.
// Each emoji is 2 cols; at width=4 we fit 2 emoji per line.
func TestThinkingBlock_EmojiNoStripe(t *testing.T) {
	b := NewThinkingBlock(false)
	b.SetContent("🚀🚀🚀🚀") // 4 rocket emoji = 8 cols
	lines := b.Render(4)
	// Should produce 2 lines of 2 emoji each (4 cols per line).
	if len(lines) != 2 {
		t.Fatalf("emoji wrap: got %d lines want 2 (content=%v)", len(lines), lines)
	}
}
