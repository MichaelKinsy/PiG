# Writing p2 plugin code: practices

Rules for anyone (person or agent) who writes or reviews code in `apps/p2`: a plugin's server, worker or contract, and the stores they use. This page is Pi's `apps/p2/docs/plugin-practices.md` (Pi commit `0f1e90696`) with each TypeScript rule restated for the Go port. Each rule names the mistake it prevents. The mistakes come from real reviews of Pi's p2 (Pi PR #11951 among them) and apply to the Go port unchanged, because they concern ownership and wakeups, not syntax. Read this page before you change a plugin's server, worker or contract.

The rules behind it are Pi's `packages/chord/docs/design.md` and Pi's decision "App authoring rules confirmed by the p2 audit" in `packages/chord/docs/decisions.md`. PiG's Chord is `chord/next`. Where a rule here names a Pi file, PiG's port of that file carries a `// Ports` comment that names it.

The Go snippets below are sketches of the shape, not exact signatures. Take the real names from the contract and plugin you are changing.

## 1. Decide who owns each fact before you move it

Every piece of state has exactly one owner: the host and object that is the authority for it. Before you move a field from the server to a session (or back), write down for each field who enforces it.

- **The host that enforces a fact owns it.** The server decides where a session runs (relocation fence, MCP placement, where terminals open, which ports a session may forward) and whether a worker may start (archive). Those facts are the server's, even if a copy also lives in the session.
- **Content belongs to the session.** A title, the model and thinking level, the transcript: the session's worker owns them and reports them.
- **A person's view belongs to the person.** Pinned, read marks, recent: per user, on the server, keyed by user ID.

Mistake: making the session the authority for `cwd`, `env` and `archivedAt` and letting the catalog follow the worker's report. A buggy worker, or a session store edited on disk, can then un-archive the session or change where the server thinks it runs.

## 2. A report carries only what the reporter owns

A worker pushes what the server's index shows (Pi's `p2-notes.md`, "Rules this code follows"). The receiving method takes exactly the fields the worker owns and nothing else. The server never overwrites a fact it enforces with a worker's value.

```go
// Wrong: the whole row follows the worker (cwd, env and archivedAt included).
describeSelf := contracts.Meth("describeSelf", method(obj(wholeRow)))

// Right: the report's schema holds only the worker's fields.
describeSelf := contracts.Meth("describeSelf", method(obj(schema.Properties{
	prop("title", str()), prop("model", modelRef), prop("thinkingLevel", thinking), prop("applied", schema.Int()),
})))
```

In Go the decoded value has a Go type. Give the report its own struct that has only the worker's fields. A handler that decodes into the catalog's row type lets a worker set fields it does not own.

## 3. One writer per store: never open another host's store

A session's store is written by its worker, and only by its worker. The server never opens it, not even "while the worker is held stopped".

When someone changes a session's data while its worker is not running, the server stores the change as **pending state** in its own store, and the worker **follows** it:

1. The server writes a pending edit (session, change, revision). Views show it at once (an overlay on the last report).
2. The worker holds a server view of its pending edits. When it runs (now, or at its next start) it applies them in one commit and reports the highest revision it applied.
3. The server deletes the pending edits up to that revision.

This needs no worker start for a rename and no pause or fence for edits. It survives crashes, because pending edits are persisted, and it works for archived and synced sessions.

```go
// Worker: a followed view of the server's pending edits; apply them in one commit, then report the revision.
func (w *worker) onPending(ctx context.Context, edits []PendingEdit) error {
	if len(edits) == 0 {
		return nil
	}
	state, err := w.applyEdits(ctx, edits)
	if err != nil {
		return err
	}
	return w.sessions.DescribeSelf(ctx, state, edits[len(edits)-1].Revision)
}
```

## 4. "Is the worker running?" in a write path means the design is wrong

```go
// Wrong: two paths for one write.
if slices.Contains(workers.Running(), id) {
	return info.Edit(ctx, change)
}
return withStoredSession(dataDir, id, func(session *Session) error { return editSessionInfo(ctx, session, change) })
```

Two paths for one write double the code to test and open a race (the worker starts between the check and the write). Keep one path: the owner applies the changes it follows (rule 3), or the caller holds the owner's object and Chord starts the worker on demand.

## 5. Values are immutable once handed over: no copies

