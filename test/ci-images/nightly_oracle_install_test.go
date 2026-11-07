// SPDX-License-Identifier: MIT

package ciimages

import (
	"strings"
	"testing"
)

// The nightly race shards run the Pi-oracle tests, which import Pi through node_modules trees that the oracle build alone does not install: durable/interop's
// (agent, ai and durable tests), internal/typebox/testdata/npm and ai/testdata/sdk. The nightly installed the oracle and mirrored the source by hand, left those trees
// out, and every night's ai, libs, rest and cli shards failed with ERR_MODULE_NOT_FOUND. ci.yml's Linux job prepares the oracle with `make upstream-mirror` (parity-deps,
// then the mirror), so the nightly jobs that read the oracle run the same make target and cannot list a different set of installs.
func TestNightlyPreparesTheOracleLikeCI(t *testing.T) {
	const shared = "make upstream-mirror"
	ciRuns := false
	for _, step := range readWorkflow(t, "ci.yml").Jobs["linux"].Steps {
		if strings.Contains(step.Run, shared) {
			ciRuns = true
		}
	}
	if !ciRuns {
		t.Fatalf("ci.yml's linux job does not run %q, the step the nightly jobs share", shared)
	}
	nightly := readWorkflow(t, "nightly.yml")
	for _, name := range []string{"race", "allocations"} {
		job, ok := nightly.Jobs[name]
		if !ok {
			t.Fatalf("nightly.yml has no %s job", name)
		}
		runs := false
		for _, step := range job.Steps {
			if strings.Contains(step.Run, shared) {
				runs = true
			}
			for _, own := range []string{"pin.py install-oracle", "mirror-upstream.sh", "npm-locked.py"} {
				if strings.Contains(step.Run, own) {
					t.Errorf("nightly.yml job %s step %q runs %s itself; the set of installs is %q's, so a tree added there must not be missing here", name, step.Name, own, shared)
				}
			}
		}
		if !runs {
			t.Errorf("nightly.yml job %s does not run %q, so its Pi-oracle tests and benchmarks miss the oracle test npm trees", name, shared)
		}
	}
}
