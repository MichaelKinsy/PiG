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
