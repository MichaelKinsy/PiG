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
	want := []string{coding.PigVersion + "+" + coding.UpstreamVersion, "pi " + coding.UpstreamVersion, "plain"}
	if !slices.Equal(got, want) {
		t.Fatalf("expandVersionTokens = %q, want %q", got, want)
	}
}

func TestDivergeTokensAreEvaluatedAgainstOutput(t *testing.T) {
	o := &ScenarioOutcome{Scenario: &Scenario{Diverge: DivergeSpec{PigContains: []string{"{{VERSION}}"}, PiContains: []string{"{{UPSTREAM_VERSION}}"}}}}
	o.Pig.Runs = []Result{{Output: coding.Version + "\n"}}
	o.Pi.Runs = []Result{{Output: coding.UpstreamVersion + "\n"}}
	EvaluateOutcome(o)
	if len(o.Failures) != 0 {
		t.Fatalf("composite and bare versions should satisfy D63's diverge block: %q", o.Failures)
	}
	o.Failures = nil
	o.Pig.Runs = []Result{{Output: coding.UpstreamVersion + "\n"}}
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatal("a bare Pi version from pig must fail D63's diverge block")
	}
}

func TestBothMatchRegexExpandsVersionTokensAsLiterals(t *testing.T) {
	newOutcome := func(output string) *ScenarioOutcome {
		o := &ScenarioOutcome{Scenario: &Scenario{Assert: AssertSpec{BothMatchRegex: []string{`^([0-9.]+\+)?{{UPSTREAM_VERSION}}$`}}}}
		o.Pig.Runs = []Result{{Output: output}}
		o.Pi.Runs = []Result{{Output: output}}
		return o
	}
	o := newOutcome(coding.Version)
	EvaluateOutcome(o)
	if len(o.Failures) != 0 {
		t.Fatalf("the composite version ends in the pin and must match: %q", o.Failures)
	}
	// The pin's dots are literal: a digit in a dot's place is another release.
	other := strings.Replace(coding.UpstreamVersion, ".", "0", 1)
	o = newOutcome(other)
	EvaluateOutcome(o)
	if len(o.Failures) == 0 {
		t.Fatalf("%q is not the pinned release %q and must not match", other, coding.UpstreamVersion)
	}
}
