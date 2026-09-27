# RPC parity fixes on public main

## Scope and provenance

This branch starts at public main `f4853d692`. It adapts only the RPC changes from staging commits `d499f7dccd`, `9405a8a5f6`, `2378e7363b`, and `30449a3dca`. The original [wire report](fix-rpc-parity.md) and [dispatch report](rpc-command-dispatch.md) retain the upstream audit and original evidence. Their staging gate results do not describe this branch.

The reference implementation is Pi 0.87.1, from `.upstream/v0.87.1` and `extensions/sdk-ts/node_modules/.bin/pi`. All Pi source references below are relative to that source root. Evidence from this integration is retained at `/tmp/port-rpc-public-evidence/` on the verification host.

## Public-main adaptations

- Keep main's RPC command catalog and source-info types. Do not import the pending-tests-triage resource/catalog port or its porting-law gate.
- Keep main's tool presentation architecture. `internal/codingagent/tool_definition_renderers.go` exists only on staging and depends on the unrelated tool-definition renderer port. Instead, retain active read/write arguments in `InteractiveMode`, pass them to main's existing `toolBodyRendererForCall` when the result arrives, and release the pending arguments on completion, abort, run replacement, or transcript rebuild. Completed cards retain only their own renderer state. The unchanged `TestInteractiveBuiltinFileResultsWithoutPrivateDetails` checks the same live event path as staging.
- Retain every new RPC test and scenario, the hermetic Anthropic fixture, exact nested result assertions, complete output draining, JSONL comparator guards, and deterministic model-listener barriers. Main already has the RPC process helpers and `rpc-ui.mjs` fixture; no unrelated test-port batch is required.
- Add user-facing fixes under the existing `[Unreleased]` → `Fixed` heading. This base has no `[0.2.1]` heading.
- Use the individual generators because this base has no `make generate` target. Regenerate the Go interface inventory, recommendations, and coverage from this branch; do not copy staging's generated files.

## Upstream contracts and red proof

| Contract | Pi 0.87.1 source | Public-main regression |
|---|---|---|
| LF-only JSONL preserves literal U+2028/U+2029 and escaped backslashes | `packages/coding-agent/src/modes/rpc/jsonl.ts:10-11,21-62` | Scenario 02 fails on escaped separators; `TestRPCJSONLUpstream` checks serialization, CRLF, and final unterminated input |
| Unknown-command errors retain request ID | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:714-716` | `TestRPCUnknownCommandPreservesRequestID`; this existing main behavior is a carried regression guard, not a new fix |
| A prompt has exactly one authoritative preflight response | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:394-415` | All four `TestRPCPromptResponseSemanticsUpstream` cases drain the complete process output; this existing main behavior is a carried regression guard |
| Awaited cycles cannot respond inside the current input callback | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:482-487,786,810-811`; `jsonl.ts:21-62` in the same directory | Exact atomic scenario 07 and `TestRPCResponseTurnDefersAlreadyFulfilledAwait` |
| Later cycles apply state and finish independently of blocked earlier listeners; response thinking is sampled after await | `packages/coding-agent/src/core/agent-session.ts:2178-2240` | `TestRPCSecondCycleFinishesWhileFirstListenerWaits` is red with model-two instead of model-three; `TestRPCCycleResultSamplesThinkingAfterAwait` is red because the response precedes the synchronous thinking event; scenarios 31/32 are red against the saved main binary |
| Ordinary read/write results have no details; live presentation uses retained arguments | `packages/coding-agent/src/core/tools/read.ts:110,158-188`; `write.ts:85-88`; `renderers/read.ts:111`; `renderers/write.ts:96` | `TestBuiltinToolResultDetailsWire` is red on leaked metadata; `TestInteractiveBuiltinFileResultsWithoutPrivateDetails` is red on collapsed read content and missing write preview; scenario 28 fails on the complete wire transcript |
| Tool execution error flag belongs on the event, not inside result; persisted toolResult retains it | `packages/agent/src/agent-loop.ts:870-894` | `TestRPCToolExecutionResultExactWire` is red on nested isError; production JSON/RPC persistence assertion rejects it |
| Termination disposes an idle runtime without waiting for stdin EOF | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:366-378,728-744` | `TestRPCSignalWithOpenStdin/terminated` is red; scenario 29 is red with timeout/status -1 instead of 143 |
| agent_end includes only the current run's messages | `packages/agent/src/agent-loop.ts:253,290,319` | `TestAgentEndContainsOnlyCurrentRunMessages` is red with `[user assistant user assistant]` on run two; scenario 28 covers both runs |
| Tool metadata is sparse and retains numeric limits; shell output updates include details even when empty | `packages/coding-agent/src/core/tools/bash.ts:270-276,324-374`; `grep.ts:282-307`; `find.ts:147-165`; `ls.ts:141-160` | `TestBuiltinToolResultDetailsWire` is red on PascalCase, null fields, and rounded 1.5 limits; `TestShellResultAndUpdateDetailsWire` is red on null instead of {}; tools scenario 15 compares complete factory results |

