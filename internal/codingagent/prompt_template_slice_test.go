package codingagent

import (
	"strings"
	"testing"
)

// JavaScript's String.prototype.slice cuts between the two units of an astral character and keeps the high surrogate; a PiG string carries it as WTF-8 (U+D83D is ED A0 BD), which the session JSON writes as \ud83d and a terminal or stream write sends as U+FFFD.
// packages/coding-agent/src/core/prompt-templates.ts:137 `firstLine.slice(0, 60)`. A decode through unicode/utf16 replaced it with U+FFFD (EF BF BD).
func TestPromptTemplateDescriptionSliceKeepsJavaScriptUnits(t *testing.T) {
	line := strings.Repeat("a", 59) + "\U0001F600" + "zz"
	want := strings.Repeat("a", 59) + "\xed\xa0\xbd" + "..."
	if got := truncatePromptDescription(line); got != want {
		t.Fatalf("truncatePromptDescription = %q, want %q", got, want)
	}
}
