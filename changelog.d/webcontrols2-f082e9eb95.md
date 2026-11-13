### Added

- The reference web client of plugin views (`examples/ui-view-web`) lets the user open a view its extension hid: a hidden mount that declares a `whenHidden` intent is a button with the mount's title in a list above the prompt, and choosing it raises that intent with `{}`. Before, the page did not offer a hidden mount.
- The reference web client raises the `dismiss` intent of a dialog or overlay mount with `{}` when the user presses Escape in it or the browser asks to close the dialog. The page still closes no mount itself, and a mount that declares no such intent stays as it is.
- The reference web client has a colour for every colour token of Pi's theme schema: 56 `--pig-<token>` properties, each set from the page's neutral tokens, so a text or a shape of a drawing coloured `mdLink` or `mdHeading` is drawn in the accent or the text colour.

### Changed

- The reference web client draws a mount's `title` as the heading of its card; a mount without one keeps the small caption `<plugin> / <key>`. The page under a modal dialog is dimmed by the new token `--view-backdrop` (black at 20 % in the light scheme, 30 % in the dark one), a loader stands 8 px under the node before it, and under a panel taller than the window the prompt stays in sight at the window's lower edge.
