//go:build parity

// Package runner test entry point.
//
// Run with:
//
//	go test -tags=parity ./parity/runner -v -timeout 30m
//
// Each scenario in parity/scenarios/**/*.toml becomes a subtest. Failures
// are symmetric: both pig and pi go through identical assertions. The
// test fails if any assertion fails, including the perf gate
// (runtime_ratio_max).
package runner

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	flagScenarioDir = flag.String("pig-parity.dir", "", "override scenario directory")
	flagTags        = flag.String("pig-parity.tags", "", "comma-separated tags all required for inclusion (empty = all)")
	flagDrivers     = flag.String("pig-parity.drivers", "", "comma-separated scenario drivers to include (empty = all)")
	flagResultsOut  = flag.String("pig-parity.results", "", "write JSON results to this path")
	flagSerial      = flag.Bool("pig-parity.serial", false, "force serial execution (default: parallel where safe)")
	flagGroupLimits = flag.String("pig-parity.group-limits", defaultParityGroupLimits, "bounded concurrency buckets (name=n,name=n)")
	flagArtifacts   = flag.String("pig-parity.artifacts", "", "write failure artifacts under this directory (default: parity/artifacts)")
	flagAllowStale  = flag.Bool("pig-parity.allow-stale", false, "allow running parity against a pig binary older than source files")
	flagRuns        = flag.Int("pig-parity.runs", 0, "override each scenario's declared run count (0 preserves the scenario)")
	flagRuntimeOnly = flag.Bool("pig-parity.runtime-ratio-only", false, "run only scenarios with runtime_ratio_max")
)

