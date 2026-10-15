package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d types.ts ToolExecutionApi.retainedOutput and harness/tool.ts: the output retained so far, as the model will see
// it, and whether earlier output was dropped (head retention keeps the first maxBytes).
func TestRetainedOutputIsTheOutputKeptSoFar(t *testing.T) {
	setup := chatSetup(t)
	var kept, afterDrop string
	var truncated, droppedTruncated bool
	addTool(t, setup.Registry, tlTool("talk", func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("one ")
		api.Output([]byte("two"))
		kept, truncated = api.RetainedOutput()
		api.Output(strings.Repeat("x", 100))
		afterDrop, droppedTruncated = api.RetainedOutput()
		return durable.ToolExecutionResult{}, nil
	}, func(tool *durable.ToolRegistration) {
		tool.OutputLimits = &durable.ToolOutputLimits{MaxBytes: new(20), MaxLines: new(100), Retain: durable.RetainHead}
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"talk", map[string]any{}, "c1"}), tlDone()}, nil)
	if kept != "one two" || truncated {
		t.Fatalf("retained %q truncated %v, want %q and not truncated", kept, truncated, "one two")
	}
	if !droppedTruncated || len(afterDrop) != 20 || !strings.HasPrefix(afterDrop, "one two") {
		t.Fatalf("after dropping: retained %q (%d bytes) truncated %v, want the first 20 bytes and truncated", afterDrop, len(afterDrop), droppedTruncated)
	}
	mustClose(t, run.harness)
}
