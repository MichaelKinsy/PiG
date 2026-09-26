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

- D47 — Width stripping consumes DEC private-mode set/reset sequences. Retired by the width-parity change. `tui/widthx.ExtractAnsi` now delegates to the upstream-compatible `ExtractAnsiCode`; the ID remains reserved. `TestExtractAnsi_PrivateModeMatchesUpstream` and `TestPiWidthDifferential` verify the shared ANSI parsing behavior. No active divergence or source marker remains.
- D71 — Nonfatal main-screen overflow recovery. Withdrawn on 2026-09-25 by owner decision; the ID remains reserved. PiG now matches upstream `tui-main-screen.ts`: an over-wide non-image row that reaches the differential-render loop writes the TUI crash log (`pig-tui-crash.log` in the agent directory), stops the TUI, and ends the process through the uncaught-exception path with status 1. Initial, full, and resize renders emit the row unchanged. Tests: `tui/render_overflow_test.go` and parity scenario `extensions-runtime/20-differential-render-overflow-terminates.toml`. No active divergence or source marker remains.

## Active divergences (29)

## D2 PiG uses a separate command and configuration identity

What: the command, startup banner, and terminal title use `pig`. Configuration
uses `~/.pig`. A binary resolved under the name `pi` exits with status 2 instead
of shadowing Pi.

Why: separate command and configuration identities prevent accidental writes to
Pi state.

Remove when: never.

Call-site markers: `cmd/pig/guard.go`, `cmd/pig/main.go`, `internal/codingagent/paths.go`, and `internal/codingagent/startup_header.go`.

The built-in header follows Pi's compact and expanded help layout. Only the logo and onboarding product name use PiG's identity; config/bin/cwd reports and a separate readiness paragraph are not part of this exception.

Locked by: `parity/scenarios/startup/00-startup-banner.toml`, `02-startup-compact-help.toml`, `03-startup-expanded-help.toml`,
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

## D26 Provider attribution headers are pig-branded

What: upstream's `mergeProviderAttributionHeaders` (provider-attribution.ts)
sends telemetry-gated attribution headers for OpenRouter
(`HTTP-Referer: https://pi.dev`, `X-OpenRouter-Title: pi`,
`X-OpenRouter-Categories: cli-agent`), NVIDIA NIM
(`X-BILLING-INVOKE-ORIGIN: Pi`), and Cloudflare (`User-Agent: pi-coding-agent`),
plus an always-on OpenCode session-correlation pair
(`x-opencode-session: <id>`, `x-opencode-client: pi`) that upstream's
`getSessionHeaders` emits regardless of the telemetry gate. pig's
`mergeProviderAttributionHeaders` (`coding/model.go`) matches that shape
file-for-file with pig branding substituted for every pi-branded value:
OpenRouter gets `HTTP-Referer: https://github.com/MichaelKinsy/PiG`,
`X-OpenRouter-Title: PiG`, `X-OpenRouter-Categories: cli-agent`; NVIDIA gets
`X-BILLING-INVOKE-ORIGIN: PiG`; Cloudflare gets `User-Agent: pig-coding-agent`;
OpenCode gets `x-opencode-client: pig` (still unconditional on telemetry, only
gated on a session ID being present, matching upstream). The OpenRouter/
NVIDIA/Cloudflare headers are gated on
`SettingsManager.IsInstallTelemetryEnabled()`, matching upstream's
`getDefaultAttributionHeaders` telemetry gate exactly.

Why: every upstream attribution value is pi-branded (`pi.dev`,
`X-OpenRouter-Title: pi`, `X-BILLING-INVOKE-ORIGIN: Pi`, `pi-coding-agent`,
`x-opencode-client: pi`). A rebranded port must not misattribute its traffic
as pi, so pig substitutes pig branding host-for-host and header-for-header
instead of narrowing upstream's provider set. Only the literal string values
change; the provider/host matching, the telemetry gate, and the
always-on OpenCode session pair are unchanged from upstream.

`IsInstallTelemetryEnabled` mirrors upstream `isInstallTelemetryEnabled`
(telemetry.ts:8): `PI_TELEMETRY` env truthiness wins when set, otherwise the
`enableInstallTelemetry` setting (default true). This is the same gate that
also covers the install/update ping, sent to PiG's own `pi-in-go.dev`
endpoint instead of pi.dev (D64; `internal/codingagent/install_telemetry.go`).

Remove when: pig sends byte-identical (unbranded) attribution headers to
upstream, which would require pig to claim it is pi — not planned.

Call-site markers:
- `coding/model.go`: the OpenRouter/NVIDIA/Cloudflare branch and the
  OpenCode session pair in `mergeProviderAttributionHeaders`
- `internal/codingagent/settings.go`: `IsInstallTelemetryEnabled`,
  `isTruthyTelemetryEnvFlag`
Locked by: `internal/codingagent/settings_test.go` -
`TestSettingsManager_IsInstallTelemetryEnabled`;
`coding/provider_attribution_0861_test.go` -
`TestMergeProviderAttributionHeadersMatchesPinnedProvidersAndHosts`,
`TestProviderAttributionWrapperReachesHTTPRequest`;
`coding/model_test.go` -
`TestBuildModelGatesAttributionHeadersOnInstallTelemetrySetting`.
PORT_MAP path: `packages/coding-agent/src/core/provider-attribution.ts`,
`packages/coding-agent/src/core/telemetry.ts` (setting/env gate ported; the
install-report ping it also gates is not wired in yet).
SCRUTINIZED:approved

## D27 Word navigation classifies per grapheme, not per Intl.Segmenter word segment

What: upstream's `findWordBackward`/`findWordForward` (`word-navigation.ts`) segment text with `Intl.Segmenter` (ICU UAX#29 word
segmentation) and skip whole word-like segments, breaking at internal ASCII
punctuation via `PUNCTUATION_REGEX`. pig's `prevWordStart`/`nextWordEnd`
(tui/editor.go) classify each grapheme cluster as
whitespace / ASCII-punctuation / word and skip runs of like graphemes.

Parity: identical for ASCII/Latin/code, the dominant editing case. pig's
`punctuationChars` set is byte-for-byte the same as upstream's
`PUNCTUATION_REGEX` (both exclude `_`, so `foo_bar` stays one word), and
`isWhitespaceGrapheme` mirrors `/\s/`. For runs of word characters separated
only by whitespace or ASCII punctuation the two approaches converge
(`foo.bar`, `don't`, `abc123`, `...` all produce the same cursor stops).

Diverges only for scripts with intra-run word boundaries that have no
whitespace or ASCII punctuation between words: CJK, Thai, etc. There ICU
segments linguistic words (Japanese 学生/です) while pig treats the whole
non-whitespace run as a single word and skips it in one move.

Why deferred, not ported: a faithful port would replace the hot editor-nav
path (every Ctrl/Alt-arrow and Ctrl+W / Alt-Backspace / Alt-D) with the
already-vendored `github.com/clipperhouse/uax29/v2/words` segmenter. Two
problems block a verified port: (1) uax29's UAX#29 trie is not guaranteed
byte-identical to ICU's `Intl.Segmenter` (emoji ZWJ sequences, contractions,
script-specific tailoring), so even a careful port cannot reach verified
parity with pi for the very scripts it targets; (2) rewriting the common-case
nav for a niche CJK improvement risks regressing the dominant path. pig
matches Pi's cursor stops for text whose words are separated by whitespace or
ASCII punctuation (code and most Latin-script prose); CJK word-nav is a
tracked fidelity gap, not a design choice.

