### Fixed

- A tool's `handoff` control now continues the run in the new context in the Durable core as it does in Pi: the handoff's reset is written first, queued writes and selected steers land after it and join the continuing run, a follow-up queued across the handoff waits for the continuing run's final boundary, and a queued reset ends the handed-off run. The core used to end the run at the handoff and answer it without another model request.
- `HarnessSettings.afterFailedRun` reaches the Durable core from the TypeScript host, so inputs queued behind a failed run start the next run with `continue` and settle `run_failed` with `withdraw`.

### Added

- `HarnessOptions.onReport` of the TypeScript host takes the `ReportInfo` as its second argument (the task that reported or faulted, the extension whose definition, hook, section or wrapper failed, the host callback, a listener or a failed Session), and `HarnessOptions.models` and `settings` may be functions read at each use, as in Pi.
