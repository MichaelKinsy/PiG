package evals

// Differential evidence for report.ts: testdata/oracle.json records upstream's summarizeEvalObservations,
// formatEvalComparisonReport and readTaskObservation (with @vitest-evals/core 0.15.0) on Node v24 over generated
// observation sets and hand-written report variants. testdata/oracle.ts regenerates it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type oracleFixture struct {
	Summaries []struct {
		Expected     []ExpectedEvalRun `json:"expected"`
		Observations []EvalObservation `json:"observations"`
		Report       json.RawMessage   `json:"report"`
		Formatted    string            `json:"formatted"`
	} `json:"summaries"`
	Observations []struct {
		Name        string            `json:"name"`
		Task        EvalTask          `json:"task"`
		Report      string            `json:"report"`
		Observation json.RawMessage   `json:"observation"`
		Files       map[string]string `json:"files"`
	} `json:"observations"`
}

func loadOracle(t *testing.T) oracleFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture oracleFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// sameJSON compares JSON values structurally, so number spelling and key order do not matter.
func sameJSON(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(encoded, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func TestSummarizeEvalObservationsMatchesUpstream(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	fixture := loadOracle(t)
	if len(fixture.Summaries) == 0 {
		t.Fatal("oracle has no summaries")
	}
	for index, summary := range fixture.Summaries {
		var recorded struct {
			ProtocolDigest string `json:"protocolDigest"`
		}
		if err := json.Unmarshal(summary.Report, &recorded); err != nil {
			t.Fatal(err)
		}
		report := SummarizeEvalObservations(recorded.ProtocolDigest, summary.Expected, summary.Observations)
		if !sameJSON(t, report, summary.Report) {
			encoded, _ := json.Marshal(report)
			t.Fatalf("summary %d:\n got %s\nwant %s", index, encoded, summary.Report)
		}
		if formatted := FormatEvalComparisonReport(report); formatted != summary.Formatted {
			t.Fatalf("formatted summary %d:\n got %q\nwant %q", index, formatted, summary.Formatted)
		}
	}
}

func TestReadTaskObservationMatchesUpstream(t *testing.T) {
	fixture := loadOracle(t)
	if len(fixture.Observations) == 0 {
		t.Fatal("oracle has no observations")
	}
	for _, testCase := range fixture.Observations {
		t.Run(testCase.Name, func(t *testing.T) {
			directory := t.TempDir()
			reportPath := filepath.Join(directory, "report.json")
			if err := os.WriteFile(reportPath, []byte(testCase.Report), 0o600); err != nil {
				t.Fatal(err)
			}
			artifacts := filepath.Join(directory, "artifacts")
			observation, err := ReadTaskObservation(testCase.Task, reportPath, artifacts)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, observation, testCase.Observation) {
				encoded, _ := json.Marshal(observation)
				t.Fatalf("observation:\n got %s\nwant %s", encoded, testCase.Observation)
			}
			files := map[string]string{}
			_ = filepath.WalkDir(artifacts, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return nil
				}
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				relativePath, _ := filepath.Rel(artifacts, path)
				files[filepath.ToSlash(relativePath)] = string(content)
				return nil
			})
			if !reflect.DeepEqual(files, testCase.Files) {
				t.Fatalf("persisted files:\n got %q\nwant %q", files, testCase.Files)
			}
		})
	}
}
