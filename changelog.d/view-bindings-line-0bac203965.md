### Added

- A view's `title`, its `key` and the `name` of a key binding are labels a client writes on one line. The host answers `invalid` for one that holds a control character, a line separator or a control that reorders the text around it (U+202A to U+202E and U+2066 to U+2069), so that a tab or a key hint cannot pass for another. The direction marks of Arabic and Hebrew text (U+200E, U+200F, U+061C) are accepted.
- An extension can bind `+`: `+` and `ctrl++` are key ids, as in Pi.
- In the terminal, a key your keybindings give to interrupt, clear, exit or suspend never raises an extension's binding, also after you change `keybindings.json` and run `/reload`. The extension reads one notice for such a binding.
- A Go program that mounts a view through `uiview.Registry` with a record it built itself is held to the same checks as an extension: `Registry.Mount` and `Registry.Update` answer `invalid` for what `DecodeMount` and `DecodeUpdate` refuse.
