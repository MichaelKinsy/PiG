package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// blockUntilAbortTool is regressions/8409-aborted-tool-auth-setup.test.ts's tool: it blocks until its signal aborts, then returns a result.
type blockUntilAbortTool struct{ started chan struct{} }

func (blockUntilAbortTool) Name() string                           { return "block" }
func (blockUntilAbortTool) Label() string                          { return "Block" }
func (blockUntilAbortTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (blockUntilAbortTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "block", Description: "Blocks until aborted", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool blockUntilAbortTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool aborted"}}}, nil
}

// pi: packages/coding-agent/test/suite/regressions/8409-aborted-tool-auth-setup.test.ts (Pi main f3a64af07, #8635). The turn after an
// aborted tool starts its request through the model runtime, whose lazyStream setup sees the aborted signal; the turn ends as aborted
// (lazy.ts: signal?.aborted ? "aborted" : "error"), not as an error carrying the signal's reason.
func TestAbortedToolAuthSetupEndsTheTurnAsAborted(t *testing.T) {
	tool := blockUntilAbortTool{started: make(chan struct{})}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}}, fauxToolCall("block"))
	runtime := h.session.ModelRuntime()
	h.session.Agent().SetStreamFunction(func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return runtime.StreamSimple(ctx, model, transcript.FrozenContext(), options), nil
	})
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "run the blocking tool", nil) }()
	waitFor(t, tool.started, "the blocking tool")
	if err := h.session.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-prompt; err != nil {
		t.Fatal(err)
	}
	last := lastAssistantMessage(h.session.Messages())
	if last == nil || last.StopReason != ai.StopReasonAborted {
		t.Fatalf("final assistant message = %+v, want stopReason aborted", last)
	}
}
