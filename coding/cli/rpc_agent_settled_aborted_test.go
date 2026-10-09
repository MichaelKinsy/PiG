package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 1.1.0 agent-session.ts:203 AgentSessionEvent { type: "agent_settled"; aborted: boolean }: toJsonEvent writes the event
// unchanged, so the RPC and JSON-mode wire carries `aborted` after `type` (CHANGELOG 1.1.0, #10607).
func TestRPCAgentSettledCarriesAborted(t *testing.T) {
	for _, tc := range []struct {
		event agent.AgentSettledEvent
		want  string
	}{
		{agent.AgentSettledEvent{}, `{"type":"agent_settled","aborted":false}`},
		{agent.AgentSettledEvent{Aborted: true}, `{"type":"agent_settled","aborted":true}`},
	} {
		frames, err := rpcAgentEvent(tc.event)
		if err != nil || len(frames) != 1 {
			t.Fatalf("frames %v, err %v", frames, err)
		}
		got, err := json.Marshal(frames[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("wire = %s, want %s", got, tc.want)
		}
	}
}

// Pi 1.1.0 agent-loop.ts:933-942 writes tool_execution_end with durationMs after isError when the tool ran (#10549); a call that did not run has none.
func TestRPCToolExecutionEndCarriesDurationMs(t *testing.T) {
	for _, tc := range []struct {
		durationMs *int64
		want       string
	}{
		{new(int64(4200)), `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[],"details":{}},"isError":false,"durationMs":4200}`},
		{new(int64(0)), `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[],"details":{}},"isError":false,"durationMs":0}`},
		{nil, `{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[],"details":{}},"isError":false}`},
	} {
		frames, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "c1", ToolName: "bash", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}, DurationMs: tc.durationMs})
		if err != nil || len(frames) != 1 {
			t.Fatalf("frames %v, err %v", frames, err)
		}
		got, err := json.Marshal(frames[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("wire = %s\nwant   %s", got, tc.want)
		}
	}
}

// Pi 1.1.0 event-stream.ts:127-128 sets durationMs on the final assistant message after the provider's own keys and before the agent loop assigns thinkingLevel (agent-loop.ts:409); the RPC and JSON stream write it there, and omit it for a message that was not timed.
func TestRPCAssistantMessageCarriesDurationMsBeforeThinkingLevel(t *testing.T) {
	for _, tc := range []struct {
		durationMs *int64
		want       string
	}{
		{new(int64(250)), `,"endTurn":true,"durationMs":250,"thinkingLevel":"off"}`},
		{nil, `,"endTurn":true,"thinkingLevel":"off"}`},
	} {
		assistant := &agent.AssistantMessage{Role: agent.RoleAssistant, API: "faux", Provider: "p", ModelID: "m", StopReason: ai.StopReasonStop, EndTurn: new(true), DurationMs: tc.durationMs, ThinkingLevel: "off"}
		value, err := rpcAgentMessage(agent.AgentMessage{Assistant: assistant})
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(got), tc.want) {
			t.Errorf("wire = %s, want it to end with %s", got, tc.want)
		}
	}
}
