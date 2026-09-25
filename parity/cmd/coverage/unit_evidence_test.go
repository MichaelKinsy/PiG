package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitEvidenceValidatesExecutableTestsAndProvenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		change    func(*unitEvidence)
		wantError bool
	}{
		{"valid", func(*unitEvidence) {}, false},
		{"unknown source", func(e *unitEvidence) { e.Upstream = "missing.ts" }, true},
		{"missing test", func(e *unitEvidence) { e.Tests = []string{"TestMissing"} }, true},
		{"lowercase suffix", func(e *unitEvidence) { e.Tests = []string{"Testhelper"} }, true},
		{"wrong import", func(e *unitEvidence) { e.Tests = []string{"TestOther"} }, true},
		{"helper is not test", func(e *unitEvidence) { e.Tests = []string{"TestHelper"} }, true},
		{"missing mutation", func(e *unitEvidence) { e.Mutation = "" }, true},
		{"missing upstream reference", func(e *unitEvidence) { e.UpstreamReference = "" }, true},
		{"outside package", func(e *unitEvidence) { e.Package = "./../outside" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write := func(path, text string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("thing/behavior_test.go", "package thing\nimport \"testing\"\nfunc TestBehavior(t *testing.T) {}\nfunc TestHelper() {}\nfunc Testhelper(t *testing.T) {}\nfunc TestOther(t *other.T) {}\n")
			e := unitEvidence{Upstream: "thing.ts", Package: "./thing", Tests: []string{"TestBehavior"}, UpstreamReference: "thing.ts observable output", Mutation: "drop output: assertion fails"}
			tc.change(&e)
			body, err := json.Marshal([]unitEvidence{e})
			if err != nil {
				t.Fatal(err)
			}
			write("parity/unit-evidence/thing.json", string(body))
			got, err := loadUnitEvidence(root, []portMapEntry{{UpstreamPath: "thing.ts", Status: "✅"}})
			if (err != nil) != tc.wantError {
				t.Fatalf("load error=%v, want error=%v", err, tc.wantError)
			}
			if !tc.wantError && strings.Join(got["thing.ts"], ",") != "unit:./thing:TestBehavior" {
				t.Fatalf("evidence=%v", got)
			}
		})
	}
}

func TestUnitEvidenceCountsBehaviorWithoutInventingScenarioResults(t *testing.T) {
	t.Parallel()
	coverage, behavioral := map[string][]string{}, map[string][]string{}
	addUnitEvidence(coverage, behavioral, map[string][]string{"thing.ts": {"unit:./thing:TestBehavior"}})
	entries := []portMapEntry{{UpstreamPath: "thing.ts", Status: "✅"}}
	stats := computeStatusStats(entries, coverage, behavioral)
	if stats.Behavioral != 1 || stats.Untested != 0 {
		t.Fatalf("stats=%+v", stats)
	}
	scenarios, units := splitEvidence(matchScenarios("thing.ts", coverage))
	if len(scenarios) != 0 || len(units) != 1 {
		t.Fatalf("scenarios=%v units=%v", scenarios, units)
	}
	file, err := os.CreateTemp(t.TempDir(), "report")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	emitReport(file, entries, coverage, behavioral, nil, nil)
	body, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "(untested)") || !strings.Contains(string(body), "not run | ./thing:TestBehavior") {
		t.Fatalf("report:\n%s", body)
	}
}
