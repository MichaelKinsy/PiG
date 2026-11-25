// SPDX-License-Identifier: MIT

package cidag

import (
	"fmt"
	"slices"
	"strings"
)

// DefaultMaxFiles is the fail-closed ceiling on changed files: a change larger
// than this skips graph pruning and runs the full CI plan.
const DefaultMaxFiles = 300

// DefaultModule is the root Go module path for PiG.
const DefaultModule = "github.com/MichaelKinsy/PiG"

// DefaultBuildTags matches the build tags used by changed-packages.py and CI.
const DefaultBuildTags = "integration,live,parity"

// DefaultGOOSList is the set of operating systems whose package graphs are
// unioned so platform-gated files and reverse dependencies stay visible.
var DefaultGOOSList = []string{"linux", "windows", "darwin"}

// GlobalGateLanes are the fail-fast gate lanes that remain locked in for every
// non-docs-only change (CI-DAG-DESIGN.md section 2.4 and 4.1).
var GlobalGateLanes = []string{"preflight", "build", "checks", "contracts"}

// GlobalGuardPackages are tree-wide guard packages always selected for every
// non-docs-only change so repository invariants stay verified.
var GlobalGuardPackages = []string{
	"./internal/testenv",
	"./test/ci-images",
	"./test/docs-drift",
}

// Tier classifies a CI lane in the two-level tiering lattice (pr <= full).
type Tier string

const (
	TierPR    Tier = "pr"
	TierQueue Tier = "queue"
)

// Mode controls the DAG selector integration in ci-plan.sh and the CLI.
type Mode string

const (
	ModeOff    Mode = "off"
	ModeShadow Mode = "shadow"
	ModeOn     Mode = "on"
)

// ParseMode validates a PIG_CI_DAG mode string. Empty string means ModeOff.
func ParseMode(s string) (Mode, error) {
	switch strings.TrimSpace(s) {
	case "", string(ModeOff):
		return ModeOff, nil
	case string(ModeShadow):
		return ModeShadow, nil
	case string(ModeOn):
		return ModeOn, nil
	default:
		return "", fmt.Errorf("PIG_CI_DAG is %q, want off, shadow or on", s)
	}
}

// LaneSpec describes one lane in the PiG CI catalogue.
type LaneSpec struct {
	Name       string  `json:"name"`
	Group      string  `json:"group"` // preflight, linux, windows, macos, single
	Tier       Tier    `json:"tier"`
	FullMin    float64 `json:"full_min"`
	SetupMin   float64 `json:"setup_min"`
	GlobalGate bool    `json:"global_gate,omitempty"`
}

// LaneCatalogue is the canonical PiG CI lane catalogue aligned with ci-tiers.txt
// and CI-DAG-DESIGN.md. Minutes are full-cost / setup-floor estimates for
// simulation and replay reporting.
var LaneCatalogue = []LaneSpec{
	// Linux gates (global-invariant lock-in for every non-docs change)
	{Name: "preflight", Group: "preflight", Tier: TierPR, FullMin: 20, SetupMin: 20, GlobalGate: true},
	{Name: "build", Group: "linux", Tier: TierPR, FullMin: 10, SetupMin: 10, GlobalGate: true},
	{Name: "checks", Group: "linux", Tier: TierPR, FullMin: 9, SetupMin: 9, GlobalGate: true},
	{Name: "contracts", Group: "linux", Tier: TierPR, FullMin: 8, SetupMin: 8, GlobalGate: true},

	// Linux unit, conformance, ratchet, parity, and portmap lanes
	{Name: "test-fast", Group: "linux", Tier: TierPR, FullMin: 26, SetupMin: 5},
	{Name: "test-fast-sharded", Group: "linux", Tier: TierPR, FullMin: 17, SetupMin: 4},
	{Name: "test-fast-sharded-2", Group: "linux", Tier: TierPR, FullMin: 15, SetupMin: 4},
	{Name: "alloc-ratchet", Group: "linux", Tier: TierPR, FullMin: 6, SetupMin: 2},
	{Name: "test-cli-1", Group: "linux", Tier: TierPR, FullMin: 15, SetupMin: 5},
	{Name: "test-cli-2", Group: "linux", Tier: TierPR, FullMin: 12, SetupMin: 5},
	{Name: "test-subprocess", Group: "linux", Tier: TierPR, FullMin: 12, SetupMin: 5},
	{Name: "test-conformance", Group: "linux", Tier: TierQueue, FullMin: 18, SetupMin: 5},
	{Name: "toolchains", Group: "linux", Tier: TierQueue, FullMin: 7, SetupMin: 3},
	{Name: "parity-1", Group: "linux", Tier: TierPR, FullMin: 12, SetupMin: 4},
	{Name: "parity-2", Group: "linux", Tier: TierQueue, FullMin: 12, SetupMin: 4},
	{Name: "portmap", Group: "linux", Tier: TierQueue, FullMin: 44, SetupMin: 12},

	// Single (non-matrix) jobs
	{Name: "node-runtime", Group: "single", Tier: TierQueue, FullMin: 11, SetupMin: 5},
	{Name: "chord", Group: "single", Tier: TierQueue, FullMin: 8, SetupMin: 3},
	{Name: "durable-cf-host", Group: "single", Tier: TierQueue, FullMin: 25, SetupMin: 8},
	{Name: "durable-contract", Group: "single", Tier: TierQueue, FullMin: 24, SetupMin: 8},
	{Name: "android", Group: "single", Tier: TierPR, FullMin: 5, SetupMin: 3},
	{Name: "node-loader", Group: "single", Tier: TierQueue, FullMin: 9, SetupMin: 9},

	// Windows matrix lanes
	{Name: "win-native-1", Group: "windows", Tier: TierQueue, FullMin: 17, SetupMin: 8},
	{Name: "win-native-2", Group: "windows", Tier: TierQueue, FullMin: 18.7, SetupMin: 8},
	{Name: "win-native-3", Group: "windows", Tier: TierQueue, FullMin: 18.7, SetupMin: 8},
	{Name: "win-native-4", Group: "windows", Tier: TierQueue, FullMin: 18.7, SetupMin: 8},
	{Name: "win-cli", Group: "windows", Tier: TierQueue, FullMin: 96, SetupMin: 40},
	{Name: "win-ext-host", Group: "windows", Tier: TierQueue, FullMin: 35, SetupMin: 18},
	{Name: "win-ext-conformance", Group: "windows", Tier: TierQueue, FullMin: 38, SetupMin: 16},

	// macOS matrix lanes
	{Name: "mac-native", Group: "macos", Tier: TierQueue, FullMin: 13, SetupMin: 3},
	{Name: "mac-cli", Group: "macos", Tier: TierQueue, FullMin: 36, SetupMin: 6},
	{Name: "mac-ext-host", Group: "macos", Tier: TierQueue, FullMin: 35, SetupMin: 6},
}

