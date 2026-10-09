# durable/core/history

The transcript half of the Durable core: entries, fork ancestry, head markers, context edits, compaction ranges and reset, and the context derivation they feed. It is one Go source that TinyGo (`-target=wasip1 -scheduler=none`), standard Go wasip1 and native Go build alike: no reflection, no `encoding/json`, no goroutines, no I/O, no clock, no package-level mutable state. `build-check.sh` builds and tests all three.

Contract: [CONTRACT.md](../../../docs/plan/durable-core/CONTRACT.md) sections 2.3, 3.3, 3.7 and 5.6, [ADR-0001](../../../docs/plan/durable-core/ADR-0001-core-architecture.md) D4, D5 and D14. Reference: pi-durable main `da866ada` (`src/harness/context.ts`, `src/harness/compaction.ts`, `src/harness/inbox.ts`, `src/session/forks.ts`, `#appendEntry` and `#stageConversation` in `src/session/transaction.ts`). Stores written by pi-durable 1.0.4 read unchanged: the scanner tolerates unknown fields, unknown entry kinds, any key order and whitespace.

## What is here

| file | owns |
|---|---|
| `jscan.go` | structural JSON scanner (validating, no decoded values), WTF-8 string decoding, UTF-16 lengths |
| `jenc.go` | JSON.stringify-faithful encoder: `Canonical` (parse then stringify: key order with integer keys first, duplicate keys, ECMAScript numbers, well-formed escapes), `AppendNumber` |
| `jsstr.go` | JavaScript string concatenation, `slice` by UTF-16 unit and `trim` on WTF-8 |
| `chain.go` | one conversation's entries: stored bytes in an append-only arena beside a fixed-width index (entry, message, tool-call-id spans, head markers, edits) |
| `store.go`, `context.go` | the Session's conversations, the cold-read protocol (`Need`), `Store.Context` (derivation with an incremental cache) |
| `derive.go` | spec section 2.1 rules 4-10: edits, contributions, excluded stop reasons, tool result ordering, synthesized results, system message leading |
| `tokens.go` | pi-ai `estimateMessageTokens` and `calculateContextTokens` over message bytes |
| `compaction.go`, `summary.go`, `checkpoint.go` | range selection (`SelectCut`), thresholds, `EstimateContext`, summary prompt and entry draft, `pi.compaction` checkpoint codec |
| `compactplan.go` | the pi.compaction task's decisions as pure functions: `PlanSelect`, the `beforeCompact` payload, summary stream options, `DecideSummary`, usage key, task input, request id |
| `draft.go` | `AppendEntryRecord` (the stored record of an entry), reset draft, stale head write rule |
| `rows.go` | the row writes of `conversations` and `entries` and their record id claims |
| `fork.go` | `VisibleEntry`, `NewFork`, `PrepareForkDocumentCopies` |
| `sql.go` | the three statements that answer a `Need` |

## Seams

**To the Session (dcore-session).** Commit assembly calls `AppendEntryRecord(draft, id, conv, task)` where Pi calls `#appendEntry`, and after the commit is applied `Store.Append(conv, id, seq, record)`. A conversation creation calls `Store.AddConversation(rec, true)` (`AppendConversation` writes the stored record), a fork `Store.NewFork` after `VisibleEntry` found the fork point; document copies come from `PrepareForkDocumentCopies` with a consumer-owned `DocScanner`, minting through the Session's id counter in Pi's order (conversation id first, then one id per copy in scan order, before the duplicate check). The inbox boundary calls `DraftIsReset` and `IsStaleHeadWrite`; `AppendResetDraft` builds `Conversation.reset`'s write.

