# fix-992-hygiene: mechanical findings D, E, F, I, J from the 0.4.0 aggregate

Base: `staging/porter/pi-0.99.1` @ cf873f0ab (upstream pin 0.99.2). Scope: tests, evidence ledgers and one dead function. No production behavior changed, so there is no new red test to port; each fix below names the failing symptom measured on this base.

## Status of each finding on this base

| finding | state on the base | action |
|---|---|---|
| D mapping rows | 8 rows `pending` although their Go tests pass | marked `ported` with evidence (commit `docs(parity): mark the stale 0.99.2 test-mapping rows ported with evidence`) |
| E generated files | only `test/parity/coverage.md` was stale (the 0.99.2 test-porting line: 529/1/111 became 537/1/103 with D); the SDK-surface matrix and the AGENTS.md coverage block are already current; `known-gaps` cannot run until C is closed | regenerated in the separate last commit `chore(port-99): regenerate generated files` (`make coverage RESULTS=`); see "E: SDK surface" |
| F private paths | `gate-992-coding.md:114` is already clean on the base; `port-992-durable.md` is not on the base (its lane is unmerged) | nothing to change here; see "Left for the merge" |
| I test data race | already fixed on the base by `78dddf3dd` (`causeMu` guards `cause`) | verified: removing the mutex gives `WARNING: DATA RACE` at `go test -race -count=10`; with it `-race -count=24 GOMAXPROCS=4` is clean |
| J `TestGoVersionPolicy` | red: `docs/plan/evidence/port-99-f8-engine-spike/go.mod declares "go 1.27.1"; want Go 1.26 language floor` | module declares `go 1.26`; `go vet` of the spike with stub embeds passes under the 1.27.1 toolchain |
| J `TestWindowsTestsCreateSymlinksThroughSymlink` | red: `coding/extension/host/subprocess/socket_runtime_dir_test.go` calls `os.Symlink` | uses `testenv.Symlink`; `GOOS=windows go vet` clean |
| J two lint findings | `tui/theme_loader.go:267 resolveTheme` unused; `coding/mcpext/tools_render_test.go:62` SA1019 | `resolveTheme` deleted (no caller anywhere); this makes the generated `test/parity/interfaces/pig-go.json` stale (its `tui#resolveTheme` entry), so `make interface-go-drift` fails until the integrator runs `make generate`; the test uses `tui.TerminalColorModeTrueColor`; `golangci-lint` on `./tui/... ./coding/mcpext/...`: 0 issues |

## D: rows marked ported

Each row cites tests that pass on this base, and the case count equals the inventory count.

