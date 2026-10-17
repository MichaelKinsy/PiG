package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type sharedDetails struct{ Lines int }

// upstream: packages/agent/src/agent-loop.ts streamAssistantResponse passes `messages: llmMessages` to the stream function: the
// request holds the history's own messages, copied nowhere. A copy would marshal and decode every earlier tool result's details
// again on every turn, which costs a quarter of an interactive session's CPU.
func TestAgentLoop_RequestSharesTheHistoryMessages(t *testing.T) {
	details := &sharedDetails{Lines: 2000}
	var got any
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("unused")}), StreamFn: func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		for i := range transcript.Len() {
			if result, ok := transcript.At(i).(ai.ToolResultMessage); ok {
				got = result.Details
			}
		}
		return doneStream(textMessage("done")), nil
	}})
	history := []AgentMessage{
		userMessage("read it"),
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{toolCall("call-1", "read", nil)}}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call-1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: details, Timestamp: 1}},
		userMessage("again"),
	}
	if _, err := a.SendMessages(t.Context(), history); err != nil {
		t.Fatal(err)
	}
	if got != any(details) {
		t.Fatalf("the request's tool result details = %#v, want the history's own pointer %p: the loop copied the history", got, details)
	}
}

type marshalCountedDetails struct {
	Lines int
	calls *atomic.Int64
}

func (d *marshalCountedDetails) MarshalJSON() ([]byte, error) {
	d.calls.Add(1)
	return []byte(`{"lines":2000}`), nil
}

// The work a request does for the history is flat in the history's length: each tool result's details are validated for the
// request once, not once per later turn. pi-ai validates nothing; the independent denominator is one marshal per result.
func TestAgentLoop_ValidatesEachHistoryValueOnce(t *testing.T) {
	const turns = 8
	var calls atomic.Int64
	tool := &scriptTool{name: "read", execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: &marshalCountedDetails{Lines: 2000, calls: &calls}}, nil
	}}
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call <= turns {
			return doneStream(toolUseMessage(toolCall(fmt.Sprintf("call-%d", call), "read", nil)))
		}
		return doneStream(textMessage("done"))
	}}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}})
	if _, err := a.SendMessages(t.Context(), []AgentMessage{userMessage("go")}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != turns {
		t.Fatalf("details were marshaled %d times for %d results over %d requests: the history is validated again every turn", got, turns, turns+1)
	}
}
