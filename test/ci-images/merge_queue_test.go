// SPDX-License-Identifier: MIT

package ciimages

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// ciPlan is the output of automation/ci/ci-plan.sh for one event.
type ciPlan struct {
	Full   bool
	Keys   []string
	Matrix map[string][]planEntry
	Run    map[string]bool
}

type planEntry struct {
	Shard  string `json:"shard"`
	Target string `json:"target"`
}

func (p ciPlan) shards(job string) []string {
	var shards []string
	for _, entry := range p.Matrix[job] {
		shards = append(shards, entry.Shard)
	}
	return shards
}

func runPlan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := testenv.ScriptCommand(t, filepath.Join(repoRoot(t), "automation", "ci", "ci-plan.sh"), args...)
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// planFor runs the planner on the committed tier table, as the changes job does.
func planFor(t *testing.T, event, light string) ciPlan {
	t.Helper()
	output, err := runPlan(t, event, light)
	if err != nil {
		t.Fatalf("ci-plan.sh %s %q: %v\n%s", event, light, err, output)
	}
	plan := ciPlan{Matrix: map[string][]planEntry{}, Run: map[string]bool{}}
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			t.Fatalf("ci-plan.sh printed %q, want key=value lines", line)
		}
		plan.Keys = append(plan.Keys, key)
		switch {
		case key == "full":
			plan.Full = value == "true"
		case strings.HasPrefix(key, "run-"):
			plan.Run[strings.TrimPrefix(key, "run-")] = value == "true"
		default:
			var entries []planEntry
			if err := json.Unmarshal([]byte(value), &entries); err != nil {
				t.Fatalf("ci-plan.sh %s=%s: %v", key, value, err)
			}
			plan.Matrix[key] = entries
		}
	}
	return plan
}

// tierRow is one row of automation/ci/ci-tiers.txt, read here without the planner so the planner is checked against the table.
type tierRow struct{ job, shard, tier, lightTarget string }

