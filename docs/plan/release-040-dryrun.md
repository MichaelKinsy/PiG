<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# PiG 0.4.0 release dry run

Status: evidence for the release lead. This file is not release approval and claims nothing about the published 0.4.0.

- Tree: integration tip `7cc499686` (the pin before the Pi 0.99.2 move; PiG 0.3.0). Section "After the pin move" records what changed when the pin move was merged. Public `v0.3.0` names `f144ac89d`; the 0.3.0 candidate tip in this history is `b1b503a37`.
- Method: every step of `docs/project/RELEASING.md` that needs neither the owner nor the version bump ran in this checkout. Every step that needs the bump ran in a detached scratch worktree after `make set-version VERSION=0.4.0 SET_VERSION_ARGS='--move-unreleased --release-modules'`. All runs used temporary `HOME`, `PIG_CODING_AGENT_DIR` and `PIG_HOME`.
- Not run here: `make test`, `make test-race`, `make test-integration`, `make parity-fast` and `make parity`. They need several hours and the locked Pi comparator (`extensions/sdk-ts/node_modules`). Use the lanes' latest evidence or run them on the release commit. The last recorded `parity-fast` run (`docs/plan/evidence/parity-fast-0.99.1-run6.txt`) had 47 failing scenarios before the later j5 to j8 merges; it says nothing about the present tip.

## What fails today, and why

Numbers match the commands in the last column. "Owner" means who must decide or fix.

