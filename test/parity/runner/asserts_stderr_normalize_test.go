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
