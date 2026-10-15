// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// A nested call's admission is a commit of the calling task (tool.ts startNestedCall: runtime.commit), so once the caller
// has its abort mark, executeTool rejects with the gate's error (scheduler.ts:1386-1388 #gated), as every commit of an
// aborted task does.
func TestCoreANestedCallOfAnAbortedCallerRejectsWithTheGateError(t *testing.T) {
	var admission error
	var caller durable.TaskId
	running := make(chan struct{})
	_, root, sub := nestedCoreHarness(t, echoCoreTool(),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			caller = api.TaskId()
			close(running)
			<-ctx.Done()
			_, admission = api.ExecuteTool(context.WithoutCancel(ctx), "echo", map[string]any{"text": "late"})
			return durable.ToolExecutionResult{}, ctx.Err()
		}))
	<-running
	if err := root.Abort(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	nestedCoreWait(t, sub, durable.SubmissionUnanswered)
	if want := fmt.Sprintf("Task %d has a durable abort mark", caller); admission == nil || admission.Error() != want {
		t.Fatalf("admission error = %v, want %q", admission, want)
	}
}
