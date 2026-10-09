package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func capturedMistralPayload(t *testing.T, model *Model, opts StreamOptions) map[string]any {
	t.Helper()
	var payload map[string]any
	sentinel := errors.New("payload captured")
	opts.APIKey = "fake-key"
	opts.OnPayload = func(p any, _ *Model) (any, error) {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		return nil, sentinel
	}
	// The raw API entry point: mistral-conversations.ts streamSimple builds its MistralOptions from the base options and the reasoning level only,
	// so a raw promptMode reaches the payload through stream(), not streamSimple().
	stream, err := MistralConversationsAPI().Stream(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), opts)
	if err != nil {
		t.Fatal(err)
	}
	if message := stream.Result(); !strings.Contains(message.ErrorMessage, sentinel.Error()) || payload == nil {
		t.Fatalf("capture ended with %v %q payload=%v", message.StopReason, message.ErrorMessage, payload)
	}
	return payload
}

func mistralPromptModeModel(id string, reasoning bool, levels ThinkingLevelMap) *Model {
	return &Model{ID: id, DisplayName: id, Input: []string{"text"}, ThinkingLevelMap: levels, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: reasoning}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}}
}

// .upstream/current/packages/ai/src/api/mistral-conversations.ts:530 (buildPayload): `if (options?.promptMode) payload.promptMode =
// options.promptMode`. The raw option is sent whenever it is set, whatever the model's reasoning flag, and next to a
// reasoningEffort; an unset option sends nothing.
func TestMistralRawPromptModeOptionIsSentIndependentlyOfReasoningControls(t *testing.T) {
	// Not a reasoning model: Pi's derived controls are absent, the raw option still reaches the payload.
	payload := capturedMistralPayload(t, mistralPromptModeModel("mistral-medium-2505", false, nil), StreamOptions{PromptMode: "reasoning"})
	if payload["promptMode"] != "reasoning" {
		t.Fatalf("promptMode = %v, want reasoning for a non-reasoning model", payload["promptMode"])
	}

	// The raw reasoningEffort option is sent next to the raw promptMode (line 530 then line 531).
	levels := ThinkingLevelMap{ThinkingOff: new("none"), ThinkingHigh: new("high")}
	payload = capturedMistralPayload(t, mistralPromptModeModel("mistral-small-2603", true, levels), StreamOptions{PromptMode: "reasoning", ReasoningEffort: "high"})
	if payload["promptMode"] != "reasoning" || payload["reasoningEffort"] != "high" {
		t.Fatalf("promptMode=%v reasoningEffort=%v, want reasoning and high", payload["promptMode"], payload["reasoningEffort"])
	}

	// Unset: nothing is sent.
	payload = capturedMistralPayload(t, mistralPromptModeModel("mistral-medium-2505", false, nil), StreamOptions{})
	if _, present := payload["promptMode"]; present {
		t.Fatalf("promptMode = %v, want omitted", payload["promptMode"])
	}
}
