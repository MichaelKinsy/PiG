package evals

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// packages/evals/src/report.ts:136-160: readTaskObservation awaits persistSession (:122-134) without a catch once the report has a harness run, and before it compares the reported model (:161-162). A failed mkdir or writeFile therefore rejects readTaskObservation, which the CLI awaits (cli.ts:177), while a report that ends before the run check never writes the session.
// mutation-checked: dropping persistSession's error, or persisting after the model check, fails it.
func TestReadTaskObservationAwaitsTheSessionWriteBeforeTheModelCheck(t *testing.T) {
	t.Run("a failed session write rejects the read", func(t *testing.T) {
		directory, reportPath := writeTaskReport(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{})})
		if err := os.WriteFile(filepath.Join(directory, string(reportTask.Variant)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		observation, err := ReadTaskObservation(reportTask, reportPath, directory)
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Fatalf("ReadTaskObservation = %+v, %v; want the mkdir ENOTDIR error", observation, err)
		}
	})
	t.Run("a report without a run never writes the session", func(t *testing.T) {
		directory, reportPath := writeTaskReport(t, reportAssertion{status: ReportCaseStatusPassed})
		if err := os.WriteFile(filepath.Join(directory, string(reportTask.Variant)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		observation, err := ReadTaskObservation(reportTask, reportPath, directory)
		if err != nil {
			t.Fatal(err)
		}
		requireOutcome(t, observation, EvalOutcomeErrored)
	})
	t.Run("the session is written before a model mismatch errors the observation", func(t *testing.T) {
		directory, observation := readObservation(t, reportAssertion{meta: scoredMeta(scoredMetaOverrides{model: "other"})})
		requireOutcome(t, observation, EvalOutcomeErrored)
		requireSessionArtifact(t, directory)
	})
}
