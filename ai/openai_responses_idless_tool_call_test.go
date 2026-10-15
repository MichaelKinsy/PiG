package ai

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// pi-mono #10299 on the Responses path. Pi stores a streamed function_call under `${item.call_id}|${item.id}`
// (openai-responses-shared.ts createSlot). An absent call_id is "undefined" and null is "null", which replay as a non-empty call_id.
// An empty call_id is stored as "|<item id>" and replays as call_id "" for the call and for its output, so every later request fails.
// pig divergence (D129): a call streamed with an empty call_id gets a generated call_id, and a stored "|"-prefixed id is omitted from a
// Responses replay with its result. Every other target replays that call as Pi does.
func TestResponsesToolCallWithoutCallIDDoesNotWedgeReplay(t *testing.T) {
	stream := func(callID string) string {
		item := `{"type":"function_call","id":"fc_1",` + callID + `"name":"read","arguments":""}`
		done := `{"type":"function_call","id":"fc_1",` + callID + `"name":"read","arguments":"{\"a\":1}"}`
		return `data: {"type":"response.output_item.added","output_index":0,"item":` + item + `}` + "\n\n" +
			`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":1}"}` + "\n\n" +
			`data: {"type":"response.output_item.done","output_index":0,"item":` + done + `}` + "\n\n" +
			`data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}` + "\n\n"
	}
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{API: APIOpenAIResponses, ProviderID: "openai"}, Input: []string{"text"}}
	transcriptWith := func(call ToolCall) TranscriptContext {
		message := AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "m", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{call}}
		return NormalizeContext(Context{Messages: []Message{
			UserMessage{Content: UserText("x"), Timestamp: 1}, message,
			ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "r"}}, Timestamp: 3},
			UserMessage{Content: UserText("y"), Timestamp: 4},
		}})
	}
	replay := func(t *testing.T, call ToolCall) []ResponsesInputItem {
		t.Helper()
		items, err := ConvertResponsesMessages(model, transcriptWith(call), map[string]bool{"openai": true}, ConvertResponsesMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	streamed := func(t *testing.T, sse string) ToolCall {
		t.Helper()
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "m")
		(&openAIResponsesProvider{}).parseResponsesSSE(context.Background(), strings.NewReader(sse), builder, nil)
		message := builder.stream.Result()
		call, ok := message.Content[0].(ToolCall)
		if !ok {
			t.Fatalf("content = %#v", message.Content)
		}
		return call
	}
	callIDs := func(items []ResponsesInputItem) []string {
		var ids []string
		for _, item := range items {
			if item.Type == "function_call" || item.Type == "function_call_output" {
				ids = append(ids, item.CallID)
			}
		}
		return ids
	}
	for _, tc := range []struct{ name, member, id string }{
		{"an absent call_id is interpolated as undefined", ``, "undefined|fc_1"},
		{"a null call_id is interpolated as null", `"call_id":null,`, "null|fc_1"},
		{"a call_id is kept", `"call_id":"call_1",`, "call_1|fc_1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := streamed(t, stream(tc.member))
			if call.ID != tc.id {
				t.Fatalf("id = %q, want Pi's %q", call.ID, tc.id)
			}
			want, _, _ := strings.Cut(tc.id, "|")
			if got := callIDs(replay(t, call)); strings.Join(got, ",") != want+","+want {
				t.Fatalf("replayed call_ids = %q, want %q for the call and its output", got, want)
			}
		})
	}
	t.Run("an empty call_id gets a generated call_id", func(t *testing.T) {
		call := streamed(t, stream(`"call_id":"",`))
		if !regexp.MustCompile(`^read_\d{13}_\d+\|fc_1$`).MatchString(call.ID) {
			t.Fatalf("id = %q, want read_<ms>_<n>|fc_1", call.ID)
		}
		callID, _, _ := strings.Cut(call.ID, "|")
		if got := callIDs(replay(t, call)); strings.Join(got, ",") != callID+","+callID {
			t.Fatalf("replayed call_ids = %q, want %q for the call and its output", got, callID)
		}
	})
	t.Run("a stored id without a call part is omitted from a Responses replay with its result", func(t *testing.T) {
		items := replay(t, ToolCall{ID: "|fc_1", Name: "read", Arguments: JsonObject{"a": 1}})
		if got := callIDs(items); len(got) != 0 {
			t.Fatalf("unreplayable call replayed with call_ids %q: %+v", got, items)
		}
		if len(items) != 2 {
			t.Fatalf("items = %+v, want the two user messages", items)
		}
	})
}

// D129 is confined to the Responses replay, which splits the id into call_id and item id. Every other target replays a stored "|fc_1"
// call as Pi does: Chat Completions normalizes it to "_fc_1" (openai-completions.ts:1210-1215 normalizeToolCallId) and Gemini sends
// it as is (google normalizeToolCallId keeps an id when the model does not require one), so the call and its result stay.
func TestResponsesEmptyCallIDOmissionIsConfinedToTheResponsesReplay(t *testing.T) {
	call := ToolCall{ID: "|fc_1", Name: "read", Arguments: JsonObject{"a": 1}}
	messages := []Message{
		UserMessage{Content: UserText("x"), Timestamp: 1},
		AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "gpt", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{call}},
		ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: "r"}}, Timestamp: 3},
		UserMessage{Content: UserText("y"), Timestamp: 4},
	}
	t.Run("chat completions", func(t *testing.T) {
		model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1"}, Input: []string{"text"}}
		converted, err := ConvertCompletionsMessages(model, NormalizeContext(Context{Messages: messages}), nil, ConvertCompletionsMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(converted)
		if !strings.Contains(string(encoded), `"tool_calls":[{"id":"_fc_1"`) || !strings.Contains(string(encoded), `"tool_call_id":"_fc_1"`) {
			t.Fatalf("Chat Completions replay = %s", encoded)
		}
	})
	t.Run("gemini", func(t *testing.T) {
		model := &Model{ID: "gemini-2.5-flash", ProviderMeta: ProviderMetadata{API: APIGoogleGenerativeAI, ProviderID: "google"}, Input: []string{"text"}}
		out := TransformMessages(messages, model, func(id string, _ *Model, _ AssistantMessage) string { return normalizeToolCallId(model.ID, id) })
		if len(out) != 4 {
			t.Fatalf("messages = %+v", out)
		}
		assistant := out[1].(AssistantMessage)
		result := out[2].(ToolResultMessage)
		if len(assistant.Content) != 1 || assistant.Content[0].(ToolCall).ID != "|fc_1" || result.ToolCallID != "|fc_1" {
			t.Fatalf("Gemini replay = %+v / %+v", assistant.Content, result)
		}
	})
}
