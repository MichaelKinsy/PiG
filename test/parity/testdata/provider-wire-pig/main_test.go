package main

import (
	"encoding/json"
	"testing"
)

// Pi's native Anthropic stream preserves maxTokens and defaults thinkingBudgetTokens to 1024 (packages/ai/src/api/anthropic-messages.ts:1072,1175). Only streamSimple adds a reasoning budget to the output cap (:889-904).
func TestAnthropicProbeUsesNativeThinkingOptions(t *testing.T) {
	result := probeAnthropic().(map[string]any)
	var body struct {
		MaxTokens int `json:"max_tokens"`
		Thinking  struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
			Display      string `json:"display"`
		} `json:"thinking"`
		Temperature *float64 `json:"temperature"`
	}
	if err := json.Unmarshal(result["wireBody"].(json.RawMessage), &body); err != nil {
		t.Fatal(err)
	}
	if body.MaxTokens != 123 || body.Thinking.Type != "enabled" || body.Thinking.BudgetTokens != 1024 || body.Thinking.Display != "summarized" || body.Temperature != nil {
		t.Fatalf("native stream wire body = %+v; want max_tokens=123, enabled thinking budget=1024, summarized display, no temperature", body)
	}
}
