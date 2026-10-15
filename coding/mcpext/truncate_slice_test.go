package mcpext

import (
	"strings"
	"testing"
)

// JavaScript's String.prototype.slice cuts between the two units of an astral character and keeps the high surrogate; a PiG string carries it as WTF-8 (U+D83D is ED A0 BD), which the session JSON writes as \ud83d and a terminal or stream write sends as U+FFFD.
// packages/coding-agent/src/extensions/mcp/index.ts:175 `text.slice(0, max - 1).trimEnd()`. A decode through unicode/utf16 replaced it with U+FFFD (EF BF BD).
func TestTruncateKeepsJavaScriptUnits(t *testing.T) {
	text := strings.Repeat("a", 8) + "\U0001F600" + "zz"
	want := strings.Repeat("a", 8) + "\xed\xa0\xbd" + "…"
	if got := truncateChars(text, 10); got != want {
		t.Fatalf("truncateChars = %q, want %q", got, want)
	}
}