Remove when: CJK editing ergonomics are prioritized AND uax29-vs-ICU word
segmentation is confirmed equivalent for the target scripts (or the residual
difference is accepted). Port path: rewrite `prevWordStart`/`nextWordEnd`
using `words.FromString`, deriving `isWordLike` from segment letter/number
content, preserving the `PUNCTUATION_REGEX` internal-boundary skip.

Call-site markers:
- `tui/editor.go`: `prevWordStart`, `nextWordEnd`
- `tui/graphemes.go`: `punctuationChars`, `isPunctuationGrapheme`,
  `isWhitespaceGrapheme`
Locked by: `tui/components_test.go` -
`TestEditor_WordBoundaryHelpers`; `tui/components_grapheme_test.go` -
`TestEditorWordMovementPunctuationAndCrossLine`.
PORT_MAP path: `packages/tui/src/word-navigation.ts` (grapheme-classification
adaptation; ASCII-parity, CJK gap).
SCRUTINIZED:approved

## D30 Extension runner is host-scoped, not invalidated on in-process session switch

What: upstream's `AgentSession.dispose()` (agent-session.ts:717) calls
`_extensionRunner.invalidate(staleMessage)` so that a `pi`/command `ctx`
captured before a session replacement (`ctx.newSession()`, `ctx.fork()`,
`ctx.switchSession()`, and the `/new`, `/fork`, `/resume` UI flows that drive
them) throws `ErrStaleContext` on reuse. Upstream owns one `_extensionRunner`
per `AgentSession`, so disposing the outgoing session invalidates exactly that
runner and the incoming session gets a fresh one.

pig uses a single host-scoped extension runner
(`coding/extension/host/inproc/runner.go`) shared across in-process session
switches: `Session.ReplaceInner` (the `/resume` chokepoint) swaps only the
inner session and keeps the same runner, and the `/new` and `/fork` handlers
reuse `m.newRunner`. The invalidation mechanism is fully ported: `Invalidate`,
the byte-identical stale message, and the `ErrStaleContext` sentinel: and it
fires on the two points where pig genuinely builds a new runner:
`ctx.reload()` (`internal/codingagent/reload_resources.go` invalidates the old
runner, then constructs a new one) and runtime close (`coding/runtime.go`).
pig does not invalidate on `/new`, `/fork`, or `/resume` because the same
runner serves the incoming session; calling `Invalidate` there would wedge the
runner for the new session.

Observable effect: an extension that captures a `ctx` and reuses it after an
in-process `/new`, `/fork`, or `/resume` operates against the current session
instead of throwing `ErrStaleContext`. The rest of `dispose()` is covered:
`cleanupSessionResources` runs in `Session.Close()`, the `session_shutdown`
event is emitted on quit/switch, and in-flight compaction/branch-summary are
aborted on `/resume` with agent/bash/compaction teardown on signal/quit
via `context.Context` propagation.

Parity allowance: the tmux parity harness cannot currently drive this: reproducing it
needs a stateful subprocess extension that captures a `ctx` in one handler and
reuses it after a user-driven session switch, which the harness has no way to
drive or observe. Tracked as an explicit parity allowance.

Why deferred, not ported: a faithful fix requires per-session extension
runners (each `/new`/`/fork`/`/resume` builds a fresh runner and re-registers
every extension), a structural change to pig's subprocess host model with
real restart/re-register cost and broad blast radius, for a behavior the
upstream stale-ctx message explicitly warns authors not to rely on. The
capture-and-reuse-across-switch pattern it guards against is misuse; pig's
ctx is request-scoped in normal use.

Call sites:
- `internal/codingagent/interactive.go`: `NewSession` (`/new`),
  `ForkToNewSession` (`/fork`), `LoadSessionPath` (`/resume`).
- Contrast (runner genuinely replaced, so invalidation fires):
  `internal/codingagent/reload_resources.go` (reload),
  `coding/runtime.go` (runtime close).

Remove when: pig adopts a per-session extension runner and invalidates the
outgoing runner on `/new`, `/fork`, and `/resume`.

SCRUTINIZED:approved

## D35 Animated easter-egg commands (/arminsayshi, /dementedelves) not ported

What: upstream pi registers two hidden easter-egg slash commands handled inline
in the submit handler (`interactive-mode.ts` handleArminSaysHi/handleDementedDelves,
absent from the canonical `slash-commands.js` completion list). `/arminsayshi`
renders an animated 31×36 XBM bitmap (`components/armin.ts`, ~330 lines);
`/dementedelves` renders a bundled-PNG announcement (`components/earendil-announcement.ts`,
image asset `clankolas.png` + dynamic border). Pig does not port either; typing
them falls through to the normal unknown-slash path (forwarded to the model).
The functional sibling `/debug` and its ctrl+shift+d hotkey ARE ported.
Why: both are cosmetic hidden easter eggs whose faithful port is ~350 lines of
animation plus a bundled binary image asset, for no functional behavior. Porting
them would add speculative surface against the least-code rule; the observable
gap is limited to users who already know the undocumented command names, and
neither appears in autocomplete or /help on either side.
Remove when: pig ports the animated art and bundled image asset, or upstream
drops the easter eggs.
Parity allowance: outside the upstream parity denominator; these commands are
hidden (never listed in autocomplete or /help), so no completion-set scenario
diverges.
Call-site markers:
- `internal/codingagent/slash_commands.go`: "Not ported (upstream easter eggs
  ... See DIVERGENCES.md D35)" note in the removed-commands block.
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
Missing/malformed/mismatched checksums fail before staging. The signed current
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
The package-manager tier invokes the proven owner's exact `install -g`
command; immutable-binary and container tiers emit exact pull/rebuild/redeploy
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
release build. A Marketplace installer that used an explicit private CA copies
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
closing DynamicBorder). The one omission is upstream's trailing `Changelog:
https://pi.dev/changelog` line: that URL is a hardcoded pi-product page with no
generic pig equivalent, so pig drops it rather than bake a dead or wrong link.
Only the update *mechanism* diverges: upstream updates via the package manager,
pig replaces the standalone binary. The self/package command surface matches upstream (`pi update` = self, `--self`, `--extensions`, `--all`,
`--extension`, `--force`, positional package source, and conflict handling),
with `pig` replacing Pi's product name. Upstream's separate `--models` remote
catalog refresh remains unported and visible as pending in PORT_MAP; D39 does
not claim that model-catalog surface.

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

What: when `HERDR_ENV=1`, Pig ignores inherited outer-terminal image hints
unless Herdr explicitly sets `HERDR_KITTY_GRAPHICS=1`. Without that positive
signal, image components render their compact textual fallback and reserve no
graphics rows. Direct Ghostty/Kitty/WezTerm/iTerm sessions retain upstream
capability detection.

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
`parity/scenarios/providers-faux-streaming/09-orphaned-tool-result-wire.toml`
drives the identical poisoned conversation through pig's `NormalizeMessages` and
pinned pi 0.84's openai-completions transform against a hermetic endpoint; its
`[diverge]` block asserts pig omits the orphaned `call_x` from the provider
request (`tool_call_ids_in_request: []`) while pi sends it (`[call_x]`).
Ratification: user-ratified (2026-08-10 review: approved, "do it properly").
SCRUTINIZED:approved

## D50 Unrenderable Mermaid diagrams say why

What: when pig cannot draw a Mermaid diagram it appends one line naming the
cause after the raw code block, for both causes it can distinguish:

