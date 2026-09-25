package ai

import (
	"strings"
	"testing"
)

// JSON.stringify leaves U+2028 and U+2029 literal, while encoding/json
// escapes them as backslash-u sequences. The estimator counts
// JSON.stringify's length (estimate.ts safeJsonStringify).
func TestEstimateCountsJSONStringifyLength(t *testing.T) {
	lineSeparator := string(rune(0x2028))
	paragraphSeparator := string(rune(0x2029))
	backslash := `\`
	cases := []struct {
		name  string
		value string
		want  int // UTF-16 length of JSON.stringify({x: value})
	}{
		{"line and paragraph separators", strings.Repeat(lineSeparator+paragraphSeparator, 2), len(`{"x":""}`) + 4},
		// A literal backslash-u-2028 text is escaped as two backslashes in
		// both encoders.
		{"literal escape text", backslash + "u2028", len(`{"x":""}`) + 7},
		{"backspace and form feed", "\b\f", len(`{"x":""}`) + 4},
		{"other control character", "\x01", len(`{"x":""}`) + 6},
		{"html characters", "<&>", len(`{"x":""}`) + 3},
	}
	for _, tc := range cases {
		if got := JSONStringifyLength(JsonObject{"x": tc.value}); got != tc.want {
			t.Errorf("%s: JSON.stringify length = %d, want %d", tc.name, got, tc.want)
		}
	}
	separators := AssistantMessage{Content: []AssistantContentBlock{ToolCall{Name: "f", Arguments: JsonObject{"x": strings.Repeat(lineSeparator, 4)}}}}
	// f plus the 12-unit JSON text is 13 units, 4 tokens.
	if got := EstimateMessageTokens(separators); got != 4 {
		t.Fatalf("tokens = %d, want 4", got)
	}
}
