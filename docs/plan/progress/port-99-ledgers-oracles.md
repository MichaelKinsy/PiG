# port-99-ledgers-oracles: progress and report

Lane: items 2 and 3 of `docs/plan/upgrade-0.99.1.md` (ledgers and oracles). Branch `port-99-ledgers-oracles`, base `staging/porter/pin-move` (`fd2017de9`). Upstream: upstream 0.99.1 (`.upstream/v0.99.1`).

Every oracle below was produced by running the real upstream 0.99.1 package (`extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent`) under Node 24.19.0 with `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR` isolated under `/tmp` and `PI_SKIP_VERSION_CHECK=1` (LESSONS rule 17).

## What changed

### Extractor and inventory tooling
- `test/parity/interface-extractor/behavior-input-inventory.mjs`: a local renderer id that collides with another id is qualified by its nearest named enclosing function. Upstream `mcp/ui.ts:117` (`McpManagerView.menu.render`) and `:219` (`McpManagerView.render`) collided. Only colliding ids change, so the 0.87.1 output is byte-identical to the committed file. The new test is red on the old code.
- `TRACKED_PACKAGES`, `OPTIONAL_PACKAGES`, the package-key mapping and `resolvePublishedPackages` gain `codemode` (`mcp` was already tracked), per owner decision D-B. The inventory test covers both packages and is red without the change.
- `test/parity/cmd/interfaceinventory`: `expectedPackages` accepts `codemode` and `mcp` as optional. A test was red first.
- **Scope expansion (a).** upstream 0.99.1 `.d.ts.map` files carry no `sourcesContent` (1344 maps, 0 with content; the `.js.map` files do). `verifyPublishedSources` now falls back to the sibling `.js.map` when its `sources` and `sourceRoot` equal the declaration map's. A test covers the fallback and its negative cases. Without this the 0.99.1 inventory cannot be generated.
- **Scope expansion (b).** `interfaceinventory -generate-pending -previous-mapping` and `testinventorycheck -generate-pending -previous-mapping` (`carryMapping`) carry a reviewed row forward when its interface or test file is unchanged, so a version leap does not discard 15k reviewed rows. Review correction (rev-port-99-ledgers-oracles): `interfaceinventory` carries only deferred and designed-out rows; a ported, partial or divergence row is regenerated as pending, because a declaration shape hash does not see behavior (an alias such as `RpcResponse` hashes only its name). Tests cover both; the interfaceinventory one was mutation-checked.

### PORT_MAP
- 44 rows added for the files new in 0.99.1 and 5 removed. `port-map-drift`: clean (661 files).
- Statuses are honest. ✅ only for `utils/oauth-page.ts` (identical relocation of `auth/oauth/oauth-page.ts`). 🟡 for files that have Go code (`coding/mcpext/*`, `core/mcp-servers.ts`, `virtual-models.ts`, `model-operations.ts`, `models-error.ts`, `legacy-tracker.ts`, tui `colors`/`oklab`/`wheel-scroll`). n/a for the `.lazy.ts` dynamic-import wrappers and `quickjs-wasm.d.ts`. ⬜ for the system-one/typesafe/llama-cpp classifier APIs, `callback-server`, `openai-chatgpt`, `nested-tool-calls`, `codemode/*`, `tool-search/*`, `mcp/cli.ts`, `mcp/ui.ts`, pi-logo, themed-text and system-theme.
- The package-scope section states the scope of `packages/mcp` and `packages/codemode` (outside the four-package file denominator, present in the interface and test ledgers). `TestPublicPackageLayout` requires it.

