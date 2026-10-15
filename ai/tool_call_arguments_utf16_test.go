package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi's parseToolCallArguments (packages/ai/src/utils/json-parse.ts:106-119) embeds V8's JSON.parse message, which names the
// rejected code unit and quotes a 20-unit excerpt of the text by UTF-16 units: a lone surrogate stays a lone surrogate (Node 24
// probe of parseToolCallArguments, each expected message below). jsstring.FromUTF16 spells a lone surrogate in WTF-8.
func TestParseToolCallArgumentsErrorKeepsUTF16Units(t *testing.T) {
	u := func(units ...uint16) string { return jsstring.FromUTF16(units) }
	for _, tc := range []struct{ input, want string }{
		{"😀23", "Unexpected token '" + u(0xd83d) + "', \"😀23\" is not valid JSON"},
		{u(0xd83d) + "}", "Unexpected token '" + u(0xd83d) + "', \"" + u(0xd83d) + "}\" is not valid JSON"},
		{"[1,1,1,1,1,1,1,1,1,1,😀1111111😀x", "Unexpected token '" + u(0xd83d) + "', ...\"1,1,1,1,1,😀1111111" + u(0xd83d) + "\"... is not valid JSON"},
	} {
		call := ParseToolCallArguments(tc.input)
		if want := tc.want + "\n\nReceived arguments:\n" + tc.input; call.ArgumentsError != want {
			t.Errorf("%+q: ArgumentsError %+q, want %+q", tc.input, call.ArgumentsError, want)
		}
	}
}
