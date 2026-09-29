# RPC wire parity and PT-071-cli-rpc

This is the retained staging-campaign report for `d499f7dccd`, `9405a8a5f6`, and `2378e7363b`. Its gate results describe that branch, not public main. [The public-main integration report](port-rpc-parity-public.md) records the current adaptation, red/green proofs, and gates. Public main uses its existing call-argument renderer instead of the staging-only `tool_definition_renderers.go` implementation referenced below.

This report covers issue #47's three core RPC/read findings and batch PT-071-cli-rpc against Pi 0.87.1. No ACP implementation is added. Pi is the reference implementation; all upstream paths below resolve under `.upstream/v0.87.1/`.

## Assigned test files

| Upstream test file under `packages/coding-agent/test/` | Cases ported / total | Go evidence | Status |
|---|---:|---|---|
| `rpc-jsonl.test.ts` | 4 / 4 | `cmd/pig/rpc_jsonl_upstream_test.go:TestRPCJSONLUpstream` | ported |
| `rpc-prompt-response-semantics.test.ts` | 4 / 4 | `cmd/pig/rpc_prompt_semantics_upstream_test.go:TestRPCPromptResponseSemanticsUpstream` | ported |
| `rpc.test.ts` | 18 / 18 | `cmd/pig/rpc_upstream_test.go:TestRPCModeUpstream` | ported |
| `suite/regressions/5868-rpc-unknown-command-id.test.ts` | 1 / 1 | `cmd/pig/rpc_jsonl_upstream_test.go:TestRPCUnknownCommandPreservesRequestID` | ported |

Every Go case cites its upstream case line and retains its recognizable name. The four mapping rows retain their current upstream hashes. No case is skipped, live-only or designed out. `docs/parity/pending-tests-batches.md` is not regenerated.

The RPC integration tests retain the Anthropic model identity and upstream prompt strings. A local Anthropic HTTP/SSE server replaces live credentials. Bash recall extracts the random value from the actual provider request, not from a fixture variable. A held HTTP response replaces sleeps in streaming/queue tests. Tests count every prompt response after draining output through process exit, rather than stopping at the first matching response. Small deterministic replies use `keepRecentTokens=1` to give manual compaction a compactable prefix. The HTML test resolves Pi's returned relative filename in the isolated child's cwd; the upstream test and child share a cwd.

## Source fixes and regression evidence

