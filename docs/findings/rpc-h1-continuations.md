# H1: RPC queue and shell continuations

This report records the intermediate `fa0830224` result. Its remaining H1 admission gap is closed by [RPC prompt-preflight admission](rpc-preflight-admission.md). The red evidence and deferred conclusions below describe that intermediate commit, not the current admission path.

## Base and comparator

Merge `818a61134` (the strict-comparison harness) into the RPC branch in `cba8dc6ea`. Keep the existing malformed-input cases and CRLF batch, but resolve their comparator conflicts in favor of `json_output_equal`, `stderr_equal` and correlated `wait_event` barriers. Regenerate the normalization inventory and static coverage from the merged tree. Do not sort output records or restore erased error text.

The original strict `20-rpc-runtime-boundary-values` fails before the production changes: at record 5, PiG writes the steer response while Pi writes the second queue event. A direct Pi 0.87.1 probe with one pipe write containing steer, follow-up and abort-retry records produces both queue events, then the abort-retry response, then the two queue responses. `TestRPCQueueRepliesYieldToInputBatch` encodes that complete record sequence and fails on the base.

## Source fixes

Pi references are relative to `packages/coding-agent/src/` in the pinned 0.87.1 mirror.

| Surface | Cause and fix | Reference and guard |
|---|---|---|
| Queue completion | `steer` and `follow_up` flush their Session events but write success and failure responses directly. Route their completions through the existing `rpcResponseTurn`, which retains admitted input order and yields completed awaits until the input batch ends. | `modes/rpc/rpc-mode.ts:417-424,786-811`; `core/agent-session.ts:1823-1873`; `TestRPCQueueRepliesYieldToInputBatch`, `TestRPCQueueErrorsYieldToInputBatch` |
| Shell completion | The shell worker writes its override, normal result and errors directly, bypassing the continuation owner. Publish those completions through the same executor. Keep shell execution concurrent with an active Provider. | `modes/rpc/rpc-mode.ts:563-583`; `TestRPCBashCompletesWhileProviderIsPending`, existing `TestRPCResponseTurnProtectsLaterInputAndDoesNotStrandCompletions` |
| Empty text | The strict comparison exposes `content:[]` for an empty prompt. `BuildUserContent` must preserve the leading empty text block, including before image attachments. | `core/agent-session.ts:1721-1723,1882-1885,1899-1902`; red-first `TestBuildUserContentPreservesEmptyText` and `TestRPCEmptyPromptRetainsTextBlock` |
| Queue rejection text | The new rejection guard exposes the missing slash in the quoted command name. Both queue rejection paths now report the slash-prefixed invocation. | `core/agent-session.ts:1913-1920`; `TestRPCQueueErrorsYieldToInputBatch` fails with the old diagnostic |
| Faux fixture error start | The Go parity fixture emits only its classified error, while the Pi fixture's `emitPlan` snapshots an empty pending start first. Start the Go classified-error stream before publishing its error. This changes no real Provider implementation. | `test/parity/testdata/test-faux-provider.ts:emitPlan`; red-first `TestTestFauxClassifiedErrorStartsPending` |

The main production changes are in `cmd/pig/rpc_mode.go` and `coding/session.go`. `ai/test_faux.go` is the additional fixture correction. The Session text constructor is shared by fresh prompts and queue messages. No SDK interface, wire format, new runtime task, timeout or divergence is added.

## Scenario determinism

The old `input_lines` list uses separate writes, which do not define Node's data-callback boundary. It also starts a shell and a faux-provider run concurrently and assumes one fixed relative position for their completion. Pi awaits the shell result, not an arbitrary active Provider. Holding the Provider while executing a real shell command proves that an implementation must not wait for agent settlement to answer bash.

Scenario 20 retains all original commands and complete records. It now defines:

1. One explicit input batch for the invalid model, queue modes, empty steer/follow-up and invalid session operations. The awaited queue replies yield to the later queue event and synchronous command outcomes.
2. An empty prompt after those replies, with an `agent_settled` barrier.
3. A batch of rejected queue commands and a synchronous response, which checks both rejection order and complete diagnostic text.
4. An empty shell command and a synchronous response in one batch, followed by its correlated shell response barrier.

