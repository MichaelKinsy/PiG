package mcp

import (
	"strings"
	"testing"
)

// JavaScript's String.prototype.slice cuts between the two units of an astral character and keeps the high surrogate; a PiG string carries it as WTF-8 (U+D83D is ED A0 BD), which the session JSON writes as \ud83d and a terminal or stream write sends as U+FFFD.
// packages/mcp/src/transports/streamable-http.ts:178 error snippet slice. A decode through unicode/utf16 replaced it with U+FFFD (EF BF BD).
func TestHTTPFailureSnippetSliceKeepsJavaScriptUnits(t *testing.T) {
	body := strings.Repeat("a", 496) + "\U0001F600" + "zzzz"
	want := "MCP HTTP request failed with status 500: " + strings.Repeat("a", 496) + "\xed\xa0\xbd" + "..."
	if got := describeHTTPFailure(500, body); got != want {
		t.Fatalf("describeHTTPFailure = %q, want %q", got, want)
	}
}
