### Fixed

- A Node extension loaded in a TUI could lose `ctx.ui` (every widget, status and footer call became a no-op) when the host's state update reached it while it was handling the ready frame, because the update applied before the older state of the ready frame, which then replaced it. The queued update is now applied only after the ready state. <!-- fix: 2e8dcc1eea -->
