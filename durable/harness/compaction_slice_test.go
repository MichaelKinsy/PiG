package harness

import (
	"strings"
	"testing"
)

// JavaScript's String.prototype.slice cuts between the two units of an astral character and keeps the high surrogate; a PiG string carries it as WTF-8 (U+D83D is ED A0 BD), which the session JSON writes as \ud83d and a terminal or stream write sends as U+FFFD.
// packages/durable/src/harness/compaction.ts tool-result truncation `text.slice(0, maxChars)`. A decode through unicode/utf16 replaced it with U+FFFD (EF BF BD).
func TestCompactionToolOutputSliceKeepsJavaScriptUnits(t *testing.T) {
	text := strings.Repeat("a", 9) + "\U0001F600" + "zz"
	want := strings.Repeat("a", 9) + "\xed\xa0\xbd" + "\n\n[... 3 more characters truncated]"
	if got := truncateUTF16(text, 10); got != want {
		t.Fatalf("truncateUTF16 = %q, want %q", got, want)
	}
}
