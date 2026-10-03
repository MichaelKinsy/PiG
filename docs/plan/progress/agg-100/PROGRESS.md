# agg-100: Pi 1.0.0 aggregate for PiG 0.4.0

Branch `agg-100` from `porter/pi-0.99.1` at `48fff2a29` (the Pi 1.0.0 pin). The gate set is the CI `linux` matrix (`make ci-<shard>` for startup, build, test-fast, test-cli, test-subprocess, test-conformance, sdk, extensions, race, integration, parity, drift, contracts and closure) plus `make ci-node-runtime` and the docs-job checks (`go test ./test/ci-images/ ./test/docs-drift/`, `check-public-claims.py`). Each shard runs under `automation/ci/with-isolated-pig-home.sh`.

## Merged

| Order | Branch | Notes |
|---|---|---|
| 1 | `release-040-content` (0.99.2 content, CI rounds 1-3, tui-models) | clean |
| 2 | `c1-codemode`, `rev-c1-codemode` | clean |
| 2 | `c1-mcp-oauth`, `rev-c1-mcp-oauth` | clean |
| 2 | `c1-tui`, `rev-c1-tui` | progress-file conflict only |
| 3 | `rev-fix-114-pig-header` | already in `c1-tui` |
| 3 | `rev-fix-take1` | generated files only |
| 4 | `pig-eggs`, `rev-pig-eggs` (incl. the pigsayhi correction) | pig-eggs' ported logo-animation rows replace c1-tui's designed-out rows; the wordmark path records that no pig is drawn (Pi sets `onLogoClick` only with its logo, `interactive-mode.ts:1058`); `TuiAltScreen.GetScreenLines` was added twice |
| 4 | `c1-login-fixes`, `rev-c1-login-fixes` | the login lane re-ported changes c1-tui and c1-mcp-oauth already had (pending tools, MCP sign-in link and section intro, slash completion after whitespace, ANSI slice order, theme chroma, user message padding). One implementation of each stays and the login lane's tests run against it. `FormatAuthSelectorProviderType` keeps `...bool` (PiG's mapping of an optional trailing boolean). Duplicate changelog bullets removed. |
| 4 | `win-ga`, `rev-win-ga`, `win-cli-modes`, `rev-win-cli-modes` | clean; `make vet` now also vets windows/amd64 and windows/arm64 |
| 4 | `fix-ext-discovery`, `rev-fix-ext-discovery`, `p1-experimental`, `rev-p1-experimental` | clean |
| 4 | `oauth-url-order`, `rev-oauth-url-order`, `piglet-add-origins`, `rev-piglet-add-origins`, `piglet-unresolved-ext`, `rev-piglet-unresolved-ext` | clean; `go fix` rewrites one new test loop to `strings.SplitSeq` |
| 4 | `openrouter-images-error`, `rev-openrouter-images-error`, `rev-c1-login-fixes` (READY marker) | CHANGELOG `### Fixed` keeps both bullets |

