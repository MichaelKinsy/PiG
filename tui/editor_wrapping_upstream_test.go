package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The upstream constructor uses a 24-row terminal, hence max(5, floor(24*0.3)) = 7 visible lines.
func newUpstreamWrappingEditor() *Editor {
	e := NewEditor()
	e.SetMaxVisibleLines(7)
	return e
}

func assertWrappingWidths(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got != width {
			t.Errorf("line %d width = %d, want %d: %q", i, got, width, line)
		}
	}
}

func wrappingContent(lines []string) []string { return lines[1 : len(lines)-1] }

func TestUpstreamEditorScrollIndicators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		// packages/tui/test/editor.test.ts:707.
		{"centers scroll indicators on wide borders", 40},
		// packages/tui/test/editor.test.ts:720.
		{"keeps truncated scroll indicators within width and preserves their color (issue #6962)", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpstreamWrappingEditor()
			borderColor := func(text string) string { return "\x1b[35m" + text + "\x1b[39m" }
			if tc.width == 10 {
				e.BorderColor = borderColor
			}
			text := make([]string, 20)
			for i := range text {
				text[i] = fmt.Sprintf("line %d", i)
			}
			e.SetText(strings.Join(text, "\n"))
			e.Render(tc.width)
			for range 10 {
				e.HandleInput("\x1b[A")
			}
			lines := e.Render(tc.width)
			top, bottom := lines[0], lines[len(lines)-1]
			if tc.width == 40 {
				for i, row := range []string{top, bottom} {
					label := []string{" ↑ 9 more ", " ↓ 4 more "}[i]
					want := strings.Repeat("─", 15) + label + strings.Repeat("─", 15)
					if got := widthx.StripAnsi(row); got != want {
						t.Errorf("border = %q, want %q", got, want)
					}
				}
			} else {
				for i, row := range []string{top, bottom} {
					plain := widthx.StripAnsi(row)
					if prefix := []string{"─── ↑", "─── ↓"}[i]; !strings.HasPrefix(plain, prefix) {
						t.Errorf("border = %q, want prefix %q", plain, prefix)
					}
					if want := borderColor(plain); row != want {
						t.Errorf("colored border = %q, want %q", row, want)
					}
					// Strengthen the original prefix check with the exact Pi :276-295 truncation bytes.
					if want := []string{"─── ↑ 9...", "─── ↓ 4..."}[i]; plain != want {
						t.Errorf("truncated border = %q, want %q", plain, want)
					}
				}
				assertWrappingWidths(t, lines, tc.width)
			}
		})
	}
}

