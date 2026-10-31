### Added

- Extensions can format the user's configured keybindings with `keyText()` and `keyHint()` on the tool render context and on the message and entry renderer options, in the Go (`KeyText`, `KeyHint`), Rust (`key_text`, `key_hint`), Python (`key_text`, `key_hint`), Lisp (`tool-render-context-key-text`, `message-render-options-key-hint` and the matching forms for entries) and Node SDKs. `keyText` returns an empty string for a keybinding with no keys. `extensions/sdk-ts` declares `KeybindingHints` for TypeScript extensions.

### Fixed

- A renderer that imported `keyText` or `keyHint` from its own copy of the extension package rendered hints without the key, because that copy did not see the host's keybindings. The hints on the render context and options read the host's keybindings. Carried forward from Pi 9376c8d1 (earendil-works/pi#4748), past the 1.4.0+b3a29f11 pin. <!-- fix: 9376c8d1 -->
