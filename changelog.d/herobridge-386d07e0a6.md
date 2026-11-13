### Added

- `examples/ui-view-web`: `node bridge.mjs --connect <socket, or the directory it is in>` makes every page a client of the view endpoint of a running interactive session (Piglet keys `viewClient: terminal` and `viewEndpoint: socket`) instead of starting a pig for the page. The page and the terminal then draw the same mounts, and an intent raised in one is redrawn in both. The page has no prompt in this mode, keeps its connection across `/reload`, and attaches to the session that follows a new, resumed, forked or switched one.
