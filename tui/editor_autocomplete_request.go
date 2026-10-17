package tui

// Ports packages/tui/src/components/editor.ts

import (
	"context"
	"strings"
)

// requestNativeAutocomplete uses the same ordered worker as extension providers. Synchronous provider callbacks run on the owner; only captured filesystem or awaited work runs off-loop.
func (e *Editor) requestNativeAutocomplete(force, explicit bool) {
	provider := e.autocomplete
	triggers := make([]string, len(e.autocompleteTriggerCharacters))
	for i, r := range e.autocompleteTriggerCharacters {
		triggers[i] = string(r)
	}
	e.requestAutocompleteProvider(&AsyncAutocompleteProvider{
		TriggerCharacters: triggers,
		GetSuggestions: func(ctx context.Context, lines []string, row, col int, force bool) (*AutocompleteSuggestions, error) {
			type query func(context.Context) (*AutocompleteSuggestions, error)
			prepared := make(chan query, 1)
			e.scheduleAsyncApply(func() {
				if ctx.Err() != nil {
					prepared <- nil
					return
				}
				if planner, ok := provider.(AsyncSuggestionPlanner); ok {
					if prefix, task, ok := planner.SuggestionTask(lines, row, col, force); ok {
						prepared <- func(ctx context.Context) (*AutocompleteSuggestions, error) {
							items, err := task(ctx)
							return &AutocompleteSuggestions{Items: items, Prefix: prefix}, err
						}
						return
					}
				}
				var filePrefix string
				var fileTask func(context.Context) []AutocompleteItem
				if files, ok := provider.(AsyncFileSearcher); ok {
					if prefix, task, ok := files.FileSearchTask(lines, row, col); ok {
						filePrefix, fileTask = prefix, task
					}
				}
				result := provider.GetSuggestions(context.Background(), lines, row, col, AutocompleteSuggestionOptions{Force: force})
				prepared <- func(ctx context.Context) (*AutocompleteSuggestions, error) {
					if fileTask == nil {
						return result, nil
					}
					items := fileTask(ctx)
					if result != nil {
						items = append(append([]AutocompleteItem(nil), result.Items...), items...)
						filePrefix = result.Prefix
					}
					return &AutocompleteSuggestions{Items: items, Prefix: filePrefix}, ctx.Err()
				}
			})
			select {
			case run := <-prepared:
				if run == nil {
					return nil, ctx.Err()
				}
				return run(ctx)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}, force, explicit)
}

func bestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	first := -1
	if prefix != "" {
		for i, item := range items {
			if item.Value == prefix {
				return i
			}
			if first < 0 && strings.HasPrefix(item.Value, prefix) {
				first = i
			}
		}
	}
	return max(0, first)
}
