package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// A component must never emit a line wider than the width it was handed. The
// renderer relies on one rendered line occupying exactly one terminal row; a
// wider line either wraps (shifting everything below and leaving stale rows on
// screen) or is truncated by the overflow guard, which logs it as a component
// bug. Observed live at terminal width 194: seven prose lines at 195-196 and
// one code-block line at 486.
func TestMarkdownRender_NeverExceedsWidth(t *testing.T) {
	longCode := "const copilotRateLimitRaw = `github-copilot: GetBaseURL: github-copilot: refresh failed: run 'pig login' to re-authenticate: HTTP 403: {\"message\": \"API rate limit exceeded for user ID 186620145. If you reach out to GitHub Support for help, please include the request ID ECCC:3B9203:92655:DFCF0:6A81F1E4 and timestamp 2026-08-16 17:22:44 UTC.\"}`"

	cases := []struct {
		name    string
		content string
	}{
		{"long code block line", "```go\n" + longCode + "\n```"},
		{"long indented code line", "    " + longCode},
		{
			"prose that overflowed live",
			"All now consult handlers inline (the host is blocked on the verdict and upstream's handler is synchronous), degrade a throwing handler to not-consumed rather than swallowing the keystroke, and tell the host to start forwarding on the first subscription and stop on the last: so sessions without a subscriber are untouched. TestConformance_TerminalInput drives all three SDKs through subscribe → consume sentinel → pass ordinary key → unsubscribe.",
		},
		{"code fence with a long language label", "```" + strings.Repeat("lang", 20) + "\ncode\n```"},
		{"long unbroken token", strings.Repeat("x", 400)},
		{"long url", "See https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api#rate-limiting-and-more-and-more-and-more-and-more for details"},
		{"table wider than the terminal", "| a | b |\n| --- | --- |\n| " + strings.Repeat("y", 200) + " | " + strings.Repeat("z", 200) + " |"},
		{"list item with a long body", "- " + strings.Repeat("w", 300)},
		{"nested list continuation", "1. First\n   - " + strings.Repeat("v", 250)},
		{"blockquote", "> " + strings.Repeat("u", 300)},
	}

	for _, width := range []int{194, 80, 40, 20} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				lines := NewMarkdown(tc.content).Render(width)
				for i, line := range lines {
					if got := widthx.VisibleWidth(line); got > width {
						t.Errorf("width=%d line %d visible width %d exceeds it by %d\n  %q",
							width, i, got, got-width, widthx.StripTerminalSequences(line))
					}
				}
			})
		}
	}
}
