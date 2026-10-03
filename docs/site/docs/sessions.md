# Sessions

A session is one conversation with its full history. By default, PiG saves its log from the first user message. Use `--no-session` to keep the Session log in memory.

Every Session has an ID, including print (`-p`), JSON, RPC, and `--no-session` runs. A new Session gets a UUIDv7 unless you supply `--session-id`. Extensions can read the ID before the first model response. `--no-session` disables Session file persistence; it does not remove the ID or prevent an extension from keeping its own session-keyed data.

## Where sessions are stored

Sessions live under `~/.pig/agent/sessions/`, in a directory per project. The
directory name is the project path with separators replaced, wrapped in `--`, so
sessions for different projects never mix.

Each persisted session uses a JSON Lines file. PiG selects its path when the Session is created, but the file does not exist until the session holds a user or assistant message. Setup entries alone, such as a model or thinking-level change, do not create it.

Set `sessionDir` in [settings](/docs/latest/settings) to store sessions elsewhere.

PiG saves a new session when the first user message is added, so the prompt stays on disk if the first turn never finishes. On exit, the resume hint uses `pig --session <id>`. It adds `--session-dir` only when the session directory differs from the default for the working directory, including after `-c`, `-r`, or `--session`.

If `--session <id-or-prefix>` or `--fork <id-or-prefix>` finds no matching session, PiG writes `No session found matching '<id-or-prefix>'` to standard error and exits with status 1. It writes nothing to standard output and does not start a model request.

Use `--session-id <id>` to reopen an exact ID in the current project or create a Session with that ID. Lookup reads Session headers without building full transcript listings and still finds renamed files. PiG warns when it creates a missing ID. Metadata commands such as `--help` do not persist it. With `--fork`, the ID names the new fork; an existing target ID is rejected before the source is forked.

Use `--name <name>` to set the selected Session's initial name. PiG applies it before validating the requested model, so a later model-selection failure does not undo a saved Session's name. RPC exposes the initial name without emitting a runtime name-change event.

## Working with sessions

| Command | What it does |
|---|---|
| `/new` | Start a session with no history |
| `/resume` | Open the session picker |
| `/tree` | Show the session as a tree and move to any point |
| `/fork` | Select a user message and continue from before it in a new session |
| `/clone` | Duplicate the active branch at the current position and switch to the copy |
| `/name` | Give the session a name |
| `/export` | Write the session to a file |
| `/share` | Upload the session to PiG's unlisted, expiring share service. |

## Cloning and forking

Send a message before cloning a file-backed session. If the session is not saved yet, `/clone` reports an error and creates no file. A successful clone clears the editor and reports `Cloned to new session`.

If no user messages exist, `/fork` reports `No messages to fork from`. Cancelling the message selector leaves the session unchanged and emits no status message.

## Resuming after exit

On an interactive quit, PiG prints a resume command only when stdout is a terminal and the Session has an existing file. In-memory Sessions do not produce a resume command. A custom Session directory appears as `--session-dir` with shell quoting when needed.

## Fork storage

Forks and clones of an in-memory Session stay in memory. A disk-backed fork with no retained user or assistant message selects a new path but defers creating the file until the next user or assistant message. Forking before the first user message can retain native system context even though it retains no user or assistant turns.

## Importing and exporting

`/import <path>` asks before replacing the active Session. Relative paths start at the process working directory. Importing an external file never overwrites a stored Session: PiG uses the first free `name-N` suffix. A file already in the Session directory opens in place. If its working directory is missing, PiG offers the current directory.

HTML export requires a Session file that has been written. `/export` uses the first path argument, with optional quotes, exactly as supplied. It does not append `.html` or create parent directories. Use a `.jsonl` path to export the current branch as JSON Lines.

After `/fork`, `/clone`, `/new`, or `/resume`, the transcript and footer reflect the current Session. A fork or clone retains only names stored in its copied branch. An unnamed destination clears the previous footer name.

## Sharing a session

Run `/share` to upload the active branch, including an in-memory Session, to `https://pi-in-go.dev` (D64). PiG shows a privacy notice before it uploads. The artifact includes prompts, model responses, tool calls, tool output, the effective system prompt, and active tool schemas.

