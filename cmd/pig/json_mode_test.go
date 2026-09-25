// json_mode_test.go: unit tests for the --mode json wire contract.
//
// The contract pinned here comes from Pi 0.86.1
// packages/coding-agent/src/modes/json-event.ts.
//
// Per AGENTS.md: no real API calls, no network.

package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// eventTypes renders the wire sequence the way the probes above read it, so a
// failure names the offending event instead of dumping raw JSON.
func eventTypes(t *testing.T, events []any) []string {
	t.Helper()
	var out []string
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		kind, _ := decoded["type"].(string)
		if kind == "message_update" {
			inner, ok := decoded["assistantMessageEvent"].(map[string]any)
			if !ok {
				t.Fatalf("message_update without assistantMessageEvent: %s", data)
			}
			innerType, _ := inner["type"].(string)
			kind += ":" + innerType
		}
		out = append(out, kind)
	}
	return out
}

// drive replays events through the JSON/RPC adapter in order.
func drive(t *testing.T, events ...agent.AgentEvent) []any {
	t.Helper()
	var out []any
	for _, ev := range events {
		converted, err := rpcAgentEvent(ev)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, converted...)
	}
	return out
}

func assistantStart() agent.AgentEvent {
	return agent.MessageStartEvent{Message: agent.AgentMessage{
		Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant},
	}}
}

func jsonTestPartial(content ...ai.AssistantContentBlock) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: content, StopReason: ai.StopReasonPending}
}

func jsonTestUpdate(event ai.AssistantMessageEvent, partial *ai.AssistantMessage) agent.MessageUpdateEvent {
	usage := partial.Usage
	return agent.MessageUpdateEvent{
		Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: agent.RoleAssistant, Content: partial.Content, Usage: &usage,
		}},
		AssistantMessageEvent: event,
	}
}

func assistantEnd(text string) agent.AgentEvent {
	return agent.MessageEndEvent{Message: agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:    agent.RoleAssistant,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}},
		},
	}}
}

// A streamed text run must be delimited: text_start before the first delta and
// text_end before the message closes. Upstream emits all three; pig previously
// emitted only the delta, so a consumer could not tell where a run began.
func TestJSONModeTextRunIsDelimited(t *testing.T) {
	partial4 := jsonTestPartial(ai.TextContent{Text: "4"})
	partial42 := jsonTestPartial(ai.TextContent{Text: "42"})
	got := eventTypes(t, drive(t,
		agent.AgentStartEvent{},
		agent.TurnStartEvent{},
		assistantStart(),
		jsonTestUpdate(ai.TextStartEvent{ContentIndex: 0, Partial: partial4}, partial4),
		jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: "4", Partial: partial4}, partial4),
		jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: "2", Partial: partial42}, partial42),
		jsonTestUpdate(ai.TextEndEvent{ContentIndex: 0, Content: "42", Partial: partial42}, partial42),
		assistantEnd("42"),
		agent.AgentEndEvent{},
	))

	want := []string{
		"agent_start", "turn_start", "message_start",
		"message_update:text_start",
		"message_update:text_delta",
		"message_update:text_delta",
		"message_update:text_end",
		"message_end", "agent_end",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence\n got: %v\nwant: %v", got, want)
	}
}

// text_end carries the accumulated run content, which is what lets a consumer
// render a finished block without replaying every delta.
func TestJSONModeTextEndCarriesAccumulatedContent(t *testing.T) {
	partial4 := jsonTestPartial(ai.TextContent{Text: "4"})
	partial42 := jsonTestPartial(ai.TextContent{Text: "42"})
	events := drive(t,
		assistantStart(),
		jsonTestUpdate(ai.TextStartEvent{ContentIndex: 0, Partial: partial4}, partial4),
		jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: "4", Partial: partial4}, partial4),
		jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: "2", Partial: partial42}, partial42),
		jsonTestUpdate(ai.TextEndEvent{ContentIndex: 0, Content: "42", Partial: partial42}, partial42),
		assistantEnd("42"),
	)
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, event := range decoded {
		inner, ok := event["assistantMessageEvent"].(map[string]any)
		if !ok || inner["type"] != "text_end" {
			continue
		}
		if inner["content"] != "42" {
			t.Fatalf("text_end content = %v, want \"42\"", inner["content"])
		}
		return
	}
	t.Fatalf("no text_end emitted: %s", data)
}

// Switching content type mid-message closes the text run first, so text_start
// and text_end stay balanced around interleaved tool calls.
func TestJSONModeToolCallClosesOpenTextRun(t *testing.T) {
	textPartial := jsonTestPartial(ai.TextContent{Text: "thinking about it"})
	tool := ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{}}
	toolPartial := jsonTestPartial(ai.TextContent{Text: "thinking about it"}, tool)
	got := eventTypes(t, drive(t,
		assistantStart(),
		jsonTestUpdate(ai.TextStartEvent{ContentIndex: 0, Partial: textPartial}, textPartial),
		jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: "thinking about it", Partial: textPartial}, textPartial),
		jsonTestUpdate(ai.TextEndEvent{ContentIndex: 0, Content: "thinking about it", Partial: textPartial}, textPartial),
		jsonTestUpdate(ai.ToolCallStartEvent{ContentIndex: 1, Partial: toolPartial}, toolPartial),
		jsonTestUpdate(ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: `{"path":`, Partial: toolPartial}, toolPartial),
	))

	want := []string{
		"message_start",
		"message_update:text_start",
		"message_update:text_delta",
		"message_update:text_end",
		"message_update:toolcall_start",
		"message_update:toolcall_delta",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence\n got: %v\nwant: %v", got, want)
	}
}

