### Added

- A `continue` submission starts a run on the context as it is, without a user message (Pi `fb524dd2`, #11043): `durable.SubmissionDraft{Type: durable.SubmissionTypeContinue}`. It is only for an idle conversation (a busy one rejects it with `ConversationBusy`; it never queues), places the inputs an earlier failed run left queued first and joins them in its run, and records a `continue` submission (`SubmissionTypeContinue`; never queued, `placed` then `done` with the run's answer or `unanswered`). Its `pi.durable.submit` span is a root trace linked to its caller, like an input's, and `submission.type` may now be `continue`.
- `SubmissionDraft.WhenIdle = durable.WhenIdleReject` rejects an input that reaches an idle conversation with the new `durable.ConversationIdle` error (`NewConversationIdle`, carries `ConversationId`) and writes nothing; a busy conversation admits it as before (Pi `6a282511`).
- `Conversation.SubmitIn(tx, draft)` and `ConversationHandle.SubmitIn(tx, draft)` admit a submission inside the caller's commit with the rules of `Submit`, and return its ID (Pi `cc9c5231`, #11149). The admission is stored or lost with that commit; it must come before the commit's first table write (else `ReadAfterWrite`), rejections throw before any write, and a transaction of another Session (or a wrapper of one) fails with "The transaction belongs to another Session". Entries admitted from a task's or tool's commit carry its `byTaskId`; the handle form also fails once the invocation is aborted. `ConversationHandle.Submit` now takes any `SubmissionDraft`.

### Changed

- `durable.ReadAfterWrite` names the whole operation: `NewReadAfterWrite(operation)` takes `"Tx.task()"` (or `"submit(tx, draft)"`), the field `Method` is now `Operation`, and the message is `<operation> cannot read tables after the first table write` (unchanged for table reads). `Tx.SettleSubmission` on a record that is not placed fails with `Submission N is not a placed input or continue`.

### Upgrading

- Stored submission records may now have `type: "continue"`; a store written with continue submissions cannot be read by a build that does not know the type.
