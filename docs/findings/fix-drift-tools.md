# Tool wire drift fixes: W06, W07, W08, W12 and W26

Pi 0.87.1 is the oracle. This slice starts at integration commit `96284e76befed314dd69d4ef01b013ac6c40902e`. All Pi source paths below are relative to `.upstream/v0.87.1/`. The integrated release notes use the 0.3.0 heading.

## Empty-read history follow-up — COMPLETE

This follow-up starts at the integrator's `c249c310c` tree, which already contains the prior tool fixes and the strengthened canonical probe. Pi 0.87.1 `packages/coding-agent/src/core/tools/read.ts:185-188` returns one text block even for an empty file. `packages/agent/src/agent-loop.ts:880-894` preserves those blocks when creating the tool-result message. Pi also constructs a text block for an empty thrown error (`agent-loop.ts:863-867`).

The Go result flattened both explicit empty text and no text to `Content == ""`. The shared message constructor then dropped that string. `TestRPCEmptyReadTextPersistsAndReplays` fails before the fix in all three views: live Session history, the JSONL file and reopened history. The integrator's strict `tools/17-tool-rpc-error-and-bash-wire` comparison fails at `/34/record/message/content` with `pig=0 pi=1`.

`AgentToolResult.ContentPresent` now retains that otherwise-lost Go representation state without adding a serialized field. The read producer sets it, and the shared message constructor consumes it. Nonempty text remains implicitly present; zero-value results and image-only results do not acquire an empty text block. An explicit hook text replacement remains a text block, while a complete empty content-array replacement clears its presence. The same state survives the shared SDK response decoder, tool-result hooks, execution-end events, render payloads, resumed cards and HTML export reconstruction. The existing SDK payload shape is unchanged for every language. Multiple text blocks still use the existing flattened text representation; this fix does not claim arbitrary block-order fidelity.

The pure conversion guards distinguish empty error text, explicit text overrides, clearing content, empty arrays and image-only results. Additional regressions drive the real subprocess response adapter, hook projection, resumed tool card and export converter. A compiling mutation that removes the message constructor's presence condition fails the pure guards, the Session/disk/reopen regression and the strict scenario at the original JSON path. Removing the read producer's presence marker separately fails the Session/disk/reopen regression. All mutations are removed.

The final canonical tools scenario passes its three real Pi/PiG pairs without weakening its typed comparator or aliases. The entire tools and export-html parity families pass. The whole RPC and JSON families are re-probed; remaining failures are the already-owned RPC retry ordering (`rpc/17`), provider stream snapshots (`rpc/33`) and provider error API (`json/04`), not empty tool-result content. The existing H5 initial-update contract remains intact.

All touched-package suites pass, including the complete subprocess suite. The repository-wide `go test ./...` passes every package except the three existing D78 closure-dashboard tests; extension conformance passes too. Focused race tests, Linux vet, Windows vet, full lint, changed-package lint, signature/test inventories and generated coverage pass. `make ci-contracts ci-drift` reaches the existing hot-path test-porting blocker when run with disposable agent/home directories; the initial inherited worker directory had an unrelated trust-lock shape error. Separate `ci-drift` retains D78's pending approval and the existing D79/D78 ordering failure. No approval, test disposition, timeout or suppression changes.

`BenchmarkToolResultMessageEmptyText` measures creation and JSON serialization of an explicit empty-text result: 1979 ns/op, 642 B/op and 15 allocations on Go 1.27.1/linux-amd64. CPU/allocation profiles and red/green logs are retained with the external lane evidence. This is not a speedup claim. The presence state introduces no background work or new queue/lifetime ownership.

Shared integration files for this follow-up are `agent/agent.go`, `agent/tool_execution.go`, `internal/codingagent/tools/read.go`, `cmd/pig/rpc_events.go`, the tool-result hook/event converters, the subprocess result decoder/adapter/renderer, resumed-card and export reconstruction, their regression files, the changelog and the signature/test/coverage ledgers. The strengthened scenario and probe come from the integrator; this change only updates the scenario's closed-boundary comment and read source coverage. The commit does not rewrite historical session files that already lost their text blocks.

## H5 follow-up: initial bash partial content

The strict harness is merged in `149ee14d1`. Its rpc/26 comparator remains `json_output_equal = true` with complete stderr comparison and the existing narrow D2/D22 identity aliases. No output crop, tool-update alias or comparator weakening is added.

Pi 0.87.1 `packages/coding-agent/src/core/tools/bash.ts:301-302` emits `{content: []}` before execution. The shell's existing empty callback represents that snapshot, but `cmd/pig/rpc_events.go` unconditionally inserted a text block while forming `partialResult`. The shared JSON/RPC serializer now represents absent text separately from explicit empty text. Only a partial snapshot without text or details is empty; completed results still retain explicit empty text.

