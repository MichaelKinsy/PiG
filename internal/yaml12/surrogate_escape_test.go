package yaml12

import "testing"

// TestDoubleQuotedSurrogateEscapesJoinIntoOneCharacter: the yaml library builds a double-quoted scalar from UTF-16 units (String.fromCodePoint per escape),
// so a high-surrogate escape followed by a low-surrogate escape is one character outside the Basic Multilingual Plane. Pi's parseFrontmatter reads
// `a: "\uD83D\uDE00"` as {"a":"😀"}. A surrogate left unpaired has no UTF-8 form and stays U+FFFD.
func TestDoubleQuotedSurrogateEscapesJoinIntoOneCharacter(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`a: "\uD83D\uDE00"`, "😀"},
		{`a: "\uD800\uDC00"`, "\U00010000"},
		{`a: "\uDBFF\uDFFF"`, "\U0010FFFF"},
		{`a: "\U0000D83D\uDE00"`, "😀"},
		{`a: "\uD83D\U0000DE00"`, "😀"},
		{`a: "x\uD83D\uDE00y\uD83D\uDE00"`, "x😀y😀"},
		{`a: "\uD800"`, "\ufffd"},
		{`a: "\uDC00"`, "\ufffd"},
		{`a: "\uDC00\uD800"`, "\ufffd\ufffd"},
		{`a: "\uD800\uD800\uDC00"`, "\ufffd\U00010000"},
		{`a: "\uD800x\uDC00"`, "\ufffdx\ufffd"},
		{`a: "\uD800\n"`, "\ufffd\n"},
		{`a: "\uD800 \uDC00"`, "\ufffd \ufffd"},
		{`a: "\uD800\\uDC00"`, "\ufffd\\uDC00"},
		{"a: \"\\uD800\n  \\uDC00\"", "\ufffd \ufffd"},
		{"a: \"\\uD800\\\n  \\uDC00\"", "\U00010000"},
		{`a: "\u00e9\U0001F600"`, "é😀"},
	} {
		v, err := Parse(tc.source)
		if err != nil {
			t.Errorf("%s: %v", tc.source, err)
			continue
		}
		got, _ := v.(map[string]any)["a"].(string)
		if got != tc.want {
			t.Errorf("%s = %+q, want %+q", tc.source, got, tc.want)
		}
	}
	// A short escape after a high surrogate is still the library's error.
	if _, err := Parse(`a: "\uD800\uDC0"`); err == nil {
		t.Errorf(`a: "\uD800\uDC0" parsed; the library throws "Invalid escape sequence"`)
	}
}