### 0.99.1 ledgers (reviewed content, not generated)
- `mapping-v0.99.1.json`: 15275 rows (20 ported, 6 partial, 7766 deferred, 7483 pending including every new or changed interface). Deferred rows are carried from 0.87.1 where the interface is unchanged. The review re-promoted 26 carried ported and partial rows after checking their upstream implementation and reopened 11 whose 0.99.1 behavior changed: `RpcResponse` and `runRpcMode` (prompt, steer and follow_up `data.disposition`), `AgentSession.compact` (summarization auth resolved after preparation), and `AgentSession.setSessionName`, `ExtensionAPI.setSessionName` and `ExtensionAPI.appendEntry` (session-manager `_persist` writes the file at the first user message).
- `test-mapping-v0.99.1.json`: 670 files (437 ported, 189 pending, 42 designed-out, 1 divergence, 1 partial). `test-porting-policy-v0.99.1.json` is anchored on the mapping commit `0ab6ad89`, baselinePorted 437. New files take the area and tags of their siblings; mcp and codemode tests are `extensions`/`hot-path`.
- `behavior-input-mapping-v0.99.1.json` (59 handlers, 131 renderers, all pending) plus `behavior-owner-overrides-v0.99.1.json`. `families.toml` gains codemode, mcp and tool-search under extensions-runtime, `llama-cpp-classify`, `themed-text`, and the moved `oauth-page` path. `behavior-contracts.toml` moves to 0.99.1; two pending contracts were re-hashed (`tui/wrap/cjk-token-breaks`, `tui/terminal/herdr-image-capability`) and the other 29 ranges are unchanged.
- `async-contracts.toml` moves to 0.99.1: 19 new async sources recorded as `deferred` with PORT_MAP status and no claimed evidence; `packages/ai/src/models.ts` reopened from `translated` to `deferred` because 0.99.1 adds async members (`generateImages`, `classify`, `getAvailableOfType`, `getAllAvailable`, `getAuthenticatedProviders`); 3 rows removed for files that are gone or no longer async (`images-models.ts`, `theme.ts`, `rpc-types.ts`). One evidence entry on `agent.ts` was dropped: scenario 35 does not list `agent.ts` in `covers` (removed by `db10ac87a`), so the pointer was already invalid at the base. `make async-contracts`: clean (307 files).
- `format-versions.toml`: the six MCP protocol-version and implementation-version fields are classified `external` (MCP specification revision, `packages/mcp/src/protocol/types.ts`). `format-version-inventory`: clean.
- `known-gaps.toml`: `pi_version` 0.99.1.

### Oracles regenerated from real upstream 0.99.1
- `faux/pi.json`, `test-faux/pi.json` (3 runs each), `cmd/pig/testdata/d82-w7/rpc-usage-pi.json` and `rpc-usage-extension-pi.json`. The only difference is `message_start` outputTokens 1→0, which matches the Go backpressure tests.
- `credential-expiry` (cites `resolve.ts:119-155`, `models.ts:575,608-625`), `tool-validation` (byte-identical, `validation.ts` unchanged; cites `agent-loop.ts:726-732,767`), `pi-start.json` (`check.mjs --oracle`, 10 runs; the file now records `piVersion`), `ai/testdata/stream-cases.json` (Kimi-K3 replaces K2.6), `zen-models.json`.
- `openai-error-pi.mjs` reads OpenAI 7.19.0 from `pi-ai/node_modules` (upstream 0.99.1 depends on OpenAI 7, the extractor previously read 6.40.0).
- About 88 paired probes and their READMEs: version guard bumped to 0.99.1 and `.upstream/v0.87.1` mirror paths repointed to `v0.99.1`. Probes re-run and verified byte-identical to their committed goldens (only clocks differ): `bedrock-eventstream`, the pi-messages and bedrock oracles.
- Go guard literals bumped where the test asserts the installed Pi package version: `cmd/pig/{args_name_upstream,session_not_found,rpc_shutdown,session_path_argument}_test.go`, `internal/codingagent/pi_shared_files_test.go`, `ai/faux_observation_oracle_test.go`, `ai/test_faux_observation_oracle_test.go`, credential-expiry.

### Generated files (last commit)
`interfaces/{upstream,cli,recommendations,behavior-inputs,upstream-tests}-v0.99.1.json`, `pig-go.json`, `delta-v0.84.0-v0.99.1.json`, `upstream-sync/v0.99.1.toml`, `cmd/pig/help_upstream.txt` (now shows `pig mcp <command>`, `builtin:<name>`), `test/parity/coverage.md`, the AGENTS.md coverage block and the badge.

