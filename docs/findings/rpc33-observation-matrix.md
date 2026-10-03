# RPC33 observation matrix

## Status

This harness is source-red, not accepted observation parity. It supplements the unchanged `rpc/33-rpc-real-provider-records` scenario. It does not replace the complete RPC path, authorize a divergence, or change a comparator.

The owned test is `coding/rpc33_observation_matrix_test.go`. The exact-Pi probe, raw oracle and exported input bodies are under `coding/testdata/rpc33-observation/`. The base is `fc8d538606d1d338734b82b10ec566a18be082b1`.

## Independent denominator and oracle

The matrix derives its denominator from the Cartesian product of the probe's axes, not an observed Go count:

| Axis | Values |
|---|---|
| API | OpenAI Completions, OpenAI Responses |
| Content | text, thinking, tool |
| Path | direct, one `lazyStream` forwarder, two forwarders, real Model Runtime |
| Mode | immediate-direct, delayed-direct, held-direct, result-only, cancel, immediate-agent, delayed-agent, held-agent, delayed-held-agent |

The product has 216 combinations. The test checks the oracle's length, every case identity and the complete axis order before comparison. `inputs.json` is exported by the Node probe's actual HTTP-body function. Go serves those exact bodies from `httptest.Server` and invokes the real providers, `ai.LazyStream`, Model Runtime and Agent. No fake provider or synthesized partial message supplies expected content.

The probe pins Pi 1.0.0 and OpenAI 7.19.0. The maintained probe preserves the retained probe's operations and observations. It adds version checks, a body/axis export and terminal/result identity assertions. It does not alter continuation timing. Fresh outputs match all three retained `rpc33-pi-final-{1,2,3}.json` runs and the parent's `pi-matrix.json` after message-clock canonicalization. The checked-in `pi.json` is the first execution of the maintained probe against the real Pi 1.0.0 package, not Go output.

## Observations and ownership

Each entry is serialized immediately through the production message and provider-event marshalers. A held consumer serializes the same retained event again after `Result`. Result-only consumption awaits the producer before starting iteration. The direct terminal event and repeated `Result` calls must return the identical pointer; the maintained Pi probe asserts the corresponding object identity.

A delayed body is released only by the consumer's observed assistant start. A cancellation mode cancels the provider at start and still drains the iterator. The server sends immediate bodies in one write, just as Node calls `res.end(frames.join(''))`. No sleeps, retries, chunk lookahead, event-specific delays or tool-specific delays participate in observation.

The two-second per-case deadline detects a missing body/start handshake. Expiry fails the row and cancels the request; it never releases body data to manufacture start. The Go test timeout detects failures that do not drain after cancellation. These are failure bounds, not successful scheduling rules. Every server closes and joins its handlers. Agent execution is synchronous and is drained before return. Terminal iteration drains lazy forwarders. The harness adds no detached consumer goroutine.

Agent mode installs no tools and sets `FinishTurn` to `end`. In the exact Pi oracle, a tool-call response still emits `Tool read not found` execution events and a tool-result message. The test retains those records. The finish decision prevents a second provider turn; the server independently checks the single request.

## Strict comparison and adapter boundaries

The comparator compares full JSON structures. It retains field presence, JSON types, array order, delta bytes, all message metadata, parser scratch fields and stop state. It does not reconstruct messages from deltas. Object property insertion order is not part of this parsed-record comparison. Only numeric `timestamp` properties on assistant and tool-result message objects are canonicalized. Missing, null and string timestamps remain distinct. The fixed user timestamp, tool arguments, details and delta payloads remain unchanged.

`TestRPC33ObservationComparator` rejects changes to missing/null/string clocks, user and argument timestamps, scratch deletion/null state, numeric scratch-index type, delta data, stop reasons, array order, array/null distinctions and absent properties. Its clock-value mutation is the only accepted difference.

Go `agent.AgentEvent` has no wire marshaler. The test supplies a narrow tagged-union adapter to the upstream `runAgentLoop` event shape. The adapter does not use RPC's reduced `message_update`: both the full Agent message and the full embedded provider event remain present. These implementation-only projections apply identically to both APIs:

| Native Go shape | Upstream Agent observation | Justification |
|---|---|---|
| Concrete event struct name and exported field casing | `type` discriminator and upstream property names | Go represents the upstream closed event union with concrete structs. |
| `TurnStartEvent.TurnIndex` and `.Timestamp`; `TurnEndEvent.TurnIndex` and persistence-entry IDs | No such fields | The oracle calls `runAgentLoop`, not Session persistence or timing. |
| `AgentEndEvent.WillRetry` | No such field | Session retry augmentation is outside `runAgentLoop`. |
| `TimingEvent` | No record | Go exposes a timing-recorder side channel; the production RPC adapter also excludes this event type. |
| Nil `TurnEndEvent.ToolResults` slice | Empty array | The upstream loop constructs `[]`; Go's production wire adapter iterates the slice into a non-nil array. No message content array is normalized. |
| `ToolExecutionEndEvent.Result.IsError` | Top-level `isError` | Upstream `AgentToolResult` and the execution event carry this value in different places. Content, details and usage are retained. |
| Tool execution label, duration, preview and termination bookkeeping | No added fields | The oracle's unregistered tool has no tool implementation or presentation. The raw native event remains retained. |

