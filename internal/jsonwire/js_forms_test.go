package jsonwire

import "testing"

func TestMayHaveJSForms(t *testing.T) {
	for text, want := range map[string]bool{
		``: false, `{"a":1,"b":"x"}`: false, `"\u0041\n\\u"`: false, `"\u00e9\ufff"`: false, `"\u`: false, `"\ufff`: false,
		`{"a":-0}`: true, `[-0.5]`: true, `"x-0"`: true, `"\ufffd"`: true, `"\uFFFD"`: true, `"\uFfFd"`: true, `"\u0041\ufffd"`: true,
		`"\\ufffd"`: true, `"\ud800\ufffd"`: true, `"\u2028\u`: false, `{"x":-1e-400}`: true, `[-2E-324]`: true, `{"x":1e5,"y":"e-"}`: false,
		`{"x":-1.5e-400}`: true, `[-1e5]`: false, `[-1.5e+5,-12E5]`: false,
	} {
		if got := MayHaveJSForms([]byte(text)); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}