## Discrepancies for the porter
1. `coding.UpstreamReviewedVersion` is still 0.84.0, so the Makefile consumes `delta-v0.84.0-v0.99.1.json`. The plan says 0.87.1→0.99.1. I generated the Makefile-consumed file, and generated `upstream-sync/v0.99.1.toml` from 0.87.1 (216 files, all pending). Decide the reviewed version and regenerate, or move the constant.
2. The carried mapping, test-mapping and behavior rows come from the 0.87.1 files as they exist on `pin-move`. Later edits to those files by other lanes are not included.
3. `internal/wordsegmenter/testdata/sea-icu78.json` was **not** regenerated. It needs Node 26.7.0 and an ICU 78.3 source, which this machine lacks. Pi's word-segmenter inputs (`word-navigation.ts` and the word functions in `utils.ts`) are byte-identical between 0.87.1 and 0.99.1 for the exercised paths, and the `sea_test` guard still expects 0.87.1.
4. `.upstream/v0.87.1` path literals remain in many ported Go tests (`coding/*_upstream_test.go`, extension examples, skill fixtures, x11). Repointing each needs its family to re-verify the changed fixtures, so I left them.
5. The vendored Pi dist (`runtime-node/shims/pi-dist`, its manifest and harness `package.json`) is still 0.87.1 (`TestVendoredPiDistMatchesThePinnedPackage`, `TestVendoredPiTuiContainsEveryRuntimeModuleAndNativeAsset`, `TestNodeRuntimeShimsExportEveryPinnedPiValue`, `TestHarnessEntryIsNamedLikePinnedPi`). Not an oracle; needs its own regeneration.
6. `.upstream/current/.pig-upstream-source.json` is missing in my local mirror (`TestUpstreamMirror_MatchesPin`). Environment only.
7. `TestInteractiveSignalsRetainHandlersThroughDisposalAndDrain` still compares `pig --version` to `0.87.1` (a reference-binary pin); left alone.

## Blocked, or red by design
- `make custom-factory-ledger` (and its package tests): the generator has no reviewed semantic row for the new members `Theme.{style,colors,appearance}`, `ThemeStyle*`, `ThemeToken`, `ThemeBg`, `ThemeAppearance`, `TUI.queryTerminalColors`, `TerminalColors*`, `TerminalColorMode`, `Color`. Adding rows needs the extension/theme lane's disposition per member (`queryTerminalBackgroundColor` and `queryTerminalColorScheme` also left the 0.99.1 API).
- `make known-gaps` and `test-porting-release`: red because hot-path tui, ai and coding-agent test files are pending (for example `packages/tui/test/wheel-scroll.test.ts`). The 189 pending rows include the chord and durable files, whose D-H per-file designed-out rationale is not written.
- `make upstream-delta`: pending rows by design (216 files).
- `known-gaps.toml` now reports real gaps with the pin bumped: `McpServersChangeEvent` and `ProviderStreamEvent` missing in `test/upstream-parity/registry.go`; `Runner.{BindToolActions,CreateToolContext,ReportUnhandledMcpServers}` unclassified; `app.clipboard.pasteImage` description and darwin behavior differ. Correspondence reports two findings, `fullscreen-wheel-scroll-lines` and `settings-selector` table order.
- `TestColorDetectionMatchesPi`: the probe crashes because 0.99.1 removed `detectTerminalBackgroundFromEnv` and `parseOsc11BackgroundColor` (replaced by `detectColorFgBgTheme`, `detectTerminalTheme`, `parseOscColorResponse`). Rewriting the probe needs the tui lane's Go API for the new detectors. Left red.

