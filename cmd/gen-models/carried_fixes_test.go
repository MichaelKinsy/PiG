package main

import "testing"

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
