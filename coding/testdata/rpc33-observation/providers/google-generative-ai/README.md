# Google Generative AI oracle (Pi 0.99.1, `@google/genai` 2.21.0)

`probe.mjs` drives Pi's real `google-generative-ai` pipeline and records what each consumer observes and when. `inputs.json` exports the axes and the exact SSE bodies. `pi.json` is the raw oracle. Do not replace either with Go output.

## What is recorded

Axes: `shape` (text, thinking, tool, mixed) × `delivery` × `consumer` × `layers` (0, 1, 2 `lazyStream` wrappers, or Pi's `ModelRuntime.streamSimple`): 288 in-process cases, plus 12 real `pi --mode rpc` observations.

| Delivery | Server behavior |
|---|---|
| `buffered` | Headers and the whole body leave in one write. |
| `pending` | Headers leave alone; the body is written after the consumer observes `start` (`result-only`: after the headers were read). |
| `split` | Headers and the first record leave together; the rest is written after release. A single-record body splits mid-record. |

Each case records `records` (what the consumer saw, deep-copied at the delivery tick), `pushes` (each assistant `EventStream.push`, deep-copied at the call, with its stream number: layers create one stream each) and `epochs`. `seq` is the total order across records and pushes of one case. The `rpc` array holds the assistant `message_start`, `message_update` (`usage` only, as on the wire) and `message_end` events that the real Pi RPC process wrote for the same fixtures. The RPC start states are the contract that the Go RPC path must equal.

## Tick order

Every record and push carries `epoch`, `seq`, `tick` and `round`.

- `epoch` counts client socket `data` events: each socket read is a macrotask, so buffered delivery is one epoch and pending or split delivery two.
- `tick` is the number of microtask jobs (Promise reactions and `queueMicrotask` callbacks) that ran since the epoch began, counted by an `async_hooks` `before` hook. Jobs include unrelated background work (undici, Web Streams), so they give a total order but not a dependency distance.
- `round` is the FIFO generation: a self-rescheduling `queueMicrotask` chain starts at each socket `data` event and occupies one queue slot per generation. Two steps in different rounds run in round order; steps in the same round run in enqueue order. This is the number a Go model that posts one reaction per JavaScript job reproduces. The chain starves `process.nextTick` callbacks until it stops after 160 rounds, and undici delivers the end of the body from `nextTick`, so a `done` push reads round 160. Rounds are exact until the end-of-body notification.

The probe observes every case twice, once per clock, and asserts that the two passes agree on every record and push. The job hook only increments a counter, so it does not perturb order; `PROBE_TICKS=0` runs without either clock and produces identical records (verified). A first attempt that used only the round chain moved the end-of-body notification past hundreds of rounds, which is why jobs are kept as the total-order clock.

## Node internals (`undici.mjs`, `undici.json`)

Pi's provider reads the body through the real undici stream. `undici.mjs` measures, in rounds, what the Go model takes as constants: the `fetch()` promise settles 7 rounds after the headers' socket read; the first `reader.read()` settles 2 rounds after the call when the chunk arrived with the headers (buffered, split) and 3 rounds after the socket read that carries it (pending); `new Response(text).json()` settles 2 rounds after the call. The end of the body is not measured because it is delivered from a `nextTick` callback: it runs when the microtask queue is empty, like an external completion. `undici.json` is the recorded output (Node 24.19.0; Node 26.7.0 is identical).

`trace.mjs <shape> <delivery>` prints every microtask job of one case with the stack that created its Promise. It is a reading aid for transcribing awaits, not an oracle.

## Reproduce

Node 24.19.0 (`.node-version`); Node 26.7.0 produces an identical file. `PI_PACKAGE_ROOT` is the installed `@earendil-works/pi-coding-agent` 0.99.1 with its dependency tree.

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node coding/testdata/rpc33-observation/providers/google-generative-ai/probe.mjs "$EVIDENCE/pi.json" "$EVIDENCE/inputs.json"
```

Compare two runs after replacing 13-digit `timestamp` values; everything else, including every tick and round, is identical between runs and between Node versions. Three runs (two under parallel load) and Node 26.7.0 were compared for this file.

Generated tool-call ids are `read_<ms>_<counter>` (`google-generative-ai.ts:199`). The clock and the process-wide counter are not part of the contract, so the oracle stores them as `read_T_N`; the Go replay canonicalizes its own ids the same way.

## Replays

| Test / tool | What it proves |
|---|---|
| `TestGoogleSDKPipelineMatchesNodeTickOrder` (`ai`) | The SDK's promise chain, both tslib generators and the Web Streams reader, over the d82-w0 mock-fetch oracle: exact (segment, round) of the call, `generateContentStream`, each chunk and the end. |
| `TestGoogleProviderConsumerMatchesNodeTickOrder` (`ai`) | The provider loop on the same deliveries: a plain consumer sees each event in the same (segment, round) with the same partial. |
| `TestGoogleProviderRoundsMatchNodeRealSockets` (`ai`) | This oracle's layer-0 direct cases through real HTTP: exact (socket read, round) of every event before the end of the body. |
| `TestGoogleUndiciConstantsMatchProbe` (`ai`) | The model's Node constants equal `undici.json`. |
| `TestRPC33GoogleObservationMatrix` (`coding`) | All 288 in-process cases (every shape, delivery, consumer and layer) equal Pi record for record. |
| `node check.mjs <pig> <runs> [concurrency] [shape/delivery]` | PiG's `--mode rpc` binary equals the 12 real-Pi RPC observations over N runs. |

`providers/check.mjs <pig> 200 google-generative-ai` (the integrator's start-state oracle) reports 200/200 equal Pi.

Known limit: `check.mjs` is 200/200 for eleven of the twelve cases; `thinking/buffered` (three records in one body read, usage on the last) differs from Pi in most runs and `text/split` in about one run in two hundred. The difference is the RPC `message_update` `usage`: Pi serializes it after a fixed number of microtasks, while PiG's Session waits for its ordered worker over a real channel, so how much of the body the provider has read by then depends on wall-clock time. A three-record buffered OpenAI Completions stream shows the same two-way split through the same Session, so the cause is the Session/RPC wait, not the provider. The in-process matrix, which has no Session, is exact.

## Files

- `probe.mjs`: the oracle.
- `undici.mjs`, `undici.json`: undici/Web Streams round constants.
- `trace.mjs`: per-job trace of one case.
- `check.mjs`: PiG RPC binary against the twelve real-Pi RPC observations.
- `server.mjs`: fixture server, forked so the probe process sees only client sockets.
- `inputs.json`, `pi.json`: exported bodies and the recorded oracle.
