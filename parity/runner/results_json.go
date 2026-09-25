//go:build parity

package runner

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/coding"
)

// jsonOutcome is the on-disk shape for --pig-parity.results.
// Kept separate from ScenarioOutcome so the test-facing type stays clean
// and the file shape can evolve independently.
type jsonOutcome struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Driver      string   `json:"driver"`
	Group       string   `json:"group"`
	Tags        []string `json:"tags"`
	Covers      []string `json:"covers"`
	Source      string   `json:"source"`
	PigMedian   int64    `json:"pig_median_ms"`
	PiMedian    int64    `json:"pi_median_ms"`
	Passed      bool     `json:"passed"`
	Failures    []string `json:"failures,omitempty"`
}

type jsonResults struct {
	UpstreamVersion string        `json:"upstream_version"`
	Outcomes        []jsonOutcome `json:"outcomes"`
}

func writeResultsJSON(path string, results []*ScenarioOutcome) error {
	out := make([]jsonOutcome, 0, len(results))
	for _, o := range results {
		out = append(out, jsonOutcome{
			Name:        o.Scenario.Name,
			Description: o.Scenario.Description,
			Driver:      o.Scenario.Driver,
			Group:       o.Scenario.SchedulingGroup(),
			Tags:        o.Scenario.Tags,
			Covers:      o.Scenario.Covers,
			Source:      o.Scenario.SourcePath,
			PigMedian:   o.Pig.MedianMs,
			PiMedian:    o.Pi.MedianMs,
			Passed:      o.Passed(),
			Failures:    o.Failures,
		})
	}
	slices.SortFunc(out, func(a, b jsonOutcome) int { return cmp.Compare(a.Name, b.Name) })
	data, err := json.MarshalIndent(jsonResults{UpstreamVersion: coding.UpstreamVersion, Outcomes: out}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".parity-results-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace results: %w", err)
	}
	return nil
}
