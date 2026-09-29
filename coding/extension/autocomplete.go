package extension

import (
	"context"
	"errors"
	"slices"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

// SetupAutocompleteProvider folds retained factories over a fresh base and merges their trigger characters in first-seen order.
func SetupAutocompleteProvider(ctx context.Context, base *AutocompleteProvider, factories []AutocompleteProviderFactory) (*AutocompleteProvider, error) {
	provider := base
	triggers := []string{}
	for _, factory := range factories {
		next, err := factory(ctx, provider)
		if err != nil {
			return nil, err
		}
		if next == nil || next.GetSuggestions == nil || next.ApplyCompletion == nil {
			return nil, errors.New("autocomplete factory must return a provider")
		}
		provider = next
		for _, character := range provider.TriggerCharacters {
			if !slices.Contains(triggers, character) {
				triggers = append(triggers, character)
			}
		}
	}
	if len(triggers) > 0 {
		provider.TriggerCharacters = triggers
	}
	return provider, nil
}
