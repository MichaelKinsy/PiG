### Fixed

- A notification, status or message that an extension queued with `setImmediate` or `setTimeout` from a tool that returned while a sibling tool in the same batch still awaited reached an RPC client behind the sibling's own output in up to two runs in five, where Pi writes it right after the first tool's `tool_execution_end`. The Node runtime is another process: it wrote the callback's host call a few hundred microseconds after the tool's answer, and the host passed that tool's `tool_execution_end` in about the same time, so a call that arrived after it waited for the next `tool_execution_end` of any tool. The call now says which tool call queued its callback (`origin_tool_call_id`). The host runs it behind that call's `tool_execution_end` when it arrives before the event, and at once when it arrives after, where the runtime says a sibling tool still runs and the loop turns. <!-- fix: 65932e6efb, 4ac21c86c4 -->

### Changed

- The wire's host call gains an optional `origin_tool_call_id` (`CallPayload.OriginToolCallID`), which the Node runtime sets with `tick` on the call of a callback that a tool call's handler queued; the host runs the call behind that call's `tool_execution_end`, or at once if the event already passed. The host and the Node runtime change together with no negotiation. The Go, Rust and Python SDKs never set `tick`, so they send none.

### Upgrading

- If code outside this repository builds `subprocess.CallPayload` with an unkeyed literal, add a sixth field (`""` keeps today's behaviour) or switch to keyed fields.
