package llama

import (
	"math"
	"testing"
)

// huggingface.ts findHuggingFaceToken trims HF_TOKEN with String.trim: U+0085 is not JS whitespace, so a lone NEL is a token; U+FEFF and U+00A0 are whitespace.
func TestFindHuggingFaceTokenTrimsLikeJS(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"\u0085", "\u0085"},
		{"\u0085tok\u0085", "\u0085tok\u0085"},
		{"\ufeff \u00a0tok\u2028", "tok"},
		{"\ufeff\u00a0", ""},
	} {
		env := map[string]string{"HF_TOKEN": tc.value}
		t.Setenv("HOME", t.TempDir())
		if got := FindHuggingFaceToken(func(name string) string { return env[name] }); got != tc.want {
			t.Errorf("HF_TOKEN %q = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// Number(response.headers.get("retry-after")) || parseRateLimitDelay(...) (huggingface.ts) and Number(args[index + 1]) (ui.ts) read numbers with StringToNumber: a hex,
// octal or binary literal counts, and "inf", "nan" and hex floats are NaN.
func TestHeaderAndArgumentNumbersFollowNumber(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
		nan  bool
	}{
		{"0x10", 16, false}, {"0b11", 3, false}, {"0o17", 15, false}, {" 12 ", 12, false}, {"1e3", 1000, false}, {"", 0, false}, {"-5", -5, false},
		{"inf", 0, true}, {"nan", 0, true}, {"0x1p3", 0, true}, {"1_0", 0, true}, {"1,5", 0, true}, {"Infinity", math.Inf(1), false}, {"\u0085 7", 0, true},
	} {
		got, ok := jsNumberValue(tc.text)
		if ok == tc.nan || (ok && got != tc.want) {
			t.Errorf("jsNumberValue(%q) = %v, %v, want %v, nan=%v", tc.text, got, ok, tc.want, tc.nan)
		}
		wantDelay := tc.want
		if tc.nan {
			wantDelay = 0
		}
		if got := jsNumber(tc.text); got != wantDelay {
			t.Errorf("jsNumber(%q) = %v, want %v", tc.text, got, wantDelay)
		}
	}
}

// A template literal prints a number with Number::toString: exponent form from 1e21, and negative exponents below 1e-6.
func TestContextLabelNumbersPrintLikeJS(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{{999, "999"}, {0.0000001, "1e-7"}, {1.5, "1.5"}, {1500, "2k"}, {1e25, "1e+22k"}, {123456789012345680000, "123456789012345680k"}} {
		if got := formatContext(tc.value); got != tc.want {
			t.Errorf("formatContext(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