**To the loop (dcore-loop).** `Store.Context(conv, at)` returns a `View`: head marker, active entries with kind and seq, per-entry contributions after edits, and the ordered model messages, all as references into the arena. `View.Message(i)` is the stored JSON of message `i`; the loop splices it into the `model_context` effect without decoding. A view is valid until the next call that changes the store or asks for another context of the same conversation. Prompt planning reads `View.Message`/`EntryKind` (replaying sections and tools is the loop's). Compaction is asked through `SelectCut`, `SummarizedMessages`, `AppendSummaryContext`, `SummaryText`, `SummaryFailure`, `AppendSummaryEntryDraft`, `ThresholdCompaction`, `EstimateContext`; the task state machine (phases, commits, hooks, retries, live-document status, usage) stays with the scheduler: it calls `PlanSelect` in `select`, `AppendSummaryContext` and `AppendSummaryOptions` in `summarize`, `DecideSummary` on the answer, `AppendSummaryEntryDraft` to place it, and stores `Checkpoint` (`AppendCheckpoint`, `ParseCheckpoint`, `NextAttempt`). The ids, retry verdict (`isRetryableAssistantError`, `retryDelayMs`), usage ledger and provider session id are the caller's inputs.

**To the host (ABI reads).** A derivation that lacks rows returns a `Need`. The core turns it into a `read` of `Need.SQL()` with `Need.Params()` and calls `BeginLoad` and `AddRow` (`NeedEntries`), `SupplyBounds` (`NeedBounds`) or `AddConversation(rec, false)` (`NeedConversation`) with the rows, then calls `Context` again. A cold open of a conversation with a head marker reads the conversation row, one bounds row and the active range (ADR D5); a fork reads the parent range through `parent.at` the same way. `Store.Drop` forgets a conversation (hibernation is a cache drop).

**To the sidecar (ADR D6).** `BeginLoad`/`AddRow` take any source of stored record bytes; a sidecar that stores the index can fill the chain without scanning (not implemented).

## Evidence

`oracle/` regenerates the checked-in corpora in `testdata/` from pi-durable main: `gen_context.mjs` builds random conversations (forks, head markers, edits, tool rounds with missing, duplicate and out-of-order results, excluded stop reasons, truncation boundaries) in pi-durable's own `MemoryStorage`, derives with its `deriveContext`, `selectCut`, `estimateContext`, `serializeConversation` and `summaryPrompt`, and writes the replay script and a hash of each expected output; `gen_summary.mjs` covers `summaryText`, `summaryFailure`, the entry record formula, the summary request and entry draft, reset drafts and checkpoint spreads; `gen_canon.mjs` covers `JSON.stringify(JSON.parse(text))`. `gen_real.mjs` runs a real Harness (faux provider, tool rounds with parallel calls, manual compactions, forks, a reset, edits written through submissions, an error answer) and dumps the rows the Session wrote with the contexts `Conversation.context()` returned, plus the summarization request and placed entry of each compaction run (`W` lines); `PI_MAIN` pointed at a pi-durable 1.0.4 tree (`NO_AT=1 NO_COMPACTION=1 LEAD_SYSTEM=1`, see the script) produces `real104.hex`, rows written by 1.0.4 that must derive the same contexts (1.0.4 predates rule 10, range selection and the context estimate; main's are the target). Run them with `node --import ./oracle/pimain.mjs oracle/gen_*.mjs` (`PI_MAIN` names the pi-durable main checkout). The Go tests replay the scripts warm (full load, then append and derive after each entry, which exercises the incremental cache) and cold (a fresh store answered by an in-memory host through the `Need` protocol) and require equal bytes.

## Measurements

Index memory is about 56 bytes per entry for a tool-calling transcript (32 for the entry, 20 per message, 8 per tool call id), 0.65 MB at 11,669 entries: above ADR H-D4's 32-byte hypothesis, which counted the entry alone. A warm turn at 3,500 entries of history (eight appends, a derivation after each) takes about 27 us and 32 allocations; a cold open of 3,500 entries takes about 11 ms natively, and a conversation with a head marker reads a constant number of rows at any history length (`TestColdOpenReadsOnlyTheActiveRange`). `FuzzCanonical` and `FuzzScanRecord` run with `-parallel=4 -fuzztime=30s`.