| 4 | `p1-2860`, `rev-p1-2860` | Node runtime archive, digest and SDK surface matrix regenerated from the merged sources |
| 4 | `rpc-usage-snapshot-100`, `parity-fullscreen-100`, `oracle-regen-100` and their reviews; `c1-tui` (fullscreen selector key routing) | c1-tui and parity-fullscreen-100 fixed the same routing and scenarios: parity-fullscreen-100's `consumeModalHostInput` (also routes a focused transcript search) and scenario re-probes stay; c1-tui's 24-fullscreen PageDown scenario and 99-column 03-startup-expanded-help stay |
| 4 | `rev-win-ga` (READY), `d-foundation`, `rev-d-foundation`, `win-symlink-skips`, `win-auth-acl` and reviews | durable rows that d-foundation maps take its dispositions; the review's inlined `new(expr)` helpers stay |
| 4 | `fix-win-native`, `win-ci-coverage` and reviews; latest `c1-tui` (remote overlays route through `consumeModalHostInput`), `rev-c1-login-fixes`, `rev-p1-2860`, `rev-piglet-unresolved-ext` | clean |
| 4 | `d-storage`, `rev-d-storage`, `win-python-alias`, `rev-win-python-alias` | durable rows take d-storage's dispositions; `go fix` applied to the new durable code |
| 4 | `d-session`, `rev-d-session`, `chord-100`, `rev-chord-100`, `win-bitmap-paste`, `rev-win-bitmap-paste`, latest `c1-tui`, latest `d-foundation` | `AssertValidOp` keeps the JS `String(value)` verb text; `Session.Close` keeps the review's panic-safe listener barrier |
| 4 | latest `d-session`, `rev-chord-100` (READY), `stubgen-100`, `rev-stubgen-100` | clean; `go fix` applied to two new client tests |
| 4 | READY re-reviews of `chord-100` (already in), `parity-fullscreen-100`, `win-auth-acl` | marker commits only |
| 4 | `d-harness-a`, `rev-d-harness-a`, `xd-models-iface` (carries exp-durable and d-env-tools) | harness test rows take d-harness-a's dispositions; `AddTools` keeps the review's filter read; `SectionOptions.Tag` is `*bool` |
| 4 | `syntax-highlight`, `rev-syntax-highlight`, `rev-d-env-tools`, final `d-harness-a` (9308fc2d3), latest `xd-models-iface` | clean merges |
| 4 | `sg-durable-tools`, `rev-sg-durable-tools` | three harness tests now call `durable/env/node`; `sg-chord-rest` is rejected and not merged |
| 4 | `baseline-reachable-100` (C2), `p100-rest`, `ci-hygiene-100` and their reviews | p100-rest's two ported ai rows win the test-mapping conflict; the pi-telemetry schema version is classified upstream (C6) |

| 4 | `sg-telemetry-evals` and review | its telemetry port replaces the duplicate from p100-rest; p100-rest's two telemetry tests that ported the same upstream files against the other types are removed |
| 4 | `rev-d-env-tools` (final), `xd-transcript-bridge` and review | clean |
| 4 | `sg-server-check`, `rev-sg-server-check`, `rev-d-harness-a` (final) | `routing.SessionMetadata` stays the interface Pi's generic `ServerHost<TMetadata extends SessionMetadata>` needs; the new `routingtest` package uses `BasicSessionMetadata` |
| 4 | `mcp-fixture-exe-100`, `sprites-restore` and their reviews | the D2 call-site list takes the union of both sides |

| 4 | `xd-coding-harness`, `d-harness-c`, `cmdpig-timeout-100` and their reviews | the durable/env/node split keeps the reviewed rev-sg-durable-tools version (later fixes); the unused test helpers go (R11); the racing harness cases keep the final rev-d-harness-a ordering |
| 4 | `xd-harness-retire` and review | `agent/harness` is deleted; `ai/uuid.go` keeps p100-rest's injected-clock generator; the review's Pi-exact UUIDv7 case and two Go-only cases are ported onto it |
| 4 | `retained-cancel-race-100`, `preflight-r4-fixes`, final `cmdpig-timeout-100`, `rev-xd-coding-harness` (READY) | clean |

| 4 | `d-harness-b` (with its review) | the reviewed d-harness-c port of harness-tools-recovery stays; d-harness-b's duplicate and its bash wrapper go; three colliding structured-test helpers are renamed |
| 4 | `nodespawn-lazy-regexp` (Windows lane; its two commits are unsigned, so they must not reach public as-is) | clean |

| 4 | `custom-header-spacing-100`, `partial-snapshot-100`, `clone-after-first-turn-100` and their reviews; final `d-harness-b` and `preflight-r4-fixes` follow-ups | the agg-100 notes take the lane's R8 row |

