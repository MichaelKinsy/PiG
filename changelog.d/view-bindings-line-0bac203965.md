### Fixed

- A view's `title`, its `key` and the `name` of a key binding could hold text that breaks the line a client writes it on or that passes for another label. The host now answers `invalid` for one that holds a control character, a line separator or a control that reorders the text around it (U+202A to U+202E and U+2066 to U+2069). The direction marks of Arabic and Hebrew text (U+200E, U+200F, U+061C) are accepted. <!-- fix: 8e93ec85d6 -->
- An extension can bind `+`: `+` and `ctrl++` are key ids, as in Pi. The host refused them. <!-- fix: 8e93ec85d6 -->
- In the terminal, a key your keybindings give to interrupt, clear, exit or suspend no longer raises an extension's binding, also after you change `keybindings.json` and run `/reload`. The extension reads one notice for such a binding. <!-- fix: 8e93ec85d6 -->
- A Go program that mounts a view through `uiview.Registry` with a record it built itself is now held to the same checks as an extension: `Registry.Mount` and `Registry.Update` answer `invalid` for what `DecodeMount` and `DecodeUpdate` refuse. <!-- fix: 8e93ec85d6 -->
