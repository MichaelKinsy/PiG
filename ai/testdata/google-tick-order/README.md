# Google tick-order oracle (D82)

`probe.mjs` runs Pi 1.0.2's real Google Generative AI provider over the real `@google/genai` 2.21.0 SDK and records, for ten body-delivery cases, the exact microtask round (`tick`) inside each macrotask (`seg`) at which the SDK, the provider and a plain consumer observe each step. `pi.json` is the retained oracle. It was produced on Node 24.19.0 (`.node-version`). Node 26.7.0 produces identical stamps, and three executions on each Node version were byte-identical apart from the `node` field.

Only `globalThis.fetch` is replaced. It resolves from a timer callback, like a network callback, and returns a `Response` over a Web `ReadableStream`. Each case states when each byte chunk is enqueued (`deliveries[].at`): `0` means the bytes are already queued when the response resolves, so the first `reader.read()` finds them buffered. `N > 0` means the bytes arrive in the N-th later macrotask, so the read is pending. This makes the body-read readiness an explicit input, never a scheduling accident.

## Stamp semantics

A segment starts in a macrotask. A self-rescheduling microtask chain starts first in every segment and `tick` is the number of chain steps that have run. The chain shares the microtask FIFO with the code under test, so `(seg, tick)` is the exact FIFO round, and array order inside one stamp is the exact execution order. The chain occupies one slot per round and never reorders other reactions. The probe asserts `tick < cap - 8`.

Timelines per case:

- `sdk`: `await client.models.generateContentStream(params)` resumes (`sdk-resolved`), each `for await` chunk, and the end. This is what Pi's `for await (const chunk of googleStream)` sees (`.upstream/v0.87.1/packages/ai/src/api/google-generative-ai.ts:105-107`).
- `provider`: every `AssistantMessageEventStream.push` by Pi's provider with a deep copy of the partial at the push tick.
- `consumer`: a plain `for await` over the returned stream (`packages/ai/src/utils/event-stream.ts:44-91`), with the partial deep-copied when the consumer resumes, plus `result()`. This shows what an observer of the shared `output` object sees at delivery. The Agent, Session and RPC layers add their own rounds on top (plan §3).

## Modeled awaits (what the tick numbers include)

Each stamp is the sum of the following steps. W5's Go transcription must reproduce each step with the shared primitives and cite the same lines:

| Step | Source |
|---|---|
| `await retryGoogleRequest(() => client.models.generateContentStream(params))` | `google-generative-ai.ts:100`; `google-shared.ts` `retryGoogleRequest` (async wrapper `await request()` inside `retryProviderRequest`) |
| `generateContentStream` async arrow, `maybeMoveToResponseJsonSchema`, `await processParamsMaybeAddMcpUsage`, `return await generateContentStreamInternal` | `@google/genai/dist/node/index.mjs:15309-15315` |
| `generateContentStreamInternal` returns `response.then(apiResponse => __asyncGenerator(...))` | `index.mjs:15815-15845` |
| `requestStream`, `await includeExtraHttpOptionsToRequestInit` (`await getHeadersInternal`) | `index.mjs:13725-13745` |
| `streamApiCall`: `apiCall(...).then(async response => { await throwErrorIfNotOK(response); return this.processStreamResponse(response) }).catch(...)` | `index.mjs:13765-13775` |
| `apiCall`/`runFetch` (`await fetch`) | `index.mjs:13871-13877` |
| tslib `__asyncGenerator`/`__await`/`__asyncValues` (`yield __await(reader.read())`, `yield yield __await(new HttpResponse(...))`) | `index.mjs:13780-13830` |
| Web Streams `reader.read()`, buffered versus pending, in Node's `internal/webstreams/readablestream.js` | Node 24.19.0 |
| `new Response(text).json()` inside `chunk.json()` (native undici body mixin) | `index.mjs:15835` |
| second `__asyncGenerator` layer over the first (`for (... yield __await(apiResponse_2.next()) ...)` then `yield yield __await(typedResp)`) | `index.mjs:15825-15845` |
| Pi's `for await (const chunk of googleStream)` and `stream.push` | `google-generative-ai.ts:102,106` |

## Key observed stamps (Node 24.19.0)

The first segment (`seg 0`) is the synchronous call. `seg 1` is the fetch-resolution macrotask; later segments are later deliveries. Ticks are rounds after the segment's first chain step.

| Case | `sdk-resolved` | first `chunk` | second `chunk` | Pi `start` push | first content push | consumer `start` | consumer first content |
|---|---|---|---|---|---|---|---|
| tool/buffered | 1.12 | 1.20 | | 1.14 | 1.22 | 1.17 | 1.25 (already `toolUse`) |
| tool/pending | 1.12 | 2.8 | | 1.14 | 2.8 | 1.17 | 2.11 |
| text2/buffered-split | 1.12 | 1.20 | 1.28 | 1.14 | 1.22 | 1.17 | 1.25 |
| text2/pending-split | 1.12 | 2.8 | 3.8 | 1.14 | 2.8 | 1.17 | 2.11 |

Full data for all ten cases is in `pi.json`. Observations that constrain the Go model:

1. `start` is pushed exactly 2 rounds after the SDK stream resolves (1.12 to 1.14) and 6 rounds before a buffered first chunk arrives, in every case. A pending body never changes the start tick.
2. A buffered first chunk arrives 8 rounds after `sdk-resolved`. A pending chunk arrives 8 rounds after its delivery macrotask begins. Each further buffered record costs 8 more rounds (chunk 0 to chunk 1: 8 for split, 7 for joined records that share one `read()`), a fragmented record costs one extra round for the additional `read()` (tool/fragmented-buffered chunk at 1.21).
3. The consumer receives `start` (1.17) three rounds after the push (1.14). Its snapshot is still empty and `pending`, because the body is not yet read. On a buffered body the consumer's next event (`toolcall_start`, 1.25) already shows the completed tool call and `toolUse`: the whole body pipeline ran before that resume. A consumer reads the provider's shared `output` object, so its view depends on how many rounds separate it from the provider. Real Pi adds the `lazyStream`, Agent and Session rounds (plan §1), which is why the full RPC path measures a different start state.

## Reproduce

```bash
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
node ai/testdata/google-tick-order/probe.mjs --check ai/testdata/google-tick-order/pi.json
```

`--check` fails on any stamp difference and never rewrites the golden. To regenerate after a reviewed Pi or SDK pin change, run `node probe.mjs pi.json` under Node 24.19.0 three times and confirm the outputs are identical before accepting.

## Go replay

The Go replay belongs to W5 (Google transcription). It must feed each case's `deliveries` through the modeled Node stream and assert every `(seg, tick)` for the `sdk` and `provider` timelines, and the snapshots for the `consumer` timeline through the executor. No Go code reads this fixture yet.