func tierRows(t *testing.T) []tierRow {
	t.Helper()
	file, err := os.Open(filepath.Join(repoRoot(t), "automation", "ci", "ci-tiers.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []tierRow
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		for len(fields) < 4 {
			fields = append(fields, "")
		}
		rows = append(rows, tierRow{fields[0], fields[1], fields[2], fields[3]})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

type workflowFile struct {
	On   any
	Jobs map[string]struct {
		Name  string
		If    string
		Needs any
		Steps []struct {
			Name string
			If   string
			Uses string
			Run  string
			With map[string]any
			Env  map[string]string
		}
	}
}

func readWorkflow(t *testing.T, name string) workflowFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return workflow
}

// triggers are the event names of a workflow's on: key, which is a string, a list or a map.
func (w workflowFile) triggers() map[string]any {
	events := map[string]any{}
	switch on := w.On.(type) {
	case string:
		events[on] = nil
	case []any:
		for _, event := range on {
			events[fmt.Sprint(event)] = nil
		}
	case map[string]any:
		events = on
	}
	return events
}

func needsOf(needs any) []string {
	switch needs := needs.(type) {
	case string:
		return []string{needs}
	case []any:
		var names []string
		for _, name := range needs {
			names = append(names, fmt.Sprint(name))
		}
		return names
	}
	return nil
}

// requiredChecks are the status checks the repository ruleset requires on main (.github/required-checks.txt).
func requiredChecks(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "required-checks.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		t.Fatal(".github/required-checks.txt lists no required check")
	}
	return names
}

// A required check that no run of a merge group reports blocks the queue until its timeout, so every required check's job must
// run on merge_group, unconditionally, in a workflow that has no path filter.
func TestEveryRequiredCheckRunsOnMergeGroup(t *testing.T) {
	workflows, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil || len(workflows) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	holders := map[string][]string{}
	for _, path := range workflows {
		name := filepath.Base(path)
		for _, job := range readWorkflow(t, name).Jobs {
			holders[job.Name] = append(holders[job.Name], name)
		}
	}
	for _, check := range requiredChecks(t) {
		files := holders[check]
		if len(files) != 1 {
			t.Errorf("required check %q is the name of %d jobs (%v), want exactly one", check, len(files), files)
			continue
		}
		workflow := readWorkflow(t, files[0])
		events := workflow.triggers()
		if _, ok := events["merge_group"]; !ok {
			t.Errorf("%s reports required check %q but does not run on merge_group", files[0], check)
		}
		if _, ok := events["pull_request"]; !ok {
			t.Errorf("%s reports required check %q but does not run on pull_request", files[0], check)
		}
		for _, event := range []string{"pull_request", "merge_group"} {
			if filters, ok := events[event].(map[string]any); ok && (filters["paths"] != nil || filters["paths-ignore"] != nil || filters["branches"] != nil) {
				t.Errorf("%s filters %s by path or branch, so some runs would never report %q", files[0], event, check)
			}
		}
		for _, job := range workflow.Jobs {
			if job.Name == check && !strings.Contains(job.If, "always()") {
				t.Errorf("required check %q has if: %q, want always() so it reports when a job it needs fails or is skipped", check, job.If)
			}
		}
	}
}

// The aggregator is the required check, so a job it does not wait for is a job the queue never waits for.
func TestRequiredCheckWaitsForEveryJobOfItsWorkflow(t *testing.T) {
	for _, file := range []string{"ci.yml", "security.yml"} {
		workflow := readWorkflow(t, file)
		var aggregator []string
		for id, job := range workflow.Jobs {
			if slices.Contains(requiredChecks(t), job.Name) {
				aggregator = needsOf(job.Needs)
				// The aggregator's own id is exempt below.
				aggregator = append(aggregator, id)
			}
		}
		// linux is waited for through verify; cache-prune only prunes caches.
		covered := map[string]string{"linux": "verify", "cache-prune": ""}
		for id := range workflow.Jobs {
			if slices.Contains(aggregator, id) {
				continue
			}
			via, ok := covered[id]
			if !ok || (via != "" && !slices.Contains(aggregator, via)) {
				t.Errorf("%s: the required check does not wait for job %s", file, id)
			}
		}
	}
}

func TestMergeGroupRunsTheFullPlan(t *testing.T) {
	rows := tierRows(t)
	for _, tc := range []struct{ event, light string }{
		{"merge_group", "true"},
		{"merge_group", ""},
		{"push", "true"},
		{"schedule", "true"},
		{"workflow_dispatch", "true"},
		// A pull request runs the full set until the repository variable switches it to the light subset.
		{"pull_request", ""},
		{"pull_request", "false"},
	} {
		t.Run(tc.event+"/"+tc.light, func(t *testing.T) {
			plan := planFor(t, tc.event, tc.light)
			if !plan.Full {
				t.Fatal("the plan is not the full run")
			}
			for _, job := range []string{"linux", "windows", "macos"} {
				var want []string
				for _, row := range rows {
					if row.job == job {
						want = append(want, row.shard)
					}
				}
				if got := plan.shards(job); !slices.Equal(got, want) {
					t.Errorf("%s shards = %v, want every table row %v", job, got, want)
				}
			}
			for _, row := range rows {
				if row.job == "single" && !plan.Run[row.shard] {
					t.Errorf("single job %s is not planned", row.shard)
				}
			}
			for _, entry := range plan.Matrix["linux"] {
				if entry.Target != "ci-"+entry.Shard {
					t.Errorf("full run: Linux shard %s runs %s, want ci-%s", entry.Shard, entry.Target, entry.Shard)
				}
			}
		})
	}
}

// The light run keeps the pr rows under the same check names as the full run and never invents a shard the full run lacks.
func TestLightPullRequestRunKeepsTheFastGatesUnderTheSameNames(t *testing.T) {
	rows := tierRows(t)
	light := planFor(t, "pull_request", "true")
	full := planFor(t, "merge_group", "true")
	if light.Full {
		t.Fatal("the light pull-request plan claims to be the full run")
	}
	for _, job := range []string{"linux", "windows", "macos"} {
		var want []string
		for _, row := range rows {
			if row.job == job && row.tier == "pr" {
				want = append(want, row.shard)
			}
		}
		if len(want) == 0 {
			t.Errorf("the light run has no %s job", job)
		}
		if got := light.shards(job); !slices.Equal(got, want) {
			t.Errorf("light %s shards = %v, want the pr rows %v", job, got, want)
		}
		for _, shard := range light.shards(job) {
			if !slices.Contains(full.shards(job), shard) {
				t.Errorf("light %s shard %q is not a full-run check name", job, shard)
			}
		}
	}
	for _, row := range rows {
		if row.job == "single" && light.Run[row.shard] != (row.tier == "pr") {
			t.Errorf("single job %s runs in the light plan = %v, want %v for tier %s", row.shard, light.Run[row.shard], row.tier == "pr", row.tier)
		}
	}

	// A light target runs a subset of its full-run shard: every gate is a gate of the shard, or lint-changed for lint.
	substitute := map[string]string{"lint-changed": "lint"}
	leaves := gateLeaves(t)
	fullTargets := map[string]string{}
	for _, entry := range full.Matrix["linux"] {
		fullTargets[entry.Shard] = entry.Target
	}
	for _, entry := range light.Matrix["linux"] {
		fullGates := leaves(fullTargets[entry.Shard])
		lightGates := leaves(entry.Target)
		if len(lightGates) == 0 {
			t.Errorf("light shard %s runs %s, which runs no gate", entry.Shard, entry.Target)
		}
		for _, gate := range lightGates {
			if replaced, ok := substitute[gate]; ok {
				gate = replaced
			}
			if !slices.Contains(fullGates, gate) {
				t.Errorf("light shard %s runs gate %s, which the full run's %s does not", entry.Shard, gate, fullTargets[entry.Shard])
			}
		}
	}

	// The gates the light run keeps: compile and vet (the preflight job), lint of the changed files, the drift and contract gates,
	// the sharded unit tests and one parity shard. The gates it leaves to the queue are the expensive suites.
	lightGates := leaves("ci-preflight")
	for _, entry := range light.Matrix["linux"] {
		lightGates = append(lightGates, leaves(entry.Target)...)
	}
	for _, gate := range []string{"build", "vet", "lint-changed", "lint-scenarios", "port-map-drift", "correspondence-check", "interface-gaps", "test-fast-parallel", "test-fast-sharded", "test-cli-1", "test-cli-2", "test-subprocess", "parity-fast-1"} {
		if !slices.Contains(lightGates, gate) {
			t.Errorf("the light run does not run %s", gate)
		}
	}
	for _, gate := range []string{"lint", "module-size", "test-race", "test-conformance", "test-sdk-rs", "parity-fast-2", "portmap-check-1"} {
		if slices.Contains(lightGates, gate) {
			t.Errorf("the light run runs %s, which belongs to the merge_group run", gate)
		}
	}
	// Whatever the light run leaves out, the full run runs: nothing is dropped from what runs before a change lands.
	fullGates := leaves("ci-preflight")
	for _, entry := range full.Matrix["linux"] {
		fullGates = append(fullGates, leaves(entry.Target)...)
	}
	for _, gate := range leaves("check") {
		if !slices.Contains(fullGates, gate) {
			t.Errorf("the full run does not run %s of make check", gate)
		}
	}
}

// The jobs of ci.yml read the plan through the changes job, so the table and the workflow cannot disagree.
func TestCIWorkflowFollowsThePlan(t *testing.T) {
	workflow := readWorkflow(t, "ci.yml")
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Jobs map[string]struct {
			Outputs  map[string]string
			Strategy struct{ Matrix struct{ Include string } }
		}
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	plan := planFor(t, "pull_request", "true")
	for _, key := range plan.Keys {
		want := "${{ steps.plan.outputs." + key + " }}"
		if got := raw.Jobs["changes"].Outputs[key]; got != want {
			t.Errorf("changes output %s = %q, want %q", key, got, want)
		}
	}
	for _, job := range []string{"linux", "windows", "macos"} {
		want := fmt.Sprintf("${{ fromJSON(needs.changes.outputs.%s) }}", job)
		if got := raw.Jobs[job].Strategy.Matrix.Include; got != want {
			t.Errorf("%s matrix include = %q, want %q", job, got, want)
		}
	}
	for _, row := range tierRows(t) {
		if row.job != "single" {
			continue
		}
		job, ok := workflow.Jobs[row.shard]
		if !ok {
			t.Errorf("ci-tiers.txt names single job %s, which ci.yml does not have", row.shard)
			continue
		}
		if want := "needs.changes.outputs.run-" + row.shard + " == 'true'"; !strings.Contains(job.If, want) {
			t.Errorf("job %s has if: %q, want it to read the plan (%s)", row.shard, job.If, want)
		}
	}
	for _, event := range []string{"merge_group", "schedule", "workflow_dispatch", "pull_request"} {
		if _, ok := workflow.triggers()[event]; !ok {
			t.Errorf("ci.yml does not run on %s", event)
		}
	}
}

func TestPlannerRejectsAMalformedTable(t *testing.T) {
	for name, table := range map[string]string{
		"unknown tier":                  "linux | build | sometimes\nwindows | native-1 | pr\nmacos | native | pr\n",
		"unknown job":                   "linux | build | pr\nlinus | build | pr\nwindows | native-1 | pr\nmacos | native | pr\n",
		"duplicate row":                 "linux | build | pr\nlinux | build | queue\nwindows | native-1 | pr\nmacos | native | pr\n",
		"missing shard":                 "linux | | pr\nwindows | native-1 | pr\nmacos | native | pr\n",
		"light target on windows":       "linux | build | pr\nwindows | native-1 | pr | ci-x\nmacos | native | pr\n",
		"no row runs in the light plan": "linux | build | pr\nwindows | native-1 | queue\nmacos | native | pr\n",
		"quote in a shard":              "linux | b\"d | pr\nwindows | native-1 | pr\nmacos | native | pr\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tiers.txt")
			if err := os.WriteFile(path, []byte(table), 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := runPlan(t, "pull_request", "true", path); err == nil {
				t.Fatalf("ci-plan.sh accepted the table:\n%s", output)
			}
		})
	}
}

