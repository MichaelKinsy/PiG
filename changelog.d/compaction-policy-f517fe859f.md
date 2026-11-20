### Added

- `compaction.summaryModel` setting (as `provider/modelId`) lets a registered model write the compaction summary while the session keeps its own model, falling back to the session model on any non-cancel error.
- `/compact-cancel` slash command explicitly cancels a running compaction.

### Changed

- Aborting a turn or pressing Escape no longer cancels an in-flight compaction; only `/compact-cancel` or ending/replacing the session stops it, and a second compaction request while one runs returns `"Already compacting"`.
