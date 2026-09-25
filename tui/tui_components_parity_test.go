package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestBox_Render_PaddingAndBg verifies Box renders padding rows and content
// rows with background applied, matching upstream box.ts behavior.
//
// Upstream evidence (packages/tui/src/components/box.ts):
//
//	new Box(1, 1, (text) => `[BG]${text}[/BG]`)
//	box.addChild({render: () => ["hello"]})
//	box.render(20)
//	→ ["[BG]                    [/BG]",   // top padding: 20 spaces
//	   "[BG] hello              [/BG]",   // 1 leftPad + "hello" + padding to 20
//	   "[BG]                    [/BG]"]   // bottom padding: 20 spaces
func TestBox_Render_PaddingAndBg(t *testing.T) {
	bg := func(s string) string { return "[BG]" + s + "[/BG]" }
	box := NewPaddedBox(1, 1, bg)

	child := &stubComponent{lines: []string{"hello"}}
	box.AddChild(child)

	lines := box.Render(20)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (top pad + content + bottom pad), got %d: %v", len(lines), lines)
	}

	// Top padding: bg applied to 20 spaces
	wantTop := bg(strings.Repeat(" ", 20))
	if lines[0] != wantTop {
		t.Errorf("top padding:\n got: %q\nwant: %q", lines[0], wantTop)
	}

	// Content: leftPad(1) + "hello" + rightPad to fill width=20
	// Inner content = " hello" (1 space + "hello" = 6 visible chars)
	// Padding to fill: 20 - 6 = 14 spaces
	wantContent := bg(" hello" + strings.Repeat(" ", 14))
	if lines[1] != wantContent {
		t.Errorf("content line:\n got: %q\nwant: %q", lines[1], wantContent)
	}

	// Bottom padding: same as top
	if lines[2] != wantTop {
		t.Errorf("bottom padding:\n got: %q\nwant: %q", lines[2], wantTop)
	}
}

// TestBox_Render_NoBgFn verifies Box without bgFn still pads to width.
func TestBox_Render_NoBgFn(t *testing.T) {
	box := NewPaddedBox(2, 0, nil)
	child := &stubComponent{lines: []string{"hi"}}
	box.AddChild(child)

	lines := box.Render(20)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (no paddingY), got %d", len(lines))
	}

	// Content: "  hi" (2 leftPad + "hi") padded to 20.
	want := "  hi" + strings.Repeat(" ", 16) // visWidth("  hi") = 4, pad = 16
	if lines[0] != want {
		t.Errorf("got: %q\nwant: %q", lines[0], want)
	}
}

// TestBox_Render_EmptyChildren returns empty slice (matches upstream).
func TestBox_Render_EmptyChildren(t *testing.T) {
	box := NewPaddedBox(1, 1, nil)
	lines := box.Render(20)
	if len(lines) != 0 {
		t.Fatalf("expected empty slice for no children, got %d lines", len(lines))
	}
}

// TestLoader_Render_PaddingMatchesUpstream verifies the fixed Loader
// rendering matches upstream Loader which extends Text(paddingX=1, paddingY=0).
//
// Upstream evidence (packages/tui/src/components/loader.ts):
//
//	new Loader(ui, s => s, s => s, "Loading...", undefined)
//	loader.render(40)
//	→ ["",                                        // leading empty line
//	   " ⠋ Loading...                           "] // 1-space left margin + content + right pad
func TestLoader_Render_PaddingMatchesUpstream(t *testing.T) {
	l := NewLoader("Loading...")
	lines := l.Render(40)

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "" {
		t.Errorf("first line should be empty, got %q", lines[0])
	}

	// Second line: " ⠋ Loading..." padded to width=40.
	// paddingX=1: leftMargin=" ", rightMargin=" "
	// content = "⠋ Loading..." (width: 1 + 1 + 10 = 12)
	// withMargins = " ⠋ Loading... " (width: 14)
	// pad = 40 - 14 = 26
	content := " ⠋ Loading... "
	visLen := widthx.VisibleWidth(content)
	pad := 40 - visLen
	want := content + strings.Repeat(" ", pad)
	if lines[1] != want {
		t.Errorf("loader line:\n got: %q (len %d)\nwant: %q (len %d)", lines[1], len(lines[1]), want, len(want))
	}
}

// TestLoader_Render_StyledColors verifies ANSI color codes are applied
// to spinner and message matching upstream's updateDisplay behavior.
func TestLoader_Render_StyledColors(t *testing.T) {
	l := NewStyledLoader("\x1b[36m", "\x1b[2m", "thinking", nil)
	lines := l.Render(50)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// Should contain the colored spinner
	if !strings.Contains(lines[1], "\x1b[36m⠋\x1b[0m") {
		t.Errorf("expected colored spinner in %q", lines[1])
	}
	// Should contain the colored message
	if !strings.Contains(lines[1], "\x1b[2mthinking\x1b[0m") {
		t.Errorf("expected colored message in %q", lines[1])
	}
	// Should have leading space (paddingX=1)
	if !strings.HasPrefix(lines[1], " ") {
		t.Errorf("expected leading space (paddingX=1), got %q", lines[1])
	}
}

