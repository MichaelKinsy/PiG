# Bedrock ConverseStream oracle (Pi 0.99.1, `@aws-sdk/client-bedrock-runtime` 3.1127.0, `@smithy/core` 3.33.3)

`probe.mjs` drives Pi's real `bedrock-converse-stream` pipeline (pi-ai provider, AWS SDK client, smithy event-stream deserializer, Node `Http2Stream`/`IncomingMessage` `Readable`) and records what each consumer observes and when. `inputs.json` exports the axes and the exact `application/vnd.amazon.eventstream` bodies (base64, one frame per entry). `pi.json` is the raw oracle. Do not replace either with Go output.

## What is recorded

Axes: `shape` (text, thinking, tool, mixed) × `delivery` × `http` (`h2`, `h1`) × `consumer` (direct, held-direct, result-only, cancel, agent, held-agent) × `layers` (0 or 2 `lazyStream` wrappers, or Pi's `ModelRuntime.streamSimple`): 432 cases. On top of that, each `http` runs the direct consumer over 0 layers for eight failure and odd-frame shapes (`throttling`, `abrupt`, `weirdstop`, `badframe`, `unmodeled`, `errframe`, `unknownevent`, `emptybody`: a modeled and an unmodeled exception, an error frame, a stream without a stop reason, an unknown stop reason, a corrupt frame, an event the SDK drops, an event with an empty payload) and for `text` and `tool` without the `onPayload`/`onResponse` hooks (`hooks: false`): 492 in-process cases in all, plus 24 real `pi --mode rpc` observations.

| Delivery | Server behavior |
|---|---|
| `buffered` | Headers and the whole body leave in one write. |
| `pending` | Headers and the first frame (`messageStart`) leave together; the rest is written after the consumer observes `start` (`result-only`: after the first bytes were read). |
| `split` | Headers, the first frame and half of the second frame leave together; the rest is written after release. |

There is no headers-only delivery. The SDK's ConverseStream deserializer reads the first event inside `client.send` (`@smithy/core` `EventStreamSerde.deserializeEventStream`: `await asyncIterator.next()`), so `start` cannot be pushed before the first frame arrives. `pending` and `split` therefore differ only in whether the second frame is complete.

`http` selects the transport. `h2` is Pi's default: `NodeHttp2Handler` over cleartext prior-knowledge HTTP/2. `h1` sets `AWS_BEDROCK_FORCE_HTTP1=1` (`bedrock-converse-stream.ts:227`), which selects `NodeHttpHandler`. Both give identical states and different job counts.

Each case records `records` (what the consumer saw, deep-copied at the delivery tick), `pushes` (each assistant `EventStream.push`, deep-copied at the call, with its stream number: layers create one stream each) and `epochs`. The `rpc` array holds the assistant `message_start`, `message_update` (`usage` only, as on the wire) and `message_end` events that the real Pi RPC process wrote for the same fixtures. The RPC start states are the contract that the Go RPC path must equal.

Every provider call passes `onPayload` and `onResponse` as `sdk.ts:349-388` does. `onResponse` adds Pi's deserialize middleware (`bedrock-converse-stream.ts:510-524`), which awaits the callback before `send` resolves.

## Replays

- `ai/bedrock_observation_test.go` replays the HTTP/1 direct and result-only cases through PiG's provider at Pi's `(epoch, tick)`, and the cancel cases by events and terminal message (Pi's abort listeners run inside `controller.abort()`; PiG delivers the cancellation as an external completion).
- `cmd/pig/rpc_bedrock_observation_test.go` runs the real binary for the 12 HTTP/1 `rpc` rows.
- `../check.mjs` (`node check.mjs <pig> 200 bedrock-converse-stream`) is the multi-provider start-state check; `frames.mjs` builds its fixture.
- `chain.mjs` records job positions of the AWS SDK stack alone (`chain.json`): the two constants of `ai/bedrock_stream_pipeline.go` (`bedrockClientSendPrefixHops`, `bedrockClientSendReturnHops`) and their hook variants come from it.
- `ai/testdata/bedrock-eventstream/probe.mjs` records what the real AWS SDK and `@smithy/core` event-stream stack yields or throws for 40 byte sequences (`golden.json`); `ai/bedrock_eventstream_test.go` replays them through PiG's framing and decoding.

## Findings

- Every `rpc` row with `buffered` delivery starts with exactly the state after the second event item (`messageStart`, then one more item): a text or thinking block with its first delta, or a tool call with `arguments:{}`, `partialJson:""` and `index:0`. `pending` and `split` deliveries start with an empty, `pending` message. The rows are identical on `h1` and `h2`.
- A tool call handled from the second item alone shows the scratch fields (`index`, `partialJson`, and for thinking `thinkingSignature:""`) in the serialized start.
- Per event the pipeline costs 15 microtask jobs (`toolcall_start` push to `toolcall_delta` push) and a `direct` consumer sees each event 5 jobs after its push.

## Tick order

Every record and push carries `epoch` and `tick`. A tick is one microtask job counted by an `async_hooks` `before` hook (`PROMISE` and `Microtask` resources). An epoch is one macrotask that delivers bytes: an HTTP/1 client socket `data` event, or an HTTP/2 stream `response` event or body push. Two triggers with no job between them share an epoch. Epoch 0 is everything before the first byte. A record at `(epoch, tick)` ran after `tick` jobs of that epoch.

The counter only increments a number, so it does not create Promises and cannot reorder jobs. `PROBE_TICKS=0` runs without the hook. A first attempt used a self-rescheduling `Promise` ticker chain; it starved `process.nextTick` callbacks, which the `Readable` iterator's `readable`/`end` stages need, so it measured a different schedule and was discarded.

## Reproduce

Node 24.19.0 (`.node-version`); Node 26.7.0 produces an identical file. `PI_PACKAGE_ROOT` is the installed `@earendil-works/pi-coding-agent` 0.99.1 with its dependency tree.

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/bedrock-converse-stream/probe.mjs "$EVIDENCE/pi.json" "$EVIDENCE/inputs.json"
```

`PROBE_SKIP_RPC=1` skips the 24 real RPC processes. Compare two runs after replacing 13-digit `timestamp` values; everything else, including every tick, is identical between runs and between Node versions. Two consecutive runs (one under parallel load) and Node 26.7.0 were compared for the in-process cases.

## Files

- `probe.mjs`: the oracle.
- `frames.mjs`, `chain.mjs`, `chain.json`: see Replays.
- `server.mjs`: fixture server, forked so the probe process sees only client sockets. It serves HTTP/1.1 or cleartext HTTP/2.
- `inputs.json`, `pi.json`: exported bodies and the recorded oracle.
