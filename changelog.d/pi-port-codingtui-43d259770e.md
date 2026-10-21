### Fixed

- `@` file autocomplete finds paths for queries that hold regex metacharacters such as `[` or `(`, for a query with or without a `/` (Pi 612a172e, #10972). <!-- fix: 43d259770e -->
- `/compact <instructions>` applies the instructions to the summary of the start of a turn that compaction splits, and no longer ignores them when the split turn is the only new content since the last compaction (Pi adfc1446, #11058). <!-- fix: 43d259770e -->
- A resumed session and `/tree` navigation activate the extension tools the branch never added or removed, such as tools of an extension installed after the session started, unless they are registered with `defaultActive: false`; a tool the branch removed, also before a compaction, stays inactive. Built-in tools keep following `defaultTools` (Pi bd3c9b82, #11062, #11075). <!-- fix: 43d259770e -->
- Switching to a session whose system message holds a `toolsAdded` or `toolsRemoved` member the loadout restore cannot read fails with the TypeError of Pi's tool-list replay; a system message with an unreadable `content` no longer fails the switch (Pi bd3c9b82). <!-- fix: 43d259770e -->
