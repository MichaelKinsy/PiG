package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestUpstreamTruncatedText(t *testing.T) {
	cases := []struct {
		name, text                string
		paddingX, paddingY, width int
		contains, excludes        []string
	}{
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:11
		{"pads output lines to exactly match width", "Hello world", 1, 0, 50, nil, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:23
		{"pads output with vertical padding lines to width", "Hello", 0, 2, 40, nil, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:36
		{"truncates long text and pads to width", "This is a very long piece of text that will definitely exceed the available width", 1, 0, 30, []string{"..."}, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:51
		{"preserves ANSI codes in output and pads correctly", "\x1b[31mHello\x1b[39m \x1b[34mworld\x1b[39m", 1, 0, 40, []string{"\x1b["}, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:65
		{"truncates styled text and adds reset code before ellipsis", "\x1b[31mThis is a very long red text that will be truncated\x1b[39m", 1, 0, 20, []string{"\x1b[0m..."}, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:79
		{"handles text that fits exactly", "Hello world", 1, 0, 30, nil, []string{"..."}},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:93
		{"handles empty text", "", 1, 0, 30, nil, nil},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:101
		{"stops at newline and only shows first line", "First line\nSecond line\nThird line", 1, 0, 40, []string{"First line"}, []string{"Second line", "Third line"}},
		// .upstream/v0.87.1/packages/tui/test/truncated-text.test.ts:116
		{"truncates first line even with newlines in text", "This is a very long first line that needs truncation\nSecond line", 1, 0, 25, []string{"..."}, []string{"Second line"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			lines := NewPaddedTruncatedText(tt.text, tt.paddingX, tt.paddingY).Render(tt.width)
			if len(lines) != 2*tt.paddingY+1 {
				t.Fatalf("lines = %q, want %d rows", lines, 2*tt.paddingY+1)
			}
			for _, line := range lines {
				if got := widthx.VisibleWidth(line); got != tt.width {
					t.Errorf("width(%q) = %d, want %d", line, got, tt.width)
				}
			}
			for _, part := range tt.contains {
				if !strings.Contains(lines[tt.paddingY], part) {
					t.Errorf("%q missing %q", lines, part)
				}
			}
			for _, part := range tt.excludes {
				if strings.Contains(lines[tt.paddingY], part) {
					t.Errorf("%q contains %q", lines, part)
				}
			}
		})
	}
}