func TestUpstreamEditorGraphemeWrapping(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		width      int
	}{
		// packages/tui/test/editor.test.ts:745.
		{"wraps lines correctly when text contains wide emojis", "Hello ✅ World", 20},
		// packages/tui/test/editor.test.ts:760.
		{"wraps long text with emojis at correct positions", "✅✅✅✅✅✅", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpstreamWrappingEditor()
			e.SetText(tc.text)
			assertWrappingWidths(t, wrappingContent(e.Render(tc.width)), tc.width)
		})
	}
	// packages/tui/test/editor.test.ts:777.
	t.Run("renders isolated Thai and Lao AM clusters without width drift", func(t *testing.T) {
		for _, text := range []string{"ำabc", "ຳabc"} {
			e := newUpstreamWrappingEditor()
			e.SetText(text)
			assertWrappingWidths(t, e.Render(8), 8)
		}
	})
	// packages/tui/test/editor.test.ts:789.
	t.Run("wraps CJK characters correctly (each is 2 columns wide)", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("日本語テスト")
		lines := wrappingContent(e.Render(11))
		assertWrappingWidths(t, lines, 11)
		plain := make([]string, len(lines))
		for i, line := range lines {
			plain[i] = widthx.JSTrim(widthx.StripAnsi(line))
		}
		if !slices.Equal(plain, []string{"日本語テス", "ト"}) {
			t.Fatalf("content = %q", plain)
		}
	})
	// packages/tui/test/editor.test.ts:809.
	t.Run("handles mixed ASCII and wide characters in wrapping", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Test ✅ OK 日本")
		lines := wrappingContent(e.Render(16))
		if len(lines) != 1 {
			t.Fatalf("content = %q, want one line", lines)
		}
		assertWrappingWidths(t, lines, 16)
	})
	// packages/tui/test/editor.test.ts:825.
	t.Run("renders cursor correctly on wide characters", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("A✅B")
		line := e.Render(20)[1]
		if !strings.Contains(line, "\x1b[7m") {
			t.Errorf("no reverse-video cursor in %q", line)
		}
		assertWrappingWidths(t, []string{line}, 20)
	})
	// packages/tui/test/editor.test.ts:841.
	t.Run("does not exceed terminal width with emoji at wrap boundary", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("0123456789✅")
		for _, line := range wrappingContent(e.Render(11)) {
			if widthx.VisibleWidth(line) > 11 {
				t.Errorf("line exceeds width 11: %q", line)
			}
		}
	})
	// packages/tui/test/editor.test.ts:856.
	t.Run("shows cursor at end of line before wrap, wraps on next char", func(t *testing.T) {
		for _, paddingX := range []int{0, 1} {
			e := newUpstreamWrappingEditor()
			e.SetPaddingX(paddingX)
			for _, ch := range "aaaaaaaaa" {
				e.HandleInput(string(ch))
			}
			lines := wrappingContent(e.Render(10 + paddingX))
			if len(lines) != 1 {
				t.Fatalf("padding %d: content = %q, want one line", paddingX, lines)
			}
			if !strings.HasSuffix(lines[0], "\x1b[7m \x1b[0m") {
				t.Errorf("cursor not at end: %q", lines[0])
			}
			e.HandleInput("a")
			if lines = wrappingContent(e.Render(10 + paddingX)); len(lines) != 2 {
				t.Errorf("padding %d: content = %q, want two lines", paddingX, lines)
			}
		}
	})
}