Chord and Durable values are immutable by convention once handed over. Durable's `Snapshot()` returns an immutable, structurally shared revision. A transaction's draft exists only inside its transaction (Pi's `packages/durable/docs/spec.md` §3.3 and §3.4). Do not copy to "be safe", and never copy through a `json.Marshal` and `json.Unmarshal` round trip:

- It defends against nothing: nothing may mutate a snapshot, and drafts die with their transaction.
- It hides missing ownership: a defensive copy says that nobody knows who owns the value. Fix that instead.
- It changes the value: the round trip turns every number into `float64`, drops fields the target type does not name, turns a `nil` slice into `null` or an empty one, and fails on values JSON cannot hold.
- It costs on every call, usually on a hot path.

```go
// Wrong: copy a snapshot, copy a draft out of a transaction.
return deepCopy(read.Snapshot(ctx, SessionInfoDoc))
err := session.Commit(ctx, func(tx Tx) error { /* change the draft */ out = deepCopy(draft); return nil })

// Right: return the snapshot as is; after the commit, read the committed revision.
state := read.Snapshot(ctx, SessionInfoDoc) // read-only by convention: do not write to it
err := session.Commit(ctx, func(tx Tx) error { /* change the draft */ return nil })
return session.Snapshot(ctx, SessionInfoDoc)
```

Go has no `Readonly` type. State the contract in the comment of the function that returns the snapshot, and do not write to maps or slices you did not create.

## 6. Types say the truth: no unchecked assertions, no sentinels

- Do not use an unchecked type assertion (`x.(T)`, which panics) or a discarded `ok` (`v, _ := x.(T)`) to make a value fit a type. Decode the value into a struct, or check `ok` and handle the false case. A discarded `ok` is allowed only for a decided invariant that the type system cannot express, with a comment that says which. Decoding Pi's own JSON wire shapes at the edge of a port is the usual exception.
- Do not use magic values for states. A `createdAt == 0` that means "not written yet" is an explicit state: a pointer, an `ok` result or a two-case type (`unwritten` and `written` with its info), or an absent document.

## 7. Know what wakes your code, and do the least per wake

Every routine, signal or listener runs on some trigger. Check how often that trigger fires before you put work behind it.

Mistake: a report loop woken by every entry of the main conversation (to catch the first user message for the title) that, on every wake, read the document, copied it, called the server with the whole row. The server rewrote the row and re-read every catalog row, then pushed the full session list to every open page. One assistant message re-sent all sessions to every client.

- Filter the trigger to what you need: wake on your document's changes, not on every entry. Stop watching entries once the session is titled.
- Report only when the reported data changed.
- On the server, update the one row. A list state member changes by that row (a keyed member, a table with a live query), never by re-reading and re-sending everything.

## 8. Errors are reported, never swallowed

- Do not discard an error with `_ =` or an empty `if err != nil {}`. A failure either matters (return it: a routine that fails is reported and retried with backoff by Chord) or the code says in a comment why it does not.
- Do not log and continue where the caller can act on the error. Plugin code reports errors through the host's error path (Chord's error hook, the logs plugin), not through a print to standard error. Where a port keeps Pi's `console.error` as a print to standard error, keep it and say so in the port's comment.

## 9. One implementation per piece of logic

If the logic exists (the first user message as a title is search's), import it from where it lives, or move it to a shared package that both import. A second copy drifts. A new package must not reach into another plugin's unexported code: move the shared logic to a package both may import.

## 10. Comments are claims: check them

A comment that says "the catalog ignores an unchanged report" while the code rewrites the row and re-pushes the list every time is worse than no comment. Re-read every comment you wrote against the code before you open the PR. PiG's source-hygiene gate also removes comments that restate the code or narrate a change.

## 11. The PR says what and why

The description states the problem, the decision or design it implements (link it), what changed for users and for other plugins, and what you tested. A one-word description is not enough. A change of who owns data (rule 1) needs its ownership table in the description. A PR that ports a Pi commit names its Pi SHA and every Go file that differs from Pi's shape, with the reason.

## 12. Test the hard paths

Happy paths are the least interesting tests. For a change that moves data between hosts, test: the worker starting in the middle of an edit; an edit while the worker is stopped, then a crash before it starts; a report racing an edit; and how many reports and pushes one message causes (a cost test). Run the race detector (`go test -race`) on the packages you change. Contract changes keep the released versions working (Chord's `evolve` with `accepts`, and the Go port of Pi's contract-versions test).

## What this page does not carry over

- Pi's TypeScript examples that rely on language features Go lacks (`as` casts, `Readonly<T>`, `.catch(() => {})`, `console.error`) became the Go forms in rules 5, 6 and 8.
- Pi's lint-level rules (no `as`, no `JSON.parse(JSON.stringify(x))`) have no Go lint counterpart here. PiG's `portlint` and `iface` gates cover other rules; they do not enforce rules 1 to 12.
