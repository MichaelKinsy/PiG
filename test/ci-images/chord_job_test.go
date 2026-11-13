// SPDX-License-Identifier: MIT

package ciimages

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// chordGates are the make targets that run a chord surface's tests under a requirement the general suites do not set, with the
// package each tests and the variable its recipe sets: without CHORD_REQUIRE_TUI_NODE a Node older than the sandbox's floor
// skips the terminal surface's tests, and without CHORD_REQUIRE_CHROMIUM a runner with no browser skips the web surface's
// (internal/browsertest.Start), so a green run would have tested neither.
var chordGates = map[string]struct{ pkg, variable string }{
	"ci-chord-tui": {"./chord/nextnode/tui", "CHORD_REQUIRE_TUI_NODE"},
	"ci-chord-web": {"./chord/nextnode/app", "CHORD_REQUIRE_CHROMIUM"},
}

// makeTargets returns the targets a step's script passes to make by name: on each line, the words after `make` that are
// neither options nor variable assignments.
func makeTargets(run string) []string {
	var targets []string
	for line := range strings.SplitSeq(run, "\n") {
		fields := strings.Fields(line)
		at := slices.IndexFunc(fields, func(field string) bool { return field == "make" || strings.HasSuffix(field, "/make") })
		if at < 0 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, field := range fields[at+1:] {
			if field == "|" || field == "&&" || field == "||" || field == ";" {
				break
			}
			if !strings.HasPrefix(field, "-") && !strings.Contains(field, "=") {
				targets = append(targets, field)
			}
		}
	}
	return targets
}

// jobsRunning returns the jobs of ci.yml that run the make target, one entry per step that runs it.
func jobsRunning(jobs map[string]verificationJob, target string) []string {
	var running []string
	for name, job := range jobs {
		for _, step := range job.Steps {
			if slices.Contains(makeTargets(step.Run), target) {
				running = append(running, name)
			}
		}
	}
	slices.Sort(running)
	return running
}

// Moving the chord tests between jobs must not drop one or run one twice: each chord gate runs in exactly one step of one job
// of ci.yml, no other make target reaches it (so no matrix shard runs it under another name), its recipe sets the variable that
// turns a missing Node or browser into a failure, and the required check fails when that job fails or does not run.
func TestEachChordGateRunsInExactlyOneJobThatCIResultRequires(t *testing.T) {
	sources := readMakeSources(t, repoRoot(t))
	var declared []string
	for _, match := range regexp.MustCompile(`(?m)^(ci-chord-[A-Za-z0-9_-]+):`).FindAllStringSubmatch(sources, -1) {
		declared = append(declared, match[1])
	}
	slices.Sort(declared)
	want := make([]string, 0, len(chordGates))
	for target := range chordGates {
		want = append(want, target)
	}
	slices.Sort(want)
	if !slices.Equal(declared, want) {
		t.Fatalf("make declares the chord gates %v, this test places %v: give a new gate a job and a row here", declared, want)
	}

	jobs := verificationJobs(t)
	result := jobs["result"]
	var resultEnv map[string]string
	for _, step := range result.Steps {
		if strings.Contains(step.Run, "expect()") {
			resultEnv = step.Env
		}
	}
	if resultEnv == nil {
		t.Fatal("the CI result job has no aggregation step")
	}
	for _, target := range want {
		gate := chordGates[target]
		running := jobsRunning(jobs, target)
		if len(running) != 1 {
			t.Errorf("make %s runs in the jobs %v, want exactly one step of one job", target, running)
			continue
		}
		job := running[0]

		word := regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(target) + `($|[^A-Za-z0-9_-])`)
		recipe, inRecipe := "", false
		for line := range strings.SplitSeq(sources, "\n") {
			switch {
			case line == target+":":
				inRecipe = true
			case inRecipe && strings.HasPrefix(line, "\t"):
				recipe += line + "\n"
			default:
				inRecipe = false
				if !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ".PHONY:") && word.MatchString(line) {
					t.Errorf("another make rule reaches %s, so a job could run it under another name: %q", target, line)
				}
			}
		}
		required := false
		for line := range strings.SplitSeq(strings.ReplaceAll(recipe, "\\\n", " "), "\n") {
			fields := strings.Fields(line)
			at := slices.Index(fields, "go")
			if at >= 0 && slices.Contains(fields[at:], "test") && slices.Contains(fields[at:], gate.pkg) && slices.Contains(fields[:at], gate.variable+"=1") {
				required = true
			}
		}
		if !required {
			t.Errorf("the recipe of %s does not run go test %s under %s=1, so a runner without what the tests need skips them and reports success:\n%s", target, gate.pkg, gate.variable, recipe)
		}

		if !slices.Contains(result.Needs, job) {
			t.Errorf("%s runs in the job %s, which CI result does not need: %v", target, job, result.Needs)
			continue
		}
		name := ""
		for key, value := range resultEnv {
			if strings.Join(strings.Fields(value), "") == "${{needs."+job+".result}}" {
				name = key
			}
		}
		if name == "" {
			t.Errorf("the CI result step does not read needs.%s.result", job)
			continue
		}
		for _, outcome := range []string{"failure", "cancelled", "skipped"} {
			if output, err := ciResultRun(t, map[string]string{name: outcome}, ""); err == nil {
				t.Errorf("CI result passed a full run whose %s job (make %s) was %s:\n%s", job, target, outcome, output)
			}
		}
	}
}

