<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT

DRAFT ONLY. Not release approval. These notes cover changes since PiG 0.3.1, which already shipped the npm self-update fix, `--exclude-tools` in RPC, the identity fix ("PiG never identifies as Pi"), the #103 and #104 fixes, the `/model` selector, export and lock fixes. Drafted on the integration line after it merged public main at the 0.3.1 release, the upstream 0.99.2 pin move, `port-992-mcp`, `port-992-core` and `fix-992-mcp-session`. Every claim was checked in that tree; the evidence is in docs/plan/release-040-dryrun.md. Recheck each claim against the final release commit before publication.

Open items that change this text before publication:
1. PUBLIC MAIN: PR #109 (`5e78a4e99`, #105 and #108) is merged in this tree and credited below. Merge public main again before the content PR if it moves.
2. #106: PR #110 (`2ab6f9fcb`) is merged in this tree and credited below.
3. DOCS. MCP, codemode and virtual-model pages ship on the site (`docs/site/docs/mcp.md`, `codemode.md`, `virtual-models.md`) and in the binary (`pig docs show mcp|codemode|virtual-models`); a test now checks that every page the system prompt cites is in the bundle. The Highlights may link them.
4. CHANGELOG. `changelog.d` is folded into `## [Unreleased]` of `CHANGELOG.md` (legacy fragments included, the two 0.3.1 fragments deleted unfolded), with new entries for codemode, MCP in sessions, ChatGPT sign-in and the system theme. The release commit's `--move-unreleased` moves them under `[0.4.0]`.
5. The Verification paragraph at the end is a placeholder for the release evidence.
-->

# PiG 0.4.0

PiG 0.4.0 follows upstream 0.99.2 in Go. It moves the Pi target from 0.87.1 to 0.99.2 and brings the Pi features of that range into PiG: codemode and tool search, a native Model Context Protocol client that runs in every mode, typed and virtual models, the extension API for tools and models in every SDK, Sign in with ChatGPT, Anthropic workload identity federation, and the system theme. It also carries MCP and Windows fixes and extension API fixes found while porting.

