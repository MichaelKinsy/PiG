# mistral-conversations oracle (D82 W5)

`probe.mjs` drives Pi 0.99.1's real pipeline for the `mistral-conversations` API (`packages/ai/src/api/mistral-conversations.ts`) and writes `pi.json`. Do not replace `pi.json` with Go output. Pi 0.99.1 does not use `@mistralai/mistralai` for streaming: `requestMistralStream` calls `fetch` and `readMistralEvents` reads `response.body` through a `ReadableStreamDefaultReader`, so the whole pipeline is Pi source plus Node's Web Streams and undici.

Fixtures: `tool` (the RPC33 tool call with finish and usage in one chunk), `mixed` (empty string content, array thinking, string and array text, a tool call whose arguments split across two chunks, usage on a chunk with no choices), `multi` (two tool calls in one chunk), `errorFinish` (`finish_reason: "error"` followed by more chunks, which Pi still consumes), `truncated` (end of body with no finish reason), `noDone` (finish reason, no `[DONE]`), `malformed`, `notjson` and `nochoices` (records Pi rejects). Deliveries: `buffered` (headers and body in one write), `pending` (headers first, the whole body after the first body read is pending), `chunked` (one SSE record per write), `split` (40-byte writes, so records span reads), `tail` (two records, then the rest). `cancels` aborts the request while the first body read is pending and while a read is pending after the start event. Paths: `direct` (pi-ai `stream()`), `runtime` (`ModelRuntime.streamSimple`, the lazy layers), `agent` (`runAgentLoop`), plus `rpc`, the assistant records of the first turn of the real `pi --mode rpc` binary.

`trace` is the tick order: `read` (`ReadableStreamDefaultReader.read()` call), `decode:N` (a body read resolved with N bytes), `parse` (`JSON.parse` of one SSE record), `push:s<N>:<type>` (`EventStream.push` on the N-th stream), `deliver:<type>` (the consumer's `for await` body or agent sink). `states[].at` is the trace length at the tick the consumer observed the message. The hooks return the original promises and values, so they add no microtask.

`ticks.mjs` writes `ticks.json`: Pi's real `stream()` over a scripted Web Stream with `options.fetch`, so every read, await and yield is a microtask and a self-rescheduling microtask counts them from the first body read. Each mark (`read:N`, `push:TYPE`, `deliver:TYPE`) is the tick of one synchronous observation, and `result` is the terminal message. It covers chunk splits, every SSE boundary form Pi's `findMistralEventBoundary` accepts, multi-byte splits, BOM, invalid UTF-8, `[DONE]` mid-stream, unknown finish reasons, an unterminated trailing record, malformed JSON, rejected reads and bodies that end without a finish reason.

`realticks.mjs` writes `realticks.json`: the same counter over Pi's real `stream()` against a loopback server that writes headers and the whole body in one write, so the first read goes through undici's byte stream instead of a scripted one. It records the tick of every push and every delivery for the fixtures that finish inside the microtask domain. A scripted body settles its first read one generation earlier than a fetch body, which is why `ticks.json` alone cannot pin the provider's timeline against an RPC client.

`pi.json`, `ticks.json` and `realticks.json` are identical across repeated runs and across Node 24.19.0 (`.node-version`) and 26.7.0 (except the recorded `node` field). The Go replays are `TestMistralDirectMatchesPiTickOrder`, `TestMistralCancelMatchesPi`, `TestMistralBodyPathTicksMatchPi` and `TestMistralRealBodyTicksMatchPi` in `ai`, and `TestRPCMistralMatchesPi` in `cmd/pig` (the real binary; `PIG_D82_RUNS=200` for the D82 evidence).

## Reproduce

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/mistral-conversations/probe.mjs "$EVIDENCE/pi.json"
node coding/testdata/rpc33-observation/providers/mistral-conversations/ticks.mjs "$EVIDENCE/ticks.json"
node coding/testdata/rpc33-observation/providers/mistral-conversations/realticks.mjs "$EVIDENCE/realticks.json"
```
