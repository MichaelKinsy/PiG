package codingagent

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// callArgumentCompletions converts a Go callback panic into a surfaced callback error.
func callArgumentCompletions(complete extension.ArgumentCompletionsFunc, prefix string) (items []extension.AutocompleteItem, err error) {
	defer func() {
		if value := recover(); value != nil {
			items = nil
			err = fmt.Errorf("autocomplete callback panicked: %v", value)
		}
	}()
	return complete(prefix)
}

// extensionCommandSlashEntry retains the command's awaited callback. Its first result is real, not a cache-miss stand-in followed by a second query.
func (m *InteractiveMode) extensionCommandSlashEntry(command extension.ResolvedCommand) tui.SlashCommand {
	entry := tui.SlashCommand{Name: strings.TrimPrefix(command.InvocationName, "/"), Description: command.Description}
	if command.GetArgumentCompletions != nil {
		entry.AwaitArgumentCompletions = func(prefix string) ([]tui.AutocompleteItem, error) {
			items, err := callArgumentCompletions(command.GetArgumentCompletions, prefix)
			if err != nil {
				return nil, err
			}
			converted := make([]tui.AutocompleteItem, len(items))
			for i, item := range items {
				label := item.Label
				if label == "" {
					label = item.Value
				}
				converted[i] = tui.AutocompleteItem{Value: item.Value, Label: label, Description: item.Description}
			}
			return converted, nil
		}
	}
	return entry
}