- grammar rejection: `Mermaid diagram not rendered: could not parse sequence
  diagram; a ';' inside a statement splits it, so quote the text`
- area overflow: `Mermaid diagram not rendered: diagram is 380 columns wide,
  area is 76; shorten the longest label or split the diagram`

Upstream `renderMermaidToken`
(`packages/coding-agent/src/modes/interactive/components/mermaid.ts`) returns the
raw block silently for both, so an undrawn diagram is indistinguishable from a
plain ```mermaid fence.

Why: the silent fallback is unactionable for the reader. The
first observed case was a `;` inside a sequence message (`A->>B: seq [1;1:1A]`),
which mermaid treats as a statement separator, so the message parses as a bogus
second statement and the whole diagram fails. Nothing in the rendered output
indicated that. The alternative
considered and rejected was a system-prompt guideline: upstream's prompt has no
formatting section at all, the advice would be paid for on every request, and it
would not help a diagram that fails for some other reason.

Who sees the hint: the reader, and only the reader. The hint is produced by a
Markdown render transform, so it exists in the painted lines and not in the
assistant text the session stores and later requests carry. The model that wrote
the diagram cannot read it and does not correct itself; a person has to relay it.
This is a real limit on the value claimed below, and pig has no model-visible
feedback channel to route it through: neither pig nor upstream has an ephemeral
or system-reminder context mechanism. Bound by
`TestTheMermaidHintNeverEntersModelVisibleText`.

The overflow case was added after it cost three round trips in one session. A
model sent a `graph LR` that laid out at 380 columns, saw a raw fence, and
twice diagnosed a syntax cause that measurement later disproved: cycles, orphan
nodes and nested subgraphs all render fine. Silence makes an overflow read as a
syntax error, so the author edits the grammar instead of the width. Reporting
both numbers ends it in one turn once a person passes the measurement on.

The original scope excluded overflow on the grounds that hinting there changed
30 of 74 corpus cases versus 1 for the parse path. That count is real but is
not a production rate: 40 of the 74 fixtures probe the width boundary at 1–50
columns, narrower than any real message area. Measured by width, the fixtures
hint 3 of 33 times at 80/100/200 columns and 0 of 3 at both 50 and 100. The
flood is confined to a pathologically narrow window, and those 3 are diagrams an
author should genuinely shorten.

Scope is deliberately narrow:
- Only diagrams pig declines to draw diverge. A drawn diagram is byte-identical
  to upstream.
- Hints are suppressed while streaming, where a half-typed diagram fails on
  nearly every delta and a growing one exceeds the area on most of them.
- Overflow stays silent when the area is unmeasured (`AvailableWidth <= 0`,
  early layout): "0 columns available" is not a claim an author can act on.
- The line reuses upstream's own `Mermaid diagram not rendered: ` prefix, code
  span, and `warning` theme colour, already emitted when the grammar drops
  statements, so all unrendered outcomes read identically.

Call sites: `internal/codingagent/mermaid_transform.go`: `renderMermaidToken`
(the `!ok` and `art.Width >` branches), with `mermaidHint`,
`unrenderableReason`, `oversizeReason`, and `hasInlineSemicolon`.

Locked by: `internal/codingagent/mermaid_hint_test.go` (cause named for a
semicolon and for an unrecognized diagram type, no semicolon blamed when absent,
trailing `;` renders unhinted, both hint paths suppressed while streaming,
overflow names both widths, unmeasured width stays silent, good diagram renders
unhinted, and the hint stays out of the stored assistant text) and
`TestHasInlineSemicolon`. Mutation-proven: reverting either branch
to upstream's `return raw` reddens the matching cause test, and routing the
rendered lines back into the stored text reddens the visibility test.

Parity coverage: explicit allowance in
`internal/codingagent/mermaid_transform_test.go`. The byte-for-byte golden diff
against pi's own compiled transformer still runs over all 74 cases; pig's output
must equal pi's plus exactly one well-formed hint line, so a dropped diagram,
altered art, or any other drift still fails. 31 of 74 cases currently differ, 30
of them the width-boundary fixtures described above.

Remove when: upstream reports a reason for diagrams it does not draw, or the
Mermaid renderer is replaced by one that accepts `;` inside statement text and
reflows to the available width.

Ratification: user-ratified for the grammar path ("It feels like the error
helper is more context efficient and I feel it's not bad if it's just a hint",
after confirming pi's system prompt has no markdown section to extend), and
user-ratified for the overflow path in a later session ("Yes let's modify the
divergence to be more useful", after the silent overflow cost three round trips
and the per-width hint rate was measured).
SCRUTINIZED:approved

## D51 A signal-terminated session hands the terminal back

What: when a signal terminates interactive mode, pig hands the terminal back
before exiting: it pops the extended-key protocols it pushed and restores the
cooked state it captured at startup. Upstream does neither.

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
and `-isig` while a bash tool runs. SIGINT is ignored while suspended, matching
upstream's `ignoreSigint` listener, so a backgrounded session is not killed by a
signal delivered on resume.

Why: after `kill -INT`, pi 0.84.0 leaves the terminal at `-isig`, so the user's
shell no longer has a working Ctrl+C until they run `reset`. Verified against pi
0.84.0 on a pty that outlives the process, alongside a clean-exit control that
restores `isig`, and confirmed by hand in a real terminal. A tmux-driven
end-to-end check disagreed and was wrong: tmux encodes Ctrl+C as CSI-u
(`\x1b[99;5u`) when extended-keys is on, so it never delivered an INTR byte. Reproducing that corruption has no upside, and the fix is one
`tcsetattr` against a state captured at startup.

Only the termios restore runs from the signal handler. The rest of teardown
writes escape sequences and drains stdin for up to a second, which would race
the input reader and the render loop, and the single-main-loop ownership
invariant is enforced by `make test-race`.

Call sites: `internal/codingagent/interactive.go`: `handleInterruptSignal`;
`tui/signal_restore.go`: `RestoreTerminalFromSignal`.

Locked by: `tests/integration/signal_shutdown_test.go`
(`TestInteractiveSigintTerminatesAndRestoresTerminal`): asserts the terminal is
sane before launch, raw while pig runs, that pig exits on SIGINT, and that ISIG
is back afterwards. Mutation-proven twice: dropping the restore reddens it with
"without restoring the terminal", and surviving the signal reddens it with
"pig survived SIGINT".

Known gap: the captured state is taken at the first raw-mode entry, and pig
disables SUSP before that, so a restored terminal reports `susp = <undef>`
where a pristine one reports `^Z`. ISIG and INTR are restored correctly.

Remove when: upstream restores the terminal before exiting on a signal.

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

## D54 Fenced code block bodies wrap instead of dropping their tail

What: `renderCodeBlock` wraps each body line to the width it was given with
`widthx.WrapTextWithAnsi`, emitting as many rows as the content needs, instead
of clipping the line at the right edge.

Upstream state: upstream's markdown renderer wraps every other token through
`wrapTextWithAnsi`: paragraphs (`packages/tui/src/components/markdown.ts:322`),
blockquotes (594), list items (788) and the raw fallback for a token it cannot
render (856). The `code` case (520-537) is the one exception: it pushes
`${indent}${codeLine}` unmodified. If an over-wide code line reaches the differential-render loop in `packages/tui/src/tui-main-screen.ts`, Pi writes a crash log, stops the TUI, and throws `Rendered line N exceeds terminal width`. Initial, full, and resize renders bypass the overflow check and emit the over-wide row unchanged.

Why: pig cannot adopt the crash. A coding agent renders long lines constantly -
minified sources, URLs, base64, log excerpts: and losing the session to a
rendered line is not a tradeoff worth making. D50's `renderCodeBlock` clip
avoided the crash by satisfying the renderer's real invariant (a component must
never emit a line wider than its width, observed at 486 columns on a
194-column terminal), but paid for it by deleting content from the surface
people copy from, with no marker and no way to recover the bytes.

Wrapping keeps that invariant: every emitted row fits the given width: while
losing nothing. It is also the treatment upstream already applies to every
other token, so it narrows the gap rather than inventing a third behaviour, and
it reflows back to the original single line as the terminal widens.

A marker or an overflow report was considered and rejected. pig's truncations
are vertical, marked, and recoverable: collapsed content carries `(ctrl+o to
expand)` and one keystroke restores it. There is no horizontal equivalent, so a
marker on a clipped line would announce a loss it cannot undo. D50 reports
Mermaid overflow because a diagram's geometry cannot be reflowed and reporting
is the only non-silent option there; code is text, so preserving it is
available and strictly better.

Cost: a wrapped line copies with a newline inside it. That is worse than an
untouched line and better than a silently truncated one, which looks complete
and is not; widening the terminal restores exact fidelity, which clipping never
offered.

Call sites: `tui/markdown.go`: `renderCodeBlock`, the body loop.
Regression: `tui/markdown_codeblock_wrap_test.go`
(`TestCodeBlockKeepsEveryCharacterAtNarrowWidth`,
`TestCodeBlockWrapKeepsHighlightingIntact`), both of which fail with clipping
restored (mutation-verified across untagged, text and highlighted fences).
SCRUTINIZED:approved

Remove when: upstream wraps the `code` token itself, at which point pig should
match its wrap width and break points exactly rather than keep this entry.

Locked by: `tui/markdown_codeblock_wrap_test.go`
(`TestCodeBlockKeepsEveryCharacterAtNarrowWidth`), which asserts every rendered
row fits the given width and that no non-space character is lost between source
and render.

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
remains the packed-cell quarantine authority.

Remove when: Pig no longer hosts extensions across a subprocess boundary, or
upstream provides an equivalent subprocess liveness contract that Pig can port
without this divergence.

Call-site markers:
- `coding/extension/host/subprocess/conn.go`: heartbeat, request state,
  cancellation, and typed transport failures.
- `coding/extension/host/subprocess/host.go`: handler inactivity and
  supervision.
- `coding/extension/host/subprocess/render_proxy.go`: generation-scoped
  renderer inactivity and last-frame retention.

Locked by: `coding/extension/host/subprocess/liveness_test.go`,
`coding/extension/host/subprocess/node_liveness_test.go`, Go/Rust/Python SDK
liveness tests, and `coding/extension/host/subprocess/protocol_sdk_sync_test.go`.
The tests use an injected monotonic clock and deterministic channels. They cover
healthy and missing heartbeat, unbounded tool/command/event/shortcut waits,
renderer retention, cancellation ordering, writer failure, and packed-member
versus packed-process failure.
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

Scope: the same strict source resolver proves Go, Rust, Python, and Node
factories or exact standalones. Missing standard factories, ambiguous languages
or roots, and incomplete source do not trigger this refusal because they do not
prove an extension contract.

Remove when: upstream reports unloadable extension-root installs itself, or
Package discovery gains an equivalent explicit distinction.

Call-site markers:
- `cmd/pig/package_commands.go`: rejects a proven extension root before an empty Package install can be recorded.

Locked by: `cmd/pig/package_install_empty_test.go` -
`TestInstallRejectsAnExtensionDirectoryAsAPackage`,
`TestInstallDoesNotRefuseDirectoriesThatMerelyLookLikeCode`,
`TestInstallAcceptsAPackageUsingConventionDirectories`, and
`TestEveryPackageResourceKindCountsAsAContribution`.
Ratification: explicitly approved by the user for section SHA-256 `4e06f3d7200cce8f6aa65e6074a3632923f7324ac170bd4e93ae38165c31ca5d`.
SCRUTINIZED:approved
## D58 An over-wide Mermaid diagram is narrowed until it fits

What: when a Mermaid diagram lays out wider than the message area, pig re-lays it
with narrower node labels until the art fits, and draws it. Upstream
`createMermaidMarkdownTransformer`
(`packages/coding-agent/src/modes/interactive/components/mermaid.ts`) returns
`token.raw` for `art.width > context.availableWidth`, so the reader sees the
source. A diagram that already fits is untouched and byte-identical to upstream.

Why: diagram width is driven by how wide node labels run before they wrap, and
that is a rendering choice, not a property of the diagram. Upstream discards a
correct diagram over a layout parameter it could have changed. The failure is
also invisible to its author: the fallback is produced by a display transform, so
the model that wrote the diagram never learns it did not draw (see D50) and keeps
writing diagrams that do not fit. Narrowing removes the failure instead of
reporting it.

Measured, not tabulated: the label width is found by laying the diagram out at
candidate widths and reading the real width of each, keeping the widest result
that fits. A real 115-column diagram lands at exactly 100, 80 and 60 columns for
those targets, at a cost of three extra rows.

Narrowing costs rows, never words. A label's line budget grows as its wrap width
shrinks so total capacity holds, a subgraph frame is sized to its whole title
instead of clipping it, and no layout is accepted that breaks a word the natural
layout kept whole. A diagram's own longest word is therefore its floor, measured
per diagram rather than set by a constant.

The first cut of this kept grok-mermaid's flat four-line budget and truncated
labels with an ellipsis to make a diagram fit; the second sliced words mid-token
to reach very narrow areas. Both drew something that looked complete and said
less than the raw source it replaced, which is worse than not drawing it. Both
cases where narrowing fired in the 74-case golden corpus were that failure at 8
and 9 columns, so the corpus now records no narrowing at all. It fires on real
diagrams at real widths: a 115-column diagram still lands at 100, 80 and 60.

Scope:
- Only a diagram that overflows diverges. One that fits returns unchanged, so
  every case where upstream also draws stays byte-identical.
- A subgraph frame title is drawn on one border row, so a frame cannot be
  narrower than its title. A diagram with long frame titles has a high floor and
  falls back with the D50 hint.
- A word longer than the natural wrap width is sliced by grok-mermaid itself.
  That is upstream behavior and is preserved; fitting adds none of its own.
- Diagrams have a structural floor: boxes, arrows and parallel branches take
  columns no label shrink recovers. Below it the narrowest art is returned with
  its real width, and the caller applies the existing D50 overflow hint. Silently
  claiming a fit would be worse than the raw block.
- An unmeasured area (`availableWidth <= 0`) returns the natural layout, since no
  target exists to fit.
- `Render` keeps upstream's signature and behavior; the fitting pass is a
  separate `RenderWithin`.

Call sites: `internal/mermaid/index.go`: `RenderWithin`; consumed by
`internal/codingagent/mermaid_transform.go`: `renderMermaidToken`.

Locked by: `internal/mermaid/fit_test.go` (narrows until it fits at three
targets, leaves a fitting diagram byte-identical, reports a deterministic
structural floor, ignores an unmeasured area, never truncates a label, spends
rows rather than words, and introduces no word break at any target for any
fixture) and
`internal/codingagent/mermaid_transform_test.go`, whose golden diff against pi's
own compiled transformer still runs over all 74 cases: pig may draw where pi
returned raw source only when every row really fits the area, and any case where
both draw must still match byte for byte. Mutation-proven: never narrowing fails
3, returning the narrowest instead of the best fit fails 3, altering a
diagram that already fits fails 1, holding labels to a flat four-line budget
fails 10, clipping a frame title while narrowing fails 10, and accepting a
layout that slices words fails 4. Search direction on meeting a sliced
candidate is deliberately unpinned: reversing it costs width, not
correctness, because the best non-slicing candidate already seen is still
what comes back.

Remove when: upstream reflows a diagram to the available width.

Ratification: user-ratified ("yes if we can do a auto-sized (not hard coded but
actually measured) solution that will be great as well"), after the silent
fallback was traced to a diagram that fits at 80 columns once its labels wrap
narrower.
SCRUTINIZED:approved

## D59 Generic extension tool cards expose complete recoverable details

What: a generic extension tool card retains its complete structured arguments.
Its collapsed header uses the current terminal-cell width for a compact preview
and marks hidden input with `… (ctrl+o to expand)`. The expanded card shows the
complete pretty-printed arguments and complete available result output. A
resize recomputes both the preview budget and expanded wrapping from the
retained value. Built-in tool renderers and extension tools with a custom call
renderer keep their existing presentation.

Upstream state: `ToolExecutionComponent.expanded` starts false and is passed to
custom call/result renderers, but the registered-tool fallback does not consume
it. A registered extension tool counts as having a renderer definition even
when `renderCall` is absent, so `createCallFallback()` shows only the tool name.
Its arguments remain hidden before and after Ctrl+O. `formatToolExecution()`
prints `JSON.stringify(args, null, 2)` only for an unknown tool with no built-in
or extension definition. A custom renderer can define its own collapsed and
expanded output.

Why: Pig's old generic header first truncated every string to 40 characters and
then truncated the combined header to 80 characters. It retained only that
shortened string, so the ellipsis permanently hid the input and Ctrl+O could
never recover it. Fixed character limits also wasted wider terminals. Retaining
the source value makes every display omission explicit and reversible. It also
keeps generic extension cards consistent with Pig's existing recoverable result
preview rather than showing an unbounded argument payload in the transcript by
default.

Tradeoffs:
- The component owns one copy of the argument JSON for its visible lifetime.
  This costs memory proportional to the tool call, but avoids borrowing event
  buffers and is bounded by data already carried in the provider request and
  session record.
- Expanded arguments can add many rows. That cost is explicit and user-driven;
  collapsed cards stay compact.
- Explicit Ctrl+O collapse clears and rebuilds terminal scrollback in collapsed
  form. Native scrollback cannot delete only the expanded rows, so this action
  snaps the reader to the live cursor. Automatic completion follows Pi's
  ordinary main-screen redraw behavior.
- Expanded wrapping preserves every grapheme and space instead of using the
  ordinary word wrapper, which trims whitespace at line breaks. Copying wrapped
  JSON includes display line breaks, but no retained argument character is
  removed.
- The collapsed preview preserves wire key order rather than sorting fields.
  This keeps the displayed value faithful to the call and avoids manufacturing
  a second canonical representation.
- A result that the tool already truncated remains truncated. Pig preserves its
  notice and does not claim Ctrl+O can recover bytes the tool never returned.

Call sites:
- `tui/tool_execution.go`: retained structured arguments, width-aware
  header preview, expanded argument body, and generic final-state policy.
- `internal/codingagent/interactive.go`: generic extension tool detection,
  argument retention for streaming, execution, and resumed-session paths, and
  explicit-collapse scrollback reconstruction.
- `internal/codingagent/keybindings.go`, `internal/codingagent/interactive.go`,
  `internal/codingagent/slash_commands.go`, and Pig docs: Ctrl+O is described
  as toggling tool details.

Locked by: `tui/tool_execution_test.go`
(`TestGenericExtensionToolCollapsedDetailsScaleWithWidth`,
`TestGenericExtensionToolExpandedDetailsShowCompleteInputAndOutput`, and
`TestGenericExtensionToolStructuredArgsReflowAfterResize`) and
`internal/codingagent/interactive_test.go`
(`TestInteractiveMode_GenericExtensionToolDetailsRetainArguments`). The tests
cover narrow/wide previews, complete nested arguments and output, source-level
truncation notices, collapse/re-expansion identity, resize reflow, the
production event path, and the custom-renderer boundary.

Remove when: upstream gives generic extension tool calls a width-aware,
recoverable details toggle that exposes complete arguments and result output.

Ratification: user-ratified ("Let's completely close 705 in our PR") after the
upstream generic fallback and the compact-versus-recoverable tradeoff were
reviewed.
SCRUTINIZED:approved
## D61 Session replacement keeps startup-project Services and Resources

What: upstream `AgentSessionRuntime.switchSession()` opens the destination
Session, then calls `createRuntime()` with the destination Session CWD. That
constructs the incoming Session's settings, resource loader, system prompt, and
built-in tools against the destination project. PiG's `coding.Session.ReplaceInner`
swaps the Session history, identity, model, thinking level, and persisted CWD
inside one host-scoped runtime. It does not reconstruct `coding.Services`, the
resolved Resource set, or built-in tool instances.

Observable effect: after an in-process switch to a Session whose recorded CWD is
a different project, direct RPC Bash runs in the destination CWD because it reads
the active inner Session. Built-in tools, project settings, context files,
prompts, skills, themes, and the system prompt still use the project selected at
process startup. Same-project new, fork, clone, resume, and switch operations are
unaffected.

Why deferred: a faithful fix is the same per-Session runtime reconstruction
required to retire D30. Updating only tool CWD would leave settings, resources,
and the system prompt stale while making the partial switch appear complete.
PiG must replace these as one owned runtime transition, rebind event forwarding,
and preserve cancellation and extension lifecycle order.

Call site:
- `coding/session.go`: `Session.ReplaceInner`, the shared replacement
  chokepoint used by RPC and interactive new/resume/fork/clone flows.

Parity allowance: current replacement scenarios deliberately use Session
fixtures whose CWD is rewritten to each binary's isolated working-directory
snapshot. A cross-project scenario would only restate this approved gap until
the runtime can rebuild the complete destination Resource closure.

Remove when: every Session replacement constructs and atomically installs
Services, settings, resources, system prompt, tools, event forwarding, and an
extension runner for the destination CWD; then add a cross-project switch
scenario that proves destination context and tool resolution.

SCRUTINIZED:approved

## D62 /bug exports locally and links to a PiG issue

What: Pi's `/bug` asks for consent, then either uploads the report to Earendil's report gateway or exports a zip archive. PiG runs the same consent flow (description, transcript consent, optional model-written summary) but offers only the export. It writes `pig-bug-report-<id>.zip` to the current directory, records the same `pi.bug-report` session entry with `delivery: "zip"`, and prints a prefilled link to the PiG bug issue form. The link carries only the form fields `title` (the first line of the description, at most 72 characters), `version`, `platform`, and `actual` (the report ID and archive name). It never carries session content. The user attaches the archive. The command description is "Export a bug report to attach to a PiG issue" instead of "Report a bug to the Pi developers".

Why: PiG is not an Earendil product. Sending PiG reports to Pi's gateway would misdirect them and share user data with a party the user did not choose. The owner ratified export-only plus a PiG issue link on 2026-09-23 (delivery/WORKSTREAMS.md 16).

Observable effect: the delivery selector lists "Export as Zip" and "Cancel" with no "Upload Report". PiG makes no network request for a report; only the optional summary calls the session's own provider, after the same consent prompt as Pi. `diagnostics.json` carries the crash log (`<agentDir>/crashes.json`) as Pi's does, and a written report clears it.

Call sites:
- `internal/codingagent/slash_bug.go`: the delivery selector and the issue link.
- `internal/codingagent/slash_commands.go`: the `/bug` registration.

Locked by: `internal/codingagent/bug_report_test.go` mirrors upstream `test/bug-report.test.ts` (redaction and the multi-line description prompt) and proves the export path makes no HTTP request and imports no network package. `tests/upstream-parity` `TestBuiltinSlashCommands_CoverUpstream` covers the command's presence.

Remove when: never, unless PiG gains its own report service that the owner approves.

SCRUTINIZED:approved

## D63 `--version` prints PiG's composite version

What: `pi --version` prints Pi's bare version (for example `0.87.1`). `pig --version` prints one composite version: PiG's release with the Pi release PiG ports as semver build metadata (for example `0.2.0+0.87.1`, `coding.Version`). The help and diagnostics banner, the interactive startup line, `pig build`'s installed line, and `pig verify` show the same composite. `pig version` keeps its separate `pig:` and `upstream pi:` fields, which the Piglet image check and `pig build` read.

Why: one string tells a user both which PiG they run and which Pi it ports. The owner chose this on 2026-09-23.

Observable effect: a script that runs `pig --version` and expects Pi's bare version sees `0.2.0+0.87.1`. Semver precedence ignores build metadata, so the composite sorts as `0.2.0`; self-update comparisons, release tags, and the Piglet compatibility check still use `coding.PigVersion` itself.

Call sites:
- `cmd/pig/main.go`: `cliVersionString`.

Locked by: `cmd/pig/main_test.go` `TestCLIVersionStringIsCompositeVersion`; the parity scenario `parity/scenarios/startup/01-version-flag.toml` asserts both outputs in its `[diverge]` block.

Remove when: never, unless the owner returns `--version` to Pi's bare version.

SCRUTINIZED:approved

## D64 PiG's hosted endpoints live on pi-in-go.dev

What: Pi 0.87.1 points its hosted endpoints at pi.dev and uploads shared-session artifacts to Earendil's Radius gateway at `radius.pi.dev`. PiG serves its hosted paths from `https://pi-in-go.dev` through the private PiG platform repository. The version check, install report, and managed installer API keep Pi's response shapes at the PiG host; the installer API serves PiG GitHub Release archives and returns 404 for npm-only `package.json` and `package-lock.json` paths because PiG has no npm package.

Pi's `/share` uploads an organization-visible JSONL artifact to Radius only when Radius auth is available, then otherwise falls back to a private GitHub gist. PiG always requires the explicit `/share` command, displays a persistent privacy notice before the request, and uploads the same `exportSessionForShare` JSONL shape to `https://pi-in-go.dev/v1/artifacts?visibility=unlisted&title=PiG+session`. PiG needs neither Radius nor `gh` login for this path. The PiG platform stores at most 8 MiB per artifact, gives it an unlisted `https://pi-in-go.dev/session/p_<id>` URL, and expires it after 30 days. `PI_SHARE_GATEWAY_URL` explicitly replaces the complete upload URL; `PI_INSTALLER_API_BASE` keeps its upstream meaning for the installer.

Pi's `reportInstallTelemetry` (interactive-mode.ts:1292-1307) sends its anonymous install/update ping to `https://pi.dev/api/report-install?version=<version>`. PiG sends the identical ping — only the `version` query parameter, plus PiG's own `User-Agent` — to `https://pi-in-go.dev/api/report-install?version=<version>` instead, fired at the same two occasions Pi fires it (a fresh install, and an update whose changelog has new entries), gated by the same `enableInstallTelemetry` setting and `PI_TELEMETRY`/`PI_OFFLINE` env overrides, with the same 5s timeout and fire-and-forget error handling. `PIG_INSTALL_TELEMETRY_URL` explicitly replaces the endpoint (tests use it to point at a local server; there is no upstream equivalent).

Why: PiG is not an Earendil product. Sending PiG users or Session data to pi.dev, radius.pi.dev, or an implicit third-party gist would misattribute PiG traffic and depend on a service PiG does not operate. The owner chose `pi-in-go.dev` on 2026-09-23 and approved the explicit, privacy-noted PiG share gateway on 2026-09-24 (delivery/OWNER-DECISIONS.md).

Observable effect: `/share` shows what Session data will be uploaded, runs the request behind an Escape-cancellable loader, and prints a 30-day unlisted PiG URL instead of a Radius or GitHub-gist URL. Anyone with that URL can read the artifact. The install/update ping goes to `pi-in-go.dev` instead of `pi.dev`; the PiG-hosted endpoint stores one Analytics Engine data point per call (the version and arrival time only) and rate-limits at 10 calls/minute per client. PiG does not read Pi's `PI_SHARE_VIEWER_URL`, so `--help` does not list it or its pi.dev default. Other hosted endpoint clients use `pi-in-go.dev` instead of `pi.dev` as they land.

Call sites:
- `internal/codingagent/session_share.go`: `defaultShareGatewayURL`, `shareGatewayURL`, and `shareSession`.
- `internal/codingagent/interactive_share.go`: `shareSessionWithLoader`.
- `internal/codingagent/slash_commands.go`: the `/share` destination and retention description.
- `internal/codingagent/install_telemetry.go`: `defaultInstallTelemetryURL`, `installTelemetryURL`, and `sendInstallTelemetry`.
- `internal/codingagent/interactive.go`: the startup changelog/install-telemetry block in `Run`.
- `automation/gen/gen-help.sh`: drops `PI_SHARE_VIEWER_URL` from the rendered `cmd/pig/help_upstream.txt`.

Locked by: `internal/codingagent` `TestShareSessionUploadsJSONLWithPrivacyNotice`, `TestShareSessionKeepsConcurrentExportsIsolated`, `TestUploadShareArtifactHonorsCancellationAndCanonicalOrigin`, `TestShareLoaderEscapeCancelsUpload`, `TestSharePrivacyNoticeRemainsVisibleAfterResult`, `TestShareGatewayURLDefaultAndOverride`, and `TestShareBuiltinDescribesUnlistedExpiry`; `cmd/pig` `TestHelpOmitsUnusedShareViewerURL`; the private-platform patch's `worker/test/share.test.ts` covers route shape, R2 limits, expiry, escaping, and hashed rate limiting. `TestReportInstallTelemetry_SendsOnlyVersionToConfiguredEndpoint`, `TestReportInstallTelemetry_DefaultURLIsPiInGoDevNotPiDev`, `TestReportInstallTelemetry_SettingDisabledSkipsRequest`, `TestReportInstallTelemetry_EnvOverrideDisablesEvenWhenSettingIsOn`, `TestReportInstallTelemetry_PIOfflineSkipsEvenWhenTelemetryIsOn`, `TestReportInstallTelemetry_NeverContactsPiDotDev`, `TestRecordChangelogVersionAndMaybeReportInstall_FreshInstallPingsAndRecordsNoBanner`, `TestRecordChangelogVersionAndMaybeReportInstall_UpdateWithNewEntriesPingsAndShowsBanner`, `TestRecordChangelogVersionAndMaybeReportInstall_SameVersionNeverPings`, and `TestRecordChangelogVersionAndMaybeReportInstall_VersionBumpWithNoNewEntriesNeverPings` lock the install-telemetry ping.

Remove when: never, unless PiG's owner selects another PiG-operated host or returns `/share` to Pi's Radius/GitHub flow (update this entry and every call-site marker then).

SCRUTINIZED:approved

## D65 PiG identifies itself as `pig/<coding.Version>`

What: upstream `getPiUserAgent()` has two package-local forms. The AI helper (`packages/ai/src/utils/pi-user-agent.ts`) sends every HTTP provider request's default `User-Agent` as `pi (<platform> <release>; <arch>)` (or `pi (browser)` with no Node/Bun runtime). The coding-agent helper (`packages/coding-agent/src/utils/pi-user-agent.ts`) reports `pi/<version> (<platform>; <runtime>; <arch>)` for management requests and bug-report metadata. PiG has no browser build and always runs as a native process, so both PiG surfaces use the one owner-approved product identity: `pig/<coding.Version> (<platform> <release>; <arch>)`, for example `pig/0.2.0+0.87.1 (darwin 25.6.0; arm64)`. The release comes from `uname` on unix (`ai/user_agent_unix.go`, `internal/codingagent/bug_report_unix.go`) and from `RtlGetVersion` on Windows (`ai/user_agent_windows.go`, `internal/codingagent/bug_report_windows.go`), matching Node's `os.release()` on each platform.

Why: PiG is not Pi; providers, diagnostics, and their logs should distinguish PiG traffic and artifacts from Pi's, and identify both the PiG release and the Pi release it ports. The owner chose this shape on 2026-09-23 (delivery/OWNER-DECISIONS.md Q4).

Observable effect: every default provider `User-Agent` (Anthropic Messages, OpenAI Completions, OpenAI/Azure/Codex Responses, Google Generative AI/Vertex, Mistral Conversations) and the `report.json` environment identity in an exported bug report read `pig/...` instead of one of Pi's `pi...` forms. A user-configured provider `User-Agent` header overrides the default except for OpenAI Codex Responses, whose upstream `buildBaseCodexHeaders` deliberately reapplies the product identity after model/request headers. An Anthropic OAuth (subscription-token) request keeps sending `claude-cli/2.1.280` regardless (D63's sibling decision, Q3): that identity is not `getPiUserAgent()`'s output and is untouched by this divergence.

