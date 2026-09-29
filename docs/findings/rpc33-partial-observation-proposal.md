# RPC33 partial-observation proposal

## Owner decision (2026-09-28)

Owner Michael Kinsy approves recording cross-process partial-message observation as a visible known gap for 0.3.x, now D82 in [docs/parity/DIVERGENCES.md](../parity/DIVERGENCES.md). This supersedes the earlier no-divergence decision below only for foreign partial snapshots and the three named scratch properties. Same-process observation, start admission, delta arguments, event order, cancellation and final/persisted results are not waived. Keep the strict RPC33 scenario enabled and retain its raw failure; approval is not a normalized pass or full observation closure. [The integration record](0.3.0-known-gaps.md) distinguishes the tested candidate from subsequent implementation checkpoints.

## Earlier decision and investigation (superseded in the limited D82 scope)

**The owner selects the full Pi-faithful observation design, without a divergence.** The `rpc33-observation` lane owns that work from the integrated B checkpoint. The exception considered below is not selected or approved. Keep the strict `rpc/33-rpc-real-provider-records` comparator unchanged and red until the owning implementation is qualified. This document retains the investigation and rejected option; it does not permit release or partial-observation drift.

The restart examines candidate `7121b8dc7d453652d484fdbcc27a252343dd4030` against Pi 0.87.1. The existing [observation finding](rpc33-partial-observation.md) remains applicable. The bounded investigation tests a general immutable-version implementation instead of reintroducing shared mutable Go pointers.

The investigation presented the full observation/continuation design and the following limited alternative. The owner chooses the full design, not this exception:

> Native Go provider streams expose independently owned nonterminal partial-message snapshots instead of Pi's live JavaScript message references. A snapshot does not advance after delivery or acquire later mutations while queued. The derived Agent `message_start` and `message_update` therefore need not contain the later content, usage or stop state that Pi observes through its asynchronous forwarding and shallow copies. Intermediate snapshots omit only the named parser scratch properties `partialArgs` and `streamIndex` for Completions, and `partialJson` for Responses. Final messages do not retain those properties in either implementation.

This proposed scope is limited to partial-message observation and the named scratch properties. It does **not** permit missing events, a different event order, incorrect delta payloads, missing parsed tool arguments at their own emission boundary, delayed stream admission, different terminal results, different persisted messages, dropped cancellation, changed tool execution, or transport-frame aliasing. It does not exempt arbitrary extension-provider data or other scratch properties without separate evidence and review.

If approved, the owner must allocate an ID, add scrutinized/remove-when accounting and production markers, document the affected API and RPC observation points, and add an explicit allowance that preserves the raw failure. Approval must not turn the current strict comparison into a passing normalized comparison. Until that work is reviewed, RPC33 remains an open parity defect.

## Upstream observation boundaries

All source references below are under `.upstream/current/` at Pi 0.87.1.

| Boundary | Source contract |
|---|---|
| Reference queue | `packages/ai/src/utils/event-stream.ts:44-63,78-91` queues the event itself and yields it through an async iterator. It does not clone the partial. |
| Async forwarding | `packages/ai/src/api/lazy.ts:31-39,46-61` awaits source iteration and forwards the same event. `packages/coding-agent/src/core/model-runtime.ts:638-643` selects this mechanism for `streamSimple`. Provider composition adds forwarding at `packages/coding-agent/src/core/provider-composer.ts:499-513`. |
| Completions | `packages/ai/src/api/openai-completions.ts:379,427-470,553-680` publishes start, mutates shared blocks while processing chunks, and deletes parser scratch properties when finalizing blocks. |
| Responses | `packages/ai/src/api/openai-responses.ts:178-180,197-212` publishes start before processing the body and terminates after finalization. `openai-responses-shared.ts:485-502,653-658,709-726` exposes then removes the tool-call scratch buffer. |
| Agent | `packages/agent/src/agent-loop.ts:408-453` awaits iteration and the event sink, but copies messages only shallowly. A copied top-level stop reason can remain unchanged while the shared content array advances. |
| Session and RPC | `packages/coding-agent/src/core/agent-session.ts:894-919` awaits extension-event handling before notifying subscribers. `packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363` serializes the mode event. The observation point is not the provider's push call. |

The strict candidate pair again fails at `/7/message/content`: PiG has no content at assistant start; Pi has a parsed tool call, `partialArgs`, `streamIndex: 0`, and `toolUse`. No scenario or comparison field changes during this investigation.

## New probes

