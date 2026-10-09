package ai

import (
	"strings"
	"testing"
)

// packages/ai/src/utils/overflow.ts:37-76: the patterns are non-unicode /i regexes. JavaScript's case folding keeps ASCII letters apart
// from non-ASCII ones, so U+017F (long s) is not "s" and U+212A (Kelvin sign) is not "k", although Go's (?i) folds both. Measured with
// Node 24: /too many tokens/i.test("too many tokenſ") === false and .test("too many toKens") === true.
func TestOverflowPatternsFoldCaseLikeJavaScript(t *testing.T) {
	overflow := func(message string) bool {
		return IsContextOverflow(AssistantMessage{Provider: "p", StopReason: StopReasonError, ErrorMessage: message}, 0)
	}
	for _, tc := range []struct {
		message string
		want    bool
	}{
		{"too many tokens", true},
		{"TOO MANY TOKENS", true},
		{"Too Many ToKeNs", true},
		{"too many tokenſ", false},
		{"too many to\u212Aens", false},
		{"TOO MANY TOKENS", true},
		{"token limit exceeded", true},
		{strings.Replace("token limit exceeded", "k", "\u212A", 1), false},
		{"Request_Too_Large", true},
		{"reque\u017Ft_too_large", false},
		{"exceeds the context window", true},
		{strings.ReplaceAll("exceeds the context window", "s", "ſ"), false},
	} {
		if got := overflow(tc.message); got != tc.want {
			t.Errorf("IsContextOverflow(%q) = %v, want %v", tc.message, got, tc.want)
		}
	}
	// Every pattern, not only the ones above: replacing each ASCII s and k of the text it matches with the non-ASCII letters Go folds onto
	// them breaks the match. (A pattern whose match has neither letter has nothing to fold.)
	samples := overflowPatternSamples()
	patterns := GetOverflowPatterns()
	fold := strings.NewReplacer("s", "ſ", "S", "ſ", "k", "\u212A", "K", "\u212A")
	for i, sample := range samples {
		matched := patterns[i].FindString(sample)
		if matched == "" {
			t.Errorf("pattern %d %q does not match %q", i, patterns[i], sample)
			continue
		}
		changed := strings.Replace(sample, matched, fold.Replace(matched), 1)
		if changed != sample && patterns[i].MatchString(changed) {
			t.Errorf("pattern %d %q matches %q", i, patterns[i], changed)
		}
		if !patterns[i].MatchString(strings.ToUpper(sample)) {
			t.Errorf("pattern %d %q does not match %q", i, patterns[i], strings.ToUpper(sample))
		}
	}
	// The Cerebras and Bedrock exclusions are /i too, and equally strict about non-ASCII letters.
	if !IsContextOverflow(AssistantMessage{Provider: "cerebras", StopReason: StopReasonError, ErrorMessage: "400 STATUS CODE (NO BODY)"}, 0) {
		t.Error("the Cerebras pattern is case-insensitive")
	}
	if overflow("RATE LIMIT: too many tokens") || overflow("THROTTLING ERROR: too many tokens") || overflow("Too Many Requests: too many tokens") {
		t.Error("the non-overflow patterns are case-insensitive")
	}
	if !overflow("too many requeſts: too many tokens") || !overflow("ſervice unavailable: too many tokens") {
		t.Error("a long s must not trigger the non-overflow exclusions")
	}
}
