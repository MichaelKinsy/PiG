### Added

- In the terminal, the views extensions mount in `panel` are tabs: one row lists them by title, the front tab is drawn, and F6 walks from tab to tab and back to the editor. A view its extension hid is listed too when the extension lets the user open it; Enter on it opens it.
- A view an extension shows comes to the front when you are in the editor. While you work in another view its tab is marked with `*` instead, and nothing moves under your hands.

### Changed

- A view that asks for the keyboard gets it only from an empty editor. While you are typing, the view is shown and the focus stays in the editor; the extension is told why.
- Two panels that an extension mounts are no longer drawn one under the other in the terminal: they are tabs. One panel without a title is drawn as before.
