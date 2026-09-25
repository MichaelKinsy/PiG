# Settings

PiG reads settings from `~/.pig/agent/settings.json`. The file is optional. Every
key is optional, and a key you leave out keeps its default.

Set `PIG_HOME` to move the whole configuration root. Set `PIG_CODING_AGENT_DIR`
to move only this directory.

```json
{
  "defaultModel": "claude-sonnet-4",
  "theme": "dark",
  "compaction": { "enabled": true }
}
```

Use `/settings` to change a setting from inside a session. PiG writes the file
for you and applies the change without a restart.

## Where PiG reads settings

PiG merges two files. A key set in the project file overrides the same key in the
global file. `defaultProjectTrust` is the exception: PiG reads it from the global
file only.

| Scope | Path |
|---|---|
| global | `~/.pig/agent/settings.json` |
| project | `<project>/.pig/settings.json` |

PiG reads the project file only after you trust the project. An untrusted project
contributes nothing, so cloning a repository cannot change your shell, your model
or your packages before you agree to it. See [Security](/docs/latest/security).

An empty default column means PiG has no value for the key until you set one, or
the value depends on what it detects.

## Models and providers

| Key | Type | Default | Meaning |
|---|---|---|---|
| `defaultProvider` | string |  | Provider used when none is chosen. |
| `defaultModel` | string |  | Model used when none is chosen. |
| `defaultThinkingLevel` | string |  | Thinking level applied to a new session. |
| `modelThinkingLevels` | object |  | Thinking level for a new session per model, keyed by `provider/modelId`. It overrides `defaultThinkingLevel`. |
| `enabledModels` | string[] | `all` | Models offered in the model selector. |
| `thinkingBudgets` | object |  | Token budget per thinking level: `minimal`, `low`, `medium`, `high`. |
| `transport` | string | `auto` | Transport for providers that support more than one: `auto`, `sse`, `websocket`, or `websocket-cached`. |
| `retry` | object | see meaning | Retry policy: `enabled` (default `true`), `maxRetries` (default `3`), `baseDelayMs` (default `2000`), `maxAgentDelayMs` (the cap on each retry delay, default `60000`), and a per-`provider` override. An explicit `0` is kept. |
| `httpIdleTimeoutMs` | number | `300000` | Idle timeout for provider requests, in milliseconds. `0` or `"disabled"` turns it off. |
| `cacheWarming` | string | `streaming` | `off`, `streaming`, or `idle`. Keeps the provider's prompt cache warm with periodic requests. Global setting only: PiG ignores it in a project file. See [cache warming](#cache-warming). |

### Cache warming

Cache warming is on by default, as in Pi. Each refresh is a real provider
request that you pay for. PiG replays the last request with a one-token output
limit shortly before the prompt cache expires, so the next request reads the
cache instead of writing it again at full price.

- `off`: PiG never sends refreshes.
- `streaming`: only while the agent is running. This is the default.
- `idle`: while the agent runs, and between runs for up to 30 minutes after
  the last request.

A refresh is sent only when the model declares a prompt cache lifetime and PiG
expects it to save at least $0.05 in avoided cache-miss cost. Warming stops when
the conversation or model changes, after one hour (30 minutes when idle), and
when the session ends. Refresh usage counts toward the session's token and cost
totals, but it never enters the model's context. `/session` shows the mode, the
next decision, and its estimated cost.

To turn cache warming off, choose `off` for **Cache warming** in `/settings`, or
set it in `~/.pig/agent/settings.json`:

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
| `steeringMode` | string | `one-at-a-time` | How queued steering messages are dispatched. |
| `followUpMode` | string | `one-at-a-time` | How queued follow-up messages are dispatched. |
| `sessionDir` | string |  | Directory that holds session files. |
| `defaultProjectTrust` | string | `ask` | Trust decision applied to a project that has none: `ask`, `always`, or `never`. PiG reads this key from the global file only. |

## Interface

| Key | Type | Default | Meaning |
|---|---|---|---|
| `theme` | string |  | Active theme name. |
| `tuiMode` | string | `regular` | `regular` or `fullscreen`. |
| `fullscreenScrollbar` | string | `auto` | Scrollbar in fullscreen mode: `auto`, `always`, or `hidden`. No effect in regular mode. |
| `fullscreenExitOutput` | string | `transcript` | `transcript` prints the final transcript when fullscreen exits. `resume-hint` restores the previous screen and prints only the resume hint. |
| `fullscreenCopyOnSelect` | boolean | `true` | Copy selected fullscreen text automatically. When disabled, `ctrl+x` copies the active selection. No effect in regular mode. |
| `hideThinkingBlock` | boolean | `false` | Hide thinking blocks in the transcript. |
| `doubleEscapeAction` | string | `tree` | Action bound to pressing escape twice. |
| `treeFilterMode` | string | `default` | Filter `/tree` opens with. |
| `editorPaddingX` | number | `0` | Horizontal padding inside the editor. |
| `outputPad` | number | `1` | Horizontal padding for messages and thinking blocks: `0` or `1`. |
| `autocompleteMaxVisible` | number | `5` | Rows shown in the autocomplete list. |
| `showHardwareCursor` | boolean | `false` | Show the terminal's own cursor. |
| `markdown` | object |  | Markdown rendering: `codeBlockIndent`, `mermaid`. |
| `quietStartup` | boolean | `false` | Suppress the startup banner. |
| `collapseChangelog` | boolean | `false` | Show a condensed changelog. |
| `lastChangelogVersion` | string |  | Last changelog version shown. PiG writes this. |

