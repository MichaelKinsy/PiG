# Compaction

A model has a fixed context window. A long session eventually fills it.
Compaction replaces the older part of the conversation with a summary, so the
session continues instead of failing.

## When Pig compacts

Pig compacts when the context in use passes the window minus a reserve:

```
contextTokens > contextWindow - reserveTokens
```

The reserve leaves room for the next prompt and its answer. Without it, Pig would
compact only after a request was already too large to send.

Pig does not compact when compaction is off, or when it does not know the model's
context window.

## Settings

| Key | Default | Meaning |
|---|---|---|
| `compaction.enabled` | `true` | Compact automatically |
| `compaction.reserveTokens` | `16384` | Tokens held back from the window |
| `compaction.keepRecentTokens` | `20000` | Recent conversation kept in full |

Set them in [settings](settings.md):

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}
```

Raise `reserveTokens` if requests still fail as too large. Raise
`keepRecentTokens` to keep more recent detail, which compacts more often.

## What is kept

Pig keeps the most recent `keepRecentTokens` of conversation in full and
summarizes what comes before it.

Pig cuts only where a cut is valid. A tool result must follow its tool call, so
Pig never cuts between them. Valid cut points are user messages, assistant
messages, shell executions, branch summaries, custom messages and earlier
compactions.

## Compacting by hand

Use `/compact` to compact now, before the window fills. This is useful when you
finish one task and start another in the same session: the summary keeps the
outcome and drops the intermediate steps.

## What you lose

A summary is smaller than what it replaces. Exact wording, full file contents and
individual tool output do not survive it. The session file keeps every entry, so
nothing is lost on disk. Only what the model sees is reduced. See
[sessions](sessions.md).

Start a new session rather than compacting when the next task shares nothing with
the last one. A summary of unrelated work costs context and adds no value.

## Related

- [Settings](settings.md) lists every compaction key.
- [Sessions](sessions.md) covers what the session file keeps.
- [Commands](commands.md) lists `/compact`.
