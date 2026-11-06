package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/tui/src/tui-main-screen.ts BoundedTerminalWriter

// An unpaired surrogate is one UTF-16 unit that PiG carries as three bytes: it fills one unit of a write, and a write never ends inside it.
func TestBoundedTerminalWriterCountsLoneSurrogateAsOneUnit(t *testing.T) {
	lone := jsstring.FromUTF16([]uint16{0xd83d})
	var writes []string
	w := boundedTerminalWriter{write: func(data string) { writes = append(writes, data) }}
	w.append(strings.Repeat("x", maxRenderWriteChars-1) + lone + "y")
	w.flush()
	if len(writes) != 2 || writes[0] != strings.Repeat("x", maxRenderWriteChars-1)+lone || writes[1] != "y" {
		t.Fatalf("writes = %d, first %d bytes, want the surrogate to end the first write and y to start the second", len(writes), len(writes[0]))
	}
}
