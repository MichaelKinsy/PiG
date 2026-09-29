package ai

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMistralContentWhitespaceMatchesECMAScript(t *testing.T) {
	// Pi's String.trim removes U+FEFF but preserves U+0085. Assistant blocks only test emptiness; tool results trim their text.
	for _, tc := range []struct{ name, kind, text, want string }{
		{"assistant NEL", "text", "\u0085", `[{"role":"assistant","prefix":false,"content":[{"type":"text","text":"\u0085"}]}]`},
		{"assistant BOM", "text", "\ufeff", `[]`},
		{"thinking NEL", "thinking", "\u0085", `[{"role":"assistant","prefix":false,"content":[{"type":"thinking","thinking":[{"type":"text","text":"\u0085"}]}]}]`},
		{"tool NEL", "tool", "\u0085", `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"\u0085"}]}]`},
		{"tool BOM", "tool", "\ufeff", `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"(no tool output)"}]}]`},
		{"tool strips BOM boundaries", "tool", " \ufeffvalue\ufeff ", `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"value"}]}]`},
		{"tool preserves NEL boundaries", "tool", " \u0085value\u0085 ", `[{"role":"tool","tool_call_id":"abc123456","name":"lookup","content":[{"type":"text","text":"\u0085value\u0085"}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var content AssistantContentBlock = TextContent{Text: tc.text}
			if tc.kind == "thinking" {
				content = ThinkingContent{Thinking: tc.text}
			}
			var message Message = AssistantMessage{API: APIMistralConversations, Provider: "mistral", Model: "model", Content: []AssistantContentBlock{content}, StopReason: StopReasonStop, Timestamp: 1}
			if tc.kind == "tool" {
				message = ToolResultMessage{ToolCallID: "abc123456", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: tc.text}}, Timestamp: 1}
			}
			var payload map[string]any
			provider := NewMistralProvider(MistralConfig{APIKey: "fixture", Model: "model"})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{message}}), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(request *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				return mistralUpstreamSSE(mistralUpstreamTerminal), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatal(result)
			}
			assertCatalogJSON(t, payload["messages"], tc.want)
		})
	}
}

func TestMistralSSEWhitespaceAndEmptyEvents(t *testing.T) {
	const event = `{"choices":[{"delta":{},"finish_reason":"stop"}]}`
	terminal := "data: " + event + "\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name, body string
		want       StopReason
	}{
		{"BOM before JSON", "data: \ufeff" + event + "\n\n", StopReasonStop},
		{"NEL before JSON remains invalid", "data: \u0085" + event + "\n\n", StopReasonError},
		{"event without data", "event: keepalive\n\n" + terminal, StopReasonStop},
		{"empty data", "data:\n\n" + terminal, StopReasonStop},
		{"BOM-only data", "data: \ufeff\n\n" + terminal, StopReasonStop},
		{"NEL-only data remains invalid", "data: \u0085\n\n" + terminal, StopReasonError},
		{"trimStart for each data line", "data: {\"choices\":\ndata: \ufeff[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", StopReasonStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
				return mistralUpstreamSSE(tc.body), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != tc.want {
				t.Fatalf("result=%+v want=%s", result, tc.want)
			}
		})
	}
}

func TestMistralHTTPErrorWhitespaceAndStatusText(t *testing.T) {
	for _, tc := range []struct{ name, body, status, want string }{
		{"BOM", " \ufeffblocked\ufeff ", "403 Forbidden", "Mistral API error (403): blocked"},
		{"NEL", "\u0085blocked\u0085", "403 Forbidden", "Mistral API error (403): \u0085blocked\u0085"},
		{"empty custom status", "", "403 Custom denied", "Mistral API error (403): Custom denied"},
		{"empty status", "", "", "Mistral API error (403): Request failed with status 403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), StreamOptions{Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
				response := mistralUpstreamSSE(tc.body)
				response.StatusCode, response.Status = 403, tc.status
				return response, nil
			})}})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

func BenchmarkMistralEventData(b *testing.B) {
	for _, tc := range []struct{ name, input, want string }{
		{"ordinary", `{"choices":[]}`, `{"choices":[]}`},
		{"BOM", "\ufeff{\"choices\":[]}\ufeff", `{"choices":[]}`},
		{"multiline", "{\"choices\":\n\ufeff[]}", "{\"choices\":\n[]}"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got := mistralEventData(tc.input); got != tc.want {
					b.Fatalf("data=%q want=%q", got, tc.want)
				}
			}
		})
	}
}
