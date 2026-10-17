package driver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// Pi 42a3497d types.ts ToolExecutionApi.retainedOutput: the output retained so far, as the model will see it, and whether
// earlier output was dropped. The core holds the call's output buffer; the tool runs in the host, so the host's api
// keeps the same retained view.
func TestRetainedOutputIsTheOutputKeptSoFar(t *testing.T) {
	var text string
	var truncated bool
	core := &funcCore{respond: openWith("t", `{}`, "")}
	host := toolHost(t, core, tool(func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("one ")
		api.Output([]byte("two"))
		text, truncated = api.RetainedOutput()
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	if text != "one two" || truncated {
		t.Fatalf("retained = %q truncated %v", text, truncated)
	}
}

func TestRetainedOutputReportsDroppedOutputAsTruncated(t *testing.T) {
	var text string
	var truncated bool
	core := &funcCore{respond: openWith("t", `{}`, `{"maxBytes":10,"maxLines":2}`)}
	host := toolHost(t, core, tool(func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("tail", env.ShellOutputSkip{Bytes: 9, Newlines: 2, EndsWithNewline: true})
		text, truncated = api.RetainedOutput()
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	if !truncated || !strings.HasSuffix(text, "tail") {
		t.Fatalf("retained = %q truncated %v", text, truncated)
	}
}
