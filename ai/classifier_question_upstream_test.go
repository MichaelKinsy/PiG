package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/ai/src/types.ts:649-667 ClassifierQuestion = ClassifierChoiceQuestion | ClassifierScoreQuestion | ClassifierBoolQuestion, discriminated by `type` ("choice", "score", "bool"). TypeSafe's wire type "noul" is not a public question type: packages/ai/test/typesafe-system-one.test.ts:55-60 maps the public bool question to "noul" only on the way out.
// The three variants are the questions of that test's context (:19-40). Each is held as a ClassifierQuestion, reports its discriminator, survives the {id: question} wire round trip as the same variant in the same order, and a question of type "noul" or any other literal is rejected.
// mutation-checked: the bool variant reporting "noul", the decoder turning a score question into a choice question, and the decoder accepting "noul" each fail it.
func TestClassifierQuestionUnionVariantsAndDiscriminatorsUpstream(t *testing.T) {
	var questions ClassifierQuestions = ClassifierQuestions{
		{ID: "category", Question: ClassifierChoiceQuestion{Instructions: "Classify the message", Criteria: []ClassifierChoiceCriterion{{Key: "success", Description: "Successful"}, {Key: "failure", Description: "Failed"}}}},
		{ID: "satisfaction", Question: ClassifierScoreQuestion{Instructions: "Score satisfaction", Criteria: []string{"low", "neutral", "high"}}},
		{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "Does the user approve?", Criteria: ClassifierBoolCriteria{True: "Approval", False: "No approval"}}},
	}
	for i, want := range []string{"choice", "score", "bool"} {
		var question ClassifierQuestion = questions[i].Question
		if got := question.QuestionType(); got != want {
			t.Errorf("%s: QuestionType = %q, want %q", questions[i].ID, got, want)
		}
	}

	encoded, err := json.Marshal(questions)
	if err != nil {
		t.Fatal(err)
	}
	const wire = `{"category":{"type":"choice","instructions":"Classify the message","criteria":{"success":"Successful","failure":"Failed"}},"satisfaction":{"type":"score","instructions":"Score satisfaction","criteria":["low","neutral","high"]},"approved":{"type":"bool","instructions":"Does the user approve?","criteria":{"true":"Approval","false":"No approval"}}}`
	if string(encoded) != wire {
		t.Fatalf("questions = %s\nwant      %s", encoded, wire)
	}
	var decoded ClassifierQuestions
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, questions) {
		t.Fatalf("round trip = %#v, want %#v", decoded, questions)
	}

	for _, literal := range []string{"noul", "ranking"} {
		var rejected ClassifierQuestions
		err := json.Unmarshal([]byte(`{"q":{"type":"`+literal+`","instructions":"i","criteria":{"true":"a","false":"b"}}}`), &rejected)
		if err == nil || !strings.Contains(err.Error(), "unknown question type") {
			t.Errorf("type %q: error = %v, want unknown question type", literal, err)
		}
	}
}
