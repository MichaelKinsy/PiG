package ai

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The arguments of a tool call count as the text JSON.stringify writes for them, whether the counter encodes them or not.
func TestJSONStringifyLengthOfToolCallArguments(t *testing.T) {
	for _, arguments := range []JsonObject{
		nil,
		{},
		{"path": "a.ts", "content": strings.Repeat("const a = \"b\";\n\t// c\\d\n", 400)},
		{"path": "a.ts", "content": "naïve café 日本語 😀"},
		{"command": "seq 1 3", "timeout": 5.0},
		{"edits": []any{map[string]any{"oldText": "a", "newText": "b"}}},
	} {
		encoded, err := jsstring.MarshalJSON(arguments)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := JSONStringifyLength(arguments), jsstring.Length(string(encoded)); got != want {
			t.Errorf("%v: got %d, want %d (%s)", arguments, got, want, encoded)
		}
	}
}