| # | Gate | Result | Cause | Owner / fix | Command |
|---|---|---|---|---|---|
| 1 | `make test-porting-release`, `make known-gaps`, `make known-gaps-drift` | FAIL: 184 findings (hot-path pending: 49 cli, 12 extensions, 61 providers, 16 sessions, 6 tools, 40 tui) | The 0.99.1 test mapping has 189 pending files and 1 partial (`2860-replaced-session-context`). AGENTS.md: no release may treat a pending or partial `hot-path` upstream test as closed. The 0.3.x policy closed this gap with owner-approved `deferred-0.3.x` rows; nothing equivalent exists for 0.99.1. `make known-gaps` cannot write its block while the gate is red, so the ledger cannot be refreshed first. | Owner decision: port the files, or approve `deferred-0.4.x` rows in `test/parity/interfaces/test-porting-policy-v0.99.1.json` (same procedure as `test/parity/README.md#approved-03x-test-porting-gaps`). Then regenerate the known-gaps document. | `make test-porting-release` |
| 2 | `make compliance` (pins) | FAIL: 6 findings | The CI parity image still pins the previous Pi release (0.3.0's pin): `automation/images/ci-parity/Dockerfile` `ARG PI_VERSION`, `metadata.env` `IMAGE_TAG` (its `-pi<version>-` part), and `package.json` oracle versions (`pi-agent-core`, `pi-ai`, `pi-coding-agent`, `pi-tui`). Every CI parity job runs in that image. | Lead: bump the four files and the lock, then publish the image with `ci-images.yml` (it publishes only from `main`, so the PR that changes it must merge first or the release branch must use an image built from the bump). Do this after the pin move so it is done once. | `make compliance` |
| 3 | `make correspondence-check` | FAIL: 6 tests | `test/parity/known-gaps.toml` lists two correspondence gaps that are now closed (`finding:table-item:missing:fullscreen-wheel-scroll-lines`, `finding:table-order:table:settings-selector`); `TestCompactionSettingsCorrespondenceCurrentPin` counts 41 mappings, wants 40; `TestExtractGoCurrentSettingsAndPrompts` reads a settings-ID list that no longer matches. | Correspondence owner: remove the closed gaps and reconcile the mapping count. | `go test ./test/parity/correspondence ./test/parity/cmd/correspondence` |
| 4 | `make interface-inventory-test` | FAIL: 1 of 60 Node tests | `test/parity/interface-extractor/test/behavior-input-inventory.test.mjs:247` uses `os` without importing it (`ReferenceError`). | Inventory owner: import `node:os`. | `make interface-inventory-test` |
| 5 | `make sdk-surface-drift` | FAIL | `docs/extension-sdk-surface.md` is stale. | Regenerate with `go run ./test/parity/cmd/sdksurface` and commit. | `make sdk-surface-drift` |
| 6 | `make go-fix-clean` (part of `check-core`) | FAIL: 4 files | `go fix -diff ./...` is not empty: `coding/extension/host/runtimecell/build_failure.go` (`strings.Cut`), `internal/experimental/services/connection_state_test.go` (`t.Context()`), `test/extension-conformance/model_types_test.go` and `coding/virtual_models.go` (`slices.Backward`). | Lead: run `go fix ./...`, review, commit. | `go fix -diff ./...` |
| 7 | `automation/release/module-tags.sh 0.4.0 go.mod` | FAIL before the bump, PASS after | Root `go.mod` requires `extensions/sdk v0.3.0`; the script wants `v0.4.0`. `set-version … --release-modules` fixes this (see the bump rehearsal). | Expected. | `automation/release/module-tags.sh 0.4.0 go.mod` |
| 8 | `make lint` | FAIL: 2 issues | `coding/mcpext/tools_render_test.go:62` uses deprecated `tui.ColorModeTrueColor` (SA1019); `tui/theme_loader.go:267` `resolveTheme` is unused. | Lead: use `tui.TerminalColorModeTrueColor`; delete `resolveTheme` or bind a caller. | `make lint` |
| 9 | `make module-publication` | PASS on the integration tip; FAIL after the bump with `unpublished nested-module tag(s): extensions/sdk/v0.4.0` | The nested tag does not exist until the `publish` job creates it. `make check` includes this gate, so a release branch is red on it until publication. | Expected; run `make check-core` and `make parity-fast` on the release branch instead, and re-run this gate after the tags exist. | `make module-publication` |

Also open, not failing a gate:

- **The Pi pin moved to 0.99.2 after this run.** See the next section.
- **Resolved since this run:** the 0.3.1 fixes (#103, identity, #104, `--exclude-tools`, npm self-update) arrived with the merge of public main at the 0.3.1 release, and `builtin:mcp` is registered in every mode (`fix-992-mcp-session`: `/mcp` appears in RPC `get_commands`). See "Latest rerun".
- **The system prompt names a document that does not ship.** `internal/codingagent/prompts/coding.go:157` tells the model "MCP servers (docs/mcp.md)". `pig docs show mcp` fails with `open content/mcp.md: file does not exist`, and there is no user page for codemode or virtual models in `docs/site/docs`. AGENTS.md requires the user docs in the same change.
- **No changelog fragment** exists for codemode and tool search, the system theme and header, or Sign in with ChatGPT. `grep -il` over `changelog.d` and `CHANGELOG.md` finds none.
- **SDK migration docs.** `extensions/sdk/README.md`, `extensions/sdk-rs/README.md`, `extensions/sdk-py/README.md` and `docs/site/docs/extensions.md` have "Breaking changes / migration to 0.3.0" sections and none for 0.4.0. The Rust SDK breaks `ToolDefinition`, `ToolInfo` and `Provider`; the Go library breaks the `ai` model API, `extension.EventBus`, `extension.API` and `Session.Steer`/`FollowUp`.
- **`changelog.d` holds 88 fragments.** 72 are the 0.3.0 leftovers that `f144ac89d` already carried and `CHANGELOG.md` never folded (116 of their 129 bullets have no text match in `CHANGELOG.md`; only `x-perf-node-terminal-capabilities.md` differs from its 0.3.0 copy). 16 are new. The hotfix lane asked whether to fold the 72 and got no answer; folding them lists shipped 0.3.0 changes as 0.4.0 changes. Decide before the bump.
- **Workflow and doc drift.** `release-candidate.yml` builds six targets (it includes `windows/arm64`; `RELEASING.md` "Build targets" lists five) and smoke-tests three (linux/amd64, darwin/arm64, windows/amd64). `linux/arm64`, `darwin/amd64` and `windows/arm64` are published, and npm ships six platform packages, without a native smoke. `RELEASING.md` says not to list a target as supported until its native or approved equivalent verification passes. The publish job creates the draft with `--generate-notes`, so the owner must replace the body with the real notes. `RELEASING.md` still uses 0.3.0 and 0.2.0 in examples and the npm bootstrap text.
- **Open public issues that a 0.4.0 tag does not fix:** #106 (Windows intermittent `rename: Access is denied` when publishing a Node extension cell), #105 (Kitty keyboard flags in the alternate screen; PR open), #87 (transport cause in diagnostics), #92 (Piglets that strip built-ins).

## After the pin move

The lead confirmed the 0.4.0 target is Pi 0.99.2. The pin move (`2fed2afd1`, re-vendor `185dd701d`, catalogs `9ccf2f43e`) was merged into this branch. Re-runs of the cheap gates on the merged tree show the porter's ledger work is unfinished, so none of the gates below is a verdict on the release; re-run the whole dry run once the porter reports the pin move done.

| Gate after the merge | Result | Cause |
|---|---|---|
| `make test-porting-release` | FAIL, earlier than before | `test/parity/interfaces/test-mapping-v0.99.2.json` does not exist yet; the mapping, policy and baseline are still the 0.99.1 files. The 184-finding count must be re-measured on the new mapping. |
| `make compliance` | FAIL | CI parity image still pins the old Pi (as before), plus `test/parity/known-gaps.toml` `pi_version` and `test/parity/behavior-contracts.toml` `upstream_version` still say 0.99.1. |
| `make correspondence-check` | FAIL | same two tests as before, plus `known-gaps.toml` `pi_version = "0.99.1"` must be re-derived against the new pin. |
| `make docs-drift` (public-claims) | FAIL: 102 findings | About 100 lines in `docs/`, `README.md`, `CHANGELOG.md` and `internal/pigdocs/content` still cite the previous pin. Each needs the new pin, or a `version-records.toml` entry when it records work against 0.99.1 (the `docs/plan/progress/*` files and `docs/plan/upgrade-0.99.1*.md` are such records). The three release documents in this change pass. |
| `make go-fix-clean`, `make interface-inventory-test`, `make sdk-surface-drift` | FAIL, unchanged | items 5 and 6 above. |
| `make divergence-quality` | pass | |

## Rerun with the 0.99.2 mirror and ledgers (porter tip `f0a01af13` merged)

| Gate | Result |
|---|---|
| `make interface-delta`, `make docs-drift`, `check-public-claims.py` | pass |
| `make test-porting-release` | FAIL: 203 findings on the 0.99.2 mapping (685 files: 434 ported, 1 partial, 42 designed-out, 1 divergence, 207 pending). Hot-path pending: 58 cli, 13 extensions, 68 providers, 16 sessions, 6 tools, 41 tui. Same owner decision as item 1. |
| `make upstream-delta` | FAIL: files of the 0.99.2 leap remain `pending` (for example `packages/ai/src/api/anthropic-messages.ts`, `constrained-sampling.ts`, `env-api-keys.ts`, `providers/anthropic.ts`, `utils/overflow.ts`: the `port-992-core` scope). Not part of `make check` or the release workflow. |
| `make compliance` | FAIL: CI parity image still pins the previous Pi (item 2). |
| `make correspondence-check`, `make sdk-surface-drift`, `make go-fix-clean`, `make known-gaps-drift` | FAIL, unchanged (items 1, 3, 5, 6). |

## Latest rerun (tip after `8d1d6c896`, public main at 0.3.1 merged)

| Gate | Result |
|---|---|
| `make test-porting-release` | FAIL, new cause: `packages/ai/test/images-models.test.ts` mapping `designedOutCases` differ from the approved policy list (`[]`); approve or revert the mapping change. Inventory is now 685 files: 529 ported, 1 partial, 43 designed-out, 1 divergence, 111 pending. The hot-path count behind this failure was not printed; re-measure after the policy is fixed. |
| `make interface-delta`, `make docs-drift`, `make divergence-guard`, `make divergence-consistency`, `make source-hygiene`, `make port-map-drift`, `make coverage-drift`, `make interface-inventory-test` | pass (`interface-inventory-test` now passes). |
| `make compliance` | FAIL: CI parity image still pins the previous Pi (item 2). |
| `make correspondence-check`, `make sdk-surface-drift`, `make go-fix-clean` | FAIL, unchanged. |
| `make upstream-delta` | FAIL: 0.99.2-leap files still `pending` (`packages/ai/src/api/anthropic-messages.ts`, `constrained-sampling.ts`, `env-api-keys.ts`, `providers/anthropic.ts`); `foundation-check` only. Its lane merged, so the ledger rows await review. |
| `make lint` | FAIL (govet 3, staticcheck 1, unused 1, goimports 1): `ai/anthropic_token_cache_test.go` (3 `inline` findings for `ai.seconds`), `ai/openai.go:1084` goimports, `coding/mcpext/tools_render_test.go:62` deprecated `tui.ColorModeTrueColor`, `tui/theme_loader.go:267` unused `resolveTheme`. |
| Real binary | `pig -e builtin:mcp` loads; RPC `get_commands` lists `mcp`; `pig mcp add --help` lists `--description` and `--oauth-client-name`; `pig docs show mcp` still fails (`open content/mcp.md`). |
| Vendored JS identity | `X-BILLING-INVOKE-ORIGIN` is `"PiG"` in `provider-attribution.js` and `sdk.js`; no `originator=pi` in `ai/`. |
| Public main | `5e78a4e99` (PR #109, #105 and #108) is newer than the merge and not in this tree; PR #110 (#106) is open. |
| Changelog fragments | 93 in `changelog.d`: 72 legacy 0.3.0 leftovers, 21 newer. `fix-rpc-exclude-tools.md` and `fix-update-channels.md` describe 0.3.1 changes that `CHANGELOG.md` already lists under 0.3.1; delete them, do not fold them. |

## Rerun after merging PR #109 (tip after `6d3a0f17a`)

| Gate | Result |
|---|---|
| `make test-porting-release` | FAIL on the same `designedOutCases` mismatch for `packages/ai/test/images-models.test.ts`. Inventory: 685 files, 537 ported, 1 partial, 43 designed-out, 1 divergence, 103 pending (was 111 pending). |
| `make go-fix-clean`, `make interface-delta`, `make docs-drift` | pass (`go-fix-clean` now passes). |
| `make known-gaps-drift` | FAIL: the same `designedOutCases` mismatch. |
| `make compliance` | FAIL: the `pins` check; the CI parity image, its tag and its oracle packages pin the previous Pi release. Every other compliance check passes. The image bump is a content change, and `ci-images.yml` publishes only from `main`: checklist step B1a publishes it after the content lands and before the release run. |
| `make sdk-surface-drift` | FAIL: 27 problems, for example `ctx.modelRegistry.classify`, `findOfType`, `getAvailableOfType`, `getModelOfType` and `getModelsOfType` are missing in the Node runtime and `test/parity/sdk-surface-exceptions.toml` lists no exception. |
| `make correspondence-check` | FAIL: compaction settings mapping count 41, want 40; `TestExtractGoCurrentSettingsAndPrompts`. **Fixed afterwards:** the `fullscreen-wheel-scroll-lines` selector row exists in Go, so its two `known-gaps.toml` entries were closed gaps and the denominator subtracted a row that is present; the Go settings inventory, the runtime-effect table and the packet counts (34 settings questions, 44 questions, 213 obligations) now include it. The gate passes. |
| `make upstream-delta` | FAIL: 29 files still `pending` (foundation-check only). |
| `make lint` | FAIL, 2 findings (down from 5): `ai/anthropic_token_cache_test.go:85` unused `seconds`; `ai/openai.go:1084` goimports. **Fixed afterwards:** `make lint` reports 0 issues. |
| Public main | PR #109 (`5e78a4e99`) is merged in this tree and its two changelog fragments are folded into `CHANGELOG.md` (`changelog.d` is empty). PR #110 is not merged. |

## Rerun after merging the sdk-surface, ledger, export-theme and parity-fixture work (tip after `3c05c037c`)

| Gate | Result |
|---|---|
| `make lint`, `make correspondence-check`, `make interface-delta`, `make docs-drift` | pass |
| `make go-fix-clean` | passes after one mechanical fix in `test/extension-conformance/context_signal_test.go` (`context.WithCancel` whose cancel only ran in a `defer` became `t.Context()`; the Signal conformance tests still pass). |
| `make sdk-surface-drift` | FAIL: 5 problems (was 27): `ctx.ui.theme.appearance`, `colors` and `style` are missing in Go and in the Node runtime, with no listed exception. |
| `make compliance` | FAIL: the `pins` check only (CI parity image; checklist step B1a). |
| `make test-porting-release`, `make known-gaps-drift` | FAIL: the `designedOutCases` mismatch for `packages/ai/test/images-models.test.ts` (owner approval). Inventory unchanged: 537 ported, 103 pending. |
| `make upstream-delta` | FAIL: 29 rows pending (foundation-check only). |

## Pi 0.99.2 items (verify before the notes keep them)

Pi 0.99.2's coding-agent changelog (tag `v0.99.2`, commit `005af57d88ee23b33778f343a9595b32e67ff788`) is ported by two lanes. `port-992-mcp` merged (`3b84fbf46`): `pig mcp add --help` lists `--description` and `--oauth-client-name`, and `describeNamespace` exists in `coding/extension/builtin/codemode`; its session-level behavior is unreachable because `pig -e builtin:mcp` still fails with `Unknown built-in extension`. The lane reports #10204 as N/A for the Go engine (QuickJS on wazero runs in process, with no worker file). `port-992-core` merged (`5194c15f3`): `ANTHROPIC_FEDERATION_RULE_ID` is in `ai/auth_env_keys.go`, and the targeted tests pass (`TestAnthropicWorkload*`, `TestAnthropicFederation*`, `TestAnthropicStrict*`, `TestOverflowUpstream`, `TestProviderRetryDelay*`, `TestDefaultToolsReloadPort`, `TestModelRuntimeNativeCompatibilityUpstream`, `TestGetBranchSelectionLooksUpOnlyTheLastModelChange`, the Node, Go SDK, Python and Rust command-validation tests). Its report finds the quadratic remote-catalog merge has no PiG counterpart (PiG has no remote catalog refresh, owner-approved in PORT_MAP), so the notes do not claim it. Before the merges none of these existed: `git grep` for `ANTHROPIC_FEDERATION_RULE_ID`, `describeNamespace` and `oauth-client-name` finds no production code. The notes carry each bullet between `<!-- lane:... -->` markers. For each bullet, run the check and keep the bullet only when it passes; delete a lane's block if the lane did not merge.

| Notes bullet | Lane | Check on the release commit |
|---|---|---|
| MCP servers with default `codemode` exposure leave the codemode description, connect in the background, `mcp_servers` prompt section, `describeNamespace`, server `description` | port-992-mcp | `git grep -l describeNamespace -- '*.go'` lists production files; RPC `get_state`/system prompt of a run with a configured server shows an `mcp_servers` section. Needs MCP servers to run in a session (see "`builtin:mcp` is not registered" above). |
| `oauth.clientName`, `--oauth-client-name`, `"auth": {"provider": …}` | port-992-mcp | `pig mcp add --help` lists `--oauth-client-name` and `--description`; a config with `auth.provider` over `http://` (non-loopback) is rejected. |
| MCP name normalization, hash suffix, colliding server names rejected | port-992-mcp | `mcp__my-server__x` registers as `mcp__my_server__x`; two servers `a-b` and `a_b` fail with an error. |
| `/mcp` sign-in URL hyperlink, `image()` validation, wrapped-line previews, `codemode.mode: "only"` prompt list | port-992-mcp | the lane's ported upstream tests pass; the notes cite behavior, not test names. |
| Standalone Windows codemode worker (#10204) | port-992-mcp | The lane must say whether it applies to PiG's QuickJS/wazero engine or is N/A; the notes do not mention it. |
| Anthropic workload identity federation | port-992-core | `git grep -l ANTHROPIC_FEDERATION_RULE_ID -- '*.go'` lists production files and a hermetic request test. |
| `/reload` enables newly added `defaultTools` | port-992-core | the lane's test for added, removed, session-off and `--tools` override cases passes. |
| default model with an extension native provider; prompt-submission and catalog-merge performance; Z.AI CN overflow; Anthropic strict schemas; `Retry-After` fallback; extension command without name or handler fails to load | port-992-core | the lane's red-then-green tests; the performance items also need the lane's before/after benchmark. The two example-extension fixes (`built-in-tool-renderer.ts`, `minimal-mode.ts`) are not in the notes because PiG may not ship them. |

## What passes

| Gate | Result |
|---|---|
| `make set-version VERSION=0.4.0 SET_VERSION_ARGS=--dry-run` | Plan shows the new heading, `pigversion.go` and `piglets/standard/pig-standard.yaml`. Without `--move-unreleased` the four `[Unreleased]` bullets stay above an empty `[0.4.0]`: use `--move-unreleased`. |
| `make set-version VERSION=0.4.0 SET_VERSION_ARGS='--release-modules --dry-run'` | Plan rewrites 17 `go.mod`/`go.sum` files (root, fixtures, examples, the standard piglet). After the real apply the same dry run prints nothing, which is the `source` job's precondition. |
| `make set-version … --move-unreleased --release-modules` (apply, scratch tree) | Exit 0; `go test ./test/gomodule` and `TestParseChangelog_RealFile` pass; `automation/release/module-tags.sh 0.4.0 go.mod` prints `v0.4.0` and `extensions/sdk/v0.4.0`. |
| `make divergence-consistency divergence-quality divergence-guard` | 47 current approved records with markers; 50 baselined guard hits. |
| `make docs-drift`, `make source-hygiene`, `make port-map-drift`, `make coverage-drift` | clean (661 upstream source files accounted for). |
| `make ci-drift` | clean. |
| `automation/ci/check-public-claims.py` | no contradicted claims. Also clean with the draft notes passed as an extra path, and clean in the bumped scratch tree. |
| `make compliance`, other than the pin rows of item 2 | files, citation, actions, permissions, reuse and govulncheck pass. |
| `make npm-dist-test` | pack generator and launcher unit tests pass. |
| `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./cmd/... ./internal/... ./coding/... ./ai/...` | pass. |
| `go test` of `codemode`, `coding/extension/builtin/...`, `mcp/...`, `coding/mcpext`, and targeted `coding` and `ai` tests for virtual models, typed models and ChatGPT sign-in | pass. |

## Artifact rehearsal (scratch tree at the bumped version)

The scratch tree built the six release targets with the release workflow's flags (`CGO_ENABLED=0 -trimpath`, `-X …DefaultUpdateURL`, `-X …DefaultUpdateTrustRoot` from `update-trust.pem`), archived them as the workflow does, and ran the release scripts. SBOMs, Grype scans, attestations and Sigstore bundles are not reproducible locally; the rehearsal uses placeholder evidence files so the assembly code runs.

| Step | Result |
|---|---|
| `assemble-evidence.py` (and its unit test) | 7 targets (six plus source) assembled; rejects a missing or mismatched file. |
| `combine-checksums.py`, `sha256sum -c SHA256SUMS`, `sha256sum -c EVIDENCE-SHA256SUMS` | 7 archives listed once each, sorted; both checks pass. |
| `gen-update-manifest.py --sha256sums` | 4 entries (darwin/linux × amd64/arm64), Windows omitted (D39), `packageName` `@pi-in-go/pig`. |
| `sign-update-manifest.sh` with a throwaway Ed25519 key against the real `update-trust.pem` | Refuses: `signing key 1 of 1 has no public key in automation/release/update-trust.pem`. This is the workflow's guard against a wrong `PIG_UPDATE_SIGNING_KEY`. |
| Same script with the throwaway key's own trust file | prints one base64 signature; `openssl pkeyutl -verify -pubin -inkey trust.pem -rawin -in update.json -sigfile sig.bin` prints `Signature Verified Successfully`. |
| `docs/site/public/install.sh` against a local HTTPS server (`PIG_DOWNLOAD_BASE`, `PIG_VERSION=0.4.0`, throwaway CA) | Downloads, verifies the SHA-256, installs, prints `0.4.0+0.99.1`, writes an owner-only `install-receipt` with `update-source` `…/releases/latest/download/update.json`. `install.sh` refuses plain `http` (curl `--proto =https`). |
| `pig update self` from a 0.3.0 build that trusts the throwaway key, against a local manifest for 0.4.0 | `Updating pig 0.3.0 → 0.4.0... Updated to pig 0.4.0.`; then `pig --version` prints `0.4.0+0.99.1`. This exercises receipt → manifest → signature → archive SHA-256 → replace. |
| `pack_npm.py --archives release --version 0.4.0` | 7 tarballs (six platform packages, then the launcher) and `publish-order.txt`; each platform package has `os`/`cpu`, the binary, `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES.md`; no install scripts; descriptions say "based on Pi <pin>". |
| `npm install -g --prefix TMP --offline` of the launcher and the linux-x64 package, then `pig --version` | `0.4.0+0.99.1` through the launcher. |
| `npm publish --dry-run --access public` on the launcher tarball | `+ @pi-in-go/pig@0.4.0` (dry run; needs a login only for the real publish). |
| Registry state | `@pi-in-go/pig` has `0.2.0` and `0.3.0` (`latest` is `0.3.0`); all six platform packages exist at `0.3.0`, so trusted publishing is already configured for all seven. |
| Public tags | `v0.2.0`, `v0.3.0`, `extensions/sdk/v0.2.0`, `extensions/sdk/v0.3.0` exist; no `v0.3.1`. |

Not rehearsable here: the `release` environment's reviewers and `PIG_UPDATE_SIGNING_KEY` secret, `actions/attest-build-provenance`, Sigstore bundles, the private hosting repository's `install.ps1`, `/api/installer/releases` and the `dl.pi-in-go.dev` mirror, macOS and Windows native runs, and the external `go install` proof (it needs the published tags).

## Feature claims checked by name

Each claim in the draft notes was checked in the tree. Paths are relative to the repository root.

| Claim | Evidence |
|---|---|
| Codemode and tool search, no Node | `codemode/engine.go`, `codemode/assets/quickjs.wasm` (provenance in `codemode/assets/PROVENANCE.md`), `github.com/tetratelabs/wazero v1.12.0` in `go.mod`, `coding/extension/builtin/codemode`, `…/toolsearch`; `pig -p -e builtin:codemode` loads; tests pass. |
| MCP client and `pig mcp` | `mcp/`, `mcp/oauth/`, `coding/mcpext/`; built binary prints `pig mcp add|remove|list|login|logout` usage; tests pass. Session wiring absent (see above). |
| Typed models, images, classifiers | `coding/model_runtime_typed.go` (`GenerateImages`, `GetModelsOfType`, …); `TestModelRuntime…` image and classifier tests pass. |
| Virtual models | `coding/virtual_models.go` (`RegisterVirtualModel`); footer routing commit `ef31ac289`. |
| Extension API in every SDK | changelog fragments `port-99-f6d`, `f6f-go`, `f6f-node`, `f6f-py`, `f6f-rs`; `extensions/sdk/event_bus.go` (`Events()`); `extensions/sdk-py/tests/test_extension_api_099.py`; conformance rows under `test/extension-conformance`. |
| Sign in with ChatGPT, GPT-6.1 Sol, Kimi K3 defaults | `ai/oauth_openai_chatgpt.go` (5 tests pass), `ai/models_generated.go`, `internal/codingagent/default_models.go:12`. |
| System theme, `oklch`/`okhsl`, header logo | `tui/theme_setting_test.go`, `tui/colors.go`; commits `0ba28f0af` (logo header), `f3e558b64`. One `tui` test needs the locked Pi comparator and fails without it. |
| Built-in extensions in `pig config`, `builtin:` paths | `coding/extension/builtin/builtin.go`; `pig -e builtin:mcp` error above shows the path form. |
| Settings `fullscreenWheelScrollLines`, `codemode`, `deviceId`, `+name`/`-name` | `internal/codingagent/settings.go`; fragment `port-99-f6a-settings-session`. |
| npm self-update package name | `internal/codingagent/paths.go:22` `PackageName = "@pi-in-go/pig"`; fragment `fix-update-channels`; npm rehearsal above. |
| Windows fixes | commits `036a72174` (npm quarantine), `e878b29c8` (cross-spawn `cmd.exe` line), `ea239b5ef` (env names with `=`), `dbc9550e1`, `a4f1368a4`, `141f9e4e1` (lock errors). |
| Credits | `git log b1b503a37..HEAD` (non-owner authors: Samarth Boranna, PR #99) and the public issue tracker for #85, #86, #88, #89, #90, #98, #99, #101, #103, #104, #105, #106. |
| No "official" wording | `check-public-claims.py` clean; `grep -i official` over the notes prints nothing. |
