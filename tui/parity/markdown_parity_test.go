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

func TestParityMarkdown_GoldenBlocksAndLists(t *testing.T) {
	src := strings.Join([]string{
		"# Heading",
		"",
		"> first quote line",
		"continued quote line",
		"> - quoted bullet",
		"",
		"1. first item",
		"   1. nested ordered",
		"   - nested bullet",
		"",
		"| Name | Value |",
		"| --- | ---: |",
		"| alpha | 10 |",
		"| beta | `wrapped code sample` |",
		"",
		"Read [docs](https://example.com/docs)",
	}, "\n")

	render := func(w *bytes.Buffer) {
		buf, cleanup := captureMarkdownRender(t, 18, 52, src, tui.TerminalCapabilities{Hyperlinks: false})
		defer cleanup()
		w.Write(buf.Bytes())
	}

	AssertGolden(t, "markdown-blocks-and-lists", 18, 52, render)
}

func TestParityMarkdown_HyperlinkVisibleGoldenAndOSC8(t *testing.T) {
	src := "Visit [example docs](https://example.com/docs) or email user@example.com"
	render := func(w *bytes.Buffer) {
		buf, cleanup := captureMarkdownRender(t, 6, 70, src, tui.TerminalCapabilities{Hyperlinks: true})
		defer cleanup()
		w.Write(buf.Bytes())
	}

	AssertGolden(t, "markdown-hyperlinks-visible", 6, 70, render)

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
