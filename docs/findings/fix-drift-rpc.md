# RPC wire audit fixes

## Scope and reference

This change addresses W04, W16, W17, W18 and W22 from the wire-drift audit against Pi 0.87.1. The base is `96284e76b` on the integration candidate. W05, W25 and the requested built-in tool metadata repairs already exist in that base. They are verified here, not reimplemented. No new divergence or lint suppression is added.

All Pi source references below are relative to `packages/coding-agent/src/` in the pinned upstream mirror.

| Finding | Pi rule | Root fix or inherited disposition |
|---|---|---|
| W04 | `modes/rpc/rpc-mode.ts:354-363,502-508,538-541` writes Session events before returning mutation responses | Flush the existing Session event barrier before thinking-level, model and compaction responses. Flush failed compactions too. Do not change the earlier lane's input-turn executor. |
| W05 | `modes/rpc/rpc-mode.ts:151-210` writes status and widget calls synchronously in invocation order | Already fixed by the per-parent host call lane. The paired probe compares all four UI records in order. Reintroducing concurrent synchronous calls fails `TestExtensionCallsApplyInSendOrder`. |
| W16 | `core/session-manager.ts:1029-1058,1763-1782` preserves a missing explicit path and defers its first write; `core/agent-session-runtime.ts:195-222` reports a successful switch | Implement open-or-create at `SessionManager.Load`, rather than synthesizing an RPC-only success. The file remains absent until an assistant is appended. RPC selects the new identity and exact path. |
| W17 | `core/sdk.ts:231-255,405-412` selects and clamps thinking before bootstrapping metadata | Pass `ThinkingLevel` through `SessionStartOptions` and `SessionOptions`. Apply the explicit preference before initial persistence, including the no-model clamp to off. Remove RPC's late Agent-only override. |
| W18 | `core/agent-session.ts:1015-1028,3395-3404` emits the exact appended recovery context edit before continuing | Resolve the newly appended entry and enqueue `EntryAppendedEvent` on the Session event funnel before refreshing context. |
| W22 fork | `core/agent-session-runtime.ts:274-287` rejects missing and non-user fork targets with `Invalid entry ID for forking` | Validate at the shared fork operation and return the exact diagnostic. |
| W22 JSON | `modes/rpc/rpc-mode.ts:755-771` forwards `JSON.parse`'s diagnostic | Scan rejected JSON for V8's contextual EOF, string, number and token diagnostics. Count positions in UTF-16 and preserve CR, LF and CRLF line accounting. Valid command decoding remains unchanged. |
| W25 | `modes/rpc/rpc-mode.ts:54-63` enters RPC without host tool-inventory diagnostics | Already removed on the base. Both paired probes require empty stderr, including with a Node extension loaded. Reinserting a startup diagnostic fails the CLI guard. |
| Tool metadata | `core/tools/write.ts:85-87`, `read.ts:157-188`, `bash.ts:50-53,273`, `grep.ts:43-47,291`, `find.ts:43-46,285`, `ls.ts:25-28,148` expose sparse lower-camel details | Already fixed on the base, including truncation fields, fractional limits and renderer-only fields. The complete tools family, including `15-builtin-tool-wire-details`, is re-probed. |

## Red, green and mutation evidence

Tests are added before changing the corresponding production behavior. The original failures are:

- `TestRPCMutationEventsPrecedeResponses`: the thinking response arrives with no preceding event.
- `TestRPCSwitchMissingCreatesExplicitSession` and `TestSessionManagerLoadMissingDefersExplicitPath`: opening the absent file returns ENOENT.
- `TestRPCInitialThinkingEntryMatchesState`: the initial entry says medium while state says off.
- `TestRecoveryPublishesContextEditEntry`: the persisted context edit has no corresponding public event.
- `TestRPCInvalidForkDiagnostic`: the error is `fork: entry "absent" not found`.
- `TestRPCJSONDiagnosticsMatchNode`: `{` reports generic end-of-input instead of the property-name diagnostic. Other syntax classes expose Go parser prose.

