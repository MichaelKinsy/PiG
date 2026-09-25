//go:build parity

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// DiscoverScenarios walks parity/scenarios/**/*.toml and returns scenarios
// sorted by relative path. Canonical scenarios live in behavior directories
// (e.g. scenarios/model/, scenarios/settings/); legacy .tmux files may still
// coexist at the top level until migrated.
func DiscoverScenarios(dir string) ([]*Scenario, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".toml") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk scenarios dir %s: %w", dir, err)
	}
	sort.Strings(paths)

	var out []*Scenario
	for _, p := range paths {
		sc, err := LoadScenario(p)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// RunScenario executes one scenario against both binaries and returns
// the populated ScenarioOutcome. When skipPerfGate is true, EvaluateOutcome
// records the runtime ratio but does not enforce it.
func RunScenario(ctx context.Context, t *testing.T, sc *Scenario, pig, pi BinaryRef, skipPerfGate bool) *ScenarioOutcome {
	t.Helper()
	d, ok := DriverRegistry[sc.Driver]
	if !ok {
		return &ScenarioOutcome{
			Scenario: sc,
			Failures: []string{fmt.Sprintf("unknown driver %q", sc.Driver)},
		}
	}

	// Authentication for requires-auth scenarios is injected only into the
	// ephemeral directory snapshots created by drivers. Fixture roots stay
	// immutable and credential-free.
	o := &ScenarioOutcome{Scenario: sc, SkipPerfGate: skipPerfGate}
	o.Pig.System = "pig"
	o.Pi.System = "pi"
	runs := sc.Assert.Runs
	if runs <= 0 {
		runs = 1
	}
	for run := range runs {
		var pigResult, piResult Result
		switch {
		case skipPerfGate:
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				pigResult = d.Run(ctx, t, pig, sc)
			}()
			go func() {
				defer wg.Done()
				piResult = d.Run(ctx, t, pi, sc)
			}()
			wg.Wait()
		case sc.Assert.RuntimeRatioMax <= 0 || run%2 == 0:
			pigResult = d.Run(ctx, t, pig, sc)
			piResult = d.Run(ctx, t, pi, sc)
		default:
			// Serial performance pairs alternate launch order so filesystem and
			// runtime warming do not always benefit Pi, the second process.
			piResult = d.Run(ctx, t, pi, sc)
			pigResult = d.Run(ctx, t, pig, sc)
		}
		pigResult = maskResultCWDSnapshotIDs(pigResult)
		piResult = maskResultCWDSnapshotIDs(piResult)
		pigResult.System = pig.Label
		o.Pig.Runs = append(o.Pig.Runs, pigResult)
		piResult.System = pi.Label
		o.Pi.Runs = append(o.Pi.Runs, piResult)

		// Assertions are checked after every paired run. Durability runs continue
		// only while every completed pair is green; retries and remaining declared
		// runs cannot turn an earlier failure into a pass. Runtime-ratio checks need
		// the complete sample and are evaluated only below.
		probe := &ScenarioOutcome{
			Scenario:     sc,
			Pig:          o.Pig,
			Pi:           o.Pi,
			SkipPerfGate: true,
		}
		EvaluateOutcome(probe)
		if len(probe.Failures) > 0 {
			break
		}
	}
	EvaluateOutcome(o)
	return o
}

func scenarioWithRuns(sc *Scenario, runs int) *Scenario {
	if runs <= 0 {
		return sc
	}
	copy := *sc
	copy.Assert.Runs = runs
	return &copy
}

// FilterByTags returns scenarios whose tag list contains every requested tag.
// An empty request returns all scenarios. AND semantics prevent a command such
// as "fast,hermetic" from silently admitting fast scenarios that require live
// credentials or network access.
// FilterByRuntimeRatio keeps scenarios with an explicit performance contract.
func FilterByRuntimeRatio(scenarios []*Scenario) []*Scenario {
	filtered := make([]*Scenario, 0, len(scenarios))
	for _, scenario := range scenarios {
		if scenario.Assert.RuntimeRatioMax > 0 {
			filtered = append(filtered, scenario)
		}
	}
	return filtered
}

func FilterByTags(scenarios []*Scenario, want []string) []*Scenario {
	if len(want) == 0 {
		return scenarios
	}
	out := make([]*Scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		matches := true
		for _, w := range want {
			if !sc.HasTag(w) {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, sc)
		}
	}
	return out
}

// FilterByDrivers returns scenarios handled by any requested driver. Driver
// names are alternatives so one command can audit all non-interactive modes.
func FilterByDrivers(scenarios []*Scenario, want []string) []*Scenario {
	if len(want) == 0 {
		return scenarios
	}
	wanted := make(map[string]bool, len(want))
	for _, driver := range want {
		wanted[driver] = true
	}
	out := make([]*Scenario, 0, len(scenarios))
	for _, scenario := range scenarios {
		if wanted[scenario.Driver] {
			out = append(out, scenario)
		}
	}
	return out
}