// Tool activity must be observable: name, id, structured arguments on start,
// and an explicit error flag on the result.
func TestJSONModeToolExecutionEventsCarryNameAndErrorState(t *testing.T) {
	events := drive(t,
		agent.ToolExecutionStartEvent{
			ToolCallID: "call-1", ToolName: "read", Args: []byte(`{"path":"/tmp/x"}`),
		},
		agent.ToolExecutionUpdateEvent{
			ToolCallID: "call-1", ToolName: "read", Args: []byte(`{"path":"/tmp/x"}`), Content: "working", Details: map[string]any{"progress": float64(1)},
		},
		agent.ToolExecutionEndEvent{
			ToolCallID: "call-1", ToolName: "read",
			Result: agent.AgentToolResult{Content: "boom", Images: []ai.ImageContent{{Data: "aW1n", MimeType: "image/png"}}, Details: map[string]any{"nested": map[string]any{"value": "kept"}}, IsError: true},
		},
	)
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 {
		t.Fatalf("want start, update, and end events, got %s", data)
	}
	if decoded[0]["type"] != "tool_execution_start" || decoded[0]["toolName"] != "read" {
		t.Fatalf("tool start missing name: %s", data)
	}
	args, ok := decoded[0]["args"].(map[string]any)
	if !ok || args["path"] != "/tmp/x" {
		t.Fatalf("tool start args not structured: %s", data)
	}
	updateArgs, ok := decoded[1]["args"].(map[string]any)
	if !ok || !reflect.DeepEqual(updateArgs, args) {
		t.Fatalf("tool update args diverged from start: %s", data)
	}
	if decoded[2]["type"] != "tool_execution_end" || decoded[2]["isError"] != true {
		t.Fatalf("tool end lost error state: %s", data)
	}
	result := decoded[2]["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) != 2 || content[0].(map[string]any)["text"] != "boom" || content[1].(map[string]any)["data"] != "aW1n" || !reflect.DeepEqual(result["details"], map[string]any{"nested": map[string]any{"value": "kept"}}) {
		t.Fatalf("tool end lost structured result: %s", data)
	}
}

// A streamed tool call is delimited the same way text is, and its terminating
// toolcall_end carries the assembled call so a consumer can render the
// invocation without reassembling argument deltas itself.
func TestJSONModeToolCallRunIsDelimitedAndAssembled(t *testing.T) {
	startCall := ai.ToolCall{ID: "call-1", Name: "bash", Arguments: ai.JsonObject{}}
	partialStart := jsonTestPartial(startCall)
	finalCall := ai.ToolCall{ID: "call-1", Name: "bash", Arguments: ai.JsonObject{"command": "expr 20 + 22"}}
	partialFinal := jsonTestPartial(finalCall)
	events := drive(t,
		assistantStart(),
		jsonTestUpdate(ai.ToolCallStartEvent{ContentIndex: 0, Partial: partialStart}, partialStart),
		jsonTestUpdate(ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"command":`, Partial: partialStart}, partialStart),
		jsonTestUpdate(ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `"expr 20 + 22"}`, Partial: partialFinal}, partialFinal),
		jsonTestUpdate(ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: finalCall, Partial: partialFinal}, partialFinal),
		assistantEnd(""),
	)

	got := eventTypes(t, events)
	want := []string{
		"message_start",
		"message_update:toolcall_start",
		"message_update:toolcall_delta",
		"message_update:toolcall_delta",
		"message_update:toolcall_end",
		"message_end",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence\n got: %v\nwant: %v", got, want)
	}

	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, event := range decoded {
		inner, ok := event["assistantMessageEvent"].(map[string]any)
		if !ok || inner["type"] != "toolcall_end" {
			continue
		}
		call, ok := inner["toolCall"].(map[string]any)
		if !ok {
			t.Fatalf("toolcall_end without toolCall: %s", data)
		}
		if call["id"] != "call-1" || call["name"] != "bash" || call["type"] != "toolCall" {
			t.Fatalf("toolcall_end identity wrong: %s", data)
		}
		args, ok := call["arguments"].(map[string]any)
		if !ok || args["command"] != "expr 20 + 22" {
			t.Fatalf("toolcall_end did not assemble split argument deltas: %s", data)
		}
		return
	}
	t.Fatalf("no toolcall_end emitted: %s", data)
}

type jsonProductionProvider struct {
	mu    sync.Mutex
	calls int
}

func (*jsonProductionProvider) ID() string   { return "json-production-provider" }
func (*jsonProductionProvider) Close() error { return nil }
func (provider *jsonProductionProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	provider.mu.Lock()
	provider.calls++
	callNumber := provider.calls
	provider.mu.Unlock()
	stream := ai.NewAssistantMessageEventStream()
	if callNumber == 1 {
		call := ai.ToolCall{ID: "json-production-call", Name: "json_production_tool", Arguments: ai.JsonObject{"path": "x", "nested": ai.JsonObject{"depth": float64(2)}}}
		partial := &ai.AssistantMessage{Provider: provider.ID(), Model: "json-production-model", Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}
		terminal := &ai.AssistantMessage{Provider: provider.ID(), Model: "json-production-model", Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonToolUse}
		for _, event := range []ai.AssistantMessageEvent{
			ai.StartEvent{Partial: partial},
			ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
			ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"path":"x","nested":{"depth":2}}`, Partial: partial},
			ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial},
			ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: terminal},
		} {
			if err := stream.Push(event); err != nil {
				return nil, err
			}
		}
		return stream, nil
	}
	terminal := &ai.AssistantMessage{Provider: provider.ID(), Model: "json-production-model", StopReason: ai.StopReasonStop}
	if err := stream.Push(ai.StartEvent{Partial: terminal}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: terminal}); err != nil {
		return nil, err
	}
	return stream, nil
}

