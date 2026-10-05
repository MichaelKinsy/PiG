# DIVERGENCES

Only user visible or interop relevant differences from upstream belong here.

Do not log:

- mechanical TS to Go translation
- test harness differences
- multi language SDK surface differences over the same wire protocol

Every active divergence must have:

- id `D<N>`
- `// pig divergence (D<N>): ...` at each call site
- parity coverage or an explicit parity allowance
- `SCRUTINIZED:approved`

## Retired divergences

- D35 — Hidden `/arminsayshi` and `/dementedelves` commands. Retired by the Pi component ports (`armin.ts` and `earendil-announcement.ts` are identical in upstream 0.87.1 through 1.0.0). The commands, their recognition and `/dementedelves`'s announcement remain Pi's. Since 2026-10-01, D87 replaces the art and label `/arminsayshi` shows with PiG's pig head and `pigsayhi`, keeps Pi's effects, and adds `/pigsayhi`. `TestArminFramesMatchPinnedPi` runs the pinned `armin.ts` with PiG's image and compares every frame of all seven effects, and `TestEarendilAnnouncementMatchesPinnedPi` compares styled announcement rows. The `slash-commands/12-earendil-announcement` scenario compares exact terminal output; `13-armin-bitmap` asserts each binary's settled image. The ID remains reserved.
- D77 — Explicit Node isolation prevented sharing `pi.events`. Retired by owner decision Q1 = B (2026-09-29). Every Node process is one cross-process reference realm, and every realm shares the one bus: packed cells, crash-recovery groups, quarantined members, strict isolation and exact standalones. Payloads cross by reference, so listeners in other processes see the emitter's original object; D83 records the remaining cross-realm scheduling and lifetime boundaries. Process, crash and memory isolation remain; a hung foreign listener blocks its emitter, as a hung listener blocks Pi. Tests: `TestXrefEventBusForeignPrefixMutationMatchesPi`, `TestXrefEventBusMatchesPiAcrossRealms`, `TestXrefEventBusListenerOrderMatchesPi` and `TestXrefEventBusReentrantDispatchMatchesPi` run strict isolation, packed and mixed topologies against Pi's own `createEventBus`. The ID remains reserved.
- D59 — Generic extension tool cards exposed complete recoverable details. Retired by the upstream 0.99.1 port: `formatToolCallWithArgs` (`render-utils.ts:71-96`) now draws the fallback call header of a registered tool without `renderCall`, as `key=value` pairs cut at 100 characters while collapsed and one `key: value` line per argument when expanded (`tool-execution.ts:155-157`). PiG uses that function and the definition card path for every registered tool; the structured-argument card, its width-aware preview, the extra Ctrl+O scrollback rebuild and the "Toggle tool details" key description are removed, so Ctrl+O is "Toggle tool output" again. Tests: `TestFormatToolCallWithArgs`, `TestToolExecutionComponentUpstream/shows_arguments_in_the_fallback_call_header` and `TestRegisteredToolWithoutRenderersDrawsTheFallbackHeaderThroughTheEventPath`. The ID remains reserved.
- D50 — Extra unsupported/oversized Mermaid hints. Retired under the lead's parity-completion authority. upstream 0.99.1 `mermaid.ts:76-77` (unchanged since 0.87.1) preserves the raw code block; partial-parse warnings remain display-only. `TestMermaidUnsupportedAndOversizedRemainSource`, `TestMermaidFallbackAssistantBlockMatchesDisabledTransform`, and the now byte-exact `TestMermaidTransformMatchesUpstream` verify the contract. The ID remains reserved.
- D58 — Automatic Mermaid label narrowing. Retired under the same authority. The transformer uses the natural layout and preserves source when it is too wide. The caller-free fitting search and its private bookkeeping are removed. The oversized transformer/caller cases retain the wide diagram input, and the natural engine corpus remains unchanged. The ID remains reserved.

- D47 — Width stripping consumes DEC private-mode set/reset sequences. Retired by the width-parity change. `tui/widthx.ExtractAnsi` now delegates to the upstream-compatible `ExtractAnsiCode`; the ID remains reserved. `TestExtractAnsi_PrivateModeMatchesUpstream` and `TestPiWidthDifferential` verify the shared ANSI parsing behavior. No active divergence or source marker remains.
- D71 — Nonfatal main-screen overflow recovery. Withdrawn on 2026-09-25 by owner decision; the ID remains reserved. PiG now matches upstream `tui-main-screen.ts`: an over-wide non-image row that reaches the differential-render loop writes the TUI crash log (`pi-tui-crash.log` in the configured agent directory, or the OS temp directory when none is configured), stops the TUI, and ends the process through the uncaught-exception path with status 1. Initial, full, and resize renders emit the row unchanged. Tests: `tui/render_overflow_test.go` and parity scenario `extensions-runtime/20-differential-render-overflow-terminates.toml`. No active divergence or source marker remains.

- D76 — Untransformed Markdown before an extension reply. Retired by the off-loop transform-generation path. The complete ordered chain finishes before new Markdown content is painted. Each component retains at most one active and one replaceable pending generation; cancellation and stale-result rejection keep replacements from publishing late. Tests: `TestMarkdownTransformFirstPaintWaitsForWholeChain`, `TestAsyncMarkdownReplacesPendingGeneration`, `TestAsyncMarkdownOwnerCancellationDrains`, and `extensions-runtime/33-markdown-transformer`. D56 still governs failed or stalled subprocess rendering.

- D54 — Fenced-code wrapping. Retired after re-probing Pi: `Markdown.render` already wraps every non-image rendered row, including code rows (`markdown.ts` at 0.99.1 still passes each non-image line through `wrapTextWithAnsi`; the only change since 0.87.1 is a token cache). PiG now uses that same final content-width pass and its continuation breakpoints. The ID remains reserved. Evidence: `tui/markdown_upstream_test.go`, `tui/markdown_codeblock_wrap_test.go`, and `test/parity/scenarios/tui-components/16-markdown-user-components.toml`.

## Active divergences (36)

D78, D82 and D83 record owner-approved known gaps for 0.3.x (decision 2026-09-28). Approval records a difference; it does not prove parity, waive an unrelated defect, or turn a failing comparison into a pass. Same-process object behavior must remain Pi-exact. See `docs/findings/0.3.0-known-gaps.md` for the integration boundary and retained failures.

## D2 PiG uses a separate command and configuration identity

What: the command, startup banner, and terminal title use `pig`. Configuration defaults to `~/.pig` and project `.pig`. A binary resolved under the name `pi` exits with status 2 instead of shadowing Pi.

The exact environment value `PIG_USE_PI_DIRS=1` explicitly selects Pi's agent and project directories. Shared mode uses `PI_CODING_AGENT_DIR` or `~/.pi/agent`, project `<cwd>/.pi`, and `PI_CODING_AGENT_SESSION_DIR` rather than the two `PIG_CODING_AGENT_*` overrides. `--session-dir` and the `sessionDir` setting keep Pi's precedence. Project trust, Package/resource discovery, system prompt files, sessions, and imported Node configuration helpers all use the selected directories. PiG-owned SDK caches, docs and user Piglet state keep the PiG configuration root. No files are copied, no trees are merged, and no setting inside a relocated directory controls the selection.

Owner decision: 2026-09-27, release 0.3.0. Pi's config.ts:528-573 selects its directory identity before loading settings; a process-level opt-in avoids circular settings resolution. `PIG_CODING_AGENT_DIR=~/.pi/agent` alone remains an agent-only relocation and does not select project `.pi` or configure external adapters.

Shared auth/settings/trust and dynamic catalog cache writes use Pi's lock-directory protocol. Auth JSON preserves provider fields and required empty OAuth fields; existing auth modes and ACLs remain intact. PiG reads but does not write models.json. Session JSONL is interoperable, but simultaneous edits of one session are not supported by either host. pi-acp owns `~/.pi/pi-acp/session-map.json`; PiG does not rewrite it. Use one adapter process per home and Pi-compatible resources in shared settings. This opt-in does not make Piglets or native PiG extensions loadable by Pi.

Evidence: `TestPiDirectoriesOptIn`, `TestPiDirectoriesProjectSettingsAndSessions`, `TestPiDirectoriesSessionOverridePrecedence`, `TestPiSharedFilesRoundTrip`, `TestPiSharedFilesConcurrentWriters`, and `settings/10-pi-directories-opt-in`. The real Powerline and pi-acp replay and file-lock limits are recorded in `docs/findings/pi-directories-opt-in.md`.

Why: separate command and configuration identities prevent accidental writes to
Pi state.

Remove when: never.

Call-site markers: `cmd/pig/guard.go`, `cmd/pig/main.go`, `internal/codingagent/paths.go`, `internal/codingagent/startup_header.go`, `internal/codingagent/ext_ui_context.go`, `coding/piglogin/doc.go`, `coding/piglogin/registry.go`, `coding/extension/host/subprocess/ui_bridge.go`, `coding/extension/builtin/builtin.go`, `cmd/pig/project_trust.go`, `cmd/pig/extension_set.go`, and `internal/codingagent/project_trust_warning.go`, `internal/evals/harness.go`.

Every message that tells the user to run a command names `pig`, never `pi`: the replaced built-in extension warning says `pig config` where Pi's resource-loader.ts says `pi config`, and the startup hint says `pig -ne`. `TestProductionStringsNeverInstructAPiCommand` rejects a production string literal that tells the user to run `pi`.

The trust prompt and warning name the selected project directory and the `pig` command. Their wording, styling, and conditions otherwise match Pi. `test/parity/scenarios/project-trust/09-cancel-trust-shows-warning-with-extensions-off.toml` and `10-startup-trust-prompt-wording.toml` compare these surfaces with only the D2 identity substitution.

The startup header shows PiG's pig head in Pi's logo slot, 7 rows instead of 2, so PiG never presents itself as Pi; the hints, help and resource listing otherwise match Pi. Owner approved 2026-10-01; after trying a smaller 12-by-8 head live, the owner chose the sprite's 16-by-14 pig at its original size, with the version and hint lines beside it.

The head is the sprite's 16-by-14 pig, drawn in half blocks as 16 cells by 7 lines: the standard pig recolored for the color sprites, and each character's original art. `coding/piglogin/testdata/head-*.golden` pins each one. Pi prefixes its 2-line logo to the first two lines of the header text and renders all of it as one Text (interactive-mode.ts:998-1006 in the v1.0.0 upstream mirror); PiG prefixes the head's 7 lines to the first seven lines in the same way: the version beside the first, then in compact mode the first hint line, the `Press` line, the blank line and the onboarding line, or in expanded mode the first six hints. A line beside the head wraps in the cells right of the head; the rest wraps at Pi's full width. The compact header is therefore 2 lines taller than Pi's, the expanded header has Pi's 22 lines, and a line beside the head wraps in `width-19` cells where Pi's wraps in `width-7`.

