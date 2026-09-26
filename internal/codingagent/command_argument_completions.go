package codingagent

import (
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// commandArgumentCompletions serves extension commands' getArgumentCompletions
// to the editor's synchronous autocomplete provider. Upstream's editor awaits
// the provider and shows its answer when the buffer has not changed since; an
// extension's answer here arrives off the TUI loop, so the first query for an
// argument prefix starts the request and draws nothing, and the answer asks
// the editor to query again, when the prefix is answered from the latest
// result.
type commandArgumentCompletions struct {
	// refresh runs on the TUI loop when an answer arrives.
	refresh func()

	mu    sync.Mutex
	key   string
	items []tui.AutocompleteItem
	ready bool
}

// forCommand returns the editor-side completion callback for one command.
func (c *commandArgumentCompletions) forCommand(name string, complete extension.ArgumentCompletionsFunc) func(string) []tui.AutocompleteItem {
	return func(prefix string) []tui.AutocompleteItem {
		key := name + "\x00" + prefix
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.key == key {
			if c.ready {
				return c.items
			}
			return nil
		}
		c.key, c.items, c.ready = key, nil, false
		go c.fetch(key, prefix, complete)
		return nil
	}
}

func (c *commandArgumentCompletions) fetch(key, prefix string, complete extension.ArgumentCompletionsFunc) {
	items, err := callArgumentCompletions(complete, prefix)
	converted := make([]tui.AutocompleteItem, 0, len(items))
	if err == nil {
		for _, item := range items {
			label := item.Label
			if label == "" {
				label = item.Value
			}
			converted = append(converted, tui.AutocompleteItem{Value: item.Value, Label: label, Description: item.Description})
		}
	}
	c.mu.Lock()
	current := c.key == key
	if current {
		c.items, c.ready = converted, true
	}
	c.mu.Unlock()
	if current && len(converted) > 0 && c.refresh != nil {
		c.refresh()
	}
}

// callArgumentCompletions runs an extension's callback; one that panics has
// no completions, as upstream's provider treats a failed request.
func callArgumentCompletions(complete extension.ArgumentCompletionsFunc, prefix string) (items []extension.AutocompleteItem, err error) {
	defer func() {
		// upstream: packages/tui/src/components/editor.ts:runAutocompleteRequest
		if recover() != nil {
			items, err = nil, nil
		}
	}()
	return complete(prefix)
}

// extensionCommandSlashEntry is an extension command's autocomplete entry.
func (m *InteractiveMode) extensionCommandSlashEntry(command extension.ResolvedCommand) tui.SlashCommand {
	name := strings.TrimPrefix(command.InvocationName, "/")
	entry := tui.SlashCommand{Name: name, Description: command.Description}
	if command.GetArgumentCompletions != nil {
		entry.GetArgumentCompletions = m.argumentCompletions().forCommand(name, command.GetArgumentCompletions)
	}
	return entry
}

func (m *InteractiveMode) argumentCompletions() *commandArgumentCompletions {
	m.argumentCompletionsOnce.Do(func() {
		m.commandArgumentCompletions = &commandArgumentCompletions{refresh: func() {
			m.postUITask(func() {
				if m.editor != nil {
					m.editor.RefreshAutocomplete()
				}
				if m.tuiInst != nil {
					m.tuiInst.RequestRender()
				}
			})
		}}
	})
	return m.commandArgumentCompletions
}
