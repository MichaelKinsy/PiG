package ai

import (
	"strings"
	"testing"
)

// JavaScript's String.prototype.slice cuts between the two units of an astral character and keeps the high surrogate; a PiG string carries it as WTF-8 (U+D83D is ED A0 BD), which the session JSON writes as \ud83d and a terminal or stream write sends as U+FFFD.
// The Anthropic SDK credentials error body slice (node_modules/@anthropic-ai/sdk/lib/credentials/types.mjs MAX_ERROR_BODY_CHARS). A decode through unicode/utf16 replaced it with U+FFFD (EF BF BD).
func TestAnthropicTokenErrorBodySliceKeepsJavaScriptUnits(t *testing.T) {
	body := strings.Repeat("a", 1999) + "\U0001F600" + "bbb"
	want := strings.Repeat("a", 1999) + "\xed\xa0\xbd" + "... <4 more chars>"
	if got := truncateAnthropicTokenText(body, utf16Length(body)); got != want {
		t.Fatalf("truncateAnthropicTokenText = %q, want %q", got, want)
	}
}