Call sites:
- `ai/anthropic_client.go`: `mergeAnthropicClientHeaders` (seeds the `User-Agent` key; the Claude Code OAuth identity still wins on the wire).
- `ai/openai.go`, `ai/openai_responses.go` (also reached by `ai/azure_openai_responses.go` and `ai/openai_codex_responses.go`, which delegate to the same request builder), `ai/google.go` (also reached by `ai/google_vertex.go`), `ai/mistral.go`: the `User-Agent` header construction. Ordinary model/request headers retain upstream override precedence; Codex uses `forceUserAgent` to mirror its trailing `headers.set("User-Agent", getPiUserAgent())`.
- `internal/codingagent/bug_report.go`: `codingAgentUserAgent`, used for the local bug-report metadata field retained by D62.

Locked by: `ai/user_agent_test.go` (`TestPiUserAgentFormat`, `TestProviderDefaultUserAgent`, `TestProviderUserAgentOverridePrecedence`), `ai/anthropic_oauth_test.go` `TestAnthropicClientUserAgent`, and `internal/codingagent/bug_report_test.go` `TestBugReportEnvironmentUsesPiGUserAgent`.

Remove when: never, unless the owner changes PiG's product identity.

SCRUTINIZED:approved

## D66 Narrow TUI rows stay within the requested width

