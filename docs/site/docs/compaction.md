# Compaction

A model has a fixed context window. A long session eventually fills it. Compaction replaces the older part of the conversation with a summary, so the session continues instead of failing.

Compaction works as in Pi 1.4.0 ([Pi's compaction reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/compaction.md)), apart from the differences named on this page.

## When PiG compacts

PiG compacts when the context in use passes the window minus a reserve:

```
contextTokens > contextWindow - reserveTokens
```

The reserve leaves room for the next prompt and its answer. Without it, PiG would compact only after a request was already too large to send.

PiG also compacts when the provider rejects a request as too large for the context window, and then retries the turn.

PiG does not compact when compaction is off, or when it does not know the model's context window.

PiG measures `contextTokens` from the usage of the last assistant response plus an estimate of the entries after it. A `context_edit` entry that changes only entries after that response keeps the response's usage as the anchor. Pi 1.4.0 drops the anchor after any `context_edit`, so omitting a failed retry attempt makes the context figure jump to an estimate of the whole history (D136, [Pi #10287](https://github.com/earendil-works/pi/issues/10287)).

## Settings

| Key | Default | Meaning |
|---|---|---|
| `compaction.enabled` | `true` | Compact automatically |
| `compaction.reserveTokens` | `16384` | Tokens held back from the window |
| `compaction.keepRecentTokens` | `20000` | Recent conversation kept in full |
| `compaction.modelOverrides` | none | Per-model `reserveTokens` and `keepRecentTokens`, keyed by exact `provider/modelId` |

Set them in [settings](/docs/latest/settings):

```json
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}
```

Raise `reserveTokens` if requests still fail as too large. Raise `keepRecentTokens` to keep more recent detail, which compacts more often.

## What is kept

PiG keeps the most recent `keepRecentTokens` of conversation in full and summarizes what comes before it.

PiG cuts only where a cut is valid. A tool result must follow its tool call, so PiG never cuts between them. Valid cut points are user messages, assistant messages, shell executions, branch summaries, custom messages and earlier compactions.

## Compacting by hand

Use `/compact` to compact now, before the window fills. This is useful when you finish one task and start another in the same session: the summary keeps the outcome and drops the intermediate steps.

An empty or small session reports `Error: Compaction failed: Nothing to compact (session too small)`. Compaction errors remain in the transcript, with the same one-column padding as other error notices.

Press Escape during a compaction to abort it. When the autocomplete list is open, Escape closes the list and the compaction continues, as in Pi.

In RPC mode, `compact` commands of one input batch run one after another. Pi 1.4.0 starts them at the same time, so a Session with enough history gets two overlapping compactions (D147). A Session replacement in the same batch aborts a running compaction, which answers one error and writes no entry (D148).

## What you lose

A summary is smaller than what it replaces. Exact wording, full file contents and individual tool output do not survive it. The session file keeps every entry, so nothing is lost on disk. Only what the model sees is reduced. See [sessions](/docs/latest/sessions).

Start a new session rather than compacting when the next task shares nothing with the last one. A summary of unrelated work costs context and adds no value.

## Summary format

PiG asks the model for a summary with fixed Markdown sections, so that the next model reads the same structure every time:

| Section | Content |
|---|---|
| `## Goal` | What the user wants to achieve. |
| `## Constraints & Preferences` | Requirements and preferences the user stated, or `(none)`. |
| `## Progress` | Three subsections: `### Done`, `### In Progress` and `### Blocked`. |
| `## Key Decisions` | Each decision with a short reason. |
| `## Next Steps` | An ordered list of what comes next. |
| `## Critical Context` | Data and references needed to continue, or `(none)`. |

The instructions tell the model to keep exact file paths, function names and error messages.

When the session already has a compaction, PiG passes the earlier summary to the model in `<previous-summary>` tags. The model updates that summary instead of starting again, so earlier goals and decisions carry forward.

When the cut falls inside one long turn, PiG summarizes the start of that turn separately, with the sections `## Original Request`, `## Progress So Far` and `## Context Needed to Continue`.

PiG leaves thinking blocks out of the conversation it sends for summarization, as Pi 1.4.0 does, so a message with only thinking adds nothing. Pi 1.1.0 sent every thinking block whole, so a long thinking-only response could make the summary request overflow the model ([Pi #9602](https://github.com/earendil-works/pi/issues/9602)).

When the messages to summarize are longer than the model's context window, PiG splits them into chunks that each fit one summary request and summarizes them in order. Each chunk's request carries the previous chunk's summary in `<previous-summary>`. A history that fits the window is one request, as in Pi. Pi 1.4.0 sends one request that the provider rejects (D167).

PiG then appends the files the summarized part touched, as `<read-files>` and `<modified-files>` lists. A file that was both read and changed appears only under `<modified-files>`. The same two lists are stored as `readFiles` and `modifiedFiles` in the entry's `details`.

The result is a `compaction` entry in the session file with `summary`, `firstKeptEntryId` and `tokensBefore`. See [session file format](/docs/latest/session-format#compaction). When PiG rebuilds the context, the summary becomes a `compactionSummary` message. See [message types](/docs/latest/message-types#compaction-summary).

## Branch summaries

When you move to another point with `/tree`, PiG asks whether to summarize the branch you leave. Choose `No summary`, `Summarize`, or `Summarize with custom prompt`. Set `branchSummary.skipPrompt` to `true` to skip the question and move without a summary.

A branch summary uses the same sections as a compaction summary, without `## Critical Context`. PiG stores it as a `branch_summary` entry and adds the same file lists. When the provider rejects a branch summary request as too large, PiG retries with the newest half of the entries until the provider accepts it or one message is left. Pi 1.4.0 reports the overflow error (D167).

## Extension hooks

Extensions can observe or replace compaction and branch summaries. The events use the same names as in Pi:

| Event | When | What a handler can return |
|---|---|---|
| `session_before_compact` | Before PiG summarizes. The event has `preparation`, `branchEntries`, `customInstructions`, `reason` and `willRetry`. | `{"cancel": true}` to stop the compaction, or `{"compaction": {...}}` with `summary`, `firstKeptEntryId`, `tokensBefore` and optional `details` to use that result without a model call. |
| `session_compact` | After PiG saves the compaction entry. The event has `compactionEntry`, `fromExtension`, `reason` and `willRetry`. | Nothing. |
| `session_compact_failed` | After a compaction fails or is aborted, once the `compaction_end` listeners have returned. The event has `reason`, `errorMessage` (absent when the compaction was aborted), `aborted`, `willRetry` and `fromExtension`. | Nothing. |
| `session_before_tree` | Before `/tree` moves and summarizes. The event has `preparation`. | `{"cancel": true}`, a `summary` object with `summary` and optional `details`, `customInstructions`, `replaceInstructions`, or a `label`. |
| `session_tree` | After the move. The event has `newLeafId`, `oldLeafId`, `summaryEntry` and `fromExtension`. | Nothing. |

`reason` is `manual` for `/compact`, `threshold` when the context passed its limit, and `overflow` when the provider rejected a request as too large.

A `session_compact` handler that adds context with `sendMessage({ triggerTurn: false })` during an overflow compaction has its message in the Session before the interrupted turn retries. Pi 1.4.0 defers the message to the end of the run, so the retry runs without it (D138, [Pi #9051](https://github.com/earendil-works/pi/issues/9051)).

See [extensions](/docs/latest/extensions#session-compaction-events) for the extension API.

## Related

- [Settings](/docs/latest/settings) lists every compaction key.
- [Sessions](/docs/latest/sessions) covers what the session file keeps.
- [Slash commands](/docs/latest/slash-commands) lists `/compact`.