Supplemental tests added after the source fix are not claimed as initially red. The explicit-preference cases in `TestInitialThinkingPrecedence` and the separate compaction ordering case are mutation-proven. No-model, non-user-target and empty-compaction cases extend boundary coverage without separate mutations. The recovery ordering check is added after its missing-publication mutation. `TestRPCJSONDiagnosticsMatchNode` also exposes and fixes CR line accounting and the token-excerpt boundary at UTF-16 position 10 before those corrections land.

The following temporary mutations compile and fail behaviorally. All are removed from the final tree.

| Mutation | Failing guard |
|---|---|
| Bypass the thinking and compaction `FlushEvents` calls | Both thinking and compaction subtests fail; compaction has only `compaction_start` before its response |
| Disable the missing-file open branch | Missing-path Session and RPC tests fail with ENOENT |
| Ignore `opts.ThinkingLevel` | `TestInitialThinkingPrecedence` fails for explicit off and clamping; the complete initial-state probe differs from Pi |
| Disable recovery entry publication | The Session test and paired flow probe fail because the durable edit is absent from public events |
| Restore the old fork error | Exact RPC error-record comparison fails |
| Return Go JSON diagnostics | Node-oracle table and canonical `01-rpc-malformed-input` fail on complete error records |
| Run synchronous extension host calls concurrently | `TestExtensionCallsApplyInSendOrder` records an inversion |
| Reinsert an RPC startup inventory diagnostic | `TestRPCInitialThinkingEntryMatchesState` rejects nonempty stderr |

The saved mutation binary has SHA-256 `daa35ddcb9286b32bff53e3172c740b50e79c39bf932a44f82e4ecdb483b7592` and is deliberately older than the final source. Its scenario run uses `-pig-parity.allow-stale=true` only to execute that known compiling mutation. Initial attempts without the explicit Pi package path and without this mutation-only freshness override fail setup and do not count as mutation proof.

## Paired comparator

`01-rpc-malformed-input` and `04-rpc-jsonl-framing` change from normalized output with an erased parse-error line to complete canonical JSONL equality. The framing scenario now sends actual CRLF input and waits for a correlated final response instead of sleeping.

`33-rpc-wire-mutations` runs both real RPC executables against one controlled local Anthropic-compatible endpoint per process. It compares complete mutation, UI, recovery, compaction, invalid-fork and missing-path switch/state records. It also compares exit status and requires empty stderr. Provider stream records outside the recovery-entry contract are not claimed by this scenario and remain in the raw probe artifact. A persisted-entry query must contain exactly the recovery entries received incrementally.

`34-rpc-initial-thinking` compares complete state and initial-entry responses with a reasoning model, a medium configured default and explicit `--thinking off`. Both new scenarios declare three runs. IDs are mapped injectively by encounter order. Only exact temporary roots, provider URLs, generated session filenames and timestamps are substituted. Arrays, event order, field presence, nulls, error prose, contents and thinking levels are not removed or normalized.

Run the probes independently and retain raw records with:

```bash
export PATH="/absolute/toolchain/bin:$PATH"
go build -o bin/pig ./cmd/pig
EVIDENCE=$(mktemp -d)
python3 test/parity/testdata/rpc-wire-drift.py pi "$EVIDENCE/pi" > "$EVIDENCE/pi.jsonl"
python3 test/parity/testdata/rpc-wire-drift.py pig "$EVIDENCE/pig" > "$EVIDENCE/pig.jsonl"
diff -u "$EVIDENCE/pi.jsonl" "$EVIDENCE/pig.jsonl"
python3 test/parity/testdata/rpc-wire-drift.py pi --initial > "$EVIDENCE/pi-initial.jsonl"
python3 test/parity/testdata/rpc-wire-drift.py pig --initial > "$EVIDENCE/pig-initial.jsonl"
diff -u "$EVIDENCE/pi-initial.jsonl" "$EVIDENCE/pig-initial.jsonl"
make parity-family FAMILY=rpc
```

## Resource and async disposition

RPC reuses the existing cancellable event barrier and output owner. It creates no new goroutine or queue. Responses wait for already-produced events; shutdown releases the barrier through its existing context. Recovery publication uses the ordered Session funnel before retry continuation. A missing-path open allocates an ordinary unflushed Session and opens no retained file descriptor. Diagnostics scan only rejected input, retain no state between lines, and allocate proportionally to the rejected line. No change runs on the TUI input/render loop.

