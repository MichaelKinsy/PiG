### Upgrading

- Go API `durable`: `Storage.Commit` takes an adopt callback, as Pi's `commit(writes, adopt, context)`. Before: `storage.Commit(ctx, writes)`. After: `storage.Commit(ctx, writes, func(seq durable.Seq) {})`. A Storage calls it once, synchronously, when the batch is durable and before its next commit, and never for a commit that fails; a Storage that resolves without calling it fails the Session. Code that wraps or implements Storage must pass it through.
- Go API `durable`, `chord`: `WatchHandle`, `WatchEnd` and the end reasons move to `chord`; `durable.WatchHandle`, `durable.WatchEnd` and the `Watch*` reason constants are aliases of them. The reasons `session_closed` and `session_failed` are now `ended` (`WatchEnded`) and `failed` (`WatchFailed`).
- Go API `durable`: `Session.DocumentStateErased`, `DocumentState`, `DocumentStateOf`, `Conversation.ViewState` and `Harness.TaskGraph` are removed, as in Pi. Use `WatchDoc`, `Conversation.Watch` and `Harness.WatchTaskGraph`; start the watch to keep its value current, and stop it where the state was disposed.
- A document watch ends `retired` only when its document is retired, not on a null value.

### Changed

- The Durable example tests are renumbered as in Pi (`TestExample04Watches` through `TestExample30ReloadAndRestart`); the chord-state example is gone.
