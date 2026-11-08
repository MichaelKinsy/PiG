package models

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
)

// Ports the resolveModelScope assertions of test/p2/chat-contracts.test.ts ("enabledModels: patterns resolve to the
// scope"); the saving half runs against the server in the e2e package.
func TestResolveModelScope(t *testing.T) {
	models := []contract.ModelInfo{
		{Provider: "anthropic", ModelID: "claude-sonnet-4-5", Name: "S", ContextWindow: 1},
		{Provider: "anthropic", ModelID: "claude-opus-4-5", Name: "O", ContextWindow: 1},
		{Provider: "openrouter", ModelID: "qwen:exacto", Name: "Q", ContextWindow: 1},
	}
	got := ResolveModelScope([]string{"*sonnet*:high", "anthropic/claude-opus-4-5", "qwen:exacto", "nope"}, models)
	want := ScopeResult{
		Models: []ScopedModel{
			{Provider: "anthropic", ModelID: "claude-sonnet-4-5", ThinkingLevel: "high"},
			{Provider: "anthropic", ModelID: "claude-opus-4-5"},
			{Provider: "openrouter", ModelID: "qwen:exacto"},
		},
		Unmatched: []string{"nope"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %+v, want %+v", got, want)
	}
	if n := len(ResolveModelScope([]string{"claude-*", "anthropic/*"}, models).Models); n != 2 {
		t.Fatalf("no duplicates: got %d models, want 2", n)
	}
	// A model ID with a colon keeps it unless the suffix is a level; globs are case-insensitive over either form.
	if got := ResolveModelScope([]string{"QWEN:EXACTO", "OpenRouter/Q*:low"}, models).Models; len(got) != 1 || got[0].ThinkingLevel != "" {
		t.Fatalf("the exact reference came first and kept its position: %+v", got)
	}
	if got := ResolveModelScope([]string{"anthropic/claude-*-4-5:xhigh"}, models).Models; len(got) != 2 || got[0].ThinkingLevel != "xhigh" {
		t.Fatalf("a glob with a level: %+v", got)
	}
	if got := ResolveModelScope(nil, models); len(got.Models) != 0 || len(got.Unmatched) != 0 {
		t.Fatalf("no patterns: %+v", got)
	}
}
