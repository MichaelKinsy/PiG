// SPDX-License-Identifier: MIT

package ciimages

import (
	"slices"
	"testing"
)

// TestChangesAndBookkeepingJobsStayUnconditional guards the root of ci.yml's job graph. Every product job waits for Changed paths (the plan)
// and for Bookkeeping (the fail-fast gates), and each of their conditions reads those two jobs' results: a job that carried an `if`
// could be skipped, and everything behind it with it, which is how a run once tested nothing while it reported success. So `changes` has
// no condition and no prerequisite, and `bookkeeping` has no condition and waits for the verdict job alone.
func TestChangesAndBookkeepingJobsStayUnconditional(t *testing.T) {
	jobs := verificationJobs(t)
	for name, needs := range map[string][]string{"changes": nil, "bookkeeping": {"verdict"}} {
		job, ok := jobs[name]
		if !ok {
			t.Fatalf("ci.yml has no %s job", name)
		}
		if job.If != "" {
			t.Errorf("job %s has the condition %q: a skipped %[1]s job skips every job that needs it", name, job.If)
		}
		if !slices.Equal(job.Needs, needs) {
			t.Errorf("job %s needs %v, want %v", name, job.Needs, needs)
		}
	}
	if job := jobs["verdict"]; job.If != "" {
		t.Errorf("job verdict has the condition %q: it runs on every event and decides inside its steps", job.If)
	}
}