// resultScript is the shell of the CI result job, run here with each job's result and the plan it was given.
func TestCIResultRequiresEveryPlannedJob(t *testing.T) {
	jobs := verificationJobs(t)
	script := ""
	var declared map[string]string
	for _, step := range jobs["result"].Steps {
		if strings.Contains(step.Run, "expect()") {
			script, declared = step.Run, step.Env
		}
	}
	if script == "" {
		t.Fatal("the CI result job has no aggregation step")
	}
	base := map[string]string{
		"EVENT": "merge_group", "PR_AUTHOR": "", "PRODUCT": "true", "FULL": "true",
		"RUN_NODE_RUNTIME": "true", "RUN_NODE_LOADER": "true", "RUN_ANDROID": "true", "RUN_DURABLE_CF_HOST": "true",
		"CHANGES": "success", "DOCS": "skipped", "DCO": "skipped", "VERIFY": "success", "NODE_RUNTIME": "success",
		"WINDOWS": "success", "MACOS": "success", "ANDROID": "success", "NODE_LOADER": "success",
		"DURABLE_CF_HOST": "success", "PREFLIGHT": "success",
	}
	for name := range declared {
		if _, ok := base[name]; !ok {
			t.Fatalf("the CI result step reads %s, which this test does not set", name)
		}
	}
	for name := range base {
		if _, ok := declared[name]; !ok {
			t.Fatalf("this test sets %s, which the CI result step no longer reads", name)
		}
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
		pass bool
	}{
		{"merge group with every job", nil, true},
		{"merge group skipped queue-only job", map[string]string{"NODE_RUNTIME": "skipped"}, false},
		{"merge group skipped durable host", map[string]string{"DURABLE_CF_HOST": "skipped"}, false},
		{"merge group failed Windows", map[string]string{"WINDOWS": "failure"}, false},
		{"merge group that planned the light run", map[string]string{"FULL": "false", "RUN_NODE_RUNTIME": "false", "RUN_DURABLE_CF_HOST": "false", "NODE_RUNTIME": "skipped", "DURABLE_CF_HOST": "skipped"}, false},
		{"merge group without a plan", map[string]string{"FULL": "", "RUN_NODE_RUNTIME": ""}, false},
		{"push skipped queue-only job", map[string]string{"EVENT": "push", "NODE_RUNTIME": "skipped"}, false},
		{"push with every job", map[string]string{"EVENT": "push"}, true},
		{"full pull request", map[string]string{"EVENT": "pull_request", "DCO": "success"}, true},
		{"full pull request skipped queue-only job", map[string]string{"EVENT": "pull_request", "DCO": "success", "NODE_RUNTIME": "skipped"}, false},
		{"light pull request", map[string]string{"EVENT": "pull_request", "DCO": "success", "FULL": "false", "RUN_NODE_RUNTIME": "false", "RUN_DURABLE_CF_HOST": "false", "NODE_RUNTIME": "skipped", "DURABLE_CF_HOST": "skipped"}, true},
		{"light pull request skipped fast job", map[string]string{"EVENT": "pull_request", "DCO": "success", "FULL": "false", "RUN_NODE_RUNTIME": "false", "RUN_DURABLE_CF_HOST": "false", "NODE_RUNTIME": "skipped", "DURABLE_CF_HOST": "skipped", "ANDROID": "skipped"}, false},
		{"light pull request without DCO", map[string]string{"EVENT": "pull_request", "DCO": "skipped", "FULL": "false", "RUN_NODE_RUNTIME": "false", "RUN_DURABLE_CF_HOST": "false", "NODE_RUNTIME": "skipped", "DURABLE_CF_HOST": "skipped"}, false},
		{"docs-only merge group", map[string]string{"PRODUCT": "false", "DOCS": "success", "PREFLIGHT": "skipped", "NODE_RUNTIME": "skipped", "WINDOWS": "skipped", "MACOS": "skipped", "ANDROID": "skipped", "NODE_LOADER": "skipped", "DURABLE_CF_HOST": "skipped"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := os.Environ()
			values := map[string]string{}
			for name, value := range base {
				values[name] = value
			}
			for name, value := range tc.set {
				values[name] = value
			}
			for name, value := range values {
				env = append(env, name+"="+value)
			}
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "-euo", "pipefail", "-c", script)
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("pass = %v, want %v\n%s", err == nil, tc.pass, output)
			}
		})
	}
}

