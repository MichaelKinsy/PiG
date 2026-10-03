# port-99-docs-sweep: pin-move item 4 (docs sweep)

Branch `port-99-docs-sweep`, from `porter/pin-move` (6e8986038). `python3 automation/ci/check-public-claims.py` passes (`no contradicted claims (upstream 0.99.1; ...)`), and `go test ./test/docs-drift/ ./internal/pigdocs/` and `make source-hygiene divergence-consistency divergence-quality` pass. The check reported 176 lines, not 147 (the count moved while the other lanes merged).

## Method

- Real upstream 0.99.1 and 0.87.1 were installed from the sdk-ts lockfile (integrity `sha512-cWUrTOqA…` matches `extensions/sdk-ts/package-lock.json`) under `/tmp/sw` with `HOME`, `PI_CODING_AGENT_DIR`, `PI_SKIP_VERSION_CHECK=1` and `PI_OFFLINE=1` isolated (rule 17). No `pig` binary was run.
- Every `path:lines` citation in an edited statement was mapped from `.upstream/v0.87.1` to `.upstream/v0.99.1` with a line-diff mapper. A citation was renumbered only when the mapped range was identical; a range with changed or inserted lines was re-read by hand.
- Behavior probes ran against real 0.99.1 (and 0.87.1 for comparison) where the statement is a runtime fact.
- The two statements about PiG that were wrong were checked against the Go source.

## The checker (`automation/ci/check-public-claims.py`, `automation/ci/version-records.toml`)

A blind rename of about 120 lines would claim a release the text was never checked against: the investigation reports, measurements and closure notes quote 0.87.1 paths, line numbers, probes and results. The checker now accepts an earlier release in two kinds of text and nowhere else:

1. The released sections of `CHANGELOG.md` (`## [x.y.z]`), because a release note states what that release was built against. `[Unreleased]` is still checked.
2. The files that `version-records.toml` lists, for the exact release it names. A new file is checked against the pin until someone reviews it into the list. An entry that excuses nothing fails the check, so the list cannot outlive its text. The list holds 57 dated reports (`docs/findings`, `docs/performance`, `docs/release`, `docs/design/rpc33-observation.md`, and the `docs/parity` closure notes), `docs/plan/` (the upgrade plan names both releases), and `docs/extension-sdk-surface.md` (generated; see follow-ups).

`test/docs-drift/public_claims_test.go` gains `TestPublicClaimsCheckAcceptsRecordsOfAnEarlierRelease` and `TestPublicClaimsCheckRejectsUnreviewedOrStaleRecords`. Mutation check: making the released-section skip unconditional fails the first test (`CHANGELOG.md:9`, `:13`); dropping the record's version match fails the second. Both restored, both pass.

Audit of the listed records (which cited upstream ranges changed under them, so the owning lane knows what to re-read, not rename): 51 of the 60 files under `docs/parity`, `docs/findings`, `docs/performance` and `docs/design` that carry a 0.87.1 mention cite upstream lines. 33 of those 51 cite at least one range that changed or had lines inserted in 0.99.1, could not be resolved from a bare file name such as `bash.ts`, or lies outside the mirror (a dependency or example path). The changed ones are mostly `agent-session.ts` (before_agent_start and prompt admission), `rpc-mode.ts` (the prompt response now carries `disposition`), `theme.ts`, `package-manager.ts`, `model-resolver.ts` and `agent-loop.ts`. That is why they stay records. The raw output is not committed.

## Statements that were wrong or stale (fixed, not renamed)

