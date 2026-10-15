// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// tool.ts: a result the settling commit cannot store as strict JSON rejects it, so the call's task faults. Over the core the
// value cannot cross the host boundary: the call faults the same way, the Session stays up and the submission still ends.
func TestCoreAToolResultThatIsNotStrictJSONFaultsTheCallAndNotTheSession(t *testing.T) {
	for name, result := range map[string]durable.ToolExecutionResult{
		"details NaN":  {Details: map[string]any{"x": math.NaN()}, HasDetails: true},
		"details chan": {Details: map[string]any{"x": make(chan int)}, HasDetails: true},
	} {
		t.Run(name, func(t *testing.T) {
			h, root, sub := nestedCoreHarnessWith(t, &durable.Extension{Name: "x", Tools: []*durable.ToolRegistration{
				nestedCoreTool("batch", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					return result, nil
				}),
			}})
			nestedCoreWait(t, sub, durable.SubmissionDone)
			conversationID := root.Id()
			page, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
				return tx.ScanTasks(durable.TaskQuery{ConversationId: &conversationID}, 20, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			faulted := 0
			for _, task := range page.Items {
				if task.Kind == "pi.tool" && task.State.Outcome != nil && task.State.Outcome.Status == durable.OutcomeFaulted {
					faulted++
				}
			}
			if faulted != 1 {
				t.Fatalf("%d faulted tool tasks, want 1", faulted)
			}
		})
	}
}
