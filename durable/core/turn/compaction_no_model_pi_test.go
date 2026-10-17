package turn_test

// pi: packages/durable/src/harness/compaction.ts (no model :458-464), packages/durable/src/harness/live.ts (failCompactionStatus :169-173)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// compaction.ts:458-464: a compaction whose agent has no model fails with "No model is configured", and one whose model the host does not know fails with "Model <provider>/<id> is not available"; both carry `detail: {reason: "no_model"}`, take the status off the live slot, record the failure as pi.live.compactionFailure (live.ts:169-173 failCompactionStatus) and leave no summary entry.
// mutation-checked: swapping the two messages (the `ref != nil` test) fails both cases.
func TestCompactionWithoutAUsableModelFailsWithNoModel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   *turn.ModelRef
		message string
		failure string
	}{
		{"no model", nil, `"message":"No model is configured"`, `"reason":"manual","error":"No model is configured"}}`},
		{"unknown model", &turn.ModelRef{Provider: "other", ModelID: "x"}, `"message":"Model other/x is not available"`, `"reason":"manual","error":"Model other/x is not available"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := compactionRig(t)
			r.agent.Model = tc.model
			r.resume()
			r.run()
			tasks := r.tasks(turn.KindCompaction)
			if len(tasks) != 1 {
				t.Fatalf("compaction tasks = %d", len(tasks))
			}
			outcome := outcomeOf(t, tasks[0])
			if !strings.Contains(outcome, `"status":"failed"`) || !strings.Contains(outcome, tc.message) || !strings.Contains(outcome, `"detail":{"reason":"no_model"}`) {
				t.Fatalf("outcome = %s, want a failure with %s and reason no_model", outcome, tc.message)
			}
			if len(r.complete) != 0 {
				t.Errorf("a summarization request was made")
			}
			if live := r.doc(turn.LiveDef, 1); !strings.HasPrefix(live, `{"compactionFailure":{"taskId":`) || !strings.HasSuffix(live, tc.failure) {
				t.Errorf("live = %s, want the compaction status replaced by its failure", live)
			}
		})
	}
}
