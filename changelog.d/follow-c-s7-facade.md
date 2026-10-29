### Added

- The Durable core's TypeScript facade (`durable/core/host/cf`) has Pi's continue submissions, `whenIdle: "reject"` (`ConversationIdle`), `Conversation.submit(tx, draft)` (a transaction `submit` operation; a transaction of another Session, or a wrapped one, throws `The transaction belongs to another Session`), `maxTurns` on the agent, `afterRun` hooks with `runEndTask`, and entry reads (`entry`, `entries`) for hooks and tasks (Pi `fb524dd2`, `6a282511`, `cc9c5231`, `f6a9db1d`, `7e15f960`, `65fba65b`). `ReadAfterWrite` names its operation, an afterRun hook's rejected task requests report as an `AggregateError`, and `pi.live` is version 3, `pi.generation` version 2, `pi.tool` version 3.

### Changed

- The conformance corpus runs rows E32 to E34 and the lane C unit rows against the candidates; their golden traces are recorded (`durable/contract/traces`). A commit listener receives its caller's own `Context`.
