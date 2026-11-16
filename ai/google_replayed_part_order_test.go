package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// pi: packages/ai/src/api/google-shared.ts:243-277
//
// convertMessages creates a replayed thinking part as { thought: true, text, thoughtSignature }, a text part as { text, thoughtSignature } and a function call part as
// { functionCall, thoughtSignature }, and JSON.stringify writes the members in that order in the payload the hook receives. (The request on the wire is the SDK's
// lowering of it, which writes the members in the SDK's own order, as the wire oracle compares.)
func TestGoogleReplayedPartsHavePiMemberOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "gemini-2.5-flash", ProviderID: "google", BaseURL: server.URL})
	history := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("go"), Timestamp: 1},
		AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: "gemini-2.5-flash", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "hmm", ThinkingSignature: "QUJDRA=="},
			TextContent{Text: "ok", TextSignature: "QUJDRA=="},
			ToolCall{ID: "c1", Name: "t", Arguments: JsonObject{}, ThoughtSignature: "QUJDRA=="},
		}},
		ToolResultMessage{ToolCallID: "c1", ToolName: "t", Content: []ToolResultMessageContent{TextContent{Text: "done"}}, Timestamp: 3},
	}})
	var payload string
	stream, err := provider.Stream(requestvalidity.Allow(t.Context()), history, StreamOptions{MaxRetries: new(0), OnPayload: func(value any, _ *Model) (any, error) {
		encoded, held, err := MarshalHookPayload(value)
		if err != nil || !held {
			t.Fatalf("held=%v err=%v", held, err)
		}
		payload = string(encoded)
		return nil, nil
	}})
	if err == nil {
		stream.Result()
	}
	for _, want := range []string{
		`{"thought":true,"text":"hmm","thoughtSignature":"QUJDRA=="}`,
		`{"text":"ok","thoughtSignature":"QUJDRA=="}`,
		`{"functionCall":{"name":"t","args":{}},"thoughtSignature":"QUJDRA=="}`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload lacks %s:\n%s", want, payload)
		}
	}
}
