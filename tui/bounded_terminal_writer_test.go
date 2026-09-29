package tui

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Pi BoundedTerminalWriter keeps the surrogate pair together when a chunk would split an astral character.
func TestBoundedTerminalWriterPreservesUTF16Boundary(t *testing.T) {
	prefix := strings.Repeat("a", maxRenderWriteChars-1)
	var writes []string
	writer := boundedTerminalWriter{write: func(s string) { writes = append(writes, s) }}
	writer.WriteString(prefix + "🙂tail")
	writer.flush()
	if !slices.Equal(writes, []string{prefix, "🙂tail"}) {
		t.Fatalf("chunk boundary differs: lengths %v", func() []int {
			var lengths []int
			for _, s := range writes {
				lengths = append(lengths, len(s))
			}
			return lengths
		}())
	}
	for _, s := range writes {
		if !utf8.ValidString(s) {
			t.Fatal("write split a Unicode code point")
		}
	}
}

// The appended blank row expands the changed range back to the image header; it is not an append-start frame anymore (tui-main-screen.ts:393-399).
func TestKittyReservedGrowthRecomputesAppendStart(t *testing.T) {
	h := newUpstreamRenderHarness(t, 40, 5)
	image := "\x1b_Ga=T,f=100,q=2,C=1,c=2,r=3,i=89;AAAA\x1b\\"
	h.render([]string{image, ""})
	writes := h.render([]string{image, "", ""})
	want := "\x1b[?2026h" + DeleteKittyImage(89) + "\x1b[1A\r\x1b[2K\r\n\x1b[2K\r\n\x1b[2K\x1b[2A" + image + "\x1b[2B\x1b[?2026l\x1b[?25l"
	if writes != want {
		t.Fatalf("reserved-growth frame=%q, want %q", writes, want)
	}
}
