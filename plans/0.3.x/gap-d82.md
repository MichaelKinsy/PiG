# Plan: gap-d82, cross-process partial-message observation (RPC33)

Lane: `smc1/gap-d82`. Base: `release/0.3.0-base2` (`606595120`). Starting point: the lead's WIP port `team/lead/wip-d82-checkpoint-port` (`9488814fa`, `ffd13b867`). Both commits cherry-pick onto the base without conflicts and build.

## 1. Pi's observable contract

All citations are Pi 0.87.1 under `.upstream/current/packages/`.

Pi runs the provider, the event queue, the forwarders, the Agent, the Session and the RPC writer on one JavaScript thread. The serialized `message_start` is therefore a function of the microtask interleaving between the provider's body-reading pipeline and the consumer chain. It is not the state at `stream.push`.

| Boundary | Source | Rule |
|---|---|---|
| Event queue | `ai/src/utils/event-stream.ts:44-91` | `push` queues the event reference or resolves the oldest waiter. The async generator yields queued references; a waiting iterator awaits a new Promise, then yields. `result()` resolves independently of iteration. |
| Lazy forwarding | `ai/src/api/lazy.ts:31-61,76-79` | `setup()` runs its synchronous prefix before `lazyStream` returns. `.then(forwardStream)` adds a reaction; `forwardStream` awaits each source `next()` and pushes the same reference. `lazyApi` adds a second `lazyStream` layer. |
| Model Runtime | `coding-agent/src/core/model-runtime.ts:638-643` | `streamSimple` wraps auth/setup in another `lazyStream`. |
| Provider start | `ai/src/api/openai-completions.ts:371-379,553`; `openai-responses.ts:170-180`; `anthropic-messages.ts:587-601`; `google-generative-ai.ts:100-106`; `mistral-conversations.ts:151-153`; `bedrock-converse-stream.ts:286-301` | Start is pushed after response headers (Bedrock: on the first `messageStart` item) and before the first body read. The body pipeline then mutates `output` in place. |
| Body pipelines | OpenAI SDK 6.40.0 `core/streaming.mjs:28-48,191-245`; Pi `anthropic-messages.ts:411-500`; `mistral-conversations.ts:450-600`; `pi-messages.ts:280-420`; `openai-codex-responses.ts:720-800,1450`; `@google/genai` 2.21.0 `dist/node/index.mjs:7729-7751,13780-13800,15635-15683` (tslib `__asyncGenerator`); AWS SDK `@smithy/core` event-streams over `@smithy/node-http-handler` (Node `Readable` async iterator) | Each layer's `await`, `yield`, `for await` and Promise adoption costs a specific number of microtasks. Node's Web Streams reader (`internal/webstreams/readablestream.js`) and Node `Readable` iterators have their own rules. |
| Scratch state | `openai-completions.ts:425-470,644-657`; `openai-responses-shared.ts:485-529,653-676`; `anthropic-messages.ts:672-760` | Completions exposes `partialArgs`/`streamIndex`, Responses `partialJson`, Anthropic `partialJson`/`index` while a tool call is open; finalization deletes them. |
| Agent | `agent/src/agent-loop.ts:408-453` | `{ ...partialMessage }` pins top-level scalars (for example `stopReason`) at the copy tick. `content`, `usage` objects and tool-call objects stay shared with the provider. |
| Session | `coding-agent/src/core/agent-session.ts:894-919` | `_handleAgentEvent` awaits `_emitExtensionEvent` before `_emit` to subscribers. |
| RPC | `coding-agent/src/modes/rpc/rpc-mode.ts:354-363` | The session subscriber serializes synchronously. A separate Agent listener awaits `waitForRawStdoutBackpressure()`. |

### Measured Pi behavior (new evidence)

Probe: real Pi 0.87.1 CLI in `--mode rpc --no-extensions`, one loopback server per API that writes headers and the complete RPC33-shaped tool-call body in one write. The value is the first assistant `message_start`. Eight runs each on Node 24.19.0 (`.node-version`) and Node 26.7.0. Scripts: `server.mjs`, `drive.mjs` in the lane evidence directory (they move into `coding/testdata/rpc33-observation/providers/` in step W6).