| File:line | Old statement | 0.99.1 evidence |
|---|---|---|
| `docs/site/docs/compaction.md:109,114` | "PiG does not emit `session_compact_failed`" | `coding/session.go:2330,2779,2922,2957` and `coding/session_compaction_extensions.go:122` emit it; upstream `agent-session.ts:1012-1015`, `extensions/types.ts:770-782`. Added a table row with the event's fields; removed the false sentence. |
| `docs/site/docs/extensions.md:197` | (absent) | New paragraph for the same event; `internal/pigdocs/content/events.md:47` gains the `OnEvent("session_compact_failed", …)` row (`extensions/sdk/events.go:21`). |
| `docs/site/docs/session-format.md:7` | "PiG does not apply `context_edit` entries yet" | `internal/codingagent/session_manager_projection.go:113,198-215` builds `projectContextEntry` like upstream `session-manager.ts:519-540,543-575`; `coding/session.go:2794,2852` and `session_boundaries.go:231` use it. |
| `docs/parity/DIVERGENCES.md:988,990` (D70) | "`.ts` is re-evaluated on a session replacement, so a `.ts` module-scope counter behaves the same" | Probe: two `ctx.reload()` calls log `.ts` evaluated 3x, `.mjs` 1x; two RPC `new_session` calls log `.ts` 1x, `.mjs` 1x (factories 3x each). Same counts on 0.87.1 and 0.99.1 (`loader.ts:128-148` module-level factory cache; `resource-loader.ts:509` clears it only on a reload of a loaded loader). The old text was wrong for both releases. The observable effect now says module state resets on replacement for `.ts` too. **Owner note: D70 is `SCRUTINIZED:approved`; the scope is unchanged but the described gap is larger for `.ts`.** |
| `docs/extension-api-parity.md:277` | new tool "activated" on registration | 0.99.1 activates only when exposure is `direct`/`model-only` and `defaultActive !== false` (`agent-session.ts:3410-3519`, `_isActivatedOnRegistration` 3516-3519). PiG's `extension/tool.go:148-153` has the field; the session path is family 6E's to confirm. |
| `docs/extension-api-parity.md:471` | theme selection "disables automatic updates … dark fallback" | 0.99.1 `theme-controller.ts:124-131` enables auto-sync for the `system` theme; `theme.ts:772-793` falls back to `SYSTEM_THEME_NAME`, not `dark`. |
| `docs/site/docs/message-types.md:3,116,144` | field names match Pi | 0.99.1 adds assistant `thinkingLevel` (`ai/types.ts:556-557`; PiG has it, `ai/messages.go:135`) and tool-result `nestedCalls` (`ai/types.ts:603-604`; PiG has no message field). Added the `thinkingLevel` row and named the `nestedCalls` exception. |
| `docs/site/docs/providers.md:11`, `internal/pigdocs/content/providers.md:9` | "same built-in provider set" | Provider ids equal upstream's minus `typesafe` (`providers/typesafe.ts`, classifier only). Chat wire columns equal the per-provider `api` sets in `providers/data/*.json`. Classifier APIs added to `cloudflare-workers-ai`, `opencode`, `openrouter`, `vercel-ai-gateway`. Reworded to "same chat providers" and named the classifier gap. |
| `docs/extension-api-parity.md:9` | "Pi 0.87.1 added extension events and registration methods" | Reworded to the unported declarations of the pinned API. |
| `docs/parity/DIVERGENCES.md:1008` (D73) | "their Pi 0.87.1 packages export" | The shipped shim is the vendored 0.87.1 dist (`shims/pi-dist`, blocked at 0.99.1, see f6f-ts report), so "0.99.1" would be false. Now says "the Pi release vendored in `shims/pi-dist`". |
| `README.md:103`, `docs/project/PROVENANCE.md:18-19` | pin release and commit (not flagged by the check) | `pigversion.go`: 0.99.1, `d86654abb8862e201933517d6f1fce9f88dd117f`. |

## Every other changed line (statement checked, number and citations moved)

