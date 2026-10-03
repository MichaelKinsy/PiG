package tui

import (
	"context"
	"slices"
	"testing"
)

// Pi awaits command.getArgumentCompletions on the trimStart()ed command text (autocomplete.ts:338-392, #10218), so a Promise-returning argument completer also answers after leading whitespace. PiG runs that awaited form through SuggestionTask; the task must split the trimmed text the same way the synchronous path does.
func TestCombinedProviderSuggestionTaskAwaitsArgumentsAfterLeadingWhitespace(t *testing.T) {
	var prefixes []string
	provider := NewCombinedProvider([]SlashCommand{{
		Name: "model",
		AwaitArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
			prefixes = append(prefixes, prefix)
			return []AutocompleteItem{{Value: "sonnet", Label: "sonnet"}}, nil
		},
	}}, t.TempDir(), "")
	for _, line := range []string{"/model son", "  /model son", "\t/model son"} {
		prefixes = nil
		prefix, task, ok := provider.SuggestionTask([]string{line}, 0, len(line), false)
		if !ok || task == nil {
			t.Fatalf("%q: no awaited argument-completion task", line)
		}
		if prefix != "son" {
			t.Fatalf("%q: task prefix = %q, want %q", line, prefix, "son")
		}
		items, err := task(context.Background())
		if err != nil || len(items) != 1 || items[0].Value != "sonnet" {
			t.Fatalf("%q: items = %v, err = %v", line, items, err)
		}
		if !slices.Equal(prefixes, []string{"son"}) {
			t.Fatalf("%q: completer prefixes = %q, want [son]", line, prefixes)
		}
	}
}
