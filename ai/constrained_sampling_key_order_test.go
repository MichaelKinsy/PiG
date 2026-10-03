package ai

import (
	"encoding/json"
	"testing"
)

// constrained-sampling.ts:65-70 prints the unsupported keyword with JSON.stringify: authored key order at every depth,
// array-index keys first in ascending order (V8 property enumeration), and no HTML escaping.
func TestStrictRequireReasonStringifiesLikeJSON(t *testing.T) {
	cases := []struct{ name, format, want string }{
		{"nested objects", `{"z":{"y":1,"b":[{"d":1,"c":2}]},"a":null}`, `{"z":{"y":1,"b":[{"d":1,"c":2}]},"a":null}`},
		{"array index keys enumerate first", `{"b":1,"10":2,"2":3,"a":4}`, `{"2":3,"10":2,"b":1,"a":4}`},
		{"no HTML escaping", `{"b":"<a&b>","a":1}`, `{"b":"<a&b>","a":1}`},
		{"sorted source stays sorted", `{"a":1,"b":2}`, `{"a":1,"b":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tool ToolSchema
			data := `{"name":"t","description":"","parameters":{"type":"object","properties":{"a":{"type":"string","format":` + tc.format + `}},"required":["a"]},"constrainedSampling":{"type":"json_schema","strict":"require"}}`
			if err := json.Unmarshal([]byte(data), &tool); err != nil {
				t.Fatal(err)
			}
			_, err := resolveJSONSchemaStrictSampling(tool, true, anthropicStrictUnsupportedKeyword)
			want := `Tool "t" requires JSON-schema constrained sampling, but format: ` + tc.want + ` is unsupported.`
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant %s", err, want)
			}
		})
	}
}

// Probes (node 24): JSON.stringify(JSON.parse(`{"a":{"y":1,"x":2},"b":0,"a":{"x":1,"y":2}}`)) === `{"a":{"x":1,"y":2},"b":0}`:
// a duplicate key keeps its first position but takes the last value with that value's own key order. JSON.stringify
// writes U+2028 and U+2029 raw (ES2019 well-formed JSON.stringify) and never HTML-escapes a key or a string.
func TestStrictRequireReasonStringifiesLikeJSONEdgeCases(t *testing.T) {
	cases := []struct{ name, format, want string }{
		{"duplicate key takes the last value's order", `{"y":1,"x":2},"format":{"x":1,"y":2}`, `{"x":1,"y":2}`},
		{"nested duplicate key takes the last value's order", `{"k":{"y":1,"x":2},"b":0,"k":{"x":1,"y":2}}`, `{"k":{"x":1,"y":2},"b":0}`},
		{"line terminators stay raw", `{"b":"x\u2028y\u2029z","a":"\\u2028"}`, "{\"b\":\"x\u2028y\u2029z\",\"a\":\"\\\\u2028\"}"},
		{"string value line terminator", `"x\u2028"`, "\"x\u2028\""},
		{"keys are not HTML-escaped", `{"<b&>":1,"a":2}`, `{"<b&>":1,"a":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tool ToolSchema
			data := `{"name":"t","description":"","parameters":{"type":"object","properties":{"a":{"type":"string","format":` + tc.format + `}},"required":["a"]},"constrainedSampling":{"type":"json_schema","strict":"require"}}`
			if err := json.Unmarshal([]byte(data), &tool); err != nil {
				t.Fatal(err)
			}
			_, err := resolveJSONSchemaStrictSampling(tool, true, anthropicStrictUnsupportedKeyword)
			want := `Tool "t" requires JSON-schema constrained sampling, but format: ` + tc.want + ` is unsupported.`
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant %s", err, want)
			}
		})
	}
}

// constrained-sampling.ts appendGrammarToolInputJsonDelta builds deltas with JSON.stringify. Probe (node 24, Pi 0.99.2
// source): property "p<\u2029", inputs "a\u2028<" then "a\u2028<\\u2028" (closed) -> {"p<\u2029":"a\u2028< and \\u2028"}
// with U+2028/U+2029 raw and the literal backslash-u text escaped.
func TestAppendGrammarToolInputJSONDeltaWritesLineTerminatorsRaw(t *testing.T) {
	buffer := &grammarToolInputJSONBuffer{}
	first, _, err := appendGrammarToolInputJSONDelta(buffer, "p<\u2029", "a\u2028<", false)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := appendGrammarToolInputJSONDelta(buffer, "p<\u2029", `a`+"\u2028"+`<\u2028`, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"p<\u2029\":\"a\u2028<"; first != want {
		t.Fatalf("first delta = %q, want %q", first, want)
	}
	if want := `\\u2028"}`; second != want {
		t.Fatalf("second delta = %q, want %q", second, want)
	}
}