What: PiG keeps two narrow-width component paths within their requested terminal-cell width. `TruncatedText.Render` reduces horizontal padding when the full padding plus one content cell would exceed the width. At width 1 with one column of horizontal padding, PiG renders `"A"`; upstream renders `" A "`, which is three cells wide. `UserMessageSelector.Render` also clips each list, metadata, empty-state, and scroll-indicator row after adding the cursor or indentation. Upstream's `UserMessageList` truncates only the message body and then adds its two-cell cursor, while metadata and other rows are unbounded.

Why: both upstream paths can emit a row wider than the terminal. Upstream's main screen treats that as fatal only in its differential-render loop and stops with `Rendered line exceeds terminal width`; initial, full, and resize renders emit the over-wide row unchanged. PiG preserves the complete padding and rows at ordinary widths, but prioritizes keeping an unusually narrow pane usable instead of emitting an over-wide row.

Observable effect: at widths where fixed padding, cursor text, or metadata cannot fit, PiG removes padding or clips the row while Pi emits an over-wide row and terminates if that row reaches the main screen's differential-render overflow check.

Call-site markers:
- `tui/truncated_text.go`: the horizontal-padding clamp in `TruncatedText.Render`.
- `tui/user_message_selector.go`: the final row-width bound in `UserMessageSelector.Render`.

