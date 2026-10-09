### Added

- Added `aborted` to `agent_settled` session, extension, and JSON/RPC events, so integrations can tell a cancelled run from a finished one.
- Added `durationMs` to the tool render context (`ToolRenderContext`), to `tool_execution_end` events, and to stored tool result messages: how long the tool's `execute()` took, measured with a monotonic clock and excluding hooks.
- Added program status reporting with OSC 7501: terminals and agent dashboards that support it see whether PiG is working, blocked on a dialog or login, done, or failed. `PI_PROGRAM_STATUS=1|0` overrides detection (see [Terminal setup](docs/site/docs/terminal-setup.md#program-status)). The reported `app` is `pig`.
- Added `durationMs` to assistant messages: `AssistantMessageEventStream` measures each response with a monotonic clock from the start of the request to its final message, for every API implementation. Deferred results fetched later stay untimed.

### Fixed

- Fixed the error message of a failed lazy API setup, such as a module load or auth failure, using its failure time as `timestamp` instead of the request start.
- Fixed bash and PowerShell results losing `Took` after reloading a session, and the live `Took` including wall-clock steps; both now show the recorded execution time.
- Fixed the message-format page: it said PiG does not carry the `nestedCalls` record on tool result messages, which it has written since 1.0.4, and it now lists the field; the page also stops citing a `replace` field that Pi's page no longer lists.
