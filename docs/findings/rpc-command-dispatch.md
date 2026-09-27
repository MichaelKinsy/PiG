# RPC command completion ordering: PR #58

This is the retained staging-campaign report for `30449a3dca`. Its verification results and public-main snapshot refer to that campaign. [The public-main integration report](port-rpc-parity-public.md) records the current base, adaptation, red/green proofs, and gates.

## Findings

The reported race exists in the model-cycle completion path. It is not strict serialization of every RPC command. Public main `713707ba70389232fa7fe2ac45c82ac29cbb33b3` and this lane's previous commit `2378e7363b` both launch `cycle_model` in a goroutine. Neither preserves the input callback's Promise boundary, so a ready cycle response can race the following `abort_retry`. This lane's earlier wire and shutdown fixes did not change that code.

The old `cycleComplete` chain is a separate serialization defect: it waits for the entire previous cycle, including extension listeners. Pi mutates each cycle's model synchronously before awaiting its `model_select` notification. A later cycle can finish while the first listener is still pending.

All Pi citations below refer to `.upstream/v0.87.1/packages/coding-agent/src/`.

## Exact dispatch contract

`modes/rpc/jsonl.ts:21-62` dispatches every complete line in an `onData` callback synchronously. `modes/rpc/rpc-mode.ts:810-811` calls `void handleInputLine(line)` for each line. It does not await one line before admitting the next. `handleInputLine` awaits `handleCommand` at line 786.

Consequently, a fulfilled Promise is not an immediate response. When cycle and abort-retry arrive in the same input callback, `cycle_model`'s inner await adds continuations, while `abort_retry` returns without an inner await. Abort-retry responds first. If a client sends cycle alone, waits for its response, and only then sends abort-retry, cycle necessarily responds first. There is no global command-priority sort or cross-read ordering guarantee.

### Complete 33-command inventory

| Class | Commands | Suspension and response rule |
|---|---|---|
| Inner await: queue | `steer`, `follow_up` | `rpc-mode.ts:418-425` awaits Session input handling/queueing; these can be overtaken by later non-awaiting handlers |
| Inner await: abort | `abort` | `428-430` awaits abort and idle completion; `agent-session.ts:2075-2092` includes an await even when already idle |
| Inner await: session replacement | `new_session`, `switch_session`, `fork`, `clone` | `437-444`, `605-630` await the runtime operation and, on success, rebinding; `clone` without a leaf returns its error before that await |
| Inner await: model | `set_model`, `cycle_model` | `472-487` awaits the model operation. A missing set_model target returns its error before awaiting. Cycle still awaits when it has only one model and returns null |
| Inner await: compaction | `compact` | `535-537` awaits Session compaction; an immediately rejected operation still crosses a Promise boundary |
| Inner await: shell | `bash` | `563-583` awaits the extension user_bash result, then execution unless an override supplies the result |
| Inner await: export | `export_html` | `600-602` awaits export, including its extension renderer work |
| No inner await: state and catalog | `get_state`, `get_available_models`, `get_available_thinking_levels`, `get_session_stats`, `get_fork_messages`, `get_entries`, `get_tree`, `get_last_assistant_text`, `get_messages`, `get_commands` | These return through the common outer await. Their bodies and snapshots run during admission |
| No inner await: mutations and immediate cancellation | `clear_queue`, `set_thinking_level`, `cycle_thinking_level`, `set_steering_mode`, `set_follow_up_mode`, `set_auto_compaction`, `set_auto_retry`, `abort_retry`, `abort_bash`, `set_session_name` | These also return through the common outer await. Synchronous state/event effects precede the response. Unawaited extension notifications do not make the command await their completion |
| Special callback-owned response | `prompt` | `394-415` starts prompt handling without awaiting it. The authoritative success is written by the preflight callback; a preflight rejection is written by catch. There is no ordinary returned response |

The default unknown-command response also has no inner await. JSON parse failures differ: `752-765` writes the error directly rather than awaiting handleCommand, so a parse error can precede an earlier valid command's response. `extension_ui_response` is not one of the 33 commands; it resolves a pending dialog and has no response of its own.

The inventory defines possible overtaking, not one fixed total order for every asynchronous pair. Conditional branches, pending I/O/listeners, nested awaits and input callback boundaries determine completion order. This patch closes model-cycle admission/completion ordering. It does not label every other handler's existing Go implementation as async-complete; the per-file async ledger remains deferred for those broader obligations.

## Root fix

- `coding/rpcclient/jsonl.go:ReadJSONLBatches` preserves complete records already available in one buffered read. It does not wait for a future line, add a debounce timer, or merge later reads. The existing line-reader API uses the same framing implementation, including CRLF, Unicode separators and a final unterminated line.
- `cmd/pig/rpc_dispatch.go:rpcResponseTurn` owns continuations that become ready during input admission. The shared executor gates completions from earlier requests against whichever input callback is currently active. It drains them after that batch, before waiting for future input. Producers enqueue without waiting for an active callback, so a command can join completed work without deadlocking publication. The idle handoff is atomic and nested continuations remain queued in order.
- `coding/session.go:BeginModelChange` applies model, transcript and thinking state before exposing the awaited notification completion. The existing blocking `SetModel` and `CycleToModel` APIs still invoke and await that completion. The RPC caller owns and joins notification work through its command wait group and cancellation context.
- `cmd/pig/rpc_mode.go` removes whole-cycle serialization. It executes each cycle's state prefix in input order and flushes synchronous thinking events before admitting later commands. Each notification completes independently.
- Response construction is a continuation too. `agent-session.ts:2211-2240` reads `this.thinkingLevel` after awaiting `_emitModelSelect`. Two same-batch cycles can therefore make the first response report the thinking level selected by the second prefix. Merely sorting already-constructed responses would remain wrong.

