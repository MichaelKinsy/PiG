package jv

import "testing"

// AppendConcat is `a + b` on a byte buffer: a lone high surrogate that ends dst and a lone low surrogate that starts text
// join into one code point at any boundary of the UTF-16 sequence.
func TestAppendConcatJoinsSurrogatesAtTheBoundary(t *testing.T) {
	high, _ := ParseString(`"\ud83d"`)
	low, _ := ParseString(`"\ude00"`)
	cases := []struct{ name, dst, text, want string }{
		{"pair split between buffer and text", "ab" + high.(string), low.(string) + "cd", "ab😀cd"},
		{"text is only the low half", high.(string), low.(string), "😀"},
		{"high half then a whole pair", high.(string), "😀", high.(string) + "😀"},
		{"whole pair then low half", "😀", low.(string), "😀" + low.(string)},
		{"low half then high half stay apart", low.(string), high.(string), low.(string) + high.(string)},
		{"short text", "😀" + high.(string), "x", "😀" + high.(string) + "x"},
		{"empty buffer", "", low.(string) + "é", low.(string) + "é"},
		{"no surrogate", "aé", "b😀", "aéb😀"},
	}
	for _, tc := range cases {
		if got := string(AppendConcat([]byte(tc.dst), tc.text)); got != tc.want {
			t.Errorf("%s: AppendConcat(%s, %s) = %s, want %s", tc.name, Stringify(tc.dst), Stringify(tc.text), Stringify(got), Stringify(tc.want))
		}
		if want := Concat(tc.dst, tc.text); want != tc.want {
			t.Errorf("%s: Concat = %s, want %s (the table)", tc.name, Stringify(want), Stringify(tc.want))
		}
	}
}
