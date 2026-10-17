package jsonstringify

import "testing"

// JSON.stringify(JSON.parse(text)) for numbers out of double range, ill-formed UTF-8 inside an escaped string, and escaped surrogates. The text
// arrives as bytes Node decodes first: one U+FFFD per maximal subpart (Buffer.from([0x22, 0xf0, 0x9f, 0x98, 0x5c, 0x6e, 0x22]).toString()).
func TestCanonicalizeEdgesMatchJSONParseStringify(t *testing.T) {
	for raw, want := range map[string]string{
		`1e-400`:                 "0",
		`-1e-400`:                "0",
		`1e400`:                  "null",
		`[1e-400,2.50,1E2]`:      "[0,2.5,100]",
		"\"\xf0\x9f\x98\\n\"":    "\"\ufffd\\n\"",
		"\"\xff\\u00e9\"":        "\"\ufffd\u00e9\"",
		`"\ud83d\ude00\ud800"`:   "\"😀\\ud800\"",
		`"\uD800\u0041\/\u001F"`: `"\ud800A/\u001f"`,
	} {
		got, err := Canonicalize([]byte(raw))
		if err != nil || string(got) != want {
			t.Errorf("Canonicalize(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}
