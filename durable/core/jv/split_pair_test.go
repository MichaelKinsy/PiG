package jv

import "testing"

// JavaScript joins a high and a low surrogate that end up adjacent into one code point, so JSON.stringify prints the character
// (node: JSON.stringify("\ud83d" + "\ude00") === '"😀"'). A Go string that concatenates two WTF-8 halves holds that pair as two
// three-byte sequences; only an unpaired unit prints as \uXXXX (node: "\ude00\ud83d" and "\ud83d😀").
func TestAppendStringJoinsASplitSurrogatePair(t *testing.T) {
	const high, low = "\xed\xa0\xbd", "\xed\xb8\x80" // WTF-8 of U+D83D and U+DE00
	for _, tc := range []struct{ in, want string }{
		{high + low, `"😀"`},
		{"a" + high + low + "b", `"a😀b"`},
		{low + high, `"\ude00\ud83d"`},
		{high + high + low, `"\ud83d😀"`},
		{high, `"\ud83d"`},
	} {
		if got := string(AppendString(nil, tc.in)); got != tc.want {
			t.Errorf("AppendString(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
