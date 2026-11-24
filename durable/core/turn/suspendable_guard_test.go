package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// Pi v1.6.0 bumped pi.generation to v3 (afterTools, yield) and pi.tool to v4 (parked). This core accepts v3/v4 records
// for store/handoff compatibility, and must explicitly reject stored checkpoints in those unimplemented suspendable-work
// phases rather than mis-executing them.
func TestSuspendablePhasesAreExplicitlyRejected(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		phase string
	}{
		{turn.KindGeneration, "afterTools"},
		{turn.KindGeneration, "yield"},
		{turn.KindTool, "parked"},
	} {
		t.Run(tc.kind+"-"+tc.phase, func(t *testing.T) {
			r := newRig(t)
			var taskID session.ID
			r.commit(func(tx *session.Tx) error {
				if _, err := tx.CreateRootConversation(); err != nil {
					return err
				}
				var def *session.TaskDef
				if tc.kind == turn.KindGeneration {
					def = turn.GenerationDef
				} else {
					def = turn.ToolDef
				}
				id, err := tx.CreateTask(def, jv.NewObject(), session.TaskOptions{
					ConversationID: session.RootConversationID,
				})
				if err != nil {
					return err
				}
				taskID = id
				return nil
			})
			r.commit(func(tx *session.Tx) error {
				rec, err := tx.Task(taskID)
				if err != nil {
					return err
				}
				cp := jv.NewObject()
				cp.Set("phase", tc.phase)
				st := jv.NewObject()
				st.Set("status", session.StatusPending)
				st.Set("checkpoint", cp)
				next := rec.Shallow()
				next.Set("state", st)
				return tx.SetTask(next)
			})
			r.resume()
			rec, err := r.mem.Task(taskID)
			if err != nil || rec == nil {
				t.Fatalf("task %d: %v, %v", taskID, rec, err)
			}
			got := jv.Stringify(rec)
			if !strings.Contains(got, `"status":"faulted"`) || !strings.Contains(got, "suspendable work") || !strings.Contains(got, tc.phase) {
				t.Fatalf("task = %s, want faulted with explicit suspendable work rejection for %q", got, tc.phase)
			}
		})
	}
}
