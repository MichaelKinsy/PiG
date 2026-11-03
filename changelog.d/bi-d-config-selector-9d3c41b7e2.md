### Fixed

- `pig config` names the key bound to `tui.input.tab` in its "switch mode" hint, as Pi does, instead of always printing `tab`; an unbound action prints no key. In the global scope the selector no longer toggles an item whose scope is `project`, and a toggle updates the first item of the same path and resource type in the current scope's groups too, as Pi's `ResourceList.updateItem` does.
- The extension editor consumes the external-editor key even when no editor handoff is bound, as Pi's `handleInput` does, instead of typing it into the text.
