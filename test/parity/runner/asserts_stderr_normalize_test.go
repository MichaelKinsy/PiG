//go:build parity

package runner

import "testing"

// stderr_equal applies the scenario's justified stderr_normalize_replace rules: Pi c8c456c85c6e (main.ts:917-918) makes the no-models guidance a stderr warning in every mode, and it names a distribution-specific documentation root.
func TestStderrEqualAppliesNormalizeReplace(t *testing.T) {
	rule := NormalizeReplaceRule{Pattern: `/[^ \n]*/docs/(?:bundle-[0-9a-f]+/)?(providers|models)\.md`, With: "<DOCS>/$1.md", Reason: "the documentation root is distribution-specific"}
	pig := "Warning: No models available. See:\n  /tmp/a/docs/bundle-0123456789abcdef/providers.md\n"
	pi := "Warning: No models available. See:\n  /tmp/b/pkg/docs/providers.md\n"
	for _, tc := range []struct {
		name      string
		rules     []NormalizeReplaceRule
		pi        string
		wantFails int
	}{
		{"rule applied", []NormalizeReplaceRule{rule}, pi, 0},
		{"no rule keeps the strict comparison", nil, pi, 1},
		{"a rule keeps every other byte", []NormalizeReplaceRule{rule}, pi + "extra\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &ScenarioOutcome{
				Scenario: &Scenario{Assert: AssertSpec{StderrEqual: true, StderrNormalizeReplace: tc.rules}},
				Pig:      SystemResults{Runs: []Result{{Stderr: pig, StderrCaptured: true}}},
				Pi:       SystemResults{Runs: []Result{{Stderr: tc.pi, StderrCaptured: true}}},
			}
			EvaluateOutcome(o)
			n := 0
			for _, f := range o.Failures {
				if len(f) > 3 && f[:4] == "run " {
					n++
				}
			}
			if n != tc.wantFails {
				t.Fatalf("failures=%v want %d stderr failures", o.Failures, tc.wantFails)
			}
		})
	}
}

// stderr_normalize_replace reaches the stderr comparison only: a documentation root that differs in stdout still fails output_equal with the same rule set.
func TestStderrNormalizeReplaceDoesNotTouchStdout(t *testing.T) {
	rule := NormalizeReplaceRule{Pattern: `/[^ \n]*/docs/(?:bundle-[0-9a-f]+/)?(providers|models)\.md`, With: "<DOCS>/$1.md", Reason: "the documentation root is distribution-specific"}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Assert: AssertSpec{OutputEqual: true, StderrEqual: true, StderrNormalizeReplace: []NormalizeReplaceRule{rule}}},
		Pig:      SystemResults{Runs: []Result{{Output: "see /tmp/a/docs/bundle-0123456789abcdef/providers.md\n", Stderr: "", StderrCaptured: true}}},
		Pi:       SystemResults{Runs: []Result{{Output: "see /tmp/b/pkg/docs/providers.md\n", Stderr: "", StderrCaptured: true}}},
	}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("a stdout difference passed: stderr_normalize_replace must not apply to output_equal")
	}
}
