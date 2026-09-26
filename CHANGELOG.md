<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Changelog

All notable public changes to PiG will be recorded in this file.

## [Unreleased]

### Fixed

- Fixed extension tools that define `renderCall`, `renderResult` or `renderShell` being drawn with PiG's generic tool header, as it appeared above `pi-mcp-adapter`'s compact tool card. PiG now draws them as Pi 0.87.1 does: the call and result renderers share one state per tool card and receive their last component, `context.invalidate()` runs them again, the result renderer sees partial results while the tool streams, a result no longer expands the card, `renderShell: "self"` draws the tool's own framing, and a renderer that throws shows Pi's fallback. Node extensions' renderers run in the extension process. The Go, Rust and Python SDKs gain the same renderers (`SetToolRenderers`; `render_tool_call`, `render_tool_result` and `tool_render_shell`; `tool_renderers`). An override of a built-in tool that defines only one of the two renderers keeps the built-in card (D75).
- Fixed the startup resource list putting each section's heading and list on one line and labelling extensions by their entry file (`dist`, `index`). Each section now shows its heading with the list below it, package extensions are labelled by package and entry (`pi-lens:dist`, `@upstash/context7-pi:context7.ts`), other extensions by their shortest unique path, and Ctrl+O expands every section into Pi's user, project and path groups. The list also shows `[Themes]` and the system prompt files in `[Context]`, stays above the transcript when the chat is rebuilt, and is rebuilt after `/reload`, as in Pi. Ctrl+O shows Pi's "Tool output: expanded" and "Tool output: collapsed" status.
- Fixed `/q <message>` from `pi-msg-queue` showing the queued user message above "Follow-up message sent.". PiG now shows a prompt's user message when the agent starts the turn, as Pi does, so a notification an extension sends right after `pi.sendUserMessage()`, and output from `before_agent_start` handlers, appears before it, and a prompt that is rejected before the turn starts is not shown.
- Fixed a `pi.extensions` entry that names a directory without an index failing with "resolves to N entrypoints" or "no extension entry file". As in Pi, each `.ts` and `.js` file and each subdirectory entry in the directory loads as its own extension, a directory with no entries loads nothing, and a `-e` directory with a `pi` manifest loads each entry its manifest yields.

## [0.2.1] - 2026-09-26

Hotfix for Pi extensions from npm that failed to load or crashed in 0.2.0, reported on Reddit by rokrdev and WorriedAcanthisitta3. `pig --version` prints `0.2.1+0.87.1`.

### Fixed

