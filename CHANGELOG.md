<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Changelog

All notable public changes to PiG will be recorded in this file.

## [Unreleased]

### Fixed

- Report a transport failure before response headers on the `openai-completions`, `openai-responses`, `azure-openai-responses`, and `openrouter-images` paths as `Connection error.`, or `Request timed out.` for a timeout, as Pi's OpenAI SDK does (`openai@6.40.0` `APIConnectionError`/`APIConnectionTimeoutError`). Thanks to @ShoichiTect for reporting this (#86).
- `pig config` exits on Ctrl-C (also when the terminal reports it as a Kitty CSI-u sequence) and on Escape, and leaves the shell prompt below the selector with the terminal restored, as Pi does (Pi 0.87.1 `config-selector.ts:487-493`, `cli/config-selector.ts:41-49`). Thanks @baggiiiie for reporting #89 and the cursor-parking fix in #90.
- `/reload` re-invokes each extension factory inside the extension runtime process that already holds its module, as Pi does, instead of starting a fresh process. Module-level state now follows Pi's loader: a native ESM module (`.mjs`, or `.js` in a `"type": "module"` package) keeps its state and, as in Pi, an edit to it is not seen until `pig` restarts; a `.ts` module is re-evaluated, and package or module state in a compiled Go, Rust or Python factory persists (D70 is closed). The old generation leaves the shared event bus and its connection closes when the new generation replaces it. A Session replacement in `pig` still starts a fresh extension host and fresh runtime processes (D70 now covers session replacement only); `subprocess.RuntimeRetention` is the additive host API for retaining them. Module state lives in the process that holds the module, so changing an extension's `isolation` setting (a PiG setting Pi lacks) starts a new process and a new module on the next `/reload`. An extension path holding a tab, line feed or carriage return no longer breaks a Node extension's load or `/reload`.

## [0.3.0] - 2026-09-28

### Fixed

- Await model availability through the shared provider collection and reject stale snapshot/error publication. Model Runtime snapshot getters remain nonblocking, and credential-change notifications follow availability publication (Pi 0.87.1 `model-runtime.ts:286-443,537-557`).
- Report OAuth and subscription usage from the published auth check and current provider capability (Pi 0.87.1 `model-runtime.ts:472-477`).

### Fixed

- Native package-manager self-update retains the proven npm prefix, recognizes a verified pnpm launcher through its store symlink, and refuses updates when the owning package directory or parent is not writable (D39). Command display uses JavaScript whitespace rules, and configured executable names do not change the proven owner's argument contract. The signed-release caller preserves this provenance.
- Settings created with `coding.NewInMemorySettingsManager` retain initial values and Session changes across reload without settings-file I/O. `ServicesOptions.SettingsManager` preserves a caller-supplied manager. Settings snapshots and file writes preserve explicit empty arrays. Changing project trust reloads only project settings; unchanged trust no longer discards transient overrides.
- Share model-auth resolution between the Go and extension facades. Carry credential-scoped environment and authentication headers through each provider API without resolving command-backed credentials twice.
- Keep RPC compaction workers from releasing the input loop's response turn, preventing a data race and intermittent unlocked-mutex crash.
- Keep startup typing visible, queue early prompts until the input consumer is ready, and submit bare `!` and `!!` as normal prompts.
- Preserve Pi's Unicode whitespace rules for submitted and restored prompts, and do not dispatch queued input after its owner is cancelled.
- Frame terminal input before negotiation and native-key normalization. Hold split keyboard replies for Pi's fragment deadline, preserve listener and read/paint ordering, and join pending readers and progress callbacks before returning terminal ownership.
- Apply Package resource filters with Pi's shared glob, exclusion, and exact force-override rules in both startup loading and resource configuration.
- Preserve canonical Package resource precedence and metadata, resolve temporary sources before discovery, reconcile configured npm versions, and reserve noninteractive stdout through help and startup.
- Match Package command validation and help. Keep the Warnings settings submenu non-searchable and use shared Node-compatible JSON diagnostics for settings, models, trust and RPC.

### Fixed

- Select Package manager flags from the configured executable spelling without executing a version probe. Preserve case, wrapper separators, and non-Windows filename suffixes.
- Discover conventional Node extension files independently so `main.ts` and plain `package.json` files do not hide sibling extensions.
- Apply startup Session names before model validation and expose initial names in RPC. Use header-only exact-ID lookup, keep metadata-only Sessions in memory, preserve requested fork IDs, and reject existing fork targets.
- Keep the active Session visible in the picker and reject its deletion through symlink aliases. Clear confirmation before invoking deletion, and order threaded Sessions by the newest millisecond activity across each subtree.
- Convert unsupported clipboard image formats, including BMP, to PNG before paste. Omit undecodable images instead of returning unsupported bytes.
- Restore the system-prompt builder's default tool selection while preserving explicit empty selections at startup, tool changes, and reload. Preserve exact forced prompt text, including an empty replacement.
- Give editor-slot custom UI keyboard focus while it is open, then restore the overlay or editor before returning.
- Defer CLI image resizing until the request model is selected, and retain image conversion failures as omission notes instead of aborting file processing.
- Correct terminal progress-clear escape sequences.
- Preserve path prefixes, quotes, and directory cursor positions during completion. Keep direct `.git` completion available, disable attachment suggestions when `fd` is unavailable, and isolate concurrent completion sorting state.
- Match terminal image metadata, cursor and aspect-ratio defaults, and width-bounded fallback paths.
- Show saved and current-session trust separately in `/trust`. Preserve the saved-choice marker while browsing, support inherited parent decisions, and surface trust-store failures without activating a new decision in the current session.

### Fixed

