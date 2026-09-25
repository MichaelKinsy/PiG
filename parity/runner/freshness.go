//go:build parity

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FreshnessReport describes whether the pig binary under test was built after
// the source files that feed cmd/pig. It exists to prevent the most expensive
// parity failure mode: comparing upstream pi against a stale installed pig while
// editing source in this checkout.
type FreshnessReport struct {
	BinaryPath     string
	BinaryModTime  time.Time
	NewestPath     string
	NewestModTime  time.Time
	Fresh          bool
	CheckedSources []string
}

// CheckPigFreshness compares pigBin's mtime with the newest Go source file in
// the production packages compiled into the pig binary. Non-source docs,
// parity scenarios, examples, and testdata are intentionally ignored.
func CheckPigFreshness(repoRoot, pigBin string) (FreshnessReport, error) {
	report := FreshnessReport{BinaryPath: pigBin}
	info, err := os.Stat(pigBin)
	if err != nil {
		return report, fmt.Errorf("stat pig binary %s: %w", pigBin, err)
	}
	report.BinaryModTime = info.ModTime()

	for _, rel := range pigFreshnessSourceDirs {
		root := filepath.Join(repoRoot, rel)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		report.CheckedSources = append(report.CheckedSources, rel)
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := d.Name()
				if base == "testdata" || base == "target" || base == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if fi.ModTime().After(report.NewestModTime) {
				report.NewestModTime = fi.ModTime()
				report.NewestPath = path
			}
			return nil
		}); err != nil {
			return report, err
		}
	}

	// Allow a one-second filesystem timestamp precision margin.
	report.Fresh = report.NewestPath == "" || !report.NewestModTime.After(report.BinaryModTime.Add(time.Second))
	return report, nil
}

var pigFreshnessSourceDirs = []string{
	"cmd/pig",
	"agent",
	"ai",
	"coding",
	"internal",
	"piglets/standard",
}

func requireFreshPig(t *testing.T, repoRoot string, pig BinaryRef, allowStale bool) FreshnessReport {
	t.Helper()
	report, err := CheckPigFreshness(repoRoot, pig.Path)
	if err != nil {
		t.Fatalf("pig freshness check failed: %v", err)
	}
	if !report.Fresh && !allowStale {
		t.Fatalf("stale pig binary under test: %s\n  binary mtime: %s\n  newest source: %s\n  source mtime: %s\n\nBuild a fresh parity binary and rerun:\n  go build -o /tmp/pig-under-test ./cmd/pig\n  PIG_PARITY_PIG_BIN=/tmp/pig-under-test go test -tags=parity ./parity/runner ...\n\nOr use make qc / make parity, which build a fresh bin/pig-parity by default. To inspect an intentionally stale installed binary, pass -pig-parity.allow-stale=true.",
			report.BinaryPath,
			report.BinaryModTime.Format(time.RFC3339Nano),
			report.NewestPath,
			report.NewestModTime.Format(time.RFC3339Nano))
	}
	return report
}