// A docs-only change is classified the same way in a merge group as in its pull request, and a diff that cannot be computed
// fails the job: it must not read as an empty change that skips the product suites.
func TestChangeClassifierReadsMergeGroups(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		cmd.Dir = repo
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	commit := func(path, message string) string {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(message), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", message)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q")
	base := commit("README.md", "base")
	prose := commit("docs/media/logo.txt", "prose")
	source := commit("coding/session.go", "source")
	script := filepath.Join(repoRoot(t), "automation", "ci", "classify-changes.sh")
	classify := func(event, from, to string) (string, error) {
		cmd := testenv.ScriptCommand(t, script, event, from, to)
		cmd.Dir = repo
		output, err := cmd.Output()
		return strings.TrimSpace(string(output)), err
	}
	for _, tc := range []struct {
		event, from, to, want string
	}{
		{"pull_request", base, prose, "product=false"},
		{"merge_group", base, prose, "product=false"},
		{"merge_group", prose, source, "product=true"},
		{"merge_group", base, source, "product=true"},
		{"push", "", "", "product=true"},
		{"schedule", "", "", "product=true"},
		{"workflow_dispatch", "", "", "product=true"},
	} {
		got, err := classify(tc.event, tc.from, tc.to)
		if err != nil || got != tc.want {
			t.Errorf("%s %.7s..%.7s: got %q, %v, want %q", tc.event, tc.from, tc.to, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ event, from, to string }{
		{"merge_group", "", prose},
		{"merge_group", base, ""},
		{"merge_group", "0000000000000000000000000000000000000000", prose},
		{"pull_request", base, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
	} {
		if got, err := classify(tc.event, tc.from, tc.to); err == nil {
			t.Errorf("%s %q..%q was classified %q, want a failure", tc.event, tc.from, tc.to, got)
		}
	}
}

// gitleaks-action reports "event not yet supported" on merge_group and fails the job; the commits of a merge group are the pull
// request's own, which its pull_request run scanned. TruffleHog runs in both and scans the commits the queue added.
func TestSecretScanRunsInTheMergeGroup(t *testing.T) {
	job := readWorkflow(t, "security.yml").Jobs["secret-scan"]
	var gitleaks, trufflehog bool
	for _, step := range job.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "gitleaks/gitleaks-action@"):
			gitleaks = true
			if step.If != "github.event_name != 'merge_group'" {
				t.Errorf("the Gitleaks step has if: %q, want it skipped on merge_group, which the action rejects", step.If)
			}
		case strings.HasPrefix(step.Uses, "trufflesecurity/trufflehog@"):
			trufflehog = true
			if step.If != "" {
				t.Errorf("the TruffleHog step has if: %q, want it to run on every event", step.If)
			}
			if step.With["base"] != "${{ github.event.merge_group.base_sha }}" || step.With["head"] != "${{ github.event.merge_group.head_sha }}" {
				t.Errorf("TruffleHog scans base %v head %v, want the merge group's range", step.With["base"], step.With["head"])
			}
		}
	}
	if !gitleaks || !trufflehog {
		t.Fatalf("secret-scan has gitleaks = %v, trufflehog = %v", gitleaks, trufflehog)
	}
}

