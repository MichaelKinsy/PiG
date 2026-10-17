package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Pi api/mistral-conversations.ts:205-211 (streamSimple): the MistralOptions are `{...base, promptMode: model.reasoning && !effortMap && reasoning ? "reasoning" : undefined,
// reasoningEffort}`, so the raw promptMode and reasoningEffort a caller put in the options never reach the request through streamSimple (only through stream(), :530-531).
// The derived controls are the only ones: prompt mode "reasoning" for a reasoning model without a thinking level map, reasoning effort from the map (default "high") otherwise.
// mutation-checked: letting the raw PromptMode / ReasoningEffort survive StreamSimple (direct_simple.go) fails both subtests.
func TestMistralStreamSimpleSendsOnlyTheDerivedReasoningControls(t *testing.T) {
	capture := func(t *testing.T, model *Model, opts StreamOptions) map[string]any {
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
		stream, err := MistralConversationsAPI().StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), opts)
		if err != nil {
			t.Fatal(err)
		}
		if message := stream.Result(); !strings.Contains(message.ErrorMessage, sentinel.Error()) || payload == nil {
			t.Fatalf("capture ended with %v %q payload=%v", message.StopReason, message.ErrorMessage, payload)
		}
		return payload
	}
	t.Run("no thinking level map: prompt mode is derived, a raw one is dropped", func(t *testing.T) {
		model := mistralPromptModeModel("magistral-medium", true, nil)
		payload := capture(t, model, StreamOptions{Thinking: ThinkingLevel(ThinkingHigh), PromptMode: "raw-mode", ReasoningEffort: "raw-effort"})
		if payload["promptMode"] != "reasoning" {
			t.Errorf("promptMode = %v, want the derived reasoning", payload["promptMode"])
		}
		if _, present := payload["reasoningEffort"]; present {
			t.Errorf("reasoningEffort = %v, want it omitted (no map, raw value dropped)", payload["reasoningEffort"])
		}
		payload = capture(t, model, StreamOptions{PromptMode: "raw-mode"})
		if _, present := payload["promptMode"]; present {
			t.Errorf("without a reasoning level, promptMode = %v, want omitted", payload["promptMode"])
		}
	})
	t.Run("thinking level map: effort is derived, a raw one is dropped", func(t *testing.T) {
		levels := ThinkingLevelMap{ThinkingOff: new("none"), ThinkingHigh: new("high"), ThinkingLow: new("low")}
		model := mistralPromptModeModel("mistral-small-2603", true, levels)
		payload := capture(t, model, StreamOptions{Thinking: ThinkingLevel(ThinkingLow), PromptMode: "raw-mode", ReasoningEffort: "raw-effort"})
		if payload["reasoningEffort"] != "low" {
			t.Errorf("reasoningEffort = %v, want the mapped low", payload["reasoningEffort"])
		}
		if _, present := payload["promptMode"]; present {
			t.Errorf("promptMode = %v, want omitted with a level map", payload["promptMode"])
		}
	})
}
