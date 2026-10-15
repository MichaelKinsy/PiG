package codingagent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The session listing cuts a first message to 200 bytes; a cut inside a multi-byte character must not leave invalid UTF-8 in the transcript.
func TestTruncateKeepsWholeCharacters(t *testing.T) {
	for _, filler := range []string{"é", "小", "😀"} {
		for pad := range 4 {
			text := strings.Repeat("a", pad) + strings.Repeat(filler, 100)
			got := truncate(text, 10)
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q+%q*100, 10) = %q is not valid UTF-8", strings.Repeat("a", pad), filler, got)
			}
			if !strings.HasSuffix(got, "…") || len(got) > 10+len("…") {
				t.Errorf("truncate(%q+%q*100, 10) = %q: want at most 10 bytes and an ellipsis", strings.Repeat("a", pad), filler, got)
			}
		}
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short) = %q", got)
	}
	if got := truncate("0123456789", 10); got != "0123456789" {
		t.Errorf("truncate at the limit = %q", got)
	}
}
