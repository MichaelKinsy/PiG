package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestDispatchAgentLoopEvent_DeliversAllAgentLoopEvents locks the session-driven
// dispatch contract (issue #442): DispatchAgentLoopEvent maps every agent-loop
// event to the extension runner. The session calls this from forwardAgentEvents,
// the single event funnel all drivers (interactive, rpc, print) share, so this
// mapping is what every driver delivers to extensions.
func TestDispatchAgentLoopEvent_DeliversAllAgentLoopEvents(t *testing.T) {
	got := map[string]any{}
	record := func(name string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			if len(args) > 0 {
				got[name] = args[0]
			}
			return nil, nil
		}
	}
	names := []string{
		EventAgentStart, EventAgentEnd, EventTurnStart, EventTurnEnd,
		EventMessageStart, EventMessageUpdate, EventMessageEnd,
		EventToolExecutionStart, EventToolExecutionUpdate, EventToolExecutionEnd,
	}
	handlers := map[string][]extension.HandlerFn{}
	for _, n := range names {
		handlers[n] = []extension.HandlerFn{record(n)}
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/tmp/rec.ts", Handlers: handlers}}, ".")

	startMsg := agent.AgentMessage{Custom: map[string]any{"marker": "m1"}}
	var cur extension.AgentMessage
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonPending}
	events := []agent.AgentEvent{
		agent.AgentStartEvent{},
		agent.TurnStartEvent{TurnIndex: 3},
		agent.MessageStartEvent{Message: startMsg},
		agent.MessageUpdateEvent{
			Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: partial.Content}},
			AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "hi", Partial: partial},
		},
		agent.ToolExecutionStartEvent{ToolCallID: "tc1", ToolName: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
		agent.ToolExecutionUpdateEvent{ToolCallID: "tc1", ToolName: "bash", Content: "partial", Details: map[string]any{"progress": float64(1)}, Args: json.RawMessage(`{"cmd":"ls"}`)},
		agent.ToolExecutionEndEvent{ToolCallID: "tc1", ToolName: "bash", Result: agent.AgentToolResult{Content: "done", Images: []ai.ImageContent{{Data: "aW1n", MimeType: "image/png"}}, Details: map[string]any{"nested": map[string]any{"value": "kept"}}, IsError: true}},
		agent.MessageEndEvent{Message: agent.AgentMessage{Custom: map[string]any{"marker": "end"}}},
		agent.TurnEndEvent{TurnIndex: 3},
		agent.AgentEndEvent{Messages: []agent.AgentMessage{{Custom: map[string]any{"marker": "final"}}}},
	}
	for _, ev := range events {
		DispatchAgentLoopEvent(runner, ev, &cur)
	}

	for _, n := range names {
		if _, ok := got[n]; !ok {
			t.Fatalf("event %q was not delivered to the extension runner", n)
		}
	}

	if ts, ok := got[EventTurnStart].(extension.TurnStartEvent); !ok || ts.TurnIndex != 3 {
		t.Fatalf("turn_start: got %#v, want TurnIndex=3", got[EventTurnStart])
	}
	if te, ok := got[EventTurnEnd].(extension.TurnEndEvent); !ok || te.TurnIndex != 3 {
		t.Fatalf("turn_end: got %#v, want TurnIndex=3", got[EventTurnEnd])
	}
	// message_update must carry the message tracked from message_start.
	mu, ok := got[EventMessageUpdate].(extension.MessageUpdateEvent)
	if !ok {
		t.Fatalf("message_update: got %#v", got[EventMessageUpdate])
	}
	trackedMsg, ok := mu.Message.(agent.AgentMessage)
	if !ok || trackedMsg.Custom["marker"] != "m1" {
		t.Fatalf("message_update carried the wrong message: got %#v, want the message from message_start (marker=m1)", mu.Message)
	}
	delta, ok := mu.AssistantMessageEvent.(ai.TextDeltaEvent)
	if !ok || delta.ContentIndex != 0 || delta.Delta != "hi" || delta.Partial != partial {
		t.Fatalf("message_update assistant event = %#v", mu.AssistantMessageEvent)
	}
	encodedUpdate, err := json.Marshal(mu.AssistantMessageEvent)
	if err != nil {
		t.Fatal(err)
	}
	var updateWire map[string]any
	if err := json.Unmarshal(encodedUpdate, &updateWire); err != nil {
		t.Fatal(err)
	}
	assistantWire := updateWire
	if assistantWire["type"] != "text_delta" || assistantWire["contentIndex"] != float64(0) || assistantWire["delta"] != "hi" {
		t.Fatalf("message_update wire = %s", encodedUpdate)
	}
	if _, nested := assistantWire["assistantMessageEvent"]; nested {
		t.Fatalf("message_update retained outer agent wrapper: %s", encodedUpdate)
	}
	ts, ok := got[EventToolExecutionStart].(extension.ToolExecutionStartEvent)
	if !ok || ts.ToolCallID != "tc1" || ts.ToolName != "bash" {
		t.Fatalf("tool_execution_start: got %#v", got[EventToolExecutionStart])
	}
	if argsMap, ok := ts.Args.(map[string]any); !ok || argsMap["cmd"] != "ls" {
		t.Fatalf("tool_execution_start args not unmarshaled: got %#v", ts.Args)
	}
	tu, ok := got[EventToolExecutionUpdate].(extension.ToolExecutionUpdateEvent)
	wantPartial := map[string]any{"content": "partial", "details": map[string]any{"progress": float64(1)}}
	if !ok || !reflect.DeepEqual(tu.PartialResult, wantPartial) || !reflect.DeepEqual(tu.Args, ts.Args) {
		t.Fatalf("tool_execution_update: got %#v, want Args=%#v PartialResult=%#v", got[EventToolExecutionUpdate], ts.Args, wantPartial)
	}
	tend, ok := got[EventToolExecutionEnd].(extension.ToolExecutionEndEvent)
	wantResult := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "done"}, map[string]any{"type": "image", "data": "aW1n", "mimeType": "image/png"}},
		"details": map[string]any{"nested": map[string]any{"value": "kept"}},
	}
	if !ok || !reflect.DeepEqual(tend.Result, wantResult) || !tend.IsError {
		t.Fatalf("tool_execution_end: got %#v, want Result=%#v IsError=true", got[EventToolExecutionEnd], wantResult)
	}
	if ae, ok := got[EventAgentEnd].(extension.AgentEndEvent); !ok || len(ae.Messages) != 1 {
		t.Fatalf("agent_end: got %#v, want 1 message", got[EventAgentEnd])
	}
}

// TestDispatchAgentLoopEvent_NilRunnerNoPanic guards the print/headless path
// where no extensions are loaded: a nil runner must be a no-op, not a panic.
func TestDispatchAgentLoopEvent_NilRunnerNoPanic(t *testing.T) {
	var cur extension.AgentMessage
	DispatchAgentLoopEvent(nil, agent.AgentStartEvent{}, &cur)
	DispatchAgentLoopEvent(nil, agent.MessageStartEvent{Message: agent.AgentMessage{}}, &cur)
	DispatchAgentLoopEvent(nil, agent.AgentEndEvent{}, nil)
}