The first unit-red command could not compile tests that require the new fractional field types and changed helper signature. Those compiler errors are not behavioral proof. A test-only overlay omits the two type-specific test files and adjusts the benchmark call to main's existing helper signature. The resulting tools and live-render tests compile and fail behaviorally. The agent guard also fails without any overlay. The initial RPC red run overlapped later source edits, so its subprocess results are superseded by `immutable-main-unit-red.log`: a test-only binary-selection overlay drives all affected process guards against the saved, unchanged `pig-main` executable. That run shortens the hang bound to ten seconds; it does not alter a production timeout or retry a flaky assertion. Both signal cases, retained model state, post-await thinking, and tool-result persistence fail on unchanged main. The saved binary was built before production edits and is also the authoritative unchanged-main parity target.

The first parity invocation inherited the worker's `PIG_CODING_AGENT_DIR`, preventing model fixtures from loading. It is not accepted as model-cycle evidence. The clean invocation unsets that ambient variable and reproduces all five intended failures against the saved main binary and real Pi. No scenario timeout, comparator, or assertion is weakened.

## Verification environment

The host has Go 1.27.1, Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, npm 11.17.0, and tmux 3.4. npm and tmux differ from the qualified development versions; no tool is installed or replaced. Commands use `PATH=$HOME/flakes3-tools:$PATH` for rg/fd and unset ambient `PIG_CODING_AGENT_DIR` for fixture isolation.

## Mutation and resource evidence

All eight mutations compile and fail behaviorally. They restore escaped JSONL separators, remove unknown-command IDs, duplicate the preflight acknowledgement, publish fulfilled cycles immediately, flatten buffered input turns, sample thinking before await, replay complete history in agent_end, or restore private read/write results and the wrong byte-truncation limit. `mutations.exit`, `more-mutations.exit`, and each `mutation-*/test.log` retain the outcomes. Mutated binaries also fail scenarios 07 and 32, and the mutated tool probe fails scenario 15. No mutation is committed.

The corrected atomic scenario 07 passes fifty independent PiG/Pi pairs (`atomic-50.log`). RPC scenarios 06, 28, 29, 31, and 32 retain their declared three-pair durability. The full RPC family passes without retries. The input-turn tests cover idle completion, later active input callbacks, producer publication, and independent extension-listener completion.

`TestRPCSignalWithOpenStdin` checks both termination exit statuses and the extension disposal marker while stdin remains open. `TestRPCToolDetailsPersistAndReplay` checks fresh messages, disk entries, and a reopened Session. The live renderer guard covers both read and write; the extended pending-tool and reused-ID tests verify that retained arguments leave the active-call maps at lifecycle boundaries. No Session-history scan is added.

The RPC benchmarks run through the production serializer with CPU and allocation profiles (`rpc.cpu`, `rpc.mem`, `cpu-top.log`, and `alloc-top.log`). `BenchmarkRPCResponseTurn` measures 2341 ns/op, 1131 B/op, and 15 allocations/op. `BenchmarkRPCJSONLToolResult` measures 1489 ns/op, 386 B/op, and 8 allocations/op. These measurements document the path, not a performance improvement.

## Gates

| Gate | Result |
|---|---|
| Focused regression suites, including all 27 carried upstream cases | Pass (`focused-green.log`) |
| Linux `go vet ./...` | Pass |
| Windows vet for agent, AI, Session, extension details, RPC client/server, tools/rendering, and parity packages | Pass; Windows runtime execution is not claimed |
| `go tool golangci-lint config verify`, `make lint-changed LINT_BASE=public/main`, and `make lint` | Pass; no suppressions added |
| Focused race checks for response turns, JSONL batches, Session notification ordering, agent_end, metadata, and renderer lifecycle | Pass |
| Generated Go inventory, recommendations, and static coverage | Regenerated with the individual generators |
| `make ci-contracts ci-drift` | Pass (`ci-final.log`). The staged-path check initially rejects a verification-host name in this report; the name is removed rather than suppressed |
| `make async-contracts` | Pass; the broader pre-existing RPC/Session async obligations remain deferred, as in the carried dispatch ledger |
| `go fix -diff ./...` | Pass; empty diff |
| RPC, tools, JSON, interactive-rendering, export-html, and model-resolver-selector parity families | Pass against Pi 0.87.1 |
| All hermetic print-mode scenarios (`make parity-driver DRIVER=print-mode`) | Pass |
| Live print family | Blocked by the existing scenario's auth classification: this base has only `01-print-mode-arithmetic`, tagged live but not `requires-auth`. The hermetic family target selects no scenarios. An explicit live invocation fails in both binaries because `snapshotAgentDirs` strips credentials from scenarios without the required auth tag, even with `PIG_PARITY_REAL_AUTH` set. The scenario is unchanged and is not skipped or counted as passed. Auth classification and the differing missing-auth diagnostics remain lead-owned follow-ups outside this RPC port |
| `go test ./...` | Pass (`go-test-all.log`), including the complete RPC command package, subprocess host, and cross-SDK conformance suites |

No unrelated pending-tests-triage changes, divergence IDs, lint suppressions, retries, longer timeouts, skips, or comparator weakenings are added. Scenario 06 moves from selected substrings to complete canonical JSONL equality. Scenario 07 moves from normalized equality to exact output equality with one atomic input write. Generated file coverage statuses do not change.
