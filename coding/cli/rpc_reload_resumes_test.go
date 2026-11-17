package cli

import (
	"os"
	"testing"
)

// The command that awaited ctx.reload() goes on after it, as in Pi, where the old instance outlives the reload (agent-session.ts:3828-3876): its next line runs and its response follows, though the reload removed a member of its cell.
func TestRPCCommandGoesOnAfterAReloadThatRemovesACellMember(t *testing.T) {
	for name, fixture := range map[string]func(*testing.T) membershipFixture{
		"node": func(t *testing.T) membershipFixture { return nodeMembershipFixture(t, "remove") },
		"go":   goMembershipFixture,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := fixture(t)
			p := f.start(t)
			f.reload(p)
			if _, err := os.Stat(f.marker); err != nil {
				t.Fatalf("the command that awaited ctx.reload() did not go on after it: %v\n%s", err, p.stderr.String())
			}
			p.closeAndWait("the reload settled")
		})
	}
}
