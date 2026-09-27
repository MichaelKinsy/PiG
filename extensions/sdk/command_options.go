package sdk

import (
	"encoding/json"
	"fmt"
)

// AutocompleteItem mirrors @earendil-works/pi-tui AutocompleteItem: Value is
// inserted, Label is shown in place of Value when set.
type AutocompleteItem struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// ArgumentCompletionsFunc mirrors upstream RegisteredCommand
// getArgumentCompletions: the items for the text after "/<command> ", or nil
// for none.
type ArgumentCompletionsFunc func(argumentPrefix string) ([]AutocompleteItem, error)

// CommandOptions mirrors upstream registerCommand's options: the
// description, getArgumentCompletions and handler.
type CommandOptions struct {
	Description            string
	GetArgumentCompletions ArgumentCompletionsFunc
	Handler                CommandFunc
}

// RegisterCommand registers a slash command with upstream's options, as
// pi.registerCommand(name, options) does.
func (e *Extension) RegisterCommand(name string, options CommandOptions) {
	e.commands = append(e.commands, cmdDef{
		Name:                name,
		Description:         options.Description,
		ArgumentCompletions: options.GetArgumentCompletions != nil,
	})
	e.commandFuncs[name] = options.Handler
	if options.GetArgumentCompletions != nil {
		e.commandCompletions[name] = options.GetArgumentCompletions
	}
}

// commandArgumentCompletions answers the host's command_argument_completions
// request: the items as a JSON array, or null for none.
func (e *Extension) commandArgumentCompletions(name string, rawPrefix json.RawMessage) (any, error) {
	complete, ok := e.commandCompletions[name]
	if !ok {
		return nil, fmt.Errorf("command %s has no getArgumentCompletions", name)
	}
	var prefix string
	if len(rawPrefix) > 0 {
		_ = json.Unmarshal(rawPrefix, &prefix)
	}
	items, err := complete(prefix)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return items, nil
}
