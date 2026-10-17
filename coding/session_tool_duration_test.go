package coding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type durationTool struct{ fakeTool }

func (*durationTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
}

// Pi 1.1.0 (#10549): the agent loop's recorded execute() time reaches tool_execution_end for extensions and subscribers (agent-session.ts extensionEvent spread), is stored on the tool result message, and survives reopening the session file, which is what lets `Took` show after a reload.
func TestSessionRecordsToolDurationOnEventsAndPersistedResult(t *testing.T) {
	var extensionDurations []*int64
	ext := extension.Extension{Path: "duration", Handlers: map[string][]extension.HandlerFn{"tool_execution_end": {func(args ...any) (any, error) {
		extensionDurations = append(extensionDurations, args[0].(extension.ToolExecutionEndEvent).DurationMs)
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{&durationTool{fakeTool{name: "timed"}}}, extension: ext}, fauxToolCall("timed"), fauxReply("done", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "run it"); err != nil {
		t.Fatal(err)
	}
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	<-h.done

	var subscriber *int64
	for _, event := range h.events {
		if end, ok := event.(agent.ToolExecutionEndEvent); ok {
			subscriber = end.DurationMs
		}
	}
	if subscriber == nil || *subscriber < 0 {
		t.Fatalf("subscriber tool_execution_end durationMs = %v, want a recorded duration", subscriber)
	}
	if len(extensionDurations) != 1 || extensionDurations[0] == nil || *extensionDurations[0] != *subscriber {
		t.Fatalf("extension tool_execution_end durationMs = %v, want %d", extensionDurations, *subscriber)
	}

	reopened, err := NewSession(h.session.services, SessionOptions{Model: h.session.agent.Model(), ResumePath: h.session.Path(), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	var stored *int64
	for _, message := range reopened.agent.Messages() {
		if message.ToolResult != nil {
			stored = message.ToolResult.DurationMs
		}
	}
	if stored == nil || *stored != *subscriber {
		t.Fatalf("reopened tool result durationMs = %v, want %d", stored, *subscriber)
	}
}
