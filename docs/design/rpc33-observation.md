# RPC33 observation and continuation

## Contract

Pi 0.87.1 is the reference implementation. This is the Option B design. It does not authorize a divergence or a weaker comparator. The acceptance command remains the unchanged `rpc/33-rpc-real-provider-records` scenario. The retained Node matrix is a second oracle, not a replacement for the complete RPC path.

The unit of work is a Provider → Event Stream → forwarding → Agent → Session → RPC path. A provider push is not an observation boundary. Pi queues references and runs promise reactions on one JavaScript thread. Both the reference identity and the ordering of reactions affect the serialized result.

## Source worksheet

| Source under `.upstream/current/` | Rule |
|---|---|
| `packages/ai/src/utils/event-stream.ts:44-91` | Push queues the reference or resolves the oldest waiter. An async generator awaits waiter resolution and yields the reference. Result resolves independently of iteration. |
| `packages/ai/src/api/lazy.ts:31-61` | Setup and forwarding are separate promise continuations. Forwarding preserves event identity. |
| `packages/coding-agent/src/core/model-runtime.ts:638-643` | Simple streaming adds an auth/setup and forwarding boundary. |
| `packages/coding-agent/src/core/provider-composer.ts:492-513` | Composition adds another forwarding boundary. API lazy loading adds the boundary in `lazy.ts:76-79`. |
| `packages/ai/src/api/openai-completions.ts:378-379,553-680` | Successful response observation precedes start. Start precedes the first body read. A complete chunk mutates the output synchronously, including stop state, before the next iterator await. |
| `packages/ai/src/api/openai-completions.ts:425-470,491-550,644-657,711-723` | Function tools expose `partialArgs` and optional `streamIndex`. Custom tools expose `customInput`. Finalization and errors delete scratch state. |
| `packages/ai/src/api/openai-responses.ts:177-180,197-216` | Start precedes body iteration. Response processing completes before the terminal push. Error cleanup removes scratch state. |
| `packages/ai/src/api/openai-responses-shared.ts:485-529,653-676,709-739` | Function tools expose `partialJson`; custom tools expose `customInput`. Parsed arguments advance before delta publication. Finalization deletes scratch state. |
| `packages/agent/src/agent-loop.ts:408-453` | Agent messages shallow-copy top-level fields. The copied stop reason stays fixed, but nested content retains the provider reference. Event sinks are awaited. |
| `packages/coding-agent/src/core/agent-session.ts:894-919` | Extension handling precedes subscriber notification. |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363` | Serialization is an observation, not a reconstruction from deltas. |

This is required upstream-parity substrate, not a product capability.

## Representation

Use producer-owned mutable construction state and independently owned immutable published revisions. Do not let a consumer read the construction state. A synchronized reference cell owns the current revision. Queue entries and forwarded events carry a view of that cell rather than a private emission-time revision.

A full view observes the cell's current message. An Agent shallow view pins top-level scalar values at the Agent copy boundary and retains references to nested objects. In particular, content is a referenced array; usage replacement and usage mutation are different operations. A whole-message snapshot or one shared content slice does not represent those distinctions.

Go exported struct fields cannot update safely behind an arbitrary reader. A retained view therefore needs an explicit observation operation that returns owned data. Serialization performs that operation too. Ordinary field access on returned owned data does not secretly race a writer. The API and its production callers must distinguish an owned snapshot from a retained view. The transport frame encoder continues to own an immutable snapshot; encoding a second frame must not revise the first frame.

The reference cell retains its latest revision, not an append-only history. An observation retains the revision it uses until it releases it. Queued events retain the cell, not every historical revision. Consumed FIFO slots are cleared. A terminal message is immutable after provider ownership ends, and the stream preserves its existing terminal-result identity guarantee.

## Continuations

Use one owned cooperative continuation executor per streaming path. Separate ready promise reactions from external completions. Run ready reactions in FIFO order. When a reaction awaits an unresolved operation, register its continuation and release execution ownership. When it awaits an already resolved promise, queue its continuation; do not execute it inline. Iterator yield and next resolution are distinct operations. Model forwarding by those operations, not by a forwarding-depth multiplier or a fixed number of provider chunks.

The provider publishes a revision at the end of its synchronous segment, before it suspends. Several pushes inside one chunk share that segment. Result-only consumers do not hold up producer progress. A held event sink suspends its own continuation but does not suspend the producer. Cancellation removes only the canceled waiter; an already assigned event keeps its FIFO position. Shutdown joins external work and resolves the existing error/result path.

Network readiness must be explicit. A completed body read and a read still waiting for network input are not interchangeable. The executor must receive readiness from the transport, not infer it from scheduling a goroutine, a buffered channel length, a sleep, or an unconditional lookahead. Header success admits start even when the first body read stays unresolved. The exact OpenAI SDK's async generator boundaries belong in the translation alongside Pi's boundaries; counting only `await` tokens in Pi omits part of the path.

A Go callback that blocks on arbitrary work cannot implicitly announce JavaScript-style suspension. Managed callbacks must release executor ownership through the observation/await mechanism. Session extension dispatch, provider result waits, and forwarding must use that mechanism before claiming complete-path closure. Holding the executor through a blocking listener deadlocks the held-listener/result cases. Letting producers run freely through a listener makes observation depend on goroutine timing.

## Implementation sequence

1. Repair start admission after successful HTTP setup for Completions and Responses. Prove header-only admission, cancellation, setup failure, response-hook failure, and Model Runtime forwarding without changing queue representation.
2. Add the owned revision/view representation with tests for result-before-iteration and retained full/shallow views. Replace `TestAssistantStreamBuilderNonterminalEventsRetainEmissionState` with Pi-derived tests in this step, not before its replacement behavior exists.
3. Add the continuation executor and explicit network-readiness bridge. Port async iterator and forwarding operations from the current Pi and pinned OpenAI dependency. Exercise the same producer through direct, one-forwarder, two-forwarder, and Model Runtime paths.
4. Publish Completions and Responses scratch lifecycles and chunk-boundary stop state. Keep custom tool buffers and optional wire indexes distinct from the builder's internal indexes. Do not specialize the event stream for start, tool events, or either API.
5. Carry shallow views through Agent and observe them after awaited Session handling, at serialization. Keep persistence and terminal results free of scratch fields.
6. Compare all complete records in all 216 Node combinations, repeat the oracle, and run strict RPC33 unchanged. Run the RPC/JSON families, provider streams, Agent, Session, race, native/Windows vet, and lint. Retain profiles and cancellation/resource evidence.

## Evidence and acceptance

The retained oracle is `integrate-030-r2/rpc33-probe.mjs`, with complete records in `rpc33-pi-final-{1,2,3}.json`. Its denominator is two APIs × three content shapes × four forwarding paths × nine admission/consumption modes. Immediate and delayed body delivery, held listeners, result-only iteration, cancellation, and scratch cleanup are separate obligations. The runtime Completions start contains only the first buffered text chunk, not the final message. The same tool start contains parsed arguments and scratch state. Responses start stays empty. A delayed held Agent start acquires final nested content while its copied stop reason stays pending.

New evidence resides in the lane's external evidence directory. Tests must first fail behaviorally on the base or reject a compiling mutation. No snapshot-only prototype qualifies as this design. Strict RPC33 passing alone also does not qualify: all probe combinations and the retained ownership, FIFO, cancellation, result, and frame-encoding contracts must pass.

## Implementation checkpoint

The start-admission prerequisite has red/green direct-provider and Model Runtime tests. The parent also implements a cooperative promise-reaction executor and an async Event Stream iterator, and routes the AI lazy forwarders through them. The iterator models the separate pending-waiter await and yield-value await rather than advancing a fixed number of provider records.

A direct Pi probe schedules two nested `queueMicrotask` reactions around an iterator next. For a waiting iterator, observation follows both reactions. For a buffered iterator, observation occurs between them. The Go guard reproduces both orders. A compiling mutation that removes the yield await fails the guard. Another red/green guard rejects an extra reaction inserted between promise fulfillment and the awaiting continuation. These are primitive and forwarding proofs, not complete provider/Agent observation proof.

The four helper slices have landed their independent changes and the parent has imported them. Retained cells, the native OpenAI SDK generator pipeline, Agent/Session scopes, and the RPC subscriber observation point are integrated. The parent owns the executor, Event Stream, forwarding, and body-readiness integration. No helper changes the strict RPC33 comparator. `TestAssistantStreamBuilderNonterminalEventsRetainEmissionState` is replaced by source-red guards for queued final observations and delivered views advancing after Result.

The unchanged strict RPC33 scenario passes all three declared pairs. The complete 216-case matrix passes three normal executions and several ten-execution race cohorts. The RPC/JSON family subsequently passes all 48 outcomes after the paired test-faux fixture's intentionally separate initial snapshot is preserved. A later full AI/Agent/Coding race run exposes a consumer-admission ordering failure under contention: the provider can finish before the Agent attaches its first observer. The source repair attaches one shared reaction queue before the Agent publishes its active signal, owns stream creation through the awaited return, and reuses that caller continuation for iteration. This preserves the provider/subscriber signal identity rather than wrapping the signal at the StreamFn call. A controlled Pi Promise-reaction probe, an iterator-adoption source-red guard, and a compiling Agent scope-removal mutation prove the contract. The complete 216-case matrix then passes ten race executions concurrently with a passing full AI/Agent/Coding race cohort. These results do not close the complete qualification boundary.

A full repository test run subsequently exposes two cloning defects: observing a tool call with null arguments panics during historical Session serialization, and serialization-based copying turns numeric arguments into `json.Number` values in native history. The shared copier validates and creates owned JSON data once, then preserves native scalar types and nil containers without invoking custom marshalers a second time. Separate source-red observation tests and the existing Session replay and seven-realization tool-coercion conformance tests guard these boundaries.

Outstanding boundaries remain explicit: caller-supplied HTTP/2 and opaque response bodies do not expose readiness through the current Go HTTP client contract; TLS control writes need ownership proof; print/JSON still needs the awaited subscriber observation point; extension IPC and stdout backpressure need scoped waits after serialization; and malformed SDK JSON needs the exact V8 diagnostic/message path. Pi's `lazy.ts:52` invokes the synchronous setup prefix before returning its outer stream, whereas the current Go lazy setup callback runs in a later turn; general setup callbacks also need their synchronous prefix and first suspension represented explicitly. No divergence or fallback allowance is approved for these gaps.

## Status

The design defines the full acceptance boundary. The lane handoff records the restart index and exact qualification results. Passing RPC33 or the matrix in isolation does not close the remaining ordering and lifetime failures. The integrated source is a checkpoint, not a release-ready completion claim.
