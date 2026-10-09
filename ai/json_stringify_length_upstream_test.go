package ai

import (
	"math"
	"testing"
)

// estimate.ts safeJsonStringify sizes a tool call's arguments by `JSON.stringify(value).length`: negative zero is "0", <, > and & are not escaped,
// a byte that is not UTF-8 is the one-unit U+FFFD, and a backslash before the text "ufffd" stays six characters.
func TestJSONStringifyLengthCountsWhatJSONStringifyWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		value any
		want  int
	}{
		"negative zero":        {map[string]any{"a": math.Copysign(0, -1)}, len(`{"a":0}`)},
		"html characters":      {"<&>", len(`"<&>"`)},
		"invalid utf-8 byte":   {"a\xffb", 5},
		"invalid byte in key":  {map[string]any{"a": "\xff"}, 9},
		"escaped backslash":    {`\ufffd`, 9},
		"line separators":      {"\u2028\u2029", 4},
		"astral counts two":    {"😀", 4},
		"large number":         {1e21, len("1e+21")},
		"unserializable value": {make(chan int), len("[unserializable]")},
		"nan is null":          {math.NaN(), len("null")},
		"nested object":        {map[string]any{"o": map[string]any{"z": []any{1.5, nil}}}, len(`{"o":{"z":[1.5,null]}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := JSONStringifyLength(tc.value); got != tc.want {
				t.Errorf("JSONStringifyLength = %d, want %d", got, tc.want)
			}
		})
	}
}
