# anthropic-messages start-state and tick-order oracle (gap-d82, W5)

`probe.mjs` drives Pi 0.87.1's real anthropic-messages pipeline (`packages/ai/src/api/anthropic-messages.ts`, `utils/event-stream.ts`, `api/lazy.ts`, and `coding-agent/src/core/model-runtime.ts`) against a loopback server. It pins Pi, `@earendil-works/pi-ai` and `@earendil-works/pi-agent-core` to 0.87.1. `pi.json` is the raw output of one execution on Node 24.19.0. The Go differential test is `TestAnthropicMicrotaskTrace` in `ai/anthropic_microtask_trace_test.go`; the shared RPC row is `../check.mjs ... anthropic-messages`.

## What is measured

- `cases[].trace` stamps every `EventStream.push` (`side: "push"`), every consumer delivery (`"deliver"`) and the awaited result (`"result"`) with `(epoch, tick)` and the message state visible at that instant.
  - An **epoch** is one macrotask: a timer, immediate or I/O completion. Promise jobs and `process.nextTick` callbacks belong to the macrotask that scheduled them. The probe numbers macrotasks with `async_hooks` and renumbers them over the epochs that hold a record, because the loopback server's own macrotasks are noise.
  - A **tick** is one round of a self-rescheduling Promise chain that restarts at every macrotask, so equal `(epoch, tick)` plus `seq` totally orders the microtask interleaving. The first epoch is measured from its first record, Pi's `start` push, because the request and header phases run before the pipeline this API models.
  - Clocks are pinned to `0`.
- `rpc[]` is the first assistant `message_start` that `pi --mode rpc --no-extensions` prints for the tool fixture under each delivery (`--rpc <cli>`); the same command with a PiG binary reproduces it.

## Fixtures

Shapes (`inputs.json` `bodies`): `tool` (the RPC33 read call), `text`, `thinking`, `ping` (skipped records), `error-event`, `bad-json`, `truncated`, `bad-stop`, `no-stop`, `unstopped`, `crlf`, `utf8` (multi-byte text, an ill-formed byte, a truncated sequence) and `bom`.

Deliveries (`inputs.json` `plans`), each chunk a separate socket write: `buffered` (headers and the whole body in one write with `content-length`, the RPC33 fixture), `pending` (headers, then the body later), `buffered-then-pending`, `pending-per-record`, `buffered-cr-split` (first write ends after a carriage return), `buffered-byte-split` (first write ends inside a multi-byte character) and `buffered-mid-line`. Layers: direct (`0`), one and two `lazyStream` forwarders, and `ModelRuntime.streamSimple` (`runtime`). `abortAtStart` cases abort when the consumer receives `start` and are retained for the cancellation work; the Go test does not compare them, because a Go cancellation completes externally where undici rejects synchronously.

A delivery that puts several HTTP chunks into one socket write is not an oracle: Node 24.19 coalesces them into one `read()` value and Node 26.7 does not (+3 ticks).

## Findings the Go model relies on

- Node 24.19.0 and 26.7.0 produce identical `cases` for every delivery above.
- A body that arrived with the headers settles its first `reader.read()` two reactions after the call. Data that arrives in a later macrotask settles three reactions after the macrotask starts. The end of the body settles one reaction after its macrotask when the last bytes were buffered with the headers, two when they waited for the transport.
- Each SSE record then costs four reactions: `iterateSseMessages` yields (an await), `iterateAnthropicEvents` awaits that result, yields (an await), and the provider's `for await` awaits the result. A record `iterateAnthropicEvents` skips costs two.
- A throw in the provider's loop body closes `iterateAnthropicEvents`, which closes `iterateSseMessages`: four more reactions before the catch block runs. A throw inside `iterateAnthropicEvents` costs three; one from `iterateSseMessages` costs two.
- The consumer sees the state at its own tick: at `text_start`'s delivery the delta that follows has not run, and `message_start`'s response ID and usage are visible to a consumer that holds the start event before any push announces them.

## Reproduce

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
# Node 24.19.0 on PATH
node probe.mjs --rpc "$PI_PACKAGE_ROOT/dist/cli.js" pi.json inputs.json
# Trace only, for a second Node version; the cases must equal pi.json's
node probe.mjs /tmp/pi-node26.json
# PiG through the same RPC fixtures
node probe.mjs --rpc <pig-binary> /tmp/pig.json
go test ./ai -run TestAnthropicMicrotaskTrace -count=3 -race
node ../check.mjs <pig-binary> 200 anthropic-messages
```