func TestUpstreamEditorWordWrapping(t *testing.T) {
	// packages/tui/test/editor.test.ts:878.
	t.Run("wraps at word boundaries instead of mid-word", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Hello world this is a test of word wrapping functionality")
		lines := wrappingContent(e.Render(40))
		if strings.HasSuffix(widthx.JSTrim(widthx.StripAnsi(lines[0])), "-") {
			t.Error("line ends with hyphen")
		}
		lastCharPattern := regexp.MustCompile(`[\w.,!?;:]$`)
		for _, row := range lines {
			line := widthx.JSTrim(widthx.StripAnsi(row))
			if line != "" && !lastCharPattern.MatchString(line) {
				t.Errorf("line ends unexpectedly: %q", line)
			}
		}
	})
	// packages/tui/test/editor.test.ts:900.
	t.Run("does not start lines with leading whitespace after word wrap", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Word1 Word2 Word3 Word4 Word5 Word6")
		leadingWhitespace := regexp.MustCompile(`^\s+\S`)
		for _, row := range wrappingContent(e.Render(20)) {
			line := widthx.StripAnsi(row)
			if widthx.JSTrim(line) != "" && leadingWhitespace.MatchString(strings.TrimRight(line, " \t\r\n")) {
				t.Errorf("leading whitespace: %q", line)
			}
		}
	})
	// packages/tui/test/editor.test.ts:921.
	t.Run("breaks long words (URLs) at character level", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Check https://example.com/very/long/path/that/exceeds/width here")
		assertWrappingWidths(t, wrappingContent(e.Render(30)), 30)
	})
	// packages/tui/test/editor.test.ts:935.
	t.Run("preserves multiple spaces within words on same line", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("Word1   Word2    Word3")
		if got := widthx.JSTrim(widthx.StripAnsi(e.Render(50)[1])); !strings.Contains(got, "Word1   Word2") {
			t.Errorf("spaces lost: %q", got)
		}
	})
	// packages/tui/test/editor.test.ts:947.
	t.Run("handles empty string", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("")
		if lines := e.Render(40); len(lines) != 3 {
			t.Errorf("lines = %q, want three", lines)
		}
	})
	// packages/tui/test/editor.test.ts:958.
	t.Run("handles single word that fits exactly", func(t *testing.T) {
		e := newUpstreamWrappingEditor()
		e.SetText("1234567890")
		lines := e.Render(11)
		if len(lines) != 3 {
			t.Fatalf("lines = %q, want three", lines)
		}
		if got := widthx.StripAnsi(lines[1]); !strings.Contains(got, "1234567890") {
			t.Errorf("missing word: %q", got)
		}
	})
	for _, tc := range []struct {
		name, line string
		width      int
		want       []string
	}{
		// packages/tui/test/editor.test.ts:971.
		{"wraps word to next line when it ends exactly at terminal width", "hello world test", 11, []string{"hello ", "world test"}},
		// packages/tui/test/editor.test.ts:981.
		{"keeps whitespace at terminal width boundary on same line", "hello world test", 12, []string{"hello world ", "test"}},
		// packages/tui/test/editor.test.ts:991.
		{"handles unbreakable word filling width exactly followed by space", "aaaaaaaaaaaa aaaa", 12, []string{"aaaaaaaaaaaa", " aaaa"}},
		// packages/tui/test/editor.test.ts:999.
		{"wraps word to next line when it fits width but not remaining space", "      aaaaaaaaaaaa", 12, []string{"      ", "aaaaaaaaaaaa"}},
		// packages/tui/test/editor.test.ts:1007.
		{"keeps word with multi-space and following word together when they fit", "Lorem ipsum dolor sit amet,    consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,    consectetur"}},
		// packages/tui/test/editor.test.ts:1015.
		{"keeps word with multi-space and following word when they fill width exactly", "Lorem ipsum dolor sit amet,              consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,              consectetur"}},
		// packages/tui/test/editor.test.ts:1023.
		{"splits when word plus multi-space plus word exceeds width", "Lorem ipsum dolor sit amet,               consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,               ", "consectetur"}},
		// packages/tui/test/editor.test.ts:1032.
		{"breaks long whitespace at line boundary", "Lorem ipsum dolor sit amet,                         consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,                         ", "consectetur"}},
		// packages/tui/test/editor.test.ts:1041.
		{"breaks long whitespace at line boundary 2", "Lorem ipsum dolor sit amet,                          consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,                         ", " consectetur"}},
		// packages/tui/test/editor.test.ts:1050.
		{"breaks whitespace spanning full lines", "Lorem ipsum dolor sit amet,                                     consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,                         ", "            consectetur"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := wordWrapLine(tc.line, tc.width, nil)
			got := make([]string, len(chunks))
			for i, chunk := range chunks {
				got[i] = chunk.text
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("chunks = %q, want %q", got, tc.want)
			}
		})
	}
	// packages/tui/test/editor.test.ts:1059.
	t.Run("force-breaks when wide char after word boundary wrap still overflows", func(t *testing.T) {
		line := " " + strings.Repeat("a", 186) + "你"
		assertWrappingReconstruction(t, line, wordWrapLine(line, 187, nil), 187)
	})
	const marker = "[paste #1 +20 lines]"
	const secondMarker = "[paste #2 +30 lines]"
	// Explicit upstream arrays: preserve the atomic boundaries and original indices, not a fresh segmenter result.
	segment := func(text string, index int) editorSegment {
		return editorSegment{Text: text, Start: index, End: index + jsstring.Length(text), Width: widthx.VisibleWidth(text)}
	}
	for _, tc := range []struct {
		name, line                string
		segments                  []editorSegment
		first, last, lastContains string
	}{
		// packages/tui/test/editor.test.ts:1077.
		{"splits oversized atomic segment across multiple chunks", "A" + marker + "B", []editorSegment{segment("A", 0), segment(marker, 1), segment("B", 1+len(marker))}, "", "", ""},
		// packages/tui/test/editor.test.ts:1102.
		{"splits oversized atomic segment at start of line", marker + "B", []editorSegment{segment(marker, 0), segment("B", len(marker))}, "", "", "B"},
		// packages/tui/test/editor.test.ts:1122.
		{"splits oversized atomic segment at end of line", "A" + marker, []editorSegment{segment("A", 0), segment(marker, 1)}, "A", "", ""},
		// packages/tui/test/editor.test.ts:1141.
		{"splits consecutive oversized atomic segments", marker + secondMarker, []editorSegment{segment(marker, 0), segment(secondMarker, len(marker))}, "", "", ""},
		// packages/tui/test/editor.test.ts:1163.
		{"wraps normally after oversized atomic segment", marker + " hello world", []editorSegment{
			segment(marker, 0), segment(" ", len(marker)), segment("h", len(marker)+1), segment("e", len(marker)+2),
			segment("l", len(marker)+3), segment("l", len(marker)+4), segment("o", len(marker)+5), segment(" ", len(marker)+6),
			segment("w", len(marker)+7), segment("o", len(marker)+8), segment("r", len(marker)+9), segment("l", len(marker)+10), segment("d", len(marker)+11),
		}, "", "world", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := wordWrapLine(tc.line, 10, tc.segments)
			assertWrappingReconstruction(t, tc.line, chunks, 10)
			if tc.first != "" && chunks[0].text != tc.first {
				t.Errorf("first = %q, want %q", chunks[0].text, tc.first)
			}
			last := chunks[len(chunks)-1].text
			if tc.last != "" && last != tc.last {
				t.Errorf("last = %q, want %q", last, tc.last)
			}
			if !strings.Contains(last, tc.lastContains) {
				t.Errorf("last = %q, want containing %q", last, tc.lastContains)
			}
		})
	}
}