A boundary probe against the actual Pi factory also verifies `bash.ts:270-276`: `printf '\357\273\277'` produces a later BOM-only output snapshot with `content:[{type:"text",text:""}],details:{}`. Dropping every empty string would corrupt that update. The presence of output details retains this text block, as well as ordinary nonempty output. Empty completed read results also remain text blocks (`read.ts:185-188`).

`TestRPCShellInitialUpdateHasEmptyContent` executes real bash through the production event serializer for `true`, `printf hello`, and BOM-only output. It checks the complete initial record, later text/details output, and an empty completed read. Before the fix, the initial-record cases fail with an inserted empty text block. A too-broad empty-string conversion separately fails the BOM boundary. After the fix, the full test passes, including under the race detector. A compiling mutation that always supplies a text pointer fails this regression and strict rpc/26 at `/13/partialResult/content: array/record count pig=1 pi=0`.

Final strict verification completes three pairs each for rpc/26, rpc/28 tool-result wire, json/01 event output, tools/15 built-in details, and tools/17 error/bash persistence. The full `cmd/pig` suite, Linux vet, Windows vet of `cmd/pig`, lint configuration validation, changed-package/full lint and `go fix -diff ./cmd/pig` pass. `make ci-contracts ci-drift` retains the existing hot-path test-porting blocker; separate `ci-drift` retains pending D78. `go test ./test/parity/...` retains the three D78 closure-dashboard failures. No new waiver or suppression is added.

`BenchmarkRPCJSONLToolResult` records 1374 ns/op, 386 B/op and eight allocations per operation, with CPU/allocation profiles retained in the external evidence root. The fix changes only synchronous serialization; it adds no task, timer, queue or resource lifetime. This is a measurement, not a speedup claim.

H5's shared-file edits are `cmd/pig/rpc_events.go`, its wire regression and payload benchmark call site, the rpc/26 source-coverage comment, the changelog, this report, and regenerated signature/coverage ledgers. The broader strict-harness merge is a separate commit, not attributed to the H5 source fix.

## Findings and source contracts

| Finding | Pi source and observable rule | Disposition |
|---|---|---|
| W06 | `packages/coding-agent/src/core/tools/write.ts:85-88`: successful write returns content and undefined details | Already fixed in the integration base by the RPC lane. Reattaching `WriteDetails` compiles and fails both the existing complete-result unit test and the real-Pi comparator. The new RPC scenario also checks write results in events, message queries, entry queries and the session file. No second success-path fix is made. |
| W07 | `packages/coding-agent/src/core/tools/read.ts:157-188`, `bash.ts:270-276,324-374`, `grep.ts:284-307`, `find.ts:149-165`, `ls.ts:143-160`: details are sparse lower-camel objects, including nested truncation fields | Already fixed in the integration base. The existing `tools/15-builtin-tool-wire-details` scenario still compares complete ordinary, limited, fractional-limit, truncated and update results against Pi's published factories. A PascalCase `ResultLimitReached` mutation compiles and fails the unit test and that comparator. No metadata tag is changed in this slice. |
| W08 | `packages/agent/src/agent-loop.ts:863-894`: thrown-tool failures contain `details: {}` and retain it in tool-result messages | The agent error constructor and every built-in error-result constructor now supply the empty object. Successful results remain unchanged. This also fixes PowerShell through the shared shell constructor. |
| W12 | `packages/coding-agent/src/core/bash-executor.ts:125,143-146`: cancelled bash has no exit code; `packages/coding-agent/src/modes/rpc/rpc-mode.ts:578-584` forwards that result | The lower executor already returned nil. `coding.Session` incorrectly replaced it with `-1`. `coding.BashResult` and `RPCBashResult` now retain an optional `*int`, serialized with omission. Zero and nonzero exit statuses remain present. The same optional status reaches persistence. |
| W26 | `packages/coding-agent/src/core/agent-session.ts:3498-3520`, `packages/coding-agent/src/core/messages.ts:30-43`: bash messages capture a numeric completion timestamp before deferral | The Session captures that timestamp with the result. `AppendBashExecution` accepts the complete `BashExecutionMessage`, rather than reconstructing an incomplete message from scalar arguments. The entry marshal/unmarshal path preserves its independent inner timestamp. The interactive caller supplies its completion time too. |

The pointer is a Go representation of Pi's `number | undefined`, not a new wire field or compatibility mode. Recording copies a present status value before queueing, so a caller cannot mutate an already-recorded numeric result. The existing best-effort persistence fallback remains unchanged in policy; its timestamp and optional exit status now match the persisted representation. No new concurrency, background task, normalization of extension-authored results, compatibility reader, divergence or lint suppression is added.

## Red, green and compiling mutations

Before production edits, these regressions fail:

- `TestSend_ToolExecuteError_BecomesLinkedErrorResult`: error details serialize as `null`, not `{}`.
- `TestBuiltinFailureDetails`: all seven built-ins fail both error and pre-execution cancellation cases with missing details. The cancelled write/edit/bash cases also verify that the original file remains unchanged.
- `TestCancelledBashWireAndPersistence`: cancellation exposes `exitCode:-1` in both the result and projected message; the message has no timestamp.
- `TestBashExecutionMessageTimestampRoundTrip`: an imported message loses its numeric timestamp and acquires `exitCode:null` on reserialization.
- The new RPC probe differs from real Pi in the complete error events, tool-result messages, queried entries, persisted entries, cancelled bash response and bash messages.

`TestDeferredBashRetainsCompletionState` is added after the initial fix. It is mutation-proven, not base-red-proven. It verifies timestamp capture before queueing, retained time at flush, and copied numeric exit status. Existing successful, nonzero-exit, deferred and user-operations bash tests continue to pass. Three existing shell tests asserted nil details for failures; their assertions now require the exact empty JSON object required by Pi's agent-loop error conversion, without dropping their text/status assertions.

| Compiling mutation | Unit failure | Complete-record comparison |
|---|---|---|
| W06: attach `WriteDetails` to successful write | `TestBuiltinToolResultDetailsWire/write` reports leaked Path/Content/Overwrote | Existing tools scenario 15 differs on the complete write result |
| W07: change the find JSON tag to `ResultLimitReached` | `TestBuiltinToolResultDetailsWire/find_limited` reports the wrong key | Existing tools scenario 15 differs on the complete limited result |
| W08: remove details from `agent.errorToolResult` | Thrown-tool regression reports null | New RPC scenario differs on the unknown-tool failure across events and persistence |
| W12: turn a nil lower-executor status into `new(-1)` | Cancelled-bash regression reports the extra status | New RPC scenario differs on the response, message, queried entry and disk entry |
| W26: marshal zero instead of `MessageTimestamp` | Round-trip, cancelled and deferred regressions fail | New RPC scenario rejects the invalid numeric timestamp |

All mutations are removed before final verification. The three upstream test-mapping entries gain supplemental evidence paths and retain their partial dispositions, missing-case descriptions and current source hashes. This slice does not claim to port every case in those upstream files.

## Canonical scenario and reproduction

`test/parity/scenarios/tools/17-tool-rpc-error-and-bash-wire.toml` runs `test/parity/testdata/tool-rpc-wire.py` against each real binary. Both use an isolated HOME, agent directory, cwd, local HTTP/SSE provider and fake key. The provider requests successful write/read, every built-in failure, and an unknown tool. The probe then runs direct bash with exit 7 and cancels a running bash process only after receiving its `BASH_READY` output.

The comparator includes each complete selected record: `tool_execution_end`, tool `message_start`/`message_end`, direct bash responses, queried tool/bash messages, queried entries and on-disk entries. Other event families are explicitly outside this scenario. It does not drop fields within a selected record, flatten text, strip whitespace, erase nulls, sort record arrays or replace error messages. The expected tool-call inventory comes from the fixture's calls; provider failures cannot make a tool-free run pass.

Only the exact fixture cwd, injective entry/parent IDs and clock values are aliased. Numeric message timestamps must have the correct type and range and remain equal across events, queries and disk. Entry timestamps are separately validated. Omission is never converted into an alias. There is no `normalize_replace` rule.

```sh
export PATH="/absolute/toolchain/bin:$PATH"
go build -o bin/pig ./cmd/pig
EVIDENCE=$(mktemp -d)
python3 test/parity/testdata/tool-rpc-wire.py pi --evidence "$EVIDENCE/pi" > "$EVIDENCE/pi.jsonl"
PIG_PARITY_PIG_BIN="$PWD/bin/pig" python3 test/parity/testdata/tool-rpc-wire.py pig --evidence "$EVIDENCE/pig" > "$EVIDENCE/pig.jsonl"
cmp "$EVIDENCE/pi.jsonl" "$EVIDENCE/pig.jsonl"
make parity-family FAMILY=tools
```

The integration uses the shared typed JSON comparator instead of this lane's local pre-comparison substitutions. It retains complete selected records, captures raw stdout/stderr, validates clock range and repeated message times, and checks queried entries against disk entries before comparison. Eight-hex identity syntax and path suffixes remain observable. Two additional empty-file write/read calls initially exposed lost explicit empty text in tool-result message creation; the follow-up above closes that boundary with the same complete comparison. The agent-loop source row remains partial for separately held execution-order work. H5 fixes the completion event, while the follow-up also preserves message history. The lane's digest below describes the earlier local-alias probe, not the integrated comparator.

