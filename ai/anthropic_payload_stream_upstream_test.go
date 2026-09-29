package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Pi anthropic-sse-parsing.test.ts's onPayload replacement case and anthropic-messages.ts:578-580 force stream:true after a replacement. The payload observer does not own the transport mode.
func TestAnthropicPayloadReplacementForcesStreamingUpstream(t *testing.T) {
	for _, shape := range []string{"false", "omitted", "null", "typed false", "without betas"} {
		t.Run(shape, func(t *testing.T) {
			captured := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				captured <- request
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":12,\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-fable-5-1", ProviderID: "anthropic", BaseURL: server.URL})
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{OnPayload: func(value any, _ *Model) (any, error) {
				if shape == "typed false" {
					request := value.(anthRequest)
					request.Stream = false
					return request, nil
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				var request map[string]any
				if err := json.Unmarshal(encoded, &request); err != nil {
					return nil, err
				}
				switch shape {
				case "false", "without betas":
					request["stream"] = false
				case "omitted":
					delete(request, "stream")
				case "null":
					request["stream"] = nil
				}
				if shape == "without betas" {
					delete(request, "betas")
				}
				return request, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("stream = %#v", result)
			}
			request := <-captured
			if request["stream"] != true || request["model"] != "claude-fable-5-1" {
				t.Fatalf("replacement wire payload = %#v", request)
			}
		})
	}
}
