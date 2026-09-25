//go:build parity

package runner

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Default limits should preserve the historical ~full-utilization behavior of
// `make parity` while still giving the runner a place to encode tighter
// per-group caps when a driver or scenario proves contention-sensitive.
//
// Important: `go test -parallel` is still the global upper bound. These group
// limits should not silently serialize the suite back into a 5-10 minute run.
//
// tmux=8 (not 12): most tmux scenarios spawn pi/pig as a child of a
// tmux pane and also load extension subprocesses. At 12-wide, simultaneous
// node cold-starts on a contended machine can push the ready-pattern past
// the default 30s deadline. 8-wide keeps wall time within ~10% of 12-wide
// while eliminating the long tail of timeouts.
//
// Flake policy: if a scenario fails in `make parity` but passes solo, do not
// retry until green. Diagnose the contention model (driver group, node
// cold-starts, fixture isolation, timeout headroom) and encode the result here
// or in the scenario with a documented group-rationale.
const defaultParityGroupLimits = "process=3,process-ext=1,tmux=3,tmux-ext=1,ht=2,rpc=2"

// SchedulingGroup returns the concurrency bucket for a scenario.
// Default posture: every scenario is assigned to a bounded group so
// agents can run `make parity` without remembering bespoke commands.
//
//   - tag `serial` or group = "exclusive" => exclusive (never parallel)
//   - interactive-tmux with a host extension => tmux-ext
//   - interactive-tmux                   => tmux
//   - rpc-mode                           => rpc
//   - print-mode with a host extension   => process-ext
//   - print-mode / cli-mode              => process
//
// "host extension" excludes parity's in-process fixtures (see
// parityFixtureExtensions): a scenario whose only extension is the faux
// provider spins up no subprocess extension host, so it schedules by its
// driver's plain group rather than the throttled -ext group.
//
// Use the optional `group = "..."` field in a scenario to override the
// default mapping when a surface proves more or less parallel-safe than the
// driver-wide default. The scenario linter requires a `# group-rationale:`
// comment for every override so scheduling exceptions remain auditable.
func (sc *Scenario) SchedulingGroup() string {
	if sc.HasTag("serial") {
		return "exclusive"
	}
	if g := strings.TrimSpace(sc.Group); g != "" {
		return g
	}
	switch sc.Driver {
	case "interactive-tmux":
		if sc.hasSchedulingExtensions() {
			return "tmux-ext"
		}
		return "tmux"
	case "headless-terminal":
		return "ht"
	case "rpc-mode":
		return "rpc"
	case "print-mode":
		if sc.hasSchedulingExtensions() {
			return "process-ext"
		}
		return "process"
	default:
		return "process"
	}
}

func (sc *Scenario) HasExtensions() bool {
	return len(sc.Env.PigExtensions) > 0 || len(sc.Env.PiExtensions) > 0
}

// parityFixtureExtensions are parity's own lightweight extensions that load
// in-process on the pi side (no pig subprocess extension host) and so do not
// add the concurrent cold-start contention the -ext scheduler groups guard
// against. test-faux-provider.ts is the deterministic fake-model shim ~half the
// suite injects via pi_extensions; pig serves the same models from its built-in
// ai/test_faux.go with --no-extensions, so these scenarios start no extension
// host on either side. Grouping them as heavy extension scenarios needlessly
// caps them at the -ext limit. Keep this set to genuine in-process fixtures
// only; anything that starts a subprocess host must stay in the -ext groups.
// Mirrored in parity/cmd/lint/main.go (defaultGroupForScenario) so the
// redundant-group linter agrees with this real grouping; keep both in sync.
var parityFixtureExtensions = map[string]bool{
	"test-faux-provider.ts": true,
}

// hasSchedulingExtensions reports whether a scenario loads an extension that
// starts a subprocess extension host (pig side) or a non-fixture pi extension,
// i.e. the extensions whose simultaneous cold-starts the -ext groups throttle.
// In-process parity fixtures (parityFixtureExtensions) do not count, so a
// scenario whose only extension is the faux provider schedules by its driver's
// plain group.
func (sc *Scenario) hasSchedulingExtensions() bool {
	if !sc.HasExtensions() {
		return false
	}
	for _, ext := range sc.Env.PigExtensions {
		if !parityFixtureExtensions[filepath.Base(ext)] {
			return true
		}
	}
	for _, ext := range sc.Env.PiExtensions {
		if !parityFixtureExtensions[filepath.Base(ext)] {
			return true
		}
	}
	return false
}

func parseGroupLimits(spec string) (map[string]int, error) {
	if strings.TrimSpace(spec) == "" {
		spec = defaultParityGroupLimits
	}
	out := map[string]int{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, raw, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("bad group limit %q (want name=n)", part)
		}
		name = strings.TrimSpace(name)
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("bad group limit %q (n must be > 0)", part)
		}
		out[name] = n
	}
	if _, ok := out["process"]; !ok {
		out["process"] = 12
	}
	if _, ok := out["process-ext"]; !ok {
		out["process-ext"] = 2
	}
	if _, ok := out["tmux"]; !ok {
		out["tmux"] = 8
	}
	if _, ok := out["tmux-ext"]; !ok {
		out["tmux-ext"] = 2
	}
	if _, ok := out["ht"]; !ok {
		out["ht"] = 4
	}
	if _, ok := out["rpc"]; !ok {
		out["rpc"] = 6
	}
	return out, nil
}

func formatScheduleSummary(scenarios []*Scenario, limits map[string]int) string {
	counts := map[string]int{}
	for _, sc := range scenarios {
		counts[sc.SchedulingGroup()]++
	}
	groups := make([]string, 0, len(counts))
	for g := range counts {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		limit := "exclusive"
		if n, ok := limits[g]; ok {
			limit = fmt.Sprintf("%d", n)
		}
		parts = append(parts, fmt.Sprintf("%s=%d(limit:%s)", g, counts[g], limit))
	}
	return strings.Join(parts, ", ")
}
