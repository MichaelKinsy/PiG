package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/agent-loop.test.ts "records how long execute() took on the tool result, excluding hooks" (#10549).
// A tool that sleeps 30ms behind a before hook that sleeps 100ms records 25-99ms on its result message and on tool_execution_end;
// a blocked call, which never ran, has none (agent-loop.ts:826-852,908,926,941).
func TestAgentLoop_RecordsHowLongExecuteTookExcludingHooks(t *testing.T) {
	echo := iserrorTool("echo", func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		time.Sleep(30 * time.Millisecond)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}}, Details: map[string]any{}}, nil
	})
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("ran", "echo", ai.JsonObject{"value": "a"}),
		toolCall("blocked", "echo", ai.JsonObject{"value": "b"}),
	)}
	rec := newEventRecorder(nil)
	a := mustNewAgent(AgentOptions{
		Model: scriptedModel(provider), Tools: []AgentTool{echo}, EventCh: rec.ch,
		BeforeToolCallHooks: []BeforeToolCallHook{func(_ context.Context, id, _ string, _ json.RawMessage) ToolCallHookResult {
			time.Sleep(100 * time.Millisecond)
			if id == "blocked" {
				return ToolCallHookResult{Block: true, Reason: "no"}
			}
			return ToolCallHookResult{}
		}},
	})
	msgs := mustSend(t, a, "go")
	results := map[string]*ToolResultMessage{}
	for _, msg := range msgs {
		if msg.ToolResult != nil {
			results[msg.ToolResult.ToolCallID] = msg.ToolResult
		}
	}
	ran, blocked := results["ran"], results["blocked"]
	if ran == nil || blocked == nil {
		t.Fatalf("tool results = %v", results)
	}
	if ran.DurationMs == nil || *ran.DurationMs < 25 || *ran.DurationMs >= 100 {
		t.Errorf("ran durationMs = %v, want 25..99", ran.DurationMs)
	}
	if !blocked.IsError || blocked.DurationMs != nil {
		t.Errorf("blocked = %+v, want an error result without durationMs", blocked)
	}
	for _, end := range toolEndEvents(rec.stop()) {
		switch end.ToolCallID {
		case "ran":
			if end.DurationMs == nil || *end.DurationMs != *ran.DurationMs {
				t.Errorf("tool_execution_end durationMs = %v, want the message's %d", end.DurationMs, *ran.DurationMs)
			}
		case "blocked":
			if end.DurationMs != nil {
				t.Errorf("blocked tool_execution_end durationMs = %v, want none", *end.DurationMs)
			}
		}
	}
	// message JSON: durationMs sits after isError and before timestamp (agent-loop.ts:925-931), and survives a round trip.
	wire, err := json.Marshal(AgentMessage{ToolResult: ran})
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	if isError, duration, timestamp := strings.Index(text, `"isError"`), strings.Index(text, `"durationMs"`), strings.Index(text, `"timestamp"`); isError >= duration || duration >= timestamp {
		t.Errorf("wire member order = %s, want isError < durationMs < timestamp", text)
	}
	var back AgentMessage
	if err := json.Unmarshal(wire, &back); err != nil || back.ToolResult == nil || back.ToolResult.DurationMs == nil || *back.ToolResult.DurationMs != *ran.DurationMs {
		t.Errorf("round trip = %+v, %v", back.ToolResult, err)
	}
	blockedWire, _ := json.Marshal(AgentMessage{ToolResult: blocked})
	if strings.Contains(string(blockedWire), "durationMs") {
		t.Errorf("blocked wire = %s, want no durationMs", blockedWire)
	}
	for _, message := range ConvertToLLM(NormalizeMessages(msgs, scriptedModel(provider))) {
		if converted, ok := message.(ai.ToolResultMessage); ok && converted.ToolCallID == "ran" {
			if converted.DurationMs == nil || *converted.DurationMs != *ran.DurationMs {
				t.Errorf("LLM message = %+v, want durationMs carried", converted)
			}
			return
		}
	}
	t.Error("no LLM tool result for the run call")
}