- Preserve extension-visible session IDs in print and JSON mode, including `--no-session`, so session-keyed extensions such as pi-warden can keep their state (#83). Fix the Go, Python, and Rust context accessors to return the host's session ID, file path, and leaf ID, including after replacement with an in-memory Session.

### Fixed

- Fixed option-shaped prompts after the `--` delimiter being rejected or parsed as flags. Arguments after the delimiter remain messages or `@files`, as in Pi.
- Treat unprefixed Package names and SCP-shaped sources as local paths instead of invoking npm or rejecting them as source schemes. Match Pi's missing-path diagnostic.
- Resolve project-context input paths before discovery and use the shared Git-directory resolver. Nested worktrees now handle absolute `commondir` paths and uninitialized nested repositories without duplicating or losing instructions (Pi 0.87.1 `resource-loader.ts:101-124`, `footer-data-provider.ts:16-47`).
- Preserve runtime API-key overrides when credential deletion fails or is cancelled. Use the same context-aware deletion contract for file, in-memory, and read-only credential stores.
- Serialize same-provider Model Runtime login and logout through local synchronization. Report committed credential changes separately from synchronization failures, including availability errors and cancellation.
- Sanitize Session names before persistence and publish one ordered name-change event through direct, extension, RPC, and interactive paths. Suspended extension handlers no longer own the setter's completion; Session shutdown cancels and drains their notifications.
- Enforce Pi's 50 MiB stdout limit for clipboard image helper commands through the shared clipboard runner. Cancel overflowing helpers immediately and preserve caller-specific deadlines.
- Keep working, compaction, branch-summary, and retry status visible in a standalone row when a custom editor replaces the default editor.
- Apply tool expansion to every mounted expandable transcript component, including resumed skill invocations, without updating detached components.
- Live provider tests use one missing-secret skip convention and Pi's provider environment names. Credential-free CI runs skip these tests with the missing variable names; supplied credentials still expose authentication and service failures. The live-test secrets inventory documents cloud credential alternatives and the test-only Codex OAuth-token input.

### Added

- An optional nightly and manually dispatched `Live providers` workflow uses the protected `live-providers` environment and reports live test outcomes by provider. It does not run on pull requests or become a required check.

### Fixed

- Preserve fresh regular lock files during upgrade recovery and use the normal contention timeout or cancellation. Stale empty v0.2.0 sidecars still recover safely across auth, settings, trust and model-cache stores, with old-writer and Windows file-identity checks retained.
- Preserve published Go dependency pins and checksums during ordinary PiG version bumps. Require explicit release preparation for new nested-module versions, and verify their public tags and downloaded checksums in CI before publishing the root release.

### Fixed

- Create a missing `auth.json` containing `{}` during agent startup, matching Pi. New files have owner-only permissions. Startup preserves existing credentials, file permissions, and the shared auth lock.
- Include the saved current Session in `/resume`, with Pi's active-session accent and deletion protection. Selecting it reloads the same Session. Relative session paths now receive the same active-session treatment as absolute paths.

### Fixed

- Preserve Anthropic empty thinking/signature fields, cross-model replay conversion, initial streamed content and input-transformation diagnostics. Honor adaptive effort without budget clamping and keep replacement payloads in streaming mode. Cancel and join Anthropic contextual manual-code prompts when login settles.

### Changed

- Document the owner-approved 0.3.x known gaps in SDK Provider objects and foreign registered-configuration identity (D78), cross-process partial-message observation (D82), and foreign event-bus payload identity (D83). Preserve same-process parity obligations and the strict RPC33 comparison with its raw failure. Approval does not confer parity proof or waive unrelated defects.

### Added

- Added `make set-version VERSION=x.y.z` to synchronize the PiG release pin, local Go module requirements and tracked-file checksums, Standard development version, and changelog heading. Dry-run shows the diff; moving Unreleased entries requires an explicit flag.
- Added regression coverage for collecting extension Markdown transformers in load order, including Pi's original two-factory case and exact Pi comparison.
- Added factory-owned Session replacement for embedded Go applications, with awaited settlement and rebinding, cancellation hooks, and collision-safe JSONL import.
- Added regression coverage for assistant cost replacements and loaded-resource listings that preserve restored chat.

### Changed

- Renamed the unpublished release candidate to 0.3.0.
- Compress the embedded Node extension runtime and generate its content digest at build time. Reuse content-addressed materialization across self-contained extension launchers.
- Native Go replacement options support awaited `WithSession` callbacks after rebinding and `Setup` initialization before rebinding.

### Fixed

- Keep repository documentation, governance links, and parity evidence paths aligned with the internal Go package layout.

- Upgrading from PiG 0.2.0 no longer fails on leftover auth, settings, trust or model-cache lock files. PiG safely reclaims idle empty sidecars and reports when an older PiG process still holds a lock.
- Prevent a redundant `fd` or `rg` download when a concurrent request finishes installing the same helper before another request claims the download.
- Route `uninstall` through package removal, including local flags and help, instead of sending the source to the model.
- Retain `"packages": []` after removing the last user or project package.
- Show npm and Git child output as it runs, followed by Pi-compatible failure messages and exit codes.
- Restore package removal and update progress. Batch npm updates by scope and reconcile Git refs without reinstalling unchanged dependencies.
- Replace an installed package's configured ref without losing its resource filters, and remove incomplete Git checkouts after failed installation.
- Use the shared `Error:` label for CLI failures, including model selection and package commands.
- Preserve the Session's effective thinking level when interactive mode starts, so the footer, editor, and provider request agree with the saved Session. Models with sparse thinking-level maps no longer silently disable reasoning at startup. Forward explicit `--thinking` values and model-spec thinking suffixes before constructing the interactive Session.
- Show the active `/tree` branch first, mark only its active path, and recompute connectors after filtering. Digits now enter the search query instead of changing filters. Search and filters preserve selected entry identity through empty results, fall back to the nearest visible ancestor, clear folds on filter changes, and toggle an active non-default filter back to the default view.
- Refuse to clone an unsaved file-backed session without creating a stub file. Match Pi's clone confirmation, empty-fork status, and silent fork cancellation.
- Report unmatched `--session` and `--fork` IDs with Pi's exact standard-error diagnostic and exit status.
- Omit the default session directory from resume hints after `-c`, `-r`, and `--session`.
- Match Pi's error-notice padding and empty-session compaction error. Remove the duplicate provider-failure status and show the one-time `/bug` hint for non-retryable, non-cancelled assistant errors.
- Limit Markdown horizontal rules to 80 content columns, including expanded compaction summaries.
- Restore provider-owned login steps for Amazon Bedrock, Google Vertex AI, Cloudflare AI Gateway, and Cloudflare Workers AI, including provider-scoped configuration in stored credentials.
- Restore `/login <provider>` routing and argument completion, authentication-method search, Escape back navigation, and Pi's cancellation and logout messages.
- Preserve the distinction between login credential-source labels and logout's configured indicator.
- Keep all provider follow-up replies inside the login dialog until authentication finishes. Preserve the configurable secret-input masking setting.
- Show command-argument and fuzzy-file completion descriptions without adding duplicate paths to ordinary file entries.
- Match Pi's HTTP error status, prefix, and body for OpenAI-compatible completion and Responses providers. Share error-body normalization without duplicating SDK messages or serializing unread response streams.
- Respect explicit empty stored keys in Cloudflare and Vertex authentication without changing other providers' environment fallbacks. Preserve API-key input exactly as entered, including surrounding spaces and empty keys.
- Preserve explicit cache-retention request policy, response IDs, image/tool-image payloads, grammar-tool argument streaming and replay identity, and configured reasoning maps across the provider APIs.
- Retain tool-schema declaration order through JSON decoding, transcript copies and strict required-array derivation. Do not sort away ordering differences.
- Keep custom fetch execution, caller-owned redirects and request-context identity alongside native retry, timeout, raw source options and response callbacks.
- Add direct-simple request-auth checks and provider-owned login interactions without changing the native/legacy provider carrier union or the shared credential lock.
- Integrate faux queue/cache/deferred lifecycle and exact provider tests with the canonical model collection, publication and auth implementations.
- Preserve one pinned TCP/TLS connection budget, HTTP idle activation after connection establishment, HTTP/HTTPS proxy CONNECT routing and the two-second address-family fallback interval. Verify the connector default against the current upstream dependency pin and reviewed source digest.
- Preserve retained system-entry order after compaction instead of prefixing duplicate instructions and triggering a second compaction. Keep the original token-budget and event assertions unchanged.
- Preserve selected-model metadata ahead of catalog or map-only fallbacks, Vertex ADC/key routing, Codex-owned retry/response observation with custom fetch, and shared replay across Google/OpenAI provider construction.
- Center Editor scroll indicators on wide borders and preserve their colored, truncated text in narrow terminals. Apply custom border colors after the row layout is complete.
- Fixed Markdown rendering that ignored constructor padding, theme/default-style options, source-preservation flags and disabled math rendering. Nested list blocks, table/quote style restoration, matching code-span delimiters and streamed closing fences now follow Pi's rendering rules.
- Fixed user messages missing OSC 133 prompt zones and retaining stale display transforms after invalidation. User messages now use Pi's Box composition and retain the complete Markdown transformer chain, including after output-padding changes; assistant rows also include both horizontal margins and full-width padding.
- Fixed large main-screen renders building a single unbounded output buffer. Render writes now follow Pi's 1 MiB UTF-16-unit bound without splitting Unicode characters or adding redundant full-render row erases.
- Fixed Kitty images being overwritten during full-redraw row reservation, missed cleanup for raw image sequences from custom components, and unstable image-deletion ordering. Reserved-row growth now recalculates the differential starting position as Pi does.
- Fixed keyboard-driven component updates waiting for the normal render throttle and queued frames repainting after the renderer stops. Keyboard frames now coalesce on the next owner-loop turn.
- Fixed TUI diagnostics ignoring the configured log directory. Redraw logs use `pi-tui-debug.log`; overflow dumps use `pi-tui-crash.log` in the configured directory or OS temp directory. The redraw flag and filesystem failures now follow Pi's behavior.
- Fixed overlay input restoration in regular and fullscreen mode. Eligible overlays regain keyboard input after temporary base focus changes, while an active replacement keeps input until it closes, even when it is not mounted in the render tree, as in Pi.
- Fixed LaTeX math rendering with missing relation spacing and join operators, rejected font switches and multiline arguments, misaligned matrices and cases, and flattened nested display scripts. PiG now renders these expressions as Pi 0.87.1 does.
- Fixed generated Go extension cells forcing locally replaced workspace modules to version `v0.0.0` instead of retaining the highest required version. Release-pinned local consumers build without a manual `go mod tidy`.

- Reject calls through a failed Node factory's captured API before they register more capabilities or publish event-bus effects, preserving the failed-extension error and synchronous rejection.
- Return expanded paste contents to retained terminal-input callbacks instead of exposing compact paste markers to Node.
- Join the interactive input reader and decoder before terminal teardown, including early quit and cancellation. Temporary dialogs retain the shared input owner, and terminal files remain open for external-editor and subsequent-reader handoffs.
- Preserve Markdown transformer order across transcript components and ordinary streaming replacements without blocking the render loop. Equal-text updates rerun stateful transforms, replacement repaints, and removed messages discard queued work. Completion no longer invalidates its own transform revision.
- Preserve the rename field and its cursor in the interactive `/resume` picker. Typing at the first rename inserts before the existing name, and an empty submission leaves the field open.
- Preserve UTF-16 cursor positions and unpaired surrogate units in single-line inputs, including replacement, deletion, undo, and terminal rendering. Single-line word editing uses Pi's word-boundary rules with CJK and Southeast Asian dictionary refinement; D27 is retired. Active dialogs keep their cursor marker, and secret previews position the cursor in UTF-16 units.
- Interactive shutdown no longer hangs when `/scoped-models` or another selector owns keyboard input. Provider-owned login cancels and joins its worker on shutdown or input failure. The parity runner reports owned processes that remain running after tmux scenario cleanup.
- Avoid repeated whole-suffix scans when Markdown renders long unbroken text, invalid email candidates or trailing URL parentheses. Autolink recognition and rendering remain unchanged.

- Retain provider configurations registered before the host registry binds, report queued registration errors with their extension paths, and apply later registration and removal immediately. Reject invalid model configurations before replacing the prior registry entry or native Provider owner.
- Preserve registered Provider `models[].cost.tiers`, including input-token thresholds and all four per-million-token rates.
- Preserve UTF-16 cursor positions and surrogate units in native Editor text, paste, wrapping, character jumps, and key decoding. Terminal framing emits Pi's individual UTF-16 units for supplementary characters.
- Keep remapped submit keys active with `PIG_DEBUG_KEYS`; diagnostics no longer replace the active keybindings with defaults.
- Preserve Chinese/Japanese and ASCII word boundaries and registered large-paste markers through movement, deletion, painting, and mouse positioning. Word movement resets kill accumulation and sticky columns.
- Apply Pi-compatible BOM/NEL whitespace handling to prompt history and submission, including duplicate suppression after trimming.
- Render Mermaid blocks with fences longer than 1,000 backticks without panicking, matching Pi's fence-length behavior.
- Open Windows login URLs through the Windows URL handler without a command shell. Keep OAuth parameters intact and do not interpret provider-supplied URI metacharacters as commands.
- Preserve the owning provider identity on canonical faux models so native harness model references resolve through the same provider.
- Keep submitted editor text during theme detection and managed-tool setup. Report that startup is still in progress instead of discarding or prematurely submitting it.
- Show device-code login instructions and waiting status without opening a browser. Preserve browser launching for authorization URL events, including provider-owned interactions.
- Detach ending extension subscriptions before removing providers, avoiding redundant shutdown catalog rebuilds while preserving awaited shutdown handlers and process cleanup.
- Release SDK-owned event waits during native Provider shutdown without completing the provider's local stream or replacing its result. Preserve Python iterator overrides and Node iterator cleanup on delivery errors.
- Match Pi's resume-picker search ranking, preserve Recent order, normalize quoted phrases and query whitespace, and use UTF-16 positions for scores.
- Preserve lone UTF-16 units in extension terminal-input chunks, rewrites, and editor text during JSON transport. Go uses the shared lossless codec; Rust exposes `JsString` for terminal callbacks and editor text.
- Match Google Gemini and Vertex request defaults for system instructions and tool-calling mode, including explicit `none` and `any` with strict tools.
- Send Mistral's session prompt-cache key, respect disabled caching and explicit affinity headers, and preserve assistant prefix and tool-index fields during replay.
- Preserve extension registration order in RPC's initial active tool list and system-message declarations.
- Dispatch messages and slash commands for remapped editor submit keys instead of clearing input without submitting, including with key diagnostics enabled.
- Reduce repeated startup model-catalog allocation and serialization, including cache-hit copies, while retaining metadata, provider order, authentication state, and errors.
- Preserve the JSON error boundary for cyclic model sampling metadata instead of copying it recursively without end.
- Resume long Sessions without reopening a validated file, projecting messages solely for branch settings, or reparsing history when no cache misses need repricing.
- Bind custom-message actions to the active Session in print, JSON, and RPC. Preserve idle publication, deferred next-turn delivery, and Session-owned triggered runs.
- Forward validated Node extension JSON text across the IO worker boundary without reserializing decoded state graphs. Preserve parser errors, byte accounting, and delivery order, and avoid repeatedly copying incomplete frames.
- Avoid unused TypeScript presentation and schema imports during startup. Shared pinned AI/TUI bundles, validated bytecode caching, and deferred private Unicode initialization retain complete exports, shared identities, and native text segmentation.
- Retain Jiti's source-validated cache across temporary-directory changes, keyed by loader content, Jiti and Node versions, and compiler options.
- Avoid startup deadlocks when native credential refresh overlaps catalog publication. Read the existing runtime-key overlay without reacquiring the registry lock inside the credential transaction.
- Reuse compiled ignore patterns for each resource discovery walk instead of recompiling them for every path. Keep the same resources, diagnostics, and reload behavior.
- Preserve supplied SessionManager identity and derive default storage from the selected Services agent directory. Keep memory-only forks and clones in memory, and defer a disk-backed fork file until its branch contains an assistant response.
- Keep the reload blocker focused and service queued UI work until extension handlers finish. Retain the captured Session during startup binding and suppress stale subscription and title updates.
- Publish custom messages from `agent_settled` handlers before the settlement event reaches mode output.
- Match the editor completion lifecycle: debounce symbol queries, refresh after cursor movement, retain forced path completion, and accept command arguments without submitting. Preserve CJK path boundaries and use the shared list layout.
- Keep native and extension completion requests ordered, cancellable, and UI-owned without blocking the input loop on filesystem or extension work.
- Restore Editor undo grouping, cursor positions, and large-paste contents from pre-edit state. Normalize programmatic changes like pasted text and invalidate wrapping when a paste marker becomes ordinary text.
- Reset kill accumulation on text replacement, replace multiline yanks correctly, and clear sticky columns on line-start movement.
- Use adaptive thinking, native `xhigh` effort, and prompt caching for Claude Opus 5 on Amazon Bedrock, including application inference profiles identified by model name. Omit unsupported `thinking.display` for GovCloud targets.
- Preserve supplied Anthropic Messages model metadata so new and custom models retain their reasoning settings and output limits.
- Preserve `autoload: false`, empty filters, and empty Package lists in project Resource overrides. Returning an override to inherit retains explicitly installed project Packages and unrelated filters.
- Keep ordinary `pig install` error hints on the single-source syntax while retaining multi-source validation help.
- Retry transient explicit update version checks with Pi's bounded management HTTP policy, but not signature or manifest errors. Reject concurrent native standalone updates and retain ownership through receipt commit or rollback.
- Include the selected file path in model configuration read, parse, and schema errors. Report unreadable `models.json` files instead of treating them as absent.
- Preserve supplied harness cancellation signals, reject function-valued event payloads without losing committed state, and restore control values in deterministic source order.
- Emit start frames for empty faux errors, including deferred polling, and retain explicit aborted responses as error events. Clear a finished harness drive before fault publication while retaining unfinished effects.
- Import Sessions without overwriting stored files, with confirmation and missing-working-directory handling. Match HTML export path and error behavior, and allow sharing an in-memory Session.
- Keep `/settings` open while applying changes without extra transcript lines. Complete available `/thinking` arguments without submitting the editor.
- Redraw the transcript and footer after `/clone`, `/new`, and `/resume`, including clearing an old name for an unnamed Session. Match resume status messages and display `/debug` log paths as literal text.

### Added

- `pig update` updates signed script installations in place on macOS and Linux. Release binaries know the manifest URL and trust root, and the installer records an owner-only receipt. Windows standalone installations still use the installer (D39).

- Added **Mask secret input** to `/settings` (`maskSecretInput`, default `true`, D80). Secret login prompts show dots, a grapheme count and a last-four suffix, with no suffix for inputs shorter than five graphemes. False restores Pi's plain-text behavior. Authentication diagnostics redact masked input; credential storage is unchanged.
- Added cancellable, progressive Session listing through bounded joined workers with current-directory filtering.
- Added durable harness compaction with embedded retained messages, sequential summary requests, combined usage, isolated routing, and structured errors. Quoted summaries preserve failed assistant history.
- Added Session-backed scoped-model getters across extension SDKs, retaining callback and list identity, explicitly clearing replicas, and extending nonempty scopes without mutating older lists.
- Added durable-harness prompt and skill loading APIs using the selected execution environment, with source metadata, diagnostics, awaited reads, cancellation, and mapper errors. Harness argument expansion remains distinct from coding-agent expansion.

### Changed

- The README now identifies the pre-stable scope and links its evidence. Its badge reports progress against the pinned Pi release, while coverage reports source porting, behavioral evidence, and upstream test-file dispositions separately.

### Fixed

- Re-check installed tools under the download lock so a completed concurrent download is reused.

- Route main Session requests and cache warming through their shared Model Runtime, preserving provider error records and caller-owned provider callbacks.
- Preserve RPC retry-cancellation event, response and settlement ordering. Full abort waits no longer block later synchronous commands in the same input batch.
- Share provider HTTP error normalization across adapters, preserving status, body reasons, JSON property order and JavaScript whitespace semantics.
- Fixed Python extension provider requests failing with an undefined cancellation signal. Cancellation continues to follow the owning request.
- Preserve Pi's separator newline after `@file` text, including files that already end with a newline.
- Emit JSON and RPC event fields in Pi's construction order, and preserve literal HTML characters in nested JSON strings.
- Preserve built-in tool schema property order in Session records and compaction snapshots, including nested edit parameters and schemas loaded from disk.

- Fixed retained extension contexts sending expired request parents after normal completion. Go, Rust, Python and Node retain the original connection generation and preserve explicit cancellation, including live and empty/reset terminal-input state.
- Fixed schema-parity UI binding and asynchronous shell-hook test completion to match the original Pi contracts without relaxing assertions.

- Fixed final tool results losing text/image order through extension hooks, normalization, events, provider context, Session history, resumed tool displays, and custom renderers. Conversion and resize notes stay immediately after their images; unchanged content retains its identity. Explicit empty text and text signatures survive renderer forwarding. Streaming partial updates remain separately unclosed.
- Fixed `/login` not selecting and saving the provider default when no model is selected, for both OAuth and API-key providers. Dynamic catalogs are awaited without overwriting newer model or Session choices. Thanks @kanishkaverma for identifying the gap in #63.
- Fixed submitted non-secret login input disappearing instead of remaining visible as `> <value>`, and report the credential-file location returned by contributed providers.

- Fixed malformed Package manifests stopping startup. Invalid JSON uses conventional discovery; wrong-typed Pi fields do not discard valid siblings; unrelated Resources keep loading without spurious manifest diagnostics. Explicit installation validation stays strict (#78).
- Added Node extension/runtime parity checks at Pi's engine floor, the qualified Node runtime, and current Node, including loose TypeScript files and malformed Package manifests (#78).

- Fixed extension autocomplete wrappers losing their current provider, state, completion method, or file triggers. All SDKs retain wrapper chains, share them with custom editors, await command arguments, cancel stale queries, and apply completions in input order without blocking rendering.
- Fixed Node extensions reading stale tool-expansion state after dialogs. State reaches extensions before awaited results, including cancellation and failure.
- Fixed Node theme lookups returning list metadata and changes reporting success before application. Extensions receive loaded themes, absent unknown names, and the actual selection result and fallback.
- Fixed extension select and input dialogs ignoring remapped shortcuts or displaying stale hints. Dialogs honor action precedence, keep empty choices open, toggle output through the host, and reset foreground styling correctly.
- Fixed manual compaction completing before listeners or retaining admission during callbacks. Model/auth preparation precedes history preparation, and custom summarization errors retain their messages.
- Fixed compaction mistaking caller-supplied Go providers for registry-owned providers. Custom request/auth behavior remains intact, including print-mode overflow recovery.
- Fixed invalid compaction token settings being defaulted or truncated. Selected-model validation preserves Pi diagnostics; failed next-turn preparation stops further provider work and preserves the failed assistant lifecycle.
- Fixed compaction billing notices, retained-entry display order, working indicators, and interactive cancellation.
- Fixed Session resume handling for malformed leading lines, missing final newlines, explicit empty files, effective CWD overrides, and missing directories. Invalid nonempty files and stored CWD headers remain unchanged.
- Fixed Session subscribers running too late to stop tool effects or automatic compaction after abort. Listeners precede persistence and further work while preserving admission and suspended-preflight ordering.
- Fixed resumed model/thinking settings coming from abandoned branches or omitting assistant model metadata. Unrelated entry properties no longer affect branch settings.
- Fixed supplied Session IDs bypassing validation or being ignored when forking. Valid IDs reach the header and filename; omitted IDs use UUIDv7.
- Fixed forks losing resolved labels and timestamps, retaining removed-label links, misplacing compaction boundaries, or writing assistant-free files too early. In-memory forks stay in memory.
- Fixed older Pi session migration, including version-one links/compaction boundaries and version-two hook messages. Headerless preloaded entries remain unchanged.
- Fixed branch-summary diagnostics, activity-based listing recency, and shared compaction file lists' JavaScript UTF-16 ordering. Continuation still selects by file modification time.
- Fixed missing or non-object tool schemas reaching registration state, including invalid declarations hidden by later namesakes.
- Fixed captured extension contexts losing current UI/mode bindings, including cleared UI and stale-context rejection.
- Fixed interactive shell hooks blocking input. Dispatch, persistence, cancellation, replacement and reload now retain owned lifetimes; IPC workers read owner-published editor snapshots.
- Fixed embedded Rust SDK staging omitting source modules.
- Fixed shell-override validation losing native integer exits or conflating present undefined exit codes with null.
- Fixed handler removal altering the remaining callbacks in an already-started extension dispatch.
- Fixed skill discovery order when frontmatter names differ from directory names, including reload and extension-discovered resources.
- Fixed extension factory errors losing multiline messages to source locations, trailing Node warnings, or sibling failures.
- Fixed flag defaults, shared values, falsy overrides, replica resets, and print/JSON/RPC propagation. Rust exports flag option types.
- Fixed path-loaded skill YAML validation and source-aware diagnostics and precedence.
- Fixed shutdown ordering to drain terminal input before renderer teardown and restore cooked mode afterward. Signal cleanup is awaited without a one-second SIGHUP cutoff or duplicate cleanup.
- Fixed malformed prompt YAML columns/carets and mode-specific resource warnings.
- Fixed command argument whitespace to preserve U+0085, split on U+FEFF, and retain multibyte separators.

- Fixed OpenAI Completions response identity, raw finish reasons, tool-choice controls, strict-schema property order, interleaved content, and first tool-call identity.
- Fixed selected Google and OpenAI thinking maps and budgets, disabled-thinking controls, and compatibility-controlled reasoning and instruction roles.
- Fixed OpenAI cache retention and session affinity, Azure logical model identity and encrypted reasoning, and Responses native tool namespaces and terminal phases.
- Fixed Vertex authentication placeholders and endpoint selection for API keys and Application Default Credentials.
- Fixed Codex OAuth account and scope metadata being lost alongside provider-owned credential fields in storage and SDK bridges.
- Fixed Codex request-tier pricing, header-only timeouts, retries, cancellation, and continuation recovery while preserving per-attempt response observation and explicit-zero semantics.
- Fixed native and registered legacy provider callbacks receiving stock-API simple-option lowering instead of their original source options.

- Replaced deprecated Node module-hook registration with synchronous hooks where available, while retaining the Node 22.13 minimum. CI checks package loading on the minimum and current Node major across Linux, Windows and macOS.

- Fixed extension provider registrations leaving active Session metadata stale. Refresh now preserves independent host listeners and does not resolve credential commands.
- Fixed custom extension provider streams losing callbacks across subprocess boundaries. Initial producers retain ordered events, final results, cancellation and errors within their registry.
- Fixed result-only provider streams hanging after iteration ends, and packed Python factories with matching file names loading a sibling's module.
- Fixed SDK Session timeout/retry settings and assembled header hooks not reaching requests. Explicit zero remains distinct from omission; OpenAI and Anthropic zero header timeouts remain armed, while WebSocket options follow their own timeout semantics.
- Fixed transformed prompt text being redispatched as an extension command. Command handling now precedes input transformation exactly once.
- Fixed idle footer branch updates for reftable repositories and worktrees. Native watches retain the repository captured at footer binding, debounce changes, resolve off the input loop and drain on shutdown.
- Fixed global `httpProxy` settings being ignored. HTTP and HTTPS proxy requests use CONNECT, preserve environment overrides and cancel tunnel establishment with the request.
- Fixed cache-waste accounting ignoring configured model prices, and cache warming ignoring explicit retention or extension decisions.
- Fixed raw Anthropic thinking and effort fields, Bedrock request metadata and raw Google thinking options being discarded. Provider-neutral reasoning stays separate from Google's thinking object.
- Fixed poisoned and cross-model history before both OpenAI request paths, including missing tool results and failed assistant turns. Result/update ordering and Agent recovery remain intact. Tool-only Completions content remains null unless the provider requires an empty string.
- Fixed malformed surrogate handling in tool text while preserving valid emoji and replacement characters.
- Fixed raw OpenAI reasoning effort being clamped, zAI requests losing replay controls, and omitted xAI effort being converted to low.
- Added `ai.ContentText` with Pi's separator behavior, and preserved catalog prices, limits and image support in runtime model conversion.

- Accept the pasted redirect URL or code in `/login` for Anthropic, as Pi does. The paste went to the model as a prompt, and a failure after the browser callback showed as "Login cancelled.". Login dialogs for every provider also accept a terminal paste, which they dropped.
- Send `clear_thinking: false` with Z.ai thinking requests, as Pi does, so Z.ai keeps prior-turn reasoning instead of discarding it. Thanks @ByronFinn (#75).
- Remove packed and isolated extension stderr logs after normal shutdown and `/reload` for Node, Go, Python and Rust. Keep logs referenced by load or crash diagnostics for troubleshooting, including structured Node factory failures.
- Report an extension crash once, rather than again for each interrupted command or a racing socket-close notification. Preserve ordinary command errors and Pi's extension error presentation.
- Defer stored credential refresh to the first request for providers that support request-time authentication. Startup no longer fails early on an expired login, and logging out during a session restores the environment key instead of reusing the deleted credential. Keep Radius credentials on their gateway-specific authentication path.

### Contributing

- Add `make generate` to refresh committed inventories, coverage, and documentation mirrors. Drift failures name the repair command. The Go interface generator writes its committed file by default and uses the same target on macOS and Linux.
- Keep help regeneration independent of the invoking agent's configuration and project directory. Capture Pi's complete output before filtering, and preserve the committed help if generation fails.
- Document the required GitHub-verified commit signature separately from DCO sign-off, with SSH signing setup and unsigned-commit repair instructions.

## [0.2.1] - 2026-09-26

Hotfix for Pi extensions from npm that failed to load or crashed in 0.2.0, reported on Reddit by rokrdev and WorriedAcanthisitta3. `pig --version` prints `0.2.1+0.87.1`.

### Fixed

- Fixed expanded extension-renderer parity checks waiting for an optional tmux warning. Readiness now uses the real idle editor/footer in both hosts, while the complete escaped tool-card comparison remains unchanged.

- Fixed `/changelog` title spacing, borders, and content padding to match Pi's inline release view, including an empty changelog. Markdown headings now remain bold at every heading level.

- Fixed Session model selection accepting unconfigured providers, and added model/thinking cycling with scoped preferences and session-only defaults.
- Fixed direct Session prompts bypassing extension input/command handling, and native command contexts copying live system-prompt options. Tool activation now replaces stale options without discarding command edits on ordinary reads.

- Fixed empty-file read results losing their explicit empty text block in Session history, saved sessions and resumed tool results. Empty arrays and image-only results remain distinct.

- Fixed the initial bash streaming update inserting an empty text block in JSON and RPC output. It now carries `content: []`, while later output updates and completed empty reads keep their text blocks.
- Fixed tool failures and pre-execution cancellations omitting `details: {}` from RPC and JSON events, tool-result messages, and saved sessions.
- Fixed cancelled direct bash commands reporting `exitCode: -1` instead of omitting the exit code. Bash messages now retain their numeric completion timestamp in saved sessions and message queries, including deferred results.

- Session clones discard obsolete labels, reconnect the retained branch, and preserve active label timestamps and compaction boundaries.
- Session entries use Pi's eight-character hexadecimal IDs and millisecond ISO timestamps. Generated IDs are checked against the Session index before appending, so a collision cannot overwrite history.
- Print and JSON modes terminate by SIGINT instead of returning numeric exit status 130, so process supervisors observe the same signal status as Pi.
- A disconnected interactive terminal exits with status 129. SIGHUP on a live terminal restores it and exits normally.

- Fixed parity checks hiding differences behind partial output and broad normalization. Checks now compare complete JSON events and exported artifacts, retain stderr and RPC framing, and use new-output barriers. Each normalization rule carries a reason in a CI-checked inventory. System-message comparisons can alias the exact approved product names and documentation destinations without discarding instructions, whitespace, or counters.
- Fixed Porter and Standard validation gates reading the invoking user's settings and trust stores. Validators now use temporary home and agent directories while retaining the Go compiler and module caches.
- Fixed Piglet Binary build, signature, and publication tests reading the machine's trust stores. Test binaries now run with private home, agent, and project directories, including Pi-compatible directory overrides.
- Fixed the extension subprocess test suite repeatedly rebuilding shared fixtures and Rust SDK dependencies. Direct `go test` runs now reuse package-owned build artifacts, and in-process deadline checks advance virtual time without weakening their assertions.
- Fixed terminal color parity checks failing with Node stdin `EAGAIN`. The Pi oracle reads completed JSON from its own temporary file instead of a concurrently filled pipe.
- Isolated user-package metadata lookups from the invoking project's `.npmrc` while retaining the selected npm, pnpm or Bun command and user configuration (security divergence D79, approved by owner Michael Kinsy 2026-09-27). Trusted project packages keep their project registry. Updates now check installed versions before reinstalling.
- Fixed package operations executing an untrusted project's `npmCommand`. Install, remove, update, Git dependency installation, pnpm lookup, and missing-package restoration now retain the caller's resolved trust and command overrides. Updates use saved trust or explicit approval, as in Pi.
- Fixed failed package removal dropping its settings record and discarding command overrides before the uninstaller ran. Removal now persists only after the command succeeds.
- Fixed RPC prompts, steering and follow-ups being blocked or misclassified while an extension waits during preflight. Preflight now stays idle, permits other requests, and preserves Pi's acceptance, queue reply and first-event ordering. Input dialogs remain responsive, and bash can still finish while a Provider is pending.

- Fixed RPC steering, follow-up and shell completions bypassing the input-turn continuation queue. Queue replies no longer overtake later events in the same command batch, shell completions retain their independent lifetime, and queue rejection messages preserve the command's leading slash.
- Fixed empty prompts losing their text block in Session history and RPC events. Empty text now remains an explicit text block, including when image attachments follow it.

- Fixed RPC thinking-level and compaction responses overtaking their Session events. Clients now receive the mutation events before the corresponding response, including failed compactions.
- Fixed switching to a missing session path failing instead of opening a new session at that path. The file remains unwritten until the first assistant response, as in Pi.
- Fixed initial session metadata recording the configured thinking default instead of the explicit `--thinking` level. Startup now selects and clamps the effective level before recording it.
- Fixed RPC retry recovery omitting `entry_appended` for persisted context edits. Incremental clients now receive the same recovery entries that later session queries return.
- Fixed RPC malformed-JSON and invalid-fork diagnostics differing from Pi. JSON diagnostics now preserve V8's object and array context, UTF-16 positions, line and column information, and lone-surrogate escapes.
- Fixed HTML session exports using outdated skill rendering, tool-output spacing, Markdown sanitization and vendor scripts. Exports now embed Pi's pinned assets, preserve multiline previews, and match Pi's session payload encoding and custom-tool ANSI rendering.


- Fixed custom headers in `@companion-ai/feynman` and `pi-cc-extensions` losing their surrounding blank rows. Header spacing now belongs to the host container, as in Pi, and survives replacement and empty custom headers.
- Fixed `pi-cc-extensions` showing a zero context window. Extensions now read the Session's context estimate, preserve absent and unknown usage instead of fabricating zeros, and retain fractional percentages across all SDKs. Node snapshots also clear removed models, session names/files and empty lists, and `pi.setModel()` now awaits the host result before exposing the new state.
- Fixed gentle-pi's custom footer failing on `ScrollView`, being erased by unrelated Node extensions, or displaying a Git branch for a repository created after the footer initialized. Node extensions now use Pi's pinned TUI components, image and capability helpers, screen classes, and native clipboard helpers instead of throwing stand-ins. Clearing or replacing a custom footer or header also disposes its component.
- Fixed slow cold starts for Node extensions that do not import the coding-agent SDK. PiG now loads SDK modules on demand and keeps unused Session cleanup and editor helpers from loading the full SDK graph.

- Fixed `pi-btw` and other Node extensions failing with "createAgentSession is not yet supported". Extensions now use Pi's independent Session, ModelRuntime, tools, resources and persistence implementations. Child requests retain their own models, credentials and provider callbacks, and cancellation does not change the main Session.
- Fixed child Session construction blocking on Go-created auth and model-store lock files. Go and Node now share Pi's directory-lock protocol for auth, model, settings and trust stores. A regular `.lock` file left by an earlier build is not deleted automatically; stop all writers before removing an obsolete sidecar.
- Fixed `pi-btw` follow-up requests opening a second overlay and blocking. `ui.custom` now publishes its mounted handle, supports ordinary focus and visibility controls, and supplies measured stdout dimensions so the package uses the correct overlay height.
- Replaced the remaining coding-agent shim stand-ins with Pi's complete pinned SDK module graph, including compaction helpers, tool renderers, themes and independent UI classes. Absolute imports through the host package root now find the SDK. Live Go Main Screen identities remain separate (D73).
- Fixed registered native Provider objects being unavailable across extension SDKs. Go, Rust and Python can author and retrieve their callbacks, and Node extensions can retrieve objects from other processes. The carrier preserves authentication contexts, publication callbacks, synchronous model/filter methods, stream creation, deferred methods and cancellation. Builtin/composed native SDK objects and final qualification remain tracked by D78.
- Fixed packages that hard-code Pi's directories missing PiG sessions and prompt templates. Set `PIG_USE_PI_DIRS=1` to use Pi's agent directory and project `.pi` resources, including Powerline's recent sessions and pi-acp's templates and session discovery. Separate `.pig` directories remain the default (D2).
- Fixed auth, trust and dynamic model-catalog cache writes using locks that Pi could not observe. PiG now uses Pi's directory-lock protocol, preserves existing auth file permissions, and retains required empty OAuth fields when files are shared with Pi.
- Fixed `ctx.getSystemPrompt()` returning an empty string in headless extensions. Print, JSON and RPC contexts now return the Session's configured prompt, including project prompt files, as Pi does.

- Fixed automatic theme pairs such as `light/dark` not following terminal appearance changes. PiG now enables terminal color-scheme notifications, switches the live interface without overwriting the pair, and keeps switching active in settings and across terminal suspend/resume, as Pi does.

- Fixed RPC model-cycle replies racing later synchronous replies in the same input batch. Model cycles now apply their state changes in input order, wait independently for extension listeners, and construct their responses after the await boundary, as in Pi.

- Fixed built-in tool results leaking internal rendering metadata into RPC events and saved sessions. Ordinary read and write results now omit details, and truncated or limited results use Pi's sparse lowercase fields. Grep and ls retain fractional limits, and byte-truncation metadata reports Pi's maximum line limit.
- Fixed JSON and RPC tool-execution results duplicating `isError` inside `result`. The flag remains on the event and on persisted `toolResult` messages, as in Pi.
- Fixed idle RPC processes ignoring SIGTERM while clients kept stdin open. RPC now disposes the runtime before exiting 143; SIGHUP exits 129 on Unix.
- Fixed `agent_end.messages` including earlier conversation turns on later prompts. It now contains only messages produced by that run, as in Pi.
- Fixed RPC output escaping Unicode line and paragraph separators instead of preserving them as Pi does. JSONL framing still splits only on LF, and literal backslash escapes remain unchanged.

- Fixed Go SDK Sessions ignoring `defaultTools` and dropping inactive built-in definitions. Sessions now keep admission and activation separate, apply allowlists and exclusions to late extension tools, retain source metadata, and honor `noTools` without losing extension tools.
- Fixed tools called directly through a Go SDK Session missing the Session context and `PI_*` environment values. Tool wrappers now bind the live context while preserving cancellation, argument preparation, and mutation ordering.
- Fixed Windows shell cleanup depending on `PATH` for `taskkill`. It now uses the System32 executable, starts it detached and hidden, and consumes spawn failures without switching to a different kill operation, as Pi does.
- Fixed find-tool result details dropping a reached zero limit and truncating fractional limits. Custom glob operations now use the same root-relative path and output-limit handling, and find warnings retain the requested numeric value.
- Fixed tool-guideline whitespace handling to match JavaScript: BOM is trimmed, while NEL remains part of the guideline.
- Fixed extension tools without an explicit execution mode running sequentially. They now run in parallel, as in Pi, while explicit sequential tools remain serialized.
- Fixed Go SDK Sessions retaining stale tool instructions after an extension changes active tools during a run. The next provider request now records the rebuilt tool prompt, includes extension prompt snippets and guidelines, and preserves any `before_agent_start` prompt override.
- Fixed Go bash tools rejecting caller-supplied execution operations and losing the `ENOENT` code in process-spawn errors. Delegated operations now bypass local shell resolution, and spawn errors retain Pi's message and their underlying Go error.
- Fixed built-in Go tools ignoring the invocation context's working directory. Read, write, edit, grep, find, ls, and shell execution now resolve against `ctx.cwd` when provided, as in Pi.
- Fixed Go edit tools continuing to read a file when cancellation arrives during a successful access check. Custom edit and write operations now keep the shared mutation queue locked until each operation completes, including an aborted write.

- Fixed Go agent subscribers seeing future prompt messages and missing just-completed assistant messages. Transcript state now commits at `message_end` before listeners run, and rejected continuation preserves the previous turn's error.
- Fixed Go agents lacking the `ConvertToLlm` callback for application-defined message types. The callback runs after context transformation, is awaited before provider dispatch, and reports failures through the agent lifecycle.
- Fixed Go agents retaining the caller's initial tool slice and using different busy-run and continuation error text from Pi. Initial loadouts are copied, error text matches Pi, and native Sessions still reject a missing model before accepting a prompt.

- Added the missing environment-backed harness read, write, edit, and bash factories. They preserve injected image processing and shell preparation, bounded progress checkpoints, and same-file mutation ordering through cancellation, as in Pi.
- Fixed Go agent lifecycle subscriptions not receiving an agent-owned abort signal or exposing a wait-for-idle boundary. `Abort`, `Subscribe`, and `WaitForIdle` now share the active run, retain pending-tool and error state, and wait for the provider's terminal response and event listeners before settlement.
- Fixed Go agents rejecting an injected stream function when its model has no native provider runtime. Hosts can also configure the default stream function used by callers that omit one, as in Pi; stream failures retain the model's provider identity instead of dereferencing a missing runtime.

- Fixed model metadata lookups executing configured key commands, missing configured-auth diagnostics, and Copilot account filters exposing unavailable models. Registry request-auth resolution now preserves exact per-request command and header semantics.
- Fixed registry re-registration losing models or headers, validation replacing valid registrations, and empty model-input or fallback arrays reverting to defaults.
- Fixed native coding-runtime provider registration losing auth or deferred behavior through model overlays, and legacy refresh/OAuth catalogs not reaching the shared runtime. Invalid refreshed catalogs are rejected before publication.
- Fixed custom-model thinking suffixes losing reasoning metadata, print/JSON ignoring the selected thinking level, and custom Responses models using the Completions endpoint.
- Fixed persisted model defaults not extending an existing Session model scope.
- Fixed `/scoped-models` closing when no models are available, dropping unresolved settings, and displaying enabled catalog models as duplicate unavailable rows. Selection now keeps unavailable configuration separate from the available cycling scope.
- Fixed direct `/model` searches opening the picker before refreshing a cache miss. Refreshes now run off the input loop, and an exact refreshed match is selected without opening the picker.
- Fixed long interactive shell commands overflowing the terminal width and crashing the renderer.
- Fixed `PI_CACHE_RETENTION=none` incorrectly disabling Anthropic caching. As in Pi, only the explicit `cacheRetention: "none"` request option disables caching; environment values other than `long` use short retention.
- Fixed the Go model-collection API missing native provider lifecycle, provider-owned catalog publication and request-auth dispatch. Cancellation now stops waits without allowing late catalog or credential writes, and availability reports the first failed auth check without waiting for unrelated checks.
- Fixed injected empty environment values being treated as configured API keys, and environment-backed auth ignoring cancellation raised during lookup.
- Fixed the Go image-model runtime missing provider registration, auth resolution and coalesced dynamic catalog refresh. Image requests now merge provider and explicit env/headers, and undefined header overrides remove intrinsic model headers.
- Added the upstream model-data validation command for hydrated catalogs, including model identities, API groups, manifest hashes, schema and generation timestamps.
- Fixed image-catalog generation rejecting OpenRouter JSON responses instead of validating and parsing their image models, modalities and pricing. Stock catalog generation still uses the pinned published Pi catalog.
- Fixed model-store writes continuing to wait after cancellation, and repeated or concurrent catalog reads taking redundant file locks. Readers now share revision-cached reloads without one cancelled reader aborting another.
- Fixed model catalog refresh diagnostics listing failed providers alphabetically instead of in completion order, and using the wrong multi-provider warning text in `/scoped-models`.
- Fixed `/scoped-models` omitting Google and other configured built-in providers because it applied a smaller provider allowlist instead of using the Model Runtime's available catalog.
- Fixed Fireworks Kimi K3 dropping the requested reasoning effort, and Anthropic-compatible requests ignoring explicit cache retention, using the wrong session header for OpenRouter, and discarding configured model compatibility overrides.

- Fixed `ctx.abort()` doing nothing in extensions attached directly to a Go SDK Session. A preflight handler can now cancel a parallel tool batch before any prepared tool executes, as in Pi. The Session also keeps the abort binding when its runner is replaced, and a clone does not take ownership from the source Session.

- Fixed built-in and extension tools rejecting model arguments that Pi accepts, including `read` calls with `offset` or `limit` set to `null` or a numeric string. PiG now removes optional non-nullable nulls, applies TypeBox or plain JSON Schema conversion, and passes the converted arguments to hooks and tools. Validation failures use Pi's error messages instead of jsonschema implementation diagnostics.

- Fixed stock settings selecting the dark theme on light terminals. PiG now queries the terminal background before extension startup and saves a detected light or dark theme, as Pi does, while keeping an explicit theme unchanged. Terminal replies no longer reach the editor, and `COLORFGBG` decimal prefixes and OSC color replies with extra channels are parsed as in Pi.

- Fixed `/login` API-key providers and `--list-models` omitting catalog providers such as OpenCode Go, DeepSeek and xAI. Provider availability now comes from the catalog, as in Pi, instead of hand-maintained subsets (contributed by @ShoichiTect; fixes #50 and #53).

- Fixed Node extensions registering native Providers without adding their models to the host catalog. Native model selection and agent turns now call the extension's stream, authentication and refresh callbacks through the existing subprocess transport, including cancellation, provider errors, persisted catalog updates and OAuth credential rotation. SDK Provider object carriers remain tracked by D78.

- Fixed extensions reporting `ctx.hasUI` as true in print and JSON modes. As in Pi, these modes now report false and UI calls return their no-op defaults without running component factories or retaining widgets and subscriptions. Interactive and RPC modes still report true. The Go, Rust and Python SDKs expose the same host state through `HasUI()` and `has_ui()`.

- Keep same-version Piglet releases and same-path prompt files independent in a monorepo. Reject release identity mismatches, unsafe source paths, and modified pinned checkouts before installation.

- Fixed extension commands' `ctx.newSession`, `ctx.fork` and `ctx.switchSession` reporting unsupported in every mode. They now reach the Session owner in interactive, print, JSON and RPC modes, honor cancellation, preserve fork position and parent-session options, and await the replacement lifecycle. Interactive replacement failures record the fatal error and exit 1, as in Pi. Existing host-scoped runner and startup-project limitations remain (D30, D61); replacement callbacks remain listed as missing in the SDK surface matrix.
- Fixed extensions retaining an outgoing session's history after an empty replacement, or receiving only the tail of a longer replacement. Subscription cursors and the Go, Rust, Python and Node mirrors now reset with session identity. Resuming a saved session also preserves its persistence mode for later forks and new sessions.
- Fixed a tool result's `usage` being dropped between extension processes and PiG's host, for every extension runtime.
- Fixed `ctx.compact()` in Node extensions ignoring `customInstructions`, and `ctx.newSession({ parentSession })` and `ctx.fork(id, { position })` ignoring their options: the runtime nested them where PiG's host did not read them. `ctx.compact({ onComplete, onError })` now reports the compaction's result or failure, as in Pi, in the Node runtime and the Go, Rust and Python SDKs.
- Fixed Node extensions' `session_before_compact` and `session_before_tree` handlers receiving no `event.signal`, and `ctx.thinkingLevel`, `ctx.ui.getEditorComponent()`, `ctx.ui.theme.name` and `ctx.ui.theme.sourcePath` being undefined.
- Fixed a provider model's `inputLimits` and `promptCache` being dropped when an extension registers a provider.

- Fixed missing extension registry and session reads that broke model pickers in `@narumitw/pi-btw` and `pi-advisor-flow`, and disabled `billion-context-pi`. The Go, Rust and Python SDKs now expose session projections, tree and label reads, registry catalog and authentication queries, refresh, and provider configuration registration.
- Fixed imported `SessionManager` and `ModelRegistry` classes being empty stand-ins. Extensions now use Pi's own implementations for independent sessions and registry facades. Composed Node providers use Pi's API-key authentication methods instead of an empty object.
- Fixed extension session projections retaining fractional milliseconds or overflowing timestamps outside the UnixNano range. Relative `--session-dir` paths remain relative in extension reads.

- Fixed typing `/btw` and pressing Enter selecting a same-name skill instead of the extension command. Autocomplete now puts extension commands before skills, as Pi does, so equal fuzzy matches preserve command priority.

- Fixed `/login` leaving the model unset after saving an API key or completing OAuth. PiG now selects and saves the provider's default model, including OpenCode Go's `kimi-k2.6`, applies its thinking level, and keeps an already selected model, as Pi does. Dynamic catalogs finish loading before selection when needed; Radius uses its first available model if `balanced` is absent, and llama.cpp shows model-selection guidance. The API-key picker now includes all of Pi's built-in providers with their current names (reported by zRafox on Reddit).
- Fixed the untrusted-project warning never appearing at interactive startup, including with extensions disabled or quiet startup enabled. PiG now shows Pi's warning when an untrusted project has local resources, after any resumed messages, and uses Pi's trust prompt wording, multiline title styling and key hint colors. Print, JSON and RPC mode remain silent about untrusted projects, as in Pi.
- Fixed `/settings` showing terminal image controls inside tmux when the outer Herdr pane supports graphics. As in Pi, tmux and screen disable automatic image detection; image resizing and provider-blocking settings remain available.
- Fixed `/name` confirmations and lookups missing Pi's spacing, padding and theme color, and interpreting Markdown in session names instead of showing the name literally.
- Fixed `/hotkeys` showing a flat action list instead of Pi's bordered Navigation, Editing and Other tables. The tables show the effective editor and application keybindings, with Pi's paragraph spacing.
- Fixed the gap above the model picker disappearing when it replaces the editor, and cancelling `/model` adding an extra transcript message. The host now owns the above-editor spacer, as in Pi.
- Fixed `/tree` header spacing, title styling and the bottom gap when the current filter shows no entries.
- Fixed `after_provider_response` handlers never running. Extensions now receive Pi's response status and headers before stream consumption, with handlers awaited in registration order and return values ignored. This applies to Node, Go, Rust and Python extensions, including shared runtime cells.
- Fixed extension tools' `promptSnippet` and `promptGuidelines` being ignored in the system prompt. PiG now uses registered tool metadata in active-tool order, normalizes snippets and deduplicates guidelines as Pi does, removes inactive contributions, and honors built-in overrides without falling back to their old descriptions. The Go and Rust SDKs expose `ToolPromptSnippet` and `tool_prompt_snippet`; Python accepts `prompt_snippet` when registering a tool.
- Fixed explicit `--skill` paths taking precedence over discovered skills. As in Pi, resolved project, user and Package skills load before additional `--skill` paths, so the first same-name definition wins. `--no-skills` still loads explicitly requested skills, and untrusted projects do not contribute ancestor `.agents/skills`.

- Fixed `@plannotator/pi-extension` and `@kontextmind/kxm` failing to load skills declared as Markdown files. PiG now keeps file entries, reads their frontmatter names, and ignores ordinary Markdown without a skill description, as Pi does. An extension declared as `"./"` now resolves its directory entry in Pi's import order and keeps its package label.
- Fixed `context-mode` and `agent-comms` failing on manifests intended for other agent harnesses. A Package with a `pi` manifest no longer reads vendor plugin overlays, and inline hook or MCP configuration in those overlays is not treated as a list of Resource paths.
- Fixed Package themes, including `@companion-ai/feynman`'s `feynman` theme, missing from the startup `[Themes]` list and extension theme-source metadata. Themes loaded from files now retain their source paths.
- Fixed `**` entries in Package manifests missing root-level and deeply nested Resources. Discovery, validation, missing-member inspection and exclusions now use recursive glob matching, with visible-path filtering and symlink traversal matching Pi.
- Fixed duplicate skill names inside a Package stopping startup. PiG now keeps the first valid definition and shows Pi's `[Skill conflicts]` warning with the selected and skipped paths.
- Fixed `pi-vim` failing at startup with "SettingsManager.create is not a function" (reported by slypheed on Reddit). Extensions now use Pi's `SettingsManager` over PiG's settings files and Pi's real `CustomEditor`, with input, rendered frames, autocomplete, history, cursor control and app actions connected to the host. Editor-local clear and follow-up actions finish synchronously, so Ctrl+C cannot erase the next key.
- Fixed multiline extension editor dialogs missing the external-editor hint and action. The configured shortcut now hands the terminal to the external editor, restores completed edits without submitting the dialog, and repaints the screen on return.

- Fixed `pi-mcp-adapter`'s `/mcp` panel crashing the extension process, and with it every extension sharing that process. PiG's `Container` had no `clear()`, and `ctx.ui.custom` factories received an empty object instead of a keybindings manager, so the first arrow key or Enter threw. Factories now get pi-tui's `KeybindingsManager` with Pi's default bindings, and the component they return is focused, as in Pi, so an `Input` or `Editor` shows its cursor (reported by rokrdev on Reddit).
- Fixed Node extensions that export their default with `export { name as default }`, as bundlers emit, or with `module.exports`, being rejected with "has no default extension export". PiG now imports the module and checks its default export at load time, as Pi does, and reports a module without one with Pi's "does not export a valid factory function" message (reported by rokrdev on Reddit).
- Fixed Node extensions failing to load when they import any value from `@earendil-works/pi-coding-agent`, `@earendil-works/pi-tui` or `@earendil-works/pi-ai` that PiG's modules did not provide, such as `isToolCallEventType`. PiG now provides every export of Pi 0.87.1's packages, checked in CI against Pi's own `index.ts`. Helpers that run unchanged outside Pi (session context, frontmatter parsing with the same YAML library, transcript and message conversion) are Pi's own code; values that only exist inside Pi's own process throw a clear error naming them when called (D73; reported by rokrdev on Reddit).
- Fixed PiG refusing to start with packages whose `pi` manifest declares a folder of skills, such as `pi-lens`, `@upstash/context7-pi` and `@dietrichgebert/ponytail`. A declared skills entry may be a folder of skill folders, as in Pi, and a declared resource path that matches nothing is skipped at startup, as Pi skips it, instead of stopping PiG. A package with a `pi` manifest loads only what it declares, so ponytail's `hooks/` folder of other agents' hook configs is no longer read as PiG hooks (reported by WorriedAcanthisitta3 on Reddit).
- Fixed a `pi.extensions` entry that names a directory without an index failing with "resolves to N entrypoints" or "no extension entry file". As in Pi, each `.ts` and `.js` file and each subdirectory entry in the directory loads as its own extension, a directory with no entries loads nothing, and a `-e` directory with a `pi` manifest loads each entry its manifest yields.
- Fixed `pi-hermes-memory` failing to load because `@earendil-works/pi-ai/compat` lacked `completeSimple`, `getModel` and other exports. The pi-ai root and `/compat` now serve Pi 0.87.1's own compat module: `getModel`, `getModels` and `getProviders` read Pi's model catalog, `getEnvApiKey` follows Pi's environment rules, and `stream`, `complete`, `streamSimple` and `completeSimple` use Pi's dispatch, with the request itself running on PiG's port of the same provider. An explicit `apiKey` wins over configured credentials, a missing key fails with Pi's "No API key for provider" error, and aborting the signal cancels the request (D74; reported by WorriedAcanthisitta3 on Reddit).
- Fixed `pi-lens` failing on every turn with "Cannot read properties of undefined (reading 'runtime')" and its "LSP Inactive" footer status not showing. Methods of `ctx` and `ctx.ui` now keep working when an extension stores one in a variable and calls it later, as they do in Pi (reported by WorriedAcanthisitta3 on Reddit).
- Fixed extension tools that define `renderCall`, `renderResult` or `renderShell` being drawn with PiG's generic tool header, as it appeared above `pi-mcp-adapter`'s compact tool card. PiG now draws them as Pi 0.87.1 does: the call and result renderers share one state per tool card and receive their last component, `context.invalidate()` runs them again, the result renderer sees partial results while the tool streams, a result no longer expands the card, `renderShell: "self"` draws the tool's own framing, and a renderer that throws shows Pi's fallback. Node extensions' renderers run in the extension process. The Go, Rust and Python SDKs gain the same renderers (`SetToolRenderers`; `render_tool_call`, `render_tool_result` and `tool_render_shell`; `tool_renderers`). An extension's override of a built-in tool draws the built-in renderer for any half it does not define, as Pi does.
- Fixed extension commands' argument completions never showing, such as `/mcp reconnect` and the other `pi-mcp-adapter` subcommands after `/mcp `. PiG now asks the extension for them as Pi does, and the Go, Rust and Python SDKs can define them (`RegisterCommand` with `GetArgumentCompletions`; `command_argument_completions`; `command(..., get_argument_completions=)`). Enter with an argument completion highlighted now puts it in the editor without running the command, as in Pi.
- Fixed extensions loading in a different order from Pi, which decides which extension wins when two register the same tool, command or shortcut. PiG loaded Packages first and `-e` extensions last. It now loads `-e` extensions, then project settings entries, project extensions, user settings entries, user extensions, then Packages, as Pi 0.87.1 does. `get_commands` and `pi.getCommands()` now report user extensions with scope `user` and auto-discovered extensions with source `auto`, as Pi does.
- Fixed skills and prompt templates that an extension adds from its `resources_discover` handler missing in print, JSON and RPC mode, such as `pi-lens`'s and `pi-mcp-adapter`'s skills. These modes now ask extensions for resources after `session_start`, as Pi does. `get_commands` and `pi.getCommands()` attribute these resources to the extension that added them, and the system prompt lists the skills.
- Fixed PiG loading skills, prompts and themes from a Package's conventional directories when its `pi` manifest does not declare that kind. Pi loads only what the manifest declares, so `pi-mcp-adapter`'s `mcp-scripting` skill now comes only from the extension, as in Pi.
- Fixed an extension handler error printing PiG's own Go stack trace under the error line. PiG now shows the stack of the error the extension threw, as Pi does, and none for a plain returned error.
- Fixed `pi.getActiveTools()` listing tools an extension had turned off in interactive mode, and returning an empty list in print and JSON mode, where `pi.setActiveTools()` did nothing. `pi-lens` turns its on-demand tools off at session start, and they stayed on under PiG. Both calls now read and set the session's active tools in every mode, as in Pi, and RPC mode no longer prints a line to stderr for each `setActiveTools` call.
- Fixed offline mode not reaching extensions when it was turned on with `PIG_OFFLINE`. PiG now sets `PI_OFFLINE=1` and `PI_SKIP_VERSION_CHECK=1` for `--offline`, `PIG_OFFLINE` and `PI_OFFLINE`, as Pi does, so `pi-auto-update` skips its `pi update` run in offline mode instead of reporting "Pi auto-update failed" at startup.
- Fixed `pi.getAllTools()` and `pi.getCommands()` in Node extensions returning tool and command names instead of Pi's `ToolInfo` and `SlashCommandInfo` objects, and leaving out inactive built-in tools such as `grep`, `find`, `ls` and `powershell`. `pi-mcp-adapter` therefore never recognized a native tool the model asked it to call. Both lists now match Pi 0.87.1's in every mode, including `sourceInfo` for tools and commands from `-e` extensions and for `--prompt-template` and `--skill` resources, which RPC `get_commands` now also reports with Pi's `temporary` scope, and `getCommands()` also lists prompt templates and skills; print, JSON and RPC mode had answered it with an empty list. The Go, Rust and Python SDKs return the same fields; the Go SDK's `ToolInfo.Source` stays populated and is deprecated in favor of `SourceInfo`.
- Fixed `pig -e ext.ts -p "/command"` and `--mode json "/command"` sending an extension command to the model instead of running it. Print and JSON mode now route prompts as Pi does: an extension command runs, and prompt templates and `/skill:` commands expand.
- Fixed the startup resource list putting each section's heading and list on one line and labelling extensions by their entry file (`dist`, `index`). Each section now shows its heading with the list below it, package extensions are labelled by package and entry (`pi-lens:dist`, `@upstash/context7-pi:context7.ts`), other extensions by their shortest unique path, and Ctrl+O expands every section into Pi's user, project and path groups. The list also shows `[Themes]` and the system prompt files in `[Context]`, stays above the transcript when the chat is rebuilt, and is rebuilt after `/reload`, as in Pi. Ctrl+O shows Pi's "Tool output: expanded" and "Tool output: collapsed" status.
- Fixed `/q <message>` from `pi-msg-queue` showing the queued user message above "Follow-up message sent.". PiG now shows a prompt's user message when the agent starts the turn, as Pi does, so a notification an extension sends right after `pi.sendUserMessage()`, and output from `before_agent_start` handlers, appears before it, and a prompt that is rejected before the turn starts is not shown.
- Fixed a collapsed `read` card showing the full path for skill files, PiG's own documentation and context files. As in Pi, reading a `SKILL.md` shows `[skill] <name>`, a documentation page `read docs <page>`, and `AGENTS.md` or `CLAUDE.md` `read resource <path>`, until Ctrl+O expands the output.
- Fixed prompt templates and themes of the same name resolving in a different order from Pi. Within a scope, a file named in settings now wins over an auto-discovered one, and `--theme` files, then project, user and Package themes win in that order, as in Pi.
- Fixed HTML export drawing tools of Node, Go, Rust and Python extensions with the default card instead of their `renderCall` and `renderResult`, and RPC `export_html` ignoring every extension renderer. The export now asks the extension for each frame and waits for it, as Pi's export calls the renderers. Extensions in RPC mode also get the active theme, as in Pi.
- Fixed a shell command that the durable agent harness's execution environment (`agent/harness/env`) started just as the environment was being cleaned up being missed by the cleanup, which left the process running and the call waiting on it. A command now becomes known to cleanup as it starts, as in Pi.
- Fixed output a tool streams in the experimental Pico3 agent kernel sometimes being committed after the tool's next progress update, so a reader saw the progress without the output. A progress update now waits for the tool's streamed output, keeping its writes in order, as in Pi.
- Fixed Pi's theme helpers returning uncolored text inside extensions, and `ctx.ui.theme` lacking `getFgAnsi`, `getBgAnsi`, `getColorMode`, `getThinkingBorderColor` and a colored `getBashModeBorderColor`. `getSelectListTheme`, `getSettingsListTheme`, `getMarkdownTheme`, `highlightCode`, `keyHint`, `rawKeyHint` and `DynamicBorder` now draw with PiG's active theme and the same highlighter as Pi, so `pi-rtk-optimizer`'s settings panel border takes the theme's accent color, as in Pi.
- Fixed pi-tui's `Input`, `Editor`, `SelectList`, `KeybindingsManager` and `Markdown` being simplified stand-ins inside extensions. They are now Pi 0.87.1's own code, so extension prompts get cursor movement, word and line editing, kill and yank, undo, paste handling, horizontal scrolling, multi-line editing with history and autocomplete, and filterable, scrolling select lists, and `Markdown` renders through the same `marked` release, with links shown as the host terminal supports them. The other pi-tui components extensions build panels from (`Box`, `Container`, `Text`, `Spacer`, `SettingsList`, `HStack`, `VStack`, loaders), fuzzy matching, `CombinedAutocompleteProvider` and `StdinBuffer` are Pi's own code too, checked against Pi's package in CI (D73).
- Fixed pi-ai's pure utilities throwing "not available" inside extensions. `parseJsonWithRepair`, `repairJson`, `parseStreamingJson`, `calculateCost`, the thinking-level and overflow helpers, retry classification, diagnostics, `EventStream` and the assistant-message streams and frames, `validateToolArguments`, `validateToolCall`, the faux message builders, model and credential registries, `StringEnum` and `uuidv7` are now Pi 0.87.1's own code, with the `partial-json` release Pi uses. The stale `registerSessionResourceCleanup` and `cleanupSessionResources` shim values are removed because Pi 0.87.1 does not export them at runtime.
- Fixed extension messages whose content is a list of blocks, such as `pi-web-access`'s `/websearch` results, showing as `[map[text:... type:text]]`. They now show their text blocks joined by newlines, rendered as Markdown and wrapped inside the message box, as in Pi; a result line wider than the terminal had ended pig with a render-overflow crash.
- Fixed print mode hanging, and ignoring SIGTERM, while waiting for piped stdin that is never closed. PiG now exits 143 on SIGTERM there as Pi does. A signal no longer lets later prompts start, and extension handlers and commands it interrupts are no longer reported as failing with "context canceled", in any mode.
- Fixed the `grep` tool's `path` parameter description differing from Pi's.
- Fixed an extension overlay that covers the editor, such as `pi-rtk-optimizer`'s `/rtk` panel, drawing the editor cursor as an inverse bar reaching the overlay's left edge. The editor now pads each row to its full width, as Pi's does, so the cursor stays one cell wide under an overlay.
- Fixed the editor cursor at the end of a line that fills the editor's width highlighting the line's last character. As in Pi, the cursor is now a highlighted space after it, in the column the editor reserves for the cursor, or in the right padding when the editor has padding.
- Fixed fast typing such as `/compact` painting each prefix with a stale autocomplete popup, which scrolled the main screen further than Pi. PiG now handles every key of one terminal read before painting, as Pi does.
- Fixed `pi-subagents` and other extensions that import `@earendil-works/pi-agent-core` failing to load with `ERR_MODULE_NOT_FOUND`. PiG now serves Pi 0.87.1's own pi-agent-core to extensions, as Pi does, and an extension's `Agent` streams through PiG's providers (D73).
- Fixed `pi.events.on()` returning the event emitter instead of an unsubscribe function, which broke `pi-goal-x` ("this.unsubscribe is not a function") and `/task-models` in `@henryqw/pi-task-models` ("off is not a function"). `pi.events` is now Pi's event bus, shared by the extensions in PiG's Node extension process (D77), and `pi.on()` returns an unsubscribe function, as in Pi.
- Fixed `pi-cc-extensions` failing to load because `pi.registerMarkdownTransformer` was missing. Extension Markdown transformers now rewrite user messages and assistant text and thinking in the transcript, after the built-in Mermaid transformer, as in Pi, and user messages now get Mermaid diagrams too. The complete transform chain finishes off the render loop before new content is painted, without an untransformed frame. The Go, Rust and Python SDKs can register one (`MarkdownTransformer`; `markdown_transformer`).
- Fixed `gentle-pi` failing to load because `createReadToolDefinition` threw. Pi's `create<Tool>ToolDefinition` factories for read, bash, edit, write, grep, find and ls now return Pi's definitions, whose `execute` runs the tool in the extension process at `ctx.cwd`, and a tool registered from one draws PiG's built-in card for that tool. `create<Tool>Tool` returns Pi's agent tool shape, and bash's prompt guideline and the tools' `constrainedSampling` now match Pi's.
- Fixed extensions that fail to load, such as `pi-cc-extensions`, `@henryqw/pi-subagent`, `@gotgenes/pi-permission-system`, `gentle-pi` and `confluence-cli`, being reported with Node's "(Use `node --trace-warnings ...`)" line instead of the error, which was only in a log under `/tmp`. PiG now reports the error as Pi does, "Failed to load extension: <message>", and discards Node's process warnings, as Pi does.
- Fixed extension `console.log` and `console.error` output never showing, such as `@raindrop-ai/pi-agent`'s startup lines and `pi-web-access`'s version warning. As in Pi, it reaches the terminal: stdout in interactive mode, and stderr in print, JSON and RPC mode.

- Fixed TypeScript and JavaScript extensions loading differently from Pi, which failed `@gotgenes/pi-permission-system` (its `#src/...` imports name no file extension) and `confluence-cli` (it reads `__dirname`). PiG now loads extensions with the same jiti release and options Pi 0.87.1 uses, so package.json `imports` and `exports` maps, extensionless and `.js`-for-`.ts` specifiers, index files, JSON imports, CommonJS interop, `import.meta` and `require`, `__dirname` and `__filename` behave as in Pi, and load errors carry jiti's messages, as Pi's do.
- Fixed `@henryqw/pi-subagent` failing to load with "requires active Pi". An extension now sees Pi's process identity: `process.title` is `pi`, process warnings are silenced, and `process.argv` holds PiG's own arguments after an entry named like Pi's CLI package, so an extension that starts the harness again with `process.execPath process.argv[1]` runs PiG, as `@henryqw/pi-subagent`'s subagents and `pi-subagents` do under Pi. `getPackageDir()` returns the directory of the PiG binary, as Pi's does for a compiled Pi.
- Fixed above-editor widgets appearing flush left with a blank line below them. PiG now puts the blank line before the widgets and renders string lists with Pi's padding, wrapping, and truncation, including after terminal resize.
- Fixed `pi-powerline-footer`'s welcome overlay never appearing. A UI call an extension makes from a timer or other code its event handler left running, after the handler returned, is no longer cancelled with that handler's request, as in Pi. A custom footer that renders no lines now replaces PiG's built-in footer, as in Pi, instead of leaving it drawn.
- Fixed TypeScript and JavaScript extensions that share one Node process still running the extension runtime of the PiG version that first cached them after an upgrade, which would have kept the fixes above from reaching them. Their cache key now covers the runtime's content.

- Fixed custom and built-in footers appearing together during reload. Footer ownership now changes before publishing the replacement frame, whose invalidation can paint immediately.
- Fixed interleaved Node and native extensions splitting the Node event bus. Node factories now share one process and load one at a time under host admission, preserving configured factory order.
- Fixed a Node extension crash isolating every cooperating extension. Recovery now quarantines an attributable culprit and restarts healthy members together; unknown failures get one group restart, then bisection on recurrence. Interrupted tools and callbacks are not replayed.
- Fixed print, JSON and RPC extension console messages being able to arrive out of order through separate stdout/stderr pipes. Headless Node extensions now use one ordered pipe, as Pi's stdout redirection does.
- Fixed RPC startup printing internal tool inventories to stderr. Extension-authored console output and actionable errors remain visible, but successful host setup is quiet as in Pi.
- Fixed `{ ...ctx }` losing request-context fields such as `ui`, which crashed `pi-cc-extensions`' compact-thinking setup. Request contexts now expose enumerable own fields that read the current host state, as Pi's do.
- Fixed `pi.on()` unsubscribe suppressing an already-selected handler, and handlers registered after loading never reaching the host. Subscription changes now update future dispatch snapshots while admitted callbacks retain their identity.
- Fixed `generateDiffString` and `generateUnifiedPatch` throwing inside extensions such as `gentle-pi`. Both now use Pi 0.87.1's own diff module and pinned `diff` dependency, including context lines, line numbers, line endings and missing final newlines.
- Fixed extension Markdown rendering retaining every pending streaming prefix and starting an unowned request for each one. Each message segment now owns a cancellable worker with one replaceable pending generation; repeated identical messages do not reuse another message's transform result.

### Added

- Added a release gate that rejects pending or partial upstream tests on hot paths and prevents the ported-test total from falling below its committed baseline. The checked-in test batches identify the remaining work; an accounted-for gap no longer counts as release readiness.

- Publish several Piglets from one GitHub repository with `pig piglet publish --tag-prefix <name>/`, then pull each signed release with `github:owner/repo/<name>@version`.
- Update an installed GitHub Piglet Binary with `pig piglet update <name>`. Discovery stays within its signed tag namespace and retains signer pinning, revocation, and rollback checks.
- Add a Piglet from a Git repository subdirectory at a pinned commit. Declared local Resources and prompts are copied into a separate, digest-checked closure for that Piglet.

- Added `docs/extension-sdk-surface.md`, generated by `go run ./test/parity/cmd/sdksurface`: extension API rows from Pi 0.87.1's declarations and package-export and class-member rows from its compiler-derived published inventory. The Node runtime probe checks live objects, inherited symbols and conditional error fields; Go, Rust and Python symbols and host wire fields are checked from source. `make sdk-surface-drift` rejects stale output and missing cells without reviewed reasons. Symbol presence and vendored code origin do not substitute for behavioral conformance.
- Added the Pi extension surfaces the Go, Rust and Python SDKs lacked: event-name constants; full tool definitions and tool-result usage; custom message details and content blocks; execution timeout, working directory and killed status; dialog timeout; UI availability and host-backed theme methods; compaction completion/error callbacks; Go overlay layout options; and Rust/Python footer and header lines.

### Fixed

- Preserve pending Pico3 tasks when a task kind is replaced just after the harness resumes. Only tasks with unknown kinds at the time of `Resume` are orphaned, as in Pi.
- Fix the first request of a new session going to the wrong endpoint for providers without a dedicated builder, such as OpenCode, OpenCode Go, DeepSeek and Z.ai. A stored key was sent to OpenAI's default URL, and Anthropic-style models used the wrong client. Thanks @ShoichiTect (#59).

## [0.2.0] - 2026-09-25

First public release of PiG, a Go port of Pi 0.87.1. `pig --version` prints `0.2.0+0.87.1`. Release archives: macOS and Linux (amd64, arm64) and Windows (amd64, arm64, preview), with one `SHA256SUMS`.

### Extensions

- Run Pi's TypeScript and JavaScript extensions together in one Node process, as Pi does. Compatible Go, Rust and Python extensions share one process per language; `isolation: strict` gives an extension its own process.
- `/reload` starts a fresh instance of every extension, and extensions load in the order they are configured (D70).
- A crashed extension is reported and restarted with its handlers live; the session and other extensions keep running.
- Extension host calls apply in send order, outbound frames are never dropped, and subprocess tools get a live cancel signal and `onUpdate`.
- Terminal-input handlers, autocomplete providers, custom footers and status, OAuth dialogs and `exec` behave as in Pi in interactive, print, JSON and RPC modes.
- Add the plan-mode example extension and PowerShell tool event variants in every SDK.

### Terminal UI

- Detect terminal capabilities as Pi does (hyperlinks, inline images, true color, 256-color themes, Shift+Enter in Apple Terminal), with settings overrides.
- Port Pi 0.87.1's model-thinking settings submenu, model picker and selector layouts.
- Fix duplicated tool rows and choppy rendering while tools run; keep the working status through the tool lifecycle.
- Measure wrapped graphemes by visible cell width, and keep assistant content order and terminal state on redraw.
- Handle over-width lines as Pi does.

### Piglets

- `pig piglet add npm:<package>` and `git:<repo>` install Piglet sources.
- `pig piglet publish --to github` publishes signed Piglet Binaries to GitHub Releases (dry run unless `--yes`), and `pig piglet pull` installs one, checked against its signed release index.
- `pig piglet build --sign-key` signs a Piglet Binary, and pig checks the signature against your trust policy at startup; `pig piglet keygen`, `verify` and `trust` manage keys. Windows builds produce `.exe` Binaries and a `cmd.exe` launcher.

### Models, sessions and modes

- Place Anthropic cache markers on completion text parts, and keep empty text parts for Responses and Google requests.
- Keep Codex manual code entry working when port 1455 is busy.
- RPC emits `queue_update` before a queued prompt's response and keeps final blank JSONL records.
- Tree-summary navigation stays cancellable, and `pig` prints one resume hint after terminal restore.

### Platforms

- Windows (preview): Node extensions connect over a named pipe, Python extensions over AF_UNIX, owner-only files are enforced with DACLs (D68), and the external editor, `!command` config values and package managers launch as Pi does on Windows.
- WSL: clipboard image paste, trying xclip's advertised image type first.
- Install telemetry reports to PiG's own endpoint; `PI_OFFLINE=1` or `enableInstallTelemetry: false` turns it off.

### Known issues

- Windows support is a preview: tested natively, with less real-world use than macOS and Linux.
- The Package catalog listing is not in this release; install packages from a known npm or git source.

### Also in this release

- Prepare the independent PiG source repository.
- Pin upstream Pi 0.87.1 (`f07218c4d4bbc12bef056a7058c3dd49dfe41abe`) as the behavior oracle and regenerate the model catalogs from the published 0.87.1 package: 1,495 text models, 1,015 of them with input limits, and 55 image models.
- Add Pi's model input-limit metadata types (`ModelInputLimits`, `ModelImageInputLimits`, `ModelImageResizeOptions`) to generated and runtime models.
- Match Pi's default model for each provider, including Grok 4.7 for xAI.
- Report invalid `--mode`, a missing `--mode` or `--name` value, and unknown short options as errors that exit with status 1, and invalid thinking levels as warnings, with Pi's wording.
- Omit empty text parts from OpenAI-compatible user messages so image-only messages stay valid.
- Detect GIF images by their `GIF87a` or `GIF89a` signature, so text files that start with "GIF" stay text.
- Fix Node extensions losing model stream events after the host call returned, and Go SDK extensions hanging on the same race.
- Stop repainting the whole transcript when the terminal sends SIGWINCH without a size change, such as tmux window switches or focus changes. The view no longer snaps to the top, and resize storms repaint once per real size change.
- Run every tool of a parallel batch at once, as Pi does. PiG previously capped concurrency at the CPU count, which serialized batches on one- or two-vCPU machines.
- Add `/angry-pigs` to PiG Standard: a full-screen slingshot game built as an ordinary Go extension.
- Record the core committee in docs/project/MAINTAINERS.md and docs/project/GOVERNANCE.md.
- Add `pig setup`, which reports the toolchains used by extension languages and Piglet builds. `pig setup go` installs a Go toolchain from go.dev, verified against its published SHA-256, which PiG uses when `go` is not on PATH. `pig setup container` shows how to install Docker or Podman on the current system.
- Add `pig verify`, which starts from the SHA-256 of the bytes on disk: it checks files against a `SHA256SUMS` file, checks GitHub build provenance through `gh attestation verify`, checks installed npm Package signatures and git Package commits, and validates Piglet files, Packages, and extension directories. The design follows `pi verify` in [dimetron/pi-go](https://github.com/dimetron/pi-go).
- Print Pi's own `--help` text, rendered with PiG's identity by `automation/gen/gen-help.sh`, followed by PiG's commands and options, and support Pi's `--use-theme` and `--tui-mode` options.
- Send `store: false` to OpenAI-compatible providers, as Pi does. PiG sent `store: true` to OpenAI, which asks OpenAI to retain the conversation.
- Match Pi's request fields: `max_completion_tokens` from the model's output limit clamped to the remaining context, `max_tokens` only for the providers Pi lists, and no `strict` field unless a model enables strict mode. Local servers such as Ollama now receive `max_completion_tokens`; set `compat.maxTokensField` in `models.json` to override.
- Build the system prompt in Pi's section layout with Pi's tool descriptions and tool order, which shrinks the first request by about 2.9 KB. PiG-specific agent guidance moved from the prompt into the local docs bundle, which now also carries the themes, prompt templates, TUI, SDK, and custom provider pages the prompt refers to.
- Add `make evals` and `pigeval`, which measure PiG, Pi, oh-my-pi, Codex, Claude Code, and opencode against one deterministic local model, capture and diff their request bodies, run fixed coding tasks with a real model (Copilot CLI too, which cannot use a custom model endpoint), and check latency, memory, and request-size budgets.
- Add `PIG_PROFILE` (cpu, heap, allocs, block, mutex, goroutine, trace) with `make profile` and `make pgo`. With the variable unset, pig does one environment lookup.
- Expose PI_SESSION_ID, PI_SESSION_FILE, PI_PROVIDER, PI_MODEL, and PI_REASONING_LEVEL to bash tool commands, and remove inherited copies, as Pi does. The bash prompt guideline says so.
- Add `/thinking [level]`, which sets the thinking level or opens the selector, as in Pi.
- Add `make evals-mutate`, which generates seeded bug-fix tasks from real Go files after oh-my-pi's edit benchmark, and report cost per task, cost per passing task, turns, polling calls, and context tokens for every live run.
- Add `make slop` and `make slop-check`, which measure erosion and clone verbosity (SlopCodeBench metrics) over hand-written Go and hold them at the launch baseline.
- Add `make setup` and `make doctor`, which install and check every development prerequisite, and move all scripts into `automation/` with a grouped `make help`.
- Add a devcontainer for GitHub Codespaces and browser development.
- Harden workflows: no persisted checkout credentials in the docs job, and step outputs reach shell scripts through environment variables.
- Serve Node extensions the TypeBox 1.3.27 that Pi ships for `typebox`, `typebox/value`, `typebox/compile`, and `@sinclair/typebox*`, so pi-mcp-adapter loads.
- Serve Node extensions Pi's own key parsing and matching (`parseKey`, `matchesKey`, `Key`, and related helpers) from the pinned pi-tui release, so pi-doom loads and extension key handling matches Pi on Kitty-protocol terminals.
- Add a knowledge graph of PiG entities, relations, locations, and inspect commands (site page, JSON-LD, Mermaid, and the local agent docs), and an install and troubleshooting guide covering macOS quarantine, Windows SmartScreen, Linux permissions, proxies and certificates, toolchains, and display debugging.

## [0.0.0] - Development baseline, not published

This entry gives the Pi-compatible `/changelog` command a versioned development baseline. It is not a release tag or a claim that PiG has published artifacts.