var (
	laneIndex map[string]LaneSpec
	allLanes  []string
	fullMin   float64
)

func init() {
	laneIndex = make(map[string]LaneSpec, len(LaneCatalogue))
	allLanes = make([]string, 0, len(LaneCatalogue))
	for _, spec := range LaneCatalogue {
		laneIndex[spec.Name] = spec
		allLanes = append(allLanes, spec.Name)
		fullMin += spec.FullMin
	}
	slices.Sort(allLanes)
}

// AllLanes returns a sorted copy of every lane name in the catalogue.
func AllLanes() []string {
	out := make([]string, len(allLanes))
	copy(out, allLanes)
	return out
}

// LookupLane returns the LaneSpec for name, if known.
func LookupLane(name string) (LaneSpec, bool) {
	spec, ok := laneIndex[name]
	return spec, ok
}

// FullMinutes returns the total runner-minute budget across every lane.
func FullMinutes() float64 {
	return fullMin
}

// LaneMinutes returns the unscaled full runner-minute sum for lanes.
func LaneMinutes(lanes []string) float64 {
	var total float64
	seen := make(map[string]struct{}, len(lanes))
	for _, l := range lanes {
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		if spec, ok := laneIndex[l]; ok {
			total += spec.FullMin
		}
	}
	return total
}

// Plan is the machine-readable CI selection plan (ci-plan.json contract).
type Plan struct {
	Selector         string              `json:"selector"`
	Mode             Mode                `json:"mode,omitempty"`
	Event            string              `json:"event,omitempty"`
	Base             string              `json:"base,omitempty"`
	Head             string              `json:"head,omitempty"`
	DocsOnly         bool                `json:"docs_only"`
	Full             bool                `json:"full"`
	FullReason       []string            `json:"full_reason,omitempty"`
	Files            []string            `json:"files,omitempty"`
	FileCount        int                 `json:"file_count"`
	Lanes            []string            `json:"lanes"`
	PRLanes          []string            `json:"pr_lanes"`
	GraphLanes       []string            `json:"graph_lanes,omitempty"`
	Why              map[string][]string `json:"why,omitempty"`
	CompilePackages  []string            `json:"compile_packages,omitempty"`
	TestOnlyPackages []string            `json:"test_only_packages,omitempty"`
	AffectedPackages []string            `json:"affected_packages,omitempty"`
	GuardPackages    []string            `json:"guard_packages,omitempty"`
	ScenariosClosure []string            `json:"scenarios_closure,omitempty"`
	ScenariosDir     []string            `json:"scenarios_dir,omitempty"`
	ScenariosDirect  []string            `json:"scenarios_direct,omitempty"`
	Unowned          []string            `json:"unowned,omitempty"`
	OSSpecific       bool                `json:"os_specific"`
	CompileOSNarrow  bool                `json:"compile_os_narrow"`
	CompileOSBroad   bool                `json:"compile_os_broad"`
	Minutes          float64             `json:"minutes"`
	PRMinutes        float64             `json:"pr_minutes"`
	FullMinutes      float64             `json:"full_minutes"`
	SelectDurationMS float64             `json:"select_duration_ms"`
}

// Options configures Select behavior.
type Options struct {
	MaxFiles int
	Mode     Mode
	Event    string
	Base     string
	Head     string
	// ForceFull forces Full=true with a recorded reason (for example, on
	// non-pull-request events or when ci:full is requested).
	ForceFull       bool
	ForceFullReason string
	// ReadFile overrides file reads (used for //go:build OS-specific inspection
	// and parity fixture scans in unit tests). When nil, reads from Graph.Root.
	ReadFile func(relPath string) ([]byte, error)
}

// AuditReport compares a computed Plan against the lanes that actually failed
// in a full CI run (shadow-mode empirical edge audit, CI-DAG-DESIGN.md §4.4).
type AuditReport struct {
	RunID         string   `json:"run_id,omitempty"`
	Commit        string   `json:"commit,omitempty"`
	PlanFull      bool     `json:"plan_full"`
	Tier          string   `json:"tier"` // "full" (all affected lanes) or "pr" (PR tier)
	SelectedLanes []string `json:"selected_lanes"`
	FailingLanes  []string `json:"failing_lanes"`
	CoveredLanes  []string `json:"covered_lanes"`
	BlindSpots    []string `json:"blind_spots"`
	UnknownLanes  []string `json:"unknown_lanes,omitempty"`
	OK            bool     `json:"ok"`
}
