package main

import (
	"strings"
	"testing"
)

// Pi 195120a9: the OpenCode providers move to the inference endpoints, opencode-go minimax-m2.7 uses Anthropic Messages,
// and opencode-go deepseek-v4-flash is no longer listed.
func TestCarriedOpenCodeInferenceEndpoints(t *testing.T) {
	rows := []modelRow{
		{Provider: "opencode", ID: "a", API: "openai-completions", BaseURL: "https://opencode.ai/zen/v1", Record: `{"baseUrl":"https://opencode.ai/zen/v1"}`},
		{Provider: "opencode-go", ID: "b", API: "anthropic-messages", BaseURL: "https://opencode.ai/zen/go", Record: `{"baseUrl":"https://opencode.ai/zen/go"}`},
		{Provider: "opencode-go", ID: "deepseek-v4-flash", API: "openai-completions", BaseURL: "https://opencode.ai/zen/go/v1"},
		{Provider: "opencode-go", ID: "minimax-m2.7", API: "openai-completions", BaseURL: "https://opencode.ai/zen/go/v1",
			Compat: &ModelCompat{MaxTokensField: "max_tokens"}, Record: `{"api":"openai-completions","baseUrl":"https://opencode.ai/zen/go/v1","compat":{"maxTokensField":"max_tokens"},"x":1}`},
		{Provider: "other", ID: "c", BaseURL: "https://opencode.ai/zen/v1"},
	}
	got := opencodeInferenceEndpoints(rows)
	if len(got) != 4 {
		t.Fatalf("rows = %d, want 4 (deepseek-v4-flash dropped)", len(got))
	}
	want := []string{"https://opencode.ai/inference/v1", "https://opencode.ai/inference/go", "https://opencode.ai/inference/go", "https://opencode.ai/zen/v1"}
	for i, row := range got {
		if row.BaseURL != want[i] {
			t.Errorf("%s base = %q, want %q", row.ID, row.BaseURL, want[i])
		}
	}
	m := got[2]
	if m.API != "anthropic-messages" || m.Compat != nil || m.Record != `{"api":"anthropic-messages","baseUrl":"https://opencode.ai/inference/go","x":1}` {
		t.Errorf("minimax-m2.7 = %+v", m)
	}
	if again := opencodeInferenceEndpoints(got); len(again) != 4 || again[2].Record != m.Record {
		t.Error("the rule is not a no-op on an already-fixed catalog")
	}
}

// Pi 44f6851b (#9534): OpenRouter's ~anthropic/claude-*-latest aliases use the Anthropic Messages API with adaptive thinking, like the
// versioned Claude models. Chat Completions dropped the thinking signature, so OpenRouter ignored Claude's earlier thinking after a tool call.
func TestCarriedOpenRouterAnthropicLatestMessages(t *testing.T) {
	const completions = `{"type":"chat","id":"~anthropic/claude-opus-latest","name":"Anthropic: Claude Opus Latest","api":"openai-completions","baseUrl":"https://openrouter.ai/api/v1","provider":"openrouter","reasoning":true,"input":["text","image"],"compat":{"supportsDeveloperRole":false,"thinkingFormat":"openrouter","cacheControlFormat":"anthropic"},"inputLimits":{"images":{"resize":{"maxWidth":2000}}}}`
	const messages = `{"type":"chat","id":"~anthropic/claude-opus-latest","name":"Anthropic: Claude Opus Latest","api":"anthropic-messages","baseUrl":"https://openrouter.ai/api","provider":"openrouter","reasoning":true,"input":["text","image"],"compat":{"forceAdaptiveThinking":true},"inputLimits":{"images":{"resize":{"maxWidth":2000}}}}`
	rows := []modelRow{
		{Provider: "openrouter", ID: "~anthropic/claude-opus-latest", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1", Record: completions,
			Compat: &ModelCompat{CacheControlFormat: "anthropic"}},
		{Provider: "openrouter", ID: "~anthropic/claude-fable-latest", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1", Record: strings.ReplaceAll(completions, "opus", "fable")},
		// Not an alias: a versioned id, another provider, a non-Claude ~ alias and a batch id keep their rows.
		{Provider: "openrouter", ID: "anthropic/claude-opus-4.7", API: "anthropic-messages", BaseURL: "https://openrouter.ai/api", Record: "keep"},
		{Provider: "other", ID: "~anthropic/claude-opus-latest", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1", Record: "keep"},
		{Provider: "openrouter", ID: "~anthropic/gpt-latest", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1", Record: "keep"},
		{Provider: "openrouter", ID: "~anthropic/claude-opus-latest:batch", API: "openai-completions", BaseURL: "https://openrouter.ai/api/v1", Record: "keep"},
	}
	catalog := modelCatalog{Chat: rows}
	applyCarriedFixes(&catalog)
	got := catalog.Chat
	if len(got) != len(rows) {
		t.Fatalf("rows = %d, want %d", len(got), len(rows))
	}
	for _, i := range []int{0, 1} {
		m := got[i]
		if m.API != "anthropic-messages" || m.BaseURL != "https://openrouter.ai/api" || m.Compat == nil || m.Compat.ForceAdaptiveThinking == nil || !*m.Compat.ForceAdaptiveThinking {
			t.Errorf("%s = api %q base %q compat %+v", m.ID, m.API, m.BaseURL, m.Compat)
		}
		if m.Compat != nil && (m.Compat.CacheControlFormat != "" || m.Compat.ThinkingFormat != "") {
			t.Errorf("%s keeps the Chat Completions compat: %+v", m.ID, m.Compat)
		}
	}
	if got[0].Record != messages {
		t.Errorf("record = %s\nwant     %s", got[0].Record, messages)
	}
	for _, m := range got[2:] {
		if m.Record != "keep" || m.API == "" || (m.Provider == "openrouter" && m.ID == "~anthropic/claude-opus-latest:batch" && m.API != "openai-completions") {
			t.Errorf("%s/%s was changed: %+v", m.Provider, m.ID, m)
		}
	}
	again := modelCatalog{Chat: got}
	applyCarriedFixes(&again)
	if again.Chat[0].Record != messages || again.Chat[0].BaseURL != "https://openrouter.ai/api" {
		t.Error("the rule is not a no-op on an already-fixed catalog")
	}
}