`BeginModelChange` is an explicit Go representation of the synchronous-prefix/await split, not a new wire or extension capability. Go has no Promise call that executes through its first await and returns a continuation to the caller. The helper exposes that boundary while retaining the existing blocking API. No callback signature or subprocess SDK protocol changes.

The startup-name block from `acdf43f482` is not touched. All RPC changes are below runtime construction and initial naming.

## Regression evidence

| Guard | Evidence |
|---|---|
| `TestRPCResponseTurnDefersAlreadyFulfilledAwait` | A result deliberately completes before the next immediate command is admitted; it must not escape the turn |
| `TestRPCResponseTurnEvaluatesContinuationAfterAdmission` | A continuation reads state after later synchronous mutations, not at enqueue time |
| `TestRPCResponseTurnProtectsLaterInputAndDoesNotStrandCompletions` | Completions from earlier requests cannot run inside a later input callback; producers do not block and idle handoff loses no work |
| `TestRPCCycleAndAbortRetrySameInputCallback` | One real pipe write requires exact response order `models, levels, abort-retry, cycle` |
| `TestRPCCycleResponseDoesNotWaitForFutureInput` | A lone cycle responds while stdin stays open; a later abort-retry is not given artificial priority |
| `TestRPCSecondCycleFinishesWhileFirstListenerWaits` | A real extension select holds the first notification. The second cycle mutates to model-three and responds before release |
| `TestRPCCycleResultSamplesThinkingAfterAwait` | Two model prefixes select a later per-model high thinking level; both deferred cycle responses sample high |
| `TestModelChangePrefixAndAwaitedNotification` | The Session model is visible while the listener waits, but the blocking SDK call has not returned |
| `TestReadJSONLBatchesPreservesInputTurns` and `TestReadJSONLBatchesStopsWithoutReadingNextTurn` | Explicit reader chunks prove batch boundaries, partial/final framing and detach behavior |

Canonical scenario 07 now writes the whole pipeline in one pipe write and uses `output_equal`, replacing `output_normalized_equal`. Scenario 31 holds the first listener with an RPC UI response barrier and compares complete canonical JSONL, replacing only the generated select request ID. Scenario 32 compares the synchronous thinking event and complete deferred response payloads without value normalization. Scenario 23's old claim that Pi waits for the previous complete cycle is corrected; its existing multi-model ordering assertion remains.

The new blocked-cycle unit was run against the compiled public-main binary through a test-only binary-selection overlay. It fails immediately because get_state still reports model-two. Scenario 31 against that same binary fails because the second cycle cannot respond before the first listener is released. These are deterministic failures, not timing-based retries.

Compiling mutations also fail:

- publishing an already-completed response immediately produces `models, levels, cycle, abort-retry` in both the real-process unit and scenario 07;
- flattening each buffered batch into separate input turns produces the same wrong order and fails the reader batch guard;
- evaluating a cycle response's thinking level before the continuation loses the later high level and fails scenario 32.

## Public-main 50-run check

The public source was exported with `git archive 713707ba70389232fa7fe2ac45c82ac29cbb33b3` into a disposable evidence snapshot. The binary and parity harness were built from that snapshot. The original unchanged scenario 07 ran with one pair per invocation and `-count=50`, against real Pi 0.87.1.

Result: **50 paired passes, 0 failures** on this Linux host. This does not reproduce the CI flake locally and does not prove the race absent. Public main has a valid scheduling window for the reported order, and the original scenario also leaves read coalescing uncontrolled by writing each line separately. The deterministic guards above prove the fixed contract rather than treating fifty green timing samples as closure.

The external dispatch evidence directory retains the captures. `public-main-50.log` retains all fifty test results. `public-main-unit-red.log` and `public-main-blocked-cycle-red.log` retain the deterministic public-main failures. Mutation logs and parity failure artifacts retain exact output and rerun commands.

The corrected atomic scenario also passed 50 pairs on this branch. The unchanged public-main result remains 50 paired passes, not a claim that the public-main race was reproduced by those samples.

A full-package run also caught a test-fixture readiness race in the earlier clear_queue port. A prompt preflight acknowledgement precedes the initial steering poll; queueing immediately after it can legitimately let that poll consume steering before clear_queue. The upstream delayed-stream test targets mid-stream queueing. The Go fixture now explicitly waits for the HTTP provider request to start, while holding its completion, before sending queued messages. The request strings and exact expected queue/response assertions are unchanged. No sleep, larger timeout, retry or skip is added; ten repeated runs of the affected tests pass.

## Verification

The full RPC and model-resolver-selector families pass against Pi 0.87.1. Package suites and focused race checks pass. Linux/Windows vet, lint, go-fix, regenerated interface inventory/recommendations, static coverage, ci-drift and async-contracts pass. The final repository-wide test run reaches only the existing CI-shard gate-list mismatch and unclassified BindAbort method checks. The test-porting release gate still lists the other lanes' pending files; this change does not weaken that gate.

`BenchmarkRPCResponseTurn` and CPU/allocation profiles cover a fulfilled cycle/abort-retry pair through the production serializer. The measured results are retained with the external evidence; no performance-improvement claim is made.