The retained Node probe loads the exact published Pi provider implementations, `lazyStream`, `ModelRuntime`, and `runAgentLoop`. A loopback HTTP server owns each response. It exercises the Cartesian product of two APIs, text/thinking/tool content, direct/one-forwarder/two-forwarder/real-Model-Runtime paths, and nine consumption/admission modes. The modes cover immediately buffered frames, a first body chunk gated by observed start, a held direct consumer, result before iteration, cancellation, an Agent sink, and held/delayed Agent sinks. Each of three complete executions finishes all combinations. This is diagnostic evidence, not a new parity-coverage claim.

The delayed-body cases use an explicit release promise, not sleeps. The held-listener cases await the actual stream result before taking their second observation. Every server, response and cancellation controller is closed by its owner. Complete event objects are retained; the table only summarizes the relevant observations.

| Probe | Pi observation |
|---|---|
| Direct Completions provider, immediate tool chunk | Start is empty and pending at direct consumer admission. |
| Real Model Runtime, same immediate Completions tool chunk | Start contains the parsed tool call and both Completions scratch fields; stop reason is toolUse. This reproduces the strict RPC33 distinction through the real runtime. |
| Real Model Runtime, two buffered Completions text chunks | Initial content contains the first chunk, not an unconditional copy of the final result. |
| Responses direct and Model Runtime, immediate tool records | Start is empty and pending. A fixed Completions-style lookahead is not a shared API rule. |
| Delayed first body chunk, both APIs | Start is observable while the body is still held. Waiting for content to publish start is incorrect. |
| Held direct consumer, both APIs | Its retained start partial reaches final content and stop reason after result. |
| Held Agent sink after a delayed start, both APIs | Its retained message acquires final nested content, but its copied top-level stop reason remains pending. One whole-message final snapshot cannot reproduce this mixed state. |
| Result before iteration, both APIs | Queued nonterminal partials expose finalized state when iteration begins. |
| Cancellation after headers, both APIs | Start precedes cancellation and the terminal result is aborted. |
| Finalized tool calls, both APIs | Named scratch properties are absent from the final result. |

## General safe prototype and rejection

A disposable Go build overlay publishes an immutable producer-owned message version at every push. At iterator admission, every nonterminal event variant takes an independently owned copy of the latest published version. The same rule handles start, text, thinking and tool events. No consumer reads the producer's mutable message, and no delivered partial shares mutable storage with that message. Production source is not edited.

The prototype compiles. The FIFO/cancellation/result/ownership race cohort passes for both the current implementation and the prototype. The queued-after-result probe changes from red to green, as Pi requires. The existing `TestAssistantStreamBuilderNonterminalEventsRetainEmissionState` becomes red because it asserts the opposite, Go-only emission-time rule. That test is retained unchanged; it is not treated as upstream evidence.

The strict RPC33 pair still fails, now at `/7/message/content/0/partialArgs`: the prototype omits Pi's transient field. The producer can also finish before a queued event is admitted; selecting the latest version is not a deterministic model of Pi's forwarding continuations. It cannot update the nested content of an already delivered shallow Agent message without another synchronized observation mechanism. Passing the ownership test therefore does not qualify the prototype as faithful. It is not landed.

Moving the snapshot to after the next provider iterator step also needs an explicit distinction between an immediately ready continuation and a wait for network input. An unconditional lookahead can block the start/body-release handshake. Merely checking a Go receive buffer cannot establish Pi's async-generator and forwarding order. A per-start or per-tool workaround is rejected.

A faithful implementation needs producer-owned versioned message state and an owned continuation/observation executor across provider, forwarding, Agent and Session boundaries. It must define how retained Go consumers safely observe nested revisions without unsynchronized mutation of public structs. It must preserve result-only progress, cancellation, cleanup and frame-encoder snapshot ownership. Adding transient fields alone, or removing the existing snapshot, does not provide that design.

## Fixable prerequisites excluded from the proposal

Two additional boundaries are independently source-red on the unchanged candidate. They remain explicit owner-action blockers, not permitted differences under the proposed exception:

1. **Start admission before body data.** `TestRPC33StartBeforeFirstChunk` drives both real Go providers against a server that flushes successful headers and withholds its body. Both fail to produce start until the body is released. Pi produces start before reading that body (`openai-completions.ts:379,553`; `openai-responses.ts:178-180`). Go creates the stream builder at `ai/openai.go:1688` and `ai/openai_responses.go:1091`, but starts it only from later builder operations. A root fix must emit start after successful response setup, without turning setup failures into successful starts, and must test cancellation and the Model Runtime caller path.
2. **Parsed tool-delta arguments.** `TestRPC33ToolDeltaHasParsedArguments` observes the shared builder's delta before finalization. Both API cases expose an empty argument object. `ai/stream_builder.go:194-212` accumulates raw arguments; parsing occurs only in `endToolCall` at 311-320. Pi parses before publishing each delta (`openai-completions.ts:644-657`; `openai-responses-shared.ts:653-658`). A root fix must preserve incomplete-JSON salvage and custom/grammar tool behavior across all users of the shared builder.

