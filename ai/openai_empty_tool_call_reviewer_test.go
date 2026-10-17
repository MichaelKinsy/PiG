package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests from rev-fix-pi-bugs-ai-1 (810cb08cb8), kept on the reconciled D120 design: the generated id is name_<epoch ms>_<counter>
// (Pi's Google adapters, google-generative-ai.ts:195-200) and replay omits a call with no id or no name in prepareProviderToolFlow.
// pi-mono #10299 names both halves of the replay rule: a call with no id or no name is skipped with its results.
// A session stored before the fix holds a named call with id "" whose result also has tool_call_id "";
// pig divergence (D120): replay omits that call as well, so no tool call or tool message carries an empty id.
// With requiresAssistantAfterToolResult, a tool-result group whose results were all omitted adds no bridge message.
func TestOpenAICompletionsReplayOmitsStoredToolCallsWithoutIDOrNameReviewed(t *testing.T) {
	yes := new(true)
	for _, tc := range []struct {
		name   string
		call   ToolCall
		text   string
		compat *OpenAICompat
		roles  []string
	}{
		{"named call without id", ToolCall{ID: "", Name: "read", Arguments: JsonObject{"path": "a"}}, "", nil, []string{"user", "user"}},
		{"call without name", ToolCall{ID: "call_1", Name: "", Arguments: JsonObject{}}, "", nil, []string{"user", "user"}},
		{"named call without id after text", ToolCall{ID: "", Name: "read", Arguments: JsonObject{}}, "checking", nil, []string{"user", "assistant", "user"}},
		{"bridge is not added when every result is omitted", ToolCall{ID: "", Name: "", Arguments: JsonObject{}}, "checking", &OpenAICompat{RequiresAssistantAfterToolResult: yes}, []string{"user", "assistant", "user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := []AssistantContentBlock{}
			if tc.text != "" {
				content = append(content, TextContent{Text: tc.text})
			}
			content = append(content, tc.call)
			transcript := NormalizeContext(Context{Messages: []Message{
				UserMessage{Content: UserText("hi"), Timestamp: 1},
				AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: content},
				ToolResultMessage{ToolCallID: tc.call.ID, ToolName: tc.call.Name, IsError: true, Content: []ToolResultMessageContent{TextContent{Text: "Tool not found"}}, Timestamp: 3},
				UserMessage{Content: UserText("again"), Timestamp: 4},
			}})
			model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1"}, Input: []string{"text"}}
			converted, err := ConvertCompletionsMessages(model, transcript, tc.compat, ConvertCompletionsMessagesOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var roles []string
			for _, message := range converted {
				roles = append(roles, message.Role)
				for _, call := range message.ToolCalls {
					t.Errorf("replayed tool call %+v", call)
				}
			}
			if strings.Join(roles, ",") != strings.Join(tc.roles, ",") {
				encoded, _ := json.Marshal(converted)
				t.Fatalf("roles = %v, want %v: %s", roles, tc.roles, encoded)
			}
		})
	}
}

// pig divergence (D120): the id given to a call streamed without one is unique, so equal calls in two turns of one
// session do not share an id (a provider such as Anthropic rejects a transcript whose tool_use ids repeat).
func TestOpenAICompletionsGeneratedToolCallIDsAreUnique(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"read","arguments":"{}"}}]}}]}` + "\n\n" +
			`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "test-model", APIKey: "test", BaseURL: server.URL})
	seen := map[string]bool{}
	for turn := range 3 {
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		call, ok := result.Content[0].(ToolCall)
		if !ok || call.ID == "" || !strings.HasPrefix(call.ID, "read_") {
			t.Fatalf("turn %d content = %#v", turn, result.Content)
		}
		if seen[call.ID] {
			t.Fatalf("turn %d reused tool call id %q", turn, call.ID)
		}
		seen[call.ID] = true
	}
}
