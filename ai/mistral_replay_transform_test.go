//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestMistralReplayUsesSelectedModelAndSharedTransform(t *testing.T) {
	image := ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}
	for _, tc := range []struct {
		name     string
		images   bool
		messages []Message
		want     string
	}{
		{"foreign thinking becomes text", false, []Message{AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "claude", StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reason", ThinkingSignature: "foreign"}, TextContent{Text: "answer"}}}}, `[{"role":"assistant","prefix":false,"content":[{"type":"text","text":"reason"},{"type":"text","text":"answer"}]}]`},
		{"foreign redacted thinking disappears", false, []Message{AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "claude", StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{Thinking: "opaque", Redacted: true}, TextContent{Text: "answer"}}}}, `[{"role":"assistant","prefix":false,"content":[{"type":"text","text":"answer"}]}]`},
		{"same model thinking remains native", false, []Message{AssistantMessage{API: APIMistralConversations, Provider: "custom-mistral", Model: "selected-model", StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reason", ThinkingSignature: "same"}, TextContent{Text: "answer"}}}}, `[{"role":"assistant","prefix":false,"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]},{"type":"text","text":"answer"}]}]`},
		{"nonvision user images collapse to placeholders", false, []Message{UserMessage{Content: UserContentBlocks{image, image, TextContent{Text: "describe"}, image}}}, `[{"role":"user","content":[{"type":"text","text":"(image omitted: model does not support images)"},{"type":"text","text":"describe"},{"type":"text","text":"(image omitted: model does not support images)"}]}]`},
		{"nonvision tool images preserve order", false, []Message{ToolResultMessage{ToolCallID: "abc123456", ToolName: "lookup", Content: []ToolResultMessageContent{image, image, TextContent{Text: "found"}}}}, `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"(tool image omitted: model does not support images)\nfound"}]}]`},
		{"selected vision model retains tool images", true, []Message{ToolResultMessage{ToolCallID: "abc123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}, image}}}, `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"found"},{"type":"image_url","image_url":"data:image/png;base64,aGVsbG8="}]}]`},
		{"same model IDs are not renormalized", false, []Message{AssistantMessage{API: APIMistralConversations, Provider: "custom-mistral", Model: "selected-model", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "abc-123456", Name: "lookup", Arguments: JsonObject{}}}}, ToolResultMessage{ToolCallID: "abc-123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}}}}, `[{"role":"assistant","prefix":false,"tool_calls":[{"id":"abc-123456","type":"function","function":{"name":"lookup","arguments":"{}"},"index":0}]},{"role":"tool","tool_call_id":"abc-123456","name":"lookup","content":[{"type":"text","text":"found"}]}]`},
		{"foreign IDs remain paired", false, []Message{AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "claude", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "abc-123456", Name: "lookup", Arguments: JsonObject{}}}}, ToolResultMessage{ToolCallID: "abc-123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}}}}, `[{"role":"assistant","prefix":false,"tool_calls":[{"id":"abc123456","type":"function","function":{"name":"lookup","arguments":"{}"},"index":0}]},{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"found"}]}]`},
		{"nil tool arguments replay as an empty object", false, []Message{AssistantMessage{API: APIMistralConversations, Provider: "custom-mistral", Model: "selected-model", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "abc123456", Name: "lookup"}}}, ToolResultMessage{ToolCallID: "abc123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}}}}, `[{"role":"assistant","prefix":false,"tool_calls":[{"id":"abc123456","type":"function","function":{"name":"lookup","arguments":"{}"},"index":0}]},{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"found"}]}]`},
		{"nil and empty blocks do not fabricate content", false, []Message{UserMessage{Content: nil}, AssistantMessage{StopReason: StopReasonStop}, UserMessage{Content: UserContentBlocks{TextContent{Text: ""}}}}, `[{"role":"user","content":[{"type":"text","text":""}]}]`},
		{"failed history remains omitted", false, []Message{AssistantMessage{StopReason: StopReasonError, Content: []AssistantContentBlock{TextContent{Text: "bad"}}}, AssistantMessage{StopReason: StopReasonAborted, Content: []AssistantContentBlock{ToolCall{ID: "orphan", Name: "lookup", Arguments: JsonObject{}}}}, UserMessage{Content: UserText("next")}}, `[{"role":"user","content":"next"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Pi mistral-conversations.ts:135-146 transforms the entire transcript using the supplied model before toChatMessages.
			model := &Model{ID: "selected-model", DisplayName: "Selected Model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "custom-mistral", BaseURL: "https://example.test"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}}
			if tc.images {
				model.Input = append(model.Input, "image")
			}
			before, err := json.Marshal(tc.messages)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			options := StreamOptions{APIKey: "fixture", Fetch: &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				return mistralUpstreamSSE(mistralUpstreamTerminal), nil
			})}}
			options.OnPayload = func(_ any, selected *Model) (any, error) {
				if selected.ID != model.ID || selected.DisplayName != model.DisplayName || !reflect.DeepEqual(selected.Input, model.Input) {
					t.Errorf("payload callback lost selected model: %+v", selected)
				}
				return nil, nil
			}
			options.OnResponse = func(_ context.Context, _ ProviderResponse, selected *Model) error {
				if selected.DisplayName != model.DisplayName || !reflect.DeepEqual(selected.Input, model.Input) {
					t.Errorf("response callback lost selected model: %+v", selected)
				}
				return nil
			}
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: tc.messages}), options)
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
			assertCatalogJSON(t, wire["messages"], tc.want)
			after, err := json.Marshal(tc.messages)
			if err != nil || string(after) != string(before) {
				t.Fatalf("retained history changed: before=%s after=%s error=%v", before, after, err)
			}
		})
	}
}

func TestMistralSelectedModelControlsSystemMessageResolution(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		model := &Model{ID: "selected-model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "custom-mistral", Compat: &ModelCompat{SupportsMidConvoSystemMessages: &enabled}}}
		messages := []Message{SystemMessage{Content: SystemText("initial")}, UserMessage{Content: UserText("one")}, SystemMessage{Content: SystemText("update")}, UserMessage{Content: UserText("two")}}
		var roles []string
		captured := errors.New("captured")
		_, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: messages}), StreamOptions{APIKey: "fixture", OnPayload: func(value any, _ *Model) (any, error) {
			for _, message := range value.(mistralRequest).Messages {
				roles = append(roles, message.Role)
			}
			return nil, captured
		}})
		if !errors.Is(err, captured) {
			t.Fatal(err)
		}
		want := []string{"system", "user", "user"}
		if enabled {
			want = []string{"system", "user", "system", "user"}
		}
		if !reflect.DeepEqual(roles, want) {
			t.Fatalf("supportsMidConvoSystemMessages=%t: roles=%v want=%v", enabled, roles, want)
		}
	}
}