The final aliased complete-record outputs have the same SHA-256: `54808e2b2c9f565976316ab72238ab9362845a8cd8bed01b0d18f47164468583`. External lane evidence retains the raw process records, stderr, session files, red comparison, each mutation log/diff and final comparison. An initial probe implementation mishandled array-valued provider user content; that incomplete experiment is not acceptance evidence. The corrected probe checks that every declared tool call actually finishes.

## Resource evidence

Cancellation waits for the executor and reaps the process; the probe joins its reader and HTTP server threads and kills only its own process group on failure. No retry, longer timeout or skip is used. Production result lifetime and Session queue ownership are unchanged. The deferred record adds one timestamp and retains an optional copied status. Pre-execution abort tests exercise mutation prevention, not cancellation during an already-running filesystem syscall.

`BenchmarkBashExecutionMessageWire` measures serialization for empty, 256-byte and 50-KiB bash output. The lane's Go 1.27.1/linux-amd64 Xeon 6746E run records 2699/3635/169280 ns/op, 1092/1605/116475 B/op and six allocations per operation. CPU and allocation profiles identify JSON quoting/reformatting, output-buffer cloning and GC. These are measurements, not a speedup or whole-Session resource-bound claim.

## Gates and unresolved integration blockers

| Gate | Result |
|---|---|
| Touched-package tests: `./agent ./coding ./cmd/pig ./internal/codingagent ./internal/codingagent/tools` | Pass |
| Focused regression race tests | Pass |
| `make parity-family` for tools, rpc, json and print | Pass with each scenario's declared durability; clean child environment |
| `go vet ./...`; Windows vet of all touched packages | Pass; native Windows runtime is not qualified here |
| `go tool golangci-lint config verify`; touched-package lint; `make lint` | Pass |
| `make lint-changed LINT_BASE=96284e76be` | Pass against the integration base; the default `main` ref is unavailable in this lane |
| `go fix -diff` on touched packages | Empty |
| Interface inventory/drift and test inventory | Pass after regenerating `pig-go.json`; current upstream test hashes are verified |
| `make ci-contracts ci-drift` | Stops at existing pending/partial hot-path test ports; no tag, disposition or ported baseline is weakened |
| Separate `make ci-drift` | Stops at existing D78 `SCRUTINIZED:pending`; this slice does not approve it |
| Separate divergence guard | Pass with the existing baseline unchanged |
| Separate source hygiene | Stops at private-path findings in existing `test/parity/unit-evidence/fix-ext-ui-state.md` and `fix-node-scrollview.md`; not changed here |
| `go test ./test/parity/...` / repository-wide closure tests | Coverage freshness is corrected by regeneration. Remaining failures are the existing assertions that treat D78 as approved/provisional rather than pending/open |
| `go test ./...` | Fails outside touched packages: the subprocess suite exhausts its default aggregate 10-minute budget while `TestPackedPromptHandlerBodyFIFO/rust` is active; `TestColorDetectionMatchesPi` reports Node `EAGAIN` reading its oracle input; closure has the D78 failures above. These results are not retried into a success claim |

Coverage is regenerated with `make coverage RESULTS=` so an inherited last-run file cannot become this lane's evidence. No PORT_MAP disposition or file-closure claim changes. Full release acceptance remains blocked by the recorded repository-wide failures.

## Shared-file handoff

- `agent/tool_execution.go` and `agent/tool_execution_test.go`: empty failure details and thrown-tool guard.
- `internal/codingagent/tools/{read,write,edit,grep,find,ls,shell_tool}.go`: failure constructors only; retain the already-integrated W06/W07 success metadata fixes.
- `internal/codingagent/tools/{powershell_test,shell_tool_unix_test}.go`: correct old nil-details assertions. `failure_details_test.go` is new.
- `coding/session.go`: optional bash status, completion timestamp, copied status ownership and typed append; `coding/session_test.go` and `session_user_bash_operations_test.go` adapt numeric-status assertions. `session_bash_wire_test.go` is new.
- `cmd/pig/rpc_types.go` and `rpc_mode_test.go`: optional status DTO and existing zero-status test. No RPC dispatch or event-ordering code changes.
- `internal/codingagent/session.go`: typed append plus timestamp marshal/unmarshal; `interactive_turn.go` supplies the complete message. `session_test.go` and `slash_commands_test.go` adapt existing append calls. `session_bash_wire_test.go` is new.
- `test/parity/interfaces/pig-go.json`, `test/parity/interfaces/test-mapping-v0.87.1.json`, `test/parity/coverage.md` and the generated `AGENTS.md` block: signature inventory, supplemental test evidence and one scenario. Regenerate after merging, rather than manually reconciling generated counts.
- `CHANGELOG.md`, this report, the new tools scenario and its Python probe carry the user-facing note and reproducible evidence.
