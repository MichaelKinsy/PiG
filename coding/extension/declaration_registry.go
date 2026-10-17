package extension

import (
	"cmp"
	"maps"
	"slices"
	"sync"
)

// declarationRegistry holds an extension's commands, flags and shortcuts once the Extension is shared: runners, the host and the CLI read it while a late registration or an isolated restart writes it. Pi keeps each in a Map on the extension object (loader.ts registerCommand, registerShortcut, registerFlag), which iterates in insertion order and keeps a replaced key in place.
type declarationRegistry struct {
	mu            sync.RWMutex
	commands      map[string]RegisteredCommand
	commandOrder  []string
	flags         map[string]ExtensionFlag
	flagOrder     []string
	shortcuts     map[KeyID]ExtensionShortcut
	shortcutOrder []KeyID
	// messageRenderers and entryRenderers are keyed by custom type.
	messageRenderers map[string]MessageRenderer
	entryRenderers   map[string]EntryRenderer
	// sourceInfo is the provenance the loader assigned after construction; a command without its own takes it, including one a restart registers.
	sourceInfo SourceInfo
	// commandsVersion counts every write to commands, under mu.
	commandsVersion uint64
}

// CommandsVersion reports a counter that moves with every change to the extension's registered commands, so a reader that derived something from them can tell it is stale. The boolean is false for an extension whose declarations are not yet synchronized: its commands have no version.
func (e *Extension) CommandsVersion() (uint64, bool) {
	state := e.declarationState
	if state == nil {
		return 0, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.commandsVersion, true
}

// InitializeDeclarationRegistry moves the construction fields (Commands, CommandOrder, Flags, FlagOrder, Shortcuts) into the synchronized registry. Call it before the Extension is shared; afterwards the Registered* readers and Set* writers use the registry, and the fields are no longer read or written.
func (e *Extension) InitializeDeclarationRegistry() {
	if e.declarationState != nil {
		return
	}
	state := &declarationRegistry{
		commands:  maps.Clone(e.Commands),
		flags:     maps.Clone(e.Flags),
		shortcuts: maps.Clone(e.Shortcuts),

		messageRenderers: maps.Clone(e.MessageRenderers),
		entryRenderers:   maps.Clone(e.EntryRenderers),
	}
	state.commandOrder = orderedKeys(state.commands, e.CommandOrder)
	state.flagOrder = orderedKeys(state.flags, e.FlagOrder)
	state.shortcutOrder = orderedKeys(state.shortcuts, e.shortcutOrder)
	e.declarationState = state
}

// orderedKeys returns the keys of values in order, each once, then the keys order does not list, sorted.
func orderedKeys[K ~string, V any](values map[K]V, order []K) []K {
	out := make([]K, 0, len(values))
	for _, key := range order {
		if _, ok := values[key]; ok && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	if len(out) != len(values) {
		for _, key := range slices.Sorted(maps.Keys(values)) {
			if !slices.Contains(out, key) {
				out = append(out, key)
			}
		}
	}
	return out
}

// SetRegisteredCommand registers command under name: a new name is appended in registration order and an existing one is replaced in place.
func (e *Extension) SetRegisteredCommand(name string, command RegisteredCommand) {
	state := e.declarationState
	if state == nil {
		if e.Commands == nil {
			e.Commands = map[string]RegisteredCommand{}
		}
		if _, exists := e.Commands[name]; !exists {
			e.CommandOrder = append(e.CommandOrder, name)
		}
		e.Commands[name] = command
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.commands == nil {
		state.commands = map[string]RegisteredCommand{}
	}
	if _, exists := state.commands[name]; !exists {
		state.commandOrder = append(state.commandOrder, name)
	}
	if command.SourceInfo == (SourceInfo{}) {
		command.SourceInfo = state.sourceInfo
	}
	state.commands[name] = command
	state.commandsVersion++
}

// SetRegisteredFlag registers flag under name; the order keeps each name's first registration.
func (e *Extension) SetRegisteredFlag(name string, flag ExtensionFlag) {
	state := e.declarationState
	if state == nil {
		if e.Flags == nil {
			e.Flags = map[string]ExtensionFlag{}
		}
		if _, exists := e.Flags[name]; !exists {
			e.FlagOrder = append(e.FlagOrder, name)
		}
		e.Flags[name] = flag
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.flags == nil {
		state.flags = map[string]ExtensionFlag{}
	}
	if _, exists := state.flags[name]; !exists {
		state.flagOrder = append(state.flagOrder, name)
	}
	state.flags[name] = flag
}

// SetRegisteredShortcut registers shortcut under key: a new key is appended in registration order and an existing one is replaced in place.
func (e *Extension) SetRegisteredShortcut(key KeyID, shortcut ExtensionShortcut) {
	state := e.declarationState
	if state == nil {
		if e.Shortcuts == nil {
			e.Shortcuts = map[KeyID]ExtensionShortcut{}
		}
		if _, exists := e.Shortcuts[key]; !exists {
			e.shortcutOrder = append(e.shortcutOrder, key)
		}
		e.Shortcuts[key] = shortcut
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.shortcuts == nil {
		state.shortcuts = map[KeyID]ExtensionShortcut{}
	}
	if _, exists := state.shortcuts[key]; !exists {
		state.shortcutOrder = append(state.shortcutOrder, key)
	}
	state.shortcuts[key] = shortcut
}

// SetCommandSourceInfo assigns info to every command that has no source info of its own, now and on later registrations and restarts.
func (e *Extension) SetCommandSourceInfo(info SourceInfo) {
	e.InitializeDeclarationRegistry()
	state := e.declarationState
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sourceInfo = info
	state.commandsVersion++
	for name, command := range state.commands {
		if command.SourceInfo == (SourceInfo{}) {
			command.SourceInfo = info
			state.commands[name] = command
		}
	}
}

// ReplaceDeclarations makes the commands, flags, shortcuts and message and entry renderers exactly source's, in source's order, when the owning runtime restarts. Readers holding the Extension see the new declarations.
func (e *Extension) ReplaceDeclarations(source *Extension) {
	commands, flags, shortcuts := source.RegisteredCommands(), source.RegisteredFlags(), source.RegisteredShortcuts()
	messageRenderers, entryRenderers := source.RegisteredRenderers()
	e.InitializeDeclarationRegistry()
	state := e.declarationState
	state.mu.Lock()
	defer state.mu.Unlock()
	state.commandsVersion++
	state.commands = make(map[string]RegisteredCommand, len(commands))
	state.commandOrder = make([]string, 0, len(commands))
	for _, command := range commands {
		if command.SourceInfo == (SourceInfo{}) {
			command.SourceInfo = state.sourceInfo
		}
		state.commands[command.Name] = command
		state.commandOrder = append(state.commandOrder, command.Name)
	}
	state.flags = make(map[string]ExtensionFlag, len(flags))
	state.flagOrder = make([]string, 0, len(flags))
	for _, flag := range flags {
		state.flags[flag.Name] = flag
		state.flagOrder = append(state.flagOrder, flag.Name)
	}
	state.shortcuts = make(map[KeyID]ExtensionShortcut, len(shortcuts))
	state.shortcutOrder = make([]KeyID, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		state.shortcuts[shortcut.Shortcut] = shortcut
		state.shortcutOrder = append(state.shortcutOrder, shortcut.Shortcut)
	}
	state.messageRenderers = messageRenderers
	state.entryRenderers = entryRenderers
}

// Command returns the current command registered under name.
func (e *Extension) Command(name string) (RegisteredCommand, bool) {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		command, ok := state.commands[name]
		return command, ok
	}
	command, ok := e.Commands[name]
	return command, ok
}

// RegisteredCommands returns the commands in registration order, including those registered after load.
func (e *Extension) RegisteredCommands() []RegisteredCommand {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return orderedValues(state.commands, state.commandOrder, func(name string, command *RegisteredCommand) { command.Name = cmp.Or(command.Name, name) })
	}
	return orderedValues(e.Commands, orderedKeys(e.Commands, e.CommandOrder), func(name string, command *RegisteredCommand) { command.Name = cmp.Or(command.Name, name) })
}

// RegisteredFlag returns the current flag registered under name.
func (e *Extension) RegisteredFlag(name string) (ExtensionFlag, bool) {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		flag, ok := state.flags[name]
		return flag, ok
	}
	flag, ok := e.Flags[name]
	return flag, ok
}

// RegisteredFlags returns the flags in the order of each name's first registration.
func (e *Extension) RegisteredFlags() []ExtensionFlag {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return orderedValues(state.flags, state.flagOrder, func(name string, flag *ExtensionFlag) { flag.Name = cmp.Or(flag.Name, name) })
	}
	return orderedValues(e.Flags, orderedKeys(e.Flags, e.FlagOrder), func(name string, flag *ExtensionFlag) { flag.Name = cmp.Or(flag.Name, name) })
}

// RegisteredShortcuts returns the shortcuts in registration order; shortcuts written to the Shortcuts field directly follow in key order.
func (e *Extension) RegisteredShortcuts() []ExtensionShortcut {
	if state := e.declarationState; state != nil {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return orderedValues(state.shortcuts, state.shortcutOrder, func(key KeyID, shortcut *ExtensionShortcut) { shortcut.Shortcut = cmp.Or(shortcut.Shortcut, key) })
	}
	return orderedValues(e.Shortcuts, orderedKeys(e.Shortcuts, e.shortcutOrder), func(key KeyID, shortcut *ExtensionShortcut) { shortcut.Shortcut = cmp.Or(shortcut.Shortcut, key) })
}

// orderedValues returns the values in order; keyed names the value by the key it is registered under when it carries none.
func orderedValues[K comparable, V any](values map[K]V, order []K, keyed func(K, *V)) []V {
	out := make([]V, 0, len(order))
	for _, key := range order {
		if value, ok := values[key]; ok {
			keyed(key, &value)
			out = append(out, value)
		}
	}
	return out
}
