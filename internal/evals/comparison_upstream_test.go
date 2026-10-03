package evals

// Ports packages/evals/test/comparison.test.ts.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type scoredMetrics struct {
	totalTokens, toolCalls, totalMs, estimatedCostUsd *float64
}

func observationIdentity(variant DocumentationVariant, runNumber int) EvalRunIdentity {
	return EvalRunIdentity{EvalSet: "tool access", CaseID: "create", Variant: variant, Model: "fixture/model", RunNumber: runNumber}
}

func scored(variant DocumentationVariant, runNumber int, score float64, metrics scoredMetrics) EvalObservation {
	observation := EvalObservation{
		EvalRunIdentity: observationIdentity(variant, runNumber),
		Outcome:         EvalOutcomeScored,
		Score:           new(score),
		EvalMetrics: EvalMetrics{
			TotalTokens: new(100.0), ToolCalls: new(2.0), TotalMs: new(1000.0), EstimatedCostUsd: new(0.01),
		},
	}
	if metrics.totalTokens != nil {
		observation.TotalTokens = metrics.totalTokens
	}
	if metrics.toolCalls != nil {
		observation.ToolCalls = metrics.toolCalls
	}
	if metrics.totalMs != nil {
		observation.TotalMs = metrics.totalMs
	}
	if metrics.estimatedCostUsd != nil {
		observation.EstimatedCostUsd = metrics.estimatedCostUsd
	}
	return observation
}

func errored(variant DocumentationVariant, runNumber int, totalTokens *float64) EvalObservation {
	return EvalObservation{EvalRunIdentity: observationIdentity(variant, runNumber), Outcome: EvalOutcomeErrored, EvalMetrics: EvalMetrics{TotalTokens: totalTokens}}
}

func expectedFor(runNumbers ...int) []ExpectedEvalRun {
	var expected []ExpectedEvalRun
	for _, runNumber := range runNumbers {
		for _, variant := range DocumentationVariants {
			expected = append(expected, observationIdentity(variant, runNumber))
		}
	}
	return expected
}

