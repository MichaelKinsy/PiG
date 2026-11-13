### Added

- The terminal draws the views extensions mount (`ui.view`, D614) when the active Piglet sets `viewClient: terminal`; PiG Standard sets it. Views mounted in `widget-above` and `widget-below` appear above and below the editor and `panel` views directly above it. F6 moves the focus to a panel, Escape returns it to the editor, and choosing a list item, changing a setting, submitting an input or clicking a mouse region raises the intent the extension bound to that node. Stock PiG attaches no view client: a mount still fails with `unsupported`.
- Piglet manifest key `viewClient` (`none` or `terminal`), inherited down an `extends` chain and replaced by naming it.
- The terminal view client states its size to extensions as claims (`size.columns`, and `size.rows` for each slot), so an extension can send an alternative that fits.

### Changed

- The extension host's state push worker ends when it has nothing to push instead of staying parked, so a session with a view client attached and no mount holds no goroutine for it.
