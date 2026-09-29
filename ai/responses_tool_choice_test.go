package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesToolChoiceUnionAtPayloadAndWire(t *testing.T) {
	t.Parallel()
	// The function choice/context is copied from gate-close-03's TestAzureAndOpenAIResponsesObjectToolChoice and 11-azure-options probe.
	// packages/ai/src/api/{azure-openai-responses.ts:323,openai-responses.ts:340}: forward every supplied tool choice, not only strings. The request tool list is independent of the choice.
	for _, api := range []API{APIAzureOpenAIResponses, APIOpenAIResponses} {
		for _, tc := range []struct {
			name   string
			choice any
			want   string
		}{
			{"omitted", nil, ""},
			{"auto", "auto", `"auto"`},
			{"none", "none", `"none"`},
			{"required", "required", `"required"`},
			{"function", map[string]string{"type": "function", "name": "read"}, `{"type":"function","name":"read"}`},
			{"custom", map[string]string{"type": "custom", "name": "read"}, `{"type":"custom","name":"read"}`},
			{"allowed-tools", map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]string{"type": "function", "name": "read"}}}, `{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"read"}]}`},
			{"raw-object", json.RawMessage(`{"type":"function","name":"read"}`), `{"type":"function","name":"read"}`},
			// Pi tests presence (not truthiness), even for a JS caller outside the declared string union.
			{"empty-string", "", `""`},
		} {
			t.Run(string(api)+"/"+tc.name, func(t *testing.T) {
				requests := make(chan map[string]json.RawMessage, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					requests <- payload
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
				}))
				t.Cleanup(server.Close)
				var provider Provider
				if api == APIAzureOpenAIResponses {
					provider = NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "test-deployment", APIKey: "test-key", BaseURL: server.URL + "/openai/v1"})
				} else {
					provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "test-model", ProviderID: "custom-provider", APIKey: "test-key", BaseURL: server.URL + "/v1"})
				}
				defer func() { _ = provider.Close() }()
				transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Summarize this"), Timestamp: 1}}, Tools: []ToolSchema{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}})
				var captured map[string]json.RawMessage
				stream, err := provider.Stream(t.Context(), transcript, StreamOptions{ToolChoice: tc.choice, OnPayload: func(value any, _ *Model) (any, error) {
					data, err := json.Marshal(value)
					if err != nil {
						return nil, err
					}
					return nil, json.Unmarshal(data, &captured)
				}})
				if err != nil {
					t.Fatal(err)
				}
				if result := stream.Result(); result.StopReason != StopReasonStop {
					t.Fatalf("result=%+v", result)
				}
				var wire map[string]json.RawMessage
				select {
				case wire = <-requests:
				default:
					t.Fatal("request never reached transport")
				}
				for _, payload := range []map[string]json.RawMessage{captured, wire} {
					if tc.want == "" {
						if value, exists := payload["tool_choice"]; exists {
							t.Fatalf("tool_choice=%s; want absent", value)
						}
					} else {
						assertShapeJSON(t, payload["tool_choice"], tc.want)
					}
					var tools []struct{ Name string }
					if err := json.Unmarshal(payload["tools"], &tools); err != nil {
						t.Fatal(err)
					}
					if len(tools) != 1 || tools[0].Name != "read" {
						t.Fatalf("tools=%s", payload["tools"])
					}
				}
			})
		}
	}
}

func BenchmarkResponsesToolChoicePayload(b *testing.B) {
	var messages []Message
	for range 64 {
		messages = append(messages, UserMessage{Content: UserText(strings.Repeat("question ", 32))}, AssistantMessage{API: APIOpenAIResponses, Provider: "openai", Model: "test-model", StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: strings.Repeat("answer ", 128)}}})
	}
	transcript := NormalizeContext(Context{Messages: messages, Tools: []ToolSchema{{Name: "read", Description: "Read", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}}}})
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "test-model", APIKey: "test-key", BaseURL: "http://127.0.0.1:9/v1"})
	defer func() { _ = provider.Close() }()
	captured := errors.New("payload captured")
	options := StreamOptions{ToolChoice: map[string]string{"type": "function", "name": "read"}, OnPayload: func(value any, _ *Model) (any, error) {
		if _, err := json.Marshal(value); err != nil {
			return nil, err
		}
		return nil, captured
	}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := provider.Stream(b.Context(), transcript, options); !errors.Is(err, captured) {
			b.Fatal(err)
		}
	}
}
