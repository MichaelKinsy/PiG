package tui

import (
	"strings"
	"testing"
)

// Pi 0.87.1 packages/tui/src/components/markdown.ts:463-486 applies theme.bold at every heading depth.
func TestMarkdownEveryHeadingDepthIsBold(t *testing.T) {
	for depth := 1; depth <= 6; depth++ {
		line := NewMarkdown(strings.Repeat("#", depth) + " Heading").Render(80)[0]
		prefix, _, _ := strings.Cut(line, "Heading")
		if !strings.Contains(prefix, "\x1b[1m") && !strings.Contains(prefix, "\x1b[1;4m") {
			t.Errorf("heading depth %d lacks bold: %q", depth, line)
		}
	}
}
