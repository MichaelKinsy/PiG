# Strict parity comparison handoff

## rpc/26 identity follow-up

The lead-approved D2/D22 follow-up adds seven exact literal identity pairs and explicitly selects the README, documentation, and examples destinations. The complete system preamble and documentation section remain compared. The fixture project path keeps its `AGENTS.md` suffix and instructions. Generated session filenames retain the directory suffix, exact ISO-millisecond/UUID `.jsonl` syntax, and the same ID bijection as `data.sessionId`. Every alias and its justification appears in the normalization inventory.

`TestSessionStatsIdentityAliasesPreserveSystemSections` fails before the change on the documentation section and passes afterward. Its mutations still reject changed instructions, whitespace, topic filenames, destination suffixes, added/missing text, other sections, statistics, and filename references. Literal aliases are scoped, typed, and non-cascading. New README/examples roots require explicit selection, so existing path aliases do not gain substitutions. Harness unit/race tests, scenario inventory/lint, full lint, Linux vet, and Windows vet pass. Cross-probes for RPC chat, unknown-model diagnostics, tool-result records, and JSON project-trust output pass at their declared durability. The broad contract/drift gates retain the baseline hot-path-test and pending-D78 blockers listed below.

The real Pi 0.87.1 run now passes the identity section and stops at a newly exposed non-identity difference, H5: the first bash `tool_execution_update.partialResult.content` is `[{"type":"text","text":""}]` in Pig and `[]` in Pi. Pi's `packages/coding-agent/src/core/tools/bash.ts:301-302` explicitly emits the empty array. Pig's `internal/codingagent/tools/shell_tool.go:122-126` sends an empty text update through its text-based callback. This is not licensed by D2/D22 and remains unmasked for fix-drift-tools / fix-drift-rpc. The final stats match in the retained pair, including 1484 context tokens and 1.159375 percent. The full scenario cannot honestly be marked green until that source-level tool-update difference is fixed. No production change is made in this follow-up.

Follow-up evidence is under `/tmp/harness-identity-followup`; the manifest's `identity-followup` result supersedes the earlier first-failure diagnosis for this scenario.

## Scope and pins

This change hardens the parity harness. It does not fix production behavior. The only production-source edits reclassify the D67 and D69 markers as additive policy. The base is `96284e76befed314dd69d4ef01b013ac6c40902e`. The wire-audit reference is `e00bc6a2dbfa5672abbdc83f792a652442c77a29`. The oracle is the real Pi 0.87.1 executable under `extensions/sdk-ts/node_modules/.bin/pi`, with Go 1.27.1 and Node 24.19.0.

The paired result manifest is [harness-strict-compare-results.json](harness-strict-compare-results.json). It records every selected scenario and hashes the retained failing captures. The selected-scenario denominator is the manifest's name list, not a hardcoded test count. There are 65 selected scenarios, with 51 passing and 14 remaining red after correcting fixture barriers and narrowing permitted identity substitutions. Failed scenarios remain enabled. The runner stops durability at the first behavioral failure; passing scenarios use their declared runs.

Raw captures, command logs, mutation logs, and profiles remain under `/tmp/harness-strict-evidence`. The manifest's `final`, `refined`, and `provider-final` labels identify successive harness revisions, not retries of an unchanged flake. The first provider fixture used an extension and failed Pig model preflight; its OpenAI content handling was also incomplete. It is discarded. The maintained real-provider fixture is harness-owned HTTP plus isolated `models.json`, so the provider/Session/mode path does not depend on extension registration.

## Harness defects fixed

