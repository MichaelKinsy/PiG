//go:build !pig_strip_syntax_highlight

package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's interactive mode hands each tool card the partial arguments of every message_update (interactive-mode.ts handleEvent
// message_update: component.updateArgs(content.arguments)), so a streamed write call shows its first lines and the
// "(N more lines, M total" hint while its arguments arrive. The arguments here stream 16 bytes at a time, as the paced fake
// model of the pty oracle (lw/state/evidence/oracle_write.py) sends them; after every delta the card must draw what Pi
// 1.1.0's ToolExecutionComponent draws for parseStreamingJson of the same prefix, and the frame after message_end
// (setArgsComplete) must match too. The cases cover a highlighted file inside the 50-line leading block, one that grows
// past it (expanded, so every line is drawn), and a path without a language, whose lines carry carriage returns and end
// in blank lines.
func TestInteractiveStreamedWriteCardMatchesPiOracle(t *testing.T) {
	useTreeOracleTUIDefaults(t)
	restoreStartupTheme(t)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: true})

	var leading strings.Builder
	for line := range 32 {
		fmt.Fprintf(&leading, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%7, line)
	}
	leading.WriteString("\tconst tab = \"quote \\\" and é\";\n")
	var long strings.Builder
	for line := range writePartialFullHighlightLines + 8 {
		fmt.Fprintf(&long, "const v%d = %d; // step %d\n", line, line, line)
	}
	long.WriteString("/* a comment\n\tthat spans `lines` past the leading block\n*/\nexport const last = \"done\";\n\n")
	var plain strings.Builder
	for line := range 14 {
		fmt.Fprintf(&plain, "note %d:\tsome text é\r\n", line)
	}
	plain.WriteString("\r\n\n\r\n")
	cases := []struct {
		name, path, content string
		expanded            bool
	}{
		{"typescript inside the leading block", "src/stream.ts", leading.String(), false},
		{"typescript past the leading block expanded", "src/long.ts", long.String(), true},
		{"plain text", "notes/plain.txt", plain.String(), false},
		{"plain text expanded", "notes/plain.txt", plain.String(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkStreamedWriteCardAgainstPi(t, tc.path, tc.content, tc.expanded)
		})
	}
}
