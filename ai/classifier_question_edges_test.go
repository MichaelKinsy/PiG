package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// types.ts:655-659 types a score question's criteria as string[] (never absent), so a Go question whose criteria slice is nil still writes `[]`, not `null`;
// and a question that fails to decode names its ID in the error (the decoder reads one entry at a time in key order).
//
// mutation-checked: marshalling the nil slice as it is, and dropping the question ID from the decode error, each fail.
func TestClassifierQuestionEdges(t *testing.T) {
	encoded, err := json.Marshal(ClassifierScoreQuestion{Instructions: "rate it"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"score","instructions":"rate it","criteria":[]}`; string(encoded) != want {
		t.Fatalf("score question with no criteria = %s, want %s", encoded, want)
	}
	var questions ClassifierQuestions
	err = json.Unmarshal([]byte(`{"ok":{"type":"bool","instructions":"i","criteria":{"true":"t","false":"f"}},"broken":{"type":"mystery","instructions":"i","criteria":[]}}`), &questions)
	if err == nil || !strings.Contains(err.Error(), `question "broken"`) || !strings.Contains(err.Error(), `unknown question type "mystery"`) {
		t.Fatalf("decode error = %v, want it to name question \"broken\" and its unknown type", err)
	}
}
