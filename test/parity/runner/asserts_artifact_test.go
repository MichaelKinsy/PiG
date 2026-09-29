//go:build parity

package runner

import (
	"errors"
	"strings"
	"testing"
)

func artifactOutcome(name string, a AssertSpec, pig, pi Result) *ScenarioOutcome {
	return &ScenarioOutcome{
		Scenario: &Scenario{Name: name, Assert: a},
		Pig:      SystemResults{System: "pig", Runs: []Result{pig}},
		Pi:       SystemResults{System: "pi", Runs: []Result{pi}},
	}
}

// Artifact assertions compare what a command wrote, not what it printed. A
// scenario covering a file-producing surface cannot exercise it otherwise: the
// binary reports success on stdout regardless of the file's contents.
func TestEvaluateOutcome_ArtifactAssertions(t *testing.T) {
	cases := []struct {
		name      string
		asserts   AssertSpec
		pig, pi   Result
		wantFail  bool
		wantMatch string
	}{
		{
			name:    "normalized_equal passes on identical artifacts",
			asserts: AssertSpec{ArtifactNormalizedEqual: true},
			pig:     Result{Artifact: "<html>payload</html>"},
			pi:      Result{Artifact: "<html>payload</html>"},
		},
		{
			name:      "normalized_equal catches a differing artifact",
			asserts:   AssertSpec{ArtifactNormalizedEqual: true},
			pig:       Result{Artifact: "<html>pig-payload</html>"},
			pi:        Result{Artifact: "<html>pi-payload</html>"},
			wantFail:  true,
			wantMatch: "artifact mismatch",
		},
		{
			name:      "both_contain fails when pig's artifact lacks the anchor",
			asserts:   AssertSpec{ArtifactBothContain: []string{"session-data"}},
			pig:       Result{Artifact: "<html></html>"},
			pi:        Result{Artifact: "<html>session-data</html>"},
			wantFail:  true,
			wantMatch: "pig artifact missing \"session-data\"",
		},
		{
			name:      "both_not_contain rejects a legacy marker",
			asserts:   AssertSpec{ArtifactBothNotContain: []string{"tool_use"}},
			pig:       Result{Artifact: `{"type":"tool_use"}`},
			pi:        Result{Artifact: `{"type":"toolCall"}`},
			wantFail:  true,
			wantMatch: "pig artifact unexpectedly contains \"tool_use\"",
		},
		{
			// An unreadable artifact must fail rather than compare empty to
			// empty and report success on a file that was never produced.
			name:      "unreadable artifact fails loudly",
			asserts:   AssertSpec{ArtifactNormalizedEqual: true},
			pig:       Result{ArtifactErr: errors.New("no such file")},
			pi:        Result{Artifact: ""},
			wantFail:  true,
			wantMatch: "artifact unreadable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := artifactOutcome(tc.name, tc.asserts, tc.pig, tc.pi)
			EvaluateOutcome(o)
			if got := len(o.Failures) > 0; got != tc.wantFail {
				t.Fatalf("failures=%v, wantFail=%v", o.Failures, tc.wantFail)
			}
			if tc.wantMatch == "" {
				return
			}
			for _, f := range o.Failures {
				if strings.Contains(f, tc.wantMatch) {
					return
				}
			}
			t.Fatalf("failures=%v, want one containing %q", o.Failures, tc.wantMatch)
		})
	}
}

// An anchor must reject a vacuous comparison even if a reviewer mistakenly justifies an overbroad replacement. Canonical export scenarios compare full artifacts instead.
func TestEvaluateOutcome_ArtifactAnchorCatchesVacuousReduction(t *testing.T) {
	reduceToNothing := []NormalizeReplaceRule{{Pattern: `(?s)^.*$`, With: "", Reason: "Injected overbroad test rule; the anchor must catch its vacuous result."}}

	withoutAnchor := artifactOutcome("no-anchor",
		AssertSpec{ArtifactNormalizedEqual: true, NormalizeReplace: reduceToNothing},
		Result{Artifact: "<html>pig</html>"}, Result{Artifact: "<html>totally-different</html>"})
	EvaluateOutcome(withoutAnchor)
	if len(withoutAnchor.Failures) != 0 {
		t.Fatalf("precondition: over-broad reduction should compare equal, got %v", withoutAnchor.Failures)
	}

	withAnchor := artifactOutcome("anchored",
		AssertSpec{
			ArtifactNormalizedEqual: true,
			ArtifactBothContain:     []string{"session-data"},
			NormalizeReplace:        reduceToNothing,
		},
		Result{Artifact: "<html>pig</html>"}, Result{Artifact: "<html>totally-different</html>"})
	EvaluateOutcome(withAnchor)
	if len(withAnchor.Failures) == 0 {
		t.Fatal("anchor must fail a reduction that erased the payload")
	}
}
