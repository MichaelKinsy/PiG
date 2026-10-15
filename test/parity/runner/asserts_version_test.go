//go:build parity

package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestExpandVersionTokensUsesThePins(t *testing.T) {
	got := expandVersionTokens([]string{"{{VERSION}}"}, []string{"pi {{UPSTREAM_VERSION}}", "plain"})
	// {{VERSION}} is D63's composite (one plus: a commit pin's 1.1.0+42a3497d joins as 1.1.0.42a3497d); {{UPSTREAM_VERSION}}
	// is what the pinned Pi build reports, its package version.
	want := []string{coding.PigVersion + "+" + strings.ReplaceAll(coding.UpstreamVersion, "+", "."), "pi " + coding.UpstreamPackageVersion, "plain"}
	if !slices.Equal(got, want) {
		t.Fatalf("expandVersionTokens = %q, want %q", got, want)
	}
}

func TestDivergeTokensAreEvaluatedAgainstOutput(t *testing.T) {
	o := &ScenarioOutcome{Scenario: &Scenario{Diverge: DivergeSpec{PigContains: []string{"{{VERSION}}"}, PiContains: []string{"{{UPSTREAM_VERSION}}"}}}}
	o.Pig.Runs = []Result{{Output: coding.Version + "\n"}}
	o.Pi.Runs = []Result{{Output: coding.UpstreamPackageVersion + "\n"}}
	EvaluateOutcome(o)
	if len(o.Failures) != 0 {
		t.Fatalf("composite and bare versions should satisfy D63's diverge block: %q", o.Failures)
	}
	o.Failures = nil
	o.Pig.Runs = []Result{{Output: coding.UpstreamPackageVersion + "\n"}}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("a bare Pi version from pig must fail D63's diverge block")
	}
}

func TestBothMatchRegexExpandsVersionTokensAsLiterals(t *testing.T) {
	newOutcome := func(output string) *ScenarioOutcome {
		o := &ScenarioOutcome{Scenario: &Scenario{Assert: AssertSpec{BothMatchRegex: []string{`^({{VERSION}}|{{UPSTREAM_VERSION}})$`}}}}
		o.Pig.Runs = []Result{{Output: output}}
		o.Pi.Runs = []Result{{Output: output}}
		return o
	}
	for _, version := range []string{coding.Version, coding.UpstreamPackageVersion} {
		o := newOutcome(version)
		EvaluateOutcome(o)
		if len(o.Failures) != 0 {
			t.Fatalf("%q is a pinned version and must match: %q", version, o.Failures)
		}
	}
	// The pin's dots are literal: a digit in a dot's place is another release.
	other := strings.Replace(coding.UpstreamPackageVersion, ".", "0", 1)
	o := newOutcome(other)
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatalf("%q is not the pinned release %q and must not match", other, coding.UpstreamPackageVersion)
	}
}
