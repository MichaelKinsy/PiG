package tui

// width_parity_test.go: rendering regression suite for wide-char + bg-paint correctness.
//
// Carved as on 2026-05-03 after the emoji bg-paint stripe bug
// (daef9a4 + 5353ff8 + 28a10a5) existed undetected until user live testing.
// All tests are pure unit tests; no tmux, no LLM, < 100ms.
//
// AGENTS.md hard rule: use runewidth for all display width calculations.
// These tests would have caught the stripe bug that required a 6-file sweep.

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// Case 1: lineDisplayWidth counts terminal columns, not runes.
// "abc 🚀 def": a+b+c+space+🚀(2)+space+d+e+f = 10 cols, 9 runes.
func TestLineDisplayWidth_EmojiCountsColumns(t *testing.T) {
	s := "abc \U0001F680 def" // 🚀 is U+1F680, 2 terminal cols
	got := lineDisplayWidth(s)
	const want = 10
	if got != want {
		t.Fatalf("lineDisplayWidth(%q) = %d, want %d (rune count would be 9)", s, got, want)
	}
}

// Case 2: paintBgWith with emoji pads to exactly `width` visible columns,
// not width+1. Old rune-count bug overshot by 1 per wide char, causing a
// terminal soft-wrap and a no-bg stripe on the following row.
func TestPaintBgWith_EmojiNoPaddingOvershoot(t *testing.T) {
	const width = 20
	// "🚀🚀🚀 hi" = 2+2+2+1+2 = 9 visible cols. Expects 11 spaces of padding.
	line := "\U0001F680\U0001F680\U0001F680 hi"
	result := paintBgWith(UserMessageBgOpen(), line, width)
	visible := runewidth.StringWidth(stripANSI(result))
	if visible != width {
		t.Fatalf("paintBgWith with emoji: visible cols = %d, want exactly %d", visible, width)
	}
}

// Case 3: chunkByWidth splits at column boundaries, not rune boundaries.
// "🚀🚀🚀hello" at width=6: three rockets fill 6 cols exactly, then "hello".
func TestChunkByWidth_WideCharBoundary(t *testing.T) {
	chunks, _ := chunkByWidth("\U0001F680\U0001F680\U0001F680hello", 6)
	if len(chunks) != 2 {
		t.Fatalf("chunkByWidth: got %d chunks %q, want 2", len(chunks), chunks)
	}
	if chunks[0] != "\U0001F680\U0001F680\U0001F680" {
		t.Fatalf("chunkByWidth chunk[0] = %q, want 3 rockets", chunks[0])
	}
	if chunks[1] != "hello" {
		t.Fatalf("chunkByWidth chunk[1] = %q, want \"hello\"", chunks[1])
	}
}

// Case 4: padOrTrunc with emoji pads to exactly the requested width.
// "hi 🚀" = 5 visible cols; at width=6 must append exactly 1 space.
func TestPadOrTrunc_EmojiPadding(t *testing.T) {
	s := "hi \U0001F680"
	result := padOrTrunc(s, 6)
	visible := runewidth.StringWidth(stripANSI(result))
	if visible != 6 {
		t.Fatalf("padOrTrunc(%q, 6): visible cols = %d, want 6", s, visible)
	}
	if !strings.HasSuffix(result, " ") {
		t.Fatalf("padOrTrunc(%q, 6) = %q, should end with a padding space", s, result)
	}
}

// Case 5: padOrTrunc with CJK truncates at column boundary.
// "日本語テスト" = 6×2 = 12 cols. At width=5: keep 日本 (4 cols) and
// stop before 語 (would push to 6). No ellipsis: padOrTrunc is a hard
// clip, not a truncator (unlike truncToWidth which adds "…").
func TestPadOrTrunc_CJKTruncation(t *testing.T) {
	s := "日本語テスト"
	result := padOrTrunc(s, 5)
	visible := runewidth.StringWidth(result)
	if visible > 5 {
		t.Fatalf("padOrTrunc(%q, 5) = %q: visible cols = %d, must be <= 5", s, result, visible)
	}
	// Must retain the first two CJK characters (4 cols).
	if !strings.HasPrefix(result, "日本") {
		t.Fatalf("padOrTrunc(%q, 5) = %q, should start with \"日本\"", s, result)
	}
}

// Case 6: soft-wrap stripe regression.
// A bg-painted line with a wide char at the *last* position must not exceed
// `width` visible columns. The old rune-count path emitted width+1 cols for
// a line where the last char was a 2-wide emoji counted as 1 rune: the
// extra space had no background color, leaving a visible stripe.
func TestPaintBgWith_EmojiAtLastCol_NoStripe(t *testing.T) {
	const width = 80
	// Line = 78 ASCII chars + 1 emoji = 78+2 = 80 visible cols exactly.
	// paintBgWith should add 0 padding spaces: padded output must be exactly 80 cols.
	line := strings.Repeat("x", 78) + "\U0001F680"
	result := paintBgWith(UserMessageBgOpen(), line, width)
	visible := runewidth.StringWidth(stripANSI(result))
	if visible > width {
		t.Fatalf("soft-wrap stripe: painted line is %d cols, want <= %d (stripe at col %d+)", visible, width, width+1)
	}
	if visible != width {
		t.Fatalf("soft-wrap stripe: painted line is %d cols, want exactly %d", visible, width)
	}
}

func TestPaintBgWith_GrepShortReset(t *testing.T) {
	// grep --color uses \x1b[m (3-byte) instead of \x1b[0m (4-byte).
	// Both are full SGR resets. paintBgWith must re-apply the bg after both.
	open := "\x1b[48;2;30;33;38m"
	line := "test\x1b[01;31m\x1b[K:\x1b[m\x1b[K123"
	result := paintBgWith(open, line, 40)
	// The short \x1b[m should be followed by the bg re-open.
	if !strings.Contains(result, "\x1b[m"+open) {
		t.Fatalf("paintBgWith did not re-apply bg after short \\x1b[m reset in: %q", result)
	}
	// Embedded \x1b[K should be stripped (we add our own at the end).
	embeddedEraseCnt := strings.Count(result[:len(result)-10], "\x1b[K")
	if embeddedEraseCnt > 0 {
		t.Fatalf("paintBgWith should strip embedded \\x1b[K, found %d in: %q", embeddedEraseCnt, result)
	}
}