var vtControlCharacters = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestComparisonUpstream ports packages/evals/test/comparison.test.ts. Each subtest names one upstream case.
func TestComparisonUpstream(t *testing.T) {
	t.Run("summarizeEvalObservations › computes paired lift and efficiency deltas", func(t *testing.T) {
		// upstream: packages/evals/test/comparison.test.ts:62
		observations := []EvalObservation{
			scored("without_docs", 1, 0, scoredMetrics{totalTokens: new(100.0), toolCalls: new(3.0), totalMs: new(1000.0)}),
			scored("with_docs", 1, 1, scoredMetrics{totalTokens: new(120.0), toolCalls: new(2.0), totalMs: new(800.0)}),
			scored("without_docs", 2, 1, scoredMetrics{totalTokens: new(200.0)}),
			scored("with_docs", 2, 1, scoredMetrics{totalTokens: new(180.0)}),
		}
		report := SummarizeEvalObservations("digest", expectedFor(1, 2), observations)
		if len(report.Comparisons) != 1 {
			t.Fatalf("comparisons = %+v", report.Comparisons)
		}
		comparison := report.Comparisons[0]
		if comparison.EvalSet != "tool access" || comparison.TotalPairs != 2 || comparison.EligiblePairs != 2 || comparison.BlockedPairs != 0 ||
			!reflect.DeepEqual(comparison.ControlPassRate, new(0.5)) || !reflect.DeepEqual(comparison.TreatmentPassRate, new(1.0)) ||
			!reflect.DeepEqual(comparison.Lift, new(0.5)) {
			t.Fatalf("comparison = %+v", comparison)
		}
		if want := (PairedMetricSummary{EligiblePairs: 2, ControlMean: new(150.0), TreatmentMean: new(150.0), MeanDelta: new(0.0)}); !reflect.DeepEqual(comparison.TotalTokens, want) {
			t.Fatalf("totalTokens = %+v", comparison.TotalTokens)
		}
		if want := (PairedMetricSummary{EligiblePairs: 2, ControlMean: new(2.5), TreatmentMean: new(2.0), MeanDelta: new(-0.5)}); !reflect.DeepEqual(comparison.ToolCalls, want) {
			t.Fatalf("toolCalls = %+v", comparison.ToolCalls)
		}
	})

	t.Run("summarizeEvalObservations › fails closed for incomplete and errored pairs while retaining totals", func(t *testing.T) {
		// upstream: packages/evals/test/comparison.test.ts:85
		observations := []EvalObservation{
			scored("without_docs", 1, 0, scoredMetrics{}),
			errored("with_docs", 1, new(120.0)),
			scored("without_docs", 2, 1, scoredMetrics{totalTokens: new(200.0)}),
		}
		report := SummarizeEvalObservations("digest", expectedFor(1, 2), observations)
		comparison := report.Comparisons[0]
		if comparison.TotalPairs != 2 || comparison.EligiblePairs != 0 || comparison.BlockedPairs != 2 || comparison.Lift != nil {
			t.Fatalf("comparison = %+v", comparison)
		}
		if len(report.BlockedPairs) != 2 ||
			report.BlockedPairs[0].RunNumber != 1 || !reflect.DeepEqual(report.BlockedPairs[0].Reasons, []string{"with_docs: errored"}) ||
			report.BlockedPairs[1].RunNumber != 2 || !reflect.DeepEqual(report.BlockedPairs[1].Reasons, []string{"with_docs: expected 1 observation, found 0"}) {
			t.Fatalf("blocked pairs = %+v", report.BlockedPairs)
		}
		if want := (OperationalMetricTotal{AvailableRuns: 2, Total: new(300.0)}); !reflect.DeepEqual(report.OperationalTotals[0].TotalTokens, want) {
			t.Fatalf("control totalTokens = %+v", report.OperationalTotals[0].TotalTokens)
		}
	})

	t.Run("summarizeEvalObservations › blocks duplicate observations and keeps missing metrics distinct from zero", func(t *testing.T) {
		// upstream: packages/evals/test/comparison.test.ts:102
		withoutTokens := scored("without_docs", 1, 1, scoredMetrics{})
		withoutTokens.TotalTokens = nil
		report := SummarizeEvalObservations("digest", expectedFor(1), []EvalObservation{
			withoutTokens,
			withoutTokens,
			scored("with_docs", 1, 1, scoredMetrics{totalTokens: new(0.0)}),
		})
		if !reflect.DeepEqual(report.BlockedPairs[0].Reasons, []string{"without_docs: expected 1 observation, found 2"}) {
			t.Fatalf("reasons = %v", report.BlockedPairs[0].Reasons)
		}
		if want := (OperationalMetricTotal{AvailableRuns: 0}); !reflect.DeepEqual(report.OperationalTotals[0].TotalTokens, want) {
			t.Fatalf("control totalTokens = %+v", report.OperationalTotals[0].TotalTokens)
		}
		if want := (OperationalMetricTotal{AvailableRuns: 1, Total: new(0.0)}); !reflect.DeepEqual(report.OperationalTotals[1].TotalTokens, want) {
			t.Fatalf("treatment totalTokens = %+v", report.OperationalTotals[1].TotalTokens)
		}
	})

	t.Run("summarizeEvalObservations › formats blocked comparisons and operational totals", func(t *testing.T) {
		// upstream: packages/evals/test/comparison.test.ts:115
		report := SummarizeEvalObservations("digest", expectedFor(1, 2), []EvalObservation{
			scored("without_docs", 1, 1, scoredMetrics{}),
			scored("with_docs", 1, 1, scoredMetrics{}),
		})
		formatted := vtControlCharacters.ReplaceAllString(FormatEvalComparisonReport(report), "")
		for _, want := range []string{"Documentation Eval Comparisons", "Pass rate  withheld because pairs are blocked", "without_docs: 1 runs"} {
			if !strings.Contains(formatted, want) {
				t.Fatalf("formatted report lacks %q:\n%s", want, formatted)
			}
		}
	})
}

// TestSummarizeEvalObservationsScoredWithoutScore checks that a scored observation without a score has not passed, as
// undefined >= 1 is false in report.ts, instead of panicking.
func TestSummarizeEvalObservationsScoredWithoutScore(t *testing.T) {
	control := scored("without_docs", 1, 1, scoredMetrics{})
	control.Score = nil
	report := SummarizeEvalObservations("digest", expectedFor(1), []EvalObservation{control, scored("with_docs", 1, 1, scoredMetrics{})})
	comparison := report.Comparisons[0]
	if !reflect.DeepEqual(comparison.ControlPassRate, new(0.0)) || !reflect.DeepEqual(comparison.TreatmentPassRate, new(1.0)) ||
		!reflect.DeepEqual(comparison.Flags, []EvalComparisonFlag{EvalComparisonFlagTreatmentSaturated}) {
		t.Fatalf("comparison = %+v", comparison)
	}
}