// A tool that throws still ran: upstream's catch returns durationMs with the error result (agent-loop.ts:845-852).
func TestAgentLoop_ThrownToolErrorRecordsDuration(t *testing.T) {
	boom := iserrorTool("boom", func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{}, errors.New("thrown")
	})
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("c1", "boom", ai.JsonObject{"value": "x"}), toolCall("c2", "missing", ai.JsonObject{"value": "x"}))}
	rec := newEventRecorder(nil)
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{boom}, EventCh: rec.ch})
	mustSend(t, a, "go")
	for _, end := range toolEndEvents(rec.stop()) {
		switch end.ToolCallID {
		case "c1":
			if end.DurationMs == nil {
				t.Error("thrown call has no durationMs")
			}
		case "c2":
			if end.DurationMs != nil {
				t.Errorf("unknown tool durationMs = %d, want none", *end.DurationMs)
			}
		}
	}
}

// Pi 1.1.0 ai/src/types.ts:582: AssistantMessage.durationMs is stored with the message (session JSON: after timestamp, before thinkingLevel, which the agent loop assigns later), survives a round trip and ConvertToLLM, and a message without one has no member.
func TestAssistantMessageDurationMsRoundTrips(t *testing.T) {
	message := &AssistantMessage{Role: RoleAssistant, API: "faux", Provider: "p", ModelID: "m", StopReason: ai.StopReasonStop, Timestamp: 5, DurationMs: new(int64(250)), ThinkingLevel: "off"}
	wire, err := json.Marshal(AgentMessage{Assistant: message})
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	if timestamp, duration, level := strings.Index(text, `"timestamp"`), strings.Index(text, `"durationMs":250`), strings.Index(text, `"thinkingLevel"`); timestamp < 0 || timestamp >= duration || duration >= level {
		t.Errorf("wire member order = %s, want timestamp < durationMs < thinkingLevel", text)
	}
	untimed, _ := json.Marshal(AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, API: "faux", Provider: "p", ModelID: "m", Timestamp: 5}})
	if strings.Contains(string(untimed), "durationMs") {
		t.Errorf("an untimed message wrote %s", untimed)
	}
	var back AgentMessage
	if err := json.Unmarshal(wire, &back); err != nil || back.Assistant == nil || back.Assistant.DurationMs == nil || *back.Assistant.DurationMs != 250 {
		t.Fatalf("round trip = %+v, %v", back.Assistant, err)
	}
	for _, converted := range ConvertToLLM(NormalizeMessages([]AgentMessage{{Assistant: message}}, nil)) {
		if assistant, ok := converted.(*ai.AssistantMessage); ok {
			if assistant.DurationMs == nil || *assistant.DurationMs != 250 {
				t.Errorf("LLM assistant message durationMs = %v", assistant.DurationMs)
			}
			return
		}
		if assistant, ok := converted.(ai.AssistantMessage); ok {
			if assistant.DurationMs == nil || *assistant.DurationMs != 250 {
				t.Errorf("LLM assistant message durationMs = %v", assistant.DurationMs)
			}
			return
		}
	}
	t.Error("no assistant message after ConvertToLLM")
}

// The agent loop's response carries the stream's timing: a faux provider answers through AssistantMessageEventStream, so the message the Agent returns has durationMs (event-stream.ts, agent-loop.ts streamAssistantResponse).
func TestAgentLoopAssistantMessageIsTimedByTheStream(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		// A provider stamps its message when the request starts, which is after the stream exists.
		message := textMessage("hi")
		message.Timestamp = time.Now().UnixMilli() + 1
		return doneStream(message)
	}}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider)})
	msgs := mustSend(t, a, "go")
	for _, msg := range msgs {
		if msg.Assistant != nil {
			t.Logf("durationMs = %v", msg.Assistant.DurationMs)
			if msg.Assistant.DurationMs == nil {
				t.Fatal("the assistant message has no durationMs")
			}
			return
		}
	}
	t.Fatal("no assistant message")
}
