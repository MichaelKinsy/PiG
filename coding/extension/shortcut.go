package extension

import "context"

// KeyID identifies a keyboard shortcut. Mirrors @mariozechner/pi-tui KeyId,
// which is a string (e.g. "ctrl+shift+l", "alt+enter").
type KeyID = string

// ShortcutHandler is invoked when a registered shortcut fires. Mirrors
// upstream `ExtensionAPI.registerShortcut.handler` signature.
//
// ShortcutHandler receives cancellation and per-extension values through the
// Go context. Use [FromContext] to access the extension context.
type ShortcutHandler = func(ctx context.Context) error

// ShortcutOptions is the registration payload for [API.RegisterShortcut].
type ShortcutOptions struct {
	Description string          `json:"description,omitempty"`
	Handler     ShortcutHandler `json:"-"`
}

// ExtensionShortcut mirrors upstream's ExtensionShortcut: the loader's
// view of a registered shortcut, including its source extension.
type ExtensionShortcut struct {
	Shortcut      KeyID           `json:"shortcut"`
	Description   string          `json:"description,omitempty"`
	Handler       ShortcutHandler `json:"-"`
	ExtensionPath string          `json:"extensionPath"`
}
