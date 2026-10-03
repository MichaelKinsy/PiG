package ai

import (
	"encoding/json"
	"errors"
	"maps"
	"testing"
)

// mistralLevels builds a ThinkingLevelMap the way the upstream fixtures spread NONE_HIGH_LEVELS: a nil value is `null`.
func mistralLevels(overrides map[ThinkingLevel]*string) ThinkingLevelMap {
	levels := ThinkingLevelMap{ThinkingOff: new("none"), ThinkingMinimal: nil, ThinkingLow: nil, ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}
	maps.Copy(levels, overrides)
	return levels
}

// Ports packages/ai/test/mistral-reasoning-mode.test.ts (0.99.1): models with a thinking level map use reasoning_effort; other reasoning models use prompt_mode.
func TestMistralReasoningModeUpstream(t *testing.T) {
	noneHigh := mistralLevels(nil)
	glm52 := mistralLevels(map[ThinkingLevel]*string{ThinkingMax: new("max")})
	glm53 := mistralLevels(map[ThinkingLevel]*string{ThinkingOff: nil, ThinkingLow: new("low"), ThinkingMax: new("max")})
	// expect maps each payload field the upstream case asserts to its value; "" is toBeUndefined. Fields the upstream case does not assert are not checked.
	type testCase struct {
		name, id  string
		reasoning bool
		levels    ThinkingLevelMap
		thinking  ThinkingLevel
		session   string
		retention CacheRetention
		expect    map[string]string
	}
	cases := []testCase{
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:74
		{name: "uses prompt_mode for reasoning models without a thinking level map (Magistral)", id: "magistral-medium-latest", reasoning: true, thinking: ThinkingMedium, expect: map[string]string{"promptMode": "reasoning", "reasoningEffort": ""}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:81
		{name: "omits reasoning controls for Magistral when thinking is off", id: "magistral-medium-latest", reasoning: true, expect: map[string]string{"promptMode": "", "reasoningEffort": ""}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:114 (#9678)
		{name: "sends max for GLM-5.2", id: "zai-glm-5-2", reasoning: true, levels: glm52, thinking: ThinkingMax, expect: map[string]string{"reasoningEffort": "max"}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:121 (it.each low, high, max)
		{name: "zai-glm-5-3 sends reasoning_effort low", id: "zai-glm-5-3", reasoning: true, levels: glm53, thinking: ThinkingLow, expect: map[string]string{"reasoningEffort": "low", "promptMode": ""}},
		{name: "zai-glm-5-3 sends reasoning_effort high", id: "zai-glm-5-3", reasoning: true, levels: glm53, thinking: ThinkingHigh, expect: map[string]string{"reasoningEffort": "high", "promptMode": ""}},
		{name: "zai-glm-5-3 sends reasoning_effort max", id: "zai-glm-5-3", reasoning: true, levels: glm53, thinking: ThinkingMax, expect: map[string]string{"reasoningEffort": "max", "promptMode": ""}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:128
		{name: "zai-glm-5-3 maps medium to high", id: "zai-glm-5-3", reasoning: true, levels: glm53, thinking: ThinkingMedium, expect: map[string]string{"reasoningEffort": "high"}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:136 (#8700)
		{name: "omits reasoning controls for non-reasoning models", id: "mistral-medium-2505", thinking: ThinkingMedium, expect: map[string]string{"reasoningEffort": "", "promptMode": ""}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:143
		{name: "uses the session id as prompt cache key", id: "mistral-large-latest", session: "session-123", expect: map[string]string{"promptCacheKey": "session-123"}},
		// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:151
		{name: "omits prompt cache key when cache retention is disabled", id: "mistral-large-latest", session: "session-123", retention: CacheRetentionNone, expect: map[string]string{"promptCacheKey": ""}},
	}
	// .upstream/v0.99.1/packages/ai/test/mistral-reasoning-mode.test.ts:89 (describe.each over the three models; #8700, #9375)
	for _, id := range []string{"mistral-small-2603", "mistral-medium-latest", "zai-glm-5-2"} {
		levels := noneHigh
		if id == "zai-glm-5-2" {
			levels = glm52
		}
		cases = append(cases,
			// :92
			testCase{name: id + " uses reasoning_effort when thinking is enabled", id: id, reasoning: true, levels: levels, thinking: ThinkingHigh, expect: map[string]string{"reasoningEffort": "high", "promptMode": ""}},
			// :99
			testCase{name: id + " clamps unsupported levels to a supported effort", id: id, reasoning: true, levels: levels, thinking: ThinkingLow, expect: map[string]string{"reasoningEffort": "high"}},
			// :105
			testCase{name: id + " sends reasoning_effort none when thinking is off", id: id, reasoning: true, levels: levels, expect: map[string]string{"reasoningEffort": "none", "promptMode": ""}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{ID: tc.id, DisplayName: tc.id, Input: []string{"text"}, ThinkingLevelMap: tc.levels, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: tc.reasoning}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}}
			var payload map[string]any
			sentinel := errors.New("payload captured")
			_, err := StreamSimple(t.Context(), m, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", Thinking: tc.thinking, SessionID: tc.session, CacheRetention: tc.retention, OnPayload: func(p any, _ *Model) (any, error) {
				data, e := json.Marshal(p)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(data, &payload); e != nil {
					t.Fatal(e)
				}
				return nil, sentinel
			}})
			if !errors.Is(err, sentinel) || payload == nil {
				t.Fatalf("capture = %v payload=%v", err, payload)
			}
			// OnPayload precedes SDK-to-wire lowering.
			for key, want := range tc.expect {
				got, present := payload[key]
				if want == "" {
					if present {
						t.Errorf("%s = %v, want omitted", key, got)
					}
				} else if got != want {
					t.Errorf("%s = %v, want %s", key, got, want)
				}
			}
		})
	}
}
