# Settings

PiG reads settings from `~/.pig/agent/settings.json`. The file is optional. Every key is optional, and a key you leave out keeps its default. PiG uses the settings keys of Pi 1.3.0 ([Pi's settings reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/settings.md)) and adds the PiG-only keys marked below.

Set `PIG_HOME` to move the whole configuration root. Set `PIG_CODING_AGENT_DIR` to move only this directory.

```json
{
  "defaultProvider": "anthropic",
  "defaultModel": "claude-sonnet-4",
  "theme": "dark",
  "compaction": { "enabled": true }
}
```

Add `"$schema": "https://pi.dev/schemas/settings.schema.json"` to `settings.json` for editor validation and completion. PiG keeps the key when it writes the file. The schema is the one Pi 1.3.0 publishes. It does not list the PiG-only keys, but it accepts them.

Use `/settings` to change a setting from inside a session. PiG writes the file for you and applies the change without a restart.

[Settings by example](/docs/latest/settings-examples) shows the common keys at work, with the output to expect.

## Where PiG reads settings

PiG merges two files. A key set in the project file overrides the same key in the global file. Resource lists are combined. `defaultProjectTrust`, `httpProxy`, `cacheWarming`, `extensionsAutoUpgrade` and `extensionsTypescriptRuntime` are global settings: PiG ignores them in the project file.

| Scope | Path |
|---|---|
| global | `~/.pig/agent/settings.json` |
| project | `<project>/.pig/settings.json` |

PiG reads the project file only after you trust the project. An untrusted project contributes nothing, so cloning a repository cannot change your shell, your model or your packages before you agree to it. See [Security](/docs/latest/security).

If PiG is killed (`kill -9`, power loss), it can leave `settings.json.lock` next to the settings file. A lock younger than 10 seconds counts as held, so the next start waits about 0.2 seconds for each settings load, then warns `Invalid settings file ...: Lock file is already being held` and goes on with the defaults it has. After 10 seconds the lock is taken over. You can also delete the `settings.json.lock` directory. Pi behaves the same way.

A value of the wrong JSON type, such as `{"skills": 5}` or `{"enabledModels": [5]}`, is ignored, and PiG applies the other settings of the file. PiG prints one warning for each file that held such values, for example `Warning: Invalid settings file <path>: ignored settings of the wrong type: "skills" (expected an array of strings)`. Pi 1.3.0 exits with a `TypeError` for these values, or reads a string as a list of one-character patterns (D158).

An empty default column means PiG has no value for the key until you set one, or the value depends on what it detects.

## Models and providers

| Key | Type | Default | Meaning |
|---|---|---|---|
| `defaultProvider` | string |  | Provider for the saved default model. Set it with `defaultModel`. |
| `defaultModel` | string |  | Model ID within `defaultProvider`, without a provider prefix. A qualified ID such as `github-copilot/gpt-6-sol` is not supported here. |
| `defaultThinkingLevel` | string | `medium` | Thinking level applied to a new session: `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`. |
| `modelThinkingLevels` | object |  | Thinking level for a new session per model, keyed by `provider/modelId`. It overrides `defaultThinkingLevel`. |
| `enabledModels` | string[] | all available models | Model patterns used for startup selection and Ctrl+P model cycling. It uses the format of `--models`. |
| `thinkingBudgets` | object |  | Token budget per thinking level: `minimal`, `low`, `medium`, `high`. |
| `transport` | string | `auto` | Transport for providers that support more than one: `auto`, `sse`, `websocket`, or `websocket-cached`. |
| `retry` | object | see meaning | Agent retry policy: `enabled` (default `true`), `maxRetries` (default `3`), `baseDelayMs` (default `2000`) and `maxAgentDelayMs` (the cap on each retry delay, default `60000`). An explicit `0` is kept. See [provider retries](#provider-retries) for `retry.provider`. |
| `httpProxy` | string | unset | Global HTTP/HTTPS proxy URL. Sets only absent `HTTP_PROXY` and `HTTPS_PROXY` environment variables. Provider requests use CONNECT tunnels; `NO_PROXY` controls bypasses. Global setting only. |
| `websocketConnectTimeoutMs` | number | `15000` | WebSocket opening-handshake timeout in milliseconds. `0` or `"disabled"` turns it off. Explicit request options override the setting. |
| `httpIdleTimeoutMs` | number | `300000` | Idle timeout for provider requests, in milliseconds: the wait for response headers and for each part of the body. `0` or `"disabled"` turns it off. Keepalive comments and pings do not restart it, so a stream that sends only keepalives ends with an error after this timeout. Pi 1.3.0 waits for ever on such a stream (D131, [Pi #10761](https://github.com/earendil-works/pi/issues/10761)). |
| `cacheWarming` | string | `streaming` | `off`, `streaming`, or `idle`. Keeps the provider's prompt cache warm with periodic requests. Global setting only: PiG ignores it in a project file. See [cache warming](#cache-warming). |

### Provider retries

| Key | Type | Default | Meaning |
|---|---|---|---|
| `retry.provider.timeoutMs` | number | `httpIdleTimeoutMs` | Provider request timeout in milliseconds. |
| `retry.provider.maxRetries` | number | `0` | Retry attempts inside one provider request. |
| `retry.provider.maxRetryDelayMs` | number | `60000` | Longest delay a server can request before a provider retry, in milliseconds. `0` removes the limit. |

Keep `retry.provider.maxRetries` at `0` unless you need provider-level retries. Provider retries can delay PiG's own handling of quota and usage-limit errors.

### Cache warming

Cache warming is on by default, as in Pi. Each refresh is a real provider request that you pay for. PiG replays the last request with a one-token output limit shortly before the prompt cache expires, so the next request reads the cache instead of writing it again at full price.

- `off`: PiG never sends refreshes.
- `streaming`: only while the agent is running. This is the default.
- `idle`: while the agent runs, and between runs for up to 30 minutes after the last request.

A refresh is sent only when the model declares a prompt cache lifetime and PiG expects it to save at least $0.05 in avoided cache-miss cost. Warming stops when the conversation or model changes, after one hour (30 minutes when idle), and when the session ends. Refresh usage counts toward the session's token and cost totals, but it never enters the model's context. `/session` shows the mode, the next decision, and its estimated cost. An extension can change the decision with a `cache_warming_decision` handler.

To turn cache warming off, choose `off` for **Cache warming** in `/settings`, or set it in `~/.pig/agent/settings.json`:

```json
{
  "cacheWarming": "off"
}
```

## Session behavior

| Key | Type | Default | Meaning |
|---|---|---|---|
| `compaction` | object | see meaning | Automatic compaction: `enabled` (default `true`), `reserveTokens` (default `16384`), `keepRecentTokens` (default `20000`), and `modelOverrides` (per-model `reserveTokens` and `keepRecentTokens` keyed by exact `provider/modelId`). See [compaction](/docs/latest/compaction). |
| `branchSummary` | object | see meaning | Summary written when you leave a branch: `reserveTokens` (default `16384`) and `skipPrompt` (default `false`, skip the prompt and write no summary). |
| `steeringMode` | string | `one-at-a-time` | How queued steering messages are delivered: `all` or `one-at-a-time`. |
| `followUpMode` | string | `one-at-a-time` | How queued follow-up messages are delivered: `all` or `one-at-a-time`. |
| `sessionDir` | string |  | Directory that holds session files. A relative path resolves from the working directory. `PIG_CODING_AGENT_SESSION_DIR` and `--session-dir` override it. |
| `sessionShareDestination` | string | `auto` | Pi's `/share` destination: `auto`, `gist`, or `radius`. PiG accepts and keeps the value so a settings file shared with Pi stays valid, but PiG's `/share` always uploads to `https://pi-in-go.dev`, and `/settings` has no item for it (D64). |
| `defaultProjectTrust` | string | `ask` | Trust decision applied to a project that has none: `ask`, `always`, or `never`. PiG reads this key from the global file only. |

## Interface

| Key | Type | Default | Meaning |
|---|---|---|---|
| `theme` | string | `system` | Theme name, or a slash-separated automatic light/dark theme pair. `system` derives its colors from the terminal's colors. |
| `tuiMode` | string | `fullscreen` | `fullscreen` or `regular`. `regular` keeps the terminal's normal scrollback. Any other value means `fullscreen`. |
| `fullscreenScrollbar` | string | `auto` | Scrollbar in fullscreen mode: `auto`, `always`, or `hidden`. No effect in regular mode. |
| `fullscreenExitOutput` | string | `transcript` | `transcript` prints the final transcript when fullscreen exits. `resume-hint` restores the previous screen and prints only the resume hint. |
| `fullscreenCopyOnSelect` | boolean | `true` | Copy selected fullscreen text automatically. When disabled, `ctrl+x` copies the active selection. No effect in regular mode. |
| `fullscreenWheelScrollLines` | `"auto"` \| number | `"auto"` | Lines per mouse-wheel event in fullscreen mode, from 1 to 100. `"auto"` moves one line per event in local macOS terminals, which already accelerate wheel and trackpad input; elsewhere, and over SSH, it speeds up fast wheel spins to at most 6 lines per event. Alt+wheel moves five times as far. A fractional value is rounded down, and any other value falls back to `"auto"`. No effect in regular mode. |
| `extensionsAutoUpgrade` | boolean | `false` | PiG-only (D109). Run `pig extension upgrade` rules at startup and after `pig update` for Go extensions written for an older SDK, with a backup and one line per extension. Global settings only; a project file cannot turn it on. Pi's `extensions` setting is an array and unrelated. |
| `extensionsTypescriptRuntime` | string | `node` | PiG-only (D19). Runtime that runs TypeScript and JavaScript extensions: `node` or the name of a runtime an extension or Piglet installed. When it cannot run, extensions run on its fallback runtime if it names one, then on Node; `pig extensions runtime status` shows why. `PIG_TS_RUNTIME` overrides it. Global settings only. See [TypeScript extension runtimes](/docs/latest/typescript-runtimes). |
| `maskSecretInput` | boolean | `true` | PiG-only login-input privacy (D80). Show dots, a character count and the last four characters; fewer than five characters show no suffix. The setting applies to API-key logins and to `secret` prompts of an extension's OAuth login. False restores Pi's plain-text typing and submitted history. Change **Mask secret input** in `/settings`. Pi ignores this key in shared settings. |
| `hideThinkingBlock` | boolean | `false` | Hide thinking blocks in the transcript. |
| `doubleEscapeAction` | string | `tree` | Action for pressing Escape twice with an empty editor: `tree`, `fork`, or `none`. |
| `treeFilterMode` | string | `default` | Filter `/tree` opens with: `default`, `no-tools`, `user-only`, `labeled-only`, or `all`. |
| `editorPaddingX` | number | `0` | Horizontal padding inside the editor. |
| `outputPad` | `0` \| `1` | `1` | Horizontal transcript padding for messages, tool output, `!` command output, branch, compaction and skill summary blocks, and extension custom messages and entries. Changing it in `/settings` updates the transcript in place. |
| `autocompleteMaxVisible` | number | `5` | Rows shown in the autocomplete list, from 3 to 20. |
| `showHardwareCursor` | boolean | `false` | Use the terminal's own cursor in place of the drawn reverse-video cursor. PiG still positions it for input methods. Pi 1.1.0 drew both cursors. |
| `markdown.codeBlockIndent` | string | `"  "` | Prefix that indents rendered code blocks. |
| `markdown.mermaid` | string | `streaming` | Mermaid rendering mode: `off`, `final`, or `streaming`. |
| `quietStartup` | boolean \| `"header"` | `false` | `true` hides the startup header and the loaded-resource listing. `"header"` keeps the header (version and key hints) but hides the model scope line and the loaded-resource listing. `--verbose` overrides both. |
| `collapseChangelog` | boolean | `false` | Show a condensed changelog after an update. |
| `lastChangelogVersion` | string |  | Last changelog version shown. PiG writes this. |

## Images and terminal

| Key | Type | Default | Meaning |
|---|---|---|---|
| `terminal` | object | see meaning | Terminal image, width, shrink-repaint, and progress settings described below. |
| `images` | object | see meaning | Image resize and transcript-blocking settings described below. |
| `terminal.showImages` | boolean | `true` | Render images when the terminal accepts them. |
| `terminal.imageWidthCells` | number | `60` | Width of a rendered image, in terminal cells. |
| `terminal.clearOnShrink` | boolean | `false` | Repaint the screen when content shrinks. |
| `terminal.showTerminalProgress` | boolean | `false` | Show OSC 9;4 progress in the terminal tab. |
| `images.autoResize` | boolean | `true` | Resize an image to at most 2000 by 2000 pixels before PiG sends it to a model. |
| `images.blockImages` | boolean | `false` | Keep images away from the model. Each image in a message or tool result is replaced by the text `Image reading is disabled.` before the request is sent, as in Pi (`sdk.ts:312`; PiG `coding/block_images.go`). |

## Resources

| Key | Type | Default | Meaning |
|---|---|---|---|
| `packages` | object[] |  | Packages to load. See [packages](/docs/latest/packages). |
| `extensions` | string[] |  | Extension paths to load. See [extensions](/docs/latest/extensions). |
| `skills` | string[] |  | Skill paths to load. See [skills](/docs/latest/skills). |
| `prompts` | string[] |  | Prompt paths to load. |
| `themes` | string[] |  | Theme paths to load. |
| `enableSkillCommands` | boolean | `true` | Offer skills as `/skill:name` commands. |

Resource paths in the global file resolve from the agent directory. Paths in the project file resolve from the project's `.pig` directory. Absolute paths and a leading `~` work. Resource lists accept glob exclusions with `!pattern`, exact inclusion with `+path`, and exact exclusion with `-path`. A leading `~` in these patterns expands to your home directory, for example `!~/claude-backup/skills/pdf`, as in Pi 1.3.0. Pi 1.1.0 matched the `~` literally, so such a pattern matched nothing ([Pi #10765](https://github.com/earendil-works/pi/issues/10765)).

The built-in extensions are named `builtin:mcp`, `builtin:llama.cpp`, `builtin:codemode`, and `builtin:tool-search` in `extensions`. They load by default; `-builtin:mcp` turns one off. A `+builtin:<name>` or `-builtin:<name>` entry in the project file overrides the global file. `pig config` lists them under Built-in. `--no-extensions` turns them off too, and `-e builtin:<name>` loads one explicitly.

## Terminal capability overrides

| Key | Type | Default | Meaning |
|---|---|---|---|
| `terminal.hyperlinks` | `boolean \| "auto"` | `"auto"` | Override OSC 8 hyperlink detection. |
| `terminal.images` | `"kitty" \| "iterm2" \| "auto" \| false` | `"auto"` | Override inline-image protocol detection. `false` turns images off. |
| `terminal.trueColor` | `boolean \| "auto"` | `"auto"` | Override true-color detection. |

A setting wins over the matching `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL` or `PI_TRUE_COLOR` variable, and the variable wins over detection. `"auto"` and any other value leave detection in charge. Without true color, PiG draws the theme in the 256-color palette, as Pi does. See [terminal setup](/docs/latest/terminal-setup).

## Shell and tools

| Key | Type | Default | Meaning |
|---|---|---|---|
| `shellPath` | string |  | Shell used by the bash tool and `!` commands. A leading `~` expands. |
| `shellCommandPrefix` | string |  | Prefix applied to every shell command. |
| `externalEditor` | string | `$VISUAL`, `$EDITOR`, then the platform default | Editor opened by `app.editor.external`. |
| `npmCommand` | string[] | `["npm"]` | Command and arguments used to run npm for package lookup and installation. |

## Tools

| Key | Type | Default | Meaning |
|---|---|---|---|
| `defaultTools` | string[] | `read`, `bash`, `edit`, `write` | Tools active at startup. Plain names replace the defaults; `+name` adds a tool and `-name` removes one. An empty array turns off every built-in tool but keeps extension and SDK tools. |
| `codemode` | object | see meaning | How the `codemode` tool presents tools while it is active. `mode` is `on` (default) or `only`. `inlineBudget` is the estimated number of tokens (characters divided by 4) its description may spend on tool declarations, default `3000`. |

See [Codemode and tool search](codemode.md) for `codemode` and [MCP servers](mcp.md) for the servers that turn it on.

A list of only `+name` and `-name` entries changes the inherited selection instead of replacing it, so a project can adjust the tools a user chose. For example, this enables `codemode` next to the default tools:

```json
{
  "defaultTools": ["+codemode"]
}
```

This list replaces `bash` with `powershell` and enables `grep`: `["-bash", "+powershell", "+grep"]`. In one list, plain names form the selection, and `+name` and `-name` then apply in order.

`/reload` enables tools newly added to `defaultTools`. It does not turn off tools removed from it or turn on unchanged tools you turned off. `--tools` with plain names, `--no-tools` and `--no-builtin-tools` override `defaultTools`, also on reload. `--tools` with only `+name` and `-name` entries changes the `defaultTools` selection instead, and a tool removed with `-name` stays removed after `/reload`.

The built-in tools are `read`, `bash`, `powershell`, `edit`, `write`, `grep`, `find`, and `ls`. `defaultTools` can also name `codemode` and `tool_search`, which built-in extensions register inactive. The CLI tool options override this setting for one run. See [Tools](/docs/latest/cli#tools).

## Notices

| Key | Type | Default | Meaning |
|---|---|---|---|
| `warnings` | object | see meaning | Warning toggles: `anthropicExtraUsage` (default `true`) warns when Anthropic subscription auth may use paid extra usage. |
| `showCacheMissNotices` | boolean | `false` | Report a significant prompt cache miss and each successful cache-warming refresh. |
| `enableInstallTelemetry` | boolean | `true` | Gate for the anonymous install/update ping and the pig-branded OpenRouter, NVIDIA, and Cloudflare attribution headers. When on, PiG sends one HTTPS GET carrying only your PiG version (`?version=<version>`) and PiG's `User-Agent` header to `https://pi-in-go.dev/api/report-install`, after a fresh install and after an update that has new changelog entries, and at no other time. The site stores one data point (the version and arrival time) per call and never logs your IP address or User-Agent. Set to `false`, or set `PI_TELEMETRY=0`, to opt out; `PI_OFFLINE=1` also stops it. Override with `PI_TELEMETRY`. The endpoint is PiG's own (D64). |
| `enableAnalytics` | boolean | `false` | Opt in to analytics data sharing. PiG stores the choice; nothing sends data. |
| `trackingId` | string |  | Analytics tracking identifier. PiG writes this on the first opt-in and keeps it when you toggle `enableAnalytics`. Bug reports omit it. |
| `deviceId` | string |  | Stable random ID of this installation. PiG writes it to the global settings file the first time a sign-in needs it and ignores a `deviceId` in a project file. Bug reports omit it. |

## Related

- [Environment variables](/docs/latest/environment-variables) lists runtime overrides.
- [Keybindings](/docs/latest/keybindings) covers `keybindings.json`.
- [Models](/docs/latest/models) covers model selection.
