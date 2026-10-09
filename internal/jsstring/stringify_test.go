package jsstring

import "testing"

// JSON.stringify writes <, >, & and U+2028/U+2029 raw and escapes only quotes, backslashes and C0 controls; an escaped backslash before literal "u2028" text
// is not a line terminator.
func TestMarshalJSONMatchesJSONStringify(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"a<b>&c", `"a<b>&c"`},
		{"x\u2028y\u2029z", "\"x\u2028y\u2029z\""},
		{`\u2028`, `"\\u2028"`},
		{"\x1b[0m\x7f", `"\u001b[0m` + "\x7f" + `"`},
		{map[string]any{"k": "<&>"}, `{"k":"<&>"}`},
	} {
		got, err := MarshalJSON(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("MarshalJSON(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