| API | Pi start (all 16 runs identical) | PiG base | WIP port |
|---|---|---|---|
| openai-completions | tool call with parsed args, `partialArgs`, `streamIndex:0`; `toolUse`; usage 13 | empty, `pending` | = Pi |
| openai-responses | empty, `pending`, `responseId` set | = Pi | = Pi |
| anthropic-messages | tool call, `arguments:{}`, `partialJson:""`, `index:0`; `pending`; `responseId`; usage 11 | empty, `pending`, no `responseId`, usage 0 | = base |
| google-generative-ai | complete tool call; `toolUse`; usage 13 | empty, `pending` | complete, `toolUse` (id shape differs, see below) |
| mistral-conversations | complete tool call, no scratch; `toolUse`; usage 13 | empty, `pending` | = base |

Conclusions:

1. Pi is deterministic per API and identical across Node 24 and 26 for this input. The start state differs per API because each pipeline has a different microtask depth. No shared rule such as "one record of lookahead" reproduces all five.
2. Anthropic shows a mid-record state (two SSE records handled, the delta not yet). Exact parity therefore needs microtask-accurate modeling, not per-provider constants.
3. Separate drift found: Pi names generated Google tool-call IDs `${name}_${Date.now()}_${++counter}` (`google-generative-ai.ts:199`); PiG uses `name_N` (`ai/google.go:944`). This is fixed independently of D82 (W0).

## 2. Current PiG state and reusable checkpoints

Base (`606595120`): `ai/event_stream.go:Push` snapshots each nonterminal partial; `agent/agent.go` copies again. Deterministic, but every API except Responses differs from Pi. Strict RPC33 is red at `/7/message/content`.

