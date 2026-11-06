package tui

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

// A PiG string carries a lone UTF-16 surrogate as three bytes of WTF-8. Pi's buffer.length counts it as one unit and the writer never cuts it, so
// that each write encodes whole units: counting it as three bytes of invalid UTF-8 cut it between its bytes, and the terminal got one U+FFFD per
// piece where Pi writes one. A high surrogate directly before a low one (two lone units that Node's encoding joins) stays together too.
func TestBoundedTerminalWriterNeverCutsALoneSurrogate(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	for _, unit := range []struct{ name, text string }{{"high", high}, {"low", low}, {"high then low", high + low}, {"low then high", low + high}, {"two high", high + high}} {
		for before := maxRenderWriteChars - 4; before <= maxRenderWriteChars+1; before++ {
			frame := strings.Repeat("a", before) + unit.text + "bbb"
			var writes []string
			writer := boundedTerminalWriter{write: func(s string) { writes = append(writes, s) }}
			writer.WriteString(frame)
			writer.flush()
			var encoded strings.Builder
			for _, write := range writes {
				if units := jsstring.Length(write); units > maxRenderWriteChars {
					t.Fatalf("%s at %d: a write of %d units, over the %d limit", unit.name, before, units, maxRenderWriteChars)
				}
				encoded.WriteString(jsstring.StreamText(write))
			}
			if want := jsstring.StreamText(frame); encoded.String() != want {
				t.Fatalf("%s at %d: the encoded writes differ from the encoded frame (a unit was cut): %d bytes, want %d", unit.name, before, encoded.Len(), len(want))
			}
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
