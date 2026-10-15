package ai

import (
	"reflect"
	"testing"
)

// pi-mono #10299. pig divergence (D120): ids generated for calls streamed without one are unique, so two such calls in different turns
// (an OpenAI-compatible server that never sends ids, the same tool and arguments) cannot share an id and pair the wrong tool result.
func TestStreamedToolCallsWithoutIDGetDistinctIDsAcrossTurns(t *testing.T) {
	seen := map[string]bool{}
	for range 3 {
		m := rfRun(t, "openrouter", rfChunk(`{"tool_calls":[{"index":0,"type":"function","function":{"name":"read","arguments":"{}"}}]}`, "tool_calls"))
		for _, block := range m.Content {
			call, ok := block.(ToolCall)
			if !ok {
				continue
			}
			if call.ID == "" || seen[call.ID] {
				t.Fatalf("generated tool call id %q is empty or repeats an earlier turn's (%v)", call.ID, seen)
			}
			seen[call.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("got %d tool calls, want 3", len(seen))
	}
}

// pi-mono #10299 (and the transformMessages fix proposed in its thread). pig divergence (D120): every provider's replay omits a tool call
// with a blank name or an empty id, and its results, so a stored session holding one (including a session stored before ids were generated)
// replays again. Completions, Responses, Anthropic and Mistral reach this through TransformMessages; Google and Bedrock through
// prepareProviderToolFlow directly.
func TestReplayOmitsUnreplayableToolCallsOnEveryProviderPath(t *testing.T) {
	poisoned := func() []Message {
		return []Message{
			UserMessage{Content: UserText("hi")},
			AssistantMessage{StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
				TextContent{Text: "calling"},
				ToolCall{ID: "", Name: "read", Arguments: map[string]any{}},   // stored before ids were generated
				ToolCall{ID: "ghost", Name: " ", Arguments: map[string]any{}}, // blank name
				ToolCall{ID: "", Name: "", Arguments: map[string]any{}},       // neither
				ToolCall{ID: "good", Name: "read", Arguments: map[string]any{"path": "a"}},
			}},
			ToolResultMessage{ToolCallID: "", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "empty id"}}},
			ToolResultMessage{ToolCallID: "ghost", ToolName: "", Content: []ToolResultMessageContent{TextContent{Text: "Tool not found"}}},
			ToolResultMessage{ToolCallID: "good", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "a"}}},
			UserMessage{Content: UserText("again")},
		}
	}
	check := func(t *testing.T, path string, messages []Message) {
		t.Helper()
		var calls, results []string
		for _, message := range messages {
			switch message := message.(type) {
			case AssistantMessage:
				for _, block := range message.Content {
					if call, ok := block.(ToolCall); ok {
						calls = append(calls, call.ID+"/"+call.Name)
					}
				}
			case ToolResultMessage:
				results = append(results, message.ToolCallID)
			}
		}
		if !reflect.DeepEqual(calls, []string{"good/read"}) || !reflect.DeepEqual(results, []string{"good"}) {
			t.Fatalf("%s replays calls %q and results %q, want only good/read and its result", path, calls, results)
		}
	}
	model := &Model{ID: "m", Input: []string{"text"}}
	original := poisoned()
	check(t, "completions/responses", TransformMessages(original, model, nil))
	check(t, "anthropic", TransformMessages(poisoned(), model, func(id string, _ *Model, _ AssistantMessage) string { return normalizeAnthropicToolCallID(id) }))
	retained := poisoned()
	check(t, "google/bedrock", googleTranscriptMessages(NormalizeContext(Context{Messages: retained})))
	if !reflect.DeepEqual(original, poisoned()) || !reflect.DeepEqual(retained, poisoned()) {
		t.Fatal("replay edited the retained transcript")
	}

	out, err := convertCompletionsMessages(TransformMessages(poisoned(), model, nil), completionsConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range out {
		for _, call := range message.ToolCalls {
			if call.ID == "" || call.Function == nil || call.Function.Name == "" {
				t.Fatalf("completions request replays tool call %+v", call)
			}
		}
		if message.Role == "tool" && message.ToolCallID != "good" {
			t.Fatalf("completions request replays tool message %+v", message)
		}
	}
}