## Product gaps the oracles expose (not fixed here)
These fail against oracles that I verified by re-running the real Pi probes.
- Start state and usage: `TestRPCBedrockConverseStreamObservation` (Go is one buffered row short), `TestRPCPiMessagesMatchesPi` (Go `message_start` carries final usage 10/3/13, Pi shows 0), and `check.mjs` against `pi-start.json` (anthropic-messages, pi-messages, bedrock-converse-stream 0/10 equal).
- RPC shutdown: upstream 0.99.1 adds `data.disposition:"handled"` to the extension-command prompt response and changes the ordering with `session_shutdown`. About ten `cmd/pig` RPC shutdown tests (`TestRPCInputEnd…`, `TestRPCExtensionShutdownRequestExits`, …) were hidden by the old version guard and now report it.
- Theme: dark/light palettes changed in 0.99.1 (`dark.json` now has `appearance` and a new palette), so `TestExtensionDialogsMatchPiBindingsAndPalette`, `TestExtensionDialogCountdownMatchesPi` and `TestLoginDialogMaskDisabledMatchesPi` differ by colors only. `TestInteractiveThemeSelectionPresenceMatchesPi` shows the new `system` theme fallback ("Fell back to the system theme") and `project null` handling.
- Session: `TestW3SessionPiComparison` (clone-before-save message: "Send a message before cloning or forking it").
- Catalog lane: `TestCatalogPin0871`, `TestCodegenByteIdentical`, `TestOpenCodeModelsSmokeUpstream`, `TestAPIKeyProvidersMatchPinnedProviderDefinitions` (typesafe), `TestDefaultModelPerProviderMatchesPinnedUpstream`, `TestStreamUpstream`/zen after the catalog moves.
- Machine-dependent: `Xvfb` is not installed (x11 clipboard tests). Python and Rust SDK tests need the real `mise`/`cargo` homes; I pointed `MISE_DATA_DIR`, `CARGO_HOME`, `RUSTUP_HOME` at them after my first full run was polluted by the isolated `HOME`, so the first full-run log (`gotest-b`) overstates the failures.

## Gates run
Clean: `port-map-drift`, `interface-inventory`, `interface-inventory-drift`, `interface-go-drift`, `interface-recommendations-drift`, `interface-mapping-quality`, `test-inventory-drift`, `test-inventory`, `behavior-input-inventory-drift`, `behavior-contracts` (31 contracts, all pending), `async-contracts`, `format-version-inventory`, `help-text`, `knowledge-graph`, `coverage`, `gofmt`, `go vet` and `GOOS=windows go vet` for `test/parity/cmd/...`, `go fix -diff ./test/parity/...`. Extractor `npm test`: all pass.
Red for the reasons above: `custom-factory-ledger`, `known-gaps`, `test-porting-release`, `upstream-delta`, and the correspondence and closure packages (two unlisted correspondence findings).

## Loop self-report

Family: ledgers-oracles

Bugs found and fixed at the source (count: 4):
  - duplicate renderer id → qualify colliding ids by enclosing function @ test/parity/interface-extractor/behavior-input-inventory.mjs
  - declaration maps without sourcesContent rejected → fall back to the sibling script map @ test/parity/interface-extractor/ (verifyPublishedSources)
  - inventory rejected the new optional packages → accept codemode and mcp @ test/parity/cmd/interfaceinventory
  - `openai-error-pi.mjs` read OpenAI 6 from the harness instead of the OpenAI 7 that `pi-ai` uses → read from `pi-ai/node_modules`

Comparators tightened (count: 0):
  - none; no assertion was loosened. One invalid evidence pointer was dropped from `async-contracts.toml` (agent.ts, scenario 35).

Divergences numbered in docs/parity/DIVERGENCES.md (count: 0)

Lint suppressions added or changed (count: 0)

New file coverage (count: 1 PORT_MAP entry newly ✅):
  - packages/ai/src/utils/oauth-page.ts (the file moved unchanged)

Band-aids consciously chosen (count: 0)

Scope expansion (count: 3):
  - `.js.map` provenance fallback, carry-forward generation in `interfaceinventory` and `testinventorycheck`, and `interfaceinventory` optional packages: none of these could regenerate 0.99.1 without them; each has a test.

Cross-family scenarios re-probed/tightened (count: 0)

Smells investigated (count: 3):
  - the oracle regenerations with no content change (`tool-validation`, `bedrock-eventstream`) → confirmed byte-identical by re-running the real probes; upstream sources are unchanged.
  - a full run with 148 failures → about half were isolated-HOME toolchain noise; the rest are the product gaps above.
  - `async-contracts.toml` was not clean at the base (agent.ts evidence) → fixed as described.

Smells deferred to user (count: 3):
  - `custom-factory-ledger` semantics for the new Theme/TUI members
  - the reviewed version (0.84.0 vs 0.87.1) for `upstream-delta`
  - `sea-icu78.json` needs Node 26.7.0 and ICU 78.3