func TestEditorWrappingOverflowAndUTF16Offsets(t *testing.T) {
	// Pi editor.ts:153-168 force-breaks when a prior word boundary cannot fit the remainder. The original :1059 CJK input now uses the CJK opportunity at :203-209, so a non-CJK wide grapheme independently exercises this branch.
	line := " " + strings.Repeat("a", 186) + "✅"
	assertWrappingReconstruction(t, line, wordWrapLine(line, 187, nil), 187)
	// Pi TextChunk indices count UTF-16 units, not bytes or code points.
	want := []textChunk{{text: "A😀", startIndex: 0, endIndex: 3}, {text: "B😀", startIndex: 3, endIndex: 6}, {text: "C", startIndex: 6, endIndex: 7}}
	if got := wordWrapLine("A😀B😀C", 3, nil); !slices.Equal(got, want) {
		t.Errorf("UTF-16 chunks = %+v, want %+v", got, want)
	}
}

func BenchmarkEditorWrappingUpstream(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"ordinary", "Hello world this is a test of word wrapping functionality"},
		{"scroll", strings.Repeat("line\n", 19) + "line"},
		{"large mixed", strings.Repeat("alpha 日本 ✅ ", 512)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				e := newUpstreamWrappingEditor()
				e.SetText(tc.text)
				e.Render(40)
				e.HandleInput("\x1b[A")
				e.Render(11)
				e.Render(40)
			}
		})
	}
}

func assertWrappingReconstruction(t *testing.T, line string, chunks []textChunk, width int) {
	t.Helper()
	var reconstructed strings.Builder
	for _, chunk := range chunks {
		if got := widthx.VisibleWidth(chunk.text); got > width {
			t.Errorf("chunk %q width = %d, max %d", chunk.text, got, width)
		}
		reconstructed.WriteString(jsstring.Slice(line, chunk.startIndex, chunk.endIndex))
	}
	if got := reconstructed.String(); got != line {
		t.Errorf("reconstructed = %q, want %q", got, line)
	}
}
