//go:build parity

package runner

import (
	"slices"
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