- Fixed `pi-mcp-adapter`'s `/mcp` panel crashing the extension process, and with it every extension sharing that process. PiG's `Container` had no `clear()`, and `ctx.ui.custom` factories received an empty object instead of a keybindings manager, so the first arrow key or Enter threw. Factories now get pi-tui's `KeybindingsManager` with Pi's default bindings, and the component they return is focused, as in Pi, so an `Input` or `Editor` shows its cursor (reported by rokrdev on Reddit).
- Fixed Node extensions that export their default with `export { name as default }`, as bundlers emit, or with `module.exports`, being rejected with "has no default extension export". PiG now imports the module and checks its default export at load time, as Pi does, and reports a module without one with Pi's "does not export a valid factory function" message (reported by rokrdev on Reddit).
- Fixed Node extensions failing to load when they import any value from `@earendil-works/pi-coding-agent`, `@earendil-works/pi-tui` or `@earendil-works/pi-ai` that PiG's modules did not provide, such as `isToolCallEventType`. PiG now provides every export of Pi 0.87.1's packages, checked in CI against Pi's own `index.ts`. Helpers that run unchanged outside Pi (session context, frontmatter parsing with the same YAML library, transcript and message conversion) are Pi's own code; values that only exist inside Pi's own process throw a clear error naming them when called (D73; reported by rokrdev on Reddit).
- Fixed PiG refusing to start with packages whose `pi` manifest declares a folder of skills, such as `pi-lens`, `@upstash/context7-pi` and `@dietrichgebert/ponytail`. A declared skills entry may be a folder of skill folders, as in Pi, and a declared resource path that matches nothing is skipped at startup, as Pi skips it, instead of stopping PiG. A package with a `pi` manifest loads only what it declares, so ponytail's `hooks/` folder of other agents' hook configs is no longer read as PiG hooks (reported by WorriedAcanthisitta3 on Reddit).
- Fixed `pi-hermes-memory` failing to load because `@earendil-works/pi-ai/compat` lacked `completeSimple`, `getModel` and other exports. The pi-ai root and `/compat` now serve Pi 0.87.1's own compat module: `getModel`, `getModels` and `getProviders` read Pi's model catalog, `getEnvApiKey` follows Pi's environment rules, and `stream`, `complete`, `streamSimple` and `completeSimple` use Pi's dispatch, with the request itself running on PiG's port of the same provider. An explicit `apiKey` wins over configured credentials, a missing key fails with Pi's "No API key for provider" error, and aborting the signal cancels the request (D74; reported by WorriedAcanthisitta3 on Reddit).
- Fixed `pi-lens` failing on every turn with "Cannot read properties of undefined (reading 'runtime')" and its "LSP Inactive" footer status not showing. Methods of `ctx` and `ctx.ui` now keep working when an extension stores one in a variable and calls it later, as they do in Pi (reported by WorriedAcanthisitta3 on Reddit).
- Fixed an extension handler error printing PiG's own Go stack trace under the error line. PiG now shows the stack of the error the extension threw, as Pi does, and none for a plain returned error.
- Fixed `pi.getActiveTools()` listing tools an extension had turned off in interactive mode, and returning an empty list in print and JSON mode, where `pi.setActiveTools()` did nothing. `pi-lens` turns its on-demand tools off at session start, and they stayed on under PiG. Both calls now read and set the session's active tools in every mode, as in Pi, and RPC mode no longer prints a line to stderr for each `setActiveTools` call.
- Fixed offline mode not reaching extensions when it was turned on with `PIG_OFFLINE`. PiG now sets `PI_OFFLINE=1` and `PI_SKIP_VERSION_CHECK=1` for `--offline`, `PIG_OFFLINE` and `PI_OFFLINE`, as Pi does, so `pi-auto-update` skips its `pi update` run in offline mode instead of reporting "Pi auto-update failed" at startup.
- Fixed `pi.getAllTools()` and `pi.getCommands()` in Node extensions returning tool and command names instead of Pi's `ToolInfo` and `SlashCommandInfo` objects, and leaving out inactive built-in tools such as `grep`, `find`, `ls` and `powershell`. `pi-mcp-adapter` therefore never recognized a native tool the model asked it to call. Both lists now match Pi 0.87.1's in every mode, including `sourceInfo` for tools and commands from `-e` extensions and for `--prompt-template` and `--skill` resources, which RPC `get_commands` now also reports with Pi's `temporary` scope, and `getCommands()` also lists prompt templates and skills; print, JSON and RPC mode had answered it with an empty list. The Go, Rust and Python SDKs return the same fields; the Go SDK's `ToolInfo.Source` stays populated and is deprecated in favor of `SourceInfo`.
- Fixed `pig -e ext.ts -p "/command"` and `--mode json "/command"` sending an extension command to the model instead of running it. Print and JSON mode now route prompts as Pi does: an extension command runs, and prompt templates and `/skill:` commands expand.
- Fixed pi-tui's `Input`, `Editor`, `SelectList`, `KeybindingsManager` and `Markdown` being simplified stand-ins inside extensions. They are now Pi 0.87.1's own code, so extension prompts get cursor movement, word and line editing, kill and yank, undo, paste handling, horizontal scrolling, multi-line editing with history and autocomplete, and filterable, scrolling select lists, and `Markdown` renders through the same `marked` release, with links shown as the host terminal supports them. The other pi-tui components extensions build panels from (`Box`, `Container`, `Text`, `Spacer`, `SettingsList`, `HStack`, `VStack`, loaders), fuzzy matching, `CombinedAutocompleteProvider` and `StdinBuffer` are Pi's own code too, checked against Pi's package in CI (D73).
- Fixed pi-ai's pure utilities throwing "not available" inside extensions. `parseJsonWithRepair`, `repairJson`, `parseStreamingJson`, `calculateCost`, the thinking-level and overflow helpers, retry classification, diagnostics, `EventStream` and the assistant-message streams and frames, `validateToolArguments`, `validateToolCall`, the faux message builders, model and credential registries, `StringEnum` and `uuidv7` are now Pi 0.87.1's own code, with the `partial-json` release Pi uses. Only `registerSessionResourceCleanup` and `cleanupSessionResources`, whose cleanups Pi's agent session runs, remain stand-ins (D73).
- Fixed extension messages whose content is a list of blocks, such as `pi-web-access`'s `/websearch` results, showing as `[map[text:... type:text]]`. They now show their text blocks joined by newlines, rendered as Markdown and wrapped inside the message box, as in Pi; a result line wider than the terminal had ended pig with a render-overflow crash.
- Fixed print mode hanging, and ignoring SIGTERM, while waiting for piped stdin that is never closed. PiG now exits 143 on SIGTERM there as Pi does. A signal no longer lets later prompts start, and extension handlers and commands it interrupts are no longer reported as failing with "context canceled", in any mode.
- Fixed the `grep` tool's `path` parameter description differing from Pi's.

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
- Record the core committee in MAINTAINERS.md and GOVERNANCE.md.
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