// The chord gates have a job of their own (Pi aca89cfe14 gave its chord tests one): it runs both, in the order terminal then
// web, the web surface also when the terminal surface failed; it sets up what the tests read and no more (Go, the Go caches
// read-only, and current Node, the one release that has the terminal surface's sandbox); and the plan runs it in the tier
// that runs Node extensions, the job the two steps came from, which runs neither now.
func TestChordSurfacesRunInTheirOwnJob(t *testing.T) {
	jobs := verificationJobs(t)
	job, ok := jobs["chord"]
	if !ok {
		t.Fatal("ci.yml has no chord job")
	}
	for target := range chordGates {
		if running := jobsRunning(jobs, target); !slices.Equal(running, []string{"chord"}) {
			t.Errorf("make %s runs in %v, want the chord job alone", target, running)
		}
	}
	for _, need := range []string{"changes", "bookkeeping"} {
		if !slices.Contains(job.Needs, need) {
			t.Errorf("the chord job does not wait for %s: %v", need, job.Needs)
		}
	}
	// The recorded runs of both targets together took under two minutes (the comment at the job); the limit leaves the runner's
	// setup and a cold Go build cache room and still ends a hung browser long before the merge queue's 120-minute wait.
	if job.Timeout < 10 || job.Timeout > 30 {
		t.Errorf("chord timeout = %d minutes, want 10 to 30", job.Timeout)
	}
	tui, web, node, goSetup := -1, -1, -1, -1
	for i, step := range job.Steps {
		targets := makeTargets(step.Run)
		switch {
		case strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["go-version-file"] == ".go-version":
			goSetup = i
		case strings.HasPrefix(step.Uses, "actions/setup-node@"):
			if node >= 0 || step.With["node-version-file"] != "automation/ci/node-current.version" {
				t.Errorf("chord step %d sets up Node from %q; the job runs under current Node alone", i, step.With["node-version-file"])
			}
			node = i
		case strings.HasPrefix(step.Uses, "actions/cache/save@"):
			t.Errorf("chord step %d saves a cache; the jobs that fill the Go caches save them", i)
		case slices.Contains(targets, "ci-chord-tui"):
			tui = i
		case slices.Contains(targets, "ci-chord-web"):
			web = i
			if step.If != "${{ !cancelled() }}" {
				t.Errorf("the web surface step has if: %q, want it to run when the terminal surface's tests failed (${{ !cancelled() }})", step.If)
			}
		case len(targets) > 0 || strings.Contains(step.Run, "install-"):
			t.Errorf("chord step %d does more than the chord gates need: %q", i, step.Run)
		}
		if len(targets) > 0 && !strings.Contains(step.Run, "automation/ci/with-isolated-pig-home.sh make "+targets[0]+"\n") {
			t.Errorf("chord step %d does not run make %s under an isolated PiG home:\n%s", i, targets[0], step.Run)
		}
	}
	if goSetup < 0 || node < goSetup || tui < node || web < tui {
		t.Errorf("chord steps: setup-go at %d, setup-node at %d, ci-chord-tui at %d, ci-chord-web at %d; want them all, in this order", goSetup, node, tui, web)
	}
	for _, step := range jobs["node-runtime"].Steps {
		if strings.Contains(step.Run, "chord") {
			t.Errorf("the Node extensions job still runs a chord step:\n%s", step.Run)
		}
	}
	tiers := map[string]string{}
	for _, row := range tierRows(t) {
		if row.job == "single" {
			tiers[row.shard] = row.tier
		}
	}
	if tiers["chord"] == "" || tiers["chord"] != tiers["node-runtime"] {
		t.Errorf("ci-tiers.txt runs chord in tier %q and node-runtime in %q, want the same tier", tiers["chord"], tiers["node-runtime"])
	}
}