The head is drawn only when it fits with the version beside it (31 columns with a 12-character version), the terminal aligns half blocks, and the theme's color mode is truecolor. Otherwise, in 256-color terminals and narrow panes, the one-line text mark `PiG.` takes Pi's logo slot: it is 4 cells wide like Pi's logo, its line carries the version and the slot's second line the first line of key hints (interactive-mode.ts:1002-1006), so the hints wrap where Pi's wrap. In Apple Terminal, where Pi draws its text wordmark and the hints below it (`piWordmark`), PiG draws the same layout with the text mark. "PiG" is bold, and as Pi colors its "Pi" wordmark, each letter takes a mid or dark stop of the sprite's wordmark ramp (rows 8 to 10, at least 3:1 against both white and black, falling back to the period color where a ramp is too pale, as the default sprite's is); the period is in the sprite's wordmark period color. The 256- and 16-color fallbacks apply, as in Apple Terminal. Only the composite version (D63) and the onboarding product name otherwise use PiG's identity.

The built-in header is the one `setHeader(undefined)` and `/reload` restore, so Pi's logo never appears. An extension replaces it with `ctx.ui.setHeader` as in Pi, and an extension or Piglet replaces it with its own native login through `ctx.ui.setLogin` (D60). The built-in `pig-login` extension (`coding/piglogin`) registers `/sprite`, which chooses one of fifteen built-in sprites (`pig-default`, then the colors `pink`, `green`, `mint`, `sandy`, `grey`, `blush`, `lavender`, `cloud`, then the characters `pigrogu`, `darth-vader`, `kratos`, `piglet`, `spider-ham`, `sheriff`) and saves the choice in `$PIG_HOME/state/pig-standard/login.json`. `/sprite preview [id]` shows a sprite's full art, its `PiG.` wordmark and pig as the native login template draws them, with its name and tagline, in an overlay that any key closes. Pi has no `/sprite`. Clicking the head, or the text mark where it stands in for Pi's logo, plays PiG's version of Pi's logo animation (D87).

An extension or Piglet adds sprites with `ctx.ui.registerSprite` (wire call `ui.registerSprite`; Go `RegisterSprite`, Rust `register_sprite`, Python `register_sprite`, Node and TypeScript `registerSprite`). A `SpriteDefinition` has a lowercase slug `id` of at most 32 characters, a one-line `name` and `tagline`, a 16-by-14 `mascot` of palette symbols with `.` transparent, which the header draws as the head and the preview beside the wordmark, and a `palette` of `#RRGGBB` colors; unknown fields fail. The host validates the definition (`invalid_sprite`), rejects a built-in id or another extension's id (`ui_error`), and replaces the sprite when the same extension registers the id again. Registered sprites follow the built-in ones in `/sprite list` and the picker and are saved like them. The host replays them to each new UI and drops them when their extension goes away. While the saved sprite is not registered, the header draws `pig-default` and the saved choice stays. Pi has no sprites, so this is part of the same identity exception, approved with it (owner, 2026-10-01).

Locked by: `test/parity/scenarios/startup/00-startup-banner.toml` (the head's lines with the version, the first hints and the `Press` line), `02-startup-compact-help.toml`, `03-startup-expanded-help.toml`, `04-startup-verbose-collapse.toml` and `14-startup-quiet-header.toml` (every hint byte for byte with Pi, with the head's 16 cells removed), `13-startup-narrow-mark-wrapping.toml` (26 columns, the text mark, byte-equal with Pi from the first hint on), `coding/piglogin` (the head and art goldens, the text mark, the registry, `/sprite list`, `set`, `preview`), `internal/codingagent/pi_logo_upstream_test.go` (the head against the goldens, the layout, the head threshold, the text mark in narrow, 256-color and Apple terminals, replacement by `setHeader` and by a Piglet's `setLogin`, `/reload`), `coding/extension/host/subprocess/ui_bridge_sprite_test.go`, the `sprite-probe` row of `test/extension-conformance`, `cmd/pig/extension_sprite_pty_test.go` (a Go, Python and Rust extension's sprite through list, set, preview and restarts with and without the extension, and a click on the head in the default fullscreen mode, in the real binary),
`tui/terminal_test.go` (`TestBuildTerminalTitle_NoName`), and the command
identity tests in `cmd/pig`.
SCRUTINIZED:approved

## D14 tools-manager archive extraction uses Go stdlib instead of spawning tar/unzip/PowerShell

Upstream `tools-manager.ts` (`extractTarGzArchive`, `extractZipArchive`)
spawns `tar`, `unzip`, and (on Windows) PowerShell `Expand-Archive` to
unpack downloaded fd/rg release assets. Each platform has a fallback chain:
Linux/macOS try `unzip` then `tar`; Windows tries `tar.exe` (bsdtar) then
PowerShell.

pig `internal/codingagent/tools/tools_manager.go` uses Go stdlib
(`archive/tar` + `compress/gzip` + `archive/zip`) on all platforms. The
extracted binary is byte-identical for every fd/rg release asset published
to date; only two observable differences exist:

1. Error message text on extraction failure. Upstream produces e.g.
   `Failed to extract X: unzip: ...; tar: ...`. pig produces e.g.
   `Failed to extract X: <go-error>`. There is no parity scenario for the
   failure path (it requires a malformed archive on the GitHub CDN).

2. Symlink and hardlink handling. Upstream `tar xzf` preserves them on
   Unix. pig silently skips `TypeSymlink`/`TypeLink` entries. fd and rg
   archives contain manpage symlinks (`man/man1/fd.1` → `fd.1`) that
   neither binary actually needs; pig's behavior is therefore safe in
   practice but theoretically observable if a user `ls`-es the install
   dir.

Why this is a divergence (not a bug): the Go stdlib path is more reliable
than spawning system tools: it doesn't depend on `unzip` being installed
(Alpine, minimal Docker, Termux), can't be tricked by a hostile `tar` on
PATH, and produces deterministic error text regardless of locale. The
divergence is structural to the Go port: faithfully calling `os/exec.Command("unzip", ...)` would re-introduce the very brittleness Go's stdlib lets us
avoid.

Remove when: upstream uses an equivalent in-process archive reader, or PiG uses
the same external extraction contract.

Locked by: unit tests in `internal/codingagent/tools/tools_manager_test.go`
(TestDownloadTool_TarGz_NestedBinary, TestDownloadTool_TarGz_FlatBinary,
TestDownloadTool_Zip, TestDownloadTool_DeeplyNestedBinary,
TestExtractZip_PathTraversalRejected, TestExtractTarGz_PathTraversalRejected)
that exercise the full extract pipeline against synthetic fd/rg archives.

PORT_MAP path: `packages/coding-agent/src/utils/tools-manager.ts`
SCRUTINIZED:approved

## D26 PiG never identifies itself as Pi to a service

What: every string upstream sends to identify Pi, and every Pi-owned endpoint it calls, is PiG's own. One file, `internal/coding/pigidentity/identity.json`, holds the values. The Go host reads it (`internal/coding/pigidentity`), and `automation/gen/pi-identity-patches.mjs` applies the same values to the Pi JavaScript the Node extension runtime vendors, so a Node extension that calls the Pi SDK (`createAgentSession`) or pi-ai sends the identity the Go host sends. The vendoring step (`automation/gen/vendor-pi-dist.sh`) runs the patch before it compiles the SDK bundle, so a re-vendor keeps it, and `vendor-manifest.json` labels each patched file. An exact-line patch that no longer matches a new Pi release fails the vendoring step.

Replaced values (audited against upstream 0.87.1; `pi-identity-patches.mjs` applies them to the vendored 0.99.2 dist and fails when a literal is missing):
- Provider attribution (`provider-attribution.ts`, telemetry-gated, gate unchanged): OpenRouter `HTTP-Referer: https://pi.dev` → `https://github.com/MichaelKinsy/PiG`, `X-OpenRouter-Title: pi` → `PiG`, `X-OpenRouter-Categories: cli-agent` unchanged; NVIDIA NIM `X-BILLING-INVOKE-ORIGIN: Pi` → `PiG`; Cloudflare `User-Agent: pi-coding-agent` → `pig-coding-agent`. The OpenCode pair `x-opencode-session` and `x-opencode-client: pi` → `pig` stays independent of the telemetry gate, as upstream's `getSessionHeaders`. Host and provider matching are unchanged. Node: `core/provider-attribution.js` in the vendored SDK, which `createAgentSession` calls.
- User agents: D65 (`pig/<coding.Version> (<platform> <release>; <arch>)`) now also covers the vendored `getPiUserAgent` of coding-agent and pi-ai. The host names its composite version to each Node runtime process in `PIG_PRODUCT_VERSION`.
- OpenAI Codex: `originator: pi` on the SSE request, the websocket handshake and the OAuth authorize URL → `pig`. xAI device-code `referrer: pi` → `pig`.
- Child processes: `AI_AGENT=pi` → `AI_AGENT=pig` (Pi's `cli/setup.ts` and `rpc-entry.ts` export it so tools name the launching agent). `PI_CODING_AGENT=true` is unchanged, because Pi extensions read it to detect the harness.
- Pi-owned endpoints the vendored Node code would call, routed as the Go host does (D62, D64): the version check (`utils/version-check.js`), the remote model catalog overlay (`core/remote-catalog-provider.js`), the install report (`interactive-mode.js`) and the managed installer API (`package-manager-cli.js`) go to `https://pi-in-go.dev`. The bug-report upload (`core/bug-report-upload.js`) refuses, because PiG never uploads bug reports (D62). The Radius share upload (`session-share.js` `tryShareViaRadius`) returns "unavailable", because PiG never sends a Radius token to Radius's share gateway (D64). None of these run in a PiG session today (the Go host implements the interactive commands), so the patches close paths that an extension could reach by importing Pi's `InteractiveMode`, `getLatestPiRelease`, `withRemoteCatalog` or `uploadBugReport`.

Kept as Pi sends them, by design: GitHub Copilot's `GitHubCopilotChat/0.35.0`, `Editor-Version`, `Editor-Plugin-Version` and `Copilot-Integration-Id` headers and Anthropic OAuth's `claude-cli/…` user agent, because those providers accept requests only from the client they name; Radius's `radius.pi.dev` gateway, model catalog and `pi-gateway` OAuth client id, which the user selects by logging in to the `radius` provider (Pi's own product, the same in the Go host); Pi documentation and migration-guide links printed as text and the update notification's changelog link; `pi-messages` (a protocol name). The guard test `TestVendoredRuntimeCarriesNoPiIdentityOutsideTheAllowlist` scans every vendored file and lists each remaining literal with its reason.

Why: every upstream identity value is pi-branded (`pi.dev`, `pi`, `Pi`, `pi-coding-agent`). A rebranded port must not misattribute its traffic to Pi, so PiG substitutes its own branding host-for-host and header-for-header instead of narrowing upstream's provider set, and sends nothing to a service PiG does not operate on the user's behalf. The Node extension runtime ships Pi's own JavaScript, which is a second way out of the process; the owner directive for 0.3.1 is that no path identify as Pi. Only the literal values and the disabled Pi-only uploads change; the provider and host matching, the telemetry gate and the OpenCode session pair are unchanged from upstream.

`IsInstallTelemetryEnabled` mirrors upstream `isInstallTelemetryEnabled` (telemetry.ts:8): `PI_TELEMETRY` env truthiness wins when set, otherwise the `enableInstallTelemetry` setting (default true). This is the same gate that also covers the install/update ping, sent to PiG's own `pi-in-go.dev` endpoint instead of pi.dev (D64; `internal/codingagent/install_telemetry.go`).

Unverified against the live services: OpenAI's Codex backend and login and xAI's device-code endpoint accept an `originator`/`referrer` other than `pi` (Pi's own client and other Codex clients use their own names). No credentials were available to run them.

Remove when: PiG sends byte-identical (unbranded) identity to services, which would require PiG to claim it is Pi. Not planned.

Call-site markers:
- `internal/coding/pigidentity/pigidentity.go`: the values and their single source, `identity.json`.
- `coding/model.go`: the OpenRouter/NVIDIA/Cloudflare branch and the OpenCode session pair in `mergeProviderAttributionHeaders`.
- `ai/openai_responses.go`, `ai/openai_codex_responses.go`, `ai/openai_codex_websocket.go`, `ai/oauth_openai_codex.go`, `ai/oauth_xai.go`: the Codex originator and the xAI referrer. `ai/user_agent.go`: the product name in D65's user agent.
- `cmd/pig/setup_cli.go`: `AI_AGENT`.
- `coding/extension/host/subprocess/terminal_capabilities_env.go`: `PIG_PRODUCT_VERSION` for the Node runtime.
- `automation/gen/pi-identity-patches.mjs` and `coding/extension/host/subprocess/runtime-node/shims/pig-identity.mjs`: the Node runtime patches and the user-agent builder they call.
- `internal/codingagent/settings.go`: `IsInstallTelemetryEnabled`, `isTruthyTelemetryEnvFlag`.

Locked by: `internal/coding/pigidentity` `TestIdentityValuesAreThePiGBrand`, `TestIdentityJSONIsTheSingleSource`; `coding` `TestModelRuntimeRequestsCarryPiGAttributionNeverPis` (Go host, real HTTP), `TestMergeProviderAttributionHeadersMatchesPinnedProvidersAndHosts`, `TestProviderAttributionWrapperReachesHTTPRequest`, `TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting`; `cmd/pig` `TestNodeExtensionModelCallsCarryPiGIdentityNeverPis` (the real binary, a Node extension's `createAgentSession` and pi-ai `completeSimple` against OpenRouter-, NVIDIA- and OpenCode-shaped servers) and `TestSetupCliSetsInheritedProcessMarkers`; `test/extension-conformance` `TestSDKModelCallsCarryPiGIdentityNeverPis` (the Go, Python and Rust SDKs' host model calls); `coding/extension/host/subprocess` `TestVendoredRuntimeCarriesNoPiIdentityOutsideTheAllowlist`, `TestVendoredRuntimeRequestsAreNeverMadeAsPi`, `TestVendoredPiDistMatchesThePinnedPackage` (the patch table), `TestNodeExtensionUserAgentNamesPiGAndItsVersion`; `ai` `TestLoginOpenAICodexAuthorizeURLNamesPiGAsOriginator`, `TestCodexWebSocketHeadersNamePiGAsOriginator` and the Codex and xAI request tests; `internal/codingagent` `TestHostedEndpointsUseTheIdentityOrigin`, `TestSettingsManager_IsInstallTelemetryEnabled`.

PORT_MAP path: `packages/coding-agent/src/core/provider-attribution.ts`, `packages/coding-agent/src/core/telemetry.ts` (setting/env gate ported; the install-report ping it also gates is D64), `packages/ai/src/auth/oauth/openai-codex.ts`, `packages/ai/src/auth/oauth/xai.ts`, `packages/coding-agent/src/cli/setup.ts`.

SCRUTINIZED:approved

## D27 Word segmentation always uses ICU's warm dictionary-engine cache

What: PiG's word segmenter always behaves like an ICU process whose `CjkBreakEngine` is already loaded. In a fresh Pi process that has not yet built a CJK dictionary span, a span starting at U+30FC (`ー`) or U+FF70 (`ｰ`) goes to ICU's `UnhandledEngine` instead. For example, upstream 0.99.2 in a fresh Node process returns 0 for `findWordBackward("ー你好", 3)`. After any earlier Han or Kana span in the same process, or on a later call, it returns 1. PiG always returns 1.

Why: ICU's `ICULanguageBreakFactory::getEngineFor` (`brkeng.cpp`) caches break engines for the whole process, so Pi's result depends on what anything in its Node process, including in-process extensions that call `Intl.Segmenter`, segmented earlier. Emulating that needs process-global mutable segmenter state whose value PiG cannot observe from Pi's process history. PiG models the per-iterator engine stack (`rbbi.cpp`) and uses the stable warm-cache result.

Observable effect: only the first cold-cache segmentation of a dictionary span that starts at U+30FC or U+FF70 in a fresh Pi process. Every warm-process result, and the Thai, Lao, Khmer and Burmese dictionary engines, match Pi.

Scope: this records only the process-history residual. The ICU 78.3 Southeast Asian engines, `PossibleWord` lookahead and resynchronization, dictionary rule spans and per-iterator engine selection are ported and verified; they are not covered by this entry.

Owner decision: 2026-09-29, owner Michael Kinsy approves recording the ICU process-history residual as a narrowed D27 instead of retiring D27 outright, following the rev-d27 review.

Call-site marker: `internal/wordsegmenter/segments.go`: `dictionaryEngines.engineFor`, where a CJK span start selects `CjkBreakEngine` as if the process cache were warm.

Evidence: `internal/wordsegmenter/testdata/sea-icu78.json` (warm-process Node 26.7.0 / ICU 78.3 corpus, including the `engine-selection` cases), `TestICUSoutheastAsianSegments`, `TestICUSoutheastAsianWordNavigation`, and `tui-components/20-editor-word-and-paste-segments` (byte-exact Editor transitions).

Parity allowance: the differential corpus is generated in a warm process. No paired scenario asserts a fresh-process cold-cache result, because Pi's own answer changes with process history.

Remove when: upstream ICU makes engine selection independent of process history, or PiG adopts an owned model of Pi's process-wide engine cache.

SCRUTINIZED:approved

## D30 Replaced Session's extension processes are not fully invalidated

What: upstream's `AgentSession.dispose()` (agent-session.ts:717) calls
`_extensionRunner.invalidate(staleMessage)` so that a `pi`/command `ctx`
captured before a session replacement (`ctx.newSession()`, `ctx.fork()`,
`ctx.switchSession()`, and the `/new`, `/fork`, `/resume` UI flows that drive
them) throws `ErrStaleContext` on reuse. Upstream owns one `_extensionRunner`
per `AgentSession`, so disposing the outgoing session invalidates exactly that
runner and the incoming session gets a fresh one.

Stock PiG's four modes (interactive, print, JSON, RPC) create and replace their Session through `coding.CreateAgentSessionRuntime`'s factory, as Pi's `main.ts` does. Every `/new`, `/fork`, `/clone`, `/resume`, RPC `new_session`, `fork`, `clone` and `switch_session`, and `ctx.newSession()`, `ctx.fork()` and `ctx.switchSession()` builds a new Session with its own extension runner and host, and `Runtime.teardownCurrent` (`coding/runtime_replacement.go`) invalidates the outgoing in-process runner (`TestReplacedSession2860New`, `TestReplacedSession2860Fork`, `TestReplacedSession2860Switch` in `coding/session_replaced_2860_upstream_test.go`). Two parts differ from Pi.

Part 1, Go, Rust and Python extensions in the Stock modes. The replaced Session's extension processes outlive the teardown while the command handler that requested the replacement runs. The factory makes their host reject every later extension-to-host call with Pi's stale message and sends each of their connections an `invalidate` notification with it (`subprocess.Host.Invalidate`). The Node runtime invalidates Pi's extension runtime with that message, so a captured `pi` or `ctx` in a Node extension throws it at the call site as Pi does, fire-and-forget calls included (`TestPrintModeReplacedSessionHostCallsFailWithThePiStaleMessage`, `TestReplacedSession2860AcrossSDKs`). A `withSession` callback receives a context of the replacement Session in every SDK (`TestReplacedSession2860AcrossSDKs`). A captured `ctx` in a Go, Rust or Python extension still differs in two ways: (a) getters the SDK answers from its own state (`Cwd`, `Mode`, `HasUI`, `Model`, the theme, and the session-mirror `GetBranch` and `GetEntries`) return the outgoing Session's values instead of failing; the session-manager reads and every other host call fail with the stale message; (b) the host rejects a call from the replaced Session's process with a reply that carries the stale message, but what the extension sees depends on its SDK and on the method. Go returns the error from `SendUserMessage`, `AppendEntry`, `SetSessionName` and from `SetWidget` in its option-bearing or non-`[]string` form (a `[]string` `SetWidget` goes through the unchecked widget push and returns nil), discards it in `Notify`, `SetStatus`, `SetActiveTools` and `Abort`, and writes the `pig: host call ... failed` line only for `On` subscriptions, to the Go process's own stderr; PiG copies only Node extension output to the terminal (`host.go:1920-1929`), so the user never sees it. Python raises `HostCallError` from every host-call method (`notify`, `set_status`, `send_user_message`, `append_entry`, `set_active_tools`, `abort`). Rust returns `Err` from `send_user_message`, `append_entry`, `set_session_name` and `set_widget_value` (`set_widget` goes through the unchecked widget push and returns `Ok`), and discards the error in `notify`, `set_status`, `set_active_tools` and `abort`. The host never applies any of these calls to the replacement Session. Two host inputs are not checked: a widget push and a `ui.custom` frame still reach the replaced Session's detached UI bridge, where they have no visible effect.

Part 2, library callers that hold a bare `coding.Session` and replace its state in place: `Session.NewSession`, `SwitchSession`, `CloneInPlace`, `ForkToNewSession`, `ForkToNewSessionWithText`, the headless `Session.DispatchSlash` `/fork` path, `ImportFromJsonl` and the default `Session.ExtensionCommandActions` new, fork and switch actions. They go through `Session.ReplaceInner`, which keeps the same host-scoped runner (`coding/extension/host/inproc/runner.go`) and does not invalidate it. The invalidation mechanism is fully ported (`Invalidate`, the byte-identical stale message, the `ErrStaleContext` sentinel) and fires on `ctx.reload()`, runtime close and runtime replacement.

Observable effect: (Part 1) a Go, Rust or Python extension that captures `ctx` reads outgoing-Session state through its SDK-local getters after a replacement, and its host calls follow (b) instead of throwing the stale message as Pi does. (Part 2) an extension that captures a `ctx` and reuses it after an in-place replacement on a bare `coding.Session` operates against the current session instead of throwing `ErrStaleContext`. No Stock PiG mode reaches Part 2.

Evidence: Pi's invalidation and access guards are `packages/coding-agent/src/core/extensions/runner.ts:679-690,809-888` and `loader.ts:157-190,238-454`; replacement is `packages/coding-agent/src/core/agent-session-runtime.ts:167-177`; the stale-context expectations are `test/suite/regressions/2860-replaced-session-context.test.ts:147-205`. The in-process stale behavior is proven by the three `TestReplacedSession2860*` tests. Part 1 probes: `TestPrintModeReplacedSessionHostCallsFailWithThePiStaleMessage` proves that a Node extension's awaited, synchronous and fire-and-forget calls throw the exact message and that a stale `pi.sendUserMessage` never starts a turn in the replacement; `TestReplacedSession2860AcrossSDKs` proves the #2860 stale and `withSession` cases for Node, Go, Python and Rust. This record grants no synchronization or comparator exception. `docs/parity/KNOWN-GAPS-0.3.x.md` SC10(a) tracks the SDK side.

Why deferred, not ported: (Part 1) the `invalidate` notification exists, and the Node runtime applies it through Pi's own extension runtime guards. The Go, Rust and Python getters return plain values without an error, so throwing from them needs an SDK interface change or a panic, and the fire-and-forget methods return nothing to carry the error; that needs an approved spec. (Part 2) a bare `coding.Session` does not own a factory that can build a replacement Session, runner and host, so an in-place replacement has nothing to invalidate without wedging the runner it keeps. The supported way to get Pi's behavior for Part 2 is `coding.CreateAgentSessionRuntime`.

Call site:
- `cmd/pig/cli_session_factory.go`: the factory's `Dispose` invalidates the replaced Session's extension host (Part 1).
- `coding/session_extension_replacement.go`: the extension-driven replacement of a bare Session (Part 2).

Parity allowance: no paired scenario drives either part. Part 1 needs an extension that uses a captured `ctx` after a replacement, which no parity fixture does; Part 2 is a library path no Stock PiG mode reaches. The Stock modes are covered by the paired session-replacement scenarios and the tests named above.

Remove when: the Go, Rust and Python SDKs apply the invalidate notification and every SDK-local ctx and pi member fails with Pi's stale message after it (Part 1), and the in-place `Session` replacement methods and the default `Session.ExtensionCommandActions` replacement actions are deleted or routed through `Runtime`, so no caller can replace a Session without invalidating its runner (Part 2).

SCRUTINIZED:approved

## D37 Recover from stale thinking-block signatures (retry with signatures stripped)

What: when the Anthropic Messages endpoint (including the github-copilot
Claude proxy) rejects a request with a thinking-block signature error
(`invalid_request_error` whose message contains both `signature` and
`thinking`, e.g. "Invalid `signature` in `thinking` block"), pig retries the
same request once with thinking signatures stripped: every assistant thinking
block is downgraded to a plain text block (reasoning text preserved as
context) and redacted thinking is dropped. If the retry also fails, the second
error is surfaced.

Why: upstream replays every same-model signed thinking block verbatim
(`transform-messages.ts` keeps `isSameModel && block.thinkingSignature`, and
`anthropic-messages.ts convertMessages` sends `signature: thinkingSignature`)
and has no recovery. Thinking signatures generated earlier by the provider
backend can become unreplayable: observed live on `github-copilot`
`claude-opus-4.8` after a long, repeatedly-rewound, model-switched session:
one early same-model thinking block (valid when generated) is rejected as
"Invalid `signature` in `thinking` block" while later ones still validate,
which permanently wedges the session on every resume. This is a provider-side
staleness that upstream shares; pig recovers instead of hard-failing, trading
the (already-broken) reasoning-continuity signature for a working turn. The
reasoning text still reaches the model as text, so context is preserved.

Bug exists in upstream too: this is a fix-in-downstream-with-divergence per
the maintainer directive; remove when upstream gains equivalent recovery or an
upstream issue resolves the provider signature staleness.

Call-site markers:
- `ai/anthropic.go`: `anthropicProvider.Stream` (`// pig divergence (D37)`),
  `stripThinkingSignatures`, `isThinkingSignatureError`, `anthHTTPError`.
Locked by: `ai/anthropic_test.go` -
`TestAnthropicStream_D37_RetriesOnStaleThinkingSignature` (fail-then-retry,
asserts the retry drops the signature and preserves reasoning text) and
`TestIsThinkingSignatureError` (classifier scope).
Remove when: upstream pi gains equivalent stale-thinking-signature recovery, or
removes thinking-block signatures from the provider contract.
PORT_MAP path: `ai/anthropic.go` (downstream provider resilience; no upstream
equivalent).
SCRUTINIZED:approved

## D39 Standalone-binary self-update

What: pig replaces upstream pi's package-manager self-update with a
standalone-binary update path. Upstream detects the install method
(npm/pnpm/yarn/bun) and emits the matching `install -g` command, with the npm
registry as both the version-of-truth and the transport. pig is a single Go
binary (and, in production, a binary baked into a container image), so that
model does not apply: `pig update` (bare): matching upstream's documented
`pi update` "Update pi only": fetches a JSON update manifest from a configured
source, compares the running version to the manifest version, and, when newer,
downloads the platform binary, requires and verifies its SHA256, rejects declared
or streamed content above the bounded size, and atomically replaces the running
executable and its ownership receipt as one rollback-safe operation.
Missing/malformed/mismatched checksums fail before staging. A standalone replacement holds a per-executable OS lock through download, receipt commit, and rollback. A concurrent update fails before downloading. Like Pi's proper-lockfile lock (`package-manager-cli.ts:171-222`, released in `finally`), the `<executable>.update.lock` sidecar is removed when the update ends. The holder removes it before it releases the OS lock, and every acquirer checks after locking that its descriptor is still the file the path names, so a process that opened the sidecar before removal reopens instead of sharing the lock. A crash leaves an unlocked sidecar that the next update reuses and removes. An updater built before this behavior does not make that check, so two updaters of different builds that start within microseconds of each other can both proceed.

Explicit version checks use Pi's management HTTP policy: at most two immediate retries for transport failures and HTTP 408, 425, 429, 500, 502, 503, or 504, within one ten-second budget. Caller cancellation ends the check. Signature verification and manifest parsing failures are not retried. Startup checks remain best-effort and do not retry.

The signed current
manifest also carries the exact package identity used by package-manager
installs, including an approved package rename.
`pig update self`/`pig` are explicit self aliases; `pig update --extensions`
refreshes every installed package (not pig), `pig update --all` refreshes
all installed packages and then pig, and `pig update <source>` still updates one
package. `--self`, `--extension`, and `--force` follow the shared Pi routing and
conflict rules. (Pig previously made bare `pig update`
update all packages; this aligns it with upstream where bare update is the
self-update.) A startup banner surfaces an available update with the command that applies it. When no source is configured, the source is unreachable, the
platform is unsupported (a standalone Windows binary, or a read-only/containerized
install), pig prints a next-tier fallback ladder: download a new binary or
pull the container image and re-deploy without repeating `pig update`. Installation ownership is explicit before mutation: `ResolveSelfUpdateTier`
proves exactly one owner: writable standalone, package-manager, immutable
Piglet Binary, OCI/Piglet Image, or
read-only/Windows standalone/unknown: and rejects ambiguous ownership. Once a tier
starts, its failure surfaces from that tier and never falls through to another.
The package-manager tier invokes the proven owner's exact `install -g` command. An unconfigured npm command retains the prefix from its proven `lib/node_modules` root. A logical pnpm launcher supplies ownership evidence only when it resolves to the running native executable. Both the owning package directory and its parent must be writable. `SelfUpdateProvenance.GetSelfUpdateCommand` carries this evidence into the signed-release caller; it does not reclassify ownership after the operation starts. The original16 `config.test.ts` cases are covered by `internal/codingagent/config_upstream_test.go`, with caller evidence in `cmd/pig/self_update_prefix_upstream_test.go` and byte-equal command-plan comparison in `cli-utils/16-native-self-update-command-ownership`.

Immutable-binary and container tiers emit exact pull/rebuild/redeploy
remediation rather than in-place drift; read-only/Windows standalone/unknown
tiers refuse and report the executable path plus concrete remediation.

Windows follows upstream for package-manager installs. An npm or pnpm install
updates through its owner's command. Before an npm update, pig quarantines
the running pig.exe under `node_modules/.pig-native-quarantine` and copies it
back, as upstream `prepareWindowsNpmSelfUpdate` does for the native addons it
loaded, because Windows refuses to delete a running image. Every Windows start
clears that quarantine. A yarn or bun install on Windows is refused with
upstream's message ("pig self-update on Windows is only supported for npm and
pnpm installs."). Only a standalone pig.exe stays in the unsupported tier,
because a running Windows executable is not replaced in place.

The update source resolves as `PIG_UPDATE_URL` env, else a transport-neutral
sidecar at `<config-root>/update-url` (written by an installer that knows its
origin at install time, e.g. the marketplace bootstrap), else an optional
build-time `internal/codingagent.DefaultUpdateURL` set by a product's own
release build. PiG's own release builds set it to the latest release's signed
`update.json`, whose `update.json.sig` sits beside it because GitHub release
assets cannot send the `X-Pig-Release-Signature` header, and `install.sh` writes
the standalone receipt, so a script installation updates in place. A Marketplace installer that used an explicit private CA copies
those CA bytes into owner-only `<config-root>/update-ca.pem`; it never persists
the caller's source path. The standalone receipt binds that file's SHA256 to the
executable, release, and update origin. Update HTTP clients add those certificates
to the host system pool, reject changed/unowned/malformed/world-readable CA
material, and continue to require the separately signed release manifest. A
public-CA reinstall removes a prior transport-CA sidecar transactionally.
A managed container deployment that owns a specific redeploy
operation supplies it verbatim through `PIG_REDEPLOY_INSTRUCTION`; Pig renders
that operation inside its own frame (artifact identity, executable path, and the
guarantee that the running image is never rewritten) instead of asserting a
generic `docker pull`, which is wrong for an orchestrator-managed deployment.
Pig performs no product transport and knows no product topology; without that
metadata it falls back to the generic image pull.
Piglet source/build fields and `pig piglet build` never carry
an update endpoint. A Piglet Binary bakes only `release.version` into
`codingagent.PigletBinaryRelease` (published from `main.PigletBinaryVersion`
at startup); update transport remains explicit product or environment policy.
Stock Pig bakes neither a URL nor a Piglet release version.

Why: PiG previously returned nil for `GetSelfUpdateCommand` and used a stale,
unconfigured fallback URL. That silently diverged from upstream because Pi
self-updates and PiG did not. Upstream *does* check for and notify about a new version
(`checkForNewPiVersion` / `showNewVersionNotification`); pig mirrors that
notification ("Update Available. New version X is available. Run `pig update`"),
laid out identically (Spacer, warning DynamicBorder, bold-warning heading +
muted/accent instruction, an optional muted release-note block between spacers,
closing DynamicBorder), with the one-column padding Pi's `Text` and `Markdown`
blocks use. Pi's trailing `Changelog: https://pi.dev/changelog` line names a
hardcoded pi-product page. pig has no such page and bakes no URL: when the signed
manifest's `notes` is a release page URL, pig shows it on the `Changelog:` line
(and not again as a note block), and when the manifest carries no URL pig omits the
line. Any other manifest note is shown as the muted Markdown note block.
Only the update *mechanism* diverges: upstream updates via the package manager,
pig replaces the standalone binary. The self/package command surface matches upstream (`pi update` = self, `--self`, `--extensions`, `--all`,
`--extension`, `--force`, positional package source, and conflict handling),
with `pig` replacing Pi's product name. Upstream's separate `--models` command uses the shared model runtime and is not part of D39. The remote model-catalog overlay is D64's (pi-in-go.dev); D39 does not claim that model-catalog surface.

Skip conditions:
- `PIG_OFFLINE`/`PI_OFFLINE` disables the startup update check
- unparseable local versions never trigger an update notification
- no update source configured → no check, actionable fallback on `pig update self`

Remove when: upstream pi ships a standalone-binary self-update pig can mirror.
This closes the mechanism's install-ownership, package-manager, immutable
Binary/Image, OCI, Windows/read-only, and unknown-provenance gaps;
this divergence remains only for the unavoidable native delivery mechanism
(standalone in-place replace and the generic update-source sidecar), since
upstream pi self-updates through the npm registry rather than a binary
manifest.

Call-site markers:
- `internal/codingagent/selfupdate.go`: manifest fetch, version compare,
  in-place replace, source resolution (env > sidecar > baked default), optional
  receipt-bound transport CA, fallback ladder.
- `internal/codingagent/selfupdate_receipt.go`: standalone ownership binds the
  executable, release, update source, installed bytes, and optional transport CA.
- `internal/codingagent/selfupdate_tier.go`: provenance/tier classification,
  package-manager command + execution, immutable/container/unsupported
  remediation, no-fallthrough `ApplySelfUpdateTier`.
- `internal/codingagent/paths.go`: `GetSelfUpdateUnavailableInstruction`
  delegates to the standalone-binary fallback.
- `cmd/pig/self_update.go`: `pig update` resolves one tier and applies it.
- `cmd/pig/package_commands.go`: update dispatch (`runUpdateCommand`): bare
  self-update, `--all`, per-package.
- `cmd/pig/main.go`: `PigletBinaryVersion` bake, `PigletBinaryRelease`
  publication, startup `BinaryUpdateChecker`.
- `internal/codingagent/interactive.go`: startup update-available banner.
Locked by: `internal/codingagent/selfupdate_test.go`
(`TestCompareVersions`, `TestFetchUpdateManifest`, `TestCheckForBinaryUpdate`,
`TestSelfReplaceAtVerifiesChecksumAndReplaces`,
`TestSelfReplaceAtWithCommitRestoresPreviousExecutable`,
`TestDownloadBinaryRejectsOversizedResponse`,
`TestSelfUpdateFallbackReflectsConfiguredSource`,
`TestUpdateSourceURLPrefersEnvThenDefault`,
`TestUpdateSourceURLSidecarSeedsBetweenEnvAndDefault`,
`TestUpdateTransportCASidecarAllowsPrivateHTTPS`,
`TestUpdateTransportCASidecarPreservesClientRoots`,
`TestUpdateTransportCASidecarRejectsUnsafeMaterial`),
`internal/codingagent/selfupdate_tier_test.go`
(`TestResolveSelfUpdateTier_*`, `TestPackageManagerUpdateCommand_MirrorsUpstreamShape`,
`TestRemediationMessagesAreNonLoopingAndMentionExe`,
`TestContainerRemediationUsesImageRefWhenSet`,
`TestContainerRemediationRendersProductRedeployInstruction`,
`TestApplySelfUpdateTier_NoFallthroughAfterStandaloneStarts`,
`TestApplySelfUpdateTier_ImmutableRefusesWithoutAttemptingDownload`,
`TestAC4PackageManagerUpdateOwnsMutation`,
`TestAC4PackageManagerFailureSurfacesNoFallback`,
`TestResolveSelfUpdateTierOnWindowsFollowsInstallMethod`),
`internal/codingagent/windows_self_update_test.go`
(`TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack`),
`cmd/pig/self_update_windows_test.go`
(`TestSelfUpdateOnWindowsRefusesReceiptedStandalone`,
`TestWindowsNpmSelfUpdateReplacesTheRunningInstallation`),
`cmd/pig/self_update_test.go`
(`TestAC1UpdateRoutingMatchesPi`, `TestAC3StandaloneUpdateVerificationAndAtomicity`,
`TestAC3PrivateCATransportUpdatesWithoutTLSOverride`,
`TestAC11ExactReleasePlanUsesSignedReplacementPackage`,
`TestAC11ForceReinstallsCurrentStandaloneRelease`,
`TestAC5ImmutableBinaryPathRefusesMutation`, `TestAC5ContainerPathRefusesMutation`,
`TestAC6CheckAndFallbackBehavior_*`, `TestAC7NoFallbackAfterStandaloneStarts`,
`TestAC71SelfUpdateSelectsOneProvenTier`),
`cmd/pig/package_commands_test.go`
(`TestGetSelfUpdateUnavailableInstruction_PointsAtStandaloneFallback`,
`TestRunPackageCommand_SelfUpdateTargetWithoutSourceShowsFallback`),
`coding/pigletbuild/native_build_test.go`
(`TestPigletBinaryBuildArgsBakesReleaseVersionOnly`).
PORT_MAP path: n/a (standalone-binary self-replace has no upstream pi equivalent; the update notification mirrors upstream `showNewVersionNotification`).
SCRUTINIZED:approved

## D44 Positive image capability for Herdr intermediaries

What: when `HERDR_ENV=1`, Pig ignores inherited outer-terminal image hints unless Herdr explicitly sets `HERDR_KITTY_GRAPHICS=1`. Without that positive signal, image components render their compact textual fallback and reserve no graphics rows. Direct Ghostty/Kitty/WezTerm/iTerm sessions retain upstream capability detection. An intervening tmux or screen session still disables automatic image detection, regardless of Herdr's outer graphics hint. Explicit Pi image-protocol and settings overrides keep their upstream precedence.

Why: upstream and Pig normally infer image support from variables such as
`TERM_PROGRAM=ghostty`. Herdr panes inherit those variables, but Herdr is the
terminal renderer and its experimental Kitty graphics support can be disabled.
Treating an outer-terminal identity as forwarding evidence emits image bytes
and blank reserved rows that no layer owns. Keyboard protocol support is a
separate capability and does not authorize graphics.

Remove when: upstream supports positive nested-terminal graphics capability
signals, or Herdr guarantees Kitty graphics for every pane and no longer needs
an opt-in renderer.

Call-site markers:
- `tui/terminal_image.go`: `detectCapabilitiesFromEnvironment` (reached from
  `DetectCapabilities`)

Locked by: `tui/terminal_image_test.go` -
`TestDetectCapabilities` (Herdr absent/present signal and direct Ghostty) and
`TestAC51HerdrImageCapabilityOwnsRowReservation` (one fallback line, no
Kitty sequence or reserved rows).
PORT_MAP path: `packages/tui/src/terminal-image.ts` (intentional nested-terminal
interop guard beyond upstream's tmux-only gate).
SCRUTINIZED:approved

## D48 Orphaned and duplicate tool results are stripped from normalized history

What: `NormalizeMessages` (`agent/transform.go`) runs a second pass that
drops any tool-result whose `ToolCallID` matches no surviving tool_use in the
message list, drops the whole tool-result message when none of its results
survive, and drops a repeated result for a tool_use that already has one. Upstream `transformMessages` (`packages/ai/src/api/transform-messages.ts`)
only synthesizes results for orphaned tool *calls*; it never strips orphaned
tool *results*: it pushes them through to the provider unchanged.

Why: pig and upstream both drop errored/aborted assistant turns before provider
conversion (transform-messages.ts:153-159; `transform.go` StopReason check). When
a dropped assistant held the only tool_use for a tool-result that was already
persisted (an abort race where the tool ran and recorded its result before the
turn was marked aborted, a truncated/hand-built session, or a model switch that
dropped the calling turn), the result is left orphaned. Upstream then sends that
orphaned tool-result to the API, which rejects it (OpenAI "No tool call found for
function call output with call_id ...", Anthropic "tool_use_id not found"),
failing the whole request on poisoned history. pig strips it so the request
still succeeds. Making pig faithful here would reintroduce that upstream API
failure on exactly the poisoned-history sessions this risk-core surface must
survive. The strip is inert on clean sessions: every tool-result on a
well-formed session has a matching, surviving tool_use, so nothing is removed.

The same pass enforces one result per tool_use. Providers require exactly one
("each tool_use must have a single result. Found multiple `tool_result` blocks
with id: ..."), and because the rejection happens on every later turn, a single
duplicate makes the session unusable rather than degrading it. Duplicates arise
from a replayed or hand-edited session and from the synthetic placeholder for an
orphaned tool call meeting a real result that arrives out of order. The first
occurrence wins, because a tool_result must directly follow its tool_use and the
first already holds that slot.

Results are matched in order against open calls, not against the set of ids in
the conversation. A call id is not unique across a conversation: a provider that
numbers its calls per response reuses the same id every turn, so the id opens a
new call each time and a result closes whichever call is open when it arrives.
Deduplicating by id alone drops every turn after the first, which stalls the
conversation, and counting occurrences alone lets a duplicate of an early call
consume the allowance belonging to a later one.

A result arriving after the next assistant turn is out of order rather than
missing, so the placeholder slot carries that real result instead of upstream's
`"No result provided"`, and the out-of-position copy is the one dropped. Upstream
emits the placeholder and still sends the late copy, which the provider rejects;
of the two halves of that, telling the model a tool failed when it succeeded is
the more damaging, since the model may retry an operation that already ran. A
tool_use with no result anywhere still receives upstream's placeholder text and
`isError` exactly. Like the orphan strip, all of this is inert on clean sessions,
where every tool_use has exactly one result in position.

Remove when: upstream `transformMessages` strips orphaned tool-results and
enforces one result per tool_use (or otherwise guarantees the provider never
receives a violation), at which point pig's second pass matches upstream and
this divergence is retired.

Call-site markers:
- `agent/transform.go`: the second-pass orphaned-tool-result strip in
  `NormalizeMessages`.

Locked by: `agent/transform_test.go` -
`TestNormalizeMessages_OrphanedToolResult` (compaction/model-switch orphan),
`TestNormalizeMessages_ErroredAssistantOrphansToolResult` (the abort-race path
that upstream would send and error on), and
`TestNormalizeMessages_PartialOrphanSynthesizesMissingResult` (mixed valid +
orphaned results in one message). Each fails if the strip is removed.
Provider-wire lock (`agent/transform_provider_path_test.go`):
`TestPoisonedHistoryProducesValidProviderRequest` drives poisoned history through
the production `convertToLLM`→`Stream` path and asserts the orphaned id never
reaches the OpenAI-completions, OpenAI-responses, or Anthropic request body;
`TestCleanToolCycleSurvivesProviderRequest` proves the strip is inert on a clean
cycle. Both are mutation-proven: neutering the strip leaks the orphan into all
three wire formats, over-stripping drops the clean result side.
PORT_MAP path: `packages/ai/src/api/transform-messages.ts` (pig-additive
poisoned-history robustness beyond upstream's orphaned-call synthesis).
Parity coverage:
`test/parity/scenarios/providers-faux-streaming/09-orphaned-tool-result-wire.toml`
drives the identical poisoned conversation through pig's `NormalizeMessages` and
pinned pi 0.84's openai-completions transform against a hermetic endpoint; its
`[diverge]` block asserts pig omits the orphaned `call_x` from the provider
request (`tool_call_ids_in_request: []`) while pi sends it (`[call_x]`).
Ratification: user-ratified (2026-08-10 review: approved, "do it properly").
SCRUTINIZED:approved

## D51 Interactive SIGINT restores the terminal before numeric exit 130

What: when SIGINT terminates interactive mode, pig pops its extended-key protocols, restores the cooked state captured at startup, and exits with numeric status 130. upstream 0.99.1 leaves SIGINT to Node's default signal action, so process APIs report signal termination and the terminal stays raw. This exception does not cover print/JSON SIGINT, which must terminate by signal, or a dead terminal, which must exit 129. Both products restore a live terminal and exit 0 on SIGTERM or SIGHUP (`packages/coding-agent/src/modes/interactive/interactive-mode.ts:4174-4186,4260-4278`).

Both halves matter. Without the tcsetattr the terminal stays raw, so the
shell has no working Ctrl+C until `reset`. Without the protocol pop the Kitty
flags pig pushed stay on the terminal's stack, so the shell inheriting the
terminal receives CSI-u encoded keys it does not understand. The teardown goes
out as a single write, which is what makes it safe to run from the signal
goroutine alongside the render loop.
`registerSignalHandlers`
(`packages/coding-agent/src/modes/interactive/interactive-mode.ts`) never
registers a general SIGINT handler, taking SIGINT only to ignore it while
suspended, so the signal falls through to Node's default handler, which exits
without unwinding pi's terminal restore.

pig matches upstream on the part that matters most: SIGINT terminates the
session. It is not treated as an interrupt. Ctrl+C never reaches this path
anyway, because pig holds the terminal in raw mode for the whole session, so
`\x03` is consumed by the keymap. Measured on a live pty: `-isig` while idle
and `-isig` while a bash tool runs. SIGINT is ignored while the temporary suspend listener is installed. If SIGINT and SIGCONT are pending together, a SIGINT dispatched after SIGCONT removes the listener can terminate the resumed process, matching upstream's dispatch-time listener semantics.

Why: upstream 0.99.2 leaves the terminal raw after an external SIGINT. The wire audit re-probed this after a positional extension command completed, rather than at the first painted footer. The shell then has no working Ctrl+C until `reset`. The existing live-PTY regression checks PiG's restoration independently of tmux key encoding.

The signal handler uses only the signal-safe keyboard-protocol disable and termios restoration. Normal teardown also drains stdin for up to a second, which would race the input reader and the render loop. The single-main-loop ownership invariant is enforced by `make test-race`.

Call sites: `internal/codingagent/interactive_tui.go`: `handleInterruptSignal`;
`tui/signal_restore.go`: `RestoreTerminalFromSignal`.

Locked by: `test/integration/signal_shutdown_test.go`
(`TestInteractiveSigintTerminatesAndRestoresTerminal`): asserts the terminal is
sane before launch, raw while pig runs, that pig exits on SIGINT, and that ISIG
is back afterwards. Mutation-proven twice: dropping the restore reddens it with
"without restoring the terminal", and surviving the signal reddens it with
"pig survived SIGINT".

Known gap: the captured state is taken at the first raw-mode entry, and pig
disables SUSP before that, so a restored terminal reports `susp = <undef>`
where a pristine one reports `^Z`. ISIG and INTR are restored correctly.

Remove when: upstream restores the terminal before exiting on SIGINT.

Ratification: user-ratified. Verified by hand in a real terminal (Ghostty):
kill -INT exits pig and leaves the shell with a working Ctrl+C, suspend and
resume are clean, and the cost of matching upstream was accepted after
exercising it directly, namely that Ctrl+C while $EDITOR is open now terminates
pig as it does upstream.
SCRUTINIZED:approved


## D53 Full-clear when a differential rewrite would under-clear wrapped rows

What: the differential renderer clears rows with one `\x1b[2K` per logical
buffer row, which assumes each buffer row maps to exactly one physical screen
row. When the terminal narrows and an extension widget re-pushes a frame in a
follow-up render at the now-fixed width, a previous frame's row can be wider
than the new terminal width, so the terminal wrapped it across several
physical screen rows during the intervening render. Rewriting in place then
clears only the first physical row of each logical row, leaving the wrapped
remnants of the previous frame visible above the new frame until a second
resize happens to force a full clear.

Upstream (`packages/tui/src/tui.ts`) uses the same logical-row model and can
leave the same residue. PiG instead clears the full frame. The extra clear is
observable and prevents stale terminal content.

Why: pi-chain (a Node extension) renders a width-sensitive card pipeline.
On a tmux `Ctrl+Z` zoom toggle the pane narrows; the widget's wide frame was
painted before the `width_change` re-push landed, and the follow-up narrow
frame left the wrapped wide rows above it. The renderer change is the minimal
correction at the layer that owns the screen.

Skip conditions:
- if no previous frame row ever exceeds the terminal width, the check never
  fires and behavior is byte-identical to a differential rewrite
- image lines are exempt: `fullRender` and the differential path already
  reserve rows for them, and this check runs only over non-image rows
- the full clear never replaces a differential write that upstream ends with an overflow: when upstream's loop over the changed rows would reach an over-wide row (before any Kitty-image fallback), the differential path runs and terminates as Pi does (`TestOverflowD53DoesNotMaskDifferentialOverflow`)
- the replacement row must also fit: a row over-wide in both frames re-wraps to the same height, so rewriting it in place is correct. Testing only the previous row makes every render that touches a chronically over-wide line a full repaint; the narrower condition fires only when the wrap goes away and would otherwise leave residue.

Call sites: `tui/tui.go`: `doRender`, the wrapped-row guard before
the differential rewrite. Regression: `tui/render_wrap_clear_test.go`
(`TestWideFrameRepushAtNarrowerWidthFallsBackToFullRender`), which fails with
the guard removed (mutation-verified).

Upstream state: upstream pi 0.84.0 stores logical rows and rewrites per row,
so it would show the same artifact; upstream-first says record this rather
than claim parity, and the fallback is observationally identical except the
residue is cleared.
SCRUTINIZED:approved

Remove when: upstream changes the per-row clear semantics so a differential
rewrite is byte-safe under width changes, or the terminal pipeline otherwise
keeps the differential path from under-clearing wrapped rows.

Locked by: `tui/render_wrap_clear_test.go`
(`TestWideFrameRepushAtNarrowerWidthFallsBackToFullRender`), which fails
with the wrapped-row guard removed (mutation-verified).

## D55 The global debug hotkey fires once per press

What: pig drops Kitty key releases before matching the `ctrl+shift+d` debug
hotkey, so one press runs the debug handler once. Registered through
`addKeyPressListener`, which filters before any in-tree terminal-input listener
sees the chunk.

Upstream state: `packages/tui/src/tui.ts:850` tests
`matchesKey(data, "shift+ctrl+d")` and calls `onDebug()` at the top of
`handleInput`, 37 lines before its only release filter at :887, which sits
inside the focused-component branch and never runs for this path. `matchesKey`
resolves through `matchesKittySequence` (`keys.ts:653`), which compares the
codepoint and the modifier and ignores the Kitty event type, so it answers true
for the release of the chord as readily as the press. Upstream therefore runs
`onDebug` twice for one keypress whenever the Kitty protocol is active, which is
pig's default because extendedKeyInit pushes flag 2.

Why: the handler writes a debug log and appends a confirmation to the chat, so
upstream's behaviour duplicates both on every use. The affordance exists to make
a bad session legible, and a debug surface that reports each event twice
undermines the one job it has: a reader cannot tell a genuine repeat from the
hotkey's own echo.

Scope is deliberately narrow. It covers this one hotkey, not raw input delivery
in general: extension terminal-input listeners keep seeing unfiltered input
through `addTerminalInputListener`, matching upstream's `addInputListener` and
the `wantsKeyRelease` opt-in its own space-invaders and doom examples rely on.

Call sites: `internal/codingagent/interactive.go`: the debug hotkey
registration, and `addKeyPressListener` which applies the filter.
Regression: `internal/codingagent/debug_hotkey_release_test.go`
(`TestDebugHotkeyFiresOncePerPress`), which fails when the registration is moved
back to `addTerminalInputListener`.
SCRUTINIZED:approved

Remove when: upstream tests the debug hotkey after its key-release filter, or
`matchesKey` stops matching a release, at which point the raw registration
becomes correct on its own.

Locked by: `internal/codingagent/debug_hotkey_release_test.go`
(`TestDebugHotkeyFiresOncePerPress`).

## D56 Subprocess liveness and renderer isolation

What: Pig heartbeats a subprocess only while it owns outstanding work or live
provider state. Tools, commands, events, and shortcuts have no host completion
or inactivity timeout. Their caller-owned context remains authoritative, and a
healthy dispatcher keeps the connection alive while awaited work continues.
Renderer work has a five-second inactivity boundary off the TUI loop, retains
the last completed frame, and disables only the stalled generation. A missed
heartbeat closes the logical connection and fails pending work with a typed
`extension_unresponsive` error.

Upstream state: Pi runs extensions in-process and directly awaits callbacks. It
has no subprocess heartbeat, transport failure, packed-cell isolation, or
request-inactivity state machine. Its only extension timeout is the opt-in,
per-dialog `ExtensionUIDialogOptions.timeout` with a visible countdown.

Why: wall-clock completion limits reject valid extension work and interactions
that wait for a person or an external system. Heartbeat verifies that the SDK
dispatcher and transport are responsive without imposing a duration limit on
the operation. Renderer generations remain bounded because rendering is
latency-sensitive and the host can retain the last completed frame. A logical
packed-member failure never quarantines healthy siblings. Shared process death
remains the packed-cell recovery authority. Node recovery isolates an attributable culprit and restarts healthy members together. Unknown failure gets one whole-group restart, then diagnostic bisection on recurrence; identifying the failing member reunites the healthy group (D20). Neither tools nor callbacks interrupted by process death are replayed. Old-generation callbacks and UI frames retain their dead connection identity; only newly invoked named capabilities may use the recovered process. Recovery ends with the original owner or Host shutdown.

The lifecycle handler owns a failed connection's diagnostic when a crash handler is installed. Commands, shortcuts and events interrupted by that connection still fail, but do not emit duplicate notifications. Without a crash handler, the runner reports the invocation error. Ordinary handler rejections are not transport failures. Packed socket-close and process-exit detectors claim each member under the same registry lock, so a late observation neither reports it twice nor unregisters a replacement. Subprocess stderr logs are removed after teardown unless a load or lifecycle diagnostic names them.

Remove when: Pig no longer hosts extensions across a subprocess boundary, or
upstream provides an equivalent subprocess liveness contract that Pig can port
without this divergence.

Call-site markers:
- `coding/extension/host/subprocess/conn.go`: heartbeat, request state,
  cancellation, and typed transport failures.
- `coding/extension/host/subprocess/host.go`: handler inactivity and
  supervision.
- `coding/extension/host/inproc/runner.go`: lifecycle-owned invocation diagnostics.
- `internal/codingagent/interactive_helpers.go`: lifecycle-owned shortcut diagnostics.
- `coding/extension/host/subprocess/render_proxy.go`: generation-scoped
  renderer inactivity and last-frame retention.
- `coding/extension/host/subprocess/tool_render_proxy.go`: the same renderer
  boundary for tool `renderCall` and `renderResult`.

Locked by: `coding/extension/host/subprocess/liveness_test.go`,
`coding/extension/host/subprocess/node_liveness_test.go`, Go/Rust/Python SDK
liveness tests, and `coding/extension/host/subprocess/protocol_sdk_sync_test.go`.
The tests use an injected monotonic clock and deterministic channels. They cover
healthy and missing heartbeat, unbounded tool/command/event/shortcut waits,
renderer retention, cancellation ordering, writer failure, and packed-member
versus packed-process failure. `coding/extension/host/subprocess/crash_once_test.go` covers both detector orders, replacement, real crashing commands in packed/isolated mode, and ordinary errors. `isolated_log_test.go` covers normal runs, reload, cancellation and retained failure logs. `internal/codingagent/interactive_command_error_test.go` proves command and shortcut diagnostic ownership through the interactive dispatch path; `22-command-error-once` compares ordinary command rejection and recovery against Pi.
Ratification: explicitly approved by the user for section SHA-256 `a4109be02ff4f48b03c168073e2288b032971cff63982741e7582b68464bca81`.
SCRUTINIZED:approved
## D57 Installing an extension directory as a package is refused

What: `pig install <dir>` fails when the directory satisfies one complete
conventional factory or standalone extension contract and contributes no
Package resources. The source is not recorded. Upstream records it and reports
success, but Package discovery would load nothing. A directory with only a
language or build marker still installs as an empty Package, matching Pi.

Why: an extension root and a Package root have different ownership. A Package
contributes only exact members under its `extensions` inventory. Promoting an
arbitrary Package root because it contains source would make ordinary npm,
Cargo, Python, or Go packages executable extensions. The refusal names direct
`-e`, the canonical agent extension directory, and Package `extensions/` as the
working choices.

Scope: the source resolver proves Go, Rust, and Python factories or exact standalones. Missing standard factories, ambiguous languages or roots, and incomplete source do not trigger this refusal because they do not prove an extension contract. Node factory resolution selects only an entrypoint and defers export validation to the runtime. It does not prove an extension contract. Node Packages, including ordinary npm libraries with `index.js`, therefore install without importing their code or requiring Pi resources, matching Pi's `package-manager.ts:1005-1031`.

Remove when: upstream reports unloadable extension-root installs itself, or
Package discovery gains an equivalent explicit distinction.

Call-site markers:
- `cmd/pig/package_commands.go`: rejects a proven extension root before an empty Package install can be recorded.

Locked by: `cmd/pig/package_install_empty_test.go` -
`TestInstallRejectsAnExtensionDirectoryAsAPackage`,
`TestInstallDoesNotRefuseDirectoriesThatMerelyLookLikeCode`,
`TestInstallAcceptsAPackageUsingConventionDirectories`,
`TestPackageInstallPlainNpmPersistsWithoutLoadingCode`, and
`TestEveryPackageResourceKindCountsAsAContribution`.
Ratification: explicitly approved by the user for section SHA-256 `4e06f3d7200cce8f6aa65e6074a3632923f7324ac170bd4e93ae38165c31ca5d`.
SCRUTINIZED:approved
## D61 Session replacement keeps startup-project Services and Resources

What: upstream `AgentSessionRuntime.switchSession()` opens the destination
Session, then calls `createRuntime()` with the destination Session CWD. That
constructs the incoming Session's settings, resource loader, system prompt, and
built-in tools against the destination project. Stock PiG's four modes now create every replacement Session through `coding.CreateAgentSessionRuntime`'s factory, which rebuilds Services, settings, Resources, system prompt, built-in tools and the extension host for the destination cwd (`cmd/pig/cli_session_factory.go`; RPC `switch_session` to another project is proven by `cmd/pig/session_replacement_modes_test.go`). That path matches upstream and this record does not cover it.

The divergence that remains is for library callers that replace a bare `coding.Session` in place (`Session.NewSession`, `SwitchSession`, `CloneInPlace`, `ForkToNewSession`, `ForkToNewSessionWithText`, the headless `Session.DispatchSlash` `/fork` path, `ImportFromJsonl` and the default `Session.ExtensionCommandActions` replacement actions). `coding.Session.ReplaceInner` swaps the Session history, identity, model, thinking level and persisted CWD inside one runtime. It does not reconstruct `coding.Services`, the resolved Resource set, or built-in tool instances.

Observable effect: after such an in-place switch to a Session whose recorded CWD is a different project, built-in tools, project settings, context files, prompts, skills, themes and the system prompt still use the project the bare Session was built for. Same-project operations are unaffected. No Stock PiG mode reaches this path.

Why deferred: a bare `coding.Session` has no factory that can build the destination's Services and Resources. Updating only tool CWD would leave settings, resources, and the system prompt stale while making the partial switch appear complete. The supported way to get Pi's behavior is `coding.CreateAgentSessionRuntime`.

Call site:
- `coding/session.go`: `Session.ReplaceInner`, the in-place replacement chokepoint of the library `Session` methods above. No Stock PiG mode calls it.

Parity allowance: no paired scenario drives the library path, which no Stock PiG mode reaches. The Stock modes are covered by the paired session-replacement scenarios and the tests named above.

Remove when: the in-place `Session` replacement methods are deleted or routed through `Runtime`, so no caller can replace a Session without rebuilding Services, Resources and tools for the destination cwd.

SCRUTINIZED:approved

## D62 /bug exports locally and links to a PiG issue

What: Pi's `/bug` asks for consent, then either uploads the report to Earendil's report gateway or exports a zip archive. PiG runs the same consent flow (description, transcript consent, optional model-written summary) but offers only the export. It writes `pig-bug-report-<id>.zip` to the current directory, records the same `pi.bug-report` session entry with `delivery: "zip"`, and prints a prefilled link to the PiG bug issue form. The link carries only the form fields `title` (the first line of the description, at most 72 characters), `version`, `platform`, and `actual` (the report ID and archive name). It never carries session content. The user attaches the archive. The command description is "Export a bug report to attach to a PiG issue" instead of "Report a bug to the Pi developers".

Why: PiG is not an Earendil product. Sending PiG reports to Pi's gateway would misdirect them and share user data with a party the user did not choose. The owner ratified export-only plus a PiG issue link on 2026-09-23 (delivery/WORKSTREAMS.md 16).

Observable effect: the delivery selector lists "Export as Zip" and "Cancel" with no "Upload Report". PiG makes no network request for a report; only the optional summary calls the session's own provider, after the same consent prompt as Pi. `diagnostics.json` carries the crash log (`<agentDir>/crashes.json`) as Pi's does, and a written report clears it.

Call sites:
- `internal/codingagent/slash_bug.go`: the delivery selector and the issue link.
- `internal/codingagent/slash_commands.go`: the `/bug` registration.

Locked by: `internal/codingagent/bug_report_test.go` mirrors upstream `test/bug-report.test.ts` (redaction and the multi-line description prompt) and proves the export path makes no HTTP request and imports no network package. `test/upstream-parity` `TestBuiltinSlashCommands_CoverUpstream` covers the command's presence.

Remove when: never, unless PiG gains its own report service that the owner approves.

SCRUTINIZED:approved

## D63 `--version` prints PiG's composite version

What: `pi --version` prints Pi's bare version (for example `0.99.1`). `pig --version` prints one composite version: PiG's release with the Pi release PiG ports as semver build metadata (for example `0.3.0+0.99.1`, `coding.Version`). The help and diagnostics banner, the interactive startup line, `pig build`'s installed line, and `pig verify` show the same composite. `pig version` keeps its separate `pig:` and `upstream pi:` fields, which the Piglet image check and `pig build` read.

Why: one string tells a user both which PiG they run and which Pi it ports. The owner chose this on 2026-09-23.

Observable effect: a script that runs `pig --version` and expects Pi's bare version sees `0.3.0+0.99.1`. Semver precedence ignores build metadata, so the composite sorts as `0.3.0`; self-update comparisons, release tags, and the Piglet compatibility check still use `coding.PigVersion` itself.

Call sites:
- `cmd/pig/main.go`: `cliVersionString`.

Locked by: `cmd/pig/main_test.go` `TestCLIVersionStringIsCompositeVersion`; the parity scenario `test/parity/scenarios/startup/01-version-flag.toml` asserts both outputs in its `[diverge]` block.

Remove when: never, unless the owner returns `--version` to Pi's bare version.

SCRUTINIZED:approved

## D64 PiG's hosted endpoints live on pi-in-go.dev

What: upstream 0.99.2 points its hosted endpoints at pi.dev and uploads shared-session artifacts to Earendil's Radius gateway at `radius.pi.dev`. PiG serves its hosted paths from `https://pi-in-go.dev` through the private PiG platform repository. The version check, install report, and managed installer API keep Pi's response shapes at the PiG host; the installer API serves PiG GitHub Release archives and returns 404 for npm-only `package.json` and `package-lock.json` paths because PiG has no npm package.

Pi's `/share` uploads an organization-visible JSONL artifact to Radius only when Radius auth is available, then otherwise falls back to a private GitHub gist. PiG always requires the explicit `/share` command, displays a persistent privacy notice before the request, and uploads the same `exportSessionForShare` JSONL shape to `https://pi-in-go.dev/v1/artifacts?visibility=unlisted&title=PiG+session`. PiG needs neither Radius nor `gh` login for this path. The PiG platform stores at most 8 MiB per artifact, gives it an unlisted `https://pi-in-go.dev/session/p_<id>` URL, and expires it after 30 days. `PI_SHARE_GATEWAY_URL` explicitly replaces the complete upload URL; `PI_INSTALLER_API_BASE` keeps its upstream meaning for the installer.

Pi's built-in providers read a remote model-catalog overlay from `https://pi.dev/api/models/providers/<provider>` (remote-catalog-provider.ts:DEFAULT_CATALOG_BASE_URL). PiG reads the same route from `https://pi-in-go.dev` and never calls pi.dev for it. `CreateModelRuntimeOptions.CatalogBaseURL` (Pi's `catalogBaseUrl`) replaces the endpoint. The request, the `?types=` query, the 4 hour freshness window, etag revalidation and the 404/501 unavailable path are Pi's; the `User-Agent` is PiG's (D65), so a server that redirects `pi/` agents to a `pi-version` URL (Pi's catalog protocol) sends PiG no redirect. A host that does not serve the route leaves the bundled catalog, exactly as Pi's unavailable overlay does. PiG's generated catalogs carry no generation timestamp, so a stored remote catalog is always newer than the bundled one (Pi compares the stored catalog's `Last-Modified` with `getBuiltinModelDataGeneratedAt`).

Pi's `reportInstallTelemetry` (interactive-mode.ts:1312-1327) sends its anonymous install/update ping to `https://pi.dev/api/report-install?version=<version>`. PiG sends the identical ping — only the `version` query parameter, plus PiG's own `User-Agent` — to `https://pi-in-go.dev/api/report-install?version=<version>` instead, fired at the same two occasions Pi fires it (a fresh install, and an update whose changelog has new entries), gated by the same `enableInstallTelemetry` setting and `PI_TELEMETRY`/`PI_OFFLINE` env overrides, with the same 5s timeout and fire-and-forget error handling. `PIG_INSTALL_TELEMETRY_URL` explicitly replaces the endpoint (tests use it to point at a local server; there is no upstream equivalent).

The owner designed out experimental Radius composition on 2026-09-28. The experimental server and client use only native Unix sockets: `StartServer` opens no relay host and reports no Radius status, `radius://` connection addresses fail with the unsupported-transport diagnostic, `--auth-token` and `--auth-token-file` are unsupported experimental options, and `OpenClientRuntime` rejects non-Unix routes before discovery. This replaces Pi's `experimental/server.ts:637-677`, `experimental/commands.ts:14-29`, `experimental/client-runtime.ts:57-68,125-133,156-169` and the Radius branches of `cli/experimental/command-options.ts`. The explicit-gateway Radius relay library in `internal/experimental` and its ported tests are unchanged.

Why: PiG is not an Earendil product. Sending PiG users or Session data to pi.dev, radius.pi.dev, or an implicit third-party gist would misattribute PiG traffic and depend on a service PiG does not operate. The owner chose `pi-in-go.dev` on 2026-09-23 and approved the explicit, privacy-noted PiG share gateway on 2026-09-24 (delivery/OWNER-DECISIONS.md).

Observable effect: `/share` shows what Session data will be uploaded, runs the request behind an Escape-cancellable loader, and prints a 30-day unlisted PiG URL instead of a Radius or GitHub-gist URL. Anyone with that URL can read the artifact. The install/update ping goes to `pi-in-go.dev` instead of `pi.dev`; the PiG-hosted endpoint stores one Analytics Engine data point per call (the version and arrival time only) and rate-limits at 10 calls/minute per client. PiG does not read Pi's `PI_SHARE_VIEWER_URL`, so `--help` does not list it or its pi.dev default. Other hosted endpoint clients use `pi-in-go.dev` instead of `pi.dev` as they land.

Call sites:
- `internal/codingagent/session_share.go`: `defaultShareGatewayURL`, `shareGatewayURL`, and `shareSession`.
- `internal/codingagent/interactive_share.go`: `shareSessionWithLoader`.
- `internal/codingagent/slash_commands.go`: the `/share` destination and retention description.
- `internal/codingagent/install_telemetry.go`: `defaultInstallTelemetryURL`, `installTelemetryURL`, and `sendInstallTelemetry`.
- `internal/codingagent/remote_catalog_provider.go`: `DefaultCatalogBaseURL` and `builtinModelDataGeneratedAt`.
- `internal/codingagent/interactive.go`: the startup changelog/install-telemetry block in `Run`.
- `automation/gen/gen-help.sh`: drops `PI_SHARE_VIEWER_URL` from the rendered `cmd/pig/help_upstream.txt`.
- `internal/experimental/server_runtime.go`: Unix-only `StartServer` composition.
- `internal/experimental/client_runtime.go`: `OpenClientRuntime` Unix-only route selection.
- `internal/experimental/command_parse.go`: `connectOption` and `parseTransportAddress`.
- `cmd/pig/main_experimental.go`: `runServerCommand` without relay status.

Locked by: `internal/codingagent` `TestShareSessionUploadsJSONLWithPrivacyNotice`, `TestShareSessionKeepsConcurrentExportsIsolated`, `TestUploadShareArtifactHonorsCancellationAndCanonicalOrigin`, `TestShareLoaderEscapeCancelsUpload`, `TestSharePrivacyNoticeRemainsVisibleAfterResult`, `TestShareGatewayURLDefaultAndOverride`, and `TestShareBuiltinDescribesUnlistedExpiry`; `cmd/pig` `TestHelpOmitsUnusedShareViewerURL`; the private-platform patch's `worker/test/share.test.ts` covers route shape, R2 limits, expiry, escaping, and hashed rate limiting. `TestReportInstallTelemetry_SendsOnlyVersionToConfiguredEndpoint`, `TestReportInstallTelemetry_DefaultURLIsPiInGoDevNotPiDev`, `TestReportInstallTelemetry_SettingDisabledSkipsRequest`, `TestReportInstallTelemetry_EnvOverrideDisablesEvenWhenSettingIsOn`, `TestReportInstallTelemetry_PIOfflineSkipsEvenWhenTelemetryIsOn`, `TestReportInstallTelemetry_NeverContactsPiDotDev`, `TestRecordChangelogVersionAndMaybeReportInstall_FreshInstallPingsAndRecordsNoBanner`, `TestRecordChangelogVersionAndMaybeReportInstall_UpdateWithNewEntriesPingsAndShowsBanner`, `TestRecordChangelogVersionAndMaybeReportInstall_SameVersionNeverPings`, and `TestRecordChangelogVersionAndMaybeReportInstall_VersionBumpWithNoNewEntriesNeverPings` lock the install-telemetry ping. `internal/experimental` `TestExperimentalRadiusSelectionIsClosed`, `TestExperimentalClientRejectsNonUnixBeforeDiscovery` and the D64 row of `TestServerSelectedPresentationFacetsUpstream` lock the experimental Unix-only boundary.

Remove when: never, unless PiG's owner selects another PiG-operated host, returns `/share` to Pi's Radius/GitHub flow, or reapproves experimental Radius (update this entry and every call-site marker then).

SCRUTINIZED:approved

## D65 PiG identifies itself as `pig/<coding.Version>`

What: upstream `getPiUserAgent()` has two package-local forms. The AI helper (`packages/ai/src/utils/pi-user-agent.ts`) sends every HTTP provider request's default `User-Agent` as `pi (<platform> <release>; <arch>)` (or `pi (browser)` with no Node/Bun runtime). The coding-agent helper (`packages/coding-agent/src/utils/pi-user-agent.ts`) reports `pi/<version> (<platform>; <runtime>; <arch>)` for management requests and bug-report metadata. PiG has no browser build and always runs as a native process, so both PiG surfaces use the one owner-approved product identity: `pig/<coding.Version> (<platform> <release>; <arch>)`, for example `pig/0.3.0+0.99.1 (darwin 25.6.0; arm64)`. The release comes from `uname` on unix (`ai/user_agent_unix.go`, `internal/codingagent/bug_report_unix.go`) and from `RtlGetVersion` on Windows (`ai/user_agent_windows.go`, `internal/codingagent/bug_report_windows.go`), matching Node's `os.release()` on each platform.

Why: PiG is not Pi; providers, diagnostics, and their logs should distinguish PiG traffic and artifacts from Pi's, and identify both the PiG release and the Pi release it ports. The owner chose this shape on 2026-09-23 (delivery/OWNER-DECISIONS.md Q4).

Observable effect: every default provider `User-Agent` (Anthropic Messages, OpenAI Completions, OpenAI/Azure/Codex Responses, Google Generative AI/Vertex, Mistral Conversations) and the `report.json` environment identity in an exported bug report read `pig/...` instead of one of Pi's `pi...` forms. A user-configured provider `User-Agent` header overrides the default except for OpenAI Codex Responses, whose upstream `buildBaseCodexHeaders` deliberately reapplies the product identity after model/request headers. An Anthropic OAuth (subscription-token) request keeps sending `claude-cli/2.1.280` regardless (D63's sibling decision, Q3): that identity is not `getPiUserAgent()`'s output and is untouched by this divergence.

Call sites:
- `ai/anthropic_client.go`: `mergeAnthropicClientHeaders` (seeds the `User-Agent` key; the Claude Code OAuth identity still wins on the wire).
- `ai/openai.go`, `ai/openai_responses.go` (also reached by `ai/azure_openai_responses.go` and `ai/openai_codex_responses.go`, which delegate to the same request builder), `ai/google.go` (also reached by `ai/google_vertex.go`), `ai/mistral.go`: the `User-Agent` header construction. Ordinary model/request headers retain upstream override precedence; Codex uses `forceUserAgent` to mirror its trailing `headers.set("User-Agent", getPiUserAgent())`.
- `internal/codingagent/bug_report.go`: `codingAgentUserAgent`, used for the local bug-report metadata field retained by D62.
- `internal/codingagent/selfupdate.go`: `FetchUpdateManifest`, which sends the identity with the update-manifest request as upstream `getLatestPiRelease` sends its `User-Agent` and `accept` headers to the version-check API.

Locked by: `ai/user_agent_test.go` (`TestPiUserAgentFormat`, `TestProviderDefaultUserAgent`, `TestProviderUserAgentOverridePrecedence`), `ai/anthropic_oauth_test.go` `TestAnthropicClientUserAgent`, `internal/codingagent/bug_report_test.go` `TestBugReportEnvironmentUsesPiGUserAgent`, and `internal/codingagent/version_check_upstream_test.go` `TestVersionChecks` (the update-manifest request).

Remove when: never, unless the owner changes PiG's product identity.

SCRUTINIZED:approved

## D66 Narrow TUI rows stay within the requested width

What: PiG keeps two narrow-width component paths within their requested terminal-cell width. `TruncatedText.Render` reduces horizontal padding when the full padding plus one content cell would exceed the width. At width 1 with one column of horizontal padding, PiG renders `"A"`; upstream renders `" A "`, which is three cells wide. `UserMessageSelector.Render` also clips each list, metadata, empty-state, and scroll-indicator row after adding the cursor or indentation. Upstream's `UserMessageList` truncates only the message body and then adds its two-cell cursor, while metadata and other rows are unbounded. `Editor.Render` at width 1 without padding highlights the final grapheme of a line when the cursor is at its end, rendering `"g"` as one inverse `g`; upstream appends a highlighted space, two cells wide. At every wider width, and with padding, PiG appends the space as upstream does.

Why: both upstream paths can emit a row wider than the terminal. Upstream's main screen treats that as fatal only in its differential-render loop and stops with `Rendered line exceeds terminal width`; initial, full, and resize renders emit the over-wide row unchanged. PiG preserves the complete padding and rows at ordinary widths, but prioritizes keeping an unusually narrow pane usable instead of emitting an over-wide row.

Observable effect: at widths where fixed padding, cursor text, or metadata cannot fit, PiG removes padding or clips the row while Pi emits an over-wide row and terminates if that row reaches the main screen's differential-render overflow check.

Call-site markers:
- `tui/truncated_text.go`: the horizontal-padding clamp in `TruncatedText.Render`.
- `tui/user_message_selector.go`: the final row-width bound in `UserMessageSelector.Render`.
- `tui/editor.go`: the final-grapheme cursor in `Editor.buildVisualLines`.

Locked by: `tui/component_width_table_test.go` `TestSelectorDialogListComponentsNeverExceedRenderWidth`, whose `TruncatedText`, `UserMessageSelector`, `UserMessageSelectorScrolled`, `UserMessageSelectorEmpty`, and `EditorSlashAutocomplete` cases render every width from 1 through 120 and reject any over-wide row, and `tui/editor_overlay_cursor_test.go` `TestEditorCursorAtWidthOneStaysInBounds` pins the editor's width-1 row. Restoring upstream's full padding or removing the selector's final clip fails the matching width-1 case.

Parity allowance: paired interactive scenarios use a viable terminal width. The intentional difference exists only when these rows cannot fit; the width matrix directly locks the allowed behavior and its boundary.

PORT_MAP paths: `packages/tui/src/components/truncated-text.ts`, `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts`, and `packages/tui/src/components/editor.ts`.

Remove when: upstream clamps `TruncatedText` padding, bounds every user-message selector row, and fits the editor's end-of-line cursor at width 1, or its main screen safely handles over-wide rows without terminating.

SCRUTINIZED:approved

## D68 Windows owner-only files are enforced by DACL

What: PiG requires some files to be readable by their owner only and writes some that way: Piglet `secrets` read `from: {file: ...}`, the secret environment file staged for the agent container, a Piglet signing private key, the self-update transport CA sidecar, and the standalone self-update receipt. On Linux and macOS that is mode `0600` (no group or other permission bits). Windows has no POSIX mode bits, so there `internal/ownerfile` creates the file with a protected DACL (no inherited entries) whose only entry grants the current user full access, as part of the creation itself, and accepts a file only when its DACL allows no one but the file's owner, SYSTEM, and Administrators. A NULL DACL, an allow entry for any other principal, and object or callback allow entries are refused.

Why: mode bits do not exist on Windows (Go reports 0666 for every writable file), so the POSIX check refused every such file there and a written file got no protection. The DACL form gives the same guarantee, access by the owner only, while still allowing SYSTEM and Administrators, which Windows grants on user profile files by default. The lead chose this contract for Piglet secret files on 2026-09-25 (team/lead/inbox WIN-NOTES.md, Q4). Q4 does not establish approval for signing private keys, the update transport CA sidecar, or standalone update receipts. Their existing use of this policy remains pending the explicit Q15 scope decision; the approval marker below applies to the Piglet secret-file contract only.

Observable effect: on Windows a file that inherits the default ACL of a user profile directory is accepted, a file with an Everyone, Users, or other-user allow entry is refused with the same "owner-only" error as on POSIX, and the files PiG writes carry a protected current-user-only DACL from the moment they exist, so no other principal can open them before they hold a secret. Upstream Pi has no Piglet secrets, signing keys, or standalone update receipts, so no Pi behavior changes.

Call-site markers:
- `internal/ownerfile/ownerfile_windows.go`: `CreateNew` (and `CreateTemp` through it) and `OwnerOnly`, used by `coding/piglet` (secrets and the staged secret environment), `coding/piglet/signature` (private keys), and `internal/codingagent` (update transport CA and standalone receipt).

Locked by: `internal/ownerfile` `TestCreateNewIsOwnerOnlyBeforeItsFirstWrite` and `TestCreateTempIsOwnerOnlyBeforeItsFirstWrite` (in a directory whose new files Everyone can read); `coding/piglet/signature` `TestKeygenPrivateKeyIsOwnerOnlyBeforeItsFirstByte`; `coding/piglet` `TestSecretFileMustBeOwnerOnly` and `TestAC7PigletSecretsFailClosedAtResolution/unsafe_file_permissions` (an Everyone read entry on Windows, mode 0644 elsewhere, must be refused), `TestOwnerOnlySecretFileHasAProtectedCurrentUserDACL` (Windows), `TestAC7SecretEnvironmentFileIsProtectedAndValuesStayOffArgv`, and `TestAC7PigletSecretContract`; `coding/piglet/signature` `TestKeygenAndTrustStore`; `internal/codingagent` `TestUpdateTransportCASidecarRejectsUnsafeMaterial`. Making the Windows check accept every file fails the refusal tests.

Parity allowance: none of these files has an upstream counterpart; the unit tests above lock the contract on each platform.

Remove when: never, unless Windows gains POSIX permission bits or these files stop requiring owner-only access.

SCRUTINIZED:approved

## D70 Session replacement re-evaluates every extension module, not only its factory

What: on every session replacement (`/new`, `/resume`, `/fork`, `/clone`, `ctx.newSession()`, `ctx.fork()`, `ctx.switchSession()`, and RPC `new_session`, `switch_session`, `fork` and `clone`), Pig starts a new extension host for the replacement Session and its extensions run in fresh runtime processes. (`/reload` is not affected: it re-invokes each factory inside the extension runtime process that already holds its module, as Pi does.) The new process imports the extension module and calls its factory, so module-level state (top-level `let` bindings, module-scope caches, counters, open handles) and process-wide state (`process.env` changes, `globalThis` properties) start over after every replacement. Factory-scoped state starts over in both Pi and Pig.

Upstream state: upstream 0.99.1's `DefaultResourceLoader.reload` calls `clearExtensionCache()` when its loader has loaded before (`resource-loader.ts:509`) and invokes every extension factory again inside the same Node process. `AgentSessionRuntime` builds the replacement Session's resource loader and extension factories again in the same process (`agent-session-runtime.ts:197-351`, `main.ts` `createRuntime`), but a replacement's new loader reads the module-level factory cache (`loader.ts:128-148`), which only `clearExtensionCache()` or a change of working directory empties. Whether the module body runs again therefore depends on the path. On reload, jiti (`moduleCache: false`) re-evaluates a `.ts` module, while a `.mjs` module stays in Node's native ESM cache and its module-level state survives. On a replacement in the same working directory, neither module is evaluated again. Both behaviors were probed directly on upstream 0.99.1, and the 0.87.1 release gave the same counts: two `ctx.reload()` calls logged `.ts` evaluated 3x with 3 factory calls and `.mjs` evaluated 1x with 3 factory calls, and two RPC `new_session` commands logged `.ts` evaluated 1x with 3 factory calls and `.mjs` evaluated 1x with 3 factory calls.

Observable effect: an `.mjs`, `.ts` or natively imported `.js` extension that keeps state at module scope sees it reset by each session replacement, and retained by Pi's when the replacement keeps the working directory. An extension that assigns `process.env` or `globalThis` in one Session finds it in Pi's replacement Session and does not find it in Pig's. Extensions that keep state inside the factory behave the same on both.

Why: Pig hosts extensions outside its own process, so an extension instance is a runtime process. Re-invoking a factory inside a retained process would need an in-process re-registration protocol and would keep a process whose registrations are being replaced, which the atomic start-beside/swap/stop-old reload transaction is built to avoid. A fresh process matches the part of the contract that holds for every Pi loader, which is that each factory runs again on reload and on session replacement. The outgoing process also has to outlive the command that requested the replacement, because that command awaits the replacement's result; the factory retires it when the command's handler returns. Once RPC mode's shutdown has started, retirement stops shutting hosts down, because a quit `session_shutdown` may still be pending on one and Pi leaves that handler pending; process exit terminates the held hosts.

Call-site markers:
- `cmd/pig/cli_session_factory.go`: `cliSessionFactory.create`, where a replacement Session's build starts its own extension host, and `cliRetirement.retire` and the held-retirement return in `cliSessionFactory.create`, where the replaced Session's host stops after its running command handlers return.
- `cmd/pig/cli_runtime_build.go`: `buildResources`, the extension load of every build, which a replacement runs again for the destination cwd.

Locked by: `cmd/pig/session_replacement_modes_test.go` `TestPrintModeNewSessionBuildsFreshExtensionInstances` and `TestRPCSessionCommandsReplaceThroughTheRuntimeFactory`, and `cmd/pig/session_replacement_interactive_test.go` `TestInteractiveSessionCommandsReplaceThroughTheRuntimeFactory` and `TestInteractiveExtensionNewSessionKeepsTheCallerAliveUntilItReturns`. Each starts pig with `testdata/session-replace.mjs`, whose instance identity is module-scope state, and requires a different instance for every replacement Session; each fails when the replacement keeps the outgoing extension process.

Parity allowance: no paired scenario asserts module-scope state across `/reload`, because Pi's result depends on the extension's file type and Pig's is the same for all of them. Scenario 15 keeps its state in the factory, the per-reload contract both share. Scenarios `39-extension-session-replacement`, `40-json-extension-session-replacement`, `41-rpc-extension-session-replacement` and `42-tui-extension-session-replacement` (extensions-runtime) trace a session replacement through a fixture that assigns `process.env.FIN_REPLACEMENT_LOG` in one Session and reads it in the next; Pig's side of those scenarios receives the same value at startup, and each scenario's comment cites D70. No other comparator changes.

Remove when: Pig re-invokes TS/JS extension factories inside a retained Node runtime on session replacement (the additive `subprocess.RuntimeRetention` host API exists for it), reusing each module's cached factory as Pi's module-level extension cache does for a replacement in the same working directory (`loader.ts:128-148`), or when upstream reload and session replacement re-evaluate every extension module.

SCRUTINIZED:approved. The owner approved the session-replacement scope on 2026-09-29 (WSL QUESTIONS 04:50Z, lead note 04:50Z).

## D73 Live main-process object identity does not cross into Node extensions

What: inside an extension process, `@earendil-works/pi-coding-agent`, `@earendil-works/pi-tui`, `@earendil-works/pi-ai`, and `@earendil-works/pi-agent-core` export every runtime value that the Pi release vendored in `shims/pi-dist` exports. Imported objects execute in the extension process, not in the Go host.

Pi's own code, copied verbatim from the pinned release into `shims/pi-dist` with the third-party releases Pi depends on (`yaml`, `marked`, `get-east-asian-width`, `partial-json`, `highlight.js`, `ignore`, `diff`, TypeBox):
- pi-coding-agent: the complete published root module graph, including AgentSession, ModelRuntime, SessionManager, SettingsManager, ResourceLoader, tool factories and renderers, compaction, selectors, theme helpers, and the SDK factories. `createAgentSession` delegates to Pi's implementation and binds independent child cleanup to its extension connection. The main Go Session is not exposed as an imported JavaScript AgentSession.
- pi-agent-core, the whole package (its `Agent`, agent loops, harness, compaction, session storage and tools), with the chord and pi-telemetry modules it imports. As Pi's SDK does, the runtime installs pi-ai's compat `streamSimple` as the default stream function, so an extension's `Agent` streams through PiG's providers (D74).
- pi-tui: every public runtime export, including `ScrollView`, `Image`, image/capability/cell-dimension helpers, `ProcessTerminal`, `TuiMainScreen`, `TuiAltScreen`, and `getNativeClipboard`. The complete JavaScript module tree and Pi's native helper prebuilds are bundled. TypeScript-only types are not fabricated runtime classes. Caller-created screens operate on their supplied Terminal; importing a screen does not replace the host's live Main Screen.
- pi-ai: its compat entry point, which Pi serves for the pi-ai root and `/compat`, and `providers/all`, with only its builtin API implementations running in PiG's host (D74).

The imported pure helpers, tool definitions, components, and themes execute Pi's code. The host publishes its active palette to the extension process's global theme and publishes measured terminal dimensions, including `process.stdout.columns` and `process.stdout.rows`. D74 still governs built-in provider execution. Independent imported objects do not expose the Go Main Screen or let a prototype patch modify a Go component.

Independent file-backed stores use PiG's configuration root by default (D2). `PIG_USE_PI_DIRS=1` selects `PI_CODING_AGENT_DIR` or `~/.pi/agent` and project `.pi` instead. `SettingsManager` reads the selected project settings unless the caller passes `{ projectTrusted: false }`. Node uses the exact pinned proper-lockfile dependency. Go auth, model, settings and trust stores use the same directory-lock protocol, so a Node child can open those stores without encountering a regular-file sidecar from its host. PiG reclaims an empty regular sidecar from v0.2.0 only after its mtime passes the operation's stale threshold (10 seconds synchronous, 30 seconds asynchronous), taking its old OS lock and rechecking file identity and mtime. Fresh regular sidecars use normal contention retries or caller cancellation. A live older writer keeps its lock; stop that older PiG if acquisition times out. Only an empty regular file is a legacy sidecar. A nonempty file, a link and a lock directory at the lock path follow proper-lockfile's protocol, as in Pi: a fresh one is contention, and a stale one is removed with rmdir. That removes a stale lock directory, and on Windows a link to a stale directory, whose target stays. A stale nonempty file, a link to a stale file, and on Unix any stale link stay in place: acquisition fails with ELOCKED on Windows and ENOTDIR on Unix. A stale lock directory that is not empty stays in place, and acquisition fails with ENOTEMPTY. A dangling link stays in place, and acquisition reports ELOCKED. This recovers PiG-owned upgrade state; it does not change the directory protocol used by Pi and Node children.

`CustomEditor` is Pi's, a subclass of pi-tui's `Editor`, and `ctx.ui.setEditorComponent` installs the factory's editor the way Pi's `setCustomEditorComponent` does. The editor runs in the extension process: the factory receives a TUI (terminal size, `requestRender`, `terminal.write`, `setShowHardwareCursor`/`getShowHardwareCursor`), Pi's editor theme and the keybindings manager below; the host sends it the editor text, padding, autocomplete size and focus Pi copies, every key while it is installed, a left click on its rows in fullscreen mode, and each `setText`, `insertTextAtCursor` and `addToHistory` Pi's host performs on its editor; it shows the rows the editor renders, cursor marker included, and runs the default editor's handlers for its callbacks (`onSubmit`, `onChange`, `onEscape`, `onCtrlD`, `onPasteImage`, `onExtensionShortcut` and the `app.*` action handlers). Editor-local clear, follow-up clearing and idle bash Escape run synchronously in the component process; their host callbacks do not replay those text mutations. An input-completion notification holds the input pump until the component's callbacks reach the owner loop, without blocking rendering or accumulating later keys. Its border color follows the thinking level and bash mode as Pi's `updateEditorBorderColor` sets it, and its autocomplete provider asks the host, which answers with the suggestions PiG's own editor would show for the same text.

The keybindings manager handed to editor and `ctx.ui.custom` factories, and installed as pi-tui's global keybindings, is Pi's full table: every `tui.*` and `app.*` definition with the user's `keybindings.json` overrides, sent by the host with each state snapshot.

Partial, with the reason:
- The live Main Screen, arbitrary Go component references, and prototype patches to Go-owned UI objects do not cross into Node. Imported UI classes and `Theme` instances are independent objects. `initTheme` changes the extension process's theme; use `ctx.ui.setTheme` to request a host theme change.
- `ui.custom` publishes its mounted overlay handle and supports visibility, focus, bounds and unfocus controls. `unfocus({target})` focuses exactly the target when it is `null` or `undefined`, the extension's installed editor component, or another mounted overlay of the same extension, as Pi's `tui.ts:745-775` does. A component that the extension has not mounted has no main-process identity, so `unfocus({target})` with it throws instead of focusing it.
- An editor component's autocomplete provider carries no `triggerCharacters` from other extensions' provider wrappers; it uses Pi's default trigger characters.
- `CONFIG_DIR_NAME` and `getAgentDir` name PiG's configuration tree (D2).
- `getPackageDir` identifies the shipped private Node SDK and its assets, not the Node interpreter or PiG executable directory. The harness package also exports that SDK for absolute package-root imports.

No manufactured class/function stand-ins remain in the coding-agent or TUI package exports. The export audit is `test/parity/unit-evidence/node-shim-audit.md`; the restored TUI module tests and `test/parity/unit-evidence/fix-node-scrollview.md` qualify the TUI portion. The fabricated pi-ai `registerSessionResourceCleanup` and `cleanupSessionResources` values are removed because neither was a runtime export of the Pi release that the audit read. Live main-process identity remains restricted as described above.

Independent construction is implemented by the pinned SDK modules. `extensions-runtime/53-node-independent-session` exercises child-only models, provider hooks, built-in and custom tools, persistence and cancellation without changing the parent. The locked `pi-btw@0.6.1` replay exercises side requests, overlay reuse, cancellation and a subsequent independent main turn. See `test/parity/unit-evidence/fix-node-createagentsession.md`.

Why: extensions execute in a Node process beside the Go host. Independent Pi objects can run there, but the Go Main Screen and its component identities do not become JavaScript objects.

Observable effect: imported independent SDK objects work, while operations requiring live main-process component references fail explicitly. Editor input and frames cross the extension connection, so rendering follows the input round trip rather than an in-process call.

Call-site markers:
- `coding/extension/host/subprocess/runtime-node/overlay-handle.mjs`: an unfocus target the extension has not mounted.
- `coding/extension/host/subprocess/runtime-node/runtime.mjs`: the keybindings manager handed to `ctx.ui.custom` and editor factories, and the host capabilities seeded into pi-tui's cache.
- `coding/extension/host/subprocess/runtime-node/editor-component.mjs`: the editor component's wiring to the host.

Locked by: `coding/extension/host/subprocess` `TestNodeRuntimeShimsExportEveryPinnedPiValue`, which collects every runtime export of the upstream module Pi serves for each specifier (pi-coding-agent and pi-tui `src/index.ts`, pi-ai `src/compat.ts` and `src/providers/all.ts`, pi-agent-core `src/index.ts`, following `export *`) and fails if the loader's module lacks any of them; `TestVendoredPiDistMatchesThePinnedPackage` (the copied Pi code and third-party packages equal the pinned release); `TestPiTuiComponentsMatchThePinnedPackage` and `TestPiAiUtilitiesMatchThePinnedPackage` (the runtime's modules render, handle input and compute byte-identically to the pinned packages); `TestNodeCustomFactoryGetsKeybindingsAndFocus`; `TestNodeExtensionSeesPiProcessIdentity` (`getPackageDir`); `TestNodeStateSeedsTerminalCapabilitiesForMarkdown`; `TestPiSettingsManagerMatchesThePinnedPackage`; `TestNodeSettingsManagerSharesPigSettings`; `TestNodeEditorComponentIsPisCustomEditor`; `TestRemoteEditorLocalClearDoesNotReplayOverNextKey`; `TestPiThemeHelpersMatchThePinnedPackage` (the theme helpers against Pi's own theme and keybinding-hints modules for the dark theme); `TestPiToolFactoriesMatchThePinnedPackage` (the tool factories' shape, metadata and results against Pi's); and `TestNodeRuntimeParseFrontmatterMatchesPi`; `TestImportedRegistryAndSessionClassesMatchPi`; `TestNodeComposedProviderAuthMatchesPi`; `TestNodeOverlayUnfocusSendsExplicitTarget`, `TestUIBridgeResolvesOverlayUnfocusTarget` and `internal/codingagent` `TestRemoteOverlayUnfocusExplicitTargetMatchesPi` (explicit null and mounted-overlay unfocus targets through the Node handle, Host and TUI).

Parity allowance: no paired scenario asserts arbitrary live Go component identity from a Node extension. `extensions-runtime/53-node-scrollview-footer` proves real ScrollView rendering and retained state, with unrelated extensions preserving the footer slot. `TestPiTuiPublicExportsAreRealImplementations` and `TestNodeVendoredTuiUpstreamTests` exercise the restored pi-tui exports against pinned Pi. See `test/parity/unit-evidence/fix-node-scrollview.md` for the real gentle-pi package comparison. `extensions-runtime/35-custom-editor-component` compares SettingsManager-backed modal input and rendering; `35b-editor-action-sync` proves the same-turn clear. `extensions-runtime/31-extension-runtime-surface` compares pi-agent-core and the read and write tool definitions against Pi.

Remove when: live main-process object operations preserve Pi's identity and callback behavior across the extension boundary, or upstream removes that object-sharing contract.

SCRUTINIZED:approved

## D74 Pi-ai's builtin API implementations run in PiG's host

What: inside an extension process, pi-ai's compat layer, API registry, lazy API wrappers, model catalog and env-key lookup are Pi's own code (D73). The ten builtin API implementations they load (`anthropic-messages`, `openai-completions`, `openai-responses`, `azure-openai-responses`, `openai-codex-responses`, `google-generative-ai`, `google-vertex`, `mistral-conversations`, `bedrock-converse-stream`, `pi-messages`) are replaced by a bridge. For each request the bridge applies that API's upstream credential check (`options.apiKey`, the headers that stand in for one, or ambient credentials for Vertex and Bedrock) and upstream's `Request aborted` for a signal that already fired, then streams the request through PiG's Go port of the same provider: `options.apiKey` owns the request ahead of every configured credential, `options.reasoning` sets the thinking level (a `stream()` call and a `streamSimple()` call without one run without reasoning, not at the session's level), headers and env pass through, and an aborted `options.signal` cancels the host request. `openrouter-images` `generateImages` returns an error result, because PiG has no image-generation provider.

Why: the upstream implementations import the vendor SDKs (`@anthropic-ai/sdk`, `openai`, `@google/genai`, `@aws-sdk/client-bedrock-runtime`), which PiG does not ship to extensions, and PiG already carries parity-tested Go ports of these providers, which its own agent uses. Running every builtin API through one bridge keeps credential resolution and request behavior uniform. Owner-directed launch P0 fix (Reddit report on 2026-09-26: pi-hermes-memory calls `completeSimple` from `@earendil-works/pi-ai/compat`).

Observable effect: an extension's `stream`/`complete`/`streamSimple`/`completeSimple` sends the same request (credential, model, messages) and receives the same event and result shapes as under Pi, from PiG's provider; a request aborted in flight carries the Go provider's abort message rather than the vendor SDK's. Provider-specific `stream()` options beyond the common ones (for example Anthropic `thinkingEnabled`) are not forwarded, results lack `responseId` and `rawStopReason`, and `generateImages` for OpenRouter returns an error result where Pi generates images. A direct pi-ai call also carries the provider attribution headers that PiG's Go provider adds to every model it builds (`coding.BuildModel`, `providerAttributionProvider` in `coding/model.go`): OpenRouter's `HTTP-Referer`, `X-OpenRouter-Title` and `X-OpenRouter-Categories`, and NVIDIA NIM's `X-BILLING-INVOKE-ORIGIN`, with PiG's values (D26) and subject to the install-telemetry setting. Pi's pi-ai sends only its own `User-Agent` (`packages/ai/src/api/openai-completions.ts:760`) on a direct call; Pi adds attribution headers only in the coding agent's request path (`packages/coding-agent/src/core/provider-attribution.ts`, `sdk.ts:326`).

Call-site markers:
- `coding/extension/host/subprocess/runtime-node/shims/pi-ai-bridge.mjs`: the bridge the vendored `api/<api>.js` stubs load.
- `coding/model.go`: `providerAttributionProvider.Stream`, which adds the attribution headers to a direct pi-ai call that the bridge streams through `coding.BuildModel`.

Locked by: `coding/extension/host/subprocess` `TestVendoredPiDistMatchesThePinnedPackage` (every vendored pi-ai file is verbatim except the listed bridge stubs), `TestNodeRuntimeShimsExportEveryPinnedPiValue` (the compat surface), and `TestNodeCompatCompletionAbortCancelsHostRequest` (the extension's `apiKey` reaches the host, no session thinking level is applied, and an aborted signal cancels the host request).

Parity allowance: no paired scenario runs an extension's direct provider call; the Pi-extension end-to-end run compares pi-hermes-memory's consolidation request and result against the pinned Pi over a scripted OpenAI-compatible server.

Remove when: PiG ships the vendor SDKs to extensions and runs upstream's API implementations, or upstream removes the compat entry point's global dispatch.

SCRUTINIZED:approved

## D78 SDK Provider object carriers

What: the remaining SDK Provider object surface is a documented known gap for 0.3.x. Registered native Providers have callable cross-process handles, but Go, Rust and Python `getProvider` cannot retrieve every builtin/composed raw Provider and report the unavailable carrier explicitly. Registered configuration authored or read by a Go, Rust or Python extension is a snapshot, not a transparent live alias of the author's object. Callable capabilities do not imply shared container identity. Between Node processes the effective root crosses by reference (xref, from lane gap-xproc); a cycle of references between two processes is collected only when one of them exits.

Why: Pi retains actual Provider/configuration objects in one JavaScript heap. JSON cannot preserve arbitrary author-held aliases, getters/setters, method receivers or subsequent unproxied writes across address spaces. A safe shared-reference design also needs source ownership, captured child/function identity, descriptor semantics, validation of unused cyclic metadata, and distributed lifetime/cycle handling. A reader facade alone does not implement that contract. The builtin/composed native SDK representation also lacks parts of Pi's raw Provider surface.

Observable effect: a native SDK configuration reader, or any reader of a native SDK author's configuration, cannot use reference equality or a local property mutation to observe or update the author's object. Captured children and functions must not be described as live remote aliases merely because a method can be invoked. Native SDK callers can encounter the explicit builtin/composed carrier error. Node's independent builtin/composed objects still use Pi's factories. Implemented stream/auth/callback methods keep their existing contract.

Scope: only the named SDK surface and cross-process configuration identity gaps. When caller and referents share a Node process, the accepted configuration root, shallow root replacement, original children/functions, descriptors and receivers must remain Pi-exact. A same-process copy, stale registration lookup or incorrect rollback is not authorized by the foreign-data boundary. Members of one Node cell now share one effective root per provider, validate an incoming registration before merging it over that root, pass the root as `streamSimple`'s receiver, and hand cleanup ownership to the latest registrant (`TestNodeRegisteredProviderConfigMatchesPi` against Pi's ModelRegistry, `TestNodeConfigProviderRootAcrossCellMembers` through the production host). Node processes now see the same effective root across processes: a reader's root write is shared, the author's child writes are visible, and a partial re-registration in another process merges over the author's root and takes ownership (`TestRegisteredProviderConfigAcrossProcessesMatchesPi`, `TestRegisteredProviderConfigXrefLifetime`). A partial re-registration by or over a native SDK author still does not merge the other process's fields. The later configuration-carrier checkpoints are not integrated; approval is not a claim that they are qualified. OAuth credentials now keep Pi's exact `expires` value, its absence and every provider-owned key through the host, auth storage and all four SDKs (`TestCredentialExpiryMatchesPi`, `TestConformance_OAuthTransportsMatch`, `TestNodeOAuthBridge`). Node OAuth callbacks receive Pi's signals (`refreshToken(credentials, signal)` with the caller signal, composed with Pi's 15 s timeout only for the stored-credential resolution that Pi composes it in, login's interaction signal); a retained refresh signal stays live after the callback returns and follows the caller's later abort (`TestNodeOAuthSignals`, `TestNodeOAuthRefreshSignalFollowsItsCaller`). Implicit refresh, post-callback signal lifetime for native SDK callbacks, mixed-owner replacement/rollback across native SDKs and resource qualification remain explicitly incomplete in the Provider work, not silently certified by this entry.

Owner decision: 2026-09-28, owner Michael Kinsy approves recording the remaining Provider surface and the foreign registered-config identity proposal as visible known gaps for 0.3.x. This supersedes D78's pending scrutiny, not its implementation obligations. It grants no comparator relaxation, fabricated callback result, compatibility reader or general lifecycle waiver.

Call-site markers:
- `extensions/sdk/provider_proxy.go`, `extensions/sdk-rs/src/context.rs`, and `extensions/sdk-py/pig_sdk/__init__.py`: builtin/composed raw Provider retrieval.
- `coding/extension/host/subprocess/runtime-node/runtime.mjs`: the native SDK author's registered-configuration snapshot returned by `getRegisteredProviderConfig`.

Evidence: `TestProviderObjectsAcrossSDKs`, `TestNodeRemoteProviderObjectCarrier`, `TestProviderObjectReferenceLifetime`, and `TestNativeProviderRegistrationWinsBeforeAuthCompletes` cover implemented methods, not full configuration identity. `test/parity/unit-evidence/fin-d78-carriers.md` and `docs/findings/0.3.0-known-gaps.md` distinguish that integrated evidence from subsequent SDK checkpoints. Pi source: `packages/coding-agent/src/core/model-runtime.ts:438-443,744-797` returns retained objects and creates a fresh shallow effective configuration; `packages/coding-agent/src/core/model-registry.ts:162-171` delegates those getters.

Parity allowance: record the named unavailable native SDK members and foreign configuration snapshot identity as known differences. Do not treat registration or method conformance as proof of author-held aliasing. Preserve distinguishing same-process and cross-process assertions; a source checkpoint awaiting integration is not passing evidence for this tree.

Remove when: all SDKs expose the complete raw Provider surface and a specified, race-free cross-process reference mechanism preserves Pi's author/reader aliases, captured children/functions, descriptors, receivers and lifetimes, with isolated/packed/fused caller, cancellation, replacement, cleanup and resource evidence. Re-review the record at each upstream leap and remove closed subscopes independently.

SCRUTINIZED:approved

## D79 User-package metadata ignores the invoking project's npm configuration

What: metadata lookups for user-scoped (global) npm packages run from that source's managed npm install root, including an explicit registry source's managed root. PiG retains the caller's selected `npmCommand` argv and user/environment configuration. This applies to explicit package updates and available-update checks, including commands that select npm, pnpm or Bun. Trusted project-scoped packages continue to query from the project cwd. Missing user roots are initialized as managed npm projects; an inaccessible root fails the lookup instead of falling back to the invoking directory.

Why: security. upstream 0.99.2 runs `npm view` in the invoking project even when project settings are denied. A repository's `.npmrc` can redirect global-package metadata requests, expose queried package names and influence update decisions without project approval. The owner selected the minimal cwd policy, not a new package-manager configuration sandbox.

Pi source: `.upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:1181-1195` invokes the lookup for updates; `1515-1534` invokes it for available-update checks; `1545-1564` retains the selected command and explicitly passes `cwd: this.cwd`; `2674-2694` forwards that directory to the subprocess. `package-manager-cli.ts:750-756,932-948` supplies the trust-resolved settings but does not isolate native npm configuration.

Scope: user-package metadata only. The approved difference applies even when the invoking project is trusted, because scope determines the lookup directory. Trusted project packages retain Pi's cwd and registry behavior. The user's configuration and explicit command arguments can still select a registry or configuration file. Relative command paths and arguments now resolve from managed storage for user lookups; use absolute paths for wrappers or configuration files that must live elsewhere. This rule does not sandbox package-manager code, scrub the environment or alter self-update ownership.

Call-site marker: `cmd/pig/package_npm_metadata.go`: `getLatestNpmVersion`, the shared scope-to-cwd decision.

Locked by: `cmd/pig/package_registry_scope_test.go` (`TestNpmMetadataLookupScope`, `TestPackageUpdatePerformsScopedMetadataLookup`, `TestNpmMetadataLookupRefusesUnavailableRootAndUntrustedProject`, and `TestNpmMetadataLookupCreatesManagedRootForLegacyInstall`). The selected-command test covers npm, pnpm and Bun argv with both scopes. The real npm loopback scenarios `project-trust/19-user-package-registry-isolation` and `20-trusted-project-package-registry` require a metadata lookup and no reinstall for a current package. The first records PiG's user registry versus Pi's project registry as the expected divergence; the second compares the complete measured result exactly.

Remove when: upstream isolates user-package metadata from the invoking project's configuration with equivalent command/configuration preservation, or the owner explicitly approves a different security boundary.

Approval: approved by owner Michael Kinsy 2026-09-27 (option A).
SCRUTINIZED:approved

## D80 Configurable secret-input privacy

What: `maskSecretInput` is a boolean setting with default `true`. `/settings` exposes **Mask secret input** and explains that false restores Pi's plain-text behavior. Masked prompts show up to eight dots, a grapheme count and the last four graphemes. Inputs shorter than five graphemes expose no suffix. Submitted dialog history retains only the preview. The hint reads `Input hidden (PiG default). Show like Pi: /settings → Mask secret input`.

False restores upstream 0.99.2's complete ordinary prompt and submitted-text rendering without the extra count or hint. Ordinary text and manual-code prompts are unaffected. A new dialog captures the setting; change it in `/settings`, then reopen `/login`. Normal project/global precedence applies. Pi ignores the extra JSON key and preserves it when updating shared settings.

Authentication progress and errors redact masked input, including trimmed credential forms. Login input is not appended to Session messages. The authentication flow and authorized credential store still receive the credential: display privacy does not encrypt auth.json or a provider-owned store, and the shown suffix is intentionally visible.

Why: the owner requires verification by length and suffix without echoing a complete secret, with a Pi-compatible opt-out. Pi's `packages/ai/src/auth/helpers.ts:12-16` declares secret prompts, but `packages/coding-agent/src/modes/interactive/interactive-mode.ts:6085-6093` routes them to ordinary showPrompt. The host owns authentication input and diagnostics; the approved default changes Stock PiG's visible behavior without activating a product workflow.

Call-site markers:
- `internal/codingagent/settings.go`: default and persistence.
- `internal/codingagent/slash_session_handlers.go`: the settings row.
- `internal/codingagent/interactive_auth.go`, `interactive_llama.go` and `slash_commands.go`: prompt policy and diagnostic redaction.
- `tui/login_dialog.go`: preview, count, hint and retained content.

Locked by: `TestLoginDialogMaskedPreview`, `TestLoginDialogSecretValueNeverRendered`, `TestLoginDialogMaskDisabledMatchesPi`, `TestMaskSecretInputSettingsRoundTrip`, `TestMaskSecretInputSettingsMenuAppliesToNextDialog`, `TestLoginMaskSettingReachesStandardDialog`, `TestPiIgnoresMaskSecretInputInSharedSettings`, `TestMaskedLoginErrorDoesNotEnterFramesOrSession`, and standard/llama.cpp prompt tests.

Parity allowance: the owner-approved enabled default differs from Pi's plain-text input and is guarded by masked-preview, redaction and persisted-history tests. `test/parity/scenarios/oauth/14-login-secret-mask-disabled.toml` compares the disabled production dialog against real Pi with escaped-output equality.

Remove when: upstream provides equivalent configurable privacy, or PiG removes the option.

Ratification: owner decision 2026-09-27 requires the configurable default-on feature, replacing unconditional masking. D80 retains that approval; its classification here does not change behavior or extend the approved scope.
SCRUTINIZED:approved

## D82 Cross-process partial-message observation (RPC33)

What: a partial assistant message that crosses the process boundary is an independently owned snapshot rather than Pi's live JavaScript reference. An extension process receives each event as JSON taken at dispatch; a partial it retains does not acquire the producer's later mutations. A provider implemented in an extension process delivers one snapshot per event rather than one mutated object. The host cannot see how many microtasks a foreign handler spends: it resumes after the handler's reply as an I/O completion, so the stream state that PiG's own listeners observe after an awaited foreign handler can differ from Pi's in-process count.

Same-process observation is Pi-exact for every builtin API on PiG's HTTP/1 transport. Each provider body runs as a turn of a JavaScript-order executor that models Node 24.19.0's promise, async-generator, Web Streams and `Readable` rules and the exact Pi or SDK generator chain, and each delivery boundary (event iterator, Agent listener, Session listener) materializes the partial at its own tick. See `plans/0.3.x/gap-d82.md` for the design.

Why: a JSON frame cannot remain an alias of a heap object in another process, and a foreign runtime's microtask queue is not observable from the host. An owned cross-process reference/proxy mechanism would be required.

Observable effect: an extension `message_start`/`message_update` handler that keeps the event and reads it after an `await` sees its dispatch-time snapshot, where Pi's handler sees the advanced object. With an extension handler that awaits, the RPC/JSON record serialized after it can show a different amount of buffered provider progress than Pi. Without extension handlers the records match Pi: strict `rpc/33-rpc-real-provider-records` and its Anthropic, Google and Mistral variants pass all declared pairs.

Scope: only foreign (extension-process) listeners and extension-process providers. It does not cover native same-process behavior. The following same-process producers still publish emission-time snapshots because they do not yet run under the executor, and are open defects rather than approved differences: `agent.StreamProxy` (Pi `proxy.ts`), the Codex WebSocket transport, and Bedrock responses outside an observable HTTP/1 connection (HTTP/2, caller-supplied clients). Event presence and order, deltas, parsed arguments, start before body data, terminal results, persistence, cancellation and tool execution are not waived.

Owner decision: 2026-09-28, owner Michael Kinsy approves this visible known gap for 0.3.x for the cross-process scope above. The narrowing on 2026-09-29 removes the native-observation part the original record covered; it does not extend the approval.

Call-site markers:
- `ai/assistant_publication.go`: `publishPartialLocked` stores an emission-time snapshot for a producer outside the executor (the extension-provider stream and the same-process producers listed as open defects).

Evidence: `plans/0.3.x/gap-d82.md`, `coding/testdata/rpc33-observation/providers/` (Pi start-state oracle and per-API tick-order oracles), `docs/findings/0.3.0-known-gaps.md`. `node coding/testdata/rpc33-observation/providers/check.mjs <pig> 200` reports 200/200 runs equal to Pi for openai-completions, openai-responses, azure-openai-responses, openai-codex-responses (SSE), anthropic-messages, google-generative-ai, mistral-conversations, bedrock-converse-stream, pi-messages and test-faux. Pi source: `packages/ai/src/utils/event-stream.ts:44-91`, `packages/ai/src/api/lazy.ts:31-61`, `packages/agent/src/agent-loop.ts:408-453`, `packages/coding-agent/src/core/agent-session.ts:894-919`, `packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363`.

Parity allowance: none needed for the hermetic scenarios; `rpc/33-rpc-real-provider-records*` keep `json_output_equal`, `stderr_equal` and three runs. No scenario exercises the foreign-retention difference as a pass.

Remove when: an owned cross-process reference mechanism lets extension-process listeners and providers share Pi's live partial object and report their handler's microtask boundary, with isolated/packed conformance tests, or upstream adopts the snapshot contract.

SCRUTINIZED:approved

## D83 Cross-process event-bus scheduling boundaries

What: every Node process is one realm, and all realms share one `pi.events` bus. A listener in another realm receives the emitter's original payload through cross-process references (`coding/extension/host/subprocess/runtime-node/xref.mjs`): identity, synchronous-prefix mutations, reentrant dispatch, retained and post-await aliases, functions with receivers, private fields, accessors, symbols, Maps, Dates and non-configurable properties behave as in Pi's single heap. Five boundaries remain:

1. Atomicity at synchronous waits. Each operation on a foreign object is atomic in its owner. A synchronous run is not atomic against an independent task in another realm: a foreign request can run while the realm waits in a synchronous host call, and independent tasks in different realms (including macrotasks such as timers and `setImmediate`) are ordered by timing, where Pi orders them in one event loop.
2. Microtask interleaving beyond the first post-await generation. A foreign listener reached while its realm is idle waits after its synchronous prefix until the emitter's first microtask after `emit`, then drains its own microtasks before the emitter continues. The first continuation therefore runs where Pi runs it. Later microtask-only generations of that listener run before the emitter's later microtasks, where Pi interleaves them in one FIFO queue. A listener reached while its realm is already waiting in a synchronous call runs its continuations when that wait returns.
3. Cross-realm cycles. A reference graph that crosses realms in a cycle is released only when one of its realms exits; Pi collects it. Acyclic foreign aliases are collected as in Pi.
4. Proxy brand checks, `isProxy` and three inspect prints. A foreign object is a proxy: internal-slot brand checks such as `util.types.isMap(value)`, `Map.prototype.get.call(value, key)` or `JSON.stringify` of a boxed primitive fail in another realm, and `util.types.isProxy(value)` is true where Pi's is false. V8 offers no inspection hook in three cases, so Node formats the proxy itself or its shadow target instead of the owner's object. In the `customInspect: false` and non-extensible cases, Node 26.0.0 and later wrap that output in `Proxy(...)` (nodejs/node#61029, SEMVER-MAJOR, first released in 26.0.0); Node 25 and earlier, including the pinned 24.19.0, print it unwrapped. The three cases:
   - `util.inspect` with `customInspect: false` (as `console.dir` calls it) prints the empty shadow: `Proxy(Object <[Object: null prototype] {}> {})` on Node 26.0.0 and later, `Object <[Object: null prototype] {}> {}` on Node 25 and earlier. Pi prints the object, for example `{ k: 1 }`.
   - `util.inspect` with `showProxy: true` (`%o`) shows the Proxy wrapper of a foreign payload, `Proxy [ <shadow>, <handler> ]`, where Pi prints the object. This holds on every Node version.
   - `util.inspect` of a non-extensible foreign payload (frozen, sealed or `Object.preventExtensions`) after the reader queries or changes its extensibility prints Pi's output inside `Proxy(...)` on Node 26.0.0 and later, for example `Proxy({ k: 1 })`. On Node 25 and earlier it prints exactly as in Pi, because the shadow holds the owner's own properties. Before that query it prints as in Pi on every version.

   Every other inspect, clone and write case must match Pi.
5. Native realms. Go, Rust and Python extensions have no `pi.events` API (no Pi counterpart); native data would cross by value.

Why: several processes have several event loops. Pi's guarantees that depend on one thread and one microtask queue can be reproduced for the synchronous prefix and the first continuation, but general interleaving would require serializing all extension JavaScript in every process, and distributed cycle collection has no bounded design yet (D78 shares it). Proxy-based identity cannot carry V8 internal slots.

Observable effect: code that relies on Pi's bus contract (same object, prefix mutations visible after `emit`, reentrant `emit`, later writes seen through retained aliases, callbacks run in their owner) behaves as in Pi across processes. Code that races independent tasks in different processes, depends on second-generation microtask order, builds cross-process reference cycles, brand-checks a foreign object, calls `util.types.isProxy` on it, prints one with `customInspect: false` or `showProxy: true`, or on Node 26.0.0 and later prints a non-extensible one can observe a difference.

Scope: only the five boundaries above. Any other inspect, clone or write difference is a defect, not part of this record. Two open defects found by REVIEW rev-xproc-r-r are tracked here, targeted at 0.3.1; they are not approved and are not part of this record: R3 b, a Promise, iterator, generator, `arguments` object, module namespace or buffer over 64 MiB does not count as a nesting level of the enclosing layout; R3 c, `structuredClone` and `v8.serialize` copy objects from two owners that reference one common object separately. Listeners in the same realm keep Pi's actual `EventEmitter` path with the original object. Listener order, `newListener`/`removeListener`, per-emit snapshots, unsubscribe, the unhandled `error` rule, failed-factory cleanup and lifetime of acyclic payloads must match Pi and are not covered by this record.

Owner decision: 2026-09-28 approval of the foreign-data boundary, narrowed 2026-09-29 by owner Michael Kinsy (Q2 = A) to exactly these five boundaries; everything else must match Pi.

Owner decision: 2026-09-29, owner Michael Kinsy approves boundary 4's effects: on Node 26, `util.inspect` of a foreign non-extensible payload, or with `customInspect: false`, prints `Proxy(...)` because V8 offers no hook (Opus R3 Node 26 print); `util.inspect` with `showProxy: true` shows the Proxy wrapper for a foreign payload (Opus R3 a); and `util.types.isProxy()` returns true for a foreign payload (Opus R3 d). Every other inspect, clone and write case matches Pi. R3 b (Promise nesting level) and R3 c (cross-owner shared clone) are not approved: they stay open defects tracked under Scope, targeted at 0.3.1. This resolves QUESTION xproc-r.

Call-site markers:
- `coding/extension/host/subprocess/runtime-node/runtime.mjs`: `pi.events.emit`, the shared-bus dispatch boundary, and the listener wait after a foreign prefix.
- `coding/extension/host/subprocess/runtime-node/xref.mjs`: the proxy used for foreign objects, and the release of foreign aliases.

Evidence: Pi `packages/coding-agent/src/core/event-bus.ts:12-33` passes one object to each listener and runs each synchronous prefix before `emit` returns. `coding/extension/host/subprocess/xref_event_bus_test.go` runs each scenario against Pi's own vendored `createEventBus` in one heap and against packed, strict-isolation and mixed PiG topologies: `TestXrefEventBusMatchesPiAcrossRealms`, `TestXrefEventBusListenerOrderMatchesPi`, `TestXrefEventBusPayloadLifetimeMatchesPi`, `TestXrefEventBusReentrantDispatchMatchesPi`, `TestXrefEventBusCrossingEmittersDoNotDeadlock`, `TestXrefEventBusForeignPrefixMutationMatchesPi`, `TestXrefEventBusIntegrityLevelsMatchPi`, `TestXrefEventBusRejectedWritesMatchPi`, `TestXrefEventBusInspectMatchesPi`, `TestXrefEventBusInspectNestedLayoutMatchesPi`, `TestXrefEventBusInspectReadsOnlyWhatNodePrints`, `TestXrefEventBusFormatFollowsToStringChanges`, `TestXrefEventBusCloneMatchesPi`, `TestXrefEventBusCloneLocalGraphsMatchPi`, `TestXrefEventBusSymbolLifetimeMatchesPi`, `TestXrefEventBusMaxListenersWarningIsSilentLikePi` and `TestXrefOwnerExitFailsRetainedAlias`. `TestXrefEventBusInspectProxyBoundaryIsStated` pins the approved prints against Pi's output for the running Node major (wrapped on 26.0.0 and later, unwrapped before), the approved `showProxy` and `isProxy` effects, the open layout defect listed under Scope, and that the payload arrives by reference (the listener's write reaches the emitter's object) on both. It passes on Node 24.19.0 and 26.7.0. Same-realm paths remain covered by `TestNodeRuntimeEventBusMatchesPi`, `TestNodeCellInterleavedGoFactoryKeepsOrderAndBus`, and scenarios `extensions-runtime/31-extension-runtime-surface` and `52-interleaved-node-admission`. Design: `plans/0.3.x/gap-xproc.md`.

Parity allowance: no paired scenario runs two extension processes; the unit tests above compare PiG's full Host path with Pi's bus in one heap. Same-process paired scenarios remain strict.

Remove when: cross-realm scheduling is serialized into one task and microtask order, distributed cycle collection releases cross-realm cycles, and foreign objects pass internal-slot brand checks, or upstream restricts its event bus to a process-local contract.

SCRUTINIZED:approved

## D84 Direct Go provider registration refreshes availability on the next awaited call

What: a Go caller that registers, replaces or removes a Provider directly on the Model Registry or the Model Runtime (`coding.ModelRuntime.RegisterProvider`, `RegisterNativeProvider`, `UnregisterProvider`, the equivalent `ModelRegistry` methods, and in-process callers such as the llama host), outside an extension host, gets Pi's synchronous projection immediately: the catalog, provisional configured auth and the removed models. Pi's unawaited local refresh (`void this.refresh({ allowNetwork: false })`) does not run by itself. It starts at that caller's next awaited model-runtime call (`Refresh`, `GetAvailable`, `CheckAuth`, `Login`, `Logout`, the catalog refresh coordinator). Extension-host registrations match Pi: `cmd/pig` starts the queued refresh when the host callback returns, and startup and `/reload` start it after every extension factory has loaded.

Why: Pi's refresh is a continuation that lands on the event loop's next turn, after the registering caller's synchronous code and before any later I/O completes. Go has no implicit yield. A goroutine started by the registration would run Provider callbacks during the caller's own request sequence, which is not Pi's order: `providers-faux-streaming/23-metadata-refresh-and-native-result` prints Pi's `0,2` request counters only because no callback runs inside that sequence. No Go mechanism reproduces the JavaScript turn boundary for an arbitrary direct caller.

Observable effect: a direct Go caller that registers a Provider and then reads only synchronous state (`GetAvailableSnapshot`, `HasConfiguredAuth`, `IsUsingOAuth`, `GetError`) sees the provisional projection, not the result of the Provider's auth check or local catalog refresh, until it awaits a model-runtime call. A Provider whose check would reject a provisional configured entry stays listed until then, and a refresh failure reaches `GetError` only after that call.

Scope: only registrations made directly on the Go Model Registry or Model Runtime, with no extension host and no `cmd/pig` host binding. Extension-host registrations in every mode, the startup refresh, `/reload`, provider requests, scoped and unscoped refresh semantics, supersession, cancellation, `Services.Close` draining and error reporting are not waived and must match Pi.

Owner decision: 2026-09-29, owner Michael Kinsy approves this divergence for direct Go registrations, because a background goroutine would break Pi's observable request order (scenario 23) and Go has no implicit yield. Approval covers only the scope above.

Call-site marker: `internal/codingagent/model_refresh_background.go`: `deferRegistrationRefresh` queues the refresh without starting it.

Evidence: `docs/parity/model-availability-task-ownership.md` (registration refresh contract), `TestNativeRegistrationRefreshRunsOnTheCallersYield` and `TestRegisterProviderPublishesProvisionalConfiguredAuth` in `coding/model_registration_refresh_test.go` (no callback before a yield; the awaited call runs the queued refresh), `TestPostStartupNativeProviderRegistrationBecomesAvailableWithoutAnotherCall` in `cmd/pig/startup_native_provider_test.go` (extension-host registrations need no further call), and `test/parity/scenarios/providers-faux-streaming/23-metadata-refresh-and-native-result.toml`. Pi source: `packages/coding-agent/src/core/model-runtime.ts:744-797`.

Parity allowance: no paired scenario exercises a direct Go registration because Pi has no Go caller; the paired scenarios cover extension-host registrations and remain strict. Unit tests above lock the queued, awaited-call behavior.

Remove when: Go gains a turn boundary that can run the refresh after the registering caller's synchronous work without running Provider callbacks inside its request sequence, or upstream awaits the registration refresh.

SCRUTINIZED:approved

## D85 RPC stdin end: a suspended extension command's continuation can run before its runtime stops, and one runtime's suspended session_shutdown does not keep another runtime's command alive

What: when RPC stdin ends, PiG decides for each extension command whether Pi's process would still have answered it. A command answers if it settles inside the microtasks, ticks and check phase of its line's own event-loop iteration, or while a `session_shutdown` handler that awaits a timer or I/O keeps the process alive. Two differences remain in that decision.
1. PiG stops a Node runtime process only after the host reaches its exit decision. A command handler that Pi never resumes (its continuation is a timer, file I/O, a nested `setImmediate` or a child process) is suspended in Pi's exited process. In PiG its continuation can still run in the runtime process between the suspension report and the host stopping that process, so its side effects (a file write, a recorded event, a host call the host still accepts) can happen. Pi never runs them. PiG never prints the held response.
2. A suspended quit `session_shutdown` handler keeps Pi's process alive, so a suspended command can still settle and answer. PiG observes this only within one runtime process: a suspended `session_shutdown` handler in one runtime process does not keep a command of another runtime process alive. Node factories normally pack into one process, so this is reachable only with an extension in its own runtime process (a quarantined or isolated Node extension) or a non-Node runtime whose handler is suspended. Since the per-process drain reports (`runtime_drained`, `runtime_commands_drained`) the host also counts a command of another isolated Node process as able to answer until that process reports that nothing keeps its own loop alive (`TestDrainSpansIsolatedNodeProcesses`, `TestDrainAwaitsIsolatedNodeCommandThatWaitsForSessionChange`). That covers a command that awaits a session change, a Promise or a dialog; no test covers a command that a suspended quit handler in another process keeps alive, so this difference is recorded as remaining.

Why: extensions run in runtime processes outside the host. The host learns that a handler is suspended from a `request_state` frame and must then stop the process, so the continuation races the stop. An exact match would need the host to freeze or stop the runtime before the suspension frame is sent, which stays racy, or a keepalive aggregated across runtime processes with a new wire message for handler suspension. Pi runs every extension on its one event loop (`rpc-mode.ts:726-742,802-805`, `output-guard.ts:105-108`).

Observable effect: (1) an extension whose command continues through a timer or I/O after stdin ended can perform that continuation's side effects under PiG and not under Pi, in a short window before its runtime stops. Stdout is identical. (2) With extensions in separate runtime processes, a command that Pi answers because another extension's shutdown handler holds the process open is not answered by PiG.

Scope: only RPC stdin end, and only these two cases. Outside them, which commands answer, the held responses, the per-command window, the ordering of suspensions before responses, pending dialogs, `session_shutdown` delivery and exit codes are not waived and must match Pi.

Owner decision: 2026-09-29, owner Michael Kinsy approves this divergence ("RPC shutdown residuals from process-per-extension hosting") because extensions run in separate runtime processes and the exact fixes are neither cheap nor safe now. Approval covers only the scope above.

Call-site markers: `coding/extension/host/subprocess/runtime-node/runtime.mjs`: `armRequestWindow`. `coding/extension/host/subprocess/host.go`: `setQuitHandlerSuspended`. `cmd/pig/rpc_shutdown_test.go`: the `afterExit` tolerance in `TestRPCInputEndAfterExtensionCommandComparedWithPi`.

Evidence: `TestRPCInputEndAfterExtensionCommandComparedWithPi` (stdout and the extension's event records against Pi for each command shape, with and without the default shutdown handler, plus a sibling extension), `TestRPCInputEndWindowClosesBeforeNextPollComparedWithPi` (stdout against Pi over repeated runs of the window-edge shapes), `TestRPCInputEndCommandSettlesDuringSlowShutdownHandler` and `TestRPCInputEndJoinsEachExtensionCommand` in `cmd/pig/rpc_shutdown_test.go`, `TestCommandFlightsSuspendPerCommand` in `coding/extension/host/subprocess/command_flight_test.go`, and `TestConformance_SuspendedCommandFlush` in `test/extension-conformance/command_flush_test.go`. The stdin-end contract is in `docs/extension-api-parity.md`.

Parity allowance: the Pi rows of the comparison tests are strict. The PiG rows drop the command's own event record for the shapes Pi does not answer (`afterExit`), which is difference 1. No test covers difference 2.

Remove when: the host stops or freezes a runtime process before a suspended continuation can run and the stdin-end window is aggregated across runtime processes, or PiG runs extensions on one shared event loop as Pi does.

SCRUTINIZED:approved

## D86 RPC settle tail: a settle-tail handler of a runtime that cannot report its wait holds stdin

What: in RPC mode Pi emits `agent_settled` in the microtasks that follow a published `agent_end`, so its stdin reader, which runs between macrotasks, reads no line between them (`agent-session.ts:1044-1058,1752-1777`, `rpc-mode.ts:355-360,808-810`). When an `agent_before_settle` or `agent_settled` handler awaits a timer, I/O or another real wait, Pi reads stdin while it waits. PiG reads stdin on its own goroutine, so `rpcSettleGate` holds each stdin batch, and stdin's end, from a published `agent_end` until `agent_settled` is written. The gate is open during a compaction, an auto-retry delay, a new run, an extension dialog and a settle-tail handler whose wait the host can see: a Node handler reports a suspended window to the host, as a command does. A handler of a runtime with no microtask continuation (the Go, Rust and Python SDKs) reports no window. While such a handler runs, the gate stays closed. A command sent during its wait is answered after the handler settles and `agent_settled` is written, where Pi answers it during the wait. After stdin's end, such a running handler counts as waiting, so shutdown does not wait for it.

Why: the host learns of a wait only from a runtime's report, and those runtimes have no equivalent of the Node window that closes unresponded. Without the gate, a command sent on seeing `agent_end` would be answered before `agent_settled`, which Pi never does.

Observable effect: with an `agent_before_settle` or `agent_settled` handler in the Go, Rust or Python SDK that waits on a timer or I/O, a command sent while it waits is answered later than in Pi: after `agent_settled` instead of before it. Output order for every other case, including every Node handler, is as in Pi.

Scope: only a settle-tail handler of a runtime that reports no window, and only a command or stdin end sent during its wait. Outside that case the gate matches Pi and is not waived.

Owner decision: 2026-10-01, owner Michael Kinsy approves this divergence as a temporary one ("Yes go with recommendations", in the lead session). Approval covers only the scope above.

Call-site markers: `cmd/pig/rpc_settle_gate.go`: `rpcSettleGate`.

Evidence: `TestRPCCommandAfterAgentEndAnswersAfterAgentSettledComparedWithPi`, `TestRPCCommandDuringSuspendedSettleTailAnswersComparedWithPi` and `TestRPCInputEndDuringSuspendedSettleTailShutsDownComparedWithPi` in `cmd/pig/rpc_input_end_order_test.go` (Pi and PiG for Node handlers), `TestRPCInputEndDuringGoSDKSettleTailHandlerShutsDown` in `cmd/pig/rpc_shutdown_drain_go_test.go`, `TestRPCSettleGate*` in `cmd/pig/rpc_settle_gate_test.go` and `TestSettleTailCountsHandlersPiServesStdinDuring` in `coding/extension/host/subprocess/command_window_test.go`.

Parity allowance: the Pi comparison rows cover Node handlers only. No test compares a command sent during a Go, Rust or Python handler's wait with Pi, because those handlers have no Pi counterpart.

Remove when: the Go, Rust and Python SDK runtimes report a settle-tail handler's suspension to the host, as the Node runtime does with a command's window, so the gate opens during their waits.

SCRUTINIZED:approved

## D87 PiG's versions of Pi's easter eggs

What: Pi 1.0.0 has two easter eggs that show Pi's art. PiG plays them with its own art.
1. Clicking the header logo in fullscreen mode (`interactive-mode.ts:256-262,1058`, `pi-logo-animation.lazy.ts`, `pi-logo-animation.ts`). The clickable logo is PiG's pig head (D2): cells 1 to 16 of the header's first seven lines, where Pi's logo takes cells 1 to 4 of two, or the 4 cells of the one-line `PiG.` text mark where it stands in for Pi's logo; Apple Terminal has no clickable logo, as Pi's text wordmark has none. The object that flies, grows and spins is that head, built from the active sprite's head pixels and colors (`/sprite`), not Pi's three-color logo. Where Pi's logo shuffles its blocks as a sliding puzzle (from 4.4 s, one 5.2 s cycle at a time), the spinning head gives way for the 3.6 s of Pi's shuffle to a side-view running pig, 38 pixels by 22, ray cast in braille dots with the head's shading and blocks the size of the head's, so it is more than twice the head's width: rounded body, ears, eye, snout with nostrils, cheek, curly tail and dark outline, four legs in a four-frame run cycle with a one-pixel bob. It runs in place where the head spins. In the first 0.6 s of the run the head's braille dots dissolve one by one into the pig's while the pig turns from the head's spin to face the camera, and in the last 0.6 s the pig dissolves back into the head the same way while it turns back to the head's spin, so no frame is empty or a hard swap; Esc during the run dissolves the pig back into the head over 0.6 s while Pi's exit plays. The head is back, whole and spinning, for Pi's 1 s return and 0.6 s hold. The ray caster holds up to 65535 faces where Pi's holds 255 (`Uint8Array`), because the running pig has more, and renders the head and the pig as two layers during a dissolve. The running pig takes the active sprite's colors: a color the head draws keeps its color, a body the head does not draw as `P` is the head's most common color (Kratos's skin, Spider-Ham's suit), a body too dark for a dark outline is outlined in its highlight (Vader), and characters keep accessories (the Sheriff's hat and star, Vader's red panel, Kratos's tattoo, Piglet's shirt, Spider-Ham's web, PiGrogu's robe). The dissolve, its timing and dust, the 3D ray caster and shading, the light-background halo, the starfield, the `escape to return` hint, the spin locked to the cycle, the reverse exit, the keys (`tui.select.cancel` and `app.clear`, a second press skips the exit) and the mouse handling are Pi's.
2. `/arminsayshi` (`interactive-mode.ts:3266`, `armin.ts`) draws a 31 by 34 pig head labeled `pigsayhi` instead of Armin's 31 by 36 image labeled `ARMIN SAYS HI`. The seven effects, their random selection and order of random calls, and their timing are Pi's. `/pigsayhi` is a second name for the same easter egg; Pi sends `/pigsayhi` to the model as an ordinary prompt.

`/dementedelves` keeps Pi's announcement, which still says "pi has joined Earendil" (`earendil-announcement.ts`), and the OpenCode Kimi easter egg (`daxnuts.ts`) is not ported.

Why: PiG never presents itself as Pi (D2). Pi's logo animation and Armin's portrait are Pi's art; the owner wants PiG's own versions of them.

Owner decision: 2026-10-01, owner Michael Kinsy ("PiG versions of Pi's eggs").

Call-site markers: `internal/codingagent/pig_logo_animation.go` (the object, `pigLogoBlocks`, the whole head in `blockOffsets`), `internal/codingagent/pig_logo_run.go` (the running pig), `internal/codingagent/startup_header.go` (`handleBuiltInHeaderMouse`), `internal/codingagent/armin.go` (the image and label) and `internal/codingagent/startup_input.go` (`/pigsayhi`).

Evidence: `TestPigLogoAnimationMatchesPinnedPi` runs the pinned `pi-logo-animation.ts` with the pig's blocks and geometry and compares every frame of the flight, spin, dissolve, starfield, hint, halo, exit and skip with the Go port, without the running pig's layer. `TestPigLogoRunCycle` (the run's timeline and frames, the dot-by-dot dissolves into and out of the pig and on Esc, never fewer dots than either shape shows alone, the turn to and from the camera, the braille pig in place of the head and its return), `TestPigRunTurnStaysInFrame` (the turning pig stays within the face-on pig's size and the screen, and turns the short way), `TestPigLogoRasterHoldsMoreThan255Faces` (the wider face index), `TestPigRunFramesGolden` (`internal/codingagent/testdata/pig-run/<sprite>.golden`, each built-in sprite's four frames and their colors; `docs/plan/progress/egg-run-pig/run-strip.png` shows the ray-cast frames), `TestPigLogoAnimationDrawsThePigNotPisLogo`, `TestClickingTheHeaderPigPlaysTheAnimation`, `TestClickingTheTextMarkPlaysTheAnimation`, `TestHeaderPigClickNeedsTheBuiltInSprite`, `TestHeaderPigClickIgnoredWhileAnOverlayOrTheAnimationIsOpen`, `TestPigLogoAnimationColors` and `TestPigLogoAnimationTimer` cover the running pig, the art, the click, the overlay and the timer. `TestArminFramesMatchPinnedPi` runs the pinned `armin.ts` with the pig head, `TestArminSaysHiDrawsThePigHead` pins the art and label, and the `TestHiddenEasterEggs*` tests cover `/pigsayhi`.

Parity allowance: `test/parity/scenarios/fullscreen/11-logo-click-animation.toml` compares the hint and starfield rows with Pi; the rows the 3D object covers differ. `test/parity/scenarios/slash-commands/13-armin-bitmap.toml` asserts each binary's own settled image and label.

Remove when: never; the easter eggs are PiG's.

SCRUTINIZED:approved

## D88 PiG runs its own first-time setup with a sprite step

What: Pi 1.0.0 shows its first-time setup dialog (`first-time-setup.ts`: theme with live preview, then analytics opt-in) only for its official distribution with `PI_EXPERIMENTAL=1`, the default agent directory and no `settings.json` (`startup-ui.ts:122-140`, `main.ts:674`). PiG shows it on an interactive start in the default agent directory that holds no `settings.json` yet, which includes every fresh `PIG_HOME`. As in Pi, a custom agent directory (`PIG_CODING_AGENT_DIR`) skips it (`startup-ui.ts:144-146`). Print, JSON and RPC modes never show it. Esc skips setup and saves nothing.

The dialog is a port of Pi's component with three steps: theme (System, Dark, Light, with live preview), then sprite, then analytics. The sprite step lists the fifteen built-in sprites in a scrolling window of eight rows with the position below it, then a last item, `Create your own...`. The dialog's logo is the pig head of the highlighted sprite on the sprite step and of the chosen sprite after it, never Pi's `SETUP_LOGO_LINES`. The welcome line says `Welcome to PiG` where Pi's says `Welcome to pi`, and the analytics text names PiG instead of Pi (D2). Finishing saves the theme and analytics choice in `settings.json` as Pi does, and saves the sprite exactly as `/sprite set` does (`$PIG_HOME/state/pig-standard/login.json`). `Create your own...` shows how to create a sprite (`/login`, `/model`, then `/sprite create`), and finishing with it keeps the active sprite. `/sprite create` sends a guided turn that writes a TypeScript extension registering the sprite with `ctx.ui.registerSprite` when a model with credentials exists, and explains `/login` otherwise.

PiG shows the dialog as Pi does: on its own startup screen before runtime services start, after migrations and before session selection (`main.ts:672-676`). Extensions are not loaded yet, so the sprite step offers only the built-in sprites; extension sprites are chosen later with `/sprite`. The gate is observed before startup writes any file.

Why: PiG never presents itself as Pi (D2), so Pi's official-only setup never runs for PiG; the owner wants a first-run setup that also chooses the sprite.

Owner decision: 2026-10-02, owner Michael Kinsy (first-run-sprite, release 0.4.0).

Call-site markers: `cmd/pig/main.go` (gate observation), `cmd/pig/extensions.go` (`shouldRunFirstTimeSetup`), `internal/codingagent/first_time_setup.go` (`FirstTimeSetupComponent`, the welcome line, `ShowFirstTimeSetup`).

Evidence: `TestShouldRunFirstTimeSetupGate` (interactive with and without `settings.json`, print, piped stdin, JSON, RPC), `TestFirstTimeSetupOriginalCasesAsFork` and `TestForkedDistributionDoesNotBlockOnOfficialFirstTimeSetup` (Pi's original gate inputs against PiG's real CLI), `TestFirstTimeSetupEscSkipsOnInteractiveStart` and `TestFirstTimeSetupSavesThemeSpriteAndAnalytics` (real CLI in a PTY), `TestFirstTimeSetupSpriteStepListsExtensionSpritesAndSubmits`, `TestFirstTimeSetupKeepsTheHighlightedSpriteThroughAnalyticsAndSubmit`, `TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI`, `TestShowFirstTimeSetupSkipSavesNothing`, `TestFirstTimeSetupHintUsesLowercaseKeyText`, `TestSpriteCreate`, `TestSpritePickerCreateYourOwnShowsTheHint`, `TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite`, `TestFirstTimeSetupThemePreviewAndEscSkip` and `TestFirstTimeSetupLogoIsThePigHeadAndPreviewsTheSprite`. Pi's analytics cases in `first-time-setup.test.ts` stay ported unchanged (`TestAnalyticsSettings`).

Parity allowance: Pi never shows the dialog for a fork, and parity fixtures carry `settings.json`, so no paired scenario compares it.

Remove when: never; the first-run setup is PiG's.

SCRUTINIZED:approved

## D93 /reload evaluates an edited ES module extension again

What: Pi 1.0.0 imports each extension through jiti with `moduleCache: false` (`core/extensions/loader.ts:569-572`, `loadExtensionModule`), so `/reload` evaluates a TypeScript or CommonJS extension and its local imports again. jiti 2.7.0 hands a file it treats as an ES module, an `.mjs` file or a `.js` file under `"type": "module"`, to Node's own `import()` when it imports asynchronously (`eval_evalModule`), and Node keeps an ES module for the life of the process by its URL. Pi therefore keeps running an edited ES module, an extension's entry or a local module it imports, until it restarts, even when the edit is a syntax error. This also holds for an `.mjs` module that a TypeScript extension imports. A TypeScript extension's own source runs its edit. This was probed with the pinned Pi in RPC mode.

PiG's runtime hooks record, for each local extension module that Node loads, the source it loaded and the extension modules each evaluation imports. Local means outside the runtime and outside `node_modules`. When a reload pass starts, PiG gives a new URL, with a `pig-reload=<pass>` query, to each ES module whose source changed since Node loaded it and to each ES module that imports one, directly or through others. The resolve hook resolves every import of such a module to that URL, so the reload evaluates exactly those modules again:
- Every other module keeps its instance and its state, as in Pi.
- Extensions, TypeScript ones included, that import one edited module share its one new evaluation.
- An edit that breaks a module fails each extension that imports it on `/reload`, as an edited TypeScript extension's does in Pi, and the next valid edit loads it again.
- A module that an edit no longer imports stops counting.
- An installed package under `node_modules` keeps its module, and so does a CommonJS module, which Node keeps in its `require` cache: an edit of either takes effect after a restart, and does not evaluate its importers again.
- A later unedited reload keeps the latest evaluation, and a Session replacement never evaluates a module again.
- An old generation that still serves until the swap and imports such a module dynamically gets the new evaluation.
- A module evaluated again has the query in its `import.meta.url`, in what `import.meta.resolve` returns for it, and in the file names of its stack traces.
- An import that adds its own query, such as `import("./x.mjs?t=1")`, counts as an import of `x.mjs`, so an edit of `x.mjs` evaluates the importer again.
- The hooks need Node's synchronous module hooks (`module.registerHooks`, Node 22.15 or later); with an older Node release PiG keeps Pi's behavior.
- Node keeps every evaluation of a module until the process ends.

Why: fixed ahead of upstream. The owner fixes a non-destructive inherited Pi bug in PiG when the fix only stops a failure and changes nothing for a user who does not hit it, and reports it upstream.

Owner decision: 2026-10-04, owner Michael Kinsy (FIXLANE 2: fix the `.mjs` reload in 0.4.1 instead of deferring it; ID assigned by the lead).

Call-site markers: `coding/extension/host/subprocess/runtime-node/loader.mjs` (`track`, `reevaluateEdited`).

Evidence: these tests are in `coding/extension/host/subprocess`.
- `TestHostReloadEvaluatesOnlyAnEditedESModuleExtensionAgain` (`.mjs` and `"type": "module"` `.js`, packed and isolated):
  - an unedited reload keeps the module;
  - an edit of a static import, a dynamic import, or a module first imported since the last reload evaluates it again;
  - a later unedited reload keeps the new evaluation;
  - an edited installed package keeps its module.
- `TestHostReloadKeepsAnUneditedSharedModuleOfPackedESModuleExtensions`: shared instance kept, then one new shared instance.
- `TestHostReloadKeepsOneSharedEvaluationAcrossAFailedImporter`.
- `TestHostReloadForgetsAnImportTheEditRemoved`.
- `TestHostReloadGivesTypeScriptAndESModuleImportersOneNewEvaluation`.
- `TestHostReloadKeepsAnESModuleWhoseCommonJSDependencyChanged`.
- `TestNodeReloadEvaluatesAnEditedTypeModuleJsEntryAgain`: an edit runs, a broken edit fails the load, and the fix loads it again. It replaces the test that pinned Pi's behavior.
- `TestSessionReplacementKeepsAnEditedESModuleUntilAReload`.
- `TestHostReloadKeepsMjsModuleStateAndReevaluatesTsModules`: Pi's rule for an unedited `.mjs` module.
- `TestReloadRunsTheCurrentSourceOfACommandLineExtension/node-mjs` (`cmd/pig`): the real CLI over RPC.

Each test fails with an earlier runtime: the CLI test with the runtime before D93, and the last four regression tests with the first D93 runtime. Each of ten mutations of the rules fails at least one test. Loading 19 more ES module extensions in one process takes the same 30 ms as before D93.

Parity allowance: Pi keeps the old code of an edited ES module, so no paired scenario compares the reload; the tests above pin PiG's behavior.

Remove when: Pi's loader evaluates an edited ES module extension again on `/reload` (upstream issue drafted 2026-10-04).

SCRUTINIZED:approved

## D94 Subprocess editor working status runs from host snapshots

What: When a subprocess editor opts into embedded working status, Pi runs the StatusIndicator's own animation timer inside the editor and re-checks `isWorkingStatusEditor` each time a status is shown. PiG advances the animation on the host and sends the editor snapshots (`RemoteEditorStatus`), so the JavaScript side builds the indicator with empty initial frames and takes the frames and frame index from the snapshot. PiG also reads the opt-in once, at editor install, and the Go host places the status from it; an editor that changes `embedWorkingStatus` afterwards is not re-checked. The working colors match Pi: the editor's own `borderColor`, read at render time.

Why: One animation timer on the host keeps the spinner in step with the rest of the UI across the process boundary, and the host decides placement before the editor can answer.

Owner decision: 2026-10-05, owner Michael Kinsy (pr137).

Call-site markers: `coding/extension/host/subprocess/runtime-node/editor-component.mjs` (install opt-in and indicator construction), `coding/extension/remote_editor.go` (`RemoteEditorStatus`).

Evidence: the Node editor test in `coding/extension/host/subprocess/runtime_node_editor_component_test.go` compares border rows with Pi's own CustomEditor and StatusIndicator.

Parity allowance: observable rows match Pi; only timer ownership and opt-in re-check timing differ.

Remove when: the editor protocol can return a live opt-in and snapshots are replaced by an editor-side timer.

SCRUTINIZED:approved