// TestLoader_Render_EmptyFrames verifies behavior when frames=[] is given,
// matching upstream which sets indicator="" when frame is empty.
func TestLoader_Render_EmptyFrames(t *testing.T) {
	l := &Loader{Message: "waiting", Frames: []string{}}
	lines := l.Render(30)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// No indicator, just message with padding
	if !strings.Contains(lines[1], "waiting") {
		t.Errorf("expected message in %q", lines[1])
	}
	// Should have leading space from paddingX
	if !strings.HasPrefix(lines[1], " ") {
		t.Errorf("expected leading space from paddingX, got %q", lines[1])
	}
}

// TestText_Render_WrapsAndPads verifies Text with paddingX/Y matches
// upstream Text component behavior.
func TestText_Render_WrapsAndPads(t *testing.T) {
	txt := NewPaddedText("hello world", 1, 1, nil)
	lines := txt.Render(20)

	// paddingY=1 means 1 empty line top + content + 1 empty line bottom
	// "hello world" at contentWidth=18 doesn't wrap (11 < 18)
	// Content: " hello world " (margins) padded to 20
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (padY=1 + content + padY=1), got %d: %v", len(lines), lines)
	}

	// Top padding: 20 spaces
	wantEmpty := strings.Repeat(" ", 20)
	if lines[0] != wantEmpty {
		t.Errorf("top pad:\n got: %q\nwant: %q", lines[0], wantEmpty)
	}

	// Content: " hello world " + padding
	content := " hello world "
	vis := widthx.VisibleWidth(content)
	wantContent := content + strings.Repeat(" ", 20-vis)
	if lines[1] != wantContent {
		t.Errorf("content:\n got: %q\nwant: %q", lines[1], wantContent)
	}

	// Bottom padding
	if lines[2] != wantEmpty {
		t.Errorf("bottom pad:\n got: %q\nwant: %q", lines[2], wantEmpty)
	}
}

// TestText_Render_EmptyReturnsEmpty verifies empty/whitespace text returns
// empty slice matching upstream behavior.
func TestText_Render_EmptyReturnsEmpty(t *testing.T) {
	for _, content := range []string{"", "   ", "\t"} {
		txt := NewPaddedText(content, 1, 1, nil)
		lines := txt.Render(20)
		if len(lines) != 0 {
			t.Errorf("content=%q: expected empty slice, got %d lines", content, len(lines))
		}
	}
}

// TestTruncatedText_Render_TruncatesLong verifies TruncatedText truncates
// text that exceeds available width, matching upstream truncated-text.ts.
func TestTruncatedText_Render_TruncatesLong(t *testing.T) {
	tt := NewPaddedTruncatedText("This is a very long string that should be truncated at width", 1, 0)
	lines := tt.Render(20)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	// availableWidth = 20 - 2*1 = 18
	// "This is a very long string..." truncated to 18 cols with "..."
	vis := widthx.VisibleWidth(lines[0])
	if vis != 20 {
		t.Errorf("expected visible width 20 (padded), got %d: %q", vis, lines[0])
	}
	if !strings.Contains(lines[0], "...") {
		t.Errorf("expected ellipsis in truncated output, got %q", lines[0])
	}
}

// TestTruncatedText_Render_FitsNoTruncation verifies short text passes
// through without truncation.
func TestTruncatedText_Render_FitsNoTruncation(t *testing.T) {
	tt := NewPaddedTruncatedText("hi", 1, 1)
	lines := tt.Render(20)
	// paddingY=1: top + content + bottom = 3 lines
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Content line should contain "hi" without ellipsis
	if !strings.Contains(lines[1], "hi") {
		t.Errorf("expected 'hi' in content, got %q", lines[1])
	}
	if strings.Contains(lines[1], "...") {
		t.Errorf("should not have ellipsis for short text: %q", lines[1])
	}
}

// TestSpacer_Render verifies Spacer produces N empty lines matching upstream.
func TestSpacer_Render(t *testing.T) {
	s := NewSpacer(3)
	lines := s.Render(80)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if line != "" {
			t.Errorf("line[%d] should be empty, got %q", i, line)
		}
	}
}

// stubComponent is a minimal Component for testing Box.
type stubComponent struct {
	invalidatable
	lines []string
}

func (s *stubComponent) Render(_ int) []string { return s.lines }
