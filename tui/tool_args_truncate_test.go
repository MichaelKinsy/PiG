package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// A header's argument preview is cut to a byte budget; the cut must land between characters. Slicing at the byte offset split a multi-byte character, so a
// non-ASCII argument longer than the budget showed the literal text \ufffd where the character was cut.
func TestFormatToolArgsTruncatesAtACharacterBoundary(t *testing.T) {
	for _, char := range []string{"é", "日", "🙂"} {
		for offset := range 4 {
			value := strings.Repeat("a", offset) + strings.Repeat(char, 60)
			raw, _ := json.Marshal(map[string]string{"text": value})
			got := FormatToolArgs(raw)
			// The JSON quoting of a split character prints it as the literal text \ufffd.
			if !utf8.ValidString(got) || strings.Contains(got, `\ufffd`) || strings.ContainsRune(got, utf8.RuneError) {
				t.Errorf("%q x60 after %d bytes: %q holds a split character", char, offset, got)
			}
			if len(got) > 80+len("\u2026") {
				t.Errorf("%q: %d bytes, over the budget", char, len(got))
			}
		}
	}
	if got := truncateArg("short", 80); got != "short" {
		t.Errorf("short argument changed: %q", got)
	}
	if got := truncateArg(strings.Repeat("x", 100), 80); got != strings.Repeat("x", 79)+"\u2026" {
		t.Errorf("ASCII cut: %q", got)
	}
}