// .github/AGENTS.md renders ci-tiers.txt for the people who read it, so the table cannot say a check runs where it does not.
func TestAgentsDocRendersTheTierTable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	singles := map[string]string{"Node extensions": "node-runtime", "Durable Cloudflare host": "durable-cf-host", "Android build": "android", "Node loader": "node-loader"}
	prefixes := map[string]string{"Linux / ": "linux", "Windows ": "windows", "macOS ": "macos"}
	documented := map[[2]string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if !strings.HasPrefix(line, "| ") || len(cells) < 3 {
			continue
		}
		name, tier := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if tier != "pr" && tier != "queue" {
			continue
		}
		matched := false
		for prefix, job := range prefixes {
			if rest, ok := strings.CutPrefix(name, prefix); ok {
				for _, shard := range strings.Split(rest, ", ") {
					documented[[2]string{job, shard}] = tier
				}
				matched = true
			}
		}
		for label, job := range singles {
			if strings.HasPrefix(name, label) {
				documented[[2]string{"single", job}] = tier
				matched = true
			}
		}
		if !matched {
			t.Errorf(".github/AGENTS.md table row %q names no check in ci-tiers.txt", name)
		}
	}
	listed := map[[2]string]bool{}
	for _, row := range tierRows(t) {
		key := [2]string{row.job, row.shard}
		listed[key] = true
		if got := documented[key]; got != row.tier {
			t.Errorf(".github/AGENTS.md lists %s %q in tier %q, ci-tiers.txt says %q", row.job, row.shard, got, row.tier)
		}
	}
	for key, tier := range documented {
		if !listed[key] {
			t.Errorf(".github/AGENTS.md lists %s %q (%s), which ci-tiers.txt does not have", key[0], key[1], tier)
		}
	}
}
