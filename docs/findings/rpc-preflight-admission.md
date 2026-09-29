# RPC prompt-preflight admission

## Contract and implementation

This change closes the remaining H1 prompt-preflight admission gap after `fa0830224`. Pi is the real pinned 0.87.1 executable. The RPC wire has no new fields or event kinds.

| Pi source | Preserved rule | Go implementation |
|---|---|---|
| `packages/coding-agent/src/core/agent-session.ts:1555-1578,1634-1667` | Input runs before prompt expansion. Streaming behavior in the input event is sampled at invocation, but prompt delivery rechecks streaming after the input await. | `cmd/pig/rpc_admission.go` captures the input event's delivery mode and resumes routing on the FIFO executor after the input result. |
| `packages/coding-agent/src/core/agent-session.ts:1700-1760` | `before_agent_start` and image normalization finish before preflight acceptance. A suspended preflight does not reserve the Agent. | `coding/session_preflight.go` stores one preparation per prompt. It does not hold the Session run mutex across extension callbacks. Image normalization follows the callback result. |
| `packages/coding-agent/src/core/agent-session.ts:1468-1488` and `packages/agent/src/agent.ts:370-380,499-525` | Acceptance precedes the Agent claim. The claim is synchronous; a second prepared prompt does not silently wait and run later. A rejected concurrent run still settles. | `Agent.BeginSendContent`, `PromptRun` and `PreparedPromptRun` separate claim, first-event dispatch and owned execution. A busy rejection retains the live Agent's abort controller. |
| `packages/coding-agent/src/core/agent-session.ts:894-919,1062-1066` | Extension dispatch is awaited before publishing `agent_start`. Its position relative to queue replies depends on admission order. | The executor dispatches the real first event through `PromptRun.Start`, flushes it, and separately resumes the rest of the run. It does not synthesize or duplicate an RPC event. |
| `packages/coding-agent/src/core/agent-session.ts:1823-1903` and `packages/coding-agent/src/modes/rpc/rpc-mode.ts:417-424,786-811` | Queue helpers, their public wrappers and RPC dispatch each await their callee. Fulfilled awaits still yield; reactions retain registration order. | `rpcPromise` records completion and queues reactions through the existing `rpcResponseTurn`. A batch of reactions is enqueued atomically before a reaction can add another. |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts:393-415` | Successful preflight writes the authoritative prompt response immediately. Rejection reaches the response through the prompt's rejection observer. | Acceptance and rejection use their separate continuation paths. The `promptPending` streaming approximation is removed. |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts:563-583` and `packages/coding-agent/src/core/agent-session.ts:3494-3519` | Bash awaits its own operation, not Provider completion. A result is buffered once a run is claimed, including before its first event. | Shell execution retains its existing worker and continuation owner. `TestRPCBashCompletesWhileProviderIsPending` requires a complete shell response before releasing the held Provider. `TestPreparedPromptBuffersBashBeforeFirstEvent` guards transcript deferral. |

Go does not provide implicit Promise continuations. The concrete preparation and claimed-run handles are a language implementation mechanism for those boundaries, not new extension capabilities. The blocking Session/Agent methods use the same handles and drain them. RPC schedules the boundaries explicitly. Pending input and preflight callbacks use the existing subprocess invocation acknowledgement at their first suspension; they do not wait for a user dialog before admitting another command. Cancellation and EOF close the UI and join owned work. A preparation cannot commit into a replacement Session. Concurrent preflight reads use message snapshots and synchronized system-prompt overrides.

## Red-first and mutation evidence

Before the source changes, `TestRPCPreflightAdmission/preflight_is_idle` fails because `get_state.isStreaming` is true during the suspended callback. `second_prompt_and_queues_do_not_wait_for_preflight` fails because the second prompt is rejected as already processing. The existing mixed-batch comparison also exposes the prompt response arriving after queue replies.

The regression suite now covers:

- suspended `input` handlers for prompt, steer and follow-up while the reader remains live;
- idle state during suspended `before_agent_start`;
- another prompt and both queues completing while that preflight remains suspended;
- streaming becoming active while an input handler is suspended, followed by correct follow-up delivery;
- a prepared prompt resuming while another Provider is active, with successful preflight acknowledgement but no second Provider invocation;
- EOF while preflight is suspended;
- synchronous state replies preceding an asynchronous preflight rejection;
- Agent claim before execution, rejection of a second claim, cancellation and release;
- fulfilled Promise reaction order, including a reaction that registers another reaction;
- shell completion while the Provider remains pending, and result buffering between Agent claim and first-event dispatch.

