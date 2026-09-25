package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResultsRejectsStaleUpstreamVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	data := []byte(`{"upstream_version":"0.83.0","outcomes":[]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResults(path, "0.84.0"); err == nil || !strings.Contains(err.Error(), "want \"0.84.0\"") {
		t.Fatalf("stale results error = %v", err)
	}
}

func TestComputeStatusStats_AllStatusKinds(t *testing.T) {
	entries := []portMapEntry{
		{UpstreamPath: "a.ts", Status: "✅"}, // covered behaviorally
		{UpstreamPath: "b.ts", Status: "✅"}, // untested
		{UpstreamPath: "c.ts", Status: "🟡"},
		{UpstreamPath: "d.ts", Status: "⏸"},
		{UpstreamPath: "e.ts", Status: "n/a"},
		{UpstreamPath: "f.ts", Status: "🔴"},
		{UpstreamPath: "g.ts", Status: "⬜"},
	}
	coverage := map[string][]string{
		"a.ts": {"scenario-1"},
	}
	behavioral := map[string][]string{
		"a.ts": {"scenario-1"},
	}
	got := computeStatusStats(entries, coverage, behavioral)
	want := statusStats{
		Total: 7, Ported: 2, Partial: 1, Deferred: 1,
		Broken: 1, NotStarted: 1, NA: 1,
		Covered: 1, Behavioral: 1, NonBehavioralOnly: 0, Untested: 1,
	}
	if got != want {
		t.Fatalf("stats mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestComputeStatusStats_NonBehavioralOnly_NotCountedAsBehavioral(t *testing.T) {
	// An entry covered ONLY by a weak/non-behavioral scenario must show up as
	// NonBehavioralOnly, not Behavioral. This prevents metric inflation.
	entries := []portMapEntry{
		{UpstreamPath: "behav.ts", Status: "✅"},
		{UpstreamPath: "def-only.ts", Status: "✅"},
		{UpstreamPath: "untested.ts", Status: "✅"},
	}
	coverage := map[string][]string{
		"behav.ts":    {"real-scenario"},
		"def-only.ts": {"deferred-scenario"},
	}
	behavioral := map[string][]string{
		"behav.ts": {"real-scenario"},
	}
	got := computeStatusStats(entries, coverage, behavioral)
	if got.Behavioral != 1 {
		t.Errorf("Behavioral = %d, want 1 (only behav.ts counts)", got.Behavioral)
	}
	if got.NonBehavioralOnly != 1 {
		t.Errorf("NonBehavioralOnly = %d, want 1 (def-only.ts)", got.NonBehavioralOnly)
	}
	if got.Covered != 2 {
		t.Errorf("Covered = %d, want 2 (both have coverage entries)", got.Covered)
	}
	if got.Untested != 1 {
		t.Errorf("Untested = %d, want 1", got.Untested)
	}
}

func TestStatusStatsCoverageBadgeReportsBehavioralPercentage(t *testing.T) {
	badge := (statusStats{Ported: 353, Behavioral: 300}).coverageBadge()
	for _, want := range []string{"behavior tests: 85.0%", ">85.0%</text>", `fill="#97ca00"`} {
		if !strings.Contains(badge, want) {
			t.Errorf("badge does not contain %q:\n%s", want, badge)
		}
	}
}

func TestStatusStats_SummaryLine_FullyPorted(t *testing.T) {
	s := statusStats{Total: 100, Ported: 80, NA: 20, Covered: 40, Behavioral: 40, Untested: 40}
	line := s.summaryLine()
	if !strings.Contains(line, "80 / 80 intended-portable") {
		t.Errorf("expected denominator to be intended-portable surface (80), got: %s", line)
	}
	if !strings.Contains(line, "100.0%") {
		t.Errorf("expected 100%% porting, got: %s", line)
	}
	if !strings.Contains(line, "50.0%") {
		t.Errorf("expected 50%% verification coverage, got: %s", line)
	}
	if !strings.Contains(line, "behavioral") {
		t.Errorf("expected 'behavioral' in verification headline, got: %s", line)
	}
}

func TestStatusStats_SummaryLine_NonBehavioralOnlySurfaced(t *testing.T) {
	// When NonBehavioralOnly > 0, the headline must surface it so the reader
	// sees the gap between referenced-as-listed and behaviorally verified.
	s := statusStats{Total: 100, Ported: 80, NA: 20, Covered: 50, Behavioral: 40, NonBehavioralOnly: 10, Untested: 30}
	line := s.summaryLine()
	if !strings.Contains(line, "10 weak-only") {
		t.Errorf("expected '10 weak-only' in headline, got: %s", line)
	}
	if !strings.Contains(line, "40 behavioral") {
		t.Errorf("expected behavioral count (not covered count) as headline number, got: %s", line)
	}
}

func TestStatusStats_SummaryLine_PartialPort(t *testing.T) {
	// 80 ✅ + 10 🟡 + 5 ⬜ + 5 n/a = 100 total. Intended portable = 95.
	s := statusStats{Total: 100, Ported: 80, Partial: 10, NotStarted: 5, NA: 5, Covered: 40, Behavioral: 40, Untested: 40}
	line := s.summaryLine()
	if !strings.Contains(line, "80 / 95") {
		t.Errorf("expected denominator 95 (intended portable excludes n/a), got: %s", line)
	}
	// 80/95 = 84.2%
	if !strings.Contains(line, "84.2%") {
		t.Errorf("expected 84.2%% porting, got: %s", line)
	}
}

func TestStatusStats_BreakdownLine_ItemizesNonPorted(t *testing.T) {
	s := statusStats{Total: 100, Ported: 70, NA: 15, Deferred: 10, Partial: 5}
	line := s.breakdownLine()
	if !strings.Contains(line, "Raw PORT_MAP rows: 100") {
		t.Errorf("missing raw row count, got: %s", line)
	}
	for _, want := range []string{"15 n/a", "10 ⏸ deferred", "5 🟡 partial", "DIVERGENCES.md"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in breakdown, got: %s", want, line)
		}
	}
}

func TestStatusStats_BreakdownLine_AllPorted(t *testing.T) {
	s := statusStats{Total: 50, Ported: 50}
	line := s.breakdownLine()
	if !strings.Contains(line, "all ✅") {
		t.Errorf("expected 'all ✅' for clean port, got: %s", line)
	}
}

func TestStatusStats_StrictError(t *testing.T) {
	tests := []struct {
		name    string
		stats   statusStats
		wantErr bool
	}{
		{name: "complete", stats: statusStats{Ported: 10, Behavioral: 10}},
		{name: "designed out and deferred are explicit", stats: statusStats{Ported: 10, Behavioral: 10, NA: 2, Deferred: 1}},
		{name: "partial", stats: statusStats{Ported: 9, Behavioral: 9, Partial: 1}, wantErr: true},
		{name: "broken", stats: statusStats{Ported: 9, Behavioral: 9, Broken: 1}, wantErr: true},
		{name: "not started", stats: statusStats{Ported: 9, Behavioral: 9, NotStarted: 1}, wantErr: true},
		{name: "weak only", stats: statusStats{Ported: 10, Behavioral: 9, NonBehavioralOnly: 1}, wantErr: true},
		{name: "untested", stats: statusStats{Ported: 10, Behavioral: 9, Untested: 1}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if gotErr := test.stats.strictError() != nil; gotErr != test.wantErr {
				t.Fatalf("strictError() error = %v, want error %v", gotErr, test.wantErr)
			}
		})
	}
}

func TestScenarioQualityWeakClasses(t *testing.T) {
	cases := []struct {
		name string
		sc   scenarioFile
		want scenarioQualityKind
	}{
		{"behavioral", scenarioFile{Name: "real"}, qualityBehavioral},
		{"deferred", scenarioFile{Name: "deferred", Tags: []string{"deferred"}}, qualityDeferred},
		{"boot description", scenarioFile{Name: "boot", Description: "boot-only: startup"}, qualityBootOnly},
		{"registration tag", scenarioFile{Name: "reg", Tags: []string{"registration-only"}}, qualityRegistrationOnly},
		{"smoke tag", scenarioFile{Name: "smoke", Tags: []string{"smoke-only"}}, qualitySmokeOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scenarioQuality(tc.sc); got != tc.want {
				t.Fatalf("scenarioQuality = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRegistrationOnlyCoverageCountsOnlyRegistryPaths(t *testing.T) {
	sc := scenarioFile{Name: "list-models", Tags: []string{"registration-only"}}
	if !isBehavioralCoverage(sc, "packages/ai/src/providers/register-builtins.ts") {
		t.Fatal("registration-only scenario should behaviorally cover register-builtins catalog wiring")
	}
	if isBehavioralCoverage(sc, "packages/ai/src/providers/openai-completions.ts") {
		t.Fatal("registration-only scenario must not behaviorally cover provider payload/stream conversion")
	}
}
