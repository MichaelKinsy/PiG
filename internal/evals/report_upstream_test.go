package evals

// Ports packages/evals/test/report.test.ts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var reportTask = EvalTask{
	DiscoveredEvalCase: DiscoveredEvalCase{
		File:     "evals/example.docs.eval.ts",
		FullName: "Example workflow > handles the case",
		EvalSet:  "Example workflow",
		CaseID:   "handles the case",
	},
	Variant:   "without_docs",
	Model:     "fixture/model",
	RunNumber: 1,
}

const reportSession = "{\"type\":\"session\"}\n"

type reportAssertion struct {
	status ReportCaseStatus
	meta   map[string]any
}

func writeTaskReport(t *testing.T, assertion reportAssertion) (string, string) {
	t.Helper()
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "vitest.json")
	status := assertion.status
	if status == "" {
		status = ReportCaseStatusPassed
	}
	meta := assertion.meta
	if meta == nil {
		meta = map[string]any{}
	}
	passed, pending := 0, 0
	if status == ReportCaseStatusPassed {
		passed = 1
	}
	if status == ReportCaseStatusPending || status == ReportCaseStatusSkipped {
		pending = 1
	}
	report := map[string]any{
		"numFailedTests":  0,
		"numPassedTests":  passed,
		"numPendingTests": pending,
		"numTodoTests":    0,
		"numTotalTests":   1,
		"startTime":       0,
		"success":         true,
		"testResults": []any{map[string]any{
			"message": "",
			"name":    "/repo/packages/evals/evals/example.docs.eval.ts",
			"status":  "passed",
			"assertionResults": []any{map[string]any{
				"ancestorTitles":  []string{reportTask.EvalSet},
				"fullName":        reportTask.EvalSet + " " + reportTask.CaseID,
				"status":          status,
				"title":           reportTask.CaseID,
				"failureMessages": []string{},
				"meta":            meta,
			}},
		}},
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return directory, reportPath
}

type scoredMetaOverrides struct {
	avgScore    any
	hasAvgScore bool
	model       string
	errors      []map[string]string
	artifacts   map[string]string
}

func scoredMeta(overrides scoredMetaOverrides) map[string]any {
	var avgScore any = 0.5
	if overrides.hasAvgScore {
		avgScore = overrides.avgScore
	}
	model := "model"
	if overrides.model != "" {
		model = overrides.model
	}
	errors := overrides.errors
	if errors == nil {
		errors = []map[string]string{}
	}
	artifacts := overrides.artifacts
	if artifacts == nil {
		artifacts = map[string]string{"runId": "run-1", PiSessionSnapshotArtifact: reportSession}
	}
	return map[string]any{
		"eval": map[string]any{
			"avgScore":        avgScore,
			"scores":          []any{map[string]any{"name": "StructuredOutputJudge", "score": 0.5}},
			"thresholdFailed": false,
		},
		"harness": map[string]any{
			"name": "without_docs",
			"run": map[string]any{
				"output":  map[string]any{"ok": true},
				"session": map[string]any{"events": []any{map[string]any{"type": "message", "role": "user", "content": "prompt"}}},
				"usage": map[string]any{
					"provider": "fixture", "model": model, "inputTokens": 10, "outputTokens": 5, "totalTokens": 15, "toolCalls": 1,
					"metadata": map[string]any{"cacheReadTokens": 2, "cacheWriteTokens": 3, "estimatedCostUsd": 0.01},
				},
				"timings":   map[string]any{"totalMs": 1234},
				"artifacts": artifacts,
				"errors":    errors,
			},
		},
	}
}

func readObservation(t *testing.T, assertion reportAssertion) (string, EvalObservation) {
	t.Helper()
	directory, reportPath := writeTaskReport(t, assertion)
	observation, err := ReadTaskObservation(reportTask, reportPath, directory)
	if err != nil {
		t.Fatal(err)
	}
	return directory, observation
}

func fullMetrics() EvalMetrics {
	return EvalMetrics{
		InputTokens: new(10.0), OutputTokens: new(5.0), CacheReadTokens: new(2.0), CacheWriteTokens: new(3.0),
		TotalTokens: new(15.0), ToolCalls: new(1.0), TotalMs: new(1234.0), EstimatedCostUsd: new(0.01),
	}
}

func requireSessionArtifact(t *testing.T, directory string) {
	t.Helper()
	sessions := filepath.Join(directory, string(reportTask.Variant), "sessions")
	hashes, err := os.ReadDir(sessions)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 1 {
		t.Fatalf("session directories = %d, want 1", len(hashes))
	}
	content, err := os.ReadFile(filepath.Join(sessions, hashes[0].Name(), "session.jsonl"))
	if err != nil || string(content) != reportSession {
		t.Fatalf("session artifact = %q, %v", content, err)
	}
}

