package docsdrift

import (
	"os/exec"
	"testing"
)

// A fragment in changelog.d is named <lane>-<sha>.md and holds sections of bullets, so the integrator can assemble the
// Unreleased changelog from them without editing any of them. Fragments that predate the rule are exempt.
func TestChangelogFragmentsFollowTheRules(t *testing.T) {
	cmd := exec.Command("go", "run", "./automation/ci/changelogfrag", "-check")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