A share is unlisted, not private. Anyone with its URL can read and download it. The service accepts artifacts up to 8 MiB and deletes them after 30 days. Press `Escape` while the upload indicator is open to cancel the request. PiG never uploads a Session automatically. Use `/export` instead when the Session must stay local.

## The session tree

`/tree` shows the session as its entries. Move to an entry and continue from
there. Earlier work stays; the new turns branch from the point you chose.

Use this after a wrong turn. Rather than telling the model to forget, move above
the mistake and continue, so the failed attempt never reaches the model again.

The active branch appears first. A `•` marks each entry on the path to the active leaf, not every branch. Type words or digits to search. Filtering reconnects each matching entry to its nearest visible ancestor. Press Escape to clear a search before closing the tree.

Search and filter changes keep the selected entry when it remains visible. Otherwise, selection moves to its nearest visible ancestor, or the last visible entry if no ancestor matches. An empty result retains the selection for the next filter change. Search and filter changes clear folded branches. Press the active non-default filter key again to return to the default view.

Filter the tree when it grows. `ctrl+u` shows your messages only, `ctrl+t` hides
tool results, and `ctrl+l` shows labeled entries. See [keybindings](/docs/latest/keybindings).

## Naming and finding

Session names replace each run of carriage returns or line feeds with a space and trim surrounding whitespace before persistence. Name-change subscribers receive the resulting name before the setter returns; suspended extension notifications do not block the setter.

An unnamed session is identified by its time and first message, which is hard to
recognize later. `/name` gives it a name, and `ctrl+n` in the session list filters
to named sessions.

Run `pig -r` or `/resume` to open the session picker. Search matches the session ID, name, user and assistant message text, and working directory. Separate words must all match. Unquoted words use fuzzy matching; double quotes match a phrase with whitespace normalized. Use `re:<pattern>` for a case-insensitive regular expression.

The `/resume` picker includes the current Session when its file is saved and matches the selected scope and filters. Its name or first-message preview uses the accent color. Selecting it reloads the same Session and reports `Resumed session`. `Ctrl+D` reports that it cannot delete the active Session, including when the file is reached through a symlink alias. For another Session, `Ctrl+D` opens deletion confirmation even with a search query. `Ctrl+Backspace` opens confirmation only when the query is empty.

Press `ctrl+s` to cycle Threaded, Recent, and Fuzzy order. Threaded mode orders roots and siblings by the newest activity anywhere in each subtree. A nonempty search uses match quality in Threaded and Fuzzy modes, with newer activity breaking score ties. Recent mode keeps the incoming session order. The picker starts on the first row. Editing the search retains the selected row index and clamps it when results shrink. Press `Enter` to resume the highlighted session.

After successful deletion, the row disappears before the Session list refresh starts. Other rows remain selectable during the refresh; the deleted row does not. Deletion first runs the `trash` command when it is on `PATH`. If `trash` succeeds or the file is already gone, PiG reports `Session moved to trash`. Otherwise PiG attempts permanent deletion and reports `Session deleted` on success or an error on failure. On Windows only `trash.com` or `trash.exe` counts, as in Pi.

In the interactive `/resume` picker, press `ctrl+r` to rename the selected Session. The first rename starts with the cursor before the existing name. Later visits retain the rename cursor, clamped to the new name's length. Press `Enter` to save a nonempty name or `Escape` to cancel. An empty submission keeps the rename field open. The startup `--resume` picker does not offer renaming or show the rename hint.

## What a session keeps

The file keeps every entry: your messages, the model's replies, every tool call
and its result, and each compaction. It is the complete record.

The model sees less than the file holds. Compaction replaces older entries with a
summary for the model, and does not change the file. See
[compaction](/docs/latest/compaction).

## Related

- [Compaction](/docs/latest/compaction) covers what the model sees as a session grows.
- [Keybindings](/docs/latest/keybindings) lists the tree and session-list keys.
- [Slash commands](/docs/latest/slash-commands) lists every Session command.
