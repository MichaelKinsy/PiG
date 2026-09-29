package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/coding"
)

// Test dispositions report file-level porting, not executed tests or passing cases.
// testinventorycheck owns the evidence and release-policy validation gates.
type testPortingStats struct {
	Ported, Partial, Pending, Other int
	Deferred                        int
	PolicyAvailable                 bool
}

func loadTestPortingStats(root string) (testPortingStats, error) {
	var stats testPortingStats
	path := filepath.Join(root, "test/parity", "interfaces", "test-mapping-v"+coding.UpstreamVersion+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return stats, err
	}
	var mapping struct {
		UpstreamVersion string `json:"upstreamVersion"`
		Entries         []struct {
			Path        string `json:"path"`
			Disposition string `json:"disposition"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &mapping); err != nil {
		return stats, fmt.Errorf("%s: %w", path, err)
	}
	if mapping.UpstreamVersion != coding.UpstreamVersion {
		return stats, fmt.Errorf("%s: upstreamVersion = %q, want %q", path, mapping.UpstreamVersion, coding.UpstreamVersion)
	}
	dispositions := make(map[string]string, len(mapping.Entries))
	for _, entry := range mapping.Entries {
		if entry.Path == "" || dispositions[entry.Path] != "" {
			return stats, fmt.Errorf("%s: empty or duplicate test path %q", path, entry.Path)
		}
		dispositions[entry.Path] = entry.Disposition
		switch entry.Disposition {
		case "ported":
			stats.Ported++
		case "partial":
			stats.Partial++
		case "pending":
			stats.Pending++
		case "scenario-covered", "designed-out", "divergence":
			stats.Other++
		default:
			return stats, fmt.Errorf("%s: unknown test disposition %q", path, entry.Disposition)
		}
	}

	path = filepath.Join(root, "test/parity", "interfaces", "test-porting-policy-v"+coding.UpstreamVersion+".json")
	data, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stats, nil
	}
	if err != nil {
		return stats, err
	}
	var policy struct {
		UpstreamVersion string `json:"upstreamVersion"`
		Entries         []struct {
			Path string   `json:"path"`
			Tags []string `json:"tags"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return stats, fmt.Errorf("%s: %w", path, err)
	}
	if policy.UpstreamVersion != coding.UpstreamVersion {
		return stats, fmt.Errorf("%s: upstreamVersion = %q, want %q", path, policy.UpstreamVersion, coding.UpstreamVersion)
	}
	stats.PolicyAvailable = true
	seen := make(map[string]bool, len(policy.Entries))
	for _, entry := range policy.Entries {
		disposition, ok := dispositions[entry.Path]
		if !ok || seen[entry.Path] {
			return stats, fmt.Errorf("%s: unknown or duplicate test path %q", path, entry.Path)
		}
		seen[entry.Path] = true
		if (disposition == "pending" || disposition == "partial") && slices.Contains(entry.Tags, "deferred-0.3.x") {
			stats.Deferred++
		}
	}
	return stats, nil
}

func (s testPortingStats) summaryLine() string {
	line := fmt.Sprintf("**Upstream test porting:** %d ported / %d partial / %d pending (%d other dispositions; [file mapping](interfaces/test-mapping-v%s.json)).",
		s.Ported, s.Partial, s.Pending, s.Other, coding.UpstreamVersion)
	if !s.PolicyAvailable {
		return line + " Deferral count unavailable (release policy absent)."
	}
	return line + fmt.Sprintf(" Of the partial/pending files, %d deferred to 0.3.x by the [release policy](interfaces/test-porting-policy-v%s.json) (`deferred-0.3.x`).", s.Deferred, coding.UpstreamVersion)
}