Both hosts receive the same explicit system prompt and disable ambient context files. This controls D2/D22 inputs instead of replacing a documentation section in captured output. Only exact cwd prefixes and typed timestamps are aliased. Empty text, errors, booleans, arrays, every output record and stderr remain compared. No records are dropped or sorted. The scenario still fails with the original immediate queue replies after these fixture changes.

This is not a claim that the unbarriered shell and faux-provider completion always have one order. The original stimulus remains in scenario 35, with one explicit input batch and complete comparison, so the prompt-preflight gap cannot disappear behind scenario 20's barriers. Its first current failure is `/7/command: pig="steer" pi="prompt"`. The full RPC async ledger remains deferred for that broader interaction; no artificial wait-for-idle policy is added.

## Red and mutation proof

The initial strict scenario and queue unit test fail on immediate response publication. The first continuation fix exposes the empty-text difference. Its pure constructor and real RPC tests fail before the source correction. The next strict run exposes the classified-error fixture's missing start. Its unit test fails with one error event instead of pending-start followed by error. The rejection test independently exposes the missing command slash before that correction.

Compiling mutations restore immediate queue successes, drop empty text and remove the classified-error start. They fail the corresponding unit tests. The maintained strict scenario also fails again at record 5 with those mutations, proving that its explicit barriers do not hide the original H1 queue defect. All mutations are removed.

`TestRPCBashCompletesWhileProviderIsPending` uses a held local HTTP Provider, executes `printf bash-during-provider`, and requires the complete shell result before releasing the Provider. It rejects the tempting global wait-for-idle workaround. The existing response-executor tests prove deferred fulfilled awaits, no stranded completion, no waiting for future input and reentrant continuation scheduling.

## Verification and lifetime

Run the strict scenario with the pinned executable and package root:

```bash
export PATH="/absolute/toolchain/bin:$PATH"
go build -o bin/pig-parity ./cmd/pig
export PIG_PARITY_PIG_BIN="$PWD/bin/pig-parity"
export PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi"
export PI_PACKAGE_ROOT="$PWD/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
go test -tags=parity ./test/parity/runner -count=1 -run '^TestParity/20-rpc-runtime-boundary-values$'
```

The strict scenario passes all three declared pairs. Focused RPC, constructor, fixture and executor tests pass. `BenchmarkRPCResponseTurn` records 2.24 µs/op, 1131 B/op and 15 allocations on Go 1.27.1/Linux amd64/Xeon 6746E; CPU and allocation profiles are retained. This is an observation, not a speedup claim. The change reuses the existing continuation queue, Session event barrier, shell worker and shutdown join. Completion producers do not block waiting for the input executor, and no shell is tied to Provider completion.

## Integration files

In addition to the three production files, this fix changes `CHANGELOG.md`, the RPC async-contract evidence, the RPC test mapping, scenarios 20 and 35 and the small rejection-command fixture, the normalization inventory, and three regression-test files. Scenario 17 receives only typed bijective aliases for its now-published recovery entry IDs; its retry-cancellation ordering remains red after those random identities are accounted for. The merge commit contains the separately authored strict harness and its ledger changes. Keep that merge separate from the H1 production commit when reviewing scope.

## Wider strict results

The complete compaction and providers-faux-streaming families pass. The full RPC family remains red on scenario 17's retry-cancellation order, scenario 26's D2/D22 documentation section and scenario 33's real-provider message-start records. The new real-provider error scenarios in print and JSON also remain red on the separately owned provider error conversion. No comparator is weakened or scenario disabled for those findings.

Full touched-package tests, focused race tests, the strict harness unit tests, full-repository lint, changed-package lint, Linux vet and Windows vet pass. The prior malformed-input/framing and model-cycle-await scenarios also pass after the strict harness merge. `make -k ci-contracts ci-drift` remains blocked by the existing hot-path test-porting backlog, unapproved D78 and private-path findings in two existing reports. The normalization inventory is regenerated after scenario edits; the final separate `ci-drift` run has no normalization, coverage or source-map drift. `go fix -diff` for touched packages is empty. No lint suppression or new divergence approval is added.
