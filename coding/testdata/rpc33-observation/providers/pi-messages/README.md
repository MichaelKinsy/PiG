# pi-messages oracle (D82 W5)

`probe.mjs` drives Pi 0.87.1's real pipeline for the `pi-messages` API (`packages/ai/src/api/pi-messages.ts`) and writes `pi.json`. Do not replace `pi.json` with Go output.

Fixtures: `tool` (the RPC33 tool call), `mixed` (thinking, text, and a tool call whose arguments split across two deltas), `error` (a terminal error event), `truncated` (end of body with no terminal event), `malformed` and `notjson` (a record `JSON.parse` rejects). Deliveries: `buffered` (headers and body in one write), `pending` (headers first, whole body after the first body read is pending), `chunked` (one SSE record per write), `split` (40-byte writes, so records span reads), `tail` (two records, then the rest). `cancels` aborts the request while the first body read is pending and while a read is pending after the start event. Paths: `direct` (pi-ai `stream()`), `runtime` (`ModelRuntime.streamSimple`, the lazy layers), `agent` (`runAgentLoop`), plus `rpc`, the assistant records of the first turn of the real `pi --mode rpc` binary.

`trace` is the tick order: `read` (`ReadableStreamDefaultReader.read()` call), `decode:N` (a body read resolved with N bytes), `parse:<type>` (`JSON.parse` of one SSE record), `push:s<N>:<type>` (`EventStream.push` on the N-th stream), `deliver:<type>` (the consumer's `for await` body or agent sink). `states[].at` is the trace length at the tick the consumer observed the message. The hooks return the original promises and values, so they add no microtask.

`ticks.mjs` writes `ticks.json`: Pi's real `stream()` over a scripted Web Stream with `options.fetch`, so every read, await and yield is a microtask and a self-rescheduling microtask counts them from the first body read. Each mark (`read:N`, `push:TYPE`, `deliver:TYPE`) is the tick of one synchronous observation, and `result` is the terminal message. It covers chunk splits, CRLF, multi-byte splits, BOM, invalid UTF-8, malformed JSON, rejected reads and bodies that end without a terminal event.

The Go replays are `TestPiMessagesDirectMatchesPiTickOrder`, `TestPiMessagesCancelMatchesPi` and `TestPiMessagesBodyPathTicksMatchPi` in `ai`, and `TestRPCPiMessagesMatchesPi` in `cmd/pig` (the real binary; `PIG_D82_RUNS=200` for the D82 evidence). `pi.json` and `ticks.json` are identical across repeated runs and across Node 24.19.0 (`.node-version`) and 26.7.0.

## Reproduce

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/pi-messages/probe.mjs "$EVIDENCE/pi.json"
node coding/testdata/rpc33-observation/providers/pi-messages/ticks.mjs "$EVIDENCE/ticks.json"
```
