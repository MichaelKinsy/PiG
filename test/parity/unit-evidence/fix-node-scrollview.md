<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Node ScrollView and gentle-pi footer repair

## Reference and scope

Pi 0.87.1 is the oracle. Source citations below resolve under `.upstream/v0.87.1/`. The executable is the locked `extensions/sdk-ts/node_modules/.bin/pi`. The starting PiG revision is `fdcf667708`.

Read `docs/design/pi-extension-runner.md` from `f50f294b62` without merging it. The repair keeps its real-module approach: Pi supplies extension-local components and caller-created screens; Go retains the live terminal and cached-frame transport. No new wire operation, runner, global configuration, or divergence is added. The measured package path uses regular terminal mode. This report does not claim that gentle-pi's private fullscreen `layoutRoot` mutation is transported to the Go host.

## Source defects and regressions

| Defect | Source rule | Repair and guard |
|---|---|---|
| `ScrollView` throws D73 during gentle-pi's footer factory. | `packages/tui/src/components/scroll-view.ts:41-236` constructs a local Container with one child, retained scrolling/follow state, and ordinary component rendering. | Re-export Pi's pinned TUI index. `TestPiScrollViewMatchesThePinnedPackage` first fails with the original throwing constructor. It compares empty, fitting, overflowing, fractional/non-finite, 100,000-line geometry, follow suppression, scrollbar, inherited mouse, and single-child contracts against Pi. |
| Other pi-tui runtime exports are throwing stand-ins; erased TypeScript types become fake classes. | `packages/tui/src/index.ts:1-159` distinguishes runtime exports from type-only exports. `components/image.ts`, `terminal-image.ts`, `terminal.ts`, `tui-main-screen.ts`, `tui-alt-screen.ts`, and `native-platform.ts` supply the implementations. | Bundle the complete TUI JavaScript tree and native assets. `TestPiTuiPublicExportsAreRealImplementations` first fails at `resetCapabilitiesCache`; it compares actual calls and the exact exported namespace against the locked package. Vendoring and embedding tests verify every copied file. |
| Every state update from an extension without a footer/header clears another extension's slot. | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2427-2495,2554-2555` changes these slots through explicit setters. | `renderSpecialSurface` returns without publishing a setter when it has no factory. An explicit undefined setter still clears. `TestNodeSurfaceUpdatesDoNotClearUnownedSlots` first fails on four unsolicited clearing calls. The paired scenario adds a later non-owning extension and fails before the repair. |
| Replacing/clearing a local footer/header drops its component without disposing its timers/subscriptions. | `interactive-mode.ts:2432-2435,2460-2463` disposes the old custom component. | The setters dispose the previous local component. A compiling mutation removing those calls fails the same unit guard with `0 !== 1`. |
| A repository created during session_start appears in PiG's footer but not Pi's. | `packages/coding-agent/src/core/footer-data-provider.ts:120-123,239-249,308-309` binds Git paths before startup callbacks and does not discover a new repository later. | `StatusLine.SetCwd` captures the repository binding; the watcher uses that binding instead of rediscovering paths after startup. `TestFooterDoesNotWatchRepositoryCreatedAfterBinding` is red first. The paired scenario is red with `branch=late` versus Pi's `branch=none`. |
| Node's footer provider reports undefined rather than null outside a repository. | `footer-data-provider.ts:127-132,239-241` returns null. | Return null and assert it in the Node surface guard; the original value is red-proven. |
| The SDK surface probe falsely reports the restored `TuiAltScreen` viewport brand as missing. | `packages/tui/src/tui-alt-screen.ts:199` declares an instance symbol field, not a prototype method. | Resolve the actual `VIEWPORT_TUI` symbol and recognize computed instance field declarations without constructing arbitrary classes. `TestProbeFindsVendoredPackageAndSymbolMembers` is red then green. No exception or suppression is added. |

The canonical scenario is `extensions-runtime/53-node-scrollview-footer`. It compares the complete escaped crop from the final command notification through the editor and two-row custom footer. It runs three pairs and keeps `escaped_output_equal = true`. A six-second observation interval exposes PiG's existing five-second Git poll; it is not a timeout increase to conceal a failure. A subsequent explicit status update publishes branch state before the final comparison.

The first branch-only probe passed before the branch fix because it never refreshed the subprocess snapshot after the Git poll. That was insufficient evidence. Adding the explicit status update makes the pre-fix binary fail. No comparator or crop is weakened.

`TestNodeVendoredTuiUpstreamTests` executes the full upstream `layout.test.ts`, `terminal-image.test.ts`, `terminal.test.ts`, `native-module-path.test.ts`, and `native-platform.test.ts` bodies against the vendored implementation. Only source-module and asset locations change. Upstream platform/clipboard opt-ins remain unchanged; Linux does not qualify the Windows/macOS runtime cases. Their existing Go-port dispositions and hashes remain in the mapping, with the new Node evidence appended, not used to promote incomplete Go tests.

Two liveness tests previously consumed the erroneous unsolicited empty-surface calls. Removing that Node-only allowance makes Node follow the same message sequence as the other SDKs. The initial full Go run detects the conformance wait on those obsolete calls; the corrected conformance suite passes without changing its deadline. A later unqualified touched-package command omitted the fixture-prebuild exports and exhausted the unchanged ten-minute subprocess budget. The retained `touched-without-fixtures.log` records that setup mistake. The qualified rerun with `test-fixtures.sh --exports` passes all touched packages; no timeout is increased.

