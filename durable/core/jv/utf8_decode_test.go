package jv

import (
	"encoding/hex"
	"testing"
)

// JSON.parse reads a string, so bytes reach it through a WHATWG UTF-8 decode that replaces each maximal ill-formed
// subpart with U+FFFD. Expectations from Node 24: JSON.stringify(JSON.parse(Buffer.from(hex, "hex").toString())).
func TestParseDecodesIllFormedUTF8LikeNode(t *testing.T) {
	for _, tc := range []struct{ hex, want string }{
		{"22eda3b3edb3b322", "\"\uFFFD\uFFFD\uFFFD\uFFFD\uFFFD\uFFFD\""}, // CESU-8 surrogate pair: six subparts, not a code point
		{"22ff22", "\"\uFFFD\""},
		{"22e28222", "\"\uFFFD\""}, // truncated three-byte sequence is one subpart
		{"22e2822022", "\"\uFFFD \""},
		{"22f09f9822", "\"\uFFFD\""},
		{"22c0af22", "\"\uFFFD\uFFFD\""}, // overlong lead byte
		{"22edbfbf22", "\"\uFFFD\uFFFD\uFFFD\""},
		{"22e0809022", "\"\uFFFD\uFFFD\uFFFD\""},
		{"22f8888888888822", "\"\uFFFD\uFFFD\uFFFD\uFFFD\uFFFD\uFFFD\""},
		{"225c7564383030eda08022", "\"\\ud800\uFFFD\uFFFD\uFFFD\""}, // an escaped lone surrogate stays one
		{"22f490808022", "\"\uFFFD\uFFFD\uFFFD\uFFFD\""},            // F4 takes 80..8F second: 90 starts no subpart
		{"22f080808022", "\"\uFFFD\uFFFD\uFFFD\uFFFD\""},            // F0 takes 90..BF second: 80 starts no subpart
		{"22f4908080", ""},
		{"22f0808080", ""},
	} {
		raw, err := hex.DecodeString(tc.hex)
		if err != nil {
			t.Fatal(err)
		}
		v, err := Parse(raw)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: parsed %q, want a syntax error", tc.hex, Stringify(v))
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.hex, err)
			continue
		}
		if got := Stringify(v); got != tc.want {
			t.Errorf("%s: Stringify = %q, want %q", tc.hex, got, tc.want)
		}
	}
}