type jsonProductionTool struct{}

func (jsonProductionTool) Name() string                           { return "json_production_tool" }
func (jsonProductionTool) Label() string                          { return "JSON production tool" }
func (jsonProductionTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (jsonProductionTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "json_production_tool", Parameters: map[string]any{"type": "object"}}
}
func (jsonProductionTool) Execute(_ context.Context, _ string, _ json.RawMessage, update agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	update("working", map[string]any{"progress": float64(1)})
	return agent.AgentToolResult{
		Content: "done",
		Images:  []ai.ImageContent{{Data: "aW1n", MimeType: "image/png"}},
		Details: map[string]any{"nested": map[string]any{"value": "kept"}},
		IsError: true,
	}, nil
}

func TestJSONAndRPCProductionToolEventsMatchPersistedResult(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	provider := &jsonProductionProvider{}
	session, err := coding.NewSession(services, coding.SessionOptions{
		Model: &ai.Model{ID: "json-production-model", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
		Tools: []agent.AgentTool{jsonProductionTool{}}, SkipBuiltinTools: true,
		SessionDir: t.TempDir(), EventBufferSize: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	type wireEvent map[string]any
	var toolEvents []wireEvent
	done := make(chan error, 1)
	go func() {
		for event := range session.Events() {
			converted, err := rpcAgentEvent(event)
			if err != nil {
				done <- err
				return
			}
			for _, value := range converted {
				encoded, _ := json.Marshal(value)
				var decoded wireEvent
				_ = json.Unmarshal(encoded, &decoded)
				if strings.HasPrefix(decoded["type"].(string), "tool_execution_") {
					toolEvents = append(toolEvents, decoded)
				}
			}
			if _, settled := event.(agent.AgentSettledEvent); settled {
				done <- nil
				return
			}
		}
		done <- nil
	}()
	if _, err := session.Send(context.Background(), "run the tool"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("timed out waiting for production JSON/RPC events")
	}

	var persisted *agent.ToolResultMessage
	for _, message := range session.Inner().BuildContext(nil) {
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "json-production-call" {
			copy := *message.ToolResult
			persisted = &copy
			break
		}
	}
	if persisted == nil {
		t.Fatal("persisted ToolResultMessage not found")
	}
	if len(toolEvents) != 3 {
		t.Fatalf("tool events = %#v, want start, update, end", toolEvents)
	}
	startArgs := toolEvents[0]["args"].(map[string]any)
	updateArgs := toolEvents[1]["args"].(map[string]any)
	if !reflect.DeepEqual(startArgs, updateArgs) || updateArgs["path"] != "x" || updateArgs["nested"].(map[string]any)["depth"] != float64(2) {
		t.Fatalf("structured args differ: start=%#v update=%#v", startArgs, updateArgs)
	}
	endResult := toolEvents[2]["result"].(map[string]any)
	persistedWire, err := rpcToolResultMessage(*persisted)
	if err != nil {
		t.Fatal(err)
	}
	persistedMap := persistedWire.(map[string]any)
	if !reflect.DeepEqual(endResult["content"], persistedMap["content"]) || !reflect.DeepEqual(endResult["details"], persistedMap["details"]) || endResult["isError"] != persistedMap["isError"] {
		t.Fatalf("execution end = %#v, persisted ToolResultMessage = %#v", endResult, persistedMap)
	}
}

// A turn with no assistant text must not fabricate an empty text run.
func TestJSONModeNoTextRunWithoutDeltas(t *testing.T) {
	got := eventTypes(t, drive(t,
		assistantStart(),
		assistantEnd(""),
	))
	for _, kind := range got {
		if strings.HasPrefix(kind, "message_update") {
			t.Fatalf("fabricated text run with no deltas: %v", got)
		}
	}
}
