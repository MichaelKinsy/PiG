//go:build parity

package runner

import "testing"

// JSON.parse preserves UTF-16 code units: lone surrogates are not replacement characters, and distinct lone surrogates are not equal.
func TestJSONComparisonKeepsUTF16StringIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, pig, pi string
		equal         bool
	}{
		{"high-versus-replacement", `"\ud800"`, `"�"`, false},
		{"different-high-surrogates", `"\ud800"`, `"\ud801"`, false},
		{"high-versus-low", `"\ud800"`, `"\udc00"`, false},
		{"object-key", `{"\ud800":1}`, `{"�":1}`, false},
		{"literal-escape", `"\\ud800"`, `"\ud800"`, false},
		{"valid-pair", `"\ud83d\ude00"`, `"😀"`, true},
		{"same-unpaired-unit", `"\ud800"`, `"\ud800"`, true},
		{"distinct-unpaired-keys", `{"\ud800":1,"\ud801":2}`, `{"\ud800":1,"\ud801":2}`, true},
		{"ordinary-escape", `"\u0061"`, `"a"`, true},
		{"nested", `{"a":[{"x":"\ud800"}]}`, `{"a":[{"x":"�"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pig, pi := tc.pig, tc.pi
			if pig[0] == '"' {
				pig, pi = `{"value":`+pig+`}`, `{"value":`+pi+`}`
			}
			err := compareJSONResults(Result{Output: pig}, Result{Output: pi}, nil)
			if (err == nil) != tc.equal {
				t.Fatalf("comparison=%v, equal=%t for %s versus %s", err, tc.equal, tc.pig, tc.pi)
			}
		})
	}
}
