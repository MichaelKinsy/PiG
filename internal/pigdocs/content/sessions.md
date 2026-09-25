# Sessions

A session is one conversation with its full history. Pig writes every session to
disk as it goes, so you can leave and come back, look at what happened, or start
a new line of work from an earlier point.

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
| `/resume` | Open a different session |
| `/tree` | Show the session as a tree and move to any point |
| `/fork` | Continue from the current point in a new session |
| `/clone` | Copy the session |
| `/name` | Give the session a name |
| `/export` | Write the session to a file |
| `/share` | Upload the session to PiG's unlisted, expiring share service. |

## Sharing a session

Run `/share` to upload the active branch to `https://pi-in-go.dev`. PiG shows a privacy notice before it uploads. The artifact includes prompts, model responses, tool calls, tool output, the effective system prompt, and active tool schemas.

A share is unlisted, not private. Anyone with its URL can read and download it. The service accepts artifacts up to 8 MiB and deletes them after 30 days. Press `Escape` while the upload indicator is open to cancel the request. PiG never uploads a Session automatically. Use `/export` instead when the Session must stay local.

## The session tree

`/tree` shows the session as its entries. Move to an entry and continue from
there. Earlier work stays; the new turns branch from the point you chose.

Use this after a wrong turn. Rather than telling the model to forget, move above
the mistake and continue, so the failed attempt never reaches the model again.

Filter the tree when it grows. `ctrl+u` shows your messages only, `ctrl+t` hides
tool results, and `ctrl+l` shows labeled entries. See [keybindings](keybindings.md).

## Naming and finding

An unnamed session is identified by its time and first message, which is hard to
recognize later. `/name` gives it a name, and `ctrl+n` in the session list filters
to named sessions.

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