| 4 | `win-ext-job-100` and review (Windows lane; unsigned commits), final `rev-d-harness-b` follow-ups | `tool_hooks_test.go` keeps agg-100's `resultText` helper and takes the review's typed `prepareArguments` test |
| 4 | `unix-probe-limit-100` and review (test-only, `internal/experimental/client`) | clean; merged during round 19, so round 19's test-fast may predate it |
| 4 | `header-truecolor-100` and review (header logo tests) | clean |
| 4 | `chord-guide-watch-race-100` and review (C22) | clean; fixes round 22's `TestChordUsageGuide` job-output watch failure |
| 4 | `details-null-100`, `facet-observer-order-100` and their reviews | coverage regenerated |
| 4 | `chord-js-equiv-100` (speed mode: folded on green tests, review in parallel) | the tracker row is closed by equivalence tests (R14a) |
| 4 | `win-agentdir-lifetime-100` (speed mode; test-only, `coding`) | clean; `coding` passes, leaves no TMPDIR entries |
| 4 | `rev-chord-js-equiv-100`, `rev-win-agentdir-lifetime-100` (review fixes on top) | clean; affected packages pass |
| 4 | `first-run-sprite` and review (owner-approved first-run setup with the sprite step) | clean; `internal/codingagent` and the 17 changed `cmd/pig` tests pass; generated files current |
| 4 | `chord-js-equiv-100` (R14b follow-ups) and its final review | `tracker_equivalence_test.go` keeps both sides; `known-gaps` no longer reports a designed-out chord case |
| 4 | final `rev-win-agentdir-lifetime-100`; `win-tests-batch-100` and review (Windows test fixes; unsigned win commits) | `fileSymlinkCallers` keeps agg-100's list with the lane's reason for the filesystem file-info case; affected packages pass |
| 4 | `wizard-polish` and review (owner live-tested), `durable-partials-100` and review | D88 keeps agg-100's order with wizard-polish's text; affected packages and the changed `cmd/pig` tests pass |
| 4 | `node-equiv-100`, `durable-final-100` and their reviews | clean; `known-gaps`, `test-porting-release` and `make generate` pass (609 ported, 0 partial, 0 pending) |
| 4 | `egg-run-pig` (braille running pig, owner-approved live) and its re-review; `wizard-polish` C29 and its review | the superseded half-block review (fc5832aed) is not merged on its own: rev-egg-run-pig merged it without changes; the bundled divergences.md takes egg-run-pig's D87 and wizard-polish's D88 |
| 4 | `wizard-polish` READY `0e407f8b8` (owner decision `2ee069fed`: Create your own... shows a hint only) | clean; affected packages pass |

Waiting: `win-native-100` (R19), `harness-close-race-100` (a second TestHarnessClose ordering case; merges cleanly).

## Fixed in this branch

