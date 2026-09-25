package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestTruncatedText_PreservesANSI verifies that truncation does not strip
// SGR codes and uses the upstream "..." ellipsis. Mirrors
// .upstream/v0.69.0/packages/tui/src/components/truncated-text.ts::render
// delegating to utils.truncateToWidth (utils.ts:812).
func TestTruncatedText_PreservesANSI(t *testing.T) {
	t.Run("plain truncation uses three-dot ellipsis", func(t *testing.T) {
		c := NewTruncatedText("hello world", 1)
		got := strings.Join(c.Render(8), "\n")
		if !strings.Contains(got, "...") {
			t.Fatalf("expected three-dot ellipsis, got %q", got)
		}
		if strings.Contains(got, "\u2026") {
			t.Fatalf("Unicode ellipsis leak, got %q", got)
		}
	})
	t.Run("ansi codes survive truncation", func(t *testing.T) {
		c := NewTruncatedText("\x1b[31mhello world\x1b[0m", 1)
		got := strings.Join(c.Render(8), "\n")
		if !strings.Contains(got, "\x1b[31m") {
			t.Fatalf("expected color prefix preserved, got %q", got)
		}
	})
	t.Run("stops at newline", func(t *testing.T) {
		c := NewTruncatedText("first\nsecond", 1)
		got := c.Render(20)
		if len(got) != 1 {
			t.Fatalf("expected 1 line, got %d (%q)", len(got), got)
		}
		if strings.Contains(got[0], "second") {
			t.Fatalf("should not include second line, got %q", got[0])
		}
	})
}

// TestSpacer_SetLines verifies the upstream-parity setter.
func TestSpacer_SetLines(t *testing.T) {
	s := NewSpacer(1)
	if got := len(s.Render(5)); got != 1 {
		t.Fatalf("initial Render expected 1 line, got %d", got)
	}
	s.SetLines(3)
	if got := len(s.Render(5)); got != 3 {
		t.Fatalf("after SetLines(3) expected 3 lines, got %d", got)
	}
}

func TestText_EmptyRendersNoLines(t *testing.T) {
	tx := NewText("   ")
	if got := tx.Render(20); len(got) != 0 {
		t.Fatalf("expected empty text to render no lines, got %v", got)
	}
}

func TestText_ReplacesTabsAndWraps(t *testing.T) {
	tx := NewPaddedText("a\tb", 1, 0, nil)
	lines := tx.Render(10)
	if len(lines) == 0 {
		t.Fatal("expected rendered text lines")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "a   b") {
		t.Fatalf("expected tabs replaced with three spaces, got %q", joined)
	}
}

type staticLines []string

func (s staticLines) Render(_ int) []string { return []string(s) }
func (s staticLines) Invalidate()           {}

func TestBox_AppliesPaddingAndBackground(t *testing.T) {
	box := NewPaddedBox(1, 1, func(text string) string { return "[bg]" + text + "[/bg]" })
	box.AddChild(staticLines([]string{"hi"}))
	lines := box.Render(6)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (top/content/bottom), got %v", lines)
	}
	if !strings.HasPrefix(lines[0], "[bg]") || !strings.HasPrefix(lines[1], "[bg]") || !strings.HasPrefix(lines[2], "[bg]") {
		t.Fatalf("expected bg fn applied to all lines, got %v", lines)
	}
	plain := strings.TrimPrefix(strings.TrimSuffix(lines[1], "[/bg]"), "[bg]")
	if widthx.VisibleWidth(plain) != 6 {
		t.Fatalf("content line visible width = %d want 6 (%q)", widthx.VisibleWidth(plain), plain)
	}
	if !strings.Contains(plain, " hi") {
		t.Fatalf("expected left padding around child line, got %q", plain)
	}
}
