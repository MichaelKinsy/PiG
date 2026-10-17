package ai

import (
	"encoding/json"
	"testing"
)

// TestClassifierAnswerTypeIsTheWireDiscriminant ports the `type` member of the ClassifierAnswer union (types.ts:680,687,693 and :697 ClassifierAnswer =
// choice | score | bool): each member's AnswerType is the literal its own JSON carries as `type`, and the three differ.
func TestClassifierAnswerTypeIsTheWireDiscriminant(t *testing.T) {
	seen := map[string]bool{}
	for want, answer := range map[string]ClassifierAnswer{
		"choice": ClassifierChoiceAnswer{Choice: "a", Probabilities: []ClassifierProbability{{Key: "a", Probability: 1}}, Confidence: 1},
		"score":  ClassifierScoreAnswer{Score: 2, Confidence: 0.5},
		"bool":   ClassifierBoolAnswer{Probability: 0.25},
	} {
		if got := answer.AnswerType(); got != want {
			t.Errorf("%T.AnswerType() = %q, want %q", answer, got, want)
		}
		raw, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil || head.Type != answer.AnswerType() {
			t.Errorf("%T wire type = %q (err %v), AnswerType() = %q", answer, head.Type, err, answer.AnswerType())
		}
		seen[answer.AnswerType()] = true
	}
	if len(seen) != 3 {
		t.Errorf("answer types = %v, want three distinct", seen)
	}
}
