// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// make durable-core-budget checks the Durable core's size budget, the TinyGo heap policy and the allocation-site baseline
// (durable/core/budget/allocs.json). The baseline stayed red for a release because no job ran it: ci.yml must run the target
// in the job that installs the pinned TinyGo, after the install.
func TestCIRunsTheDurableCoreBudgetGate(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := workflowJob(string(workflow), "durable-cf-host")
	if !ok {
		t.Fatal("ci.yml has no durable-cf-host job")
	}
	for _, problem := range budgetGateProblems(job) {
		t.Error(problem)
	}

	_, recipes := makeDatabase(t)
	recipe := recipes["durable-core-budget"]
	for _, want := range []string{"node measure.mjs --check", "node allocs.mjs --check"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the durable-core-budget recipe must run %q, got:\n%s", want, recipe)
		}
	}
}

// The checker fails a job that never runs the target and a job that runs it before it installs TinyGo.
func TestDurableCoreBudgetGateCheckerRejectsMissingAndMisorderedSteps(t *testing.T) {
	install := "      - name: Install the pinned TinyGo\n        run: echo \"TINYGO=$(sh durable/core/budget/tinygo-install.sh)\" >> \"$GITHUB_ENV\"\n"
	gate := "      - name: Check the Durable core budget\n        run: automation/ci/with-isolated-pig-home.sh make durable-core-budget\n"
	if got := budgetGateProblems(install + gate); len(got) != 0 {
		t.Errorf("install then gate: unexpected problems %v", got)
	}
	if got := budgetGateProblems(install); len(got) == 0 {
		t.Error("a job without the gate step passed")
	}
	if got := budgetGateProblems(gate + install); len(got) == 0 {
		t.Error("a job that runs the gate before it installs TinyGo passed")
	}
	if got := budgetGateProblems("      # make durable-core-budget\n" + install); len(got) == 0 {
		t.Error("a comment naming the target passed as a step")
	}
}

// workflowJob returns the text of one job of a workflow: from its "  <name>:" line to the next job at the same indent.
func workflowJob(workflow, name string) (string, bool) {
	lines := strings.Split(workflow, "\n")
	next := regexp.MustCompile(`^  [A-Za-z0-9_-]+:\s*$`)
	for i, line := range lines {
		if line != "  "+name+":" {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if next.MatchString(lines[j]) {
				end = j
				break
			}
		}
		return strings.Join(lines[i:end], "\n"), true
	}
	return "", false
}

// budgetGateProblems reports what a job's text lacks of the gate: a TinyGo install, a run: step of make durable-core-budget,
// and the install before the run.
func budgetGateProblems(job string) []string {
	install := regexp.MustCompile(`(?m)^\s+run:.*durable/core/budget/tinygo-install\.sh`).FindStringIndex(job)
	gate := regexp.MustCompile(`(?m)^\s+run:.*\bmake durable-core-budget\b`).FindStringIndex(job)
	var problems []string
	if install == nil {
		problems = append(problems, "the job must install the pinned TinyGo with durable/core/budget/tinygo-install.sh")
	}
	if gate == nil {
		problems = append(problems, "the job must run make durable-core-budget in a run: step")
	}
	if install != nil && gate != nil && gate[0] < install[0] {
		problems = append(problems, "the job runs make durable-core-budget before it installs TinyGo")
	}
	return problems
}

// make durable-contract was red on main for releases because no job ran it: ci.yml must run it (through make ci-durable-contract,
// which links the host's npm tree first) for each of the three builds it gates, and the CI result job must require it.
func TestCIRunsTheDurableContractGate(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := workflowJob(string(workflow), "durable-contract")
	if !ok {
		t.Fatal("ci.yml has no durable-contract job")
	}
	for _, want := range []string{"make ci-durable-contract", "DURABLE_CONTRACT_ARGS: --impls ${{ matrix.impl }}", "impl: [pi, wasm-tinygo, native]", "durable/contract/.cache"} {
		if !strings.Contains(job, want) {
			t.Errorf("the durable-contract job lacks %q", want)
		}
	}
	if !strings.Contains(string(workflow), "expect \"Durable contract\" \"$DURABLE_CONTRACT\"") {
		t.Error("the CI result job does not require the durable-contract job")
	}
	prerequisites, _ := makeDatabase(t)
	for _, want := range []string{"durable-core-host-deps", "durable-contract"} {
		if !slices.Contains(prerequisites["ci-durable-contract"], want) {
			t.Errorf("ci-durable-contract must chain %s, got %v", want, prerequisites["ci-durable-contract"])
		}
	}
}
