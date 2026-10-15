package ai

import (
	"reflect"
	"testing"
)

// partial-json 0.1.7 parseObj (dist/index.js:118-141) skips one UTF-16 unit after a member key as "the colon", whatever it is, so Pi's
// parseStreamingJson reads a member whose colon is a fullwidth colon or any other single character (Node 24 probe of
// parseStreamingJson, each expected value below). A character outside the BMP is two units: the skip leaves a lone surrogate and the read stops.
func TestParseStreamingJsonSkipsOneCharacterForTheColon(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  JsonObject
	}{
		{`{"a"：1}`, JsonObject{"a": float64(1)}},
		{`{"path"：","x":"y"}`, JsonObject{"path": ","}},
		{`{"a"é"z","b":2}`, JsonObject{"a": "z", "b": float64(2)}},
		{`{"a"：1,"b"：[1,2`, JsonObject{"a": float64(1), "b": []any{float64(1), float64(2)}}},
		{`{"a":1,"b"€2}`, JsonObject{"a": float64(1), "b": float64(2)}},
		{`{"a"：1,"b"：{"c"：true`, JsonObject{"a": float64(1), "b": map[string]any{"c": true}}},
		{`{"a"😀1,"b":2}`, JsonObject{}},
		{`{"a" 1}`, JsonObject{}},
	} {
		if got := ParseStreamingJson(tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseStreamingJson(%s) = %#v, want %#v", tc.input, got, tc.want)
		}
	}
}
