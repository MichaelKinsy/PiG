package testfixture

import (
	"fmt"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func registerAutocompleteFixture(ext *sdk.Extension) {
	ext.Command("autocomplete-register", "Register retained provider wrappers", func(ctx sdk.Context, _ string) error {
		for _, tag := range []string{"A", "B"} {
			err := ctx.AddAutocompleteProvider(func(ctx sdk.Context, current *sdk.AutocompleteProvider) (*sdk.AutocompleteProvider, error) {
				ctx.Notify("factory:"+tag, "info")
				var calls atomic.Int32
				triggers := []string{"$"}
				if tag == "B" {
					triggers = []string{"#", "$"}
				}
				return &sdk.AutocompleteProvider{
					TriggerCharacters: triggers,
					GetSuggestions: func(ctx sdk.Context, lines []string, line, col int, force bool) (*sdk.AutocompleteSuggestions, error) {
						count := calls.Add(1)
						result, err := current.GetSuggestions(ctx, lines, line, col, force)
						if err != nil || result == nil {
							return result, err
						}
						if tag == "A" {
							items := []sdk.AutocompleteItem{}
							for _, item := range result.Items {
								if item.Value != "drop" {
									item.Label = fmt.Sprintf("%s:%d", item.Value, count)
									items = append(items, item)
								}
							}
							result.Items = items
						} else {
							result.Items = append(result.Items, sdk.AutocompleteItem{Value: "tail", Label: fmt.Sprintf("tail:%d", count)})
						}
						return result, nil
					},
					ApplyCompletion: func(ctx sdk.Context, lines []string, line, col int, item sdk.AutocompleteItem, prefix string) (sdk.AutocompleteCompletion, error) {
						result, err := current.ApplyCompletion(ctx, lines, line, col, item, prefix)
						if err != nil {
							return result, err
						}
						result.Lines[result.CursorLine] += "-" + tag
						result.CursorCol += 2
						return result, nil
					},
					ShouldTriggerFileCompletion: func(ctx sdk.Context, lines []string, line, col int) (bool, error) {
						return current.ShouldTriggerFileCompletion(ctx, lines, line, col)
					},
				}, nil
			})
			if err != nil {
				return err
			}
			ctx.Notify("registered:"+tag, "info")
		}
		return nil
	})
}