| upstream file | cases | Go evidence |
|---|---:|---|
| ai/test/anthropic-eager-tool-input-compat.test.ts | 3 | `TestAnthropicUpstreamEagerToolInputCompat` |
| ai/test/anthropic-federation-sdk.test.ts | 2 | `TestAnthropicFederationExchangesOnceAcrossRequests`, `TestAnthropicFederationSkippedForHeaderOwnedAuth` |
| ai/test/anthropic-federation.test.ts | 9 | `TestAnthropicWorkloadIdentityFederation` |
| ai/test/anthropic-strict-tool-schema.test.ts | 2 | `TestAnthropicStrictToolSchemas` |
| ai/test/overflow.test.ts | 20 | `TestOverflowUpstream`, `TestOverflowLengthStopsUpstream` (port-992-core's own list; same cause as the named rows) |
| coding-agent/test/default-tools-setting.test.ts | 8 | `TestDefaultToolsInitialSelectionPort`, `TestDefaultToolsReloadPort` |
| coding-agent/test/model-runtime-modify-models-compat.test.ts | 6 | `TestModelRuntimeNativeCompatibilityUpstream` |
| coding-agent/test/virtual-models.test.ts (the stale one of the two rows; `suite/virtual-models.test.ts` was already ported) | 17 | 15 tests in `coding/virtual_models_upstream_test.go` (3 getBranchSelection cases are subtests of one) |

The evidence audit found one mis-port: the `+name` case of `default-tools-setting.test.ts:82` registered its tool without `defaultActive: false` (a note said the Go field did not exist yet). The field exists (`coding/extension/tool.go:152`), so the test now sets it, as `.upstream/v0.99.2/packages/coding-agent/test/default-tools-setting.test.ts:82-98` does. Mutation: changing `+inactive_tool` to `inactive_tool` fails the case (`[inactive_tool]` instead of the four-tool list).

`packages/coding-agent/test/tool-renderer-examples.test.ts` stays `pending` (2 cases: `keeps the system prompt unchanged for the $name example` (`it.each`) and `keeps minimal mode's edit tool in the default shell`). It loads the 0.99.2 examples `built-in-tool-renderer.ts` and `minimal-mode.ts`, which PiG carries only as vendored copies until the 0.99.2 re-vendor, and it needs a Go edit-tool shell renderer oracle. It needs the re-vendor lane first.

## E: SDK surface (a real gap, not staleness)

`go run ./test/parity/cmd/sdksurface -check` reports 27 unexcepted missing cells while `docs/extension-sdk-surface.md` is byte-identical to a fresh run (it already shows them as `missing`), so regeneration changes nothing; `make sdk-surface-drift` stays red until the cells are implemented or an owner reviews exceptions in `test/parity/sdk-surface-exceptions.toml`. The probe needs the Node dependencies (`python3 automation/ci/npm-locked.py extensions/sdk-ts`); without them the Node column is empty.

| key | missing in |
|---|---|
| `ctx.modelRegistry.classify`, `findOfType`, `getAvailableOfType`, `getModelOfType`, `getModelsOfType`, `registerVirtualModel`, `unregisterVirtualModel` | Node runtime (Go, Rust and Python implement them) |
| `ctx.ui.theme.appearance`, `ctx.ui.theme.colors` | Go, Node runtime, Python, Rust |
| `ctx.ui.theme.style` | Go, Python, Rust |
| `provider.classifiers`, `provider.images` | Go, Node runtime, Python, Rust |
| `tool execute result.structuredContent` | Node runtime |

Per the AGENTS.md SDK rule, a stub is not an acceptable resting state, so these are feature work (Node runtime parity with the other three SDKs, theme object, provider classifiers/images), not hygiene. They belong to a lane that owns the extension wire; none is fixed here.

## C: what blocks `make test-porting-release` (owner approval needed; no approval written)

On this base the Bedrock case is already ported (not designed out). `go run ./test/parity/cmd/testinventorycheck -release-policy test/parity/interfaces/test-porting-policy-v0.99.2.json` now stops at the first of four designed-out cases that exist in the mapping but not in the reviewed policy (`validateDesignedOutApproval`, which needs the same list in both files and `SCRUTINIZED:approved` in the policy rationale):

1. `packages/ai/test/images-models.test.ts`: `Models with image models › rejects chat models at the image entry point at runtime` (lead decision 2026-09-30, a Go type-system mechanic: `GenerateImages` takes `*ImageModel`).
2. `packages/codemode/test/sandbox.test.ts`: `limits and lifetime › accepts a worker path string` and `limits and lifetime › reports a missing worker file as a sandbox error` (Bun worker-entry cases with no Go counterpart).
3. `packages/mcp/test/streamable-http.test.ts`: `StreamableHttpTransport › calls fetch without a receiver` (no receiver binding in Go).

The owner must approve these in `test-porting-policy-v0.99.2.json` (list the same ids in `designedOutCases` and add the approval mark to each rationale). After that the gate still reports 103 hot-path `pending` rows, all owned by lanes not on this base (durable 38, chord 17, ai 31, tui 13, mcp 4, codemode 2, `tool-renderer-examples`, and others), so it stays red until those lanes merge or the owner defers them.

## Environment notes

- `tui` oracle tests (`TestColorDetectionMatchesPi` and four more) fail here with `ENOENT .../extensions/sdk-ts/node_modules`: the Node dependencies are not installed in this worktree. Not caused by this change; none of them touches `resolveTheme`.
- No Xvfb on this host (group H of the aggregate).

## Left for the merge

- `docs/plan/progress/port-992-durable.md:5` (on the port-992-durable lane branch) names a private scratch path for its question record. When that branch merges, replace the sentence `The record of what was measured stays in <path>.` with `The record of what was measured stays in the lane's question record.` and re-run `make source-hygiene`.
