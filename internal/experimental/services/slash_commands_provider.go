// Ports packages/coding-agent/src/experimental/services/slash-commands-provider.ts.
package services

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

type registeredSlashCommand struct {
	command SlashCommandContribution
	closed  bool
}

type slashCommandListener struct {
	callback func([]SlashCommandContribution)
	closed   bool
}

// SlashCommandRegistry retains the first live registration for each name in insertion order. Methods and listeners run synchronously on the owning presentation executor; listeners may register, replace, or unsubscribe reentrantly.
type SlashCommandRegistry struct {
	commands   map[string][]*registeredSlashCommand
	order      []string
	listeners  []*slashCommandListener
	publishing int
}

// NewSlashCommandRegistry creates an empty contribution registry.
func NewSlashCommandRegistry() *SlashCommandRegistry { return &SlashCommandRegistry{} }

var slashCommandName = regexp.MustCompile(`^[a-z0-9][a-z0-9:-]*$`)

// Register rejects an invalid name or an existing registration, including a staged replacement.
func (registry *SlashCommandRegistry) Register(command SlashCommandContribution) (func(), error) {
	if err := validateSlashCommand(command); err != nil {
		return nil, err
	}
	if _, exists := registry.commands[command.Name]; exists {
		return nil, fmt.Errorf("Slash command /%s is already registered", command.Name)
	}
	return registry.add(command), nil
}

// Replace stages a same-name contribution until the preceding registration closes.
func (registry *SlashCommandRegistry) Replace(command SlashCommandContribution) (func(), error) {
	if err := validateSlashCommand(command); err != nil {
		return nil, err
	}
	return registry.add(command), nil
}

// List returns a detached snapshot of visible contributions in name insertion order.
func (registry *SlashCommandRegistry) List() []SlashCommandContribution {
	commands := make([]SlashCommandContribution, 0, len(registry.order))
	for _, name := range registry.order {
		commands = append(commands, copySlashCommand(registry.commands[name][0].command))
	}
	return commands
}

// Subscribe hydrates the listener immediately and returns an idempotent unsubscriber.
func (registry *SlashCommandRegistry) Subscribe(callback func([]SlashCommandContribution)) func() {
	listener := &slashCommandListener{callback: callback}
	registry.listeners = append(registry.listeners, listener)
	callback(registry.List())
	return func() {
		listener.closed = true
		registry.compactListeners()
	}
}

func (registry *SlashCommandRegistry) add(command SlashCommandContribution) func() {
	if registry.commands == nil {
		registry.commands = make(map[string][]*registeredSlashCommand)
	}
	entry := &registeredSlashCommand{command: copySlashCommand(command)}
	entry.command.registration = &SlashCommandRegistrationIdentity{marker: 1}
	entries := registry.commands[command.Name]
	if len(entries) == 0 {
		registry.order = append(registry.order, command.Name)
	}
	entries = append(entries, entry)
	registry.commands[command.Name] = entries
	if len(entries) == 1 {
		registry.publish()
	}
	return func() {
		if entry.closed {
			return
		}
		entry.closed = true
		entries := registry.commands[command.Name]
		if len(entries) == 0 || entries[0] != entry {
			return
		}
		for len(entries) > 0 && entries[0].closed {
			entries[0] = nil
			entries = entries[1:]
		}
		if len(entries) == 0 {
			delete(registry.commands, command.Name)
			index := slices.Index(registry.order, command.Name)
			registry.order = slices.Delete(registry.order, index, index+1)
		} else {
			registry.commands[command.Name] = entries
		}
		registry.publish()
	}
}

func validateSlashCommand(command SlashCommandContribution) error {
	if !slashCommandName.MatchString(command.Name) {
		return fmt.Errorf("Invalid slash command name: %s", command.Name)
	}
	return nil
}

func (registry *SlashCommandRegistry) publish() {
	commands := registry.List()
	registry.publishing++
	defer func() { registry.publishing--; registry.compactListeners() }()
	// A JavaScript Set visits listeners added during publication and skips deleted listeners.
	for i := 0; i < len(registry.listeners); i++ {
		listener := registry.listeners[i]
		if !listener.closed {
			snapshot := make([]SlashCommandContribution, len(commands))
			for j, command := range commands {
				snapshot[j] = copySlashCommand(command)
			}
			listener.callback(snapshot)
		}
	}
}

func (registry *SlashCommandRegistry) compactListeners() {
	if registry.publishing == 0 {
		registry.listeners = slices.DeleteFunc(registry.listeners, func(listener *slashCommandListener) bool { return listener.closed })
	}
}

func copySlashCommand(command SlashCommandContribution) SlashCommandContribution {
	if command.Description != nil {
		command.Description = new(*command.Description)
	}
	if command.ArgumentHint != nil {
		command.ArgumentHint = new(*command.ArgumentHint)
	}
	return command
}

// CreateSlashCommandsRuntimeFacet provides the local command registry without activating commands. A nil registry selects a new empty registry, as upstream's omitted argument does.
func CreateSlashCommandsRuntimeFacet(registry *SlashCommandRegistry) chord.Facet {
	if registry == nil {
		registry = NewSlashCommandRegistry()
	}
	return chord.Facet{Id: "@pi/slash-commands-runtime", Setup: func(env *chord.FacetEnvironment) error {
		return chord.ProvideService[SlashCommands](env, SlashCommandsDefinition, registry)
	}}
}
