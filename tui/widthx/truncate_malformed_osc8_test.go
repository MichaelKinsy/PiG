package widthx

import "testing"

// Upstream parseOsc8Hyperlink ignores an OSC 8 without the params/URL
// separator, including an empty BEL-terminated body.
func TestTruncateToWidthMalformedOsc8(t *testing.T) {
	for _, terminator := range []string{"\x07", "\x1b\\"} {
		t.Run(terminator, func(t *testing.T) {
			prefix := "\x1b]8;" + terminator
			got := TruncateToWidth(prefix+"abcdef", 4, "", false)
			want := prefix + "abcd\x1b[0m"
			if got != want {
				t.Fatalf("got %q, want upstream %q", got, want)
			}
		})
	}
}
