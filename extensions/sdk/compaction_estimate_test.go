package sdk

import "testing"

// JavaScript's Number::toString: the cases ECMA-262 7.1.12.1 and V8 print.
func TestJSNumberStringMatchesNumberToString(t *testing.T) {
	for value, want := range map[float64]string{
		0: "0", 1: "1", -1: "-1", 1.5: "1.5", 0.1: "0.1", 100: "100", 123456789012345680000: "123456789012345680000", 1e21: "1e+21", 1.5e21: "1.5e+21",
		1e-6: "0.000001", 1e-7: "1e-7", 1.5e-7: "1.5e-7", 123e-20: "1.23e-18", 0.000001234: "0.000001234", 5e-324: "5e-324", 1.7976931348623157e308: "1.7976931348623157e+308",
		-0.5: "-0.5", 4294967296.5: "4294967296.5", 1e20: "100000000000000000000", 12345.678: "12345.678",
	} {
		if got := jsNumberString(value); got != want {
			t.Errorf("jsNumberString(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestEstimateTokensCountsUTF16UnitsAndJSONStringifyLength(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message map[string]any
		want    int
	}{
		{"user string", map[string]any{"role": "user", "content": "abcde"}, 2},
		{"user astral", map[string]any{"role": "user", "content": "😀😀"}, 1},
		{"user image and text", map[string]any{"role": "user", "content": []any{map[string]any{"type": "image"}, map[string]any{"type": "text", "text": "abcd"}}}, 1201},
		{"assistant tool call", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "name": "read", "arguments": map[string]any{"path": "a\"b"}}}}, 5},
		{"unknown role", map[string]any{"role": "x", "content": "abc"}, 0},
	} {
		if got := EstimateTokens(tc.message); got != tc.want {
			t.Errorf("%s: EstimateTokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}