// TestParity is the single Go test entry point for the parity suite.
// Every scenario becomes a t.Run subtest named after Scenario.Name.
func TestParity(t *testing.T) {
	dir := *flagScenarioDir
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	canonicalScenarioDir := filepath.Join(repoRoot, "parity", "scenarios")
	if dir == "" {
		// Default: parity/scenarios relative to this package source dir.
		dir = canonicalScenarioDir
	}
	validatesCompleteSkipManifest := filepath.Clean(dir) == filepath.Clean(canonicalScenarioDir)
	artifactsDir := *flagArtifacts
	if artifactsDir == "" {
		artifactsDir = filepath.Join(repoRoot, "parity", "artifacts")
	}
	scenarios, err := DiscoverScenarios(dir)
	if err != nil {
		t.Fatalf("discover scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenarios found under %s", dir)
	}
	allScenarios := append([]*Scenario(nil), scenarios...)

	var tags []string
	if s := strings.TrimSpace(*flagTags); s != "" {
		tags = strings.Split(s, ",")
	}
	scenarios = FilterByTags(scenarios, tags)
	var drivers []string
	if value := strings.TrimSpace(*flagDrivers); value != "" {
		drivers = strings.Split(value, ",")
	}
	scenarios = FilterByDrivers(scenarios, drivers)
	if *flagRuntimeOnly {
		scenarios = FilterByRuntimeRatio(scenarios)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no scenarios match required tags %v and drivers %v", tags, drivers)
	}
	groupLimits, err := parseGroupLimits(*flagGroupLimits)
	if err != nil {
		t.Fatalf("parse group limits: %v", err)
	}
	for _, sc := range scenarios {
		group := sc.SchedulingGroup()
		if group == "exclusive" {
			continue
		}
		if _, ok := groupLimits[group]; !ok {
			groupLimits[group] = groupLimits["process"]
		}
	}
	groupSem := map[string]chan struct{}{}
	for group, n := range groupLimits {
		groupSem[group] = make(chan struct{}, n)
	}

	pig := ResolvePigBin(t)
	pi := pinPiPackageDir(t, ResolveUpstreamPiBin(t))
	freshness := requireFreshPig(t, repoRoot, pig, *flagAllowStale)

	t.Logf("comparing pig (%s) vs pi (%s; upstream %s)",
		pig.Path, pi.Path, UpstreamVersion(pi))
	t.Logf("pig freshness: binary=%s newest_source=%s fresh=%t",
		freshness.BinaryModTime.Format("2006-01-02T15:04:05Z07:00"), freshness.NewestPath, freshness.Fresh)
	t.Logf("schedule: %s", formatScheduleSummary(scenarios, groupLimits))

	hasAuth := credentialsAvailable()

	results := make([]*ScenarioOutcome, 0, len(scenarios))
	var skipped []string // names of scenarios that were skipped
	var resultsMu sync.Mutex
	// Default posture: every scenario runs in parallel. Scenarios opt OUT
	// with the "serial" tag (state dependencies the harness can't isolate).
	// The runtime_ratio_max perf gate is unreliable under contention, so
	// it's enforced ONLY in serial mode (--pig-parity.serial=true) and
	// MEASURED + logged in parallel mode. Use `make parity-perf` to gate
	// performance authoritatively.
	parallelMode := !*flagSerial
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			group := sc.SchedulingGroup()
			canParallel := parallelMode && group != "exclusive"
			if canParallel {
				t.Parallel()
			}
			if canParallel {
				if sem := groupSem[group]; sem != nil {
					sem <- struct{}{}
					t.Cleanup(func() { <-sem })
				}
			}
			if sc.HasTag("deferred") {
				resultsMu.Lock()
				skipped = append(skipped, sc.Name+" (deferred)")
				resultsMu.Unlock()
				t.Skipf("scenario is deferred (cross-family drift parked here; "+
					"unblock by removing the 'deferred' tag and fixing the covered files) path=%s",
					sc.SourcePath)
			}
			if sc.HasTag("requires-auth") && !hasAuth {
				resultsMu.Lock()
				skipped = append(skipped, sc.Name+" (requires-auth)")
				resultsMu.Unlock()
				t.Skipf("scenario tagged requires-auth but PIG_PARITY_REAL_AUTH is not set to a real auth.json; "+
					"the runner never reads ~/.pi/agent/auth.json or ~/.pig/agent/auth.json implicitly (that would silently "+
					"expose the operator's real credentials). Set PIG_PARITY_REAL_AUTH=/path/to/auth.json to opt in (path=%s)",
					sc.SourcePath)
			}
			if sc.Driver == "headless-terminal" {
				if _, err := exec.LookPath("ht"); err != nil {
					resultsMu.Lock()
					skipped = append(skipped, sc.Name+" (headless-terminal)")
					resultsMu.Unlock()
					t.Skipf("headless-terminal driver requires ht on PATH (path=%s)", sc.SourcePath)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runScenario := scenarioWithRuns(sc, *flagRuns)
			o := RunScenario(ctx, t, runScenario, pig, pi, canParallel)
			resultsMu.Lock()
			results = append(results, o)
			resultsMu.Unlock()

			t.Logf("driver=%s tags=%v declared_runs=%d completed_pairs=%d pig.median=%dms pi.median=%dms",
				sc.Driver, sc.Tags, runScenario.Assert.Runs, min(len(o.Pig.Runs), len(o.Pi.Runs)), o.Pig.MedianMs, o.Pi.MedianMs)
			if !o.Passed() {
				if artifactDir, err := WriteFailureArtifacts(t, artifactsDir, o, pig, pi, freshness); err != nil {
					t.Logf("write failure artifacts: %v", err)
				} else if artifactDir != "" {
					t.Logf("failure artifacts: %s", artifactDir)
				}
				for _, f := range o.Failures {
					t.Errorf("%s: %s", sc.Name, f)
				}
				// RunScenario stops at the first failed pair; earlier pairs can match.
				pigRun, piRun := len(o.Pig.Runs)-1, len(o.Pi.Runs)-1
				if pigRun >= 0 && piRun >= 0 {
					if d := LineDiff(o.Pig.Runs[pigRun].Output, o.Pi.Runs[piRun].Output); d != "" {
						t.Logf("output diff (pig vs pi):\n%s", d)
					}
				}
				if pigRun >= 0 {
					t.Logf("pig run %d output (truncated):\n%s", pigRun+1, trunc(o.Pig.Runs[pigRun].Output, 1200))
				}
				if piRun >= 0 {
					t.Logf("pi run %d output (truncated):\n%s", piRun+1, trunc(o.Pi.Runs[piRun].Output, 1200))
				}
				// Sibling scenarios sharing covers: these may also be
				// asserting non-faithful behavior and require re-probing
				// in the same loop if a pig fix is the right answer here.
				if siblings := SiblingScenarios(sc, scenarios); len(siblings) > 0 {
					t.Logf("scenarios sharing covers (re-probe these if fixing pig):\n  - %s",
						strings.Join(siblings, "\n  - "))
				}
				t.Logf("%s", FaithfulnessReminder)
			}
		})
	}

	// Skip-allowlist gate: fail loudly if any scenario skips that isn't
	// in the known-skip manifest. This is stronger than a count: it
	// catches the *wrong* scenarios skipping, not just "too many."
	//
	// To add a new allowed skip:
	//   1. Add the scenario name + reason to this map.
	//   2. Explain why the skip is legitimate in the comment.
	//
	// To remove a stale entry: if the suite warns that an allowed skip
	// didn't actually skip, remove it from the map.
	allowedSkips := map[string]string{
		// requires-auth: need real provider credentials
		"01-footer-default":            "requires-auth: footer renders provider-specific model name",
		"02-footer-thinking-indicator": "requires-auth: thinking indicator needs real model connection",
		"08-show-images-selector":      "deferred: upstream public component never wired into interactive flow; not assertable in tmux",
		// Clipboard / image: split from the original 8-covers scenario into per-layer stubs.
		"01-clipboard-read-deferred": "deferred: OS pasteboard read needs hermetic pasteboard backend",
		"02-image-convert-deferred":  "deferred: image-convert.ts only runs on Kitty-capable terminals",
		// headless-terminal: scenarios skip gracefully when ht is not installed
		"05-kitty-settings-visible": "headless-terminal: requires ht CLI (brew install montanaflynn/tap/ht)",
	}
	t.Cleanup(func() {
		resultsMu.Lock()
		skipSnap := append([]string(nil), skipped...)
		resultsMu.Unlock()

		if len(skipSnap) > 0 {
			t.Logf("skipped %d scenario(s):", len(skipSnap))
			for _, s := range skipSnap {
				t.Logf("  - %s", s)
			}
		}

		// Check for unexpected skips (not in allowlist).
		for _, entry := range skipSnap {
			// entry format: "scenario-name (reason)"
			name, _, _ := strings.Cut(entry, " (")
			if _, ok := allowedSkips[name]; !ok {
				t.Errorf("unexpected skip: %s: not in allowedSkips manifest. "+
					"Either add it with justification or fix the scenario so it doesn't skip.",
					entry)
			}
		}

		if validatesCompleteSkipManifest {
			allByName := make(map[string]*Scenario, len(allScenarios))
			for _, scenario := range allScenarios {
				allByName[scenario.Name] = scenario
			}
			for name := range allowedSkips {
				scenario := allByName[name]
				if scenario == nil {
					t.Errorf("stale allowed skip %q: no such scenario", name)
					continue
				}
				if !scenario.HasTag("deferred") && !scenario.HasTag("requires-auth") && scenario.Driver != "headless-terminal" {
					t.Errorf("stale allowed skip %q: scenario can no longer skip", name)
				}
			}
		}

	})

	// Register results writer as a Cleanup so it runs after every
	// parallel subtest finishes. Without this the writeResultsJSON
	// would race the parallel subtests' appends.
	if out := *flagResultsOut; out != "" {
		t.Cleanup(func() {
			resultsMu.Lock()
			snap := append([]*ScenarioOutcome(nil), results...)
			resultsMu.Unlock()
			if err := writeResultsJSON(out, snap); err != nil {
				t.Errorf("write results: %v", err)
			}
		})
	}
}

