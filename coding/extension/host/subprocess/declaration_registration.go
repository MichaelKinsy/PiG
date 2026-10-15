package subprocess

// Ports packages/coding-agent/src/core/extensions/loader.ts (registerCommand, registerShortcut, registerFlag, registerMessageRenderer, registerEntryRenderer).

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// handleDeclarationRegistration applies the commands, shortcuts, flags, message renderers and entry renderers of a RegisterPayload to the live extension. Pi's loader writes each into the extension's own Map, which every runner getter reads live, so a registration made after load (inside a handler, a command or a timer) takes effect and replaces an earlier one with the same key.
func (h *Host) handleDeclarationRegistration(ctx context.Context, me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var reg RegisterPayload
	if err := json.Unmarshal(call.Args, &reg); err != nil {
		return nil, fmt.Errorf("decode registerDeclaration: %w", err)
	}
	reg.Name = me.config.Name
	if len(reg.Tools) != 0 || len(reg.Handlers) != 0 || len(reg.Providers) != 0 {
		return nil, errors.New("registerDeclaration carries commands, shortcuts, flags and renderers only")
	}
	if err := validateRegisterPayload(me.config.Name, &reg); err != nil {
		return nil, err
	}
	h.mu.Lock()
	registered := h.exts[me.config.Name]
	h.mu.Unlock()
	if registered != me || me.ext == nil {
		return nil, errors.New("extension registration is no longer active")
	}
	built := h.buildExtension(me, &reg)
	for _, decl := range reg.Commands {
		if command, ok := built.Command(decl.Name); ok {
			me.ext.SetRegisteredCommand(decl.Name, command)
		}
	}
	shortcuts := built.RegisteredShortcuts()
	for _, decl := range reg.Shortcuts {
		for _, shortcut := range shortcuts {
			if string(shortcut.Shortcut) == decl.Key {
				me.ext.SetRegisteredShortcut(shortcut.Shortcut, shortcut)
			}
		}
	}
	for _, decl := range reg.Flags {
		if flag, ok := built.RegisteredFlag(decl.Name); ok {
			me.ext.SetRegisteredFlag(decl.Name, flag)
		}
	}
	messages, entries := built.RegisteredRenderers()
	for _, decl := range reg.MessageRenderers {
		if renderer, ok := messages[decl.CustomType]; ok {
			me.ext.SetMessageRenderer(decl.CustomType, renderer)
		}
	}
	for _, decl := range reg.EntryRenderers {
		if renderer, ok := entries[decl.CustomType]; ok {
			me.ext.SetEntryRenderer(decl.CustomType, renderer)
		}
	}
	return &CallResultPayload{}, nil
}
