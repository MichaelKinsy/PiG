package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestKittyHeaderCacheMatchesColdFrames(t *testing.T) {
	old := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(old) })
	SetCapabilities(TerminalCapabilities{})
	a := "\x1b_Ga=T,r=2,i=9;AAAA\x1b\\"
	b := "\x1b_Ga=T,r=2,i=2;BBBB\x1b\\"
	frames := [][]string{{"plain"}, {"plain", a, "", "tail"}, {"plain", a, "", "tail"}, {"plain", b, "", "tail"}, {"shift", "plain", b, "", "tail"}, {"plain"}, nil, {a, "", b, ""}, {b, ""}, {a, "", b, ""}, nil}
	var cachedOut, coldOut bytes.Buffer
	cached, cold := NewWithOutput(&cachedOut, 40, 10), NewWithOutput(&coldOut, 40, 10)
	cachedChild, coldChild := &recordingComponent{}, &recordingComponent{}
	cached.Add(cachedChild)
	cold.Add(coldChild)
	for i, lines := range frames {
		cachedChild.lines, coldChild.lines = lines, lines
		cachedOut.Reset()
		coldOut.Reset()
		// Discard only the existing reset-buffer identity so the second renderer takes the pure parser path for both old and new lines.
		cold.resetInPrev, cold.resetOutPrev, cold.resetOutWork = nil, nil, nil
		cached.Render()
		cold.Render()
		if cachedOut.String() != coldOut.String() {
			t.Fatalf("frame %d cache changed bytes:\n cached=%q\n cold=%q", i, cachedOut.String(), coldOut.String())
		}
	}
}

func TestBoundedTerminalWriterCountsAstralPairs(t *testing.T) {
	first := strings.Repeat("🙂", maxRenderWriteChars/2)
	var writes []string
	w := boundedTerminalWriter{write: func(s string) { writes = append(writes, s) }}
	w.WriteString(first + "🙂")
	w.flush()
	if len(writes) != 2 || writes[0] != first || writes[1] != "🙂" {
		t.Fatal("astral characters did not consume two UTF-16 units")
	}
}