Locked by: `tui/component_width_table_test.go` `TestSelectorDialogListComponentsNeverExceedRenderWidth`, whose `TruncatedText`, `UserMessageSelector`, `UserMessageSelectorScrolled`, and `UserMessageSelectorEmpty` cases render every width from 1 through 120 and reject any over-wide row. Restoring upstream's full padding or removing the selector's final clip fails the matching width-1 case.

Parity allowance: paired interactive scenarios use a viable terminal width. The intentional difference exists only when these rows cannot fit; the width matrix directly locks the allowed behavior and its boundary.

PORT_MAP paths: `packages/tui/src/components/truncated-text.ts` and `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts`.

Remove when: upstream clamps `TruncatedText` padding and bounds every user-message selector row, or its main screen safely handles over-wide rows without terminating.

SCRUTINIZED:approved

## D67 Windows container builds run as the image's default user

What: a Piglet container build runs `pig piglet build` inside a Linux builder image with the output directory bind-mounted. On Linux and macOS PiG passes `--user <uid>:<gid>` for the invoking user, so the artifacts it writes belong to that user. A Windows host has no numeric uid or gid (`os.Getuid` is -1 and accounts are SIDs), so PiG passes no `--user` there and the build runs as the image's default user.

Why: Docker Desktop and Podman on Windows run containers in a Linux VM that owns bind-mounted files and maps them to the Windows user, so a host uid would be both unavailable and meaningless. Failing the build instead ("resolve invoking user: no passwd entry and no numeric uid/gid") would make container builds unusable on Windows. The lead chose the image's default user on 2026-09-25 (team/lead/inbox WIN-NOTES.md, Q5).

