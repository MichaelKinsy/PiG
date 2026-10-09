### Added
- Pi Durable scans take an `Order` (`durable.ScanAscending` or `ScanDescending`) in `ConversationQuery`, `EntryQuery`, `TaskQuery` and `SubmissionQuery`. A cursor carries its order and rejects a query that asks for the other one. `Conversation.Entries` passes the order through.
- Task records carry `StartedAt` and `EndedAt`, stamped by the Session on the first change to running and on the change to terminal. `session.CreateSession` takes an optional clock.
- A conversation's context can be read as of an earlier entry with `ContextOptions{At}`, the view a fork at that entry starts with.
- `durable/storage/sqlite/cloudflare` adapts the SQLite storage of a Cloudflare Durable Object to Pi Durable's SQLite storage.
- Tools and hooks get the Harness's `Models()`. `HarnessSettings.ContextRetentionMs` (default ten minutes) sets how long an idle conversation keeps its derived context.

- Tool results carry `durationMs`, the milliseconds the tool's `execute` took; `ai.ToolResultMessage.DurationMs` is nil for calls that did not run. `durable/harness.AppendToolResult` takes the duration.

### Changed
- A system message that only user messages precede leads the model context, so a later tool change no longer invalidates the whole provider prompt cache.
- `ToolExecutionApi.Output` takes an optional skipped marker, like Pi's `output(chunk, skipped?)`; the separate `OutputSkipping` method is gone. `RegisterEnvConformance` takes an options struct for the shell and symlink support.
- `durabletest.StorageConformanceAssertions.Ok` takes `(value any, message ...string)` and checks JavaScript truthiness, as Pi's `ok(value: unknown, message?: string)` does.
