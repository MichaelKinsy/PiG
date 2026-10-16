# Settings by example

[Settings](/docs/latest/settings) is the reference: every key, its type and its default. This page shows the keys at work. Each recipe gives the JSON to put in `~/.pig/agent/settings.json`, how to check the effect and what you should see. Edit the file with any editor, or use `/settings` for the keys it lists. PiG reads `settings.json` at startup. `/reload` re-reads it in a running session (Pi `core/agent-session.ts:3698`).

The outputs below come from PiG run against a scripted test model, with each key set in turn. A recipe says "checked" when the output was observed. Keys without a recipe are in [the table at the end](#every-other-key). That table gives an example value and the Pi source line, and it was not run on this page.

Settings are merged from two files, and a key in the project file wins: `~/.pig/agent/settings.json` (yours) and `<project>/.pig/settings.json` (the project's, read only after you trust the project). Some keys are read from the global file only and are ignored in a project file: `defaultProjectTrust`, `httpProxy`, `cacheWarming`, `extensionsAutoUpgrade`, `extensionsTypescriptRuntime` and `deviceId`.

If the file is not valid JSON, PiG warns at startup (`Invalid settings file …`) and uses defaults for what it cannot read. A value of the wrong JSON type, such as `"skills": 5`, is ignored with one warning that names it, and the other keys apply. Pi 1.3.0 exits with a `TypeError` instead (D158). After an edit, start PiG again (or `/reload`) and look at the first lines of the screen.

## Choose the model, and how hard it thinks

```json
{
  "defaultProvider": "mock",
  "defaultModel": "mock-think",
  "defaultThinkingLevel": "high",
  "modelThinkingLevels": { "mock/mock-think": "low" }
}
```

`defaultProvider` and `defaultModel` choose the model for a new session. `defaultModel` is the model ID **without** the provider prefix. A value such as `mock/mock-think` is not accepted here. `defaultThinkingLevel` applies to a new session, and `modelThinkingLevels` overrides it for one model, keyed by `provider/modelId`.

**Check:** start PiG with no `--model`. The footer names the model. For a model that can think, it shows the level: `mock-think • low` (Pi: `components/footer.ts:236-238`). Checked: the provider request for `pig -p hi` named the model `mock-think` and carried `reasoning_effort: "low"`. Without `modelThinkingLevels` it carried `"high"`. A model that cannot think carries no effort at all, and `/thinking high` on it says `Error: Unknown thinking level "high". Available levels: off.`

`--model` and `--thinking` on the command line win over these keys.

**Limit the models `Ctrl+P` cycles through:**

```json
{ "enabledModels": ["mock/mock-think"] }
```

`/scoped-models` edits the same list and `Ctrl+S` there saves it (Pi `settings-schema.ts:321`). Patterns follow `--models`.

## Choose the tools

```json
{ "defaultTools": ["read", "grep"] }
```

Plain names replace the default set (`read`, `bash`, `edit`, `write`).

**Check:** ask `List the names of your tools and nothing else.` PiG's answer follows what the model was given. Checked in the provider request: the tool list was `read`, `grep`. These variants were also checked:

| `defaultTools` | Tools the model received |
|---|---|
| (not set) | `read`, `bash`, `edit`, `write` |
| `["read", "grep"]` | `read`, `grep` |
| `["+ls", "-bash"]` | `read`, `edit`, `write`, `ls` |
| `[]` | none |

`+name` adds a tool, `-name` removes one, and a list of only `+` and `-` entries adjusts the inherited set. `--tools`, `--no-tools` and `--no-builtin-tools` on the command line override the setting for one run (Pi `cli/args.ts:151`).

A project file that sets `defaultTools` can give a repository's own agent fewer tools than yours, or more. Read a project's `.pig/settings.json` before you trust the project.

## Retry a failing provider

```json
{ "retry": { "enabled": true, "maxRetries": 3, "baseDelayMs": 200 } }
```

PiG retries a request that fails with a transient provider or transport error, such as an overloaded service (a 503) or a dropped connection. It waits `baseDelayMs`, then twice that, and so on, up to `maxAgentDelayMs`. Context-length errors are not retried (they go to compaction) (Pi `ai/src/utils/retry.ts:253`, `core/agent-session.ts:3734-3737`).

**Check (print mode, a provider that answers `503` twice and then succeeds):**

```bash
pig --no-session --model mock/mock-1 -p "hi"
```

```text
ok after retries
```

The provider saw three requests and the command took about 0.65 seconds: 200 ms and 400 ms of waiting. In the interactive screen, PiG shows a line above the editor while it waits, for example `── ⠋ Retrying (3/3) in 8s... (escape to cancel) ──`. `Esc` cancels the wait.

With `"retry": { "enabled": false }` the same run makes one request, prints the error, and exits 1:

```text
503: {"message":"overloaded"}
```

## Run shell commands your way

```json
{
  "shellCommandPrefix": "export GREETING=hello",
  "httpProxy": "http://proxy.example:3128"
}
```

`shellCommandPrefix` runs before every command of the `bash` tool and every `!` and `!!` command. `httpProxy` sets `HTTP_PROXY` and `HTTPS_PROXY` for PiG and everything it starts, when those variables are not already set. PiG reads `httpProxy` from the global file only.

**Check:** in PiG, type `!echo "$GREETING $HTTPS_PROXY"` and press `Enter`. Checked:

```text
 $ echo "$GREETING $HTTPS_PROXY $HTTP_PROXY"
 hello http://proxy.example:3128 http://proxy.example:3128
```

A `!!` command runs the same way, but its output is not sent to the model. See [Shell aliases](/docs/latest/shell-aliases).

## Keep sessions somewhere else

```json
{ "sessionDir": "/home/you/pig-sessions" }
```

**Check:** run PiG, send one message and list the folder. Checked: the folder held `2026-10-11T16-29-41-621Z_01a12bcc-7375-7762-9768-9114771457de.jsonl`, one file for the session, and `/session` printed that path. The default is `~/.pig/agent/sessions/--<project path>--/`.

## Stop the prompt-cache refresher

```json
{ "cacheWarming": "off" }
```

Cache warming sends small extra requests that you pay for ([Settings](/docs/latest/settings#cache-warming)).

**Check:** `/session`. Checked:

```text
 Cache Warming
 Mode: off
 Status: Inactive (cache warming disabled)
```

The default shows `Mode: streaming`. The key is global-only.

## Trust a project

A repository can carry its own `.pig/settings.json`. PiG ignores it until you trust the project. Take this project file as the example:

```json
{ "quietStartup": true, "shellCommandPrefix": "echo PROJECT-PREFIX-RAN" }
```

With `defaultProjectTrust` unset (it is `ask`), the first start in that folder asks:

```text
 Trust project folder?
 /home/you/project
 This allows pig to load .pig settings and resources, install missing project packages, and execute
 project extensions.
 → Trust
   Trust parent folder (/home/you)
   Trust (this session only)
   Do not trust
   Do not trust (this session only)
 ↑↓ navigate  enter select  escape/ctrl+c cancel
```

Checked: after `Trust (this session only)`, `!echo hi` printed `PROJECT-PREFIX-RAN` first and the startup header was hidden. After `Do not trust (this session only)`, `!echo hi` printed only `hi`, and PiG said:

```text
 This project is not trusted. Project .pig resources and packages are ignored. Use /trust to save a
 trust decision, then restart pig.
```

The three values of `defaultProjectTrust` (global file only):

| Value | What happens in an untrusted project |
|---|---|
| `ask` | The prompt above. |
| `never` | No prompt; project files are ignored. Checked: the `This project is not trusted` line appeared. |
| `always` | No prompt; project files are loaded. Checked: `PROJECT-PREFIX-RAN` ran. |

`always` runs a repository's settings, extensions and Skills without asking. Use it only for folders you wrote. See [Security](/docs/latest/security) and `/trust`.

## Quieter, tighter screen

```json
{ "quietStartup": "header", "editorPaddingX": 2, "outputPad": 0 }
```

- `quietStartup: true` hides the header and the list of loaded resources. Checked: the first screen shows only the editor and footer. `"header"` keeps the header and hides the resource list. Checked: the `PiG. v…` line and key hints stay, and the hint reads `Press ctrl+o to show full startup help.` (without "and loaded resources"). `--verbose` overrides both.
- `editorPaddingX` indents the text you type. Checked: with `2`, typed text starts two cells in from the left. `0` is the default.
- `outputPad: 0` removes the one-cell left margin around messages, tool output, `!` command output, summary blocks, and extension messages (`1` is the default). Changing it in `/settings` redraws the transcript in place.

## Navigate branches

```json
{ "branchSummary": { "skipPrompt": true }, "doubleEscapeAction": "tree" }
```

Pressing `Esc` twice on an empty editor opens `/tree` (the default `doubleEscapeAction`). Checked:

```text
   Session Tree
  ↑/↓ move · ←/→ page · ctrl+←/→ branch · ctrl+x copy · shift+l label · shift+t label time
  filters ctrl+d/t/u/l/a · cycle ctrl+o/shift+ctrl+o
  Type to search:
  • [system]
  • user: hello
› • assistant: Here is a short answer about notes.txt.
  (3/3)
```

Pick an earlier entry and press `Enter`. By default PiG asks `Summarize branch?`. With `skipPrompt: true` it does not ask. It writes no summary and says `Navigated to selected point`. Checked, and the chosen message came back in the editor. Other values of `doubleEscapeAction` are `fork` and `none` (Pi `settings-schema.ts:332`).

## Compact sooner

```json
{ "compaction": { "enabled": true, "keepRecentTokens": 200 } }
```

PiG keeps the most recent `keepRecentTokens` tokens in full and summarises the rest. The default keeps 20,000 tokens, so a short conversation has nothing to summarise and `/compact` says `Error: Compaction failed: Nothing to compact (session too small)`. With `200`, a short conversation compacts. Checked, after three long prompts and `/compact`:

```text
 [compaction]
 Compacted from 120 tokens (ctrl+o to expand)
```

`"enabled": false` turns off automatic compaction. `/settings` shows it as `Auto-compact false`. See [Compaction](/docs/latest/compaction).

## Hide typed secrets

```json
{ "maskSecretInput": true }
```

PiG-only (D80). When you enter an API key in `/login`, PiG shows dots, a character count and the last four characters. Checked: the prompt says `Input hidden (PiG default). Show like Pi: /settings → Mask secret input` and `(0 characters)`. With `false`, PiG types plain text, as Pi does.

## Every other key

Each row gives a value to try, what it does and the line in Pi 1.3.0's `packages/coding-agent/src/core/settings-schema.ts` that defines it. These were not run on this page. Most are visible in the first screen after you restart, in `/settings`, or in the behaviour named.

| Key | Example | What you see | Pi |
|---|---|---|---|
| `transport` | `"sse"` | Uses server-sent events for providers that offer several transports. `auto` picks the best. | `:218` |
| `thinkingBudgets` | `{"high": 8192}` | Token budget for a thinking level on providers that take one. | `:353` |
| `httpIdleTimeoutMs` | `600000` | A provider request that is idle this long is cancelled. Keepalive comments and pings do not count as activity (D131). `0` disables it. | `:390` |
| `websocketConnectTimeoutMs` | `5000` | The WebSocket handshake gives up after 5 s. | `:402` |
| `steeringMode` | `"all"` | A message you send while the agent works is delivered with the others at once, not one per turn. | `:224` |
| `followUpMode` | `"all"` | Same, for follow-up messages (`Alt+Enter`). | `:229` |
| `theme` | `"light"` or `"light/dark"` | Changes the colours at once. The pair form follows your terminal. The default, `system`, derives its colours from the terminal. `/settings` previews themes. | `:234` |
| `tuiMode` | `"regular"` | Keeps the terminal's normal scrollback instead of the fullscreen view. `--tui-mode` overrides. | `:407` |
| `fullscreenScrollbar` | `"hidden"` | Hides the fullscreen scrollbar. | `:418` |
| `fullscreenExitOutput` | `"resume-hint"` | On exit, restores the previous screen and prints only the resume line. | `:412` |
| `fullscreenCopyOnSelect` | `false` | Selecting text no longer copies it; `Ctrl+X` does. | `:424` |
| `fullscreenWheelScrollLines` | `3` | Three lines per wheel event. | `:430` |
| `hideThinkingBlock` | `true` | Thinking text is hidden; `Ctrl+T` shows it. | `:238` |
| `treeFilterMode` | `"user-only"` | `/tree` opens showing only your messages. | `:338` |
| `autocompleteMaxVisible` | `8` | The suggestion list shows 8 rows (default 5). | `:370` |
| `showHardwareCursor` | `true` | The terminal's own cursor shows at the input position, in place of the drawn reverse-video cursor. Pi 1.1.0 showed both. | `:376` |
| `markdown` | `{"codeBlockIndent": "    ", "mermaid": "final"}` | Changes how code blocks indent in the transcript. `mermaid` is `off`, `final` or `streaming` (the default). | `:379` |
| `collapseChangelog` | `true` | The "what's new" notice after an update is condensed. | `:274` |
| `terminal.showImages` | `false` | Images in the transcript are not drawn. | `:75` |
| `terminal.imageWidthCells` | `40` | Images are 40 cells wide. | `:81` |
| `terminal.clearOnShrink` | `true` | The screen is cleared when the content gets shorter. | `:87` |
| `terminal.showTerminalProgress` | `true` | A progress bar appears in the terminal tab while the agent works. | `:93` |
| `terminal.hyperlinks`, `terminal.images`, `terminal.trueColor` | `false`, `"kitty"`, `true` | Overrides detection of links, inline images and 24-bit colour. A setting beats `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL`, `PI_TRUE_COLOR`. | `:99-103` |
| `images.autoResize` | `false` | Large images go to the model at full size. | `:107` |
| `images.blockImages` | `true` | The model receives `Image reading is disabled.` in place of each image (Pi `sdk.ts:312`). Checked for this page: `pig -p "what is this" @a.png` sent the text and no image. | `:113` |
| `enableSkillCommands` | `false` | Skills no longer appear as `/skill:<name>`. | `:313` |
| `packages`, `extensions`, `skills`, `prompts`, `themes` | `["./my-skills"]` | Adds those paths to discovery. See [Packages](/docs/latest/packages), [Skills](/docs/latest/skills). | `:295-312` |
| `shellPath` | `"/bin/zsh"` | The `bash` tool starts that shell. | `:248` |
| `externalEditor` | `"code --wait"` | `Ctrl+G` opens your message in that editor. | `:245` |
| `npmCommand` | `["pnpm"]` | Package lookup and installs run `pnpm`. | `:268` |
| `retry.provider` | `{"maxRetries": 2, "timeoutMs": 120000}` | Retries inside one provider request, and that request's timeout. Keep `maxRetries` at `0` unless you need it. See [Settings](/docs/latest/settings#provider-retries). | |
| `codemode` | `{"mode": "only", "inlineBudget": 2000}` | See [Codemode](/docs/latest/codemode). | `:381` |
| `warnings` | `{"anthropicExtraUsage": false}` | Hides the paid-extra-usage warning. | `:380` |
| `showCacheMissNotices` | `true` | A notice appears for each prompt-cache miss and refresh. | `:239` |
| `enableInstallTelemetry` | `false` | No install or update ping is sent (D64). `PI_TELEMETRY=0` does the same. | `:280` |
| `enableAnalytics`, `trackingId` | written by the first-time setup | `false` after you choose `Don't share` (see [Your first ten minutes](/docs/latest/first-run)). | `:286,292` |
| `extensionsAutoUpgrade`, `extensionsTypescriptRuntime` | `true`, `"bun"` | PiG-only (D109, D19). See [TypeScript extension runtimes](/docs/latest/typescript-runtimes). | none |

For `lastChangelogVersion` and `deviceId`, PiG writes the value; do not edit them.