## Images and terminal

| Key | Type | Default | Meaning |
|---|---|---|---|
| `terminal` | object | see meaning | Terminal image, width, shrink-repaint, and progress settings described below. |
| `images` | object | see meaning | Image resize and transcript-blocking settings described below. |
| `showImages` | boolean | `true` | Older flat form of `terminal.showImages`. |
| `imageWidthCells` | number | `60` | Older flat form of `terminal.imageWidthCells`. |
| `clearOnShrink` | boolean | `false` | Older flat form of `terminal.clearOnShrink`. |
| `imageAutoResize` | boolean | `true` | Older flat form of `images.autoResize`. |
| `blockImages` | boolean | `false` | Older flat form of `images.blockImages`. |
| `terminal.showImages` | boolean | `true` | Render images when the terminal accepts them. |
| `terminal.imageWidthCells` | number | `60` | Width of a rendered image, in terminal cells. |
| `terminal.clearOnShrink` | boolean | `false` | Repaint the screen when content shrinks. |
| `terminal.showTerminalProgress` | boolean | `false` | Show OSC 9;4 progress in the terminal tab. |
| `images.autoResize` | boolean | `true` | Resize an image to fit the width. |
| `images.blockImages` | boolean | `false` | Hide images in the transcript. Pi also removes images from model requests. PiG does not yet: it still sends them to the provider. |

PiG also reads the older flat keys `showImages`, `imageWidthCells`,
`clearOnShrink`, `imageAutoResize`, and `blockImages`. A nested key wins over the
flat key with the same meaning.

## Resources

| Key | Type | Default | Meaning |
|---|---|---|---|
| `packages` | object[] |  | Packages to load. See [packages](/docs/latest/packages). |
| `extensions` | string[] |  | Extension paths to load. See [extensions](/docs/latest/extensions). |
| `skills` | string[] |  | Skill paths to load. See [skills](/docs/latest/skills). |
| `prompts` | string[] |  | Prompt paths to load. |
| `themes` | string[] |  | Theme paths to load. |
| `enableSkillCommands` | boolean | `true` | Offer skills as slash commands. |

## Terminal capability overrides

| Key | Type | Default | Meaning |
|---|---|---|---|
| `terminal.hyperlinks` | `boolean \| "auto"` | `"auto"` | Override OSC 8 hyperlink detection. |
| `terminal.images` | `"kitty" \| "iterm2" \| "auto" \| false` | `"auto"` | Override inline-image protocol detection. `false` turns images off. |
| `terminal.trueColor` | `boolean \| "auto"` | `"auto"` | Override true-color detection. |

A setting wins over the matching `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL` or
`PI_TRUE_COLOR` variable, and the variable wins over detection. `"auto"` and any
other value leave detection in charge. Without true color, PiG draws the theme
in the 256-color palette, as Pi does. See [terminal setup](/docs/latest/terminal-setup).

## Shell and tools

| Key | Type | Default | Meaning |
|---|---|---|---|
| `shellPath` | string |  | Shell used by the bash tool. |
| `shellCommandPrefix` | string |  | Prefix applied to every shell command. |
| `commandPrefix` | string |  | Former name of `shellCommandPrefix`. PiG still reads it. |
| `externalEditor` | string |  | Editor opened by `app.editor.external`. |
| `npmCommand` | string[] | `["npm"]` | Command and arguments used to run npm for package lookup and installation. |

## Tools

| Key | Type | Default | Meaning |
|---|---|---|---|
| `defaultTools` | string[] | `read`, `bash`, `edit`, `write` | Built-in tools active at startup. An empty array turns off every built-in tool but keeps extension and SDK tools. |

The built-in tools are `read`, `bash`, `powershell`, `edit`, `write`, `grep`,
`find`, and `ls`. The CLI tool options override this setting for one run. See
[Tools](/docs/latest/cli#tools).

## Notices

| Key | Type | Default | Meaning |
|---|---|---|---|
| `warnings` | object | see meaning | Warning toggles: `anthropicExtraUsage` (default `true`) warns when Anthropic subscription auth may use paid extra usage. |
| `showCacheMissNotices` | boolean |  | Report a prompt cache miss and each successful cache-warming refresh. |
| `enableInstallTelemetry` | boolean | `true` | Gate for the anonymous install/update ping and the pig-branded OpenRouter, NVIDIA, and Cloudflare attribution headers. When on, PiG sends one HTTPS GET carrying only your PiG version (`?version=<version>`) and PiG's `User-Agent` header to `https://pi-in-go.dev/api/report-install`, after a fresh install and after an update that has new changelog entries, and at no other time. The site stores one data point (the version and arrival time) per call and never logs your IP address or User-Agent. Set to `false`, or set `PI_TELEMETRY=0`, to opt out; `PI_OFFLINE=1` also stops it. Override with `PI_TELEMETRY`. |
| `enableAnalytics` | boolean | `false` | Opt in to analytics data sharing. PiG stores the choice; nothing sends data. |
| `trackingId` | string |  | Analytics tracking identifier. PiG writes this on the first opt-in and keeps it when you toggle `enableAnalytics`. Bug reports omit it. |

## Related

- [Environment variables](/docs/latest/environment-variables) lists runtime overrides.
- [Keybindings](/docs/latest/keybindings) covers `keybindings.json`.
- [Models](/docs/latest/models) covers model selection.
