package coding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestOpenAICompletionsThinkingTokenBudgetUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		compat  map[string]any
		level   ai.ModelThinkingLevel
		budgets *ai.ThinkingBudgets
		max     int
		field   string
		budget  int
		kwargs  map[string]any
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:105
		{name: "sends the configured budget for the requested level", level: ai.ThinkingMedium, budgets: &ai.ThinkingBudgets{Medium: 4096}, field: "thinking_token_budget", budget: 4096},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:110
		{name: "omits the budget when neither the field nor the alias is set", compat: map[string]any{"thinkingFormat": "zai"}, level: ai.ThinkingMedium, budgets: &ai.ThinkingBudgets{Medium: 4096}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:120
		{name: "omits the budget when thinking is off", budgets: &ai.ThinkingBudgets{High: 8192}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:125
		{name: "clamps xhigh and max to the high budget/xhigh", level: ai.ThinkingXHigh, budgets: &ai.ThinkingBudgets{High: 8192}, field: "thinking_token_budget", budget: 8192},
		{name: "clamps xhigh and max to the high budget/max", level: ai.ThinkingMax, budgets: &ai.ThinkingBudgets{High: 8192}, field: "thinking_token_budget", budget: 8192},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:132
		{name: "leaves room for the answer when the budget meets the response ceiling", level: ai.ThinkingHigh, field: "thinking_token_budget", budget: 16384 - 1024},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:137
		{name: "uses the caller max_tokens as the ceiling when it is lower than the model cap", level: ai.ThinkingHigh, budgets: &ai.ThinkingBudgets{High: 8192}, max: 4096, field: "thinking_token_budget", budget: 4096 - 1024},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:146
		{name: "sends thinking_budget when thinkingTokenBudgetField is set", compat: map[string]any{"thinkingFormat": "qwen", "thinkingTokenBudgetField": "thinking_budget"}, level: ai.ThinkingMedium, budgets: &ai.ThinkingBudgets{Medium: 4096}, field: "thinking_budget", budget: 4096},
		{name: "sends thinking_budget_tokens when thinkingTokenBudgetField is set", compat: map[string]any{"thinkingFormat": "qwen", "thinkingTokenBudgetField": "thinking_budget_tokens"}, level: ai.ThinkingMedium, budgets: &ai.ThinkingBudgets{Medium: 4096}, field: "thinking_budget_tokens", budget: 4096},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:158
		{name: "lets thinkingTokenBudgetField win over the boolean alias", compat: map[string]any{"thinkingFormat": "zai", "supportsThinkingTokenBudget": true, "thinkingTokenBudgetField": "thinking_budget"}, level: ai.ThinkingMedium, budgets: &ai.ThinkingBudgets{Medium: 4096}, field: "thinking_budget", budget: 4096},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:171
		{name: "puts the clamped budget in chat_template_kwargs when $var is thinking.budget", compat: map[string]any{"thinkingFormat": "chat-template", "chatTemplateKwargs": map[string]any{"enable_thinking": map[string]any{"$var": "thinking.enabled"}, "thinking_budget": map[string]any{"$var": "thinking.budget"}}}, level: ai.ThinkingHigh, kwargs: map[string]any{"enable_thinking": true, "thinking_budget": float64(16384 - 1024)}},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-thinking-token-budget.test.ts:189
		{name: "omits thinking.budget from chat_template_kwargs when thinking is off", compat: map[string]any{"thinkingFormat": "chat-template", "chatTemplateKwargs": map[string]any{"enable_thinking": map[string]any{"$var": "thinking.enabled"}, "thinking_budget": map[string]any{"$var": "thinking.budget"}}}, kwargs: map[string]any{"enable_thinking": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compat := tc.compat
			if compat == nil {
				compat = map[string]any{"thinkingFormat": "zai", "supportsThinkingTokenBudget": true}
			}
			captured := captureThinkingBudgetRuntime(t, compat, tc.level, tc.budgets, tc.max)
			for _, field := range []string{"thinking_token_budget", "thinking_budget", "thinking_budget_tokens"} {
				value, present := captured[field]
				if field != tc.field {
					if present {
						t.Errorf("unexpected %s=%#v", field, value)
					}
				} else if value != float64(tc.budget) {
					t.Errorf("%s=%#v want=%d", field, value, tc.budget)
				}
			}
			if tc.kwargs != nil && !reflect.DeepEqual(captured["chat_template_kwargs"], tc.kwargs) {
				t.Fatalf("kwargs=%#v want=%#v", captured["chat_template_kwargs"], tc.kwargs)
			}
		})
	}
}

func captureThinkingBudgetRuntime(t *testing.T, compat map[string]any, level ai.ModelThinkingLevel, budgets *ai.ThinkingBudgets, maxTokens int) map[string]any {
	t.Helper()
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n")
	}))
	t.Cleanup(server.Close)
	definition := map[string]any{"id": "zai-org/glm-5.2", "name": "GLM 5.2 (local vLLM)", "api": "openai-completions", "reasoning": true, "input": []string{"text"}, "contextWindow": 262144, "maxTokens": 16384, "compat": compat}
	config := map[string]any{"providers": map[string]any{"local-vllm": map[string]any{"api": "openai-completions", "baseUrl": server.URL + "/v1", "models": []any{definition}}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("local-vllm/zai-org/glm-5.2", services)
	if err != nil {
		t.Fatal(err)
	}
	result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hi")}}}, ai.StreamOptions{APIKey: "test", Thinking: level.ReasoningOption(), ThinkingBudgets: budgets, MaxTokens: maxTokens}).Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result=%#v", result)
	}
	return <-requests
}
