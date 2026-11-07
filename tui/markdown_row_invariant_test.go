package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The renderer now wraps any over-wide row, but that is a backstop. Content the
// agent emits constantly should already respect the one-row-per-line invariant,
// because relying on the backstop means the wrap point is chosen by a generic
// splitter rather than by the component that knows the content's structure.
//
// These use real markdown rather than synthetic strings: a fixed-width code
// block, a long URL and a wide table are the three shapes that historically
// overflowed, and each reaches a different branch of the renderer.
func TestRealMarkdownNeverEmitsAnOverWideRow(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name: "code block far past the terminal",
			content: "```go\n" +
				"func main() { fmt.Println(\"a line of code that runs well past any sensible terminal width\") }\n" +
				"```",
		},
		{
			name:    "unbreakable url",
			content: "See https://example.com/" + strings.Repeat("segment/", 20) + "end",
		},
		{
			name: "wide table",
			content: "| column one | column two | column three |\n" +
				"| --- | --- | --- |\n" +
				"| a value that is long | another long value | a third long value |",
		},
		{
			name:    "deeply indented list",
			content: "- " + strings.Repeat("nested ", 30) + "\n  - " + strings.Repeat("deeper ", 30),
		},
	}

	// Narrow widths are where overflow shows. 40 is a split pane, 20 a phone.
	for _, width := range []int{20, 40, 80} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for i, line := range NewMarkdown(tc.content).Render(width) {
					if w := widthx.VisibleWidth(line); w > width {
						t.Errorf("at width %d, row %d is %d columns: %q\n"+
							"an over-wide row occupies two physical rows, so the "+
							"renderer's row count understates the screen",
							width, i, w, line)
					}
				}
			})
		}
	}
}