Observable effect: on Windows the builder's `docker run` or `podman run` command line has no `--user` argument, and PiG does not look up the current account or numeric ids. Upstream Pi has no Piglet builds, so no Pi behavior changes.

Call-site markers:
- `coding/pigletbuild/container_builder.go`: `containerUserArgsFor`.

Locked by: `coding/pigletbuild` `TestContainerUserArgsOnWindowsLookUpNoIdentity` (fails if the account or ids are read for Windows), `TestContainerUserArgsPassTheInvokingUser`, and `TestContainerBuilderBuildProducesHostArtifactAndContainerIdentity`, which requires `--user` exactly when the host is not Windows.

Parity allowance: Piglet container builds have no upstream counterpart; the unit tests above lock both platforms' command lines.

Remove when: PiG maps a Windows identity into the build container, or container builds stop bind-mounting host output.

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

## D69 Windows Piglet scripts are cmd.exe launchers

What: `pig piglet build <name> --format script` writes a thin launcher that runs `pig --piglet <source>` with the caller's arguments. On Linux and macOS it is a POSIX shell script (`#!/bin/sh`, `exec pig --piglet '<source>' "$@"`, mode 0755). On Windows it is a cmd.exe batch file of two CRLF-terminated lines, `@setlocal DisableDelayedExpansion` and `@pig --piglet "<source>" %*`. The source path is double-quoted and each `%` is doubled, so spaces, `&`, `^`, apostrophes, and percent signs reach pig unchanged; the first line turns off delayed expansion that a calling `cmd /v:on` enabled, so `!` does too. PiG writes the file at the `--out` path as given, so a Windows user names it `.cmd` (for example `--out research.cmd`). `--out -` prints the same bytes.

