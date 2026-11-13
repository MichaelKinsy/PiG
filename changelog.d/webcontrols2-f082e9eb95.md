### Added

- The reference web client of plugin views (`examples/ui-view-web`) lets the user open a view its extension hid: a hidden mount that declares a `whenHidden` intent is a button with the mount's title in a list above the prompt, and choosing it raises that intent with `{}`. Before, the page did not offer a hidden mount.
- The reference web client raises the `dismiss` intent of a dialog or overlay mount with `{}` when the user presses Escape in it or the browser asks to close the dialog. The page still closes no mount itself, and a mount that declares no such intent stays as it is.

### Fixed

- The reference web client has a colour for every colour token of Pi's theme schema (56 `--pig-<token>` properties, each set from the page's neutral tokens). Before, it had 16, and a text or a shape of a drawing coloured by another token, such as `mdLink` or `mdHeading`, was drawn without that colour or not at all.
- The reference web client keeps its prompt in sight under a panel taller than the window: the page scrolls and the prompt stays at the window's lower edge. Before, the prompt was below the fold.