| Root cause | Failing checks | Fix |
|---|---|---|
| Progress notes claimed the old pin as a Pi version or named private branches | `docs-drift` (public claims), `check-scratch-paths` | reworded `c1-mcp-oauth`, `p1-mcp-oauth`, `c1-login-fixes`, `p1-login-fixes` notes |
| `extensions/sdk-ts/README.md` named 0.99.2 while the package depends on Pi 1.0.0 | `ci-sdk` (`test-sdk-ts`, `check-upstream-pin.mjs`) | README names Pi 1.0.0 |
| `TestProbeFindsVendoredPackageAndSymbolMembers` still expected the harness errors that Pi 1.0.0 removed from pi-agent-core | `test-fast` (`test/parity/cmd/sdksurface`) | rows dropped; member probing stays covered by `ModelsError.cause` and `CredentialSynchronizationError.cause` |
| pi-coding-agent 1.0.0's shrinkwrap gives its seven `@earendil-works` packages no integrity, so the ci-parity oracle lock had none | `test-fast` (`TestCINpmLocksPinDownloadIntegrity`) | registry `dist.integrity` added; `npm ci` installs the lock unchanged and fails with EINTEGRITY on a corrupted digest |
| `TestResolveModelProviderFlagWithoutModelIsIgnored` asserted the 0.99.2 rule that c1-login-fixes replaced with Pi 1.0.0 `main.ts:469-474` | `test-cli` | test asserts the 1.0.0 error |
| The #5943 regression test moved its cases two lines down in 1.0.0; the paired probes still selected 0.99.2 line sites (R5) | `parity`: `session/24-runtime-cost-and-resource-order`, `session/28-session-reload-ui`, `session/29-session-rebind-ui` | Pi probes, PiG record sites and scenario comments use the 1.0.0 sites; pig and Pi probe outputs are equal |
| `packages/server/test/conformance.test.ts` changed only its metadata types at 1.0.0 (R3) | `test-porting-release` | the Go cases take 1.0.0's literals and line sites; all 20 pass; row ported again |
| `15-grammar-tool-stream-and-replay` expected 0.99.2's normalized foreign id `fc_otg7yamm1iir`; Pi 1.0.0 drops an id without the item type's prefix (`openai-responses-shared.ts:299-305`) | `parity` (pig and Pi fail alike) | asserts the id-less foreign `custom_tool_call` |
| Local environment only: the isolated HOME hides mise's tool versions, and Xvfb was missing | `startup`, `contracts` (`sdk-surface-drift`), `node-runtime`, `test-subprocess`, `internal/nativeplatform` | real tool directories ahead of the mise shims; Xvfb from the Ubuntu package, unpacked outside the system. CI installs both. |

## Open, by root cause

