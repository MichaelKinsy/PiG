package parity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func captureMarkdownRender(t *testing.T, rows, cols int, src string, caps tui.TerminalCapabilities) (*bytes.Buffer, func()) {
	t.Helper()
	oldCaps := tui.GetCapabilities()
	tui.SetCapabilities(caps)
	var buf bytes.Buffer
	ti := tui.NewWithOutput(&buf, cols, rows)
	ti.Add(tui.NewMarkdown(src))
	ti.Render()
	cleanup := func() {
		tui.SetCapabilities(oldCaps)
	}
	return &buf, cleanup
}

func TestParityMarkdown_HyperlinkOSC8(t *testing.T) {
	src := "Visit [example docs](https://example.com/docs) or email user@example.com"
	buf, cleanup := captureMarkdownRender(t, 6, 70, src, tui.TerminalCapabilities{Hyperlinks: true})
	defer cleanup()
	raw := buf.String()
	if !strings.Contains(raw, "\x1b]8;;https://example.com/docs\x1b\\") {
		t.Fatalf("missing OSC 8 sequence for markdown link: %q", raw)
	}
	if !strings.Contains(raw, "\x1b]8;;mailto:user@example.com\x1b\\") {
		t.Fatalf("missing OSC 8 sequence for email autolink: %q", raw)
	}
	if strings.Contains(raw, "(https://example.com/docs)") {
		t.Fatalf("hyperlink-capable render should not print parenthetical URL: %q", raw)
	}
}
