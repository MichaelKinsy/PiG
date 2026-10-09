package ai

import (
	"reflect"
	"testing"
)

// .upstream/current/packages/ai/src/api/llama-cpp-classify.ts:195-218 (answerFromProbabilities). Pi's tests drive it through
// classify with distinct probabilities; these cases pin the branches they leave open.

// A choice answer takes the first label with the highest probability: the scan updates only on a strictly greater value
// (line 209-211), so a tie keeps the earlier key.
func TestLlamaCppAnswerFromProbabilitiesChoiceTieKeepsTheFirstLabel(t *testing.T) {
	got := LlamaCppAnswerFromProbabilities(ClassifierChoiceQuestion{}, []string{"a", "b", "c"}, []float64{0.25, 0.375, 0.375}).(ClassifierChoiceAnswer)
	if got.Choice != "b" {
		t.Fatalf("choice = %q, want b (first of the tied labels)", got.Choice)
	}
}

// A choice answer's probabilities are Object.fromEntries(keys.map(...)): a record, so JavaScript lists integer-like keys first in
// ascending order and the remaining keys in insertion order.
func TestLlamaCppAnswerFromProbabilitiesChoiceProbabilitiesFollowRecordKeyOrder(t *testing.T) {
	got := LlamaCppAnswerFromProbabilities(ClassifierChoiceQuestion{}, []string{"b", "10", "a", "2"}, []float64{0.1, 0.2, 0.3, 0.4}).(ClassifierChoiceAnswer)
	want := []ClassifierProbability{{Key: "2", Probability: 0.4}, {Key: "10", Probability: 0.2}, {Key: "b", Probability: 0.1}, {Key: "a", Probability: 0.3}}
	if !reflect.DeepEqual(got.Probabilities, want) {
		t.Fatalf("probabilities = %v, want %v", got.Probabilities, want)
	}
	if got.Choice != "2" {
		t.Fatalf("choice = %q, want 2 (highest probability 0.4)", got.Choice)
	}
}

// A bool answer reads the probability of the "true" label wherever it sits in keys (line 201: probabilities[keys.indexOf("true")]).
func TestLlamaCppAnswerFromProbabilitiesBoolReadsTheTrueLabel(t *testing.T) {
	got := LlamaCppAnswerFromProbabilities(ClassifierBoolQuestion{}, []string{"false", "true"}, []float64{0.75, 0.25}).(ClassifierBoolAnswer)
	if got.Probability != 0.25 {
		t.Fatalf("probability = %v, want 0.25", got.Probability)
	}
}

// A score answer is the probability-weighted level sum with the peak confidence (lines 203-206).
func TestLlamaCppAnswerFromProbabilitiesScoreIsTheExpectedLevel(t *testing.T) {
	probabilities := []float64{0.25, 0.5, 0.25}
	got := LlamaCppAnswerFromProbabilities(ClassifierScoreQuestion{}, []string{"0", "1", "2"}, probabilities).(ClassifierScoreAnswer)
	if got.Score != 1 || got.Confidence != LlamaCppPeakConfidence(probabilities) {
		t.Fatalf("answer = %+v, want score 1 with confidence %v", got, LlamaCppPeakConfidence(probabilities))
	}
}
