package coding

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// abortWaitTool runs until its run is aborted.
type abortWaitTool struct{ started chan struct{} }

func (abortWaitTool) Name() string                           { return "wait" }
func (abortWaitTool) Label() string                          { return "Wait" }
func (abortWaitTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (abortWaitTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait", Description: "Wait until aborted", Parameters: map[string]any{"type": "object"}}
}
func (tool abortWaitTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "aborted"}}, Details: map[string]any{}}, nil
}

// upstream: packages/coding-agent/src/core/agent-session.ts:674-683 and
// 626-671 dispatch turn_end from the Agent's finishTurn hook through
// emitBoundary, which takes no abort signal: after an abort, turn_end handlers
// still run for the aborted tool-call turn and the aborted response turn
// (agent-loop.ts:244-252,285). A subprocess extension refuses a call whose
// context is already cancelled, so the boundary must not hand its handlers the
// aborted run's context.
func TestTurnEndBoundaryAfterAbortGetsLiveContext(t *testing.T) {
	var mu sync.Mutex
	var reasons []ai.StopReason
	var cancelled []bool
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
		event := args[0].(extension.TurnEndEvent)
		ctx, _ := args[1].(context.Context)
		mu.Lock()
		defer mu.Unlock()
		if message, ok := event.Message.(agent.AgentMessage); ok && message.Assistant != nil {
			reasons = append(reasons, message.Assistant.StopReason)
		}
		cancelled = append(cancelled, ctx == nil || ctx.Err() != nil)
		return nil, nil
	}}}}
	tool := abortWaitTool{started: make(chan struct{})}
	h := newBoundaryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{tool}}, boundaryToolReply("wait", ai.JsonObject{}, ai.StopReasonToolUse), boundaryReply("must not run", ai.StopReasonStop, 0))
	prompt := make(chan error, 1)
	go func() { _, err := h.session.Prompt(t.Context(), "start", nil); prompt <- err }()
	<-tool.started
	h.session.RequestAbort()
	if err := <-prompt; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) == 0 || reasons[0] != ai.StopReasonToolUse {
		t.Fatalf("turn_end messages = %v, want the tool-call turn first", reasons)
	}
	for i, wasCancelled := range cancelled {
		if wasCancelled {
			t.Fatalf("turn_end %d (%s) ran with a cancelled context; stop reasons %v", i, reasons[i], reasons)
		}
	}
}
