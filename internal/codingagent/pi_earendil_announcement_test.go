package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// pi: packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts
//
// EarendilAnnouncementComponent stacks an accent border, the bold accent title (1 column of left padding), a spacer, "Read the blog post:", the blog URL, a spacer,
// the bundled clankolas.png (640x537, shown as terminal-image fallback text without image support) and a spacer, and an accent border (earendil-announcement.ts:
// 27-47; fallback text from terminal-image.ts:750-762). Pi has no test of this file.
func TestPiCodingAgentSrcModesInteractiveComponentsEarendilAnnouncement(t *testing.T) {
	caps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(caps) })
	tui.SetCapabilities(tui.TerminalCapabilities{})
	const width = 60
	lines := newEarendilAnnouncementComponent().Render(width)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = strings.TrimRight(stripANSITest(line), " ")
	}
	border := strings.Repeat("─", width)
	want := []string{
		border,
		" pi has joined Earendil",
		"",
		" Read the blog post:",
		" https://mariozechner.at/posts/2026-04-08-ive-sold-out/",
		"",
		"[Image: clankolas.png [image/png] 640x537]",
		"",
		border,
	}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("announcement lines:\n%q\nwant\n%q", plain, want)
	}
	if !strings.Contains(lines[1], "\x1b[1m") {
		t.Fatalf("title %q is not bold", lines[1])
	}
}
