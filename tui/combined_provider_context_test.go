package tui

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func comboValues(s *AutocompleteSuggestions) []string {
	if s == nil {
		return nil
	}
	values := make([]string, len(s.Items))
	for i, item := range s.Items {
		values[i] = item.Value
	}
	return values
}

// autocomplete.ts:304,308,344-345,382: the list is `(SlashCommand | AutocompleteItem)[]`; an entry that has no `name` is named by its `value`.
// Name completion offers it, and an item takes no arguments, so `/<item> ` answers null even when a command of another name has some.
// mutation-checked: matching on Name only (no Value fallback) drops the item from the name list.
func TestCombinedProviderNamesAutocompleteItemsByValue(t *testing.T) {
	p := NewCombinedProvider([]SlashCommand{
		{Name: "model", Description: "Pick a model"},
		{Value: "settings", Label: "settings", Description: "Open settings"},
	}, t.TempDir(), "")
	got := p.GetSuggestions(context.Background(), []string{"/se"}, 0, 3, AutocompleteSuggestionOptions{})
	if want := []string{"settings"}; !slices.Equal(comboValues(got), want) {
		t.Fatalf("/se = %q, want %q", comboValues(got), want)
	}
	if got.Items[0].Label != "settings" || got.Items[0].Description != "Open settings" || got.Prefix != "/se" {
		t.Fatalf("item = %+v prefix %q", got.Items[0], got.Prefix)
	}
	if args := p.GetSuggestions(context.Background(), []string{"/settings x"}, 0, 11, AutocompleteSuggestionOptions{}); args != nil {
		t.Fatalf("an item takes no arguments: %v", args)
	}
}

// autocomplete.ts:314-318,338-339: getSuggestions awaits the argument completions under its `signal`; in Go the awaited branch is the query the editor runs.
// A command's awaited completions run to the end under a live signal; an aborted signal stops the awaited search and surfaces the abort.
func TestCombinedProviderAwaitsArgumentsUnderTheSignal(t *testing.T) {
	p := NewCombinedProvider([]SlashCommand{{Name: "pick", AwaitArgumentCompletions: func(prefix string) ([]AutocompleteItem, error) {
		return []AutocompleteItem{{Value: prefix + "-one", Label: "one"}}, nil
	}}}, t.TempDir(), "")
	got, err := NewAutocompleteQuery(p, []string{"/pick ab"}, 0, 8, false).RunResult(context.Background())
	if err != nil || !slices.Equal(comboValues(got), []string{"ab-one"}) || got.Prefix != "ab" {
		t.Fatalf("awaited arguments = %+v, %v", got, err)
	}
	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := NewAutocompleteQuery(p, []string{"/pick ab"}, 0, 8, false).RunResult(aborted); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("an aborted signal returns the abort, got %+v, %v", got, err)
	}
}

// autocomplete.ts:339: `{ force: true }` skips the slash-command branch, so a `/` buffer completes as a path instead of as a command name.
// mutation-checked: ignoring force answers the command list.
func TestCombinedProviderForceOptionSkipsTheCommandBranch(t *testing.T) {
	p := NewCombinedProvider([]SlashCommand{{Name: "model"}}, t.TempDir(), "")
	plain := p.GetSuggestions(context.Background(), []string{"/mo"}, 0, 3, AutocompleteSuggestionOptions{})
	if !slices.Equal(comboValues(plain), []string{"model"}) {
		t.Fatalf("without force = %+v", plain)
	}
	forced := p.GetSuggestions(context.Background(), []string{"/mo"}, 0, 3, AutocompleteSuggestionOptions{Force: true})
	if slices.Contains(comboValues(forced), "model") {
		t.Fatalf("force must skip the command list, got %q", comboValues(forced))
	}
}