| Root cause | Failing checks | Owner |
|---|---|---|
| R1. Pi 1.0.0 replaced the agent harness (`packages/agent/src/harness`, experimental mini/micro) with `pi-durable`; PiG still carries the 0.99.2 Go harness, and D-H keeps durable out of scope | `divergence-guard` (7 invalid `// upstream:` markers, 23 hits in `agent/harness/**`, `internal/codingagent/tools/harness_bash.go`); `agent/harness/env` (`TestExecReportsSpawnArgumentErrorsAsPiDoes`, `TestExecGivesTheShellPisEnvironment`); `agent/harness/pico3` (`TestBashToolRejectsSpawnArgumentErrorsAsPiDoes`); `internal/experimental/mini/shared` (`TestRpcUpstreamChildPeer`, `TestRpcInvocationOrderUpstreamOracle`, `TestJSONConnectionUpstreamOracle`); `internal/experimental/services` (`TestControllerMatchesPinnedProvider`, `TestModelsProviderMatchesPinnedUpstream`) | exp-durable and the durable lanes (the owner reversed D-H: 0.4.0 includes pi-durable) |
| R2. Recorded oracles still carry the 0.99.2 stamp | `coding` (6 RPC33 and faux oracle tests); `cmd/pig` (`TestFauxRPCObservation`, `TestTestFauxRPCObservation`) | oracle-regen-100 (started) |
| R3. Pi 1.0.0 durable tests are pending hot-path rows | `test-porting-release`, `known-gaps-drift`: 27 `packages/durable/test/*` findings (the 2860 row closed with p1-2860) | durable lanes (d-foundation, d-storage, d-session, d-harness-a/b/c, d-env-tools) |
| R4. Pi 1.0.0 defaults to fullscreen; tmux scenarios that wait for a repainted line or read scrollback no longer see one, on Pi and PiG alike | `parity`: `settings/03-settings-filter-theme`, `settings/04-settings-theme-submenu`, `04-changelog-structure`, `11-changelog-complete-history`, `13-scoped-models-fuzzy-filter`, `14-model-picker-fuzzy-filter`, `22-model-picker-unbound-keys-refilter` | parity-fullscreen-100 (started) |
| R6. Startup header first line: Pi ends the first hint line's color after the line break, PiG before it | `parity`: `startup/03-startup-expanded-help` (escaped bytes) | fix-header-art |
| R7. RPC toolcall_end carried the final usage in 1 of 3 chunked runs | `cmd/pig` `TestRPCPiMessagesMatchesPi/tool/chunked` (round 3) | rpc-usage-snapshot-100 (started) |
| R9. The durable port adds version fields that the format-version inventory does not classify (`durable/codecs.go`, `documents.go`, `tasks.go`, `types.go`: `Version` of document and task definitions, `DocumentContent`, `StoredDocument`, `TaskRecord`; from d-foundation) | `ci-contracts` `format-version-inventory` (round 8) | durable lanes (classify as upstream-owned pi-durable schema versions) |
| R10. `durable/env` holds the Node execution environment, but Pi splits it into `./env` (portable) and `./env/node` (packages/durable/package.json exports); d-storage's boundary test rejects the host imports | `durable/storage` `TestDurableStorageRuntimeBoundaries` (3 subtests) | d-env-tools (still failing after rev-d-env-tools): move the Node environment to a `durable/env/node` package |
| R11. Durable lint after the batch: unused helpers (round 15: only `requestId`, `intPtr` and `boolPtr` remain) in `durable/harness/harness_compaction_test.go` and `harness_generation_test.go`, gocritic appendAssign and gosec G103 in `durable/harness/tool.go` | `ci-build` lint | d-harness lanes (the compaction cases are not ported yet, or their helpers belong to d-harness-b) |
| R12. `TestModelsConcurrentCatalogRevisionsAreDistinct` got 99 distinct revisions, want 100 | `internal/experimental/services` | exp-durable |
| R13. After the durable batch two durable harness tests fail deterministically in the merged tree: `TestHarnessClose` (`harness_lifecycle_test.go:636`: "Session is closed") and `TestToolProgressAndLifetime` (`harness_tools_progress_test.go:156`: details n 2, want 3). Likely an interaction of rev-d-session's close-listener barrier with the harness close and progress paths | `durable/harness` | closed: `durable/harness` passes after the final rev-d-harness-a |
| R14. `make known-gaps` (so `make generate`) stops at the release policy: `packages/chord/test/delta-tracker/tracker.test.ts` lists `designedOutCases` without `SCRUTINIZED:approved` owner approval | `known-gaps`, `test-porting-release` | owner approval or the chord lane (recorded as a blocker in the chord lane commit d0b0884df) |
| R14a. After preflight-r4-fixes, four JavaScript-only cases of `packages/chord/test/delta-tracker/tracker.test.ts` stay in `designedOutCases` pending `SCRUTINIZED:approved` owner approval, so `known-gaps` still fails | `known-gaps`, `test-porting-release` | chord-js-equiv-100 (owner: closed by equivalence tests, not by approval) |
| R17. Fixed here: `TestHarnessClose` (inspect queued before close reports closing) failed 22 of 400 runs because `queueOnLine` accepted any line-job growth, such as scheduler work, as Inspect's own queued job. `queueOnLineIn` waits for a goroutine in `harnessImpl.Inspect` blocked in `session.enqueue`; 0 of 400 runs fail | `ci-test-fast` (`durable/harness`) | closed in this branch; the harness-close-queue-100 fix lane is not needed |
| R18. Parity `34-empty-custom-footer`, `35-custom-editor-component` and `53-node-scrollview-footer` fail: PiG's screen has two fewer rows between the header region and the footer or editor than Pi's, after the sprites-restore header change (D2) | `ci-parity` | custom-header-spacing-100 (in review; compares these scenarios in 256 colors) |
| R19. `TestRadiusLoginOffersTheRadiusMCPServerUpstream/points_an_existing_server_at_the_Radius_login` read `mcp.json` while Pi rewrites it in place: "unexpected end of JSON input" at `radius_login_mcp_offer_upstream_test.go:119` (round 23) | `ci-test-fast` (`internal/codingagent`) | win-native-100 (its commit 9896f5a9b waits for the final mcp.json; awaiting acceptance) |
| R14b. With the tracker row closed, `known-gaps` reaches `packages/chord/test/json.test.ts`, whose policy row lists `copyJson › optionally omits undefined object properties without normalizing arrays` in `designedOutCases` with `SCRUTINIZED:proposed` | `known-gaps`, `test-porting-release` | chord-js-equiv-100 or a follow-up: an equivalence test, as for R14a (no approval marker) |
| R20. Closed by durable-final-100: no hot-path durable test file remains partial | `ci-contracts` | closed |
| R21. Fixed here: first-run-sprite left D88 out of order, out of the active count and out of the bundled divergences.md, and its first-time dialog blocked three integration tests | `ci-drift`, `ci-integration`, `ci-test-fast` | closed in this branch (`c46c09584`) |
| R15. `cmd/pig` passes the 10-minute package timeout in test-cli; the Pi side of `TestRPCInputEndCommandSettlesDuringSlowShutdownHandler` (timer50, fspromises) reports "RPC output did not close" | `ci-test-cli` | cmdpig-timeout-100 (in review) |
| R16. Fixed here: nine sprite catalogue sites carried `pig additive (D2)`, but D2 is a divergence record (`divergence-consistency`); five durable test callers of `testenv.Symlink` were not classified (`TestSymlinkCallersAreFileOrUnresolvableLinks`) | `ci-drift`, `ci-test-fast` | closed in this branch |
| R8. `session/66-session-replacement-lifecycle`: in the full parallel run `/clone` reports "Error: This session has not been saved yet. Send a message before cloning or forking it." right after the first answer, and the error is drawn above that turn's user message (rounds 3 and 4; 3 of 3 runs pass alone) | `parity` | clone-after-first-turn-100 READY (`0036d5341`): the cause is the parity runner, not PiG. `wait_contains` counted raw transcript bytes, so `ESC[42;1H` from the repaint after Enter satisfied a wait on "42" before the answer existed and `/clone` ran ahead of the turn. Pi emits message_end to listeners and then persists, as PiG does. The runner now strips CSI and OSC from the transcript (`TestWaitIgnoresDigitsInTerminalControlSequences`, red before, red under an OSC mutation). Not reproduced at 16 parallel: `-count=20` over 16 extensions-runtime tmux scenarios passed 320/320 with and without the fix, so the full parallel run is the remaining confirmation. 65, tui-components/02, slash-commands/05, compaction/03 and tree/10 wait on "42" too. |