The busy-run rule is also probed directly against real Pi with a held local Anthropic-compatible HTTP response. Pi acknowledges the second prompt, then acknowledges the released first preflight and emits its rejected-run settlement before the held Provider is released. It issues only one Provider request. The Go regression asserts those same effects.

Four compiling Go overlays prove the guards without editing source beneath a running test process:

1. Execute a fulfilled reaction inline: `TestRPCPromiseReactionOrder` reports `[1,3,2]` instead of `[1,2,3]`, and strict scenario 20 fails at its first record.
2. Mark preparation active: the idle-state RPC regression fails with `isStreaming:true`.
3. Omit the synchronous Agent claim: `TestPreparedPromptClaimsBeforeExecution` fails before execution.
4. Publish preflight rejection inline: the rejection-order regression observes prompt failure before the state response.

The rejection-order guard is added after that source correction and is mutation-proven, not claimed as initially red. The first two preflight regressions and the pre-existing mixed-batch failure precede production changes. The claimed-run bash guard also fails before its correction: the result enters history while the admitted prompt has not emitted its first event. Supplemental lifetime cases extend those guards. No mutation remains in the source tree.

## Strict scenarios

`20-rpc-runtime-boundary-values` again puts the queue commands and prompt in one input batch. The prompt acceptance must precede the nested queue replies. It still compares all original command values and every output record. Shell completion uses its own explicit external-operation boundary; no production shell wait is tied to agent settlement.

`35-rpc-queue-prompt-await` adds state queries before and after the queued input within a single prompt-first batch. Both queries must snapshot idle state and empty queues, even though queue effects appear before some replies. It also checks the opposite first-event ordering from a queue-first batch.

`36-rpc-preflight-admission` runs both real executables with the same extension and deterministic UI suspension points. A second prompt and both queues run while the first preflight is held. A later input dialog proves that UI replies and state queries remain live. The complete streams and stderr are compared. Only typed UUIDs, timestamps and exact isolated cwd prefixes are aliased. The explicit system prompt controls D2/D22 inputs without deleting output sections.

These scenarios pass their declared three pairs with `json_output_equal` and `stderr_equal`. A final `-count=3` run repeats all three declared pairs for scenarios 20, 35 and 36, for nine pairs per scenario. The earlier framing and model-cycle await scenarios are re-probed rather than weakened.

## Resource evidence

`BenchmarkRPCPromptAdmission` drives Session creation, the actual admission pipeline, Agent execution, RPC conversion, event acknowledgement and joined shutdown with an immediate local faux Provider. On Go 1.27.1/Linux amd64/Xeon 6746E, the recorded sample is 605 µs/op, 104819 B/op and 1456 allocations/op. CPU and allocation profiles are retained. This is a representative cost observation, not a speedup claim. The profile includes Session/event allocation, JSON conversion and runtime scheduling. No work moves onto the TUI render loop. Preflight and run workers are owned; fulfilled awaits allocate queued continuations rather than detached goroutines.

## Shared integration files

Production changes are in `agent/agent.go`, `agent/agent_loop.go`, `coding/session.go`, `coding/session_run_state.go`, `coding/session_transcript.go`, new `coding/session_preflight.go`, `cmd/pig/rpc_mode.go`, `cmd/pig/rpc_dispatch.go` and new `cmd/pig/rpc_admission.go`. The subprocess wire and language SDKs are unchanged. `docs/parity/PORT_MAP.md` records the new source locations. Async and test-mapping evidence expands without promoting unrelated pending upstream tests or claiming complete async closure for every RPC command.

## Gates and remaining repository failures

Full tests for `cmd/pig`, `coding` and `agent` pass. The preflight, Promise, claimed-run and held-Provider bash regressions pass, including their race-enabled runs. Full `coding` and `agent` race tests pass. Full lint, changed-package lint, Linux vet and Windows vet pass. Interface, recommendation, normalization and coverage drift checks are current. Session and compaction parity pass.

The wider strict RPC family still exposes the separately owned retry-cancellation, prompt-identity and real-provider record findings. The strict real-provider error scenarios remain outside this admission fix. `go test ./...` reaches the same existing subprocess-suite aggregate timeout, D78-dependent closure test failures and TUI Node-stdin `EAGAIN` failure. `make -k ci-contracts ci-drift` remains blocked by the existing hot-path test backlog, unapproved D78 and private-path findings in two earlier reports. No gate is skipped or weakened, no timeout is increased, and no divergence or lint suppression is added.

The first combined lint/build/race shell invocation exceeds its tool deadline and supplies no passing race evidence. The final standalone race invocation completes successfully under the original test budgets. The explicit final gate results above, not that interrupted shell, are the evidence.
