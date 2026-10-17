package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi types.ts:640-667: ClassifierQuestion is the union of ClassifierChoiceQuestion {type "choice", criteria Record<string, string>},
// ClassifierScoreQuestion {type "score", criteria string[]} and ClassifierBoolQuestion {type "bool", criteria {true, false}}. Each member
// carries its discriminator on the wire, choice criteria keep their declaration order, and a question with any other (or no) type is not a
// ClassifierQuestion.
func TestClassifierQuestionUnionWireShapeAndDiscriminator(t *testing.T) {
	var choice ClassifierQuestion = ClassifierChoiceQuestion{Instructions: "Which?", Criteria: clsChoices("b", "B", "a", "A")}
	var score ClassifierQuestion = ClassifierScoreQuestion{Instructions: "How?", Criteria: []string{"low", "high"}}
	var boolean ClassifierQuestion = ClassifierBoolQuestion{Instructions: "Yes?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}
	for _, tc := range []struct {
		question ClassifierQuestion
		wantType string
		wantJSON string
	}{
		{choice, "choice", `{"type":"choice","instructions":"Which?","criteria":{"b":"B","a":"A"}}`},
		{score, "score", `{"type":"score","instructions":"How?","criteria":["low","high"]}`},
		{boolean, "bool", `{"type":"bool","instructions":"Yes?","criteria":{"true":"y","false":"n"}}`},
	} {
		if got := tc.question.QuestionType(); got != tc.wantType {
			t.Errorf("%T.QuestionType() = %q, want %q", tc.question, got, tc.wantType)
		}
		wire, err := json.Marshal(ClassifierQuestions{{ID: "q", Question: tc.question}})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"q":` + tc.wantJSON + `}`; string(wire) != want {
			t.Errorf("%T wire = %s, want %s", tc.question, wire, want)
		}
		var decoded ClassifierQuestions
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded) != 1 || decoded[0].Question == nil || decoded[0].Question.QuestionType() != tc.wantType {
			t.Errorf("%s decoded to %+v, want a %s question", wire, decoded, tc.wantType)
		}
	}
	for _, raw := range []string{
		`{"q":{"type":"rank","instructions":"i","criteria":[]}}`,
		`{"q":{"instructions":"i","criteria":[]}}`,
		`{"q":null}`,
	} {
		var decoded ClassifierQuestions
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil || !strings.Contains(err.Error(), "unknown question type") {
			t.Errorf("%s decoded with err %v, want an unknown question type error", raw, err)
		}
	}
}