## Fix lanes

- FIXLANE: oracle-regen-100 regenerate the 0.99.2-stamped recorded oracles (coding/testdata/rpc33-observation/**, the faux and test-faux observation oracles read by coding and cmd/pig) from real Pi 1.0.0 with their probes, flip the oracle version guards, and fix any PiG behavior the new oracles expose (R2: 8 tests).
- FIXLANE: parity-fullscreen-100 re-probe the 7 tmux scenarios of R4 against Pi 1.0.0's fullscreen default: replace waits that count a repainted line and scrollback captures that the alternate screen never fills with waits and captures that hold in fullscreen, keeping the comparators at least as strict; touch test/parity/runner only if a driver change is needed for every fullscreen scenario.
- Superseded by exp-durable (owner reversed D-H): harness-retire-100 the 0.99.2 Go harness (`agent/harness/**`, `internal/codingagent/tools/harness_bash.go`, `internal/experimental/mini`, the experimental services providers) has no Pi 1.0.0 counterpart and D-H keeps it from moving to pi-durable. Per package, decide with the owner: delete it with its callers, or keep it as a PiG-only capability recorded in docs/additive-features.md, with tests that no longer import upstream files 1.0.0 removed. Then remove or rewrite the 7 invalid `// upstream:` markers and account for the 23 divergence-guard hits (a baseline entry only for code that stays, under one owning finding).
- FIXLANE: clone-after-first-turn-100 under load, PiG's `/clone` right after the first turn's answer reports "This session has not been saved yet", and the error renders above that turn's user message (`session/66-session-replacement-lifecycle`, rounds 3 and 4; it passes alone). Pi's AgentSession persists the message at message_end before listeners see it. Find where PiG's clone check reads the persisted state relative to the visible turn (Session persistence versus the interactive event path), make the order match Pi, and add a regression that delays persistence or event delivery.
- FIXLANE (folded into fix-header-art): header-first-line-100 make the first line of the built-in startup header end its hint color where Pi does (Pi's escaped capture closes the color after the line break, PiG's before it; startup/03-startup-expanded-help, R6). Start after fix-header-art lands, which redraws that header.
- FIXLANE: rpc-usage-snapshot-100 cmd/pig TestRPCPiMessagesMatchesPi/tool/chunked failed 1 of 3 runs in round 3: PiG's toolcall_end message_update carried the final usage (input 10, output 3) where Pi's carries zero usage, so the event's partial message is read after the usage chunk mutates it. Find where the RPC record snapshots the partial assistant message and make it copy at emit time, with a regression that forces the usage chunk to arrive before serialization.

## Status log

- Round 32 done on `99e80ea52` (includes first-run-sprite, wizard-polish READY with C29 and the owner's 11:57 decision, egg-run-pig with its re-review, node-equiv-100, durable-final-100): all 15 shards green, including contracts, integration, test-cli (five shards) and parity. `make test-integration` also passes on `b25426351`. Preflight round 17's failures on `cff68f823` predate the D88 test seeding (`77336c21f`), the C29 gate fix and the durable-final fold.

- After the egg-run-pig and wizard-polish C29 folds (`0a493cd4b`): drift, integration and test-fast green. D88 follows D87; 34 active divergences.

- After node-equiv-100 and durable-final-100: contracts gates pass locally (`known-gaps`, `test-porting-release`, `known-gaps-drift`); `make generate` completes. Full round 31 started.

- Round 29 on `cff68f823`: contracts (R20), integration and test-cli failed. Integration and test-cli: four interactive tests in fresh agent directories waited behind the first-time setup dialog (D88); fixed by seeding settings.json (R21 follow-up). Parity was not reached before this fix.

- Round 27 (partial, `1459bc276`): drift, integration and test-fast failed on first-run-sprite's D88 record and dialog (R21, fixed). After chord-js-equiv-100, contracts fails only on R20 (four partial durable hot-path files).

- Round 26 done on `ff7110dc1`: all 15 shards green except contracts, which fails only on R14b (`json.test.ts` copyJson designed-out case). test-cli green in five shards. Review fixes for chord-js-equiv and win-agentdir-lifetime folded on top; affected packages pass.

- Round 24 done on `da4ec7817`: all 15 shards green except contracts, which fails only on R14a (owner approval of four chord tracker designedOutCases). test-fast green (R19 did not recur). The gate is blocked only on R14a.

- Round 23 done on `4746d8fec`: all shards green except contracts (R14a, owner) and test-fast (R19, one intermittent mcp.json read race that win-native-100 fixes). Parity, test-cli, test-subprocess, test-conformance and race green; C22 and R17 did not recur.

- Round 22 on `b4bd76dc6` stopped after its first wave for the chord-guide-watch-race merge: contracts R14a; test-fast `TestChordUsageGuide` job-output watch (C22, fixed by that merge).

- Round 21 done on `5e5d3ea0e` (the tip `16118d1e5` adds only notes): all 15 shards green except contracts, which fails only on R14a (four chord tracker designedOutCases awaiting owner `SCRUTINIZED:approved`). test-fast is green, so the TMPDIR leak fix holds; R17 and R12 did not recur. The gate is blocked on the owner's R14a decision.

- Round 19 done on `0e5a4c968`: every shard green except contracts (R14a, owner question) and test-fast (TMPDIR leak, fixed in `d15a32f46`). Parity green: R8 and R18 closed. Round 20 stopped for the header-truecolor merge. Gate rounds now run in their own process group (setsid) and are stopped only by that group id.

- Round 19: test-fast failed only on assert-clean-tmp: `durable/harness` `TestCodingTools` left the bash tool's spilled full output (`tmp-*/pi-output-*.log`) in TMPDIR. Fixed in this branch (the case removes the directory its diagnostic names). Contracts: only R14a.

- Round 18 stopped after the first six fast shards (all green) for the win-ext-job and d-harness-b follow-up merges; round 19 follows.

- Round 17 stopped for the merge batch (first wave matched round 16: contracts R14a, test-fast R17). Round 18 checks R8 (clone-after-first-turn-100) and R18 (custom-header-spacing-100).

- Round 16 done on `f2ad56a84`: every shard green except contracts (R14a), test-fast (R17) and parity (R18). test-cli passes in five shards, so R15 is closed. After the d-harness-b merge the test inventory has 0 pending files, so R3 is closed. `make generate` stops only at R14a; `interface-go-drift` is clean and pig-go.json carries the sprite API.

- Round 16 on `f2ad56a84` (first wave): drift, build, closure, sdk, startup, integration, extensions, race green. contracts: only R14a. test-fast: only R17 (flaky TestHarnessClose).

- After the xd-harness-retire batch: `divergence-guard` is green (R1 closed with `agent/harness`); durable lint is clean (R11 closed); `make coverage`, format-version inventory, divergence consistency, hygiene and claims pass. Open before the next round: R3, R12, R14a, R15 (cmdpig-timeout-100 now merged).

- Round 1 (`23d124298`): first full run; R1-R6 and the fixed items above found.
- Round 2 (`b7989d160`, after login-fixes): login, OAuth page and OAuth parity scenarios green; R1-R6 remain.
- Round 3 (`e43eb9e71`, after win-ga and win-cli-modes): drift, contracts and test-fast show only R1-R3; parity: R4, R6 and R8 (R5 and 15-grammar fixed).
- After the sg-durable-tools merge: C4 (`make generate`) regenerated; C6: the 13 durable version fields are classified upstream in format-versions.toml and pig-go.json is regenerated, so R9 is closed. R10 is closed by rev-sg-durable-tools (`durable/env/node`). TestHarnessClose passes again; R13 remains for `TestToolProgressAndLifetime`.
- Round 10 (`1b5664e09`): parity, test-cli, test-subprocess, test-conformance, race, build and the other shards are green. Left: drift (R1 divergence-guard), contracts (R3 test-porting-release and known-gaps-drift, R9 format-version-inventory) and test-fast (R1: 8 harness and experimental tests), all owned by the durable lanes. R8 did not recur.
- Round 8 (`f1319d2aa`): R1, R3, R8 and the new R9 only.
- Round 7 (`1f9214a49`): R2, R4, R6 and R7 closed by their lanes. Left: R1 and R3 (durable lanes), R8 (clone-after-first-turn-100; recurred).
- p1-2860 fit (`9d03cb285`): testbudget for its pig runs, the cargo build in the reviewed build allowlist, the stale Node getModelInfo capability gap removed.
- Round 5 (`c6a784e6a`): R1, R2, R3, R4 and R6 only; R8 did not recur. Then the owner reversed D-H: the 27 durable rows are pending again (`c8ecd0f41`), and p1-2860 closed the 2860 row.
- Round 4 (`dc641335a`): no new causes except R8 repeating and a `go fix` finding (fixed); R5 and R3 fixes hold.
- `dc641335a`: durable rows designed out, server conformance ported; `make test-porting-release` reports only the 2860 row.