## Real npm package evidence

Copy the existing 46-package corpus installation into fresh private Pi and PiG homes. Do not resolve newer npm dependencies or modify the original install. The copied package is `gentle-pi@3.7.0`:

- Lock SHA-256: `3a27b51cfb03a7cd3e44d29a75b9a781c523ef8a6c6bd2bb2c2cdb13dbda0d6a`.
- npm integrity: `sha512-SXBp9jIRnVIcOsLCW/Zw4XDTXxhViXNxrCZS7LyioB6kdRl99Ohojeaj+yIH5+K/ucwLlTlHYBz8zwue9aspNQ==`.
- Load all 13 package extension entries through ordinary package configuration, not only gentle-shell's factory.
- Run Pi first and PiG second against the same local scripted OpenAI-compatible provider.
- Use the original 170×55 terminal, `hello` print prompt, startup observation, `/gentle-live-research-probe`, Escape, and `hello` interactive turn.
- Keep the original command and observation budgets. Use a lane-specific tmux server and clean up each session.

The pre-fix run reproduces `footer render failed: ScrollView is not available ... D73`. The first component-only repair reveals the unsolicited-clear defect. The next run reveals the late `master` branch. After all three source repairs, both print prompts exit 0 and the escaped footer rows match exactly at startup, after the recorded command, and after the model turn. No path, ANSI, whitespace, branch, or usage normalization is applied to the footer. Whole-pane plain-text differences are limited to the pre-existing Pi/PiG startup identity text. Raw captures retain those differences.

The regular-mode footer ends as:

```text
✿ gentle shell ⟡ ~/w ⟡ e2e-model ⟡ ctx ▱▱▱▱▱▱▱▱ 0% ⟡ $0.000
```

`tmp/fix-node-scrollview/corpus.py` is the integration replay script with only package selection, output/tool paths, and the tmux server name changed. `server.mjs` retains the integration's scripted provider. The `corpus-red`, `corpus-green`, `corpus-final`, `corpus-verified`, and `corpus-complete` directories preserve the investigation and final replay. Package identities and artifact hashes are recorded in [0.2.1-scrollview-results.json](../../../docs/release/0.2.1-scrollview-results.json).

## Verification

Toolchain: Go 1.27.1, Node 24.19.0, npm 11.17.0, tmux 3.7b. npm differs from the qualification version; no npm install or toolchain replacement is performed. `rg` and `fd` come from the existing corpus tool directory. Unset inherited `PIG_CODING_AGENT_DIR` and `PIG_SDK_GO_ROOT`; use the maintained `automation/ci/test-fixtures.sh --exports` for Go integration tests.

Passed:

- Focused red/green tests and the canonical scenario's three escaped pairs.
- `go test` for subprocess, interactive coding-agent, SDK-surface, and extension-conformance packages.
- The complete `extensions-runtime`, `footer`, `interactive-rendering`, and `fullscreen` parity families at declared durability. Live-auth footer cases are not qualified by the hermetic family run.
- Targeted race tests for Node components/surface ownership and footer Git binding.
- `go vet ./...` and Windows vet on the touched packages.
- `go tool golangci-lint config verify`, touched-package lint, `make lint-changed LINT_BASE=fdcf667708`, and `make lint`.
- `go fix -diff ./...` produces no changes.
- Generated Go interface, SDK surface, coverage, scenario lint, and drift gates after regeneration.

Inherited release blockers remain unchanged:

- `go test ./...` fails only `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit` in `test/parity/closure`.
- `make ci-contracts ci-drift`, followed by `make -k ci-contracts ci-drift`, remains red for 358 open hot-path upstream-test obligations and unapproved D77/D78. All other constituent targets pass after regeneration. Do not approve those records or promote the test dispositions to make this lane green.

## Performance and lifetime

Retain `startup-bench.log`, Go CPU/allocation profiles, and Node CPU profiles under `tmp/fix-node-scrollview/`. The production host's `BenchmarkNodeAdmissionStartup/one-node` runs three samples of three iterations: 589–612 ms/op, 121–147 KB/op, and 527–546 allocs/op. The Go profile mainly attributes time to system calls and artifact materialization. It excludes Node heap accounting.

A separate local Pi/Node component probe alternates widths 80/170 for 100,000 renders and scroll updates with 100,000-line geometry. It records 130 ms under Pi's module and 161 ms under the re-exported module in single profiled runs. Ending process RSS is about 69.2 MiB and 64.6 MiB respectively. These are instrumentation samples, not a speedup claim, a latency budget, or a complete host benchmark. The implementations are the same pinned bytes; the original throwing stand-in is not a valid performance baseline.

ScrollView timers use Pi's unchanged unref/clear behavior. Explicit footer/header replacement disposes the component. The new Git binding starts no watcher when the repository was absent at initialization. No new IPC, history scan, terminal write, or background task is added to the input/render loop. Corpus tmux sessions are gone after cleanup. Native desktop clipboard operation and gentle-pi's private fullscreen layout integration remain outside the measured regular-footer path.