WIP port (now cherry-picked onto the base as the lane's first commits):

- `ai/continuation.go`, `ai/stream_observation.go`, `ai/stream_continuation.go`: a cooperative promise-reaction executor with FIFO ready reactions, external completions, turns and awaits. Reusable as the core.
- `ai/event_stream_continuation.go`: the Pi `EventStream` async generator (buffered versus waiting `next`). Reusable.
- `ai/native_body_iterator.go`, `ai/http_body_observation.go`: body-read readiness (immediate versus pending) and Node's `ReadableStream.values()` first-read rule. Reusable; generalize for Web Streams `reader.read()`.
- `ai/openai_stream_decoder.go`: OpenAI SDK `iterSSEChunks`/`_iterSSEMessages`/`Stream` generator chain. Reusable for Completions, Responses and Azure Responses.
- `ai/assistant_observation.go`, `ai/assistant_publication.go`: producer-owned revisions in a cell, full and shallow views. Reusable, but the public surface is the stale trap (below).
- `coding/rpc33_observation_matrix_test.go` + `coding/testdata/rpc33-observation/`: the 216-case exact-Pi matrix for both OpenAI APIs. Reusable; extend to all APIs.
- `coding/session.go`, `cmd/pig/rpc_events.go`, `cmd/pig/print_mode.go`: Session and RPC/print observation points. Reusable.

Known defects in the WIP:

- D1 nondeterminism: Anthropic, Google, Bedrock, Mistral, Codex, pi-messages, faux and caller-supplied `Fetch` push from free goroutines into a live cell. The consumer observes whatever revision exists at its tick (the lead measured three Anthropic states in 200 runs).
- D2 stale trap: `cell.view()` returns one stable handle whose exported fields are frozen at the first view; `Observe()` returns the real state; `MarshalJSON` silently re-observes. Fields, marshaled JSON and `Observe()` can disagree.
- D3 named gaps: synchronous setup prefix of `lazyStream`, readiness for opaque/HTTP-2/custom `Fetch` bodies, scoped waits for extension IPC and stdout backpressure, and the malformed-SDK-JSON error path. The cancellation error text (`context canceled` versus Pi's `Request was aborted`) recorded in the matrix finding.
- D4 new exported surface (`StreamObservation`, `StreamContinuation`, `ObserveEvents`, `ForwardStream`, `Observe*`, `ShallowCopy`, `agent.EventObservation`) that is not on the base. It is unreleased, so it can be narrowed without a compatibility alias.

## 3. Design

### 3.1 Evaluation of the lead's direction

Put every builtin provider under the executor and yield where its JavaScript pipeline awaits: **accepted**. The measured table shows that anything less gives either Pi-different or timing-dependent states. Each provider's Go stream loop becomes one executor turn chain. Its body source is a modeled Node stream, and its decoder is a transcription of the exact Pi or SDK generator chain built from shared primitives (3.3), not hand-counted yields.

Emit an immutable snapshot at emission time: **accepted with a precise definition of "emission"**. A snapshot at `push` is the base's wrong answer. The snapshot is taken at each delivery boundary, when a consumer's continuation resumes with the event (3.2). That is the tick at which Pi code reading `event.partial` sees the same data, so exported fields are never stale at delivery.

### 3.2 Representation: delivery-time materialization

- The producer mutates private construction state and publishes a revision into its cell at the end of each synchronous segment (before it awaits). Queue entries carry the cell reference, not data.
- Every delivery boundary materializes an owned `AssistantMessage` at its tick: the `ai` iterator yield, the Agent event sink, `agent.Subscribe` listener dispatch, the Session's subscriber dispatch, and the extension-host serializer. Exported fields always equal the state at that delivery. `MarshalJSON` serializes fields only; there is no hidden re-observation.
- The Agent's shallow copy is a materialized message plus an unexported shallow link (pinned top-level values, referenced nested objects). Only PiG dispatchers use the link to re-materialize at a later tick, for example Session → RPC after the awaited extension emission. Consumers never need to call anything.
- Exported surface after the change: none of the WIP's `Observe*`/`ShallowCopy`/`StreamObservation`/`StreamContinuation` types stay exported unless a production caller outside the package needs them. Where a cross-package caller in PiG needs them (`agent`, `coding`, `cmd/pig`), they move behind `internal/` helpers or unexported accessors. `pig-go.json` is regenerated. There is no base API to alias because none of these existed in `606595120`.
- A Go consumer that retains a delivered message and reads it later sees the delivery state (ordinary Go value semantics, documented in `docs/site/docs/sdk.md`). A foreign listener (extension process) that retains an event likewise keeps its snapshot. That is the residual cross-process case, covered by narrowed D82 (section 7).

### 3.3 Execution: a microtask-accurate model of the JavaScript constructs involved

Shared primitives in `ai` (unexported), each with a direct Node differential tick-order test:

1. Promise, resolve, `then`, adoption of a thenable (the extra two reactions of `return promise` from an async function and of `resolve(promise)`).
2. `await` of a settled or pending value (one reaction versus registration).
3. Native async generator: `yield` (await of the yielded value, then resolution of the `next` promise), `return`, and the AsyncGenerator request queue.
4. tslib `__asyncGenerator`/`__await`/`__asyncValues` as compiled into `@google/genai` (different tick counts from native generators).
5. `for await` over an async iterator, including `return()` on early exit.
6. Node Web Streams: `ReadableStreamDefaultReader.read()` (buffered versus pending), `values()` iterator first-step rule (`PromiseResolve().then(nextSteps)`), cancel. Pinned to Node 24.19.0 `internal/webstreams/readablestream.js`.
7. Node `Readable` `Symbol.asyncIterator` (for the AWS SDK Node HTTP handler).
8. Readiness bridge: a body read either completes in the current turn (bytes already buffered by the Go transport) or becomes an external completion. It is supplied by the transport, never by a sleep, goroutine scheduling or a channel length. The WIP's `observedResponseBody` generalizes this.

Every provider's Go stream loop is rewritten as a turn over these primitives, in the same order as its Pi source. Each translation cites the Pi or SDK line of every await it models.

Executor ownership: one executor per streaming path, created at stream creation before any signal context is published (the WIP's admission fix). Ready reactions run FIFO before external completions. A Go callback that blocks (extension IPC, stdout write, tool approval) must release execution through `Await`. The executor never runs a user callback while another turn holds it.

### 3.4 Lifetime and cleanup

- The cell retains only the latest revision; a materialized message owns its data; no historical revisions accumulate. The WIP's `weak`/`runtime.AddCleanup` publication registry is replaced by the cell pointer carried in queue entries, which is dropped when the entry is consumed. This removes GC-dependent cleanup from the hot path.
- The body reader goroutine is owned by the provider turn: it is joined on `Close`, on cancellation and on error. The executor has no goroutine of its own; turns run on the goroutines that already exist (provider, consumer).
- Cancellation removes only the canceled waiter; an assigned event keeps its FIFO slot. Shutdown drains the executor and joins the body reader. Tests assert no leaked goroutines (`goleak`-style count checks already used in `ai`) and a bounded retained heap after 10k streamed events.

### 3.5 Reentrancy

- Posting a reaction never runs it inline in the current turn (FIFO only).
- A dispatcher that re-materializes does so while holding the turn; it never calls back into the provider.
- Extension-host IPC during dispatch releases the turn through `Await`, so a synchronous extension call back into the host (for example `getMessages`) cannot deadlock on the executor. A regression test drives an extension handler that calls a host method during `message_start`.

### 3.6 Failure modes

| Failure | Behavior |
|---|---|
| Setup or header failure | No start; the existing error path. Tested for both setup and `onResponse` hook failure. |
| Malformed SSE JSON | Pi's exact error text per pipeline (OpenAI SDK `JSON.parse` V8 message, Anthropic repair path). Existing tests plus one per API. |
| Cancellation before headers, after start, mid-body | Start precedes abort when headers arrived; terminal `aborted`; error text `Request was aborted` where Pi uses it. |
| A callback blocks without `Await` | Detected: turn held past a bound in race tests fails the test; production does not deadlock because provider body reads are external completions queued behind the turn. |
| Opaque `Fetch` body with no readiness | Treated as always pending (every read is external). This matches Pi for a real network and is deterministic. Recorded in the design doc. |

### 3.7 Windows, Linux, macOS

The model has no OS-dependent step. Readiness comes from the Go transport's own buffer (`bufio.Reader.Buffered()` on the connection reader via the observed body), not from socket syscalls. Loopback TCP coalescing differs per OS: a server's single write can arrive as one or two segments. Tests therefore serve fixtures through an in-process `net.Pipe`/`httptest` path where the delivery split is controlled, and the parity scenarios use the single-write fixture already proven on Linux. CI native vet runs for windows/darwin; the unit matrix runs on all three in CI.

## 4. Public API impact

- No base API is removed or changed. `ai.AssistantMessageEventStream.Events`, `Push`, `Result`, `agent.Subscribe` and event types keep their signatures and meaning. One meaning is corrected: a delivered partial's fields now describe the stream state at delivery rather than at `Push`. The base behavior was the D82 difference, so this is a parity fix, noted in `changelog.d/`.
- The WIP-only exported names are not released. They are unexported or moved to `internal/` before READY. Anything that must stay exported gets a doc comment stating its exact contract.
- No SDK wire change. `extensions/sdk*` are not touched; extension events stay JSON snapshots at dispatch.

## 5. Work breakdown

Each item is a focused commit with its own red-then-green test and is pushed when done.

- W0. Google tool-call ID shape: `name_<ms>_<counter>` as Pi. Unit test red on base. Separate from D82.
- W1. Rebase the WIP (done: cherry-picked). Re-run strict RPC33 and the 216 matrix on the rebased tree; record results.
- W2. Delivery-time materialization; remove the stale trap. Unexport the WIP surface. Tests: fields equal `json.Marshal` output at every boundary; held Go consumer keeps its delivery state; the Session re-materializes for RPC. Strict RPC33 and the matrix stay green.
- W3. Determinism first: every provider not yet transcribed runs its loop as an executor turn whose body reads are external completions. This makes every API deterministic (Pi-exact only for pending-body input) before per-provider work. 200-run determinism test per API.
- W4. Shared primitives 3.3 (items 1-7) with Node differential tick-order tests (a checked-in Node probe prints the tick order; Go replays it).
- W5. Per-provider transcription, one commit each, each flipping that API's row in the multi-provider oracle to exact: Anthropic, Mistral, pi-messages, Codex SSE, Azure Responses, Google (tslib generators), Bedrock (Node `Readable` + smithy event stream), faux and test-faux. Codex WebSocket mode stays external-completion only (no buffered-frame parity claim) unless probing shows a deterministic Pi state.
- W6. Multi-provider oracle: move `server.mjs`/`drive.mjs` into `coding/testdata/rpc33-observation/providers/`; generate `pi.json` per API; Go differential test through the real `cmd/pig` RPC path for every API; 200-run determinism per API.
- W7. Remaining D3 gaps: `lazyStream` synchronous setup prefix, extension IPC scoped wait, stdout backpressure wait (RPC Agent listener), print/JSON observation point, cancellation text.
- W8. Parity: RPC scenarios for Anthropic, Google and Mistral records with the runner's provider fixture extended by API (strict `json_output_equal`, `runs = 3`).
- W9. Docs, PORT_MAP, D82 narrowing or removal, `make generate`, READY.

Order of delivery if time runs short: W0, W1, W2, W3 are shippable on their own (exact OpenAI, deterministic everywhere, no trap). W5 items land independently.

## 6. Test and evidence plan

Nothing is loosened: RPC33 keeps `json_output_equal`, `stderr_equal`, `runs = 3`.

- Strict RPC33: 3/3 pairs, plus `make parity` for `rpc/*` and `json/*`.
- Exact-Pi matrix (216 cases, both OpenAI APIs), three complete executions and ten under `-race`.
- Multi-provider oracle (W6): every API's start state equals Pi's in 200 consecutive runs, using the real RPC binary path; red on base for every API except Responses.
- Primitive tick-order differential tests against Node 24.19.0 output (W4), each with a compiling mutation that changes one reaction and must fail.
- Ownership: race tests for producer versus materialization; goroutine-leak and retained-heap checks; benchmark and CPU/alloc profile for `Events` + Agent + Session dispatch on a 10k-delta stream, compared with base.
- Gates: `go build ./...`, `go vet ./...` (plus GOOS=windows/darwin vet), `-race -count=3` on `ai`, `agent`, `coding`, `cmd/pig`, `make lint`, `make test-porting-release`.

## 7. Conditions for removing or narrowing the record

D82 is removed only when all of the following hold with retained evidence:

1. Strict RPC33 passes 3/3 unchanged.
2. Every builtin API's start state equals Pi's in 200/200 runs through the RPC path, including scratch fields and top-level/nested mixed state.
3. The 216-case matrix, extended to every API, passes three executions and ten race executions.
4. Held listeners, result-only, cancellation, delayed and buffered input, and resource lifetime tests pass for every API.

The part that cannot be met in one Go process talking to another process is an extension (or other foreign listener) that retains a partial and reads it after an `await`, or whose handler spends a number of microtasks the host cannot see. If everything else closes, D82 is narrowed to exactly that scope (retained foreign partials and foreign handler tick counts), keeps its approval, strict scenarios and markers, and its remove-when names an owned cross-process reference mechanism. That narrowing is within the owner's existing 2026-09-28 approval, which already names the foreign scope; it does not need a new decision.

## 8. Integration contract for the provider lanes (integrator: gap-d82)

Landed on `team/smc1/gap-d82`; rebase or merge it before your READY.

1. **Managed provider.** A provider is under the executor when it (a) obtains its body as `*observedResponseBody` (PiG's HTTP transport; see `openai.go:1697`), (b) sets `builder.managed = true`, and (c) runs everything after the response headers inside `builder.responseTurn(...)` (`ai/native_body_iterator.go`), which sets `stream.producer` and gives the turn. Only then do pushes publish live cells; every other push stores an emission-time snapshot (W3, D82 marker in `assistant_publication.go`).
2. **One await, one call.** Each JavaScript `await`/generator boundary in the Pi or SDK source becomes `suspendContinuation(turn)` (already-resolved await) or `awaitContinuation(turn, promise)` (pending). Body reads go through `newNativeBodyIterator(...)`; set `reader.started = true` when Pi uses `getReader().read()` rather than `values().next()` (`pi_messages.go:96-99`). Cite the Pi or SDK line of every yield in a comment.
3. **Tests.** Use `runManagedBuilder` / `runAsExecutorProducer` (`ai/event_stream_test.go`) for producer-turn tests. A lane is closed for its API when `node coding/testdata/rpc33-observation/providers/check.mjs <pig> 200 <api>` reports `200/200 equal Pi` (add the API's fixture to `server.mjs` and oracle to `pi-start.json` if it is not there; regenerate the oracle with `--oracle`), plus a Pi tick-order oracle test like the OpenAI ones.
4. **Delivered partials (W2).** Iterators deliver a fresh handle whose exported fields equal the stream state at the consumer's tick. Do not call `Observe()` to read a delivered partial. Forwarders must use the raw iterator (`events(ctx, false)`).
5. **No new exported API** without the integrator. Extending unexported helpers in `ai` is fine.
6. **Environment.** `/home` is full: use `GOCACHE`, `GOMODCACHE` and `TMPDIR` under `/var/tmp` (the lane's `env.sh`).

## 9. Integration status (READY)

All units are merged on `team/smc1/gap-d82`: W0, W2, W3 (integrator), W4a/b/c primitives, W5 Anthropic, Azure, Bedrock, Codex (SSE), faux/test-faux, Google, Mistral, pi-messages, W7 and W8.

Integration fixes made here: the Anthropic undici read hops moved onto W4c's Web Streams body source; Anthropic and Bedrock share one open-block `index` scratch; the Google abort watcher is restored on the merged executor; provider hooks await on the running producer turn (parity extensions-runtime 50/51 hung); fixture sleeps are replaced with a socket-read barrier (reviewer blocker); RPC/JSON records go through output-guard's ordered tail (reviewer HIGH); three Pig fixture programs read request failures from the stream result as their Pi fixtures do.

Evidence (lane evidence directory):
- Strict `rpc/33-rpc-real-provider-records` and its Anthropic, Google and Mistral variants: 3/3 pairs each.
- `check.mjs <pig> 200`: 200/200 runs equal Pi, one state, for openai-completions, openai-responses, azure-openai-responses, openai-codex-responses, anthropic-messages, google-generative-ai, mistral-conversations, bedrock-converse-stream, pi-messages and test-faux.
- `rpc` family 45/45 and `json` family 4/4 at declared durability; `make parity-fast`: 604/605 hermetic scenarios. The one failure, `rpc/33-rpc-wire-mutations` (`estimatedTokensAfter` pig=788 pi=787), also fails on the unmodified release base 606595120 (run 3 of 8); it is a pre-existing intermittent outside D82 and is not fixed here.
- `go test -race -count=3 ./ai ./agent ./coding`; `TestRPC33AzureTickOrder` `-race -count=60` x3; Mistral and pi-messages RPC oracles with `message_update.usage` compared, 100 runs.
- `make lint` 0 issues; `make ci-drift`, `make divergence-quality`, `make divergence-guard`, `make interface-go-drift`, `make test-porting-release` pass.

Not closed (listed in D82 and the known-gaps ledger as open, not approved): `agent.StreamProxy`, the Codex WebSocket transport and Bedrock off an observable HTTP/1 connection still publish emission-time snapshots. D82 itself is narrowed to extension-process listeners and providers.
