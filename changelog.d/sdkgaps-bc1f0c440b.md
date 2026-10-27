### Added

- The Go, Rust and Python SDKs export Pi's `buildSessionContext(entries, leafId?, byId?)`: the model context of a session (the path to the leaf with compaction, branch summaries and context edits applied, the thinking level and the model). It is tested against Pi's own answers for 41 cases in all three SDKs.