| File:line | Evidence at 0.99.1 |
|---|---|
| `README.md:21` badge, `:28`, `docs/site/docs/index.md:79` | The pin (`internal/coding/pigversion/pigversion.go`); tag `v0.99.1` exists in the mirror. |
| `README.md:121`, `docs/project/RELEASING.md:157`, DIVERGENCES D63 (`:876,880`) and the user-agent example (`:925`) | Format examples (not flagged): `0.3.0+0.99.1` is `coding.Version` at this pin (`pigversion.go:7,10,17`). |
| `docs/site/docs/how-pig-works.md:3` | `how-pi-works.md` identical in both releases; `agent-loop.ts` has no steering/follow-up change; `keybindings.md` differs only in the paste-image label. |
| `docs/site/docs/configuration.md:68`, `internal/pigdocs/content/config.md:72` | `auth-storage.ts` byte-identical; session `CURRENT_SESSION_VERSION = 3` (`session-manager.ts:41`); settings changes are additive keys (`deviceId`, `codemode`, `fullscreenWheelScrollLines`), and PiG keeps unknown keys; Pi still does not write `models.json` (`model-runtime.ts:218`, `model-config.ts`). |
| `docs/site/docs/extensions.md:362` | `ContextUsage.tokens: number \| null` and `getContextUsage(): ContextUsage \| undefined` (`extensions/types.ts:306-312,360`; `agent-session.ts:4139-4160`). |
| `docs/site/docs/message-types.md:116` | No `replace` field on `SystemMessage` (`ai/types.ts:496-539`); upstream `docs/message-types.md:103,108` still lists one and is unchanged. |
| `docs/site/docs/providers.md:92`, `internal/pigdocs/content/providers.md:92`, DIVERGENCES D80 (`:1118`) | `login-dialog.ts` byte-identical; `showAuthPrompt` (`interactive-mode.ts:6124`) still passes `prompt.placeholder` to a plain `Input`. |
| `docs/site/docs/rpc.md:74` | Probe on 0.99.1: after RPC `new_session` the extension logs `session_start startup; session_shutdown new; session_start new; session_start new; session_shutdown quit`. `rpc-mode.ts:439,606,614,626` call `rebindSession()` after the four commands. |
| `internal/pigdocs/content/divergences.md:67`, DIVERGENCES D54 (`:30`) | `markdown.ts:303-330` still wraps each non-image line with `wrapTextWithAnsi`; the release only adds a token cache. |
| DIVERGENCES D35 (`:20`) | `armin.ts`, `earendil-announcement.ts` identical. |
| DIVERGENCES D50 (`:22`) | `mermaid.ts:76-77` identical. |
| DIVERGENCES D27 (`:165`) | Probe: `findWordBackward("ー你好", 3)` in a fresh Node process returns 0, the second call 1, on 0.99.1 (`tui/dist/word-navigation.js`). |
| DIVERGENCES D51 (`:534,554`) | Probe under a pty: SIGINT kills real 0.99.1 by signal 2 and leaves the pty in raw mode (`ICANON`, `ECHO`, `ISIG` off). `interactive-mode.ts` has no SIGINT handler except the suspend listener (`:4318-4339`). SIGTERM/SIGHUP cites moved to `:4174-4186,4260-4278` (identical). |
| DIVERGENCES D64 (`:893,897`) | `pi.dev` endpoints still at `interactive-mode.ts:1321`, `package-manager-cli.ts:50`, `config.ts:543`; `radius-config.ts:4`; `session-share.ts` byte-identical (Radius, then `gh gist`); telemetry cite `:1292-1307` → `:1312-1327`; the experimental Radius cites (`server.ts:637-677`, `commands.ts:14-29`, `client-runtime.ts`) are identical. |
| DIVERGENCES `:1031`, `:1065` | `registerSessionResourceCleanup` is exported by `ai/src/session-resources.ts` in both releases, so the 0.87.1 audit sentence is kept as "the release the audit read"; the e2e comparator runs against the pinned Pi. |
| DIVERGENCES D79 (`:1099,1101`) | `getLatestNpmVersion` still runs `npm view` with `cwd: this.cwd` (`package-manager.ts:1545-1564`). Cites renumbered: `1150-1164`→`1181-1195`, `1481-1500`→`1515-1534`, `1511-1530`→`1545-1564`, `2613-2633`→`2674-2694`, `package-manager-cli.ts` `928-944`→`932-948`. |
| `docs/parity/PORT_MAP.md:497` | `model-search.ts` and `tui/src/fuzzy.ts` byte-identical. |
| `docs/additive-features.md:39` | `package-manager-cli.ts:375-385` and `cli/args.ts:253-254` identical; Package verbs unchanged. The `cli-utils/04` probe sentence is historical, now "Pi, then pinned at 0.87.1". |
| `docs/additive-features.md:807` | `utils/tools-manager.ts` and the installed `dist/utils/tools-manager.js` are byte-identical in both releases, so the 0.87.1 probe result holds. |
| `docs/extension-api-parity.md` cites (lines 140,142,144,167,171,184,227,248-250,259,263,289,308,340,341,365,369,379,395,423,446,448,477) | Each range mapped and identical except those listed above; renumbered. `:167`→`agent-session.ts:1976-1988,2020` (the code moved out of a `try`); `:227` prompt case `rpc-mode.ts:394-412` (response now `{disposition}`); `:369`→`sdk.ts:175-461`; `:326`→`agent-session.ts:643-684` (`_afterToolCall`); `:446` cites were past the end of both files, corrected to `extension-selector.ts:96-113`, `extension-input.ts:80-89`. |
| `docs/extension-api-parity.md:250` | Oracle `ai/testdata/credential-expiry.json` regenerated from real 0.99.1 with `test/parity/probes/credential-expiry.mjs`: byte-identical (`cmp`). |
| `docs/extension-api-parity.md:249,473,568`, `:259,:263` | 249 now "the pinned Pi's ModelRegistry" (the test runs the installed Pi); 473: the probe asserts 0.87.1 and needs the Node runtime shim, so it is described as such; 568: historical scenario run, "then pinned at 0.87.1"; 259 `createExtensionRuntime` differs only by added registries (`loader.ts:156-…`), subscription tracking unchanged; 263 `markdown-transform.ts:18-29` identical. |
| `CHANGELOG.md:15` (Unreleased) | `config-selector.ts:487-493` → `:493-499`, `cli/config-selector.ts:41-49` identical. Released sections are left alone. |
| `changelog.d/x-perf-node-terminal-capabilities.md:3` | `getCapabilities` `terminal-image.ts:161-170` (was 160-169; lines inserted below it), `main.ts:864,898`. |
| `test/parity/sdk-surface-exceptions.toml:81` | `usesCallbackServer` is still `@deprecated … canonical auth flows ignore it` (`extensions/types.ts:1916-1917`) and read nowhere else. |

