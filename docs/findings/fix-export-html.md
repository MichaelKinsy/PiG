# HTML export parity repair

## Pin and scope

This lane merges strict-comparison input `818a61134` in commit `298379e86`. The oracle is the real Pi 0.87.1 executable under `extensions/sdk-ts/node_modules/.bin/pi`. The toolchain is Go 1.27.1 and Node 24.19.0 on Linux amd64. The six export scenarios are the family denominator. All six pass three pairs after the fix. Scenarios 01 through 04 compare complete exported HTML with the strict harness, without artifact normalization.

Production changes stay in `internal/codingagent/export/`. This is required upstream-parity behavior, not an additive feature or a new divergence. REUSE annotations preserve vendor attribution outside the unmodified assets.

## Root causes and upstream rules

All Pi source references below are relative to `.upstream/current/`.

| Cause | Source fix | Pi rule |
|---|---|---|
| Embedded assets predate the pin. They lack skill entries, current escaping and navigation, and preserve the wrong whitespace. The highlighted preview joins source lines without newlines. Marked is stale, and injected SPDX text changes the Highlight.js artifact. | Copy the pinned CSS, application JavaScript, Marked and Highlight.js unchanged. Retain their licenses in `REUSE.toml`. The unchanged HTML template also matches the pin. | `packages/coding-agent/src/core/export-html/index.ts:144-177` reads and injects all five assets; `template.css:512-564,794-831` scopes whitespace and skill styling; `template.js:616-630,865-872,926-934,1171-1245,1398-1401,1469-1478,1586-1613,1812-1817` implements the corresponding rendering, escaping and navigation. |
| `json.Marshal` HTML-escapes the base64 payload and escapes Unicode line separators. | Reuse the existing JSON.stringify line serializer, remove its framing newline, and base64-encode its output. | `packages/coding-agent/src/core/export-html/index.ts:160-161`. |
| An empty Go entry slice serializes as `null`, which the exported script cannot iterate. | Emit an empty array. | `packages/coding-agent/src/core/export-html/index.ts:298-304` takes the SessionManager entries array; `template.js:37-39` iterates it. |
| `jsReplace` replaces every match. | Replace only the first match while preserving JavaScript dollar-substitution semantics; use that same helper for CSS substitutions. | `packages/coding-agent/src/core/export-html/index.ts:164-177` uses non-global String.replace. |
| Go's HTML escaper chooses different quote entities. | Use Pi's five entity spellings in the shared ANSI converter. | `packages/coding-agent/src/core/export-html/ansi-to-html.ts:65-72`. |
| The ANSI converter clamps grayscale indices that Pi does not clamp. | Preserve Pi's grayscale calculation, including index 257. | `packages/coding-agent/src/core/export-html/ansi-to-html.ts:56-60`. |
| Blank-line trimming strips all ANSI commands and uses Go's whitespace set. | Strip SGR only; use ECMAScript whitespace, which includes BOM but excludes NEL. Preserve OSC-only lines as content. | `packages/coding-agent/src/core/export-html/tool-renderer.ts:44-55,142-165`. |

No asynchronous browser code is rewritten. The pinned application's clipboard, deferred navigation and event handlers remain unchanged. The Go export remains blocking and owns no new background work.

## Regression proof

The original compiling implementation fails:

- `TestExportHTML_WhitespaceCSSRulePresent` after tightening it to the exact upstream positive and negative CSS patterns.
- `TestExportHTMLSkillBlock`, `TestExportHTMLMarkdownSanitization`, and `TestExportHTMLPinnedAssets`.
- `TestExportHTMLRendering`, which executes the exported scripts and real vendors through Node's VM, stops before DOM event wiring, and checks skill-only/prompt/image/ordinary messages, sibling blocks, Markdown, safe URLs, attribute escaping, and multiline previews. This is rendering-function evidence, not browser-layout evidence.
- `TestExportHTMLPayloadPreservesText` and `TestJsReplaceOnlyFirstOccurrence`.
- `TestAnsiToHTMLPiEscapingAndPalette` and `TestCustomToolResultHTMLBlankLineSemantics`.
- Strict parity scenarios 01, 02, 03, 04 and the new 06 skill-session scenario.

`TestAnsiLinesToHTMLNoSourceWhitespace` and `TestCustomToolResultHTMLTrimsSpacing` port the remaining upstream whitespace cases. They already pass before the fix; the Go newline join and basic TUI trimming were not the stale-CSS defect. All four skill cases, all three whitespace cases and all nine XSS cases are now mapped as ported with current upstream test hashes.

A separate compiling mutation restores HTML escaping of `<` in the payload while retaining every corrected asset. Scenario 06 fails complete artifact equality. The mutation is removed. This proves its payload assertion is not satisfied merely by importing current assets. The same Node rendering probe also passes on a real Pi-generated skills export.

