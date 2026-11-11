package tui

import (
	"strings"
	"testing"
)

// Ported from upstream packages/tui/test/markdown.test.ts "Images" (#12017).
func TestMarkdownKeepsHardBreaksAfterInlineImageAsSeparateRows(t *testing.T) {
	oldCaps := GetCapabilities()
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	defer SetCapabilities(oldCaps)

	image := "\x1b_Ga=T,f=100,q=2,c=20,r=3,C=1;AAAA\x1b\\"
	m := NewMarkdown("`" + image + "`  \n` `  \n` `\n\n- one")
	lines := m.Render(80)
	for _, line := range lines {
		if strings.Contains(line, "\n") {
			t.Fatalf("row contains a newline: %q", line)
		}
	}
	if len(lines) == 0 || !strings.Contains(lines[0], image) {
		t.Fatalf("first row lacks the image: %q", lines)
	}
	got := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		got = append(got, strings.TrimSpace(stripANSI(line)))
	}
	want := []string{"", "", "", "- one"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows after image = %q, want %q", got, want)
	}
}
