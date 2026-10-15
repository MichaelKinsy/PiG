package ai

import "testing"

// .upstream/current/packages/ai/src/api/llama-cpp-classify.ts:123-141 (renderTask) and :149-190 (renderQuestion). Pi's tests assert
// rendered prompts for fully described questions; these pin the omissions: a bool question states only the meanings it has, and a
// choice option without a description is just its key.

func renderedFor(t *testing.T, state JsonObject, questions ...ClassifierQuestionEntry) LlamaCppLabeledQuestion {
	t.Helper()
	got, err := LlamaCppRenderQuestion(ClassifierContext{State: state, Questions: questions}, questions[len(questions)-1].ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLlamaCppRenderQuestionBoolStatesOnlyTheMeaningsItHas(t *testing.T) {
	const intro = "State:\n{}\n\nTask: answer the following question about the state.\n\n"
	for _, tc := range []struct {
		name     string
		criteria ClassifierBoolCriteria
		task     string
	}{
		{"no meanings", ClassifierBoolCriteria{}, "Question: Is it ok?"},
		{"only yes", ClassifierBoolCriteria{True: "fine"}, "Question: Is it ok?\n\nYes means: fine"},
		{"only no", ClassifierBoolCriteria{False: "broken"}, "Question: Is it ok?\n\nNo means: broken"},
		{"both", ClassifierBoolCriteria{True: "fine", False: "broken"}, "Question: Is it ok?\n\nYes means: fine\nNo means: broken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderedFor(t, JsonObject{}, ClassifierQuestionEntry{ID: "q", Question: ClassifierBoolQuestion{Instructions: "Is it ok?", Criteria: tc.criteria}})
			want := intro + tc.task + "\n\nState:\n{}\n\n" + tc.task + "\n\nAnswer Yes or No."
			if got.Content != want {
				t.Fatalf("content:\n%q\nwant:\n%q", got.Content, want)
			}
			if len(got.Keys) != 2 || got.Keys[0] != "true" || got.Keys[1] != "false" {
				t.Fatalf("keys = %v", got.Keys)
			}
		})
	}
}

func TestLlamaCppRenderQuestionChoiceOptionWithoutDescriptionIsJustItsKey(t *testing.T) {
	got := renderedFor(t, JsonObject{}, ClassifierQuestionEntry{ID: "q", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: []ClassifierChoiceCriterion{{Key: "a"}, {Key: "b", Description: "second"}}}})
	// The overview lists options unlabeled (renderTask(question, undefined)); the final task puts the answer labels on them.
	want := "State:\n{}\n\nTask: answer the following question about the state.\n\nQuestion: Pick\n\nOptions:\n- a\n- b: second\n\n" +
		"State:\n{}\n\nQuestion: Pick\n\nOptions:\nA. a\nB. b: second\n\nAnswer with one letter."
	if got.Content != want {
		t.Fatalf("content:\n%q\nwant:\n%q", got.Content, want)
	}
}
