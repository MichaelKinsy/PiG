### Changed

- The Durable core and its host protocol follow the Pi features of `87eff524..e7a0df27`: continue submissions and `whenIdle: "reject"` (`ConversationIdle`), a submission admitted inside a host transaction (the transaction operation `submit`, refused after the transaction's first table write with `ReadAfterWrite`), the per-conversation turn limit (`maxTurns` in the resolved agent, run failure `turn_limit`), a run whose context has nothing to answer (`nothing_to_answer`, decided from the new `owes` field of the context summary), the `afterRun` hook and the run-end tasks it asks for (the host checks the requested tasks and answers `{tasks, rejected}`; several rejections report an `AggregateError` with its `errors`), `afterTool` on every tool task result including calls to tools not offered (their own flagged tool task), and entry reads for hooks and tasks. `host/PROTOCOL.md` section "Pi 7e15f960..e7a0df27 additions" is the contract.

### Upgrading

- Stored formats written by the core change as in the Harness: `pi.live` is version 3 (a run carries `turns`, `first` and `afterRun`; a stored run without `turns` reads as 0), `pi.generation` is version 2 and `pi.tool` is version 3 (input `unoffered`). A store written by this version cannot be read by a build that expects the earlier versions.
