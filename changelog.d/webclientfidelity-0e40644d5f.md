### Changed

- The reference web client of plugin views (`examples/ui-view-web`) draws what a view says about layout and colour: `gap`, `align` and each child's `stack` entry of an `hstack` or `vstack` (so cells with `basis: 0, grow: 1` are columns of one width), the padding of `box`, `text` and `truncated-text`, `bg` and `color` theme tokens, and the view's `theme` overrides. A cell is `1ch` wide and one line high. Markdown nodes are drawn as markdown; their text is never parsed as HTML and only `http`, `https` and `mailto` links are followed.
- The reference web client shows panel mounts beside the chat, side by side or as tabs. A panel behind another is still updated; which panel is shown is the user's choice.
- `make ci-chord-web` also runs the page tests of `examples/ui-view-web` in Chromium; a missing browser or Node fails them there.

### Fixed

- The reference web client no longer erases what the user is typing when a view is updated: an `input` or `editor` whose `id` is in the next view keeps its text, selection and the focus, unless the extension changed its `value`. <!-- fix: fc028ee6d6c57cc064a2b1cf59babcd0cd55a436 -->