| Finding | Exact upstream rule | Source fix | Regression and compiling mutation |
|---|---|---|---|
| F1: read renderer metadata escaped into live and persisted results | `core/tools/read.ts:110,158-188` leaves details undefined except `{truncation}` | `internal/codingagent/tools/read.go` attaches only truncation when present; display-only fields are not serialized; `tool_definition_renderers.go` binds live read/write cards to retained call arguments | `TestBuiltinToolResultDetailsWire`, `TestRPCToolDetailsPersistAndReplay`, `TestInteractiveBuiltinFileResultsWithoutPrivateDetails`; injecting an empty read details object fails the wire guards and RPC scenario 28; disabling argument-based presentation fails the live TUI guard and the previously red escaped-output read scenarios |
| Same leak in write | `core/tools/write.ts:85-88` returns undefined details | `internal/codingagent/tools/write.go` no longer attaches write preview state or performs the preview-only stat | Reattaching `WriteDetails` fails the tool guard and full result comparator |
| PascalCase/non-sparse metadata on truncated or limited bash, grep, find and ls results | `core/tools/bash.ts:324-374`, `grep.ts:284-307`, `find.ts:149-165`, `ls.ts:143-160` | `tools/details.go` and `tools/truncate.go` serialize the upstream field names and omit absent optional fields | Actual tool-result tests were red on the old shapes; tools scenario 15 compares complete returned objects with Pi's published tool factories |
| F2: nested result duplicated event-level `isError` | `packages/agent/src/agent-loop.ts:870-894` places the flag beside result and on the persisted toolResult message | `cmd/pig/rpc_events.go` stops injecting it into result and partialResult | `TestRPCToolExecutionResultExactWire`; reinserting the flag fails the exact event guard, the production JSON/RPC-to-persistence test, and RPC scenario 28 |
| F3: idle RPC ignored SIGTERM with stdin open | `modes/rpc/rpc-mode.ts:366-378,728-744` disposes, detaches input and exits 143; SIGHUP exits 129 off Windows | `cmd/pig/rpc_mode.go` selects cancellation at input admission and detaches the reader wait; `cmd/pig/main.go` registers RPC SIGHUP; shutdown runs before root cancellation | `TestRPCSignalWithOpenStdin` verifies the real process, open stdin, exit status and extension cleanup marker; disabling the cancellation select compiles and hangs both signal cases; scenario 29 fails with timeout/status -1 rather than 143 |
| Later agent_end events replayed old conversation turns | `packages/agent/src/agent-loop.ts:110,164-319` emits newMessages for that run | `agent/agent_loop.go` snapshots `r.newMessages`, not the complete retained history; the obsolete history-copy helper is removed | `TestAgentEndContainsOnlyCurrentRunMessages`; using `r.context` instead compiles and produces `[user assistant user assistant]` on run two; scenario 28 compares both runs |
| Byte-truncation metadata used Go's maximum integer instead of Pi's line-limit value | `core/tools/ls.ts:141`, `find.ts:147`, `grep.ts:282` use Number.MAX_SAFE_INTEGER | The three callers pass `1<<53-1` | `TestBuiltinByteTruncationWireLimit`; substituting `1<<62` fails the metadata assertion and tools scenario 15's byte-truncated ls case |
| Grep/ls details rounded fractional requested limits | `core/tools/grep.ts:291`, `ls.ts:148` retain effectiveLimit | The tool metadata and list renderer retain float64 values; extension-hook conversion preserves the same sparse wire object | `TestBuiltinToolResultDetailsWire` and `TestListToolFractionalLimitWarnings`; integer-rounding mutations fail the 1.5 cases; tools scenario 15 compares the same calls with Pi |
| Shell output updates omitted the empty details object | `core/tools/bash.ts:270-276` supplies a details object even when neither optional field is present | `tools/shell_updates.go` always supplies that object for output updates | `TestShellResultAndUpdateDetailsWire`; replacing the object with nil compiles and fails with `null` instead of `{}`; tools scenario 15 also compares the full nonempty shell output update |
| JSONL escaped Unicode separators | `modes/rpc/jsonl.ts:10-11` uses JSON.stringify and LF framing | `coding/rpcclient/jsonl.go:SerializeJsonLine` preserves literal U+2028/U+2029 and escaped backslashes; server and client use it | All four JSONL cases pass; bypassing the separator handling compiles and fails the literal-separator assertion; scenario 02 compares the actual Unicode response |

The built-in audit also checks edit against `core/tools/edit.ts:210`; its diff/patch/firstChangedLine shape already matches. PowerShell delegates to the same shell result construction through `core/tools/powershell.ts:49-60`; the shared shell metadata implementation and its regression cover that path. No separate PowerShell executable qualification is claimed.

The interactive-rendering cross-check initially failed scenarios 27 and 28 because the live read/write presentation still depended on private tool result state. Core file-tool cards now use the existing built-in definition renderers, which own the retained call arguments. `TestInteractiveBuiltinFileResultsWithoutPrivateDetails` reproduces both the leaked collapsed read body and the missing write preview. Its disabling mutation compiles and fails. The whole interactive-rendering family passes after this source fix; neither escaped-output comparator changes.

The existing `TestJSONAndRPCProductionToolEventsMatchPersistedResult` asserted the wrong nested `isError` placement. It now compares the event-level flag to the persisted message flag and explicitly rejects a nested flag. This corrects the asserted contract; it does not drop the content, image, details or persistence assertions.

Fresh read/write results are checked on disk and after reopening the saved Session. Existing historical JSONL is not rewritten or scrubbed: unknown extension-authored metadata must not be deleted by a compatibility reader.

## Why the old RPC scenarios passed