Every native Agent callback, including timing events and Go-only fields, is serialized into a separate `.native.json` artifact before projection. This table is a harness interface mapping, not an allowance for partial-message differences. Unknown event variants fail. Provider and message properties receive no shape correction.

## Red evidence

External evidence root: the `rpc33-observation-h4` evidence directory recorded in the maintainer handoff.

| Execution | Result | Artifacts |
|---|---|---|
| Unchanged base | All 216 cases mismatch | `base/run-782237248/`, `base.log` |
| Base durability, three complete executions | All 216 cases mismatch in each execution | `base-durability/run-{2423550473,697313067,41732655}/`, `base-durability.log` |
| Parent's start-admission prerequisite only, three executions | All delayed handshakes finish; 200 cases still mismatch in each execution | `start-only/run-{2450960557,2472077989,1781002262}/`, `start-only.log` |
| Final harness source, base durability | All 216 cases mismatch in each of three executions | `final-base/run-{2246919016,4210613496,501357732}/`, `final-base.log` |
| Final harness source, start-only prerequisite | 200 cases mismatch in each of three executions; no handshake failures | `final-start-only/run-{564344399,2032489884,1621744426}/`, `final-start-only.log` |
| Comparator mutation guards, three executions | Pass | `comparator.log` |

Each execution retains aggregate `go.json`, per-case complete Go records and complete unified diffs. Later runs also retain raw native Agent records. The start-only overlay copies only the parent's two OpenAI provider files; `start-only-snapshot/sha256.json` and `overlay.json` bind that exact prerequisite. It does not use the parent's unfinished executor or helper source.

The source-red boundaries include late start admission, premature response ID presence, emission-time rather than observation-time partials, missing scratch state, pending stop reasons after terminal chunks, and retained Agent content that does not advance. The start prerequisite removes the handshake failure but does not fix these observations. Cancellation also exposes a separate exact error-text defect: Go reports `context canceled`, while both Pi APIs report `Request was aborted`. That difference remains asserted, not normalized.

Native and Windows test compilation and vet pass for `coding`. Full `go vet ./...`, `make lint`, `make lint-changed LINT_BASE=HEAD`, touched-package lint and lint configuration checks pass without suppressions. The full `coding` test suite fails only the new matrix (`coding-full.log`). A race-enabled complete matrix also fails behaviorally, without data-race diagnostics (`race.log`). These are not green matrix or race-acceptance claims. An initial lint command collided with another lane's global lock; the subsequent command uses the linter's serial-runner option. This is tool serialization, not a behavior-test retry.

## Exact dependency continuation findings

The disposable `trace-loader.mjs` instruments the actually loaded Pi/OpenAI modules. It writes the transformed module sources under `instrumented/`. It adds synchronous trace records and one independent `queueMicrotask` checkpoint before each existing yield. It adds no await, promise reaction, body buffering or consumer delay. `trace-probe.mjs` records the server write and consumer entry boundaries. All 216 instrumented outputs equal the uninstrumented oracle excluding message clocks. Full traces are in `pi-traced.json.trace.json`; full observations are in `pi-traced.json`.

OpenAI 6.40.0 uses the native async iterator branch in `internal/shims.mjs:41-43`, not its `reader.read` fallback. The actual chain is Node ReadableStream iterator → `iterSSEChunks` → `_iterSSEMessages` → `Stream.fromSSEResponse` iterator → Pi provider loop. The OpenAI implementation is `core/streaming.mjs:28-48,191-245`.

For immediate direct consumption, the server writes the body before Pi pushes start, but the consumer observes start before the native body iterator resumes. For immediate Model Runtime consumption, body iteration resumes after the first start forwarder and before the second. The third forwarder runs after the first SSE line yield and before its JSON yield resumes. Pi handles the first provider record before the final consumer sees start. Completions' first record mutates content; Responses' first record is `response.created`. The different initial content follows the same iterator graph, not an API-specific lookahead count.

For delayed Model Runtime consumption, all three start forwarders and the consumer start run before the server writes any body. After Pi handles a buffered record, the nested generator resumptions unwind synchronously to the next `iterSSEChunks` yield before the pending Pi event waiter resumes. Assigning a pending reaction to every generator resumption is incorrect.

The running Node 24.19.0 exposes its exact built-in implementation through `process.binding('natives')['internal/webstreams/readablestream']`. The retained `node-readable-stream.js:551-568` deliberately schedules the first `nextSteps` with `PromiseResolve().then(nextSteps)` and adopts the returned read-request promise. Chunk delivery at `821-824` clears the current read and resolves that promise. Subsequent buffered default-controller reads use an immediately resolved promise at `571-594`; subsequent pending reads call `nextSteps` at `597`. A plain Go buffered-channel readiness check does not implement those rules.

These findings and exact commands are also in the external `rpc33-observation-h4-microtasks.md` handoff. They establish the observed graph, not a proof that Go transport readiness or the complete parent scheduler already matches it.

## Acceptance blocker

The retained-view, scratch-lifecycle, continuation, and Agent/Session changes belong to the parent and h1/h2/h3. Their complete merged source is not yet available for this harness's final green qualification. The source-red tests must not be silently installed as accepted default tests. Parent integration must run all combinations at the declared three-run durability, retain the complete raw records, and then run the unmodified strict RPC33 and family gates. No helper-only result closes the parent task.
