package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type anthropicRawWire struct {
	body    string
	headers http.Header
}

// anthropicReplacementWire sends replacement as the onPayload value of one Anthropic request and returns what reached the server.
func anthropicReplacementWire(t *testing.T, replacement string) (anthropicRawWire, *AssistantMessage) {
	t.Helper()
	captured := make(chan anthropicRawWire, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		captured <- anthropicRawWire{string(raw), r.Header.Clone()}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-fable-5-1", ProviderID: "anthropic", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{OnPayload: func(any, *Model) (any, error) { return json.RawMessage(replacement), nil }})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	select {
	case wire := <-captured:
		return wire, result
	default:
		return anthropicRawWire{}, result
	}
}

// @anthropic-ai/sdk beta/messages/messages.js:24-58 create(): `{ betas, user_profile_id, workspace_id, ...body }` leaves the body, and a non-null user_profile_id and
// workspace_id become the anthropic-user-profile-id and anthropic-workspace-id headers (as betas becomes anthropic-beta).
func TestAnthropicReplacementUserProfileAndWorkspaceBecomeHeaders(t *testing.T) {
	wire, result := anthropicReplacementWire(t, `{"model":"m","user_profile_id":"up_1","max_tokens":5,"workspace_id":"ws_9","messages":[],"betas":["a"]}`)
	if result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v", result)
	}
	if want := `{"model":"m","max_tokens":5,"messages":[],"stream":true}`; wire.body != want {
		t.Errorf("body = %s\nwant   %s", wire.body, want)
	}
	for header, want := range map[string]string{"anthropic-user-profile-id": "up_1", "anthropic-workspace-id": "ws_9", "anthropic-beta": "a"} {
		if got := wire.headers.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// A null or absent id sends no header (`user_profile_id != null`), and the member still leaves the body.
func TestAnthropicReplacementNullProfileIDsSendNoHeader(t *testing.T) {
	wire, _ := anthropicReplacementWire(t, `{"model":"m","user_profile_id":null,"workspace_id":null,"max_tokens":5}`)
	if want := `{"model":"m","max_tokens":5,"stream":true}`; wire.body != want {
		t.Errorf("body = %s\nwant   %s", wire.body, want)
	}
	for _, header := range []string{"anthropic-user-profile-id", "anthropic-workspace-id"} {
		if _, sent := wire.headers[http.CanonicalHeaderKey(header)]; sent {
			t.Errorf("%s was sent", header)
		}
	}
}

// messages.js:140-156 transformOutputFormat: a truthy output_format leaves its place and becomes output_config.format, merged into an existing
// output_config (which keeps its position) or appended as a new output_config after every other member.
func TestAnthropicReplacementOutputFormatBecomesOutputConfigFormat(t *testing.T) {
	for _, tc := range []struct{ name, payload, body string }{
		{"new output_config goes last", `{"model":"m","output_format":{"type":"json_schema"},"max_tokens":5}`, `{"model":"m","max_tokens":5,"stream":true,"output_config":{"format":{"type":"json_schema"}}}`},
		{"existing output_config keeps its position and members", `{"model":"m","output_config":{"effort":"high"},"output_format":{"type":"json_schema"},"max_tokens":5}`, `{"model":"m","output_config":{"effort":"high","format":{"type":"json_schema"}},"max_tokens":5,"stream":true}`},
		{"a falsy output_config.format is replaced in its position", `{"model":"m","output_config":{"format":null,"effort":"high"},"output_format":{"type":"json_schema"}}`, `{"model":"m","output_config":{"format":{"type":"json_schema"},"effort":"high"},"stream":true}`},
		{"a falsy output_format is left alone", `{"model":"m","output_format":null,"max_tokens":5}`, `{"model":"m","output_format":null,"max_tokens":5,"stream":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, result := anthropicReplacementWire(t, tc.payload)
			if result.StopReason != StopReasonStop {
				t.Fatalf("result = %+v", result)
			}
			if wire.body != tc.body {
				t.Errorf("body = %s\nwant   %s", wire.body, tc.body)
			}
		})
	}
}

// The SDK throws before any request when both spellings carry a format.
func TestAnthropicReplacementWithBothFormatsFailsBeforeTheRequest(t *testing.T) {
	wire, result := anthropicReplacementWire(t, `{"model":"m","output_format":{"type":"json_schema"},"output_config":{"format":{"type":"text"}}}`)
	const want = "Both output_format and output_config.format were provided. Please use only output_config.format (output_format is deprecated)."
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, want) || strings.HasPrefix(result.ErrorMessage, "anthropic: marshal request") {
		t.Fatalf("result = %q %q", result.StopReason, result.ErrorMessage)
	}
	if wire.body != "" {
		t.Errorf("a request was sent: %s", wire.body)
	}
}
