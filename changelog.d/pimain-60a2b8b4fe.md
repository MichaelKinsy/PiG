### Added

- Deferred commits: `Session.Commit`, `Conversation.Commit`, `TaskRuntime.Commit` and a tool's `Commit` take an optional `durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: ...}}`. A commit that changes only existing latest, initial-fork conversation documents is adopted in memory and shown to watches at once, and stored with the next commit that reads or writes the document, before the document leaves memory, at close, or within `WithinMs` (default `Settings.Progress.PersistWithinMs`, or `session.CreateSessionOptions.PersistWithinMs`); any other write makes it immediate. A crash loses what is held. `SubscribeCommits` sees a deferred commit once it is stored, with exactly what was stored. Ports Pi's deferred commits (spec section 4.1).
- `ProgressPolicy.PersistWithinMs` (`settings.progress.persistWithinMs`, default 1,000 ms) is how long deferred progress commits may stay in memory.

### Changed

- Partial answers and running tool output are stored later than they are shown: their throttled commits are deferred, so watches, views and agent events see each at once, while Storage gets them together with the next commit that reads `pi.live` (the end of the answer, a tool settling, a run ending) and at the latest after `PersistWithinMs`. A crash now loses up to about a second of streamed content instead of 100 ms; a stream that crashes before its first store leaves no aborted entry, and that attempt's usage is not recorded. <!-- fix: 60a2b8b4fe -->
- The Session's own observers (`ObserveCommits`, used by document watches, the conversation view, agent events, the task graph and the scheduler) take a `session.ObservedCommit`, which also reports a commit held in memory; a document watch follows memory, so it holds an observer, not a `SubscribeCommits` subscription.
- The core host stores every commit before it returns: it accepts `CommitOptions` and treats a deferred commit as immediate.
