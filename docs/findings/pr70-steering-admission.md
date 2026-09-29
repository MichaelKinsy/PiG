# PR 70: RPC steering and prompt admission

## Scope and source

Port `70f0d4e293` and its queue/shell prerequisite `fa0830224` onto the public RPC wire branch. Keep the public branch's command catalog, JSON parse diagnostics and existing model-cycle dispatcher. Do not import the integration branch's unrelated runtime changes or comparison harness. The new preparation and run handles are internal scheduling mechanisms behind the existing blocking Agent and Session APIs; the extension wire and SDK contracts do not change.

The independent scheduler experiment is discarded before the port. None of that experiment remains in production.

| Pi 0.87.1 source | Contract |
|---|---|
| `packages/coding-agent/src/core/agent-session.ts:1555-1578,1634-1667` | Input processing yields. The input event samples delivery mode at invocation, but prompt delivery rechecks streaming after the input await. |
| `packages/coding-agent/src/core/agent-session.ts:1700-1767` | Preflight awaits `before_agent_start` and image normalization before acceptance. A suspended preparation does not reserve the Agent. |
| `packages/coding-agent/src/core/agent-session.ts:1468-1488` and `packages/agent/src/agent.ts:370-380,499-525` | Acceptance precedes the synchronous Agent claim. A second prepared prompt is not silently serialized behind an active run. |
| `packages/coding-agent/src/core/agent-session.ts:894-919,1062-1066` and `packages/agent/src/agent-loop.ts:116-117` | First-event dispatch yields before the rest of the loop. |
| `packages/coding-agent/src/core/agent-session.ts:1823-1903` and `packages/coding-agent/src/modes/rpc/rpc-mode.ts:417-424,786-811` | Queue helpers and their callers await separately. Fulfilled Promise reactions retain registration order. |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts:563-583` and `packages/coding-agent/src/core/agent-session.ts:3494-3519` | Shell completion is independent of Provider completion. A claimed run buffers shell history before its first event. |
| `packages/coding-agent/src/core/agent-session.ts:1721-1723,1882-1885,1899-1902,1913-1920` | User content retains empty text blocks. Queue rejection names retain the slash. |
| `packages/coding-agent/src/modes/print-mode.ts:132-155` | The blocking print caller awaits Session execution and prints final assistant text. |

`cmd/pig/rpc_admission.go` schedules fulfilled and pending reactions on `rpcResponseTurn`. `coding/session_preflight.go` stores preparation separately from a claimed run. `Agent.BeginSendContent`, `PromptRun.Start` and `PromptRun.Run` share the same production implementation with blocking callers. Shutdown cancels pending work, closes RPC dialogs and joins admission and run workers. A preparation from a replaced Session cannot commit into its replacement. No timeout, retry, skip, record sorting or new divergence is introduced.

The prerequisite also aligns the Go classified-error faux stream's initial snapshot with `test/parity/testdata/test-faux-provider.ts:emitPlan`. This is a paired fixture correction, not a real Provider change.

## Red and green evidence

`TestRPCPromptPreflightAdmitsQueueBatch` fails on the original branch:

```text
actual: queue_update, response:steer, queue_update, response:follow, response:prompt, agent_start, turn_start
Pi:     queue_update, queue_update, response:prompt, agent_start, response:steer, response:follow, turn_start
```

The old substring scenario passes locally despite that difference. Replace it with one explicit input batch, an `agent_settled` capture barrier and complete ordered JSONL equality. Use identical explicit system prompts to control D2/D22 inputs. Canonicalize only object-key order and numeric message timestamps. Preserve all records, fields, arrays and message text. The strengthened scenario fails against the saved pre-fix binary at record 2, where PiG replies to steer instead of publishing the second queue update.

The port passes `rpc/13-rpc-steering-queue` for 50 consecutive PiG/Pi pairs with zero mismatches. The oracle executable reports `0.87.1`; its installed package also reports `0.87.1`. The runner's cosmetic `upstream unknown` path label does not replace that explicit version check.

Retain the port's guards for fulfilled reaction order, suspended input and preflight, post-input streaming changes, synchronous Agent claim, concurrent preflight rejection, EOF cleanup, empty text, queue rejection and shell completion during a held HTTP Provider. These imported guards were red or mutation-proven in the source lane; the public-branch admission-order unit test and strict scenario are independently red-proven here. The hermetic print scenario is supplementary caller-path evidence, not a claim that print previously failed.

Two compiling overlays independently verify the ported guards on this branch. Running a fulfilled reaction inline makes `TestRPCPromiseReactionOrder` report `[1,3,2]` instead of `[1,2,3]` and makes scenario 13 fail at its first record. Replacing the Agent's prepared user text makes the hermetic print scenario fail with the faux Provider's unhandled-request diagnostic instead of `42`. Overlay files and binaries remain outside the source tree; no mutation is committed.

## Verification

All probes run with a temporary HOME and unset ambient `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR`. Each scenario or test supplies its own isolated configuration. No worker credentials are read. Resolve installed tool binaries before the mise shims so temporary homes do not need the operator's tool-manager configuration.

| Gate | Result |
|---|---|
| `rpc/13-rpc-steering-queue`, 50 consecutive pairs | Pass, complete ordered JSONL equality; repeated after final environment isolation |
| Full RPC family | Pass, all 30 scenarios at declared durability, including 07, 31 and 32 |
| Tools, JSON, Session and compaction parity families | Pass at declared durability |
| Hermetic `print/02-print-prepared-prompt` | Pass, three complete-output pairs |
| `go test ./...` | Pass |
| `go test -race ./coding ./agent` and focused RPC race guards | Pass |
| `go vet ./...` | Pass |
| Windows vet, `parity,integration` tags, touched packages | Pass |
| Lint configuration validation, changed-package lint and full `make lint` | Pass; full lint includes `integration,live,parity` |
| `make ci-contracts ci-drift async-contracts` | Pass after regenerating Go-interface, recommendation and static coverage inventories |
| `go fix -diff ./cmd/pig ./coding ./agent ./ai` | Empty |

`make lint-changed` uses `LINT_BASE=public/fix/rpc-wire-parity` because this checkout has no local `main` ref. The extra tagged `go fix` probe reports existing modernization suggestions in the parity harness, including untouched files; these are not applied as an unrelated cleanup.

The first setup runs expose two environment errors: mise's `uv` shim cannot select a tool under a temporary HOME, and an inherited temporary coding-agent directory overrides scenarios that select `PIG_HOME`. Use the already-installed native uv executable and unset the ambient coding-agent variables. The full repository test and RPC family runs then pass without changing a test, timeout or production behavior.

The existing `print/01-print-mode-arithmetic` is a live GitHub Copilot scenario. Both executables fail without authentication. This is an explicit remaining gate blocker; do not read worker credentials, skip the scenario or call it passing. The separate hermetic print caller scenario provides repeatable coverage without replacing that live test.

Artifacts are retained outside the source tree: pre-fix binary, red unit and strict-comparison logs, complete failed-pair streams, the 50-pair result JSON, family results and gate logs. Reproduce the durability run after building a fresh binary and setting `PIG_PARITY_PIG_BIN`, `PIG_PARITY_PI_BIN` and `PI_PACKAGE_ROOT` to the selected artifacts:

```bash
go test -tags=parity ./test/parity/runner -run '^TestParity$/13-rpc-steering-queue$' -count=1 -v -args -pig-parity.runs=50
```

## Resource evidence

`BenchmarkRPCPromptAdmission` exercises Session creation, admission, Agent execution, RPC event conversion, acknowledgement and joined shutdown. Three Go 1.27.1/Linux amd64 samples on the Xeon 6746E measure 542–568 µs/op, 104658–104823 B/op and 1454–1455 allocations/op. Retain CPU and allocation profiles with the run artifacts. This is a cost observation, not a speedup claim. The port reuses the existing continuation executor and subprocess invocation acknowledgement, and it adds no TUI-loop work or detached worker lifetime.

No new PORT_MAP entry is promoted. The map adds the new source locations, and the async ledger retains deferred full-file closure outside the reviewed admission contract.
