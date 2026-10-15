package turn_test

// pi: packages/durable/src/harness/generation.ts (failNoModel :337-345)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// generation.ts:337-345: a generation whose agent has no model, or a model the host does not know, ends its run's inputs `unanswered` with reason `no_model` and fails with "No model is configured" or
// "Model <provider>/<id> is not available" and `detail: {reason: "no_model"}`; the model is never asked.
// mutation-checked: swapping the two messages (the `ref != nil` test) fails both cases.
func TestGenerationWithoutAUsableModelFailsTheRunWithNoModel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   *turn.ModelRef
		message string
	}{
		{"no model", nil, `"message":"No model is configured"`},
		{"unknown model", &turn.ModelRef{Provider: "other", ModelID: "x"}, `"message":"Model other/x is not available"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.agent.Model = tc.model
			_, submission := r.start("hi")
			r.resume()
			r.run()
			if len(r.requests) != 0 {
				t.Fatalf("the model was asked %d time(s)", len(r.requests))
			}
			tasks := r.tasks(turn.KindGeneration)
			if len(tasks) != 1 {
				t.Fatalf("generation tasks = %d", len(tasks))
			}
			outcome := outcomeOf(t, tasks[0])
			if !strings.Contains(outcome, `"status":"failed"`) || !strings.Contains(outcome, tc.message) || !strings.Contains(outcome, `"detail":{"reason":"no_model"}`) {
				t.Errorf("outcome = %s, want a failure with %s and reason no_model", outcome, tc.message)
			}
			sub, _ := r.mem.Submission(submission)
			if got := jv.Stringify(sub); !strings.Contains(got, `"status":"unanswered"`) || !strings.Contains(got, `"reason":"no_model"`) {
				t.Errorf("submission = %s", got)
			}
		})
	}
}
