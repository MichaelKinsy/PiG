// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The cause rule is one UTC instant: the process's own zone changes no verdict. time.Local is fixed when a process starts, so each zone is a child run of this test binary with TZ set; the child runs the two tests that judge commits against the rule.
func TestTheCauseRuleHoldsInEveryProcessZone(t *testing.T) {
	if os.Getenv("BUGLEDGER_ZONE_CHILD") != "" {
		t.Skip("the child run executes the rule tests themselves")
	}
	for _, zone := range []string{"UTC", "America/New_York", "Pacific/Auckland"} {
		t.Run(zone, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.count=1", "-test.run", "^(TestTheCauseRuleStartsOnItsDay|TestTheCauseRuleStartsAtOneInstantInEveryTimeZone|TestACommittedEntryIsJudgedByItsCommitInstant|TestAStaleCommittedEntryWithoutACauseIsReportedByItsCommitInstant)$", "-test.v")
			cmd.Env = append(os.Environ(), "TZ="+zone, "BUGLEDGER_ZONE_CHILD=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("under TZ=%s: %v\n%s", zone, err, out)
			}
			if n := strings.Count(string(out), "--- PASS: TestTheCauseRule"); n < 2 {
				t.Fatalf("under TZ=%s the child ran too few rule tests (%d):\n%s", zone, n, out)
			}
		})
	}
}
