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

// cmd/pig's TestReplacedSession2860AcrossSDKs starts every SDK's realization of the Lisp one too and fails without sbcl, which is the nightly's cli shard 4/4 on both
// operating systems. ci.yml's Linux and macOS jobs install the pinned SBCL with automation/ci/install-sbcl.sh; the nightly race job does too, on the same cache key.
func TestNightlyRaceJobInstallsThePinnedSBCL(t *testing.T) {
	job, ok := readWorkflow(t, "nightly.yml").Jobs["race"]
	if !ok {
		t.Fatal("nightly.yml has no race job")
	}
	installs, caches := false, false
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "automation/ci/install-sbcl.sh") {
			installs = true
			for _, want := range []string{`PIG_SBCL=$prefix/bin/sbcl" >> "$GITHUB_ENV"`, `SBCL_HOME=$prefix/lib/sbcl" >> "$GITHUB_ENV"`, `echo "$prefix/bin" >> "$GITHUB_PATH"`} {
				if !strings.Contains(step.Run, want) {
					t.Errorf("the SBCL step does not run %s:\n%s", want, step.Run)
				}
			}
		}
		if strings.HasPrefix(step.Uses, "actions/cache@") && step.With["path"] == "~/.cache/pig-sbcl" {
			caches = true
		}
	}
	if !installs {
		t.Error("the nightly race job does not run automation/ci/install-sbcl.sh, so the Lisp SDK realization of TestReplacedSession2860AcrossSDKs fails with sbcl missing")
	}
	if !caches {
		t.Error("the nightly race job does not cache ~/.cache/pig-sbcl, so every shard builds SBCL (from source on arm64 macOS)")
	}
}
