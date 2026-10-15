# 0.5.0 integration log

Each entry names the merged work, what was applied to `PORT_MAP.md`, and the gates run.

## Base

- Started from the Pi 1.0.4 port with the pi-env port and its review fixes (D96, D97).
- Merged main at the 0.4.1 pin-back (#159) without conflicts.
- Version: `internal/coding/pigversion` reports `PigVersion = 0.4.1` after the merge. Main keeps the last released version as the version of unreleased content (0.3.1 while 0.4.0 content was unreleased, 0.4.0 while 0.4.1 content was unreleased), so builds report `0.4.1+1.0.4`. `make set-version VERSION=0.4.1` changes nothing. The release target became 0.5.0 (owner decision, 2026-10-08), and lane rel-050 ran the equivalent of `make set-version VERSION=0.5.0 SET_VERSION_ARGS=-move-unreleased` on this branch, so builds from it report `0.5.0+1.1.0` before the release and `CHANGELOG.md` heads its content with `## [0.5.0] - 2026-10-09`. The release step on `release/v0.5.0` runs `make set-version VERSION=0.5.0 SET_VERSION_ARGS=--release-modules`; set-version keeps the date of an existing target heading, so correct the date by hand when the release cuts on another day, and fold any `changelog.d` fragment under `[0.5.0]`, not `[Unreleased]`.
- Gates after the merge: `go build ./...`, `ci-contracts`, `coverage-drift`, `docs-drift`, `port-map-drift`, `source-hygiene`, `divergence-consistency` and the public-claims check pass.

## gap-mcp-codemode-libs (with the review fixes)

- Merged without conflicts.
- `PORT_MAP.md`: new sections `packages/mcp/src/` (18 rows) and `packages/codemode/src/` (10 rows) with the statuses of `gap-mcp-codemode-libs.md` after review: 17 ported, 5 partial (`client.ts`, `protocol/content.ts`, `oauth/types.ts`, `oauth/discovery.ts`, `declarations.ts`), 6 designed out. The package-scope text now lists six packages, and `automation/ci/check-port-map-drift.py` tracks `packages/mcp/src` and `packages/codemode/src` so a new upstream file in either package needs a row.
- Coverage: the figures are in `test/parity/coverage.md`, which `make coverage` regenerates; the log does not repeat them.
- The closure document's two percentage cells for the two packages are fractions now, because the public-claims check accepts only the PORT_MAP figures.
- Open residuals R1 to R4 and findings 5 to 8 of the review stay open for the owner: fix or approve numbered divergences.
- Gates: `go vet` native, Windows and darwin on `mcp`, `codemode`, `internal/nodeurl`; lint-changed 0 issues; `go test` on those packages, `coding/mcpext` and the codemode builtin; divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass. No normalization-inventory change.

## rev-port-104 (review fixes for the Pi 1.0.4 port)

- Merged without conflicts. The branch changes no `PORT_MAP.md` row: it fixes the symbolic-link watch on kqueue platforms, the empty `powershell` programs error, the order of `hiddenTools`, and the CLI-boundary `application_type` test, and it regenerates `pig-go.json`.
- `make generate` and `make coverage` reproduce the committed files, so nothing else changed. The normalization inventory has no drift.
- Gates: `go vet` native, Windows amd64 and darwin on the touched trees; lint-changed 0 issues; `go test -race` on `durable/...`, `coding`, `coding/mcpext` and `internal/codingagent/...`; `ci-contracts`, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency and docs-drift pass.
- One run of `TestLoginProviderCancellationKeepsOldCredential` (`internal/codingagent`) timed out at 20 s while other packages were running. It passed in isolation (5 runs) and in a second full-package run. It belongs to the login dialog tests, which this branch does not touch.
- The merge commit of the Pi 1.0.4 port with pi-env has no sign-off: the public PR needs signed commits (review item 5).

## gap-chord (with the review fixes)

- Merged with one conflict, in the `.PHONY` line and `check-core` of the `Makefile`: kept both the `build-env-daemons` target from pi-env and the new `node-facets-chord` and `node-facets-chord-check` targets.
- `PORT_MAP.md`: new section `packages/chord/src/` with 29 rows from `gap-chord.md` (25 ported, 1 partial, 3 designed out). `delta/tracker.ts` stays partial (array roots) until the gap-chord follow-up lands. The package-scope text lists seven packages, the chord bullet no longer says the package is outside the denominator, and `automation/ci/check-port-map-drift.py` tracks `packages/chord/src`.
- Gates: `go vet` native, Windows and darwin on the chord and experimental trees; lint-changed 0 issues; `go test` on `chord/...`, `internal/chord/...` and `internal/experimental/...`; `make node-facets-chord-check`; `ci-contracts`, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-exp-services (with the review fixes)

- Merged without conflicts.
- `PORT_MAP.md`: the 13 rows of `packages/coding-agent/src/cli/experimental/` and `packages/coding-agent/src/experimental/services/` named by `gap-exp-services.md` are ported now, with the "runtime qualification pending" and "compile-only qualification" notes removed and the D64 text kept on the CLI rows. The `server.ts` row names `internal/experimental/services/server.go`. `server.go` and `plugins.go` carry the `// Ports` markers the lane asked for.
- Gates: see the end of this entry.
- Gates: `go vet` native, Windows and darwin on `internal/experimental/...`; lint-changed 0 issues; `go test -race` on `internal/experimental/...` (after `make parity-deps`, which the nine Node-dependent tests need); `ci-contracts`, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.
- Housekeeping: the source-hygiene check rejected a branch name in the closure document, so it now says "Review commits:". `automation/ci/version-records.toml` records the two earlier releases that document names.

## main at the npm publishing change (#160)

- Merged without conflicts. No divergence ID clashed (D96 and D97 are ours; main used none).
- `make generate` reproduces the committed files.
- Gates: `go build ./...`, `go vet` native and Windows on `cmd`, `coding/piglet` and `internal/npmpublish`; `go test` on the npm publishing, Piglet, Piglet build and source packages; `ci-contracts`, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift pass.

## gap-ai (with the review fixes)

- Merged with one conflict, in `internal/nodeurl/httpurl.go`. Two lanes had added a pathname and an origin to the HTTP URL parser. Kept the library lane's `HTTPHref` (href, origin field, userinfo, query, fragment) and the gap-ai `HTTPURL.Pathname` and `HTTPURL.Origin()` method; `HTTPHref` no longer has its own `Pathname` field, so `ParseHTTPURL` returns the pathname it computes. The `nodeurl`, `mcp`, `mcp/oauth` and `ai` tests pass on the result.
- `PORT_MAP.md`: 19 of the 20 `packages/ai` rows are ported, with stale "not started" and "not closed" notes rewritten (typesafe, Cloudflare system-one, Anthropic federation and fallback). `scripts/generate-models.ts` stays partial: the review rejected the proposed divergence, because Go can port the transform.
- Also applied from the gap-untested note: the `utils/ansi.ts` row names `internal/codingagent/tools/sanitize.go` (`StripANSI`).

## gap-mcp (with the review fixes)

- Merged with one conflict, in the `.PHONY` line of the `Makefile`: kept `build-env-daemons`, the chord facet targets and `test-mcp-conformance`.
- `PORT_MAP.md`: the 11 `extensions/mcp` and `extensions/tool-search` rows are ported. The two new paired scenarios (`slash-commands/14`, `15`) pass against Pi, and the normalization inventory and `pig-go.json` are regenerated.
- The network job that would run `make test-mcp-conformance` in CI is not added.
- `automation/ci/version-records.toml` records that `gap-mcp.md` names the earlier release that added the `mcp list` override.
- Gates for both: `go vet` native, Windows and darwin on `mcp`, `coding` and `test`; lint-changed 0 issues; `lint-scenarios`; `ci-contracts`; `go test -race` on `coding/mcpext`, the tool search, `mcp`, `ai`, `cmd/check-model-data` and `internal/nodeurl`; the slash-commands parity family; divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims.

## gap-experimental (with the review fixes)

- Merged without conflicts.
- `PORT_MAP.md`: the 15 rows of `packages/coding-agent/src/experimental/` (excluding `services/`) are ported, and the stale "pending" notes are gone. `coordinator.ts`, `coordinator-entry.ts`, `process.ts`, `radius-auth.ts` and `radius-relay.ts` were ⬜ rows; their Go files (`coordinator.go`, `coordinator_entry.go`, `process.go`, `radius_auth.go`, `radius_relay.go`) now carry `// Ports` markers. The `plugin.ts` row stays designed out. The lane notes that `node-facets/plugin.mjs` implements it; that is the owner's call.
- The test-mapping rationale for `experimental-remote-runtime.test.ts` says 26 cases, as the review asked.
- Open, low priority (from the review): a coordinator `isEmpty` mutant survives, and the Session profile digest does not normalize invalid UTF-8 as the package digest does.
- Gates: `go vet` native, Windows and darwin on `internal/experimental` and `cmd`; lint-changed 0 issues; `go test -race` on `internal/experimental/...` and `cmd/pig-experimental`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-chord follow-up (rev-gap-chord-2) and gap-remote-stack (with the review fixes)

- Both merged without conflicts. The chord follow-up supersedes the first chord merge: `delta/tracker.ts` is ported now (array roots), and the chord bullet no longer names an open case.
- `PORT_MAP.md`: new sections `packages/protocol/src/` (8 rows), `packages/client/src/` (8 rows) and `packages/server/src/` (16 rows): 26 ported and 6 designed out (barrels and the Promise helper). The package-scope text lists ten packages, `packages/session-backends` is noted as absent from the 1.0 releases, and `automation/ci/check-port-map-drift.py` tracks the three new roots.
- `go fix` reformatted one chord test after the chord merge.
- `automation/ci/version-records.toml` records that `gap-remote-stack.md` names the last release that contained the session-backends package.
- The open finding about worker plugin reload ordering is closed by a later review commit that adds a regression test.
- Gates: `go vet` native, Windows and darwin on the experimental, durable and chord trees; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/experimental/...` (including the Node wire differential), `durable/session`, `chord/...`, `internal/chord/...` and `internal/lineadmission`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-codemode-utils (with the review fixes)

- Merged without conflicts. `pig-go.json` merged cleanly and the generators report it current.
- `PORT_MAP.md`: seven of the nine rows are ported (tools-manager, version-check, open-browser, and the codemode execute, index, renderer and tool rows). `utils/shell.ts` and `utils/clipboard-image.ts` stay partial because native Windows and desktop clipboard execution is unproven. The review rejected the proposed 0600 clipboard divergence (Go matches Pi with the umask) and folded `formatVersionCheckError` into D39 instead of a new number; the `version-check.test.ts` test-mapping rationale says so.
- Open from the review (not applied here): Node reads TMPDIR, TMP, then TEMP for the temp directory, while Go reads TMPDIR only; the owning interactive and TUI slices decide.
- Gates: `go vet` native, Windows and darwin on `internal/codingagent`, `coding` and `cmd`; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/codingagent/...`, `internal/shellconfig`, `coding/extension/builtin/...` and `cmd/pig/...`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-interactive (second review)

- Merged without conflicts; it supersedes the first interactive review. `pig-go.json` was regenerated for the new credential synchronization method.
- `PORT_MAP.md`: twelve of the thirteen `modes/interactive` rows are ported, including `interactive-mode.ts`. `components/markdown-transform.ts` stays partial: abandoned callback bodies after an extension process disconnects belong to the extension host lane. The `utils/ansi.ts` row already names `internal/codingagent/tools/sanitize.go` (`StripANSI`). The stale show-images and Radius login notes are rewritten.
- The review fixed a data race in credential synchronization (it rebuilt shared provider maps while the off-loop Anthropic check read them) and a full-refresh failure caused by an unrelated provider's unreadable credential. The messages match Pi byte for byte in tmux.
- Gates: `go vet` native, Windows and darwin on `internal/codingagent`, `cmd`, `coding` and `tui`; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/codingagent/...`, `cmd/pig/...`, `tui/...` and `coding`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-remote-stack (second review) and gap-core (with the review fixes)

- The second remote-stack review merged cleanly. It adds one regression test for worker plugin reload ordering and supersedes the first.
- The core lane merged cleanly, including its later `model_change` fix (`getBranchSelection` stops at every `model_change`, as Pi does).
- `PORT_MAP.md`: `nested-tool-calls`, `mcp-servers`, `remote-catalog-provider`, `crash-log`, `virtual-models` and `index.ts` are ported. `prompt-templates`, `skills` and `utils/frontmatter.ts` are partial: a mapping with non-string keys now keeps its frontmatter, but YAML 1.2 scalars, top-level non-mappings and the error wording still differ from Pi's yaml engine, which the review judged portable in Go. `session-manager`, `agent-session`, `sdk` and `agent-session-runtime` stay partial (open in the lane).
- D100 (additive, in `docs/additive-features.md`, `DIVERGENCE-IDS.txt` and a source marker in `crash_log.go`) records that crash attribution also reads Go goroutine traces; a JavaScript stack matches exactly as in Pi, so the lane's proposed divergence is withdrawn. The record is marked "owner confirmation pending" like D96 and D97.
- Open from the review (not applied): a parent cycle in a session file hangs Pig exactly as it hangs Pi, so it is an upstream report, not a divergence.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/codingagent/...`, `internal/pioracle`, `coding`, `coding/extension`, `cmd/gen-models` and `cmd/pig/...`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-durable (second review)

- Merged with one conflict, in the `.PHONY` line of the `Makefile`: kept every earlier target and added `durable-interop-deps`. One semantic conflict after the merge: the remote-stack change gave `durable/session` `enqueue` an admission argument, so the lane's close-listener test passes `nil` for it. The merged code keeps both the line tickets and the admission signal.
- `packages/durable` is outside the PORT_MAP denominator, so no row changes. The three strict Pi/Go interop tests run in the default test run now: `make test` installs their locked Node packages through `durable-interop-deps`, and they pass with `-race` here (the reviewer ran them 40 times each).
- Open before the release: the order in which handlers commit when several tasks become ready together. Go starts each handler on its own thread; Pi runs on one thread. The review counts the commits as equal now, but the handler order still differs (the interop compares `main.jsonl` unordered). It needs an owner decision and a numbered record; none is added here.
- Open from the review (low): the reconcile ticket has no guard test. A flaky `internal/chord` `TestPublicDelivery` case under `-race` is the chord owner's.
- Gates: `go vet` native, Windows and darwin on `durable`; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `durable/...`, `ai`, `internal/experimental/...` and `internal/lineadmission`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## main (Windows flake fixes, dependency refresh)

- Merged with conflicts in the three divergence ledgers only: `DIVERGENCES.md`, `DIVERGENCE-IDS.txt` and the `divergences.md` docs mirror. Kept D96 and D97 from this branch and D99 from main. D98 stays reserved for the eval credential file until its lane merges. The crash attribution record moved to D100, because main reserved D98 and numbered D99. The active-divergence count is 40, and the next free ID is D101.
- Gates: build, `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/codingagent`, `internal/fsretry` and `test/upstream-parity`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-untested (with the review fixes)

- Merged with one conflict, in `internal/codingagent/tools/powershell.go`: the codemode-utils lane resolves PowerShell through the shared Pi-style path search, and this lane added a lookup seam for its tests. Kept the seam and made production pass the shared search (`findExecutableOnPath` in the shape of `exec.LookPath`). `go fix` also reformatted one PowerShell test.
- `PORT_MAP.md`: every row of the lane was already ported; `utils/ansi.ts` names `internal/codingagent/tools/sanitize.go` (`StripANSI`). The regenerated coverage and `pig-go.json` are committed.
- Open, owner decision (from the review): Windows `where` searches the current directory while `exec.LookPath` does not, and Go's `(?i)` Unicode folding differs from JavaScript's `/i`.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `ai/...`, `coding`, `internal/outputfiles`, `internal/codingagent/tools/...`, `internal/childwait`, `internal/crossspawn`, `tui/...` and `internal/experimental/services/...`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-telemetry-evals (second review)

- Merged with conflicts in the divergence ledgers (`DIVERGENCES.md`, `DIVERGENCE-IDS.txt`, the `divergences.md` docs mirror and `additive-features.md`) and no code conflicts. D98 (the eval harness OAuth credential file) lands in its reserved place. The lane numbered its additive `get_extensions` RPC command D99, which main already uses, so it is D101 here (ledgers, the `rpc_mode.go` marker, the RPC command docs and the closure ledger). D100 stays the crash attribution record. The active-divergence count is 41, and the next free ID is D102.
- The eval harness checks the loaded extensions once, when the first pig process starts, as Pi does, and the image documentation checks match Pi. `pig-go.json` was regenerated.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `cmd/pig/...`, `cmd/pig-eval-extension`, `cmd/pig-evals`, `coding/rpcclient`, `internal/evals/...`, `telemetry/...` and `internal/codingagent`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-core (second review)

- Merged with conflicts in `coding/session.go`, `coding/virtual_models.go` and its oracle test. Two fixes of the same `model_change` bug met: the first review's fix and this lane's. Took this lane's version whole: `GetBranchSelection` stops at every `model_change`, `ModelSelection.Unresolvable` marks a non-string member, and the startup resume path restores through it. The merged tree builds and the virtual-model tests pass. `go fix` reformatted one file of the new `internal/yaml12` package.
- The lane ports the eemeli/yaml 2.9.0 engine to `internal/yaml12`, and the frontmatter parser reads through it, so the proposed YAML divergence is withdrawn: none of the five proposals is numbered.
- `PORT_MAP.md`: `core/sdk.ts`, `core/agent-session-runtime.ts` and `core/session-manager.ts` are ported. `core/agent-session.ts` stays partial: two cases of `system-prompt-updates.test.ts` (forced-prompt `before_agent_start`, JSON round trip) are not matched one for one and wait for a reviewer. `core/prompt-templates.ts`, `core/skills.ts` and `utils/frontmatter.ts` stay partial until known tags (`!!binary`, `!!timestamp`, `!!set`, `!!omap`, `!!pairs`, `!!merge`) and collection keys with non-plain members behave as in Pi. Both change whether a skill or prompt loads.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/yaml12`, `internal/codingagent/...`, `coding` and `cmd/pig/...`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-untested (second review)

- Merged without conflicts. `lazyregexp.NewJSIgnoreCase` now folds as JavaScript's `/i` does (ASCII-only fold from non-ASCII, no panic on a one-letter class next to a literal, JavaScript line terminators for `.`). The provider retry patterns and the Codex retry patterns use it. `pig-go.json` and the coverage report were regenerated.
- No PORT_MAP changes: the rows were already ported.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `internal/lazyregexp`, `ai/...`, `internal/crossspawn` and `internal/codingagent`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-agent-tui

- Merged without conflicts. Open from the review: the lane's agent-loop, TUI API and provider-fallback proposals (Go can match each), the unexported `compositeTuiLine`, and native Windows and macOS qualification of the terminal rows.
- `PORT_MAP.md`: `tui/stdin-buffer.ts`, `components/editor.ts`, `colors.ts`, `oklab.ts`, `wheel-scroll.ts` and `native-module-path.ts` are ported. `agent/proxy.ts` stays partial: a content index past the end of the partial message needs a numbered divergence. `agent-loop.ts`, `stream-fn.ts`, `tui.ts`, `terminal.ts`, `native-platform.ts` and `index.ts` stay partial.

## gap-core-tools-ext (second review)

- One conflict, in the imports of `coding/mcpext/runtime.go` (kept `internal/jsstring`, dropped the now unused `internal/nodepath`).
- Merged with a known red gate: `TestEverySDKCanReachEveryWireCapability` fails because the Rust and Python SDKs do not yet call `ui.editor.base`. Also open: each editor component needs its own base `tui.Editor`, and the `docs/extension-api-parity.md` row and async contract are not written. The `setEditorComponent` row stays partial.

## gap-core (third review)

- Merged without conflicts. Known tags and collection keys as mapping keys now behave as in Pi, so `core/prompt-templates.ts`, `core/skills.ts` and `utils/frontmatter.ts` are ported. One open item stays on those rows: a date used as a mapping key prints a zone abbreviation where Node prints the long zone name (a few zones), which needs a table generated from Node or a numbered divergence. The Node extension runtime vendors yaml's browser build, which the runtime owner should review.

## main (test agent-directory isolation)

- Merged without conflicts. `make test` now fails when a run touches the agent directory.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `agent/...`, `tui/...`, `internal/yaml12`, `coding/mcpext`, `coding/extension` and `internal/codingagent/...`; `ci-contracts`, lint-scenarios, divergence-guard, source-hygiene, port-map-drift, coverage-drift, divergence-consistency, docs-drift and public-claims pass.

## gap-agent-tui (second review)

- Merged without conflicts on top of the first agent-tui merge; it replaces that lane's tests and fixes a data race in `StreamProxy` and race failures in the `internal/codingagent` render tests.
- `PORT_MAP.md`: `agent/stream-fn.ts` is ported (`NewAgent` returns an error where Pi's constructor throws). `agent/proxy.ts` stays partial with two proposed divergences awaiting numbers: a content index past the end of the partial message, and a message after the terminal event. `tui/tui.ts` stays partial: the driver owns input dispatch because remote extension listeners are asynchronous, and the listener snapshot differs from Pi's live iteration. `agent-loop.ts` (standalone entry points) stays partial.
- Open from the review: Windows lint findings in `internal/codingagent/tools/bash_group_windows.go`, outside this lane.
- Gates: `go vet` native, Windows and darwin; lint-changed 0 issues; `go fix -diff` empty; `go test -race` on `agent/...`, `tui/...`, `internal/codingagent/...` and `coding`; the drift gates and public-claims pass.

## gap-core (rounds 4 to 6) and gap-agent-tui (third review)

- Merged without conflicts. The YAML engine now names a date used as a mapping key from the ICU long zone name timeline (generated from Node's ICU, which no longer depends on the host's zone data) and reads the host zone on Windows from the registry through CLDR `windowsZones`.
- `core/prompt-templates.ts`, `core/skills.ts` and `utils/frontmatter.ts` stay partial: two Windows items need a native probe (ICU picks the Windows mapping by the user's region first, and Node honours `TZ` on Windows). `internal/yaml12/zonelocal_windows_test.go` runs on the native Windows CI job and checks the registry detection against the host offset.
- `agent/agent-loop.ts` stays partial: `AgentLoopConfig` and `Agent` have no `getApiKey`, and failure reporting through `Result()` against Pi's unhandled rejection is an open divergence proposal.

## Whole-monorepo accounting

- One list of upstream packages, `test/parity/upstreampackages/packages.json`, now feeds `check-port-map-drift.py`, `upstreamdelta`, `asynccheck`, `portreconcile` and the interface extractor and inventory validator. A test requires it to equal the package directories of the pinned mirror, and the Python gate checks it again, so a new upstream package fails `make port-map-drift` until it is listed and mapped. A unit test covers a dropped package and an unmapped file in a fixture mirror.
- `PORT_MAP.md` maps every source file of all fourteen packages (721 files). `durable`, `env`, `evals` and `telemetry` are new sections, seeded from the per-file audit and the gap lanes' verdicts (all four packages' rows are ported). The Package scope text now says every package is mapped. `test/parity/families.toml` assigns the new rows (new `remote-stack` family; `env` to tools, `mcp` to extensions-runtime, `evals` and `telemetry` to cli-utils).
- `make coverage` reports a per-package table, the four core packages, the shipped surface (the dependency closure of the coding-agent manifest, less the source paths its npm `files` list excludes, both read from the mirror) and the whole monorepo, plus the interface closure from the mapping ledger. The generated report holds the current figures. The behavioural evidence of the ten non-core packages is thin: a large part of their ported rows have no scenario or unit-evidence record.
- The interface inventory covers all fourteen packages: 12234 IDs, 2284 of them new. Packages not installed beside the published Pi package (`client`, `durable`, `env`, `protocol`, `server`) are read from the pinned source, and each inventory package records its origin. `evals` is private and has no public interface. The mapping and delta ledgers carry the new IDs as pending. Interface closure is 2 of 12234 IDs (the two designed-out rows); a file-level port does not close an ID.
- `test/parity/upstream-sync/v1.0.4.toml` gains the thirteen changed `durable` and `env` files with test evidence. `test/parity/async-contracts.toml` gains 85 `deferred` entries for the added packages; each rationale names the lane that owns the contract, and none is marked reviewed.
- Test and CI fixes found by `make check`: fixture mirrors in the PORT_MAP drift tests, the coverage recipe test, the Windows-only test package selection (`!unix` files), the `node-facets-chord-check` shard, the eval harness (it now clears `PIG_HOME` and `PI_HOME`) and a test main for the eval suites.

## gap-agent-tui (fourth review), gap-core (round 7) and the preview CI fixes

- Merged with one conflict: `internal/yaml12/zonelocal_windows_test.go` existed on both sides; the lane's Windows-native tests replace the integration's. The Windows zone items stay CI-pending, so the three YAML rows stay partial until the native Windows jobs pass. `GetAPIKey` can now fail and fail the run, as Pi's `getApiKey` does; the failure log of `AgentLoop` is still a divergence proposal for the owner.
- Preview CI fixes: `make generate` after the final PORT_MAP edit (the coverage ledger was stale), `interface-recommendations-generate`, the `node-facets-chord-check` gate in a Linux shard, the drift test fixtures and the Package scope lines, and the closure counts. The closure counts were a merge slip plus a missing marker: D99's approval marker had slid below D98 when the ledgers were merged, and D96 never carried one. Both records now end with their marker (D96's and D98's mark the lead's review, not the owner's sign-off; the owner can still amend them). D98 now sits before D99 as the order check requires.
- `divergence-quality` flagged two additive records: D97 gained a locking test (`TestPackagedDaemonLocation`) and D100 gained its locking test and an approval line (owner sign-off pending).

## gap-core (round 8), gap-core-tools-ext (third review), gap-agent-tui (fifth review)

- Merged; the only conflict was the closure ledger `gap-core.md`, where both sides appended (kept both). The YAML key comparison now follows JavaScript equality and no longer panics on binary keys. A delegated editor base is a fresh editor per factory, and `setEditorComponent` no longer hangs in RPC mode in the Go, Python and Rust SDKs, so `core/extensions/types.ts` is ported. The tui default keybindings no longer depend on the host platform (the coding agent still installs Pi's per-platform table).
