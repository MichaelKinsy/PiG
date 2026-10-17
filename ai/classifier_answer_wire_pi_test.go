package ai

import (
	"encoding/json"
	"math"
	"testing"
)

// types.ts:679-697 fixes the three ClassifierAnswer members and their member order: choice { type, choice, probabilities, confidence },
// score { type, score, confidence } and bool { type, probability }. A ClassifierResult carries them in `answers`, keyed by question id in
// the order given. JSON.stringify writes numbers as Number.prototype.toString renders them (1e21 as 1e+21, 1e-7 as 1e-7, -0 as 0), and the
// wire form is what a classifier caller or a persisted result reads, so each form is asserted byte for byte.
func TestClassifierAnswerMembersWriteTheirPiWireShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer ClassifierAnswer
		typ    string
		want   string
	}{
		{
			name: "choice keeps probability order and renders JS numbers", typ: "choice",
			answer: ClassifierChoiceAnswer{Choice: "b", Probabilities: []ClassifierProbability{{Key: "b", Probability: 0.7}, {Key: "a", Probability: 1e21}}, Confidence: 1e-7},
			want:   `{"type":"choice","choice":"b","probabilities":{"b":0.7,"a":1e+21},"confidence":1e-7}`,
		},
		{
			name: "choice with no probabilities writes an empty object", typ: "choice",
			answer: ClassifierChoiceAnswer{Choice: "", Confidence: 1},
			want:   `{"type":"choice","choice":"","probabilities":{},"confidence":1}`,
		},
		{
			name: "score", typ: "score",
			answer: ClassifierScoreAnswer{Score: math.Copysign(0, -1), Confidence: 0.25},
			want:   `{"type":"score","score":0,"confidence":0.25}`,
		},
		{
			name: "bool has no confidence", typ: "bool",
			answer: ClassifierBoolAnswer{Probability: 0.5},
			want:   `{"type":"bool","probability":0.5}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.answer.AnswerType(); got != tc.typ {
				t.Errorf("AnswerType() = %q, want %q", got, tc.typ)
			}
			wire, err := json.Marshal(tc.answer)
			if err != nil || string(wire) != tc.want {
				t.Errorf("json.Marshal = %s (%v), want %s", wire, err, tc.want)
			}
		})
	}

	answers := ClassifierAnswers{
		{ID: "z", Answer: ClassifierBoolAnswer{Probability: 0.5}},
		{ID: "a", Answer: ClassifierScoreAnswer{Score: 2, Confidence: 1}},
	}
	wire, err := json.Marshal(answers)
	if want := `{"z":{"type":"bool","probability":0.5},"a":{"type":"score","score":2,"confidence":1}}`; err != nil || string(wire) != want {
		t.Errorf("answers = %s (%v), want %s", wire, err, want)
	}
}