Why: Windows cannot run a `#!/bin/sh` script, so a POSIX launcher written there does nothing useful outside Git Bash. The lead chose a native `.cmd` launcher on 2026-09-25 (team/lead/inbox WIN-NOTES.md, Q7).

Observable effect: on Windows the launcher's bytes, line ending, and quoting differ from other platforms, and no mode bits are set. Upstream Pi has no Piglets, so no Pi behavior changes.

Call-site markers:
- `coding/pigletbuild/main.go`: `sourceScript`.

Locked by: `coding/pigletbuild` `TestSourceScriptPerPlatform` (every platform's exact bytes on any host) and `TestAC2AC7SourceScriptFileAndStdoutCreateNoPigState`, which writes the host's launcher for a Piglet under a path with an apostrophe, `%`, and `&`, runs it, and checks the arguments pig receives. Removing the `%` doubling fails both. `TestWindowsScriptKeepsBangsUnderDelayedExpansion` runs the launcher through `cmd.exe /d /v:on` for a Piglet under `team!TOKEN!` with `TOKEN` set; removing the `setlocal` line fails it.

Parity allowance: Piglet scripts have no upstream counterpart; the unit tests above lock each platform's launcher.

Remove when: never, unless Windows runs POSIX shell scripts natively.

SCRUTINIZED:approved

## D70 /reload re-evaluates every extension module, not only its factory

What: on `/reload`, Pig replaces every enabled extension with a fresh runtime process, including an extension whose source and cached artifact are unchanged. The new process imports the extension module and calls its factory, so module-level state (top-level `let` bindings, module-scope caches, counters, open handles) starts over after every reload. Factory-scoped state starts over in both Pi and Pig.

Upstream state: Pi 0.87.1's `DefaultResourceLoader.reload` calls `clearExtensionCache()` and invokes every extension factory again inside the same Node process. Whether the module body runs again depends on its loader: jiti (`moduleCache: false`) re-evaluates a `.ts` module, while a `.mjs` module stays in Node's native ESM cache and its module-level state survives the reload. Both behaviors were probed directly on Pi 0.87.1 (module-eval and factory-call logs across two reloads: `.ts` evaluated 2x with 2 factory calls; `.mjs` evaluated 1x with 3 factory calls).

Observable effect: an `.mjs` (or natively imported `.js`) extension that keeps state at module scope sees it reset by Pig's `/reload` and retained by Pi's. Extensions that keep state inside the factory, or `.ts` extensions, behave the same on both.

Why: Pig hosts extensions outside its own process, so an extension instance is a runtime process. Re-invoking a factory inside a retained process would need an in-process re-registration protocol and would keep a process whose registrations are being replaced, which the atomic start-beside/swap/stop-old reload transaction is built to avoid. A fresh process matches the part of the contract that holds for every Pi loader, which is that each factory runs again on reload.

Call-site markers:
- `coding/extension/host/subprocess/reload_cells.go`: `stageIsolated`, where an unchanged extension is started as a fresh process.

Locked by: `coding/extension/host/subprocess/host_test.go` `TestHost_Reload_UnchangedExtensionStartsFreshInstance` (an unchanged extension gets a new process on reload), and parity scenario `parity/scenarios/extensions-runtime/15-footer-status-reload-composition.toml`, whose fixture advances a persisted generation in its factory and requires both binaries to reach generation 3 after two reloads.

Parity allowance: no paired scenario asserts module-scope state across `/reload`, because Pi's result depends on the extension's file type and Pig's is the same for all of them. Scenario 15 keeps its state in the factory, the per-reload contract both share.

Remove when: Pig re-invokes TS/JS extension factories inside a retained Node runtime on reload, using Pi's loader semantics (jiti re-evaluation for `.ts`, the native module cache for `.mjs`), or when upstream reload re-evaluates every extension module.

SCRUTINIZED:approved

## D73 Host-bound Pi exports are importable stand-ins inside extensions

What: inside an extension process, `@earendil-works/pi-coding-agent`, `@earendil-works/pi-tui`, and `@earendil-works/pi-ai` export every runtime value their Pi 0.87.1 packages export. Pure helpers are ported and behave as upstream (for example `isToolCallEventType`, the `is*ToolResult` guards, `stripFrontmatter`, `getLanguageFromPath`, `createEventBus`, `formatSkillsForPrompt`, `parseSkillBlock`, `TruncatedText`, `Loader`, `CancellableLoader`, `TUI_KEYBINDINGS`, `getSystemMessageText`). Values that belong to Pi's own process (the interactive UI and its components, `main`, print and RPC modes, session and runtime construction, package and resource loading, tool definitions bound to Pi's runner, terminal images and capability probing, pi-ai's model, provider and stream layer) are exported as stand-ins that throw `<name> is not available to extensions running in PiG ...` when called or constructed. `parseFrontmatter` parses flat `key: value` frontmatter (strings, quoted strings, numbers, booleans, null) and throws on other YAML, because the `yaml` package is not part of PiG's extension runtime. Theme-bound helpers (`getSelectListTheme`, `highlightCode`, `keyText`, `rawKeyHint`) return uncolored text, as the existing `getSettingsListTheme` and `keyHint` shims already do.

Why: PiG runs extensions in a Node process beside its Go host, not inside Pi's process, so these values have no working implementation there. An ESM import of a name a module does not export fails the whole extension at link time, before any of its code runs: pi-rtk-optimizer failed to load on PiG 0.2.0 because the shim lacked `isToolCallEventType`. Exporting every upstream name keeps an extension loadable whenever the names it imports are the ones it can use, and a stand-in reports the exact name if the extension does call one. Owner-directed launch P0 fix (Reddit report on 2026-09-26, faithful extension compatibility).

Observable effect: an extension that imports a host-bound name loads on PiG and fails only if it calls that name, with an error naming it. Under Pi the same call works. An extension whose frontmatter uses nested YAML gets an error from `parseFrontmatter` instead of a parsed object.

Call-site markers:
- `coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs`: the frontmatter parser and the host-only stand-ins.
- `coding/extension/host/subprocess/runtime-node/shims/pi-tui.mjs`: the host-only stand-ins.
- `coding/extension/host/subprocess/runtime-node/shims/pi-ai.mjs`: the host-only stand-ins.

Locked by: `coding/extension/host/subprocess` `TestNodeRuntimeShimsExportEveryPinnedPiValue`, which collects every runtime export of the pinned upstream `packages/{coding-agent,tui,ai}/src/index.ts` (following `export *`) and fails if the loader's module lacks any of them.

Parity allowance: no paired scenario calls a host-bound value from an extension; the coverage test locks the export surface.

Remove when: a stand-in's value gains a working implementation in the extension runtime (port it and drop it from the stand-in list), or upstream stops exporting it.

SCRUTINIZED:approved
