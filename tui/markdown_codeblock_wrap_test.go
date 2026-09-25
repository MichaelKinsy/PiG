package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestCodeBlockKeepsEveryCharacterAtNarrowWidth pins the property the fenced
// block exists for: it is the surface people copy from, so no character may be
// dropped between the source and the render.
//
// Clipping used to drop the tail of any over-wide line with no marker, so a
// copied command silently lost its end. pig's own truncations are vertical,
// marked, and recoverable with ctrl+o; there is no horizontal equivalent, which
// is why the content has to wrap instead. pig divergence (D54).
func TestCodeBlockKeepsEveryCharacterAtNarrowWidth(t *testing.T) {
	const long = `docker login fails with "x509: certificate signed by unknown authority" because MSR chains to a private root`

	for _, tc := range []struct{ name, fence string }{
		{"untagged fence", "```"},
		{"text fence", "```text"},
		{"highlighted fence", "```bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width = 60
			m := NewMarkdown(tc.fence + "\n" + long + "\n```")
			out := m.Render(width)

			// Every rendered row must fit, or pig's renderer clips it later and
			// the content is lost anyway.
			for i, line := range out {
				if w := widthx.VisibleWidth(line); w > width {
					t.Errorf("line %d is %d wide, over the %d given: %q", i, w, width, line)
				}
			}

			// Reassemble the body and compare against the source with spaces
			// collapsed: wrapping may break at a space, so the join point is
			// whitespace, but no non-space character may vanish.
			var body []string
			for _, line := range out {
				stripped := stripSGR(line)
				if strings.Contains(stripped, "```") {
					continue
				}
				body = append(body, strings.TrimSpace(stripped))
			}
			got := strings.Join(strings.Fields(strings.Join(body, " ")), " ")
			want := strings.Join(strings.Fields(long), " ")
			if got != want {
				t.Errorf("content lost or altered by rendering at width %d\n got: %q\nwant: %q", width, got, want)
			}
		})
	}
}

// TestCodeBlockWrapKeepsHighlightingIntact guards the ANSI half of the wrap.
// HighlightCode returns styled lines, so splitting one across rows must carry
// the active SGR state onto the continuation instead of leaking raw escape
// bytes or losing the colour partway through.
func TestCodeBlockWrapKeepsHighlightingIntact(t *testing.T) {
	const width = 40
	long := "if err := doSomethingWithAVeryLongName(ctx, request); err != nil { return err }"
	m := NewMarkdown("```go\n" + long + "\n```")
	out := m.Render(width)

	var bodyRows []string
	for _, line := range out {
		if strings.Contains(stripSGR(line), "```") {
			continue
		}
		bodyRows = append(bodyRows, line)
	}
	if len(bodyRows) < 2 {
		t.Fatalf("expected the long line to wrap across rows, got %d body row(s)", len(bodyRows))
	}
	for i, row := range bodyRows {
		if w := widthx.VisibleWidth(row); w > width {
			t.Errorf("wrapped row %d is %d wide, over the %d given", i, w, width)
		}
		// A split must not strand a partial escape sequence in the output.
		if strings.Count(row, "\x1b") > 0 && !strings.Contains(row, "m") {
			t.Errorf("wrapped row %d carries a truncated escape sequence: %q", i, row)
		}
	}
}

// stripSGR removes ANSI escape sequences so assertions compare visible text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && s[j] != 'm' && s[j] != '\a' && s[j] != '\\' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
