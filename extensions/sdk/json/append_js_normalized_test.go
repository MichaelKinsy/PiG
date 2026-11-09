package json

import "testing"

// AppendJSNormalized rewrites a negative zero and the \ufffd escape and nothing else: HTML escapes, escaped U+2028 and other escapes stay.
func TestAppendJSNormalized(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{"a":-0,"b":-0.0,"c":-0e5,"d":[-0,0,-1,-0.5,2e-5]}`, `{"a":0,"b":0,"c":0,"d":[0,0,-1,-0.5,2e-5]}`},
		{`"\ufffd\uFFFD\uFfFd"`, "\"\ufffd\ufffd\ufffd\""},
		{`"\u003c\u003e\u0026\u2028\u2029\u007f\ud800 \u00e9\n"`, `"\u003c\u003e\u0026\u2028\u2029\u007f\ud800 \u00e9\n"`},
		{`"-0 \\ufffd \\u003c"`, `"-0 \\ufffd \\u003c"`},
		{"\"\\\"fffd \\nFFFD \\\\fffd \\bfffd\"", "\"\\\"fffd \\nFFFD \\\\fffd \\bfffd\""},
		{" {\n\t\"k\" : [ -0 , \"\\ufffd\" ]\n} ", "{\"k\":[0,\"\ufffd\"]}"},
		{`{"s":"<&>\u2028"}`, `{"s":"<&>\u2028"}`},
	} {
		got, err := AppendJSNormalized([]byte("pre"), []byte(tc.src))
		if err != nil || string(got) != "pre"+tc.want {
			t.Errorf("%s:\n got %q (%v)\nwant %q", tc.src, got, err, "pre"+tc.want)
		}
		// In place: dst is src[:0].
		src := []byte(tc.src)
		got, err = AppendJSNormalized(src[:0], src)
		if err != nil || string(got) != tc.want {
			t.Errorf("in place %s:\n got %q (%v)\nwant %q", tc.src, got, err, tc.want)
		}
	}
	if got, err := AppendJSNormalized(nil, []byte(`{"a":`)); err == nil {
		t.Errorf("truncated text: %q, no error", got)
	}
}

// A two-character escape before the letters of "fffd" is that escape and four letters, not U+FFFD.
func TestMarshalStringifyKeepsEscapeBeforeFffd(t *testing.T) {
	const text = "\"say \\\"fffd\\\" \\nFFFD \\\\fffd\""
	got, err := MarshalStringify(RawMessage(text))
	if err != nil || string(got) != text {
		t.Errorf("got %q (%v), want %q", got, err, text)
	}
}
