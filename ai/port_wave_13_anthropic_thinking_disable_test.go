package ai

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPortWave13AnthropicThinkingDisablePayload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, thinking, effort string
		level                         ThinkingLevel
	}{
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:113
		{"sends thinking.type=disabled for budget-based reasoning models when thinking is off", "claude-sonnet-4-5", `{"type":"disabled"}`, "", ""},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:120
		{"sends thinking.type=disabled for adaptive reasoning models when thinking is off", "claude-opus-4-6", `{"type":"disabled"}`, "", ""},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:127
		{"sends thinking.type=disabled for Claude Opus 4.8 when thinking is off", "claude-opus-4-8", `{"type":"disabled"}`, "", ""},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:134
		{"omits thinking.type=disabled for Claude Fable 5 when thinking is off", "claude-fable-5", "", "", ""},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:141
		{"uses adaptive thinking for Claude Opus 4.8 when reasoning is enabled", "claude-opus-4-8", `{"type":"adaptive","display":"summarized"}`, "high", ThinkingHigh},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:148
		{"uses adaptive thinking for Claude Sonnet 5 when reasoning is enabled", "claude-sonnet-5", `{"type":"adaptive","display":"summarized"}`, "high", ThinkingHigh},
		// upstream: packages/ai/test/anthropic-thinking-disable.test.ts:155
		{"maps xhigh reasoning to effort=xhigh for Claude Opus 4.8", "claude-opus-4-8", `{"type":"adaptive","display":"summarized"}`, "xhigh", ThinkingXHigh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Upstream uses streamSimple with reasoning omitted in the first four rows, not an explicit off value.
			model := cloneGeneratedModel(t, "anthropic/"+tc.model).ToModel()
			model.ProviderMeta.BaseURL = "http://127.0.0.1:9"
			var captured map[string]json.RawMessage
			stream, _ := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: time.Now().UnixMilli()}}}), StreamOptions{APIKey: "fake-key", Thinking: tc.level, OnPayload: func(value any, _ *Model) (any, error) {
				data, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(data, &captured); err != nil {
					return nil, err
				}
				return nil, errors.New("payload captured")
			}})
			if stream != nil {
				stream.Result()
			}
			if captured == nil {
				t.Fatal("Expected payload to be captured before request failure")
			}
			if tc.thinking == "" {
				if value, present := captured["thinking"]; present {
					t.Fatalf("thinking=%s; want absent", value)
				}
			} else {
				assertShapeJSON(t, captured["thinking"], tc.thinking)
			}
			if tc.effort == "" {
				if value, present := captured["output_config"]; present {
					t.Fatalf("output_config=%s; want absent", value)
				}
			} else {
				assertShapeJSON(t, captured["output_config"], `{"effort":"`+tc.effort+`"}`)
			}
		})
	}
}
