# RPC33 partial-message observation

## Status

Closed for native observation by the gap-d82 integration (2026-09-29): strict RPC33 passes all declared pairs with no comparator change, and the same observation holds for every builtin API (see `plans/0.3.x/gap-d82.md` and D82). The remainder of this document is the historical investigation.

`rpc/33-rpc-real-provider-records` remains red on the reviewed integration candidate. Do not remove fields, normalize the initial message, or disable the scenario to close it. On 2026-09-28, owner Michael Kinsy approves the cross-process partial-observation limit as a visible 0.3.x known gap, D82. This supersedes the earlier no-divergence decision only within that scope. Native observation/admission defects and the other excluded behaviors remain implementation obligations. The [partial-observation proposal](rpc33-partial-observation-proposal.md) retains the rejected prototype and probes; [the integration record](0.3.0-known-gaps.md) distinguishes later checkpoints from this candidate. Approval is not passing parity or a closure waiver.

The byte-order input and shared Model Runtime/error repairs close JSON04 and print04. Retry reaction scheduling closes RPC17. Those changes do not close this separate provider/Agent observation contract.

## Observed difference

The canonical loopback server in `test/parity/runner/provider_fixture.go` sends a Completions chunk containing one read tool call, usage and `finish_reason: "tool_calls"`, followed by `[DONE]`.

At the first assistant `message_start`:

- PiG reports empty content and `stopReason: "pending"`.
- Pi reports the tool call with parsed arguments, `partialArgs`, `streamIndex: 0`, and `stopReason: "toolUse"`.
- Pi's later assistant `message_end` contains the finalized tool call without `partialArgs` or `streamIndex`.

The complete RPC/JSON-name run passes47/48 outcomes after the root routing/retry repairs. RPC33 is its only failed outcome. The scenario still compares complete parsed records, stderr, presence, types, array order and correlated identities.

## Source contract

Pi `packages/ai/src/utils/event-stream.ts` queues references. `packages/ai/src/api/openai-completions.ts:379-387` publishes `start` with the mutable output and declares streaming scratch fields. Its chunk loop mutates that output and the shared content blocks. `finishBlock` at425-470 finalizes tool arguments and deletes scratch fields in place. The final completion and error paths at680-713 also distinguish transient state from persisted results.

Pi `packages/agent/src/agent-loop.ts:408-453` consumes the stream through awaited iteration and emits shallow message copies. The observed message therefore depends on both shared nested values and the Promise continuation boundaries. It is not equivalent to copying the whole partial at the provider's push call.

PiG `ai/event_stream.go:Push` calls `snapshotAssistantEvent`. `agent/agent.go:consumeStream`, `agentAssistantMessage` and `cloneAssistantMessage` introduce additional copies. The Go tool-call data model does not carry the Completions parser's transient `partialArgs` and `streamIndex` fields.

## Investigation, not a proposed fix

A disposable build overlay removes only `snapshotAssistantEvent` from `Push`. This changes the failing comparison from empty content to missing `partialArgs`: the tool call and terminal stop reason become visible, but Pi's transient fields remain absent. Production source is unchanged by this overlay.

This overlay is not safe to land. A controlled concurrent producer/serializer test passes with current snapshotting and reports data races when only that snapshot is removed. It also fails the complete Pi output on missing transient fields. Merely copying later, delaying the start event until a particular chunk, or preserving scratch fields on finalized messages would change contracts that require separate proof.

`TestAssistantStreamBuilderNonterminalEventsRetainEmissionState` currently requires Go emission-time snapshots; it is not a port of Pi's reference-sharing behavior. A reviewed representation change must reconcile that local assumption against actual Pi observations rather than deleting assertions or accepting data races. The frame encoder's independently owned snapshot contract must remain separate from the in-process event queue's reference contract.

## Required closure evidence

Use a source-level ownership/continuation design, not a renderer correction. Probe immediate and delayed first chunks, several buffered chunks, held listeners, result-only consumers, cancellation, and tool-call finalization. Keep final messages free of transient parser state. Exercise both Completions and Responses when changing their shared builder or event stream. Retain race, resource-lifetime and benchmark evidence for the actual production path.

The exact public #70 build at `5a69ffda` also fails today's strict RPC33 and JSON04 tests. Neither scenario exists at that commit. Its RPC17 scenario asserts substrings rather than complete record order. Restoring that older code or comparator is not closure.

## Retained evidence

Under `tmp/integrate-030/`:

- `runtime-routing-pairs.*`: JSON04/print04 pass; RPC17/RPC33 before retry repair.
- `retry-scheduler-pairs.*`, `retry-batch-{red,green}.log`, and `retry-{return,queue}-mutation.*`: retry closure evidence.
- `post-routing-rpc-json.*` and `post-routing-rpc-json-artifacts/`:47/48 outcomes and the full RPC33 records.
- `unsafe-reference-probe.*` and `unsafe-reference-probe-artifacts/`: the non-production snapshot-removal experiment.
- `partial-reference-{safe,unsafe}-race.log`: identical producer/serializer inputs with and without emission-time snapshotting; the unsafe overlay races.
- `public70-strict-pairs.*` and `public70-strict-artifacts/`: exact public #70 comparison.

The public #70 source archive now resides outside the worktree at the integrator's probe evidence directory. Keeping a second Go tree under the repository made the source-call-site audit scan obsolete code. Moving that disposable probe, without changing the audit, restores the audit's intended input set.
