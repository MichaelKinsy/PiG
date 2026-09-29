package widthx

import (
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
