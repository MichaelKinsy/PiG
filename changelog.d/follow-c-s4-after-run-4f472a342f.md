### Added

- `GenerationHooks.AfterRun` (Pi `7e15f960`): a hook that runs after a run answered (a final answer, or a tool round whose every call asked to terminate), with a `RunSummary` (`Reason`, `First`, `Answer`, `Last`). The tasks it asks for with `RunEndTaskOf` are created together as children of the generation, which keeps the run and the conversation busy until they are terminal; the run's inputs settle `done` at the answer, and queued items are placed only after the tasks end, then the run ends. A crash before the tasks exist asks again, once created never. A request for an unknown or built-in task, a non-JSON input, or an `Initial` that fails is rejected and reported against the hook's extension. A run whose finish or drain phase was cut off `restartLimit` times ends completed, without a failure, and is reported `task_interrupted`.
- `HookApi.Entry`, `HookApi.Entries` and `TaskRuntime.Entries` (Pi `65fba65b`): committed raw history of the task's conversation, bounded by entry IDs, so a hook or task reads the entries of a run even after a compaction.

### Upgrading

- The stored checkpoint of `pi.generation` gains two phases: `finish` (`{phase, run: {reason, answer, last, first}}`) and `drain` (`{phase, answer, tasks}`), and `pi.live.run` holds `afterRun: {tasks?}` while a run that answered waits for its tasks. A build without them cannot continue a generation stored in these phases. `harness.HookApi` and `durable.TaskRuntime` gain methods: implementations (the Durable core's driver) must add `Entry`/`Entries`.