On Linux amd64, Go 1.27.1, Node 24.19.0 and an Intel Xeon 6746E, `BenchmarkRPCJSONDiagnostic` records 468 ns/168 B/4 allocations for a short malformed object, 40.9 µs/33 KB/8 allocations with 4 KiB of string data, and 462 µs/378 KB/8 allocations with 50 KiB. `BenchmarkSessionStartupWithoutTranscript` records 40.6 µs/12.7 KB/118 allocations and joins each Session's forwarding goroutine. CPU/allocation profiles are captured for both. The diagnostic profile identifies string-to-rune/UTF-16 conversion, diagnostic position scanning and GC work. These are representative observations under shared-host load, not a speedup claim or a transport backpressure qualification.

## Shared files for integration

Production shared edits are `cmd/pig/rpc_mode.go`, `coding/runtime.go`, `coding/session.go`, `coding/session_recovery.go`, `internal/codingagent/session_manager.go` and `internal/codingagent/session_selectors.go`. The new diagnostic scanner is `cmd/pig/rpc_json_diagnostic.go`.

Existing test/helper edits are `cmd/pig/rpc_upstream_test.go` (optional startup arguments), `internal/codingagent/session_manager_test.go` (replace the incorrect missing-file failure expectation with Pi's open contract), and `internal/codingagent/status_lifecycle_test.go` (use a directory to retain its status-cleanup-on-load-failure assertion now that missing files succeed). `test/parity/interfaces/test-mapping-v0.87.1.json` adds evidence without promoting pending/partial test files. It corrects the claim that strict Go reader failures prove upstream's empty-array reader contract.

Shared evidence edits are `CHANGELOG.md` under 0.3.0 Fixed, `docs/parity/PORT_MAP.md`, the two tightened RPC scenarios, generated `AGENTS.md` coverage, `test/parity/coverage.md`, `test/parity/interfaces/pig-go.json` and `test/parity/interfaces/recommendations-v0.87.1.json`. Regenerate inventories and coverage when integrating with other lanes. No PORT_MAP entry is newly promoted.

## Gate status

Focused regression tests, race-enabled regressions, Linux `go vet ./...`, Windows vet for the touched packages, touched-package golangci-lint, full-repository `make lint` and `make lint-changed LINT_BASE=96284e76b` pass. The default `make lint-changed` cannot find a merge base with main in this lane, so the assigned integration base is explicit. `go fix -diff` for touched packages is empty.

The complete RPC, tools, Session, compaction, print, JSON and model-resolver-selector parity families pass at their declared durability. The full `cmd/pig` package passes. The full `coding`, `internal/codingagent` and tools packages pass after correcting the status-test input. The initial broad touched-package run fails only that outdated status-test expectation; it is not recorded as a flake. An earlier shell invocation stops at its 240-second tool timeout before the long CLI package finishes and supplies no passing evidence.

`go test ./test/parity/...` fails three existing closure dashboard/import tests because D78 is open rather than provisionally approved. The actual divergence denominator is preserved. The remaining parity packages pass. The first `go test ./...` also hits the stale status-test expectation, the subprocess package's aggregate ten-minute timeout during packed Rust fixture startup, and `TestColorDetectionMatchesPi` failing with Node stdin `EAGAIN`. The status test is corrected; the unrelated full-suite failures are not called passes or hidden with longer timeouts. The final `go test ./...` passes all touched production packages and still fails the subprocess package's aggregate ten-minute timeout during packed fixture startup, the three closure tests, and the TUI Node-stdin `EAGAIN` oracle test. These remain explicit integration blockers. The direct changed-source hygiene scan passes; the aggregate hygiene target remains blocked only by the two pre-existing private-path reports.

`make -k ci-contracts ci-drift` is run twice, before and after regenerating the Go interface inventory, recommendations and coverage. The final run has no lane-owned inventory or coverage drift. It remains blocked by baseline pending/partial hot-path test ports, D78 lacking `SCRUTINIZED:approved`, and private-path findings in the pre-existing `test/parity/unit-evidence/fix-ext-ui-state.md` and `fix-node-scrollview.md`. This lane does not grant approval, lower a release denominator, suppress findings or edit those unrelated reports.
