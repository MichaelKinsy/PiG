//go:build !pig_strip_mistral_conversations

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

// Ports MistralOptions.promptMode (packages/ai/src/api/mistral-conversations.ts:38, :530, :384): the raw option is independent of the
// provider-neutral thinking level. `if (options?.promptMode) payload.promptMode = options.promptMode`, and the wire payload remaps promptMode
// to prompt_mode.
func TestMistralPromptModeOptionUpstream(t *testing.T) {
	model := func(baseURL string) *Model {
		return &Model{ID: "magistral-medium-latest", DisplayName: "magistral-medium-latest", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: baseURL, Reasoning: true}}
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}})

	capture := func(t *testing.T, promptMode string) map[string]any {
		t.Helper()
		var payload map[string]any
		sentinel := errors.New("payload captured")
		stream, err := MistralConversationsAPI().Stream(t.Context(), model("http://127.0.0.1:1"), transcript, StreamOptions{APIKey: "fake-key", PromptMode: promptMode, OnPayload: func(p any, _ *Model) (any, error) {
			data, e := json.Marshal(p)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(data, &payload); e != nil {
				t.Fatal(e)
			}
			return nil, sentinel
		}})
		if err != nil {
			t.Fatal(err)
		}
		if message := stream.Result(); !strings.Contains(message.ErrorMessage, sentinel.Error()) {
			t.Fatalf("capture ended with %v %q", message.StopReason, message.ErrorMessage)
		}
		return payload
	}

	t.Run("a set promptMode reaches the payload without any thinking level", func(t *testing.T) {
		payload := capture(t, "reasoning")
		if payload["promptMode"] != "reasoning" {
			t.Errorf("promptMode = %v, want reasoning", payload["promptMode"])
		}
		if _, ok := payload["reasoningEffort"]; ok {
			t.Errorf("reasoningEffort = %v, want it omitted", payload["reasoningEffort"])
		}
	})
	t.Run("an empty promptMode is left out", func(t *testing.T) {
		if payload := capture(t, ""); payload["promptMode"] != nil {
			t.Errorf("promptMode = %v, want omitted", payload["promptMode"])
		}
	})
	t.Run("the wire payload names it prompt_mode", func(t *testing.T) {
		var wire map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &wire)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		stream, err := MistralConversationsAPI().Stream(t.Context(), model(srv.URL), transcript, StreamOptions{APIKey: "fake-key", PromptMode: "reasoning"})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		if wire["prompt_mode"] != "reasoning" {
			t.Errorf("prompt_mode = %v in %v, want reasoning", wire["prompt_mode"], wire)
		}
		if _, ok := wire["promptMode"]; ok {
			t.Errorf("the wire payload still has promptMode: %v", wire)
		}
	})
}
