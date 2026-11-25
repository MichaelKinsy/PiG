// SPDX-License-Identifier: MIT

package cidag

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// TierRow represents one row from automation/ci/ci-tiers.txt.
type TierRow struct {
	Job         string
	Shard       string
	Tier        Tier
	LightTarget string
}

// ParseTierTable parses automation/ci/ci-tiers.txt with the same validation
// rules as automation/ci/ci-plan.sh.
func ParseTierTable(data []byte) ([]TierRow, error) {
	var rows []TierRow
	seen := map[[2]string]bool{}
	lines := strings.Split(string(data), "\n")
	for idx, raw := range lines {
		lineNo := idx + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(raw, "|")
		if len(parts) < 3 || len(parts) > 4 {
			return nil, fmt.Errorf("ci-tiers.txt line %d: want \"job | shard | pr|queue [| make target]\", got: %s", lineNo, raw)
		}
		job := strings.TrimSpace(parts[0])
		shard := strings.TrimSpace(parts[1])
		tierStr := strings.TrimSpace(parts[2])
		lightTarget := ""
		if len(parts) == 4 {
			lightTarget = strings.TrimSpace(parts[3])
		}
		switch job {
		case "linux", "windows", "macos", "single":
		default:
			return nil, fmt.Errorf("ci-tiers.txt line %d: unknown job %q", lineNo, job)
		}
		if shard == "" {
			return nil, fmt.Errorf("ci-tiers.txt line %d: missing shard", lineNo)
		}
		if strings.ContainsAny(shard, `"\\`) {
			return nil, fmt.Errorf("ci-tiers.txt line %d: shard has a quote or backslash", lineNo)
		}
		if tierStr != string(TierPR) && tierStr != string(TierQueue) {
			return nil, fmt.Errorf("ci-tiers.txt line %d: unknown tier %q", lineNo, tierStr)
		}
		if lightTarget != "" && job != "linux" {
			return nil, fmt.Errorf("ci-tiers.txt line %d: only a linux row has a light-run make target", lineNo)
		}
		key := [2]string{job, shard}
		if seen[key] {
			return nil, fmt.Errorf("ci-tiers.txt line %d: duplicate row %s | %s", lineNo, job, shard)
		}
		seen[key] = true
		rows = append(rows, TierRow{
			Job:         job,
			Shard:       shard,
			Tier:        Tier(tierStr),
			LightTarget: lightTarget,
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("ci-tiers.txt has no rows")
	}
	return rows, nil
}

// TierRowLane maps one row of automation/ci/ci-tiers.txt to its canonical lane
// name in LaneCatalogue so DAG plans can filter ci-plan.sh matrices cleanly.
// Unknown rows return "" and fail closed (always kept when filtering).
func TierRowLane(row TierRow) string {
	switch row.Job {
	case "single":
		return row.Shard
	case "linux":
		switch {
		case strings.HasPrefix(row.Shard, "portmap-"):
			return "portmap"
		case strings.HasPrefix(row.Shard, "test-conformance-"):
			return "test-conformance"
		default:
			return row.Shard
		}
	case "windows":
		switch {
		case strings.HasPrefix(row.Shard, "cli-"):
			return "win-cli"
		case strings.HasPrefix(row.Shard, "extension-conformance-"):
			return "win-ext-conformance"
		case strings.HasPrefix(row.Shard, "extension-host-"):
			return "win-ext-host"
		case strings.HasPrefix(row.Shard, "native-"):
			return "win-" + row.Shard
		default:
			return ""
		}
	case "macos":
		switch {
		case row.Shard == "native":
			return "mac-native"
		case strings.HasPrefix(row.Shard, "cli-"):
			return "mac-cli"
		case strings.HasPrefix(row.Shard, "extension-host-"):
			return "mac-ext-host"
		default:
			return ""
		}
	default:
		return ""
	}
}

// MatrixEntry is one element of the JSON arrays emitted by ci-plan.sh.
type MatrixEntry struct {
	Shard  string `json:"shard"`
	Target string `json:"target,omitempty"`
}

// CIPlanOutput holds the exact GITHUB_OUTPUT key/value pairs expected by ci.yml
// and test/ci-images/merge_queue_test.go.
type CIPlanOutput struct {
	Full         bool
	FullRequired bool
	Linux        []MatrixEntry
	Windows      []MatrixEntry
	MacOS        []MatrixEntry
	Singles      []SingleRun
}

// SingleRun preserves the exact row order of single jobs in ci-tiers.txt.
type SingleRun struct {
	Name string
	Run  bool
}

// RenderCIPlan applies tier + DAG selection over ci-tiers.txt rows.
// When dagPlan is nil or dagPlan.Full is true, output matches static ci-plan.sh
// behavior. When dagPlan is active (PIG_CI_DAG=on) on a pull_request, rows are
// intersected with the DAG-selected lanes while preserving global gates and
// fail-closed non-empty Linux matrix invariants.
func RenderCIPlan(rows []TierRow, event, lightMode string, fullRequested bool, dagPlan *Plan) (CIPlanOutput, error) {
	switch lightMode {
	case "", "queue", "label":
	default:
		return CIPlanOutput{}, fmt.Errorf("PIG_PR_CI_LIGHT is %q, want empty, queue or label", lightMode)
	}

	full := true
	required := false
	if event == "pull_request" && lightMode != "" {
		if !fullRequested {
			full = false
		}
		if lightMode == "label" {
			required = true
		}
	}

	// Build DAG lane allowlist when a non-full DAG plan is active on a pull request.
	var allowedLanes map[string]bool
	dagActive := event == "pull_request" && dagPlan != nil && !dagPlan.Full && !fullRequested
	if dagActive {
		lanes := dagPlan.Lanes
		if !full {
			lanes = dagPlan.PRLanes
		}
		allowedLanes = make(map[string]bool, len(lanes))
		for _, l := range lanes {
			allowedLanes[l] = true
		}
		// Keep global gates locked in for non-docs changes.
		if !dagPlan.DocsOnly {
			for _, g := range GlobalGateLanes {
				allowedLanes[g] = true
			}
		}
	}

	out := CIPlanOutput{
		Full:         full,
		FullRequired: required,
		Linux:        []MatrixEntry{},
		Windows:      []MatrixEntry{},
		MacOS:        []MatrixEntry{},
	}

	for _, row := range rows {
		runs := full || row.Tier == TierPR
		if dagActive && runs {
			lane := TierRowLane(row)
			// Fail closed: an unmapped row stays enabled.
			if lane != "" && !allowedLanes[lane] {
				runs = false
			}
		}
		if row.Job == "single" {
			out.Singles = append(out.Singles, SingleRun{Name: row.Shard, Run: runs})
			continue
		}
		if !runs {
			continue
		}
		switch row.Job {
		case "linux":
			target := "ci-" + row.Shard
			if !full && row.LightTarget != "" {
				target = row.LightTarget
			}
			out.Linux = append(out.Linux, MatrixEntry{Shard: row.Shard, Target: target})
		case "windows":
			out.Windows = append(out.Windows, MatrixEntry{Shard: row.Shard})
		case "macos":
			out.MacOS = append(out.MacOS, MatrixEntry{Shard: row.Shard})
		}
	}

	// Preserve ci-plan.sh's non-empty matrix guards (GitHub Actions rejects empty matrices).
	if len(out.Linux) == 0 {
		// Fail closed: if DAG pruning removed every Linux row (e.g., docs-only change
		// invoked through ci-plan.sh), keep the global gate rows from ci-tiers.txt.
		for _, row := range rows {
			if row.Job == "linux" && row.Tier == TierPR {
				target := "ci-" + row.Shard
				if !full && row.LightTarget != "" {
					target = row.LightTarget
				}
				out.Linux = append(out.Linux, MatrixEntry{Shard: row.Shard, Target: target})
			}
		}
	}
	if len(out.Linux) == 0 {
		return CIPlanOutput{}, fmt.Errorf("ci-tiers.txt: no linux row runs in this plan")
	}
	if full && !dagActive {
		if len(out.Windows) == 0 {
			return CIPlanOutput{}, fmt.Errorf("ci-tiers.txt: no windows row runs in this plan")
		}
		if len(out.MacOS) == 0 {
			return CIPlanOutput{}, fmt.Errorf("ci-tiers.txt: no macos row runs in this plan")
		}
	}
	return out, nil
}

// FormatGitHubOutput renders CIPlanOutput in the exact line order of ci-plan.sh.
func (o CIPlanOutput) FormatGitHubOutput() (string, error) {
	linuxJSON, err := json.Marshal(o.Linux)
	if err != nil {
		return "", err
	}
	winJSON, err := json.Marshal(o.Windows)
	if err != nil {
		return "", err
	}
	macJSON, err := json.Marshal(o.MacOS)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "full=%t\n", o.Full)
	fmt.Fprintf(&b, "full-required=%t\n", o.FullRequired)
	fmt.Fprintf(&b, "linux=%s\n", linuxJSON)
	fmt.Fprintf(&b, "windows=%s\n", winJSON)
	fmt.Fprintf(&b, "macos=%s\n", macJSON)
	for _, s := range o.Singles {
		fmt.Fprintf(&b, "run-%s=%t\n", s.Name, s.Run)
	}
	return b.String(), nil
}

// LoadTierTable reads and parses path.
func LoadTierTable(path string) ([]TierRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTierTable(data)
}
