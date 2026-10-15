//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"errors"
	"testing"
)

// A configured budget wins over the default whatever its value (simple-options.ts:82 spreads it over the defaults, google-generative-ai.ts:436 tests !== undefined, bedrock-converse-stream.ts:1301 uses ??), so zero and
// negative budgets are real settings in every provider path, and a literal with only level fields keeps working.
func TestConfiguredThinkingBudgetsWinWhateverTheirValue(t *testing.T) {
	zeroMedium := NewThinkingBudgets(nil, nil, new(0), nil)
	negativeLow := NewThinkingBudgets(nil, new(-5), nil, nil)
	if NewThinkingBudgets(nil, nil, nil, nil) != nil {
		t.Fatal("no configured level must give nil")
	}
	for name, test := range map[string]struct {
		got, want int
	}{
		"zero medium":            {ThinkingBudgetForLevel("medium", zeroMedium), 0},
		"other level default":    {ThinkingBudgetForLevel("high", zeroMedium), 16384},
		"negative low":           {ThinkingBudgetForLevel("low", negativeLow), -5},
		"xhigh reads high":       {ThinkingBudgetForLevel("xhigh", NewThinkingBudgets(nil, nil, nil, new(0))), 0},
		"literal field":          {ThinkingBudgetForLevel("medium", &ThinkingBudgets{Medium: 5000}), 5000},
		"literal zero is unset":  {ThinkingBudgetForLevel("medium", &ThinkingBudgets{}), 8192},
		"openai zero":            {resolveClampedThinkingBudget(ThinkingMedium, zeroMedium, 100000), 0},
		"openai default":         {resolveClampedThinkingBudget(ThinkingMedium, nil, 100000), 8192},
		"openai unknown level":   {resolveClampedThinkingBudget("bogus", NewThinkingBudgets(nil, nil, nil, new(0)), 100000), 0},
		"openai unknown default": {resolveClampedThinkingBudget("bogus", nil, 100000), 16384},
		"openai xhigh":           {resolveClampedThinkingBudget(ThinkingXHigh, NewThinkingBudgets(nil, nil, nil, new(7)), 100000), 7},
	} {
		if test.got != test.want {
			t.Errorf("%s: %d, want %d", name, test.got, test.want)
		}
	}

	var decoded ThinkingBudgets
	if err := json.Unmarshal([]byte(`{"minimal":0,"low":null,"high":-3,"extra":1}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(decoded); string(encoded) != `{"minimal":0,"high":-3}` {
		t.Errorf("ThinkingBudgets JSON round trip = %s, want only the configured levels", encoded)
	}
	if got := ThinkingBudgetForLevel("minimal", &decoded); got != 0 {
		t.Errorf("decoded zero minimal budget = %d", got)
	}
	if got := resolveClampedThinkingBudget(ThinkingLow, NewThinkingBudgets(nil, new(-5), nil, nil), 100000); got != -5 {
		t.Errorf("openai negative low budget = %d", got)
	}

	model := &Model{ID: "gemini-2.5-flash", Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}}
	config, err := buildGeminiThinkingConfig(model, ThinkingMedium, true, zeroMedium)
	if err != nil || config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget != 0 {
		t.Errorf("google zero budget = %#v, %v", config, err)
	}
	config, err = buildGeminiThinkingConfig(model, ThinkingMedium, true, NewThinkingBudgets(nil, nil, nil, new(9)))
	if err != nil || config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget == 0 || *config.ThinkingBudget == 9 {
		t.Errorf("google must use its own medium default when only high is configured: %#v, %v", config, err)
	}

	claude := &Model{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0", Capabilities: ModelCapabilities{MaxOutputTokens: 32768}}
	for name, test := range map[string]struct {
		budgets *ThinkingBudgets
		level   ModelThinkingLevel
		want    int
	}{"zero": {zeroMedium, ThinkingMedium, 0}, "default": {nil, ThinkingMedium, 8192}, "xhigh": {NewThinkingBudgets(nil, nil, nil, new(0)), ThinkingXHigh, 0}} {
		fields := buildBedrockAdditionalFieldsWithBudget(claude, "", StreamOptions{IsReasoning: true, Thinking: ThinkingLevel(test.level), ThinkingBudgets: test.budgets}, nil)
		thinking, _ := fields["thinking"].(map[string]any)
		if thinking["budget_tokens"] != test.want {
			t.Errorf("bedrock %s: budget_tokens %v, want %d (%v)", name, thinking["budget_tokens"], test.want, fields)
		}
	}

	anthropic := &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh, MaxOutputTokens: 32768}}
	result := thinkingToAnthropicConfig(anthropic, 8192, ThinkingMedium, zeroMedium)
	if result == nil || result.Thinking == nil || result.Thinking.BudgetTokens != 1024 || result.MaxTokens != 8192 || !result.floored {
		t.Errorf("anthropic zero budget keeps the base max tokens and floors the budget at 1024 (thinkingBudgetTokens || 1024): %+v", result)
	}
	if result := thinkingToAnthropicConfig(anthropic, 8192, ThinkingMedium, nil); result == nil || result.floored {
		t.Errorf("a default budget is not floored: %+v", result)
	}
}

// anthropic-messages.ts:974 streamSimple passes min(adjusted.thinkingBudget, max(0, maxTokens - 1024)) and buildParams (anthropic-messages.ts:1257) sends thinkingBudgetTokens || 1024, so a configured zero budget is sent as 1024 even when the context-clamped max_tokens leaves less than 1024 tokens of answer room;
// a non-zero budget is still lowered by that clamp.
func TestAnthropicConfiguredZeroBudgetIsFlooredAfterTheContextClamp(t *testing.T) {
	for name, test := range map[string]struct {
		budgets    *ThinkingBudgets
		wantBudget func(maxTokens int) int
	}{
		"zero":    {NewThinkingBudgets(nil, nil, new(0), nil), func(int) int { return 1024 }},
		"nonzero": {NewThinkingBudgets(nil, nil, new(5000), nil), func(maxTokens int) int { return max(0, maxTokens-1024) }},
	} {
		t.Run(name, func(t *testing.T) {
			// A 6000-token window leaves about 1900 output tokens after the 4096-token safety margin, under 2048.
			model := &Model{ID: "claude-budget", DisplayName: "Claude budget", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 6000, MaxOutputTokens: 32768}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true, Compat: &ModelCompat{ForceAdaptiveThinking: new(false)}}}
			captured := errors.New("payload captured")
			var payload struct {
				MaxTokens int `json:"max_tokens"`
				Thinking  struct {
					Type         string `json:"type"`
					BudgetTokens int    `json:"budget_tokens"`
				} `json:"thinking"`
			}
			options := StreamOptions{APIKey: "key", Thinking: ThinkingMedium.ReasoningOption(), ThinkingBudgets: test.budgets}
			options.OnPayload = func(value any, _ *Model) (any, error) {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(encoded, &payload); err != nil {
					return nil, err
				}
				return nil, captured
			}
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			if payload.MaxTokens <= 1024 || payload.MaxTokens >= 2048 {
				t.Fatalf("max_tokens=%d: the case needs a context-clamped max_tokens between 1024 and 2048", payload.MaxTokens)
			}
			if want := test.wantBudget(payload.MaxTokens); payload.Thinking.Type != "enabled" || payload.Thinking.BudgetTokens != want {
				t.Errorf("max_tokens=%d thinking=%+v; want budget_tokens=%d", payload.MaxTokens, payload.Thinking, want)
			}
		})
	}
}