Pi is the reference implementation: [earendil-works/pi](https://github.com/earendil-works/pi). PiG is a separate Go implementation of it. The differences a user can see are recorded, numbered, in [the divergence ledger](https://github.com/MichaelKinsy/PiG/blob/main/docs/parity/DIVERGENCES.md).

Thanks to everyone credited in the 0.3.1 and 0.3.0 notes. PiG 0.3.1 carried the reports and fixes of @ShoichiTect, @baggiiiie, @SamarthBoranna, @nguyen-tran-100x, @sj0n and @nickdeighton; this release builds on them. Thanks also to @jkerdreux-imt for the fullscreen keyboard (#105) and theme color (#108) fixes, and to @nicholas-recht for the terminal height fix after `/reload` (#115, #116), and to @worldofgeese for the detailed Windows cell-publication report ([#106](https://github.com/MichaelKinsy/PiG/issues/106)), fixed in PR #110.

## Updating

- **Installed with the script, or a standalone download, on 0.3.0 or 0.3.1:** run `pig update`. It checks the signed `update.json`, downloads the archive for your platform and verifies its SHA-256 before it replaces the executable. The first start after the update rebuilds source extensions, so it is slower.
- **Installed with npm, on 0.3.1:** run `pig update` or `npm update -g @pi-in-go/pig`. On 0.3.0, do not use `pig update` for this one step: 0.3.0 reads the wrong package name from the update manifest and runs `npm install -g pig@<version>`, which installs an unrelated package. Run `npm update -g @pi-in-go/pig`.
- **On 0.2.0:** run the installer again. PiG 0.2.0 cannot update a script installation itself. On macOS and Linux: `curl -fsSL https://pi-in-go.dev/install.sh | sh`. On Windows: `irm https://pi-in-go.dev/install.ps1 | iex`.
- **On Windows with a standalone `pig.exe`:** `pig update` does not replace a running `pig.exe` in place (D39). Run the PowerShell installer again.
- **Installed with Go:** run `go install github.com/MichaelKinsy/PiG/cmd/pig@v0.4.0` again.
- Stop running PiG processes before you update.

Extension and SDK authors: this release breaks some Go, Rust and Python SDK and Go library signatures to match upstream 0.99.2. See [Breaking changes](#breaking-changes-for-extension-and-sdk-authors).

## Highlights

- **Codemode and tool search.** The `codemode` tool runs model-written JavaScript in a QuickJS sandbox that calls PiG's tools, and `tool_search` finds tools that were not declared to the model. Both are built-in extensions written in Go; the JavaScript engine is QuickJS compiled to WebAssembly and run by wazero, so codemode needs no Node. Enable codemode with `defaultTools` or `--tools`, for example `"defaultTools": ["+codemode"]`, and configure it with the `codemode.mode` and `codemode.inlineBudget` settings.
- **Model Context Protocol client, in every mode.** PiG has a native MCP client in Go with stdio and streamable HTTP transports and OAuth (discovery, PKCE, dynamic client registration, refresh). The built-in `mcp` extension loads `mcp.json` (global, or `.pig/mcp.json` in a trusted project) in print, JSON, RPC and interactive mode, registers `mcp__<server>__<tool>` tools plus resource tools, adds the `/mcp` command, and keeps OAuth credentials in `mcp-auth.json`. Extensions can register servers too. `pig mcp add`, `remove`, `list`, `login` and `logout` manage servers without starting a session. Servers with the default `codemode` exposure connect in the background and do not delay the first prompt; they are waited for when a script names them, searches tools, or `tool_search` runs. A short `mcp_servers` system-prompt section lists each server with its description, updated at the start of each prompt. Disable the extension with `--no-extensions` or `-builtin:mcp`.
- **More MCP configuration options.** `pig mcp add` takes `--description` (what the server offers, for the system prompt and tool-search ranking), `--oauth-client-name` (the client name sent during OAuth registration, for servers that accept only known clients), and an HTTP server can use `"auth": {"provider": "<provider>"}` to send a provider's current `/login` token as the bearer token (global `mcp.json` and extensions only; https except on loopback).
- **Codemode helpers.** Scripts read a namespace's instructions with `describeNamespace()`; `searchTools()` and `describeNamespace()` accept `mcp__dev-radius`, `mcp__dev_radius`, `dev-radius` and `dev_radius`. The `codemode` and `tool_search` descriptions no longer list deferred tools, tool counts or servers, so they do not change when servers connect. `codemode-deferred` is an alias for `codemode`.
- **Anthropic workload identity federation.** PiG authenticates to Anthropic from `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID` and `ANTHROPIC_IDENTITY_TOKEN_FILE`. The variables alone count as configured authentication for `--list-models`, default model selection and `-p`.
- **`/reload` picks up new default tools.** Tools newly added to `defaultTools` are enabled on `/reload`. Tools removed from the setting stay enabled, tools you turned off during the session stay off unless newly added, and `--tools`, `--no-tools` and `--no-builtin-tools` still override the setting.
- **Typed models, image generation and classifiers.** `coding.ModelRuntime` lists and resolves chat, image and classifier models (`GetModelsOfType`, `GetModelOfType`, `GetAllModels`, `GetAvailableOfType`), generates images with the provider's key and headers (`GenerateImages`), and keeps OpenRouter image models apart from chat models. llama.cpp lists a classifier next to each chat model. Extensions can register image and classifier models with their provider.
- **Virtual models.** An extension registers a virtual model and chooses the physical model and thinking level for each request. The footer shows the routed model, and retries, compaction summaries and restored selections follow the route.
- **The extension API for tools, MCP and models, in every SDK.** Node, Go, Python and Rust extensions can register MCP servers and virtual models, read settings (`getSettings`), declare tools with `exposure` (`direct`, `codemode`, `deferred`, `hidden`), `namespace`, `annotations`, `outputSchema`, `defaultActive` and `prepareLoadout`, return `structuredContent`, call other tools with `ctx.tools` and `ctx.executeTool()` (nested calls are recorded on the tool result and emit events with `parentToolCallId`), observe `provider_stream_event`, and read `ctx.signal`, the signal of the run in progress (undefined while idle), as Pi does. Go and Python extensions also get Pi's `pi.events` bus. The TypeScript declarations are upstream 0.99.2's.
- **Sign in with ChatGPT.** `/login openai` uses a ChatGPT subscription with the OpenAI provider. GPT-6.1 Sol is in the catalog and is the default `openai-codex` model; Fireworks, Together and OpenCode Go default to Kimi K3.
- **System theme and new header.** PiG's colors now come from your terminal's palette by default (the `system` theme) and follow a light/dark switch. Theme files accept `#rgb`, `oklch()` and `okhsl()` colors. The startup header shows the Pi logo with the version.
- **Built-in extensions in `pig config`.** `pig config` lists built-in extensions in a "Built-in" group. Enable or disable one with `+builtin:<name>` or `-builtin:<name>` in the `extensions` setting, load one for a run with `-e builtin:<name>`, and disable all of them with `--no-extensions`.
- **Sessions are saved from the first prompt.** The session file is created when the first user message is added, so a crash before the first reply no longer loses the prompt. `/clone` and `/fork` before the session has a file report `This session has not been saved yet. Send a message before cloning or forking it.`
- **More settings.** `defaultTools` accepts `+name` and `-name`; `fullscreenWheelScrollLines` (`"auto"` or 1 to 100); `codemode`; and a random installation `deviceId` in the global settings, which bug reports leave out.

## Fixed

- An extension keeps the terminal height after `/reload`: a packed Go extension no longer receives a height of 0 until the next resize. The host delivers width and height changes to each extension that has not seen the value, so a resize during `/reload`, a crash restart of one extension, and a packed Node extension whose ready message waits for every factory no longer leave an extension on an old height ([#115](https://github.com/MichaelKinsy/PiG/pull/115), [#116](https://github.com/MichaelKinsy/PiG/pull/116)).
- On Windows, publishing a Node extension cell (and any other cached runtime cell) is retried with backoff for up to 10 seconds when the final directory rename fails with `Access is denied`, a sharing violation or a lock violation, instead of stopping extension loading. Anti-virus scanners and indexers hold freshly written files open for a short time. PiG adopts the cell if another PiG process publishes it meanwhile and reports any other error unchanged ([#106](https://github.com/MichaelKinsy/PiG/issues/106), [#110](https://github.com/MichaelKinsy/PiG/pull/110)).
- Fullscreen (alternate-screen) mode keeps the Kitty keyboard-protocol flags on the active screen, so `Shift+Enter` and `ctrl+digit` extension shortcuts work in it, and PiG no longer leaves those flags set in the shell after leaving fullscreen ([#105](https://github.com/MichaelKinsy/PiG/pull/105)).
- The bash execution block, the compaction and branch summary label, the editor thinking-level border and the settings list cursor take their colors from the active theme, so light and custom themes apply to them ([#108](https://github.com/MichaelKinsy/PiG/pull/108)).
### MCP, codemode and extension hosting

- MCP stdio servers started through npm `.cmd` shims on Windows run through `cmd.exe` with the command line cross-spawn builds, so `npx`-style servers start.
- Parallel MCP tool calls reach the server in the order the model issued them.
- Codemode `image()` rejects malformed base64 and unsupported image types instead of persisting an image block that makes every later provider request fail with HTTP 400.
- Collapsed `codemode` and MCP results show a preview limited to wrapped lines, so one long line such as minified JSON no longer fills the screen.
- `codemode.mode: "only"` no longer lists `read`, `bash`, `edit` and `write` in the system prompt's tool list.
- The `/mcp` sign-in URL is a terminal hyperlink with a `Cmd/Ctrl+click to open` line, so it is clickable when it wraps.
- MCP tool and namespace names replace `-` with `_` (`mcp__my-server__x` becomes `mcp__my_server__x`), colliding tools of one server get a hash suffix, and server names that differ only in `-` and `_` are rejected, so a script cannot call the wrong tool. See Breaking changes.
- Node extension processes receive every environment variable whose name contains `=`, as Node does.
- A deep `TMPDIR` no longer makes the extension host fail to start on macOS and Linux, where a Unix socket path has a length limit. PiG falls back to a per-user directory and uses it only when you own it privately.
- `tool_execution_update` events reach extensions, RPC and JSON consumers with Pi's `partialResult` shape (`content` is an array of blocks), and a tool's result members keep the order the tool wrote them, in every SDK.
- A Node extension's `ctx.executeTool(name, args, { signal: null })` runs the nested call with the calling tool's signal instead of failing with a `TypeError`.

### Providers

- Anthropic strict tool use sends a tool non-strict when its schema uses a keyword Anthropic rejects, such as `minimum`, `maxItems` or an unsupported `format`.
- Context overflow detection recognizes Z.AI CN `Prompt exceeds max length` errors.
- A `Retry-After` header with an unparseable value makes the provider retry with exponential backoff instead of immediately.
- New sessions no longer intermittently ignore the saved default model, or warn that no models are available, when it belongs to an extension-registered native provider with a stored credential.
- Prompt submission no longer slows down with session length: resolving a branch's model selection looks the catalog up once, not once per assistant message.
- An extension command registered without a string name or handler fails to load with an error instead of crashing PiG when you type `/`, in every SDK.

## Breaking changes for extension and SDK authors

PiG has no compatibility shim for these. Each follows the upstream 0.99.2 API named in the changelog.

- **Go tool progress API.** `agent.ToolUpdateCallback` is `func(partial agent.AgentToolResult)` and `agent.ToolExecutionUpdateEvent` carries `PartialResult agent.AgentToolResult` instead of `Content string` and `Details any`. Report progress as `update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "text"}}, Details: details})`.
- **Go `ai` model API.** `CreateProviderOptions.Models` and `FetchModels` use `[]ai.AnyModel` (convert with `ai.AnyModels(models)`). `ImagesModel` is `ImageModel`, `ImagesAPI` is `ImageAPI`, `ImagesCost` and the `ImagesModels` collection are removed (use `Models.GenerateImages`), and `CreateProvider` panics when given no implementation, as Pi's does.
- **Go extension host API.** `extension.EventBus` is `Emit(channel, data)` and `On(channel, handler)`, returning the unsubscribe function. `extension.API` gains `GetSettings`, the MCP and virtual-model registration methods and `OnProviderStreamEvent`, so implementations must add them. `ProviderConfigInput.Models` is `[]ai.AnyModel`. `Session.Steer` and `Session.FollowUp` return `(QueuedInputDisposition, error)`, and `PromptOptions.PreflightResult` is `func(PromptDisposition)`.
- **Settings.** `Settings.DefaultTools` holds the raw `defaultTools` list, which can contain `+name` and `-name`; read the selection through `SettingsManager.GetDefaultTools()`.
- **Built-in paths.** The `sourceInfo.path` of a built-in tool is `builtin:<name>` and the llama.cpp extension is `builtin:llama.cpp` with source `builtin`. Match on the new paths.
- **Rust SDK.** `ToolDefinition` and `ToolInfo` gain the upstream 0.99.2 tool fields; build a `ToolDefinition` with `ToolDefinition::new`. The `Provider` struct gains `generate_images` and `classify`.
- **Package manager.** `packagemanager.NpmCommandName` is replaced by `PackageManagerName`, which returns `(string, error)`; `GetGitDependencyInstallArgs` returns `([]string, error)`. An `npmCommand` whose package manager cannot be determined is an error.
- **MCP tool names.** `-` in an MCP server or tool name becomes `_`: `mcp__my-server__x` is now `mcp__my_server__x`. Update `--tools`, `defaultTools`, `-xt` and Piglet tool-scope entries that name an MCP tool with a hyphenated server, and any config with two server names that differ only in `-` and `_` (now rejected).
- **Session events.** `Session.Subscribe` no longer emits timing events; `agent.TimingEvent` remains as a deprecated alias.
- **Default models.** The defaults of `openai-codex`, `fireworks`, `together` and `opencode-go` change as listed in Highlights.

## Known issues

- RPC: a command sent immediately after `agent_end` can be answered before `agent_settled` is emitted, and an extension command's prompt response can be lost when stdin closes at once. Both are ordering races found under CPU load on a two-core machine (the first about once in 50 runs); they affect only clients that send a command on `agent_end` or close stdin right after a command.
- One hot-path Pi test file, `2860-replaced-session-context.test.ts`, is only partly ported: no extension SDK exposes `withSession`, so Node, Go, Python and Rust extensions cannot pass the callback its native cases use. It keeps its approved 0.3.x row; see `docs/parity/KNOWN-GAPS-0.3.x.md`.

## Verification

<!--
Fill in from the release evidence before publication. Do not state results that were not observed:
- Linux parity run on the release commit (make parity-fast and make parity): commit, date, result.
- Native macOS arm64 and Windows amd64 packaged-candidate smoke from release-candidate.yml (native-smoke), and the post-release `pig update` check on each.
- linux/arm64, darwin/amd64 and windows/arm64 are built and published but have no native smoke job; state that, or remove them from the supported list.
-->

PiG 0.4.0 was checked against upstream 0.99.2. The release evidence (checksums, SBOMs, provenance attestations) is attached to the GitHub release.