- RPC discarded trailing stdout framing and excluded stderr. It now retains both complete streams and keeps raw stdout beside optional key-canonicalized output.
- RPC steps searched all earlier output. Each step now starts at a pre-command byte offset, requires complete lines, and can match correlated fields together through `wait_event`. Tmux new-output waits require every marker to be new; retained state uses `wait_visible_contains`.
- Exact text comparators accepted unexplained regex replacement. Each rule now carries a per-scenario reason, and loading, evaluation, and lint reject missing reasons. JSON comparison never runs text normalizers.
- Entry and UI IDs collapsed to one constant. Explicit JSON pointer aliases now use a bijection across IDs and references. Missing/null/type/order/content distinctions remain observable. Path aliases use typed segments, so literal text such as `<temp>` cannot impersonate a real path.
- Whole errors, footer rows, token counts, transcript rows, export destinations, and HTML regions were erased. Those rules are removed or narrowed to the actual unstable scalar or exact fixture-root prefix. Full export files and complete provider HTTP bodies are compared.
- The normalization inventory is generated and checked by `make lint-scenarios`, which is a `ci-drift` dependency. It lists each rule and owner, plain-layout limitations, crops, and reviewed shared transformations. It is a review inventory, not proof that a cropped or summarized surface is complete.

`rpc/21` previously waited for the clear-name notification after sending `get_state`; both binaries had already emitted it. The notification now belongs to the clear command's barrier. The login dialog's title was similarly retained state, not new output from submitting the domain; only the newly emitted code prompt is a new-output barrier. No timeout is increased.

## Remaining red scenarios

All paths below are under `test/parity/scenarios/`. W identifiers refer to the wire audit. H identifiers are local handoff findings, not approved divergences. Pi source references are relative to `.upstream/current/` and refer to 0.87.1.

| Scenario | Finding and observed difference | Pi source and owner |
|---|---|---|
| `rpc/01-rpc-malformed-input.toml` | W22: `not-json` exposes Go parser prose instead of Pi's `Unexpected token 'o', "not-json" is not valid JSON`. The complete error now remains compared. | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:755-771`; fix-drift-rpc |
| `rpc/15-rpc-compaction-result.toml` | W04: Pig emits the compact response before `compaction_end`; Pi emits the event first. The first structural error is therefore field presence on different record kinds, not a missing `aborted` field inside Pig's eventual event. | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363,538-541`; fix-drift-rpc |
| `rpc/17-rpc-abort-retry.toml` | W04/W18: Pig responds to `abort_retry` before its terminal events and omits the retry recovery `entry_appended`; Pi emits the entry and retry-end first. | `packages/coding-agent/src/core/agent-session.ts:1015-1028,3404`, `packages/coding-agent/src/modes/rpc/rpc-mode.ts:554-556`; fix-drift-rpc / fix-drift-session |
| `rpc/20-rpc-runtime-boundary-values.toml` | H1, related to W04: immediate Pig steer/follow-up responses overtake the next queue event; Pi's awaited continuations respond later. Direct bash completion also lands at a different position. Input-batch boundaries and complete ordering need owner review, not record sorting. | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:417-424,810-811`; fix-drift-rpc |
| `rpc/26-rpc-session-stats-context-estimate.toml` | H5: after the approved D2/D22 names and destinations are narrowly aliased, the first bash update differs: Pig emits an empty text block; Pi emits an empty content array. Final counters match in the retained pair. | `packages/coding-agent/src/core/tools/bash.ts:301-302`; fix-drift-tools / fix-drift-rpc |
| `rpc/33-rpc-real-provider-records.toml` | W09: final assistant messages and saved entries lack Pi's `responseId` and `rawStopReason`. The first mismatch also shows a mutable Pi `message_start` snapshot versus Pig's earlier empty-content snapshot. That timing-sensitive start difference remains visible but is not promoted to a separate confirmed bug without an acknowledgement-controlled stream probe. | `packages/ai/src/api/openai-completions.ts:558-576`, `packages/coding-agent/src/modes/json-event.ts:49-60`; fix-drift-ai |
| `json/04-json-real-provider-error.toml` | W10: failed assistant `api` is empty rather than `openai-completions`; the full error assistant and event stream remain compared. | `packages/ai/src/api/openai-completions.ts:310,705-720`, `packages/coding-agent/src/modes/print-mode.ts:107-129`; fix-drift-ai |
| `print/04-print-real-provider-error.toml` | W10: Pig stderr wraps the whole HTTP error as `openai: HTTP 400: {"error":...}`; Pi emits `400: {"message":...,"type":...}`. Both exit 1. | `packages/coding-agent/src/modes/print-mode.ts:137-146`; fix-drift-ai |
| `extensions-runtime/29-export-tool-renderers.toml` | H2, related to W19: the complete system message exposes extension tool order. Pi retains `render_card, render_self, render_throw, render_fail`; Pig's captured order starts `render_throw, render_fail`. The former dump-prefix crop discarded this array. | `packages/coding-agent/src/core/extensions/loader.ts:280,527`, `packages/coding-agent/src/core/agent-session.ts:1252-1263,3152-3155`; fix-drift-ext |
| `providers-registry/04-provider-wire-payloads.toml` | H3: complete request bodies expose previously summarized fields. Anthropic max tokens are 16507 versus 123; Google adds thinking/tool configuration and omits `systemInstruction.role`; Mistral omits `prefix:false` and the prompt-cache key. These direct-provider fixtures also use different thinking-option forms, so the owner must separate fixture input mismatch from production drift. Pi's Bedrock probe captures no HTTP body despite its former canned path/auth summary; the new null-body guard fails. | `packages/ai/src/api/anthropic-messages.ts:1072,1175`, `google-generative-ai.ts:393-411`, `mistral-conversations.ts:380,843`, `bedrock-converse-stream.ts:166-235`; fix-drift-ai owns input alignment and missing Bedrock capture |
| `export-html/01-export-cli-plain.toml` | H4: full HTML differs; Pig lacks current skill-entry styling and uses older tool-output/ANSI whitespace rules. The status line still matches after only its isolated root is aliased. | `packages/coding-agent/src/core/export-html/index.ts:144-174`; coordinator assigns export owner |
| `export-html/02-export-cli-ansi.toml` | H4: complete HTML asset differences are now visible instead of asserting only the export status. | Same source and owner |
| `export-html/03-export-cli-tools.toml` | H4: complete HTML differs outside the formerly extracted embedded session payload. | Same source and owner |
| `export-html/04-export-cli-full-mix.toml` | H4: complete HTML differs outside the formerly extracted embedded session payload. | Same source and owner |

