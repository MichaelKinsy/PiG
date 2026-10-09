package ai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// simpleThinkingCases are packages/ai/src/api/simple-options.ts adjustMaxTokensForThinking driven through the streamSimple of anthropic-messages.ts:928-973 and bedrock-converse-stream.ts:531-580. The model has maxTokens 64000 unless a case says otherwise. Expected values follow the TypeScript: base = options.maxTokens ?? model.maxTokens; adjusted = adjustMaxTokensForThinking(options.maxTokens, model.maxTokens, reasoning, options.thinkingBudgets); the request max_tokens is adjusted.maxTokens; the budget is min(adjusted.thinkingBudget, max(0, maxTokens - 1024)); xhigh and max use the high budget; a custom budget replaces the default for its level.
var simpleThinkingCases = []struct {
	name       string
	modelMax   int
	level      ModelThinkingLevel
	maxTokens  int
	budgets    *ThinkingBudgets
	wantMax    int
	wantBudget int
}{
	{"default high, no cap", 64000, ThinkingHigh, 0, nil, 64000, 16384},
	{"custom high budget", 64000, ThinkingHigh, 0, &ThinkingBudgets{High: 5000}, 64000, 5000},
	{"explicit cap grows by the budget", 64000, ThinkingHigh, 4096, nil, 20480, 16384},
	{"xhigh uses the custom high budget", 64000, ThinkingXHigh, 0, &ThinkingBudgets{High: 3000}, 64000, 3000},
	{"max uses the default high budget", 64000, ThinkingMax, 0, nil, 64000, 16384},
	{"custom medium with a cap", 64000, ThinkingMedium, 3000, &ThinkingBudgets{Medium: 2000}, 5000, 2000},
	{"unrelated custom level keeps the default", 64000, ThinkingLow, 0, &ThinkingBudgets{High: 5000}, 64000, 2048},
	{"model cap leaves no answer room", 2000, ThinkingHigh, 0, nil, 2000, 976},
}

func simpleThinkingOptions(tc int) StreamOptions {
	c := simpleThinkingCases[tc]
	return StreamOptions{APIKey: "key", Thinking: c.level.ReasoningOption(), MaxTokens: c.maxTokens, ThinkingBudgets: c.budgets}
}

func TestAnthropicStreamSimpleThinkingBudgetsMatchPi(t *testing.T) {
	for index, tc := range simpleThinkingCases {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{ID: "claude-budget", DisplayName: "Claude budget", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: tc.modelMax}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true, Compat: &ModelCompat{ForceAdaptiveThinking: new(false)}}}
			captured := errors.New("payload captured")
			var payload struct {
				MaxTokens int `json:"max_tokens"`
				Thinking  struct {
					Type         string `json:"type"`
					BudgetTokens int    `json:"budget_tokens"`
				} `json:"thinking"`
			}
			options := simpleThinkingOptions(index)
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
			if payload.Thinking.Type != "enabled" || payload.MaxTokens != tc.wantMax || payload.Thinking.BudgetTokens != tc.wantBudget {
				t.Fatalf("max_tokens=%d thinking=%+v; want max_tokens=%d budget_tokens=%d", payload.MaxTokens, payload.Thinking, tc.wantMax, tc.wantBudget)
			}
		})
	}
}

func TestBedrockStreamSimpleThinkingBudgetsMatchPi(t *testing.T) {
	for index, tc := range simpleThinkingCases {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0", DisplayName: "Claude budget", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: tc.modelMax}, ProviderMeta: ProviderMetadata{ProviderID: "amazon-bedrock", API: APIBedrockConverseStream, BaseURL: "https://example.invalid", Reasoning: true}}
			var maxTokens int32
			var fields map[string]any
			options := simpleThinkingOptions(index)
			options.APIKey = ""
			options.Env = ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}
			captured := errors.New("payload captured")
			options.OnPayload = func(value any, _ *Model) (any, error) {
				input := value.(*bedrockruntime.ConverseStreamInput)
				maxTokens = *input.InferenceConfig.MaxTokens
				encoded, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(encoded, &fields); err != nil {
					return nil, err
				}
				return nil, captured
			}
			if _, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), options); !errors.Is(err, captured) {
				t.Fatalf("err = %v", err)
			}
			thinking, _ := fields["thinking"].(map[string]any)
			budget, _ := thinking["budget_tokens"].(float64)
			if thinking["type"] != "enabled" || int(maxTokens) != tc.wantMax || int(budget) != tc.wantBudget {
				t.Fatalf("maxTokens=%d thinking=%+v; want maxTokens=%d budget_tokens=%d", maxTokens, thinking, tc.wantMax, tc.wantBudget)
			}
		})
	}
}
