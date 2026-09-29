//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ArtifactContext captures enough state to make a parity failure replayable.
type ArtifactContext struct {
	Scenario        string          `json:"scenario"`
	ScenarioPath    string          `json:"scenarioPath"`
	Driver          string          `json:"driver"`
	Timestamp       string          `json:"timestamp"`
	PigPath         string          `json:"pigPath"`
	PiPath          string          `json:"piPath"`
	PigFreshness    FreshnessReport `json:"pigFreshness"`
	Failures        []string        `json:"failures"`
	PigMedianMs     int64           `json:"pigMedianMs"`
	PiMedianMs      int64           `json:"piMedianMs"`
	SkipPerformance bool            `json:"skipPerformance"`
}

// WriteFailureArtifacts writes escape-preserving outputs, context, diffs, and a rerun script for one failed scenario. Captures use the last completed pair: RunScenario stops at the first behavioral failure, so an earlier pair can be green. The artifact directory is returned so the test log can point at it.
func WriteFailureArtifacts(t *testing.T, root string, o *ScenarioOutcome, pig, pi BinaryRef, freshness FreshnessReport) (string, error) {
	t.Helper()
	if root == "" {
		return "", nil
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(root, sanitizeArtifactName(o.Scenario.Name), now)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	write := func(name, data string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644)
	}
	if data, err := os.ReadFile(o.Scenario.SourcePath); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "scenario.toml"), data, 0o644); err != nil {
			return "", err
		}
	}
	ctx := ArtifactContext{
		Scenario:        o.Scenario.Name,
		ScenarioPath:    o.Scenario.SourcePath,
		Driver:          o.Scenario.Driver,
		Timestamp:       now,
		PigPath:         pig.Path,
		PiPath:          pi.Path,
		PigFreshness:    freshness,
		Failures:        append([]string(nil), o.Failures...),
		PigMedianMs:     o.Pig.MedianMs,
		PiMedianMs:      o.Pi.MedianMs,
		SkipPerformance: o.SkipPerfGate,
	}
	ctxJSON, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "context.json"), ctxJSON, 0o644); err != nil {
		return "", err
	}
	pigRun, piRun := len(o.Pig.Runs)-1, len(o.Pi.Runs)-1
	if pigRun >= 0 {
		if source := o.Pig.Runs[pigRun].ChangelogSource; source != "" {
			if err := write("pig.changelog-source.md", source); err != nil {
				return "", err
			}
		}
		_ = write("pig.raw.stdout", o.Pig.Runs[pigRun].RawOutput)
		_ = write("pig.stderr", o.Pig.Runs[pigRun].Stderr)
		_ = write("pig.artifact", o.Pig.Runs[pigRun].Artifact)
		_ = write("pig.stdout", o.Pig.Runs[pigRun].Output)
		_ = write("pig.escaped", o.Pig.Runs[pigRun].Escaped)
	}
	if piRun >= 0 {
		if source := o.Pi.Runs[piRun].ChangelogSource; source != "" {
			if err := write("pi.changelog-source.md", source); err != nil {
				return "", err
			}
		}
		_ = write("pi.raw.stdout", o.Pi.Runs[piRun].RawOutput)
		_ = write("pi.stderr", o.Pi.Runs[piRun].Stderr)
		_ = write("pi.artifact", o.Pi.Runs[piRun].Artifact)
		_ = write("pi.stdout", o.Pi.Runs[piRun].Output)
		_ = write("pi.escaped", o.Pi.Runs[piRun].Escaped)
	}
	if pigRun >= 0 && piRun >= 0 {
		_ = write("diff.txt", LineDiff(o.Pig.Runs[pigRun].Output, o.Pi.Runs[piRun].Output))
	}
	rerun := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\ncd %q\nPIG_PARITY_PIG_BIN=%q go test -tags=parity ./test/parity/runner -run 'TestParity/%s$' -count=1 -v -timeout 3m\n",
		mustRepoRootForArtifact(o.Scenario.SourcePath), pig.Path, shellSingleQuoteSafeRegex(o.Scenario.Name))
	if err := os.WriteFile(filepath.Join(dir, "rerun.sh"), []byte(rerun), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func sanitizeArtifactName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "scenario"
	}
	return b.String()
}

func shellSingleQuoteSafeRegex(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func mustRepoRootForArtifact(scenarioPath string) string {
	dir := filepath.Dir(scenarioPath)
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}