func requireOutcome(t *testing.T, observation EvalObservation, outcome EvalOutcome) {
	t.Helper()
	if observation.Outcome != outcome {
		t.Fatalf("outcome = %q, want %q (%+v)", observation.Outcome, outcome, observation)
	}
}

// TestReportUpstream ports packages/evals/test/report.test.ts. Each subtest names one upstream case.
func TestReportUpstream(t *testing.T) {
	for _, status := range []ReportCaseStatus{ReportCaseStatusSkipped, ReportCaseStatusTodo, ReportCaseStatusDisabled} {
		t.Run("classifyCaseStatus › maps "+string(status)+" to skipped", func(t *testing.T) {
			// upstream: packages/evals/test/report.test.ts:106
			if got := ClassifyCaseStatus(status); got != EvalOutcomeSkipped {
				t.Fatalf("ClassifyCaseStatus(%q) = %q", status, got)
			}
		})
	}

	t.Run("classifyCaseStatus › maps failed infrastructure to errored", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:110
		if got := ClassifyCaseStatus(ReportCaseStatusFailed); got != EvalOutcomeErrored {
			t.Fatalf("ClassifyCaseStatus(failed) = %q", got)
		}
	})

	t.Run("readTaskObservation › preserves a skipped outcome when no harness run exists", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:116
		_, observation := readObservation(t, reportAssertion{status: ReportCaseStatusSkipped})
		requireOutcome(t, observation, EvalOutcomeSkipped)
	})

	t.Run("readTaskObservation › preserves a pending outcome when no harness run exists", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:121
		_, observation := readObservation(t, reportAssertion{status: ReportCaseStatusPending})
		requireOutcome(t, observation, EvalOutcomePending)
	})

	t.Run("readTaskObservation › preserves metrics from a failed eval with a partial harness run", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:126
		directory, observation := readObservation(t, reportAssertion{
			status: ReportCaseStatusFailed,
			meta:   scoredMeta(scoredMetaOverrides{errors: []map[string]string{{"message": "Prompt verification failed"}}}),
		})
		want := EvalObservation{EvalRunIdentity: taskIdentity(reportTask), EvalMetrics: fullMetrics(), Outcome: EvalOutcomeErrored}
		if !reflect.DeepEqual(observation, want) {
			t.Fatalf("observation = %+v, want %+v", observation, want)
		}
		requireSessionArtifact(t, directory)
	})

	t.Run("readTaskObservation › records an errored outcome when a passed eval has no harness run", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:153
		_, observation := readObservation(t, reportAssertion{status: ReportCaseStatusPassed})
		requireOutcome(t, observation, EvalOutcomeErrored)
	})

	t.Run("readTaskObservation › reads a scored harness run and persists the session artifact", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:158
		directory, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{})})
		want := EvalObservation{EvalRunIdentity: taskIdentity(reportTask), EvalMetrics: fullMetrics(), Outcome: EvalOutcomeScored, Score: new(0.5)}
		if !reflect.DeepEqual(observation, want) {
			t.Fatalf("observation = %+v, want %+v", observation, want)
		}
		requireSessionArtifact(t, directory)
	})

	t.Run("readTaskObservation › treats a zero score as scored data", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:184
		_, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{avgScore: 0, hasAvgScore: true})})
		requireOutcome(t, observation, EvalOutcomeScored)
		if !reflect.DeepEqual(observation.Score, new(0.0)) {
			t.Fatalf("score = %v, want 0", observation.Score)
		}
	})

	t.Run("readTaskObservation › records an unscored outcome when a completed eval has no score", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:189
		_, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{avgScore: nil, hasAvgScore: true})})
		requireOutcome(t, observation, EvalOutcomeUnscored)
	})

	t.Run("readTaskObservation › records an errored outcome when the reported model does not match the task", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:194
		_, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{model: "other"})})
		requireOutcome(t, observation, EvalOutcomeErrored)
	})

	t.Run("readTaskObservation › records an errored outcome when a completed harness run contains errors", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:199
		_, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{errors: []map[string]string{{"message": "boom"}}})})
		requireOutcome(t, observation, EvalOutcomeErrored)
	})

	t.Run("readTaskObservation › still scores a completed eval when the session artifact is missing", func(t *testing.T) {
		// upstream: packages/evals/test/report.test.ts:204
		_, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{artifacts: map[string]string{"runId": "run-1"}})})
		requireOutcome(t, observation, EvalOutcomeScored)
		if !reflect.DeepEqual(observation.Score, new(0.5)) {
			t.Fatalf("score = %v, want 0.5", observation.Score)
		}
	})
}
