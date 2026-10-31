package extension

import "github.com/MichaelKinsy/PiG/tui"

// pig divergence (D217): Pi 9376c8d1 (#4748) adds KeybindingHints, keyText and keyHint, to the tool render context and to the message and entry
// render options, so an extension formats the host's configured keybindings without importing a copy of the package that does not see them. Pinned
// Pi has neither. A method reads the host's keybinding registry and theme when it is called, so one value serves every render and a
// ToolRenderContext, MessageRenderOptions or EntryRenderOptions stays comparable and encodes to the same wire JSON as before.

// KeyText is upstream KeybindingHints.keyText: the keys bound to keybinding in the host's registry, formatted for display and joined by "/",
// or "" when none are.
func (ToolRenderContext) KeyText(keybinding string) string { return tui.ActionKeyText(keybinding) }

// KeyHint is upstream KeybindingHints.keyHint: [ToolRenderContext.KeyText] in the dim color followed by description in the muted color.
func (ToolRenderContext) KeyHint(keybinding, description string) string {
	return tui.KeyHint(tui.ActionKeyText(keybinding), description)
}

// KeyText is upstream KeybindingHints.keyText; see [ToolRenderContext.KeyText].
func (MessageRenderOptions) KeyText(keybinding string) string { return tui.ActionKeyText(keybinding) }

// KeyHint is upstream KeybindingHints.keyHint; see [ToolRenderContext.KeyHint].
func (MessageRenderOptions) KeyHint(keybinding, description string) string {
	return tui.KeyHint(tui.ActionKeyText(keybinding), description)
}

// KeyText is upstream KeybindingHints.keyText; see [ToolRenderContext.KeyText].
func (EntryRenderOptions) KeyText(keybinding string) string { return tui.ActionKeyText(keybinding) }

// KeyHint is upstream KeybindingHints.keyHint; see [ToolRenderContext.KeyHint].
func (EntryRenderOptions) KeyHint(keybinding, description string) string {
	return tui.KeyHint(tui.ActionKeyText(keybinding), description)
}