Coverage claims are narrowed rather than inflated. CLI scenario 02 does not invoke the TUI ANSI converter or the ANSI/HTML utility files. Scenario 05 has no custom tools and cannot prove custom-tool rendering. Its coverage now names `index.ts`, and its durability increases from one pair to three. Unit evidence owns the ANSI converter and custom result trimming. The corrected denominator therefore removes two previously claimed utility-file verifications; no PORT_MAP entry is newly promoted.

## Repeatable commands

```bash
export PATH="/absolute/toolchain/bin:$PATH"
go build -o bin/pig ./cmd/pig
export PIG_PARITY_PIG_BIN="$PWD/bin/pig"
export PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi"
export PI_PACKAGE_ROOT="$PWD/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
go test ./internal/codingagent/... ./coding/rpcclient
go test -race ./internal/codingagent/export
go test -tags parity ./test/parity/runner -v -run '^TestParity$' -count=1 -timeout 10m -args -pig-parity.dir="$PWD/test/parity/scenarios/export-html"
go test -tags parity ./test/parity/runner -v -run '^TestParity/(12-rpc-export-html|29-export-tool-renderers)$' -count=1
```

Raw failure artifacts, mutation logs, final HTML, gate logs and profiles remain under `/tmp/fix-export-html-evidence`. The final Pi and PiG files compare equal with `cmp`. Their SHA-256 hashes are:

| Fixture | Complete HTML SHA-256 |
|---|---|
| plain | `0bb57c895cebeb6d0af6a8a7414d7b3146b6737419bfd4939b446e85f29dabda` |
| ansi | `8da4d54ba4cf9ce79af297687e192cdb6a3e2a2e4960dba08fc78551854ab7de` |
| tools | `52d1ceda8aa6a66c94eaa445da798d0a1f7122d325a9d659c91011efa487ad42` |
| full-mix | `ffe3da4ac436edf50427568bc4497be56fd1c23b504a8fe64a6e60d3f4c097a9` |
| skills | `41353dbd11475927d0ee46be5261e35015a00a8e0237c84c95842a3490429cbc` |

## Gates and external blockers

| Gate | Result |
|---|---|
| Export family, including strict 01 through 04 | Pass; all six scenarios complete three pairs. |
| RPC 12 export command | Pass; three pairs. |
| Extension runtime 29 | Still red before export comparison: Pi emits `toolcall_end` for index 0 before the next `toolcall_start`; PiG starts index 1 instead. The first strict mismatch is `/10/assistantMessageEvent/contentIndex`. This is the provider/event-stream lane's surface, not an HTML fix. Pi's event forwarding is `packages/coding-agent/src/modes/json-event.ts:49-60`; the fixture emits toolcall-end events in `test/parity/testdata/test-faux-provider.ts`. The scenario remains enabled and unchanged. |
| `go test ./internal/codingagent/... ./coding/rpcclient` and export race tests | Pass. |
| `go vet ./...` and Windows vet for `./internal/codingagent/export/...` | Pass. |
| `go tool golangci-lint config verify`, touched-package lint and `make lint` | Pass; no suppressions added. |
| `make lint-changed` | Cannot find a merge base with `main` in this lane checkout. Full lint passes instead. |
| `make ci-contracts ci-drift` | Go inventory drift is regenerated with `go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json`. Contracts then stop at existing pending/partial hot-path tests in `test-porting-release`. The three export files are no longer pending/partial. |
| Separate `make ci-drift` | Scenario lint, port-map drift, coverage drift and divergence consistency pass. Divergence quality stops at existing unapproved D78. |
| Separate divergence guard, changed-source hygiene and docs drift | Pass. |
| `go fix -diff ./internal/codingagent/export/...` | Empty. |
| `go test ./test/parity/...` | Three existing closure tests fail because D78 is open rather than approved/provisional. Other parity packages pass. No count or approval is changed to hide this. |
| `go test ./...` | Does not complete within the command's 600-second wall-clock budget; no full-suite pass is claimed. Completed packages, including `cmd/pig`, pass before termination. |

Coverage is regenerated with `make coverage RESULTS=` so unrelated historical run results do not become claims for this revision. Normalization inventory is regenerated with `make normalization-inventory`.

## Resource disposition

`BenchmarkToHTML` exports 1000 skill entries. The observed before sample is 3.42 ms/op, 4.22 MB/op and 298 allocations/op. The corrected sample is 3.71 ms/op, 4.62 MB/op and 303 allocations/op. These are noisy shared-host measurements, not a performance claim. CPU and allocation profiles are retained as `export.cpu.pprof` and `export.mem.pprof`. Template substitution accounts for the largest application allocation share. Export retains the complete input and output in memory as Pi does, performs file I/O synchronously, and releases its local buffers after return. The change adds no goroutines, listeners or persistent handles. It does not claim bounded-memory streaming or fix unrelated export scheduling.