The original RPC family never executed a built-in read/write tool cycle. Scenario 06 sent an arithmetic prompt and checked selected event-type/text substrings. Extra fields in a nested result therefore had no assertion to fail. Most importantly, no tool result was present in that transcript at all. Existing details unit tests called `extension.ToolResultDetailsFor`; only extension hooks used that projection. RPC and Session persistence received the unconverted internal details structs.

The RPC driver did not silently delete `details` or `isError`. The gap was missing tool execution coverage and substring-only event comparison. Its general normalized-output comparator also collapses whitespace, which is unsuitable for proving complete tool text. The new checks do not use it.

Scenario 06 now compares the complete JSONL transcript for three pairs. Scenario 28 compares complete read/write event transcripts, get_messages and get_entries responses, including the second agent_end. Scenario 29 signals an idle process while the input pipe remains open. Tools scenario 15 compares complete ordinary, truncated, limited and fractional-limit results with Pi's exact published `create*Tool` implementations. The probes retain content and every result field.

`canonical_json` only canonicalizes JSON encoding and object-key order. It rejects malformed records and preserves field presence, nulls, array order, record order, numeric precision and whitespace inside strings. `TestCanonicalJSONLPreservesWireDifferences` proves that extra nested isError, extra null details, missing fields, changed text whitespace, duplicate records, reordered records and distinct large numbers remain unequal.

### Retained normalization inventory for these checks

| Check | Normalization retained | Reason |
|---|---|---|
| RPC 02 and 29 | None beyond the existing driver's removal of terminal record-delimiter whitespace | These outputs have no dynamic payload fields; result text remains JSON-escaped inside the record |
| RPC 06 | Object-key/JSON encoding canonicalization; numeric `timestamp` values become 0 | Go and Node enumerate object keys differently and run at different wall-clock times; no fields or string content are removed |
| RPC 28 | The same canonicalization; numeric and ISO entry timestamps; generated entry/parent/leaf keys | Each process creates independent time and opaque identity values. Command IDs and toolCallIds are not matched. The entries/since/tree tests separately prove reference identity and order |
| RPC snapshots | The existing runner replaces only the random digits in its own fixed-width cwd snapshot paths | Each binary owns a different ephemeral copy of the same fixture; this is a harness path identity, not application output normalization |
| Tools 15 | Recursive object-key order; random bash full-output filename becomes OUTPUT_FILE in its field and notice | Both probes first verify the file exists and remove only their own file. No other value, result field, whitespace, null or omission is changed |

Explicit equal system prompts and the canonical `test-faux/faux-1` model make fixture inputs equivalent rather than masking branding or model differences in output. No result-field filter, ANSI stripping, whitespace collapse, usage-number replacement, event sorting, chunk merging or arbitrary error-text replacement is added. Existing unrelated RPC scenario normalizations remain unchanged; they do not carry built-in tool results and did not conceal F1/F2.

The strict comparator exposed three faux-fixture inconsistencies. Go labelled responses with API `faux` while the Node fixture used `test-faux`. The Node fixture queued a mutable start message that acquired final content before consumption. Its tool-argument JSON string used insertion order while Go used sorted keys. The fixtures now provide matching API identity, an independent initial message snapshot and deterministic argument JSON. These are fixture corrections, not new production divergences.

## Resource and performance evidence

The signal test waits for an RPC response before signalling, leaves stdin open, and checks a Node extension's synchronous disposal marker after the child is reaped. The command consumer detaches on cancellation; its process-owned OS stdin read ends with process exit. Session prompts, bash work, event forwarding and extension shutdown retain their existing drain paths. No cancellation retry, shutdown sleep, larger timeout, new background Session task or ACP workaround is introduced.

Tool tests include ordinary and user-limited reads, a 2001-line read, 60000-byte shell output, 300 long filenames that trigger byte truncation, and fractional limits. Temporary tool output files created by the new probes are checked and removed.

`BenchmarkRPCJSONLToolResult` measures a representative two-line tool result through the production serializer. The retained run on the lane host measured 1372 ns/op, 386 B/op and 8 allocations/op. CPU and allocation profiles are retained with the benchmark log. This is measurement evidence, not a throughput or performance-improvement claim.

