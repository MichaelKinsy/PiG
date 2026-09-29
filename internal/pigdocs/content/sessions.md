# Sessions

A session is one conversation with its full history. PiG saves its log after the first assistant response. Use `--no-session` to keep the Session log in memory.

Every Session has an ID, including print (`-p`), JSON, RPC, and `--no-session` runs. A new Session gets a UUIDv7 unless you supply `--session-id`. Extensions can read the ID before the first model response. `--no-session` disables Session file persistence; it does not remove the ID or prevent an extension from keeping its own session-keyed data.

## Where sessions are stored

Sessions live under `~/.pig/agent/sessions/`, in a directory per project. The
directory name is the project path with separators replaced, wrapped in `--`, so
sessions for different projects never mix.

Each session is one JSON Lines file: one entry per line, appended as the session
runs. A crash costs at most the last line, because nothing is rewritten.

Set `sessionDir` in [settings](settings.md) to store sessions elsewhere.

## Working with sessions

| Command | What it does |
|---|---|
| `/new` | Start a session with no history |
| `/resume` | Open the session picker |
| `/tree` | Show the session as a tree and move to any point |
| `/fork` | Continue from the current point in a new session |
| `/clone` | Copy the session |
| `/name` | Give the session a name |
| `/export` | Write the session to a file |
| `/share` | Upload the session to PiG's unlisted, expiring share service. |

## Importing and exporting

`/import <path>` asks before replacing the active Session. Relative paths start at the process working directory. Importing an external file never overwrites a stored Session: PiG uses the first free `name-N` suffix. A file already in the Session directory opens in place. If its working directory is missing, PiG offers the current directory.

HTML export requires a Session file that has been written. `/export` uses the first path argument, with optional quotes, exactly as supplied. It does not append `.html` or create parent directories. Use a `.jsonl` path to export the current branch as JSON Lines.

After `/clone`, `/new`, or `/resume`, the transcript and footer reflect the current Session. Resuming an unnamed Session clears the previous name.

## Sharing a session

Run `/share` to upload the active branch, including an in-memory Session, to `https://pi-in-go.dev` (D64). PiG shows a privacy notice before it uploads. The artifact includes prompts, model responses, tool calls, tool output, the effective system prompt, and active tool schemas.

A share is unlisted, not private. Anyone with its URL can read and download it. The service accepts artifacts up to 8 MiB and deletes them after 30 days. Press `Escape` while the upload indicator is open to cancel the request. PiG never uploads a Session automatically. Use `/export` instead when the Session must stay local.

## The session tree

`/tree` shows the session as its entries. Move to an entry and continue from
there. Earlier work stays; the new turns branch from the point you chose.

Use this after a wrong turn. Rather than telling the model to forget, move above
the mistake and continue, so the failed attempt never reaches the model again.

Search and filter changes keep the selected entry when it remains visible. Otherwise, selection moves to its nearest visible ancestor, or the last visible entry if no ancestor matches. An empty result retains the selection for the next filter change. Search and filter changes clear folded branches. Press the active non-default filter key again to return to the default view.

Filter the tree when it grows. `ctrl+u` shows your messages only, `ctrl+t` hides
tool results, and `ctrl+l` shows labeled entries. See [keybindings](keybindings.md).

## Naming and finding

An unnamed session is identified by its time and first message, which is hard to
recognize later. `/name` gives it a name, and `ctrl+n` in the session list filters
to named sessions.

Run `pig -r` or `/resume` to open the session picker. Search matches the session ID, name, user and assistant message text, and working directory. Separate words must all match. Unquoted words use fuzzy matching; double quotes match a phrase with whitespace normalized. Use `re:<pattern>` for a case-insensitive regular expression.

The `/resume` picker includes the current Session when its file is saved and matches the selected scope and filters. Its name or first-message preview uses the accent color. Selecting it reloads the same Session and reports `Resumed session`. You cannot delete the active Session from the picker.

Press `ctrl+s` to cycle Threaded, Recent, and Fuzzy order. A nonempty search uses match quality in Threaded and Fuzzy modes, with newer activity breaking score ties. Recent mode keeps the incoming session order. The picker starts on the first row. Editing the search retains the selected row index and clamps it when results shrink. Press `Enter` to resume the highlighted session.

After successful deletion, the row disappears before the Session list refresh starts. Other rows remain selectable during the refresh; the deleted row does not. Deletion first runs the `trash` command when it is on `PATH`. If `trash` succeeds or the file is already gone, PiG reports `Session moved to trash`. Otherwise PiG attempts permanent deletion and reports `Session deleted` on success or an error on failure. On Windows only `trash.com` or `trash.exe` counts, as in Pi.

## What a session keeps

The file keeps every entry: your messages, the model's replies, every tool call
and its result, and each compaction. It is the complete record.

The model sees less than the file holds. Compaction replaces older entries with a
summary for the model, and does not change the file. See
[compaction](compaction.md).

## Related

- [Compaction](compaction.md) covers what the model sees as a session grows.
- [Keybindings](keybindings.md) lists the tree and session-list keys.
- [Commands](commands.md) lists every session command.
