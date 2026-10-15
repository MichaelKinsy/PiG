# tui ledger rows (lg-help-8): ported renames and rule requests

## Done
- `tui.TuiMainScreen` (was `TUI`), `tui.TUI` (was `Renderer`, the interface Pi calls `TUI`), `TuiMainScreenRenderState` with Pi's field names, `StackEntry` (`StackChild` is its alias), `tui.Key`. The old comments kept the Go-only names "for call-site stability"; the renames are mechanical (gofmt -r) and the tests are unchanged.

## Rule requests
- `StdinBuffer` EventEmitter members (about 45 rows: on/off/once/emit/listeners/..., `[Symbol.captureRejectionSymbol]`): Node `EventEmitter` inheritance with no Go form; Go `StdinBuffer` delivers through its callbacks. Designed out as language mechanics.
- `EditorComponent` members (`getText`, `onSubmit`, `onChange`, `addToHistory`, `insertTextAtCursor`, `getExpandedText`, `setAutocompleteProvider`, `setPaddingX`, `setAutocompleteMaxVisible`, `handleMouse`, `wantsKeyRelease`, `borderColor`): documented in tui/editor_component.go: `Text()`, `SetOnSubmit`, `SetOnChange`, and each optional member is a small interface (`EditorWithHistory`, `EditorWithTextInsertion`, `EditorWithExpandedText`, `EditorWithAutocomplete`, `EditorWithAppearance`) asked for by type assertion. Rule: optional interface member -> optional-method interface; `getText` = `Text`; `setAutocompleteProvider` = `SetAutocomplete`.
- `Focusable.focused`: `Focusable.SetFocused` (tui/tui.go).
- `[Symbol.LAYOUT_NODE]` on HStack/VStack/ScrollView: `LayoutNode()` (method named by the symbol); `[Symbol.VIEWPORT_TUI]`: the marker is `ViewportTUI` interface membership (`IsViewportTUI`).
- `ViewportTUI`/`TuiMainScreen`/`TuiAltScreen`: `fullRedraws` = `FullRedraws()` (tui/tui_mode_debug.go:15); `terminal`, `onDebug`, `wantsKeyRelease`, `hasOverlayEntries`: fields and getters of Pi's concrete classes that Go keeps as unexported members behind the driver API; port with callers when a Go consumer needs them (none today).
- `Keybindings`, `KeybindingDefinitions` (declaration-merging registries of action ids), `TUI_KEYBINDINGS` (= `TUIKeybindings`), `StdinBufferEventMap`, `TuiInputListenerResult`: type-level; `TUIKeybinding` string alias and the `KB*` constants, `AddInputListener`'s result struct.
