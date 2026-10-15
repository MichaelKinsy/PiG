package tui

import (
	"bytes"
	"testing"
)

// terminal.ts write(data) is process.stdout.write(data): Node encodes the JavaScript string as UTF-8 and writes U+FFFD for a lone surrogate
// (`node -e "process.stdout.write('a\ud83db')" | xxd` prints 61 ef bf bd 62). PiG carries that unit as WTF-8 (ED A0 BD), which no terminal decodes, so the
// terminal sink converts it; a String.slice that splits an astral character (a prompt-template description, an MCP summary) reaches the screen this way.
func TestProcessTerminalWritesALoneSurrogateAsNodeDoes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"lone high surrogate", "a\xed\xa0\xbdb", "a\xef\xbf\xbdb"},
		{"lone low surrogate at the end", "x\xed\xb8\x80", "x\xef\xbf\xbd"},
		{"valid text is unchanged", "a\U0001F600é\x1b[0m", "a\U0001F600é\x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewProcessTerminalWithOutput(nil, nil, &out)
			terminal.Write(tc.in)
			if got := out.String(); got != tc.want {
				t.Fatalf("Write(%q) wrote %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