## Follow-ups for the integrator (outside this item)

1. `docs/extension-sdk-surface.md` and `docs/parity/KNOWN-GAPS-0.3.x.md` (generated block) are generated. `go run ./test/parity/cmd/sdksurface` needs `test/parity/interfaces/upstream-v0.99.1.json`, so it waits for your step 5. After regeneration the sdk-surface entry in `version-records.toml` fails as stale: delete it. `test/parity/known-gaps.toml` still has `pi_version = "0.87.1"`.
2. Pin statements the check does not scan and this item did not touch: `NOTICE`, `THIRD_PARTY_NOTICES.md` and `REUSE.toml` (vendored 0.87.1 dependency notices; correct until `vendor-pi-dist.sh` runs at 0.99.1), `.github/badges/parity-coverage.svg` (generated), `.github/ISSUE_TEMPLATE/parity.yml`, `automation/gen/pending-test-batches.py`, `automation/gen/test-ported-baseline.py`, `automation/make/parity.mk`, `docs/project/QUICKSTART.md` and `docs/media/quickstart/quickstart.ascii` (recorded 0.2.0 captures), `docs/site/docs/evals.md` (measured run), `plans/0.3.x/*`.
3. When family 3 (ChatGPT OAuth on `openai`, `openai-codex` renamed "OpenAI Codex (legacy)"), 6B (typesafe provider and classifier models) and the tool-nesting work land, revisit: the `typesafe`/classifier sentence in both `providers.md` files, the `nestedCalls` exception in `message-types.md`, and the tool-activation sentence at `extension-api-parity.md:277`.
4. New 0.99.1 hosted endpoint: `remote-catalog-provider.ts:13` (`https://pi.dev` catalog overlay). D64 lists the other `pi.dev` endpoints; PiG has no catalog fetch yet, so 6B needs a D64 decision.
5. `docs/additive-features.md:217` says stock `pig` keeps one Host across a replacement (D30, D61), while D70 says it starts a new host. This item only corrected D70's `.ts` claim; the two entries still need reconciling.
6. The pin-move note about "docs mirrors for the new upstream docs" (`docs/mcp.md`, `docs/virtual-models.md`, 19 changed docs) has no mirror to update: `internal/pigdocs` and `docs/site/docs` are PiG-written, and no gate compares them with upstream docs. The MCP and virtual-model pages belong to the lanes that port those features.

### Loop self-report

Family: docs (pin-move item 4)

Bugs found and fixed at the source (count: 3):
  - compaction page said PiG lacks `session_compact_failed` → documented the event PiG emits @ docs/site/docs/compaction.md:109
  - session-format page said PiG ignores `context_edit` → documented that PiG applies it @ docs/site/docs/session-format.md:7
  - D70 claimed `.ts` modules are re-evaluated on replacement → measured on both releases and corrected @ docs/parity/DIVERGENCES.md:988

Comparators tightened (count: 0)

Divergences numbered in docs/parity/DIVERGENCES.md (count: 0)

Lint suppressions added or changed (count: 0)

New file coverage (count: 0)

Band-aids consciously chosen (count: 1, should be 0):
  - `version-records.toml` entry for `docs/extension-sdk-surface.md`: generated file cannot be regenerated before the 0.99.1 interface ledger exists: the entry fails as stale after regeneration and forces its removal: follow-up 1.

Scope expansion (count: 3 files outside the named list):
  - `automation/ci/check-public-claims.py`, `automation/ci/version-records.toml`, `test/docs-drift/public_claims_test.go`: "leave historical facts" needs a checker rule: callers are `TestPublicClaimsMatchTheEvidence` and CI.
  - `docs/project/PROVENANCE.md`, `RELEASING.md`, `changelog.d/x-perf-node-terminal-capabilities.md`, `test/parity/sdk-surface-exceptions.toml`: same statements, unflagged.

Smells investigated (count: 3):
  - the check reported 176 lines, not 147 → the count moved with the merged lanes; all 176 are accounted for.
  - two `extension-api-parity.md` citations lay past the end of their files → corrected (see table).
  - every 0.99.1 probe of an existing 0.87.1 statement matched except D70 → re-probed on both releases; the old text was wrong at both.