## Verification

Evidence lives in the task's external evidence directory. Compiling mutation logs include `jsonl-mutation.log`, `unknown-id-mutation.log`, `prompt-count-mutation.log`, `source-mutations.log`, `rpc-mutation.log`, `tools-mutation.log`, `signal-mutation.log`, `signal-parity-mutation.log`, `metadata-overlay-mutation.log`, `metadata-parity-mutation.log` and `renderer-projection-mutation.log`. Paired failures also retain raw output and rerun commands under `test/parity/artifacts/`.

| Gate | Result |
|---|---|
| Assigned 27 cases | pass |
| Exact tool/result, persistence/replay, signal cleanup and agent_end guards | pass |
| Complete touched-package suites and `go test ./test/parity/...` | pass |
| Focused race-detector guards for agent_end, result details and shell updates | pass |
| `go vet ./...` | pass |
| `GOOS=windows go vet` for touched production packages and the parity packages | pass; Windows runtime execution is not claimed. The parity TestMain's pre-existing syscall.Kill compile failure is fixed with os.Process self-signalling and a Windows status fallback |
| `go tool golangci-lint config verify` | pass |
| `make lint-changed LINT_BASE=public/main` after `git fetch public main` | pass |
| `make lint` | pass |
| `make ci-contracts` | fails only at the expected other-lane hot-path test-porting blockers; all four assigned files are absent from that list |
| `make ci-drift` | pass |
| `go test ./...` | fails outside this batch: `TestLinuxShardsRetainEveryCheck` has a pre-existing test-porting-release gate-list mismatch; `TestRunner_NoUnclassifiedPiGMethods` lacks the pre-existing BindAbort classification; the subprocess suite exceeds its default 10-minute aggregate limit while building a Rust fixture. These are retained blockers, not waived or retried into a success claim |
| RPC/tools/JSON/print/interactive-rendering/export-html parity families | pass against Pi 0.87.1, including unchanged escaped-output read comparators after the presentation fix |
| `go fix -diff ./...` | pass after converting two test-only split loops to SplitSeq |

`test/parity/interfaces/pig-go.json` is regenerated with `go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json`. Coverage is regenerated with `make coverage RESULTS=` to avoid importing an inherited last-run results file into this lane's status claims. No upstream test count or baseline is lowered. No divergence or lint suppression is added.

## Commits and integration surface

- `d499f7dccd`: JSONL serialization and the framing/unknown-ID upstream cases; pushed to staging.
- `9405a8a5f6`: every RPC mode and prompt-response case; pushed to staging.
- Source-fix and final-evidence commits are recorded in the final lane reply.

The first development-wide test run overlapped intermediate source edits, and one snapshot gate overlapped an edit to this report. Those runs are not acceptance evidence. Verification ran again against frozen sources; the retained full-suite blockers above are from the completed repository-wide run, not those invalidated development runs.

Shared production changes are limited to `agent/agent.go` and `agent/agent_loop.go` for run-local agent_end messages; `ai/test_faux.go` for the fixture API identity; `cmd/pig/main.go`, `rpc_mode.go`, `rpc_events.go`, `rpc_types.go` for signal lifecycle and wire serialization; `coding/rpcclient/jsonl.go` and `commands.go` for shared JSONL serialization; `internal/codingagent/tools/{read,write,details,ext_details,truncate,ls,grep,find,shell_updates}.go` for result metadata; and `internal/codingagent/tool_render_list.go` for fractional-limit rendering; `internal/codingagent/tool_definition_renderers.go` for call-owned file-tool presentation; and the numeric limit fields in `coding/extension/events.go` for typed extension details. The parity driver's test-only shutdown handler also uses the portable os.Process API instead of syscall.Kill; the initial Windows vet failure and successful final vet are retained. The integrator must preserve the run-local event change and the metadata types when merging other agent/tool lanes. The parity driver/schema/linter, fixture provider and generated inventory/coverage files change with their owning evidence.