// findRepoRoot walks up from CWD looking for go.mod with the pig module path.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		modfile := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modfile); err == nil {
			if strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found")
		}
		dir = parent
	}
}

// credentialsAvailable reports whether the operator has explicitly opted into
// real provider credentials via PIG_PARITY_REAL_AUTH. Scenarios tagged
// `requires-auth` skip when this is false.
//
// This never scans ~/.pig or ~/.pi automatically: a scenario tagged
// requires-auth is already a separate class specifically because it needs
// real credentials, and running it just because the operator happens to be
// logged in on this machine would silently copy their real credentials into
// Pi's (or Pig's) parity agent directory. See realAuthSourcePath.
func credentialsAvailable() bool {
	return realAuthSourcePath() != ""
}

func TestWriteFailureArtifacts(t *testing.T) {
	root := t.TempDir()
	scenarioFile := filepath.Join(root, "scenario.toml")
	if err := os.WriteFile(scenarioFile, []byte("name = \"artifact\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "artifact scenario", Driver: "cli-mode", SourcePath: scenarioFile},
		Pig: SystemResults{Runs: []Result{
			{Output: "first pair matched", Escaped: "first pair matched"},
			{Output: "pig out", Escaped: "pig esc"},
		}, MedianMs: 12},
		Pi: SystemResults{Runs: []Result{
			{Output: "first pair matched", Escaped: "first pair matched"},
			{Output: "pi out", Escaped: "pi esc"},
		}, MedianMs: 10},
		Failures: []string{"mismatch"},
	}
	dir, err := WriteFailureArtifacts(t, filepath.Join(root, "artifacts"), o, BinaryRef{Label: "pig", Path: "/tmp/pig"}, BinaryRef{Label: "pi", Path: "/tmp/pi"}, FreshnessReport{Fresh: true})
	if err != nil {
		t.Fatalf("WriteFailureArtifacts: %v", err)
	}
	for _, name := range []string{"context.json", "scenario.toml", "pig.stdout", "pig.escaped", "pi.stdout", "pi.escaped", "diff.txt", "rerun.sh"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("artifact %s missing: %v", name, err)
		}
	}
	for name, want := range map[string]string{
		"pig.stdout": "pig out", "pig.escaped": "pig esc",
		"pi.stdout": "pi out", "pi.escaped": "pi esc",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want the last completed pair %q, not the first passing pair", name, data, want)
		}
	}
	diff, err := os.ReadFile(filepath.Join(dir, "diff.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(diff), "pig out") || !strings.Contains(string(diff), "pi out") {
		t.Errorf("failure diff does not show the last completed pair: %s", diff)
	}
}

func TestWriteResultsJSONSortsParallelCompletionOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	results := []*ScenarioOutcome{
		{Scenario: &Scenario{Name: "z-last"}},
		{Scenario: &Scenario{Name: "a-first"}},
	}
	if err := writeResultsJSON(path, results); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(data), "a-first") > strings.Index(string(data), "z-last") {
		t.Fatalf("results are not sorted: %s", data)
	}
}