The strict read/write faux RPC scenario, JSON faux event scenario, held-open SIGTERM scenario, UI identity scenarios, and narrowed startup/selector identity comparisons pass. W01, F1/F2, F3, and W25 are not newly reproduced on this integrated base. Their prior fixes are not changed here.

## Regression proof

The first compiling test run failed `TestOutputEqualRejectsUnjustifiedReplacement`, `TestRPCDriverPreservesFraming`, `TestRPCWaitDoesNotAcceptOldOutput`, and `TestPaneWaitRequiresEveryPatternToBeNew` on the original harness. All pass after the source fixes.

The JSON comparator mutation returns equality for all nonempty parsed streams. It compiles and fails missing/extra/null/type/precision/content/order assertions, `TestJSONIdentityAliasesPreserveReferencesAndPresence`, and `TestJSONPathAliasesRetainSuffixAndText`. The mutation is removed. A separate red test caught a literal `<temp>` colliding with the initial string-based path alias; typed segments fix that defect.

`TestRPCSignalTeardownKeepsStdinOpen` distinguishes signal shutdown from EOF and checks captured stderr. `TestRPCEventBarrierRequiresOneNewCompleteCorrelatedRecord` rejects stale IDs, split fields in separate events, partial lines, and trailing malformed data. `TestProviderFixtureDrivesActualToolResultsAndHTTPFailure` checks actual tool-content echo and deterministic HTTP failure. The race-enabled JSON/RPC/barrier regressions pass.

## Commands and gates

Run from the worktree with a fresh binary:

