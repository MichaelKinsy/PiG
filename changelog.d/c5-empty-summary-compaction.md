### Fixed

- A compaction that completes with an empty summary, such as one an extension answers `session_before_compact` with, now shows the `[compaction]` block and rebuilds the transcript like any completed compaction, and the RPC `compaction_end` event carries its `result`. The interactive mode and the RPC encoder took an empty summary for a missing result.