The first draft of the tool-delta probe iterated after finalization. The immutable-version prototype made that draft pass by observing final arguments, without fixing delta-time parsing. The probe is corrected to observe before finalization; it fails behaviorally on both current source and the prototype. This correction is retained as evidence of the observation-boundary hazard, not as a green result.

Batch B already carries the parsed-argument repair through stream-A `8f8161af9`, attributed to `ec68f33fe7adc26d424f2ca65527ef8c02f71e22`. The frozen B builder explicitly parses accumulated arguments before publishing the delta. That input is not in `7121b8dc7`, and its intake/qualification is separate; do not duplicate its repair. Batch B confirms that neither provider's pre-body start admission is fixed there.

No source repair is authored by this proposal-only change. The coordinated B intake now includes the parsed-argument repair, and the corrected pre-finalization probe passes both API cases on that integrated tree (`batch-parsed-arguments-green.log`). Start admission belongs to the selected faithful-observation work. Neither prerequisite is folded into permission for snapshot drift, and the argument fix alone does not close RPC33.

## Evidence and reproduction

Evidence root: the retained `integrate-030-r2/` evidence directory, whose location is recorded in the maintainer handoff. Tests and probes use private HOME, Pi and PiG agent directories. The toolchain is Go 1.27.1, Node 24.19.0, Python 3.12.3 and Pi 0.87.1. No credentials or worker settings are read. No experimental overlay is installed in the worktree.

| Artifact | Purpose |
|---|---|
| `rpc-json-results.json`, `rpc-json-artifacts/` | Fresh unchanged-candidate strict run:47/48, only RPC33 red. |
| `rpc33-probe.mjs`, `rpc33-pi-final-{1,2,3}.json/.log/.exit` | Repeated exact-Pi provider, forwarding, Model Runtime and Agent observations. Earlier `rpc33-pi-*` files retain the intermediate probe development; use `final-*` for the complete matrix. |
| `rpc33-observation-probe_test.go.txt`, `rpc33-observation-overlay.json`, `rpc33-boundaries-red.log` | Compiling source-red probes for delayed admission, result-only reference observation and pre-finalization parsed arguments. |
| `observation-stream.go.txt`, `observation-helper.go.txt`, `observation-prototype-*.json` | Disposable general immutable-version implementation and its Go build overlays. |
| `observation-prototype-boundaries.log`, `observation-prototype-build.log`, `observation-prototype-results.json`, `observation-prototype-artifacts/` | Compiling candidate, boundary failures and strict paired rejection. |
| `observation-{current,prototype}-race.log` | Current and prototype stream ownership/FIFO/cancellation/result cohort, including concurrent producer/serializer control. |
| `observation-current-profile.log`, `observation-current-{cpu,mem}.pprof` | Existing Model Runtime simple-reply benchmark/profile, three samples. This is dispatch-path characterization, not full network-path qualification or a speedup claim. |
| `isolated.sh`, `prototype.sh`, `commands.log`, `rpc33-box-start` | Exact invocation and environment records. |

From the repository root, `bash <evidence>/isolated.sh node <evidence>/rpc33-probe.mjs <output.json>` repeats the Pi matrix. `bash <evidence>/isolated.sh bash <evidence>/prototype.sh` repeats the compiling Go probes and strict prototype comparison. The overlay paths bind this worktree; regenerate those path bindings for another checkout. The canonical acceptance command remains the unchanged strict RPC33 scenario, not the diagnostic summaries above.

## Faithful-design acceptance criteria

The selected race-free shared observation design must pass the unmodified strict RPC33 scenario and the direct-provider/Model-Runtime/Agent matrix at declared durability. Independently close both fixable prerequisites. Exercise text, thinking and tools; empty and multiple buffered chunks; delayed first data; held listeners; result without iteration; cancellation before/during/after iteration; final scratch cleanup; persisted history; and the existing frame encoder. Preserve event and record order, complete field presence and types, tool arguments and final result identity. Profile the actual production path and account for retained versions and queued-event lifetime. Do not claim closure from a compiling API or a single successful canonical fixture.
