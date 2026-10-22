# Slash commands

PiG has the built-in slash commands of Pi 1.4.0 ([Pi's slash command reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/slash-commands.md)). Rows marked `[pig]` are PiG additions. Extensions can register more slash commands.

## Core

| Command | Description |
|---|---|
| `/settings` | Open the settings menu. Changes apply in place; `Escape` closes it. |
| `/model [provider/model]` | Open the model selector, filtered by the argument. |
| `/thinking [level]` | Complete and set an available thinking level, or open the selector without a level. |
| `/scoped-models` | Enable/disable models for Ctrl+P cycling. |
| `/login` | Configure provider authentication. |
| `/logout` | Remove provider authentication. |
| `/new` | Start a new session in the same cwd. |
| `/resume` | Resume a different session. |
| `/fork` | Fork from a previous user message. |
| `/clone` | Duplicate session at the current position. |
| `/tree` | Navigate session tree. |
| `/compact [instructions]` | Manually compact the context, optionally with custom summary instructions. |
| `/reload` | Reload keybindings, extensions, skills, prompts, themes, and context files. |
| `/reload --explain` | Same plus a placement/cell report. [pig] |
| `/export [path]` | Export session (default HTML; specify `.jsonl`). |
| `/import <path>` | Confirm, import without replacing an existing file, and resume a JSONL session. |
| `/share` | Upload an unlisted Session share to `pi-in-go.dev` that expires after 30 days. Pi shares a secret GitHub gist or a Radius artifact (D64). |
| `/bug [description]` | Write a bug report archive to the current directory and print a prefilled PiG issue link to attach it to. Nothing is uploaded (D62). |
| `/copy` | Copy the last agent message to clipboard. |
| `/name [text]` | Set the session display name, or show the current name when you omit the text. |
| `/session` | Show session info and stats. |
| `/changelog` | Show changelog entries. |
| `/debug` | Write a debug log and show its path as literal text. |
| `/hotkeys` | List keyboard shortcuts. |
| `/quit` | Exit PiG. |
| `/trust` | Set the trust decision for the current project. |
| `/sprite [list\|set <id>\|preview [id]\|create]` | Choose the pig the startup header shows: the picker draws the highlighted sprite's pig right after its rows, and `Create your own...` draws none. Preview a sprite's full art, or design your own with the model (`create`; the picker's last item, "Create your own...", shows how). Registered by the built-in `pig-login` extension, so it is absent with `--no-extensions` or `-builtin:pig-login`. [pig] |
| `/llama` | Manage a local llama.cpp server. Registered by the built-in `llama.cpp` extension, so it is absent with `--no-extensions` or `-builtin:llama.cpp`. |
| `/mcp` | Manage MCP servers: sign in, reconnect, enable or disable, and change exposure. Registered by the built-in `mcp` extension, so it is absent with `--no-mcp`, `--no-extensions` or `-builtin:mcp`. See [MCP servers](/docs/latest/mcp). |

There is no `/exit` or `/clear` command. Pi has neither, and PiG matches Pi. To leave, use `/quit`. To clear the editor, press the `app.clear` key (`ctrl+c` by default). `/hotkeys` lists the current bindings.

## Piglet composition

| Command | Description |
|---|---|
| `/piglet` | Inspect the Piglet active in this process. A Piglet can strip it with `strip.commands: [/piglet]`. |

PiG Standard and other Piglets can add commands through selected extension Resources. Stock PiG does not register `/runner`, `/pig-runner`, or other product commands. A Piglet can strip a built-in command with `strip.commands`; the command then leaves autocomplete, `/hotkeys` and RPC `get_commands` (D92).

## Parity harness (only when `PIG_PARITY_HARNESS=1`)

`/probe-*` family - internal scenarios used by `test/parity/scenarios/`. Not user-facing.

## Worked examples

Each example shows what you type, what PiG prints and where the behaviour comes from. The screens are taken from a real PiG session (a test model answering `What is in notes.txt?` first), so the lines above and below the result are the session's own. Paths and IDs on your computer differ. Where PiG differs from Pi, the divergence number is given. File and line references point to Pi 1.4.0's `packages/coding-agent/src/modes/interactive/interactive-mode.ts` (written `Pi:`) and to PiG's source.

### Look at the session

**`/session`** prints the session's file, ID, message counts and token totals. Pi: `interactive-mode.ts:3258`.

```text
/session
```

```text
 Session Info

 Name: demo
 File:
 /home/you/.pig/agent/sessions/--home-you-project--/2026-10-11T16-15-21-374Z_01a12bbf-531e-759d-8c39-1469b9161672.jsonl
 ID: 01a12bbf-531e-759d-8c39-1469b9161672

 Messages
 Total: 3
 User: 1
 Assistant: 1
 Tools: 0 calls, 0 results

 Tokens
 Input: 100
 Output: 20
 Total: 120

 Cache Warming
 Mode: streaming
 Status: Inactive (cache lifetime unavailable)
```

`File:` reads `In-memory` under `--no-session`. Pi 1.1.0 showed an identical screen, and Pi 1.4.0 did not change `/session`.

**`/name <text>`** sets the session's display name, shown in the footer and in `/resume`:

```text
/name Notes review
```

```text
 Session name set: Notes review
```

The footer line changes from `…/sc • demo` to `…/sc • Notes review`. Pi: `interactive-mode.ts:3253`; PiG: `internal/codingagent/slash_session_handlers.go:81`.

**`/copy`** copies the last assistant message to your clipboard. Pi: `interactive-mode.ts:3248`.

```text
/copy
```

```text
 Copied last agent message to clipboard
```

When the terminal or system has no clipboard, PiG shows the clipboard error instead. `Ctrl+X` copies the selection or the last message without a command.

Over SSH or Mosh, PiG also sends the copied text to your local terminal with OSC 52. Set `PI_CLIPBOARD_OSC52=1` to do the same when PiG cannot detect the remote session, such as in a multiplexer attached from another machine. This variable is new in Pi 1.3.0. See [Environment variables](/docs/latest/environment-variables).

**`/hotkeys`** prints every key binding as tables (navigation, editing, other), taken from the keybindings in force, including your own. Pi: `interactive-mode.ts:3268`. Change a binding in `keybindings.json`: [Keybindings](/docs/latest/keybindings).

**`/changelog`** prints every changelog entry in the changelog file, newest first, as formatted text. It is long: scroll your terminal to read it. Pi: `interactive-mode.ts:6786-6795`.

**`/debug`** writes a debug log to the agent directory and shows its path. The log holds every line PiG drew with its width and every message of the conversation as JSON, so it contains whatever the conversation contains: treat it like a session file before you attach it anywhere. Pi: `interactive-mode.ts:3320` and `:6945-6963`.

```text
/debug
```

```text
 ✓ Debug log written
 /home/you/.pig/agent/pig-debug.log
```

### Start, leave, resume

**`/new`** starts an empty session in the same folder. The previous session stays on disk. Pi: `interactive-mode.ts:3304`.

```text
 ✓ New session started
```

The screen clears to the startup header, and the footer token counts return to `0.0%`.

**`/resume`** opens a picker of saved sessions for this folder, newest first. `Tab` switches between "Current Folder" and "All", `ctrl+n` shows only named sessions, `ctrl+r` renames and `ctrl+d` deletes the highlighted one. `Enter` opens it. Pi: `interactive-mode.ts:3335`.

```text
Resume Session (Current Folder)                  ◉ Current Folder | ○ All  Name: All  Sort: Threaded
tab scope · re:<pattern> regex · "phrase" exact
ctrl+s sort · ctrl+n named · ctrl+d delete · ctrl+p path (off) · ctrl+r rename
>
› demo                                                                                         3 now
```

**`/fork`** lists your earlier messages. Pick one and PiG creates a new session that holds everything **before** it, puts that message back in the editor and switches to the new session:

```text
 Fork from Message
 Select a user message to copy the active path up to that point into a new session
› What is in notes.txt?
  Message 1 of 1
```

After `Enter`:

```text
 Forked to new session
```

```text
────────────────────────────────────────────────────
What is in notes.txt?
────────────────────────────────────────────────────
```

Edit the message and send it to try a different question from the same point. The original session is untouched. Pi: `interactive-mode.ts:3273` and `:1994`.

**`/clone`** duplicates the active branch up to the current position and switches to the copy. It needs a saved session (at least one message). Pi: `interactive-mode.ts:3278` and `:5553`.

```text
 Cloned to new session
```

**`/tree`** shows the session as a tree, with every branch:

```text
   Session Tree
  ↑/↓ move · ←/→ page · ctrl+←/→ branch · ctrl+x copy · shift+l label · shift+t label time
  filters ctrl+d/t/u/l/a · cycle ctrl+o/shift+ctrl+o
  Type to search:
  • [system]
  • user: What is in notes.txt?
› • assistant: Here is a short answer about notes.txt.
  (3/3)
```

Move to an earlier entry and press `Enter` to continue from that point in the **same** session. PiG asks how to treat the branch you leave:

```text
 Summarize branch?
 → No summary
   Summarize
   Summarize with custom prompt
```

`Summarize` writes a short summary of the abandoned branch into the new one so the model keeps the lessons. `/tree` and `/fork` change the conversation only. They never change your files. Pi: `interactive-mode.ts:3283`; settings key `branchSummary.skipPrompt` skips the question (`settings-schema.ts:36`).

**`/quit`** exits PiG. It prints the same resume line as `Ctrl+D`:

```text
To resume this session: pig --session 01a12bb7-80df-773c-8928-031c941bb0df
```

### Choose a model and thinking level

**`/model [search]`** opens the model selector. Typing after the command filters the list. `Enter` selects the model for this session. `Ctrl+S` also saves it as the default. Pi: `interactive-mode.ts:3215`.

```text
/model mock
```

```text
 Refreshing model catalogs…
Only showing models from configured providers. Use /login to add providers.
> mock
→ ✓ mock-1 [mock]
  Model Name: Mock model
  Model catalogs refreshed.
  Enter to select · Ctrl+S to set as default · Escape/Ctrl+C to cancel
```

The `✓` marks the model in use. Only models of providers you have signed in to are listed (Pi: `components/model-selector.ts:110`; PiG: `tui/model_select.go:248`). `Ctrl+L` opens the same selector.

**`/thinking [level]`** sets how hard a reasoning model thinks, or opens a selector when you give no level. Pi: `interactive-mode.ts:3221`. A model that cannot think rejects a level and lists what it accepts:

```text
/thinking high
```

```text
 Error: Unknown thinking level "high". Available levels: off.
```

On a reasoning model the same command changes the level, and the footer shows it next to the model name: `claude-sonnet-4-5 • high`, or `claude-sonnet-4-5 • thinking off` when it is off (Pi: `components/footer.ts:236-238`). `Shift+Tab` cycles the levels.

**`/scoped-models`** chooses the models `Ctrl+P` cycles through:

```text
Model Configuration
Session-only. Ctrl+S to save to settings.
>
→ ✓ mock-1 [mock]
  Model Name: Mock model
  Enter toggle · Ctrl+A all · Ctrl+X clear · Ctrl+P provider · Alt+Up/Alt+Down reorder · Ctrl+S save
· all enabled
```

`Enter` toggles one model, `Ctrl+A` enables all, `Ctrl+X` clears, and `Ctrl+S` saves the choice to settings (`enabledModels`). Pi: `interactive-mode.ts:3210`.

### Sign in and out

**`/login [provider]`** opens the sign-in menu. Give a provider to skip the first menu. See [Your first ten minutes](/docs/latest/first-run#2-choose-a-model) for every screen. The API-key prompt masks what you type (D80).

**`/logout`** removes credentials that `/login` stored. When there are none:

```text
 No stored credentials to remove. /logout only removes credentials saved by /login; environment
 variables and models.json config are unchanged.
```

Pi: `interactive-mode.ts:6033`; PiG: `internal/codingagent/slash_auth.go:104`. A key from an environment variable or `models.json` stays, so remove it there.

### Settings and trust

**`/settings`** opens the settings list. Type to search. `Enter` or `Space` changes the highlighted value. `Esc` closes it. A change applies at once and is saved to `settings.json`. Pi: `interactive-mode.ts:3205`. See [Settings](/docs/latest/settings).

```text
→ Auto-compact                      true
  Auto-resize images                true
  Block images                      false
  Skill commands                    true
  Show hardware cursor              false
  Editor padding                    0
  Output padding                    1
  Autocomplete max items            5
  Clear on shrink                   false
  Terminal progress                 false
  (1/33)
  Automatically compact context when it gets too large
  Type to search · Enter/Space to change · Esc to cancel
```

**`/trust`** records whether PiG may load this project's own settings, extensions and Skills. The screen shows the decision saved earlier and the state of this session separately. Pi: `interactive-mode.ts:3288`; see [Security](/docs/latest/security).

```text
 Project trust
 /home/you/project
 Saved decision: none
 Current session: trusted
 →   Trust
     Trust parent folder (/home/you)
     Do not trust
 ↑↓ navigate  enter save  escape/ctrl+c cancel
```

Restart PiG after you change the decision so startup runs under it.

### Context

**`/compact [instructions]`** replaces the older conversation with a summary and keeps the most recent part (`compaction.keepRecentTokens`, default 20,000 tokens). Words after the command tell the summariser what to keep. Pi: `interactive-mode.ts:3309`.

A short session has nothing to summarise:

```text
/compact
```

```text
 Error: Compaction failed: Nothing to compact (session too small)
```

(Pi: `core/agent-session.ts:2812`.) In a longer session:

```text
/compact focus on the decisions
```

```text
 [compaction]
 Compacted from 120 tokens (ctrl+o to expand)
```

`Ctrl+O` shows the summary. The footer's context figure restarts from the summary's size. See [Compaction](/docs/latest/compaction).

**`/reload`** re-reads keybindings, extensions, skills, prompt templates, themes and context files without leaving PiG. Use it after you edit one of them. Pi: `interactive-mode.ts:3315`.

```text
 Reloaded keybindings, extensions, skills, prompts, themes, and context files
```

**`/reload --explain`** [pig] adds a report of what was loaded. Pi has no such option. It lists the counts of context files, skills, prompts, extensions and themes, the time the subprocess reload took and a note on how extensions reload (`internal/codingagent/slash_session_handlers.go:919`):

```text
 Reloaded keybindings, extensions, skills, prompts, themes, and context files
 Reload explanation:
   • context files: 0
   • skills: 0
   • prompts: 0
   • extensions: 5
   • themes: 3
   • subprocess reload wall: 0s
   • each extension reloads on its own: replacements start and register beside the old processes;
 an extension that fails to load is dropped and listed under Extension issues
```

### Save and share

**`/export [path]`** writes the session to a file. The default is an HTML page you can open in a browser. A `.jsonl` path writes the session's JSON Lines file. PiG uses the path exactly as you type it. It adds no `.html` and creates no folders. Pi: `interactive-mode.ts:3227` and `:6526`.

```text
/export /home/you/notes-review.html
```

```text
 Session exported to: /home/you/notes-review.html
```

The HTML file is self-contained (about 290 KB for a two-message session) and includes the conversation and tool output. Check it before you send it to anyone. A `.jsonl` export begins with the session header:

```text
{"type":"session","version":3,"id":"01a12bc2-c9d0-7548-b44b-94f81cc45e3c","timestamp":"2026-10-11T16:19:13.506Z","cwd":"/home/you/project"}
{"type":"session_info","id":"6c6e2433","parentId":null,"timestamp":"2026-10-11T16:19:08.368Z","name":"demo"}
{"type":"model_change","id":"05336c4e","parentId":"6c6e2433","timestamp":"2026-10-11T16:19:08.369Z","provider":"mock","modelId":"mock-1"}
```

The format is in [Session format](/docs/latest/session-format).

**`/import <path.jsonl>`** loads a session file. PiG asks before it replaces the current session. Without a path:

```text
 Error: Usage: /import <path.jsonl>
```

With a path:

```text
 Import session
 Replace current session with /home/you/notes-review.jsonl?
 → Yes
   No
```

After `Yes`: `Session imported from: /home/you/notes-review.jsonl`. Pi: `interactive-mode.ts:3232`, `:6570`. PiG never overwrites a stored session: an imported file takes the first free `name-N` name ([Sessions](/docs/latest/sessions)).

**`/share`** uploads the session so that anyone with the link can read it. This differs from Pi, whose `/share` makes a secret GitHub gist or an organization artifact. PiG uploads an unlisted artifact to `pi-in-go.dev` that expires after 30 days (D64). PiG prints a notice first and the link when the upload ends (`internal/codingagent/session_share.go:226`):

```text
Privacy: /share uploads the current session to pi-in-go.dev, including prompts, model responses,
tool calls, and tool output. Anyone with the link can read it. The service deletes it after 30 days.
```

```text
Share URL: https://pi-in-go.dev/...
```

Use `/export` instead when the session holds anything private. When the upload fails, PiG says so (for example `Error: Failed to upload session: …`) and nothing is shared.

**`/bug [description]`** writes a bug report as a zip file in the current folder and prints a link that opens a prefilled PiG issue. It never uploads anything. Pi's `/bug` can upload the report to a gateway, so this is a difference (D62). With a description on the command line, PiG goes straight to the transcript question:

```text
/bug the footer shows the wrong model
```

```text
 Include the session transcript?
 The transcript contains your messages, model output, tool calls and their results, including file
 contents and command output read during this session.
 → Yes, include the transcript
   No
```

```text
 Bug report
 Description: the footer shows the wrong model
 Transcript: included
 Summary: none
 Export writes a zip archive to the current directory. Nothing is uploaded; you can attach the
 archive to a PiG issue.
 → Export as Zip
   Cancel
```

```text
 Bug report exported to:
 /home/you/project/pig-bug-report-01a12bca-2b63-7109-98f6-d549300404d0.zip
 Report ID: 01a12bca-2b63-7109-98f6-d549300404d0
To report it, open a PiG issue and attach pig-bug-report-01a12bca-2b63-7109-98f6-d549300404d0.zip:
https://github.com/MichaelKinsy/PiG/issues/new?template=bug.yml&title=bug%3A+the+footer+shows+the+wrong+model&version=...
```

The link carries the title, PiG version, platform and report ID, and none of the session's content. Pick `No` at the transcript question to leave the conversation out of the archive. The archive holds your PiG version, operating system, model and provider settings without API keys, loaded extensions, settings and provider error details.

### PiG-only commands

**`/sprite [list|set <id>|preview [id]|create]`** chooses the pig in the startup header. The command comes from the built-in `pig-login` extension, so `--no-extensions` leaves it out (D2). `/sprite list` prints the sprites:

```text
 pig-default: PiG: The minimal coding agent, in Go.
 pink: Classic PiG: Pink, polite, and patient.
 green: Green PiG: Ready to build.
 mint: Mint PiG: Refactors before breakfast.
 sandy: Sandy PiG: All tabs, no spaces.
 grey: Slate PiG: Reads RFCs for fun.
 blush: Rosy PiG: Compiles with feelings.
 lavender: Lavender PiG: Dreams in async.
 cloud: Cloud PiG: Lives at 99.99% uptime.
 pigrogu: PiGrogu: Passing tests, this is the way.
 darth-vader: Darth Vader: I find your lack of tests disturbing.
 kratos: Kratos: War on bugs.
 piglet: Piglet: A Very Small Animal with very large diffs.
 spider-ham: Spider-Ham: Does whatever a spider can.
 sheriff: Sheriff PiG: Laying down the law, one commit at a time.
```

`/sprite set kratos` chooses one. `/sprite` alone opens the picker, and `/sprite preview kratos` shows its full art until you press a key.

**`/llama`** manages a local llama.cpp server. Before you configure llama.cpp it says:

```text
 Warning: Configure llama.cpp with /login llama.cpp
```

See [Run local models](/docs/latest/llama-cpp).

**`/piglet`** exists only in a session started with `--piglet`. In Stock PiG, with no Piglet, the text is sent to the model like any other message, because it is not a command there. The same holds for `/exit` and `/clear`: PiG has neither, as Pi has neither, and the line goes to the model:

```text
 /piglet
 Here is a short answer about notes.txt.
```

### Typing a command

- A line that starts with `/` and names a command runs the command. Any other `/…` line is an ordinary message.
- `Tab` completes a command name and then its arguments (for `/model`, the model; for `/login`, the provider). See [Using PiG](/docs/latest/usage#autocomplete).
- Skills appear as `/skill:<name>` and prompt templates as `/<name>`. See [Skills](/docs/latest/skills) and [Prompt templates](/docs/latest/prompt-templates).
