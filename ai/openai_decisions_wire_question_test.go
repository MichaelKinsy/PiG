package ai

import (
	"encoding/json"
	"testing"
)

// Pi api/openai-decisions.ts:43-49 (predicateInstructions) and :51-72 (wireQuestion): a choice question's choices come from Object.entries(criteria), so JavaScript's
// key order applies (array-index keys first, ascending) and an empty description leaves the choice with only its value; a score question's levels are its criteria in
// order; a bool question becomes a predicate whose instructions list only the meanings it has ("True means: ..." then "False means: ...", blank-line separated).
// mutation-checked: wiring the choices in declaration order, and skipping the no-meanings shortcut (a trailing blank line), each fail it.
func TestOpenAIDecisionWireQuestionShapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		question ClassifierQuestion
		want     string
	}{
		{"choice in JavaScript key order, empty description omitted", ClassifierChoiceQuestion{Instructions: "Pick", Criteria: clsChoices("b", "bee", "10", "ten", "2", "", "a", "ay")},
			`{"type":"choice","name":"q","instructions":"Pick","choices":[{"value":"2"},{"value":"10","description":"ten"},{"value":"b","description":"bee"},{"value":"a","description":"ay"}]}`},
		{"score levels keep their order", ClassifierScoreQuestion{Instructions: "Rate", Criteria: []string{"low", "mid", "high"}},
			`{"type":"score","name":"q","instructions":"Rate","levels":[{"label":"low"},{"label":"mid"},{"label":"high"}]}`},
		{"predicate with both meanings", ClassifierBoolQuestion{Instructions: "Ok?", Criteria: ClassifierBoolCriteria{True: "fine", False: "broken"}},
			`{"type":"predicate","name":"q","instructions":"Ok?\n\nTrue means: fine\nFalse means: broken"}`},
		{"predicate with only the false meaning", ClassifierBoolQuestion{Instructions: "Ok?", Criteria: ClassifierBoolCriteria{False: "broken"}},
			`{"type":"predicate","name":"q","instructions":"Ok?\n\nFalse means: broken"}`},
		{"predicate with no meanings is the bare instructions", ClassifierBoolQuestion{Instructions: "Ok?"},
			`{"type":"predicate","name":"q","instructions":"Ok?"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(openAIDecisionWireQuestion("q", tc.question))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("wire question\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
