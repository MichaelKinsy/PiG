package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// pi: packages/coding-agent/src/core/tools/renderers/write.ts refreshWriteHighlightPrefix, updateWriteHighlightCacheIncremental

// A streamed write call highlights its first 50 lines as one block after every update and every later line on its own, so a construct that starts
// before the end of the block and ends after it, or one that closes inside it long after it opened, is drawn as the block reads it. Each case streams
// 16 characters at a time and compares every frame, the one after the arguments complete included, with Pi 1.5.0's card, as
// TestInteractiveStreamedWriteCardMatchesPiOracle does: a multi-line comment opened and closed inside the block, comments and strings that open on lines
// 48 to 51 and close after line 50, and a Python docstring and a template literal across the boundary.
func TestInteractiveStreamedWriteCardMatchesPiAtTheLeadingBlockBoundary(t *testing.T) {
	useTreeOracleTUIDefaults(t)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	code := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "const v%d = %d; // step\n", i, i)
		}
		return b.String()
	}
	comment := func(before, length int) string {
		var b strings.Builder
		b.WriteString(code(before))
		b.WriteString("/* opens here\n")
		for i := range length {
			fmt.Fprintf(&b, "   still a comment %d\n", i)
		}
		b.WriteString("*/\nexport const after = \"done\";\n")
		b.WriteString(code(6))
		return b.String()
	}
	var python strings.Builder
	for i := range 48 {
		fmt.Fprintf(&python, "v%d = %d  # step\n", i, i)
	}
	python.WriteString("doc = \"\"\"opens here\n")
	for i := range 6 {
		fmt.Fprintf(&python, "still a docstring %d\n", i)
	}
	python.WriteString("\"\"\"\nafter = 1\n")
	var literal strings.Builder
	literal.WriteString(code(48))
	literal.WriteString("const t = `opens here\n")
	for i := range 6 {
		fmt.Fprintf(&literal, "still a template %d ${v%d}\n", i, i)
	}
	literal.WriteString("`;\nexport const after = 1;\n")
	for _, tc := range []struct{ name, path, content string }{
		{"comment closed inside the block", "src/a.ts", comment(2, 17)},
		{"comment opened on line 48", "src/b.ts", comment(47, 5)},
		{"comment opened on line 49", "src/c.ts", comment(48, 5)},
		{"comment opened on line 50", "src/d.ts", comment(49, 5)},
		{"comment opened on line 51", "src/e.ts", comment(50, 5)},
		{"docstring across the boundary", "tools/f.py", python.String()},
		{"template literal across the boundary", "src/g.ts", literal.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkStreamedWriteCardAgainstPi(t, tc.path, tc.content, true)
		})
	}
}