```bash
export PATH="$HOME/flakes3-tools:$PATH"
go build -o bin/pig ./cmd/pig
export PIG_PARITY_PIG_BIN="$PWD/bin/pig"
export PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi"
export PI_PACKAGE_ROOT="$PWD/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
unset PIG_CODING_AGENT_DIR PIG_SESSION PIG_MODEL PI_CODING_AGENT_DIR
go test -tags parity ./test/parity/runner -run '^TestParity$' -count=1 -timeout 10m -args -pig-parity.dir="$PWD/test/parity/scenarios/rpc"
# For one handoff, select its globally unique scenario name:
go test -tags parity ./test/parity/runner -run '^TestParity/33-rpc-real-provider-records$' -count=1
make normalization-inventory
make lint-scenarios
make coverage RESULTS=
```

| Gate | Result |
|---|---|
| Harness unit tests with `-tags parity`, plus JSON/RPC/barrier `-race` tests | Pass |
| `go test ./test/parity/cmd/lint`, inventory Python tests, `make lint-scenarios` | Pass |
| `go vet ./...`; Windows vet for runner, linter, provider fixture, and pigletbuild | Pass |
| `go tool golangci-lint config verify`; touched-package tagged lint; `make lint` | Pass, no suppressions added |
| `make lint-changed` | Blocked: this lane checkout has no merge base with `main`; full lint passes instead |
| `go test ./test/parity/... ./coding/pigletbuild ./test/parity/testdata/provider-wire-pig` | Pigletbuild and touched packages pass. Three existing closure dashboard tests fail because they assume every active divergence is approved; base D78 is pending. No expected count or approval is changed to hide it. |
| `make ci-contracts ci-drift` | Contracts stop at existing pending/partial hot-path upstream tests in `test-porting-release` |
| Separate `make ci-drift` | Inventory, scenario lint, port-map drift, coverage drift, and divergence consistency pass; divergence quality stops at existing unapproved D78 |
| Separate divergence guard / source hygiene / docs commands | Divergence guard, changed-source hygiene (`-diff-base HEAD`), and `make docs-drift` pass. The default source-hygiene prerequisite stops at existing operator-path findings in `test/parity/unit-evidence/fix-ext-ui-state.md` and `fix-node-scrollview.md`. |
| Tagged `go fix -diff` on touched packages | Reports pre-existing modernizations in tagged runner helpers; it reports no new comparator implementation change. Untouched helpers are not swept. |
| Paired scenarios | Intentionally red as listed above; no production fix or skip added |

`make coverage` initially consumed the Makefile's shared historical results path and reported unrelated prior passes. The committed reports are regenerated with `RESULTS=` so they contain only the current static denominator and make no last-run claims. Current red/green execution evidence lives in this report and its manifest. Binding historical coverage results to the exact source/scenario revisions remains a coordinator-owned evidence limitation.

A representative comparator benchmark uses 1000 complete records with ID references and timestamps. The observed sample is 55.9 ms/op, 62.3 MB allocated/op, and 226863 allocations/op. CPU and allocation profiles are retained with the logs. This is a baseline, not a performance claim. The comparator retains raw streams, parsed records, and identity maps for a pair, then releases them; it performs no production TUI work. It is not proof of bounded-memory streaming for arbitrarily large sessions.

## Ledger disposition

D67 and D69 now live in `docs/additive-features.md` as required builder substrate, with additive source markers and the existing Windows/Unix tests intact. D30 no longer claims that the harness cannot drive stale context; the audit's module-scope probe is reachable through RPC. D51 is limited to steady-state interactive SIGINT. Pi restores on interactive SIGTERM and exits 0 (`packages/coding-agent/src/modes/interactive/interactive-mode.ts:4145-4157,4231-4249`), and dead-terminal exit is 129 (`:4179-4185`). D51 does not cover W23 print/JSON SIGINT or W24 PTY disconnect. D68's separate scope decision remains with its owner. No new divergence is approved.
