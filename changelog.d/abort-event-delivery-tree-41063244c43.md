### Fixed

- A `session_tree` handler is no longer cancelled, and later extensions no longer miss the event, when the context of the tree navigation ended while it ran. As in Pi, the event reaches every extension after the leaf moved. <!-- fix: 41063244c43 -->
