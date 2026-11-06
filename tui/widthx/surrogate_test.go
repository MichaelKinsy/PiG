package widthx

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestUTF16SurrogateWidthAndSegmentation(t *testing.T) {
	// Pi 0.87.1 visibleWidth treats unpaired surrogates as nonprinting. Stripping a cursor marker can join separated UTF-16 halves into one emoji again.
	high := jsstring.FromUTF16([]uint16{0xd83d})
	low := jsstring.FromUTF16([]uint16{0xde00})
	for _, tc := range []struct {
		text string
		want int
	}{{high, 0}, {low, 0}, {high + "\u0301", 0}, {high + low, 2}, {high + "\x1b[7m" + low + "\x1b[27m", 2}} {
		if got := VisibleWidth(tc.text); got != tc.want {
			t.Errorf("width(%x)=%d want=%d", []byte(tc.text), got, tc.want)
		}
	}
	text := high + "\u0301" + "x"
	first, rest := FirstGrapheme(text)
	if first != high+"\u0301" || rest != "x" {
		t.Errorf("grapheme=%x rest=%x", []byte(first), []byte(rest))
	}
}

// Upstream applyLineResets leaves an unpaired surrogate in the row and the terminal write encodes it, so the row measures as Pi measures it. Encoding it in the
// normalization made the unit one cell wide, and a row that fills the terminal measured one cell too wide.
func TestApplyLineResetKeepsUnpairedSurrogatesMeasuredAsPi(t *testing.T) {
	high := jsstring.FromUTF16([]uint16{0xd83d})
	low := jsstring.FromUTF16([]uint16{0xde00})
	for _, unit := range []string{high, low} {
		row := strings.Repeat("a", 120) + unit
		got := ApplyLineReset(row)
		if got != row+SegmentReset {
			t.Errorf("ApplyLineReset(%x) = %x, want the row unchanged before the reset", []byte(row), []byte(got))
		}
		if width := VisibleWidth(got); width != 120 {
			t.Errorf("width after ApplyLineReset = %d, want 120 (the unit is nonprinting)", width)
		}
		if got := NormalizeTerminalOutput("a\t" + unit + "\u0e33"); got != "a   "+unit+"\u0e4d\u0e32" {
			t.Errorf("NormalizeTerminalOutput = %x", []byte(got))
		}
	}
}
