// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package hopcost derives, in microtask ticks, how long Pi's Storage adapters take, so the core can park a line job on a
// hops chain as long as Pi's job (ABI.md section 5.3).
//
// The model is additive tick arithmetic over Pi's own await structure. An `await f()` of an async function f costs the
// ticks of f's body plus one: f's promise resolves at the tick of its last await, and the caller resumes one tick later.
// An await of a function that completes without awaiting costs one. A call is therefore the sum of its callee awaits, and
// each function below cites the Pi source whose awaits it counts. The package is pure: no allocation beyond its inputs,
// no formatting, no reflection.
package hopcost

// Profile names the Storage adapter whose await structure is modelled (the open.storageProfile of the ABI).
type Profile string

// The modelled profiles.
const (
	// GateCapsql is SqliteStorage (storage/sqlite/storage.ts) over the contract gate's capsql piDurableDatabase facade
	// (durable/contract/lib/capsql.mjs piDurableDatabase), opened directly.
	GateCapsql Profile = "gate-capsql"
	// Gate is GateCapsql behind the contract gate's lazy MemoryStorage proxy (durable/contract/impls/storage.mjs lazy).
	Gate Profile = "gate"
	// Memory is MemoryStorage (storage/memory.ts): every method is an async function without awaits, so its caller
	// resumes one tick after the call.
	Memory Profile = "memory"
	// Node is SqliteStorage over the Node adapter (storage/sqlite/node.ts), opened directly: a statement outside a
	// transaction is `access.run(op)`, Promise.resolve(op()) on an idle queue, so awaiting it costs one tick (the
	// capsql facade's `serial` costs four), and a transaction starts at once (`runAsync`) and ends through
	// `started.finally` (see nodeTransactionEnd).
	Node Profile = "node"
)

// statement is the ticks of one Storage statement outside a transaction on an idle queue, from the call to the awaiting
// method's resumption: the capsql facade's `serial` (`queue.then(work)` starts the work one tick later, the work's
// promise is adopted at tick 3, the awaiting method resumes at tick 4) or the Node adapter's Promise.resolve (1).
func statement(p Profile) int {
	if p == Node {
		return 1
	}
	return 4
}

// nodeTransactionEnd is the ticks the Node adapter's `transaction` adds beyond the capsql facade's: the capsql facade starts the work one
// tick after the call (queue.then) while NodeSqliteDatabase.transaction starts it at once (runAsync, node.ts :168-190), and the result
// leaves through `started.finally(...)` (a reaction, then the returned promise's adoption: the transaction promise resolves three ticks
// after `started`, and `await this.db.transaction` resumes one tick later) where the capsql facade's `queue.then` adoption takes two
// (capsql.mjs:142-160). Net +1.
const nodeTransactionEnd = 1

// viaProxy is the caller-resume ticks of a Storage method that takes d ticks directly when the gate's lazy proxy wraps it
// (impls/storage.mjs lazy): `async (...args) => { const s = await ready; return s[prop](...args) }`. The wrapper awaits
// `ready` (one tick), calls the method, and returns the method's promise, which an async function adopts two ticks after
// that promise resolves (the thenable job, then its reaction; never earlier than tick 3). So d becomes max(d, 2) + 2:
// +2 for every method that awaits, +3 for one that resolves at once (mintId: 1 -> 4, dcore-trace-hops.md).
func viaProxy(d int) int { return max(d, 2) + 2 }

// guarded is the caller-resume ticks of a Storage call made through the Session's GuardedStorage (session.ts #call,
// packages/durable at 42a3497d) when the backend takes d ticks: `const call = (async () => run())()` returns the backend's
// promise from an async arrow, which adopts it (the thenable job, then its reaction: never earlier than tick 2), then
// `await call` (one tick) and the async #call returns the result (the caller resumes one tick later). The same
// arithmetic as viaProxy: d becomes max(d, 2) + 2.
func guarded(d int) int { return max(d, 2) + 2 }

// backend is the ticks a Storage method takes for the profile when it takes d directly: the adapter as the Session is handed it.
func backend(p Profile, d int) int {
	switch p {
	case Memory:
		return 1
	case Gate:
		return viaProxy(d)
	}
	return d
}

// Content is the revision a document action stores.
type Content uint8

// The document content commands (StorageWrite document.create/change content: none, a base, or a delta).
const (
	NoContent Content = iota
	Base
	Delta
)

// Action is the merged document action of one document ID in a commit (SqliteStorage.prepareDocumentActions,
// storage.ts:674-719): its create or copy, its content and its retire.
type Action struct {
	// Create is set for a document.create or document.copy write: the action inserts the documents row.
	Create bool
	// Copy is set for a document.copy: the content is the materialized source document.
	Copy bool
	// Content is the revision the action stores: a create's or change's content; a copy stores its base (Copy implies Base).
	Content Content
	// Retire is set when a document.retire write names the document.
	Retire bool
	// CurrentOnly is storage.ts isCurrentOnly of the document record: its revisions are replaced, not kept.
	CurrentOnly bool
	// KnownAddress is set when an earlier action of the commit already looked up this document's address (liveCounts, storage.ts:721-770).
	KnownAddress bool
}

// Writes is the StorageWrite batch of a commit.
type Writes struct {
	// Tables counts the conversation, entry, task and submission writes.
	Tables int
	// DocCreates counts the document.create and document.copy writes: each claims its ID in checkGlobalIds.
	DocCreates int
	// DocEdits counts the document.change and document.retire writes.
	DocEdits int
	// Actions are the merged document actions in write order.
	Actions []Action
}

// Commit is the ticks from the call of SqliteStorage.commit to its caller's resumption (storage.ts:174-197) over the
// capsql facade (capsql.mjs:142-160): one tick for `queue.then(work)` to start the transaction work, the awaits of the
// transaction callback, one tick for `await cb(direct)`, one for the facade's resolution with the work's promise
// (queue.then adopts it), one for `await this.db.transaction` and one for the caller's `await commit`.
func Commit(p Profile, w Writes) int { return guarded(commitBackend(p, w)) }

// commitBackend is Commit of the adapter itself, before the Session's guard.
func commitBackend(p Profile, w Writes) int {
	n := 1 // serial: queue.then(work)
	n++    // get durable_metadata
	n += checkGlobalIDs(w) + 1
	n += checkDocumentActions(w.Actions) + 1
	n += w.Tables*tableWrite + w.DocCreates + w.DocEdits // applyTableWrite: every write is awaited, only table kinds do work
	n += applyDocumentActions(w.Actions) + 1
	n++    // UPDATE durable_metadata
	n += 4 // await cb(direct); queue adoption; await db.transaction; the caller's await
	if p == Node {
		n += nodeTransactionEnd
	}
	return backend(p, n)
}

// CommitTail is the ticks from the backend's COMMIT statement to the Session's resumption from Storage.commit (#runCommit
// `seq = await this.#storage.commit(...)`): commitTailBackend, then the Session's guard, whose async arrow adopts the backend's
// promise and whose #call returns (+2, as guarded adds to the whole call).
func CommitTail(p Profile) int { return commitTailBackend(p) + 2 }

// commitTailBackend is the ticks from the COMMIT statement to the resumption of a caller of the adapter's commit
// (scenarios/storage-ticks.mjs `commitTail`): over the capsql facade the facade's adoption of the work's promise, `await
// this.db.transaction` and the caller's await (3); the Node adapter's `started.finally(...)` takes two more (nodeTransactionEnd
// is its net +1 with the transaction starting a tick earlier); the lazy proxy's adoption of the method's promise one more.
func commitTailBackend(p Profile) int {
	n := 3
	switch p {
	case Node:
		n += nodeTransactionEnd + 1
	case Gate:
		n++
	}
	return n
}

// tableWrite is applyTableWrite of a conversation, entry, task or submission (storage.ts:778-841): claimId awaits one
// statement and is awaited (2), the row statement (1), and the function's own await by the caller loop (1).
const tableWrite = 4

// checkGlobalIDs is storage.ts:652-672: one get per write that claims an ID (every table write and document create or
// copy; document.change and retire skip).
func checkGlobalIDs(w Writes) int { return w.Tables + w.DocCreates }

// checkDocumentActions is storage.ts:721-770 without the function's own return await.
func checkDocumentActions(actions []Action) int {
	n := 0
	for _, a := range actions {
		n++ // SELECT record FROM documents
		if a.Content == Delta {
			n++ // SELECT version FROM document_revisions
		}
		if !a.KnownAddress {
			n += 2 // currentDocumentId: its get, and the await of the async function
		}
	}
	return n
}

// applyDocumentActions is storage.ts:843-920 without the function's own return await.
func applyDocumentActions(actions []Action) int {
	n := 0
	for _, a := range actions {
		if a.Copy {
			n += materializeDocument + 1 // await materializeDocument (storage.ts:606-640)
		}
		if a.Create {
			n += 2 + 1 // claimId (statement and await), INSERT INTO documents
		} else {
			n++ // SELECT record FROM documents
		}
		if a.Content != NoContent {
			if a.Content == Base && a.CurrentOnly {
				n++ // DELETE FROM document_revisions
			}
			n++ // INSERT INTO document_revisions
		}
		if a.Retire {
			if !a.Create {
				n++ // UPDATE documents
			}
			if a.CurrentOnly {
				n++ // DELETE FROM document_revisions
			}
		}
	}
	return n
}

// materializeDocument is the body of storage.ts materializeDocument for a live document: the record get, the base get
// and the tail all.
const materializeDocument = 3

// Read is a Storage read of SqliteStorage.
type Read uint8

// The reads, by their await structure over the capsql facade. A statement outside a transaction goes through
// capsql.mjs `serial`: `queue.then(work)` starts the work one tick later, the work's promise is adopted (resolved at
// tick 3 of the call) and the awaiting method resumes at tick 4, so one statement is 4 ticks, and the async method
// around it returns one tick later (5). A statement that finds the queue busy waits for it (a transaction of another
// job is in the queue); that contention is not modelled here, so these are the idle-queue costs.
const (
	// ReadRow is a method with one statement: conversation, task, submission, submissionByRequest, findDocument,
	// scanConversations, scanTasks, scanSubmissions, scanDocuments (storage.ts:236-330, 541-604).
	ReadRow Read = iota + 1
	// ReadDocument is `document(id, at)`: db.transaction over materializeDocument (storage.ts:533-540, 606-640): the
	// work starts at 1, three statements of `direct` (3), the cb await (1), the facade adoption (1), the async
	// method's reaction to the facade promise (1) and the caller's await (1).
	ReadDocument
	// ReadEntry is `entry(id)`: admitRead over readEntry with one statement (storage.ts:591-600, 273-310).
	ReadEntry
	// ReadEntryIn is `entry(conversationId, id)`: readConversation first (storage.ts:273-310).
	ReadEntryIn
	// ReadScanEntries is scanEntries: readConversation, then one rows statement (storage.ts readEntries).
	ReadScanEntries
	// ReadHeadMarker is findLatestHeadMarker: readConversation, then one statement (storage.ts readLatestHeadMarker).
	ReadHeadMarker
	// MintID is mintId: an async function without awaits (storage.ts:199-203).
	MintID
)

// ReadHops is the ticks from a read's call to its caller's resumption for the profile, on an idle queue, through the Session's guard.
func ReadHops(p Profile, r Read) int { return guarded(readBackend(p, r)) }

// readBackend is ReadHops of the adapter itself, before the Session's guard.
func readBackend(p Profile, r Read) int {
	var d int
	switch r {
	case ReadRow:
		d = statement(p) + 1
	case ReadDocument:
		d = 1 + materializeDocument + 1 + 1 + 1 + 1 // work start, body, cb await, facade adoption, the async method's reaction to the facade promise, caller await
	case ReadEntry:
		d = statement(p) + 1 + 1 // readEntry returns (+1), admitRead returns (+1)
	case ReadEntryIn, ReadScanEntries, ReadHeadMarker:
		d = (statement(p) + 1) + statement(p) + 1 + 1 // readConversation, the statement, readX returns, admitRead returns
	case MintID:
		d = 1
	}
	if p == Node && r == ReadDocument {
		d += nodeTransactionEnd
	}
	return backend(p, d)
}

// Job is the ticks of one Session line job from its start to its resolution (session.ts #runCommit :405-446 under
// #enqueue :531): the change callback, the settle, the Storage commit, adoption and publication, and the two ticks of
// the return through `#enqueue` (`job-` is `sto<` + 2, dcore-trace section 9 A2).
type Job struct {
	// Change is the ticks of the change callback's own awaits (tx methods, loads and the reads they make, summed by the
	// caller from ReadHops and the tx method awaits); `await change(tx)` itself adds one.
	Change int
	// Settle is the ticks of settleSuccess's body (transaction.ts:674-753): assemble awaits, its Storage reads
	// (committed task, owners, submissions); `await tx.settleSuccess()` adds one.
	Settle int
	// Writes is the commit's batch; none: the job writes nothing and returns after the settle.
	Writes *Writes
}

// Hops is the job's duration.
func (j Job) Hops(p Profile) int {
	n := 1 + j.Change + 1 + j.Settle // await change(tx); await tx.settleSuccess()
	if j.Writes != nil {
		n += Commit(p, *j.Writes)
	}
	return n + 2
}

// Settle is the ticks of settleSuccess's body for a settle that makes `rows` single-statement Storage reads and awaits
// `cached` promises its callback had already settled (transaction.ts:674-753, 755-853): `await this.#validateOwners()`
// awaits each owner entry of the tasks the transaction creates in turn (a staged owner resolves in one tick) and its own
// return (owners+1), each read awaited in turn (the committed task of a replaced task, scanDocuments pages of a task going
// terminal, submissions: ReadRow each; Transaction hands the Storage promise itself, transaction.ts:975-990; a task the
// callback read already: 1), and the await of assemble's own return (1). `await tx.settleSuccess()` adds one more,
// counted by Job.Hops.
func Settle(p Profile, rows, cached, owners int) int {
	return owners + 1 + rows*ReadHops(p, ReadRow) + cached + 1
}

// The awaits of the change callback of a line job, in ticks from the callback's start to its promise's settlement.
// The callback is the user's `async (tx) => ...`; the Session awaits it (`await change(tx)`, session.ts:415, one tick
// later, which Job.Hops adds). The functions below give the ticks of one `await` of a Transaction method
// (transaction.ts) and of the Scheduler wrappers around a change; a callback's ticks are the sum of its awaits.

// TxRead is `await tx.X(...)` for a Transaction read that hands back the Storage promise itself (`#read`,
// transaction.ts:912-940): conversation, scanConversations, scanEntries, latestHeadMarker, scanTasks, submission and
// submissionByRequest. It costs exactly the Storage read (transaction.ts:229-275).
func TxRead(p Profile, r Read) int { return ReadHops(p, r) }

// TxTask is `await tx.task(id)` (transaction.ts:244-246, 975-990): the first call starts the Storage read and caches its
// promise (`committedRead`); a later call awaits that promise, which is already settled (1).
func TxTask(p Profile, cached bool) int {
	if cached {
		return 1
	}
	return ReadHops(p, ReadRow)
}

// TxEntry is `await tx.entry(id)` (transaction.ts:235-242): an async function around the Storage read, so one tick more.
func TxEntry(p Profile) int { return ReadHops(p, ReadEntry) + 1 }

// TxAppendEntry is `await tx.appendEntry(...)` (transaction.ts:360-382): `await #requireConversation(id)` (an async
// function: nothing to await when the transaction created the conversation, else the conversation read and the
// function's return), then `await mintId`, then the wrapper's own return.
func TxAppendEntry(p Profile, conversationCreatedHere bool) int {
	n := 1 // requireConversation of a conversation this transaction created: no await inside, resumed one tick later
	if !conversationCreatedHere {
		n = ReadHops(p, ReadRow) + 1
	}
	return n + ReadHops(p, MintID) + 1
}

// RuntimeCommit is the ticks of the callback `#gated` hands the Session for `runtime.commit(change)`
// (scheduler.ts:1147-1155, 1275-1297) when the user's change settles after u ticks and returns a next state
// (`nextDefined`): `await change(tx, current)` (u+1), then `await #commitState(...)` for a next state (1, plus the
// state's own awaits, `commitStateExtra`), and the async callback that returns that promise adopts it (settle, never
// earlier than one tick, plus one). The result is the Job's Change.
func RuntimeCommit(u int, nextDefined bool, commitStateExtra int) int {
	s := u + 1
	if nextDefined {
		s += 1 + commitStateExtra
	}
	return GatedChange(s)
}

// GatedChange is the Change of a `#gated` callback (scheduler.ts:1275-1297) whose `change` is an async function that
// settles after s ticks: the callback is `async (tx) => { ...; return change(tx, current) }`, and an async function
// that returns a promise settles one tick after that promise (never earlier than tick 1).
func GatedChange(s int) int { return max(s, 1) + 1 }

// GatedSyncChange is the Change of a `#gated` callback whose `change` returns a value without awaiting (memo, the
// Scheduler's own state changes): the async callback returns at once.
const GatedSyncChange = 0

// DocLoad is how Session.#loadDocument finds a document for tx.doc (session.ts:505-535).
type DocLoad uint8

// The three outcomes of a document load.
const (
	// DocCached: the Session's tracker cache serves it (same version): the async function returns at once.
	DocCached DocLoad = iota + 1
	// DocStored: the cache misses and the document exists: findDocument, then document().
	DocStored
	// DocAbsent: the cache misses and findDocument finds nothing: a new document is created in the transaction.
	DocAbsent
)

// RequireConversation is `await this.#requireConversation(id)` (transaction.ts:941-946): an async function that returns
// at once for a conversation the transaction created, else awaits the conversation read; its caller resumes one tick
// after its settlement.
func RequireConversation(p Profile, createdHere bool) int {
	if createdHere {
		return 1
	}
	return ReadHops(p, ReadRow) + 1
}

// CurrentTask is `await this.#currentTask(id)` (transaction.ts:975-977): a record this transaction staged is returned
// at once; else the committed task's cached promise is awaited (an unsettled first read costs the read, a settled one 1),
// and the async function's return adds one tick.
func CurrentTask(p Profile, staged, cached bool) int {
	if staged {
		return 1
	}
	return TxTask(p, cached) + 1
}

// TxDoc is `await tx.doc(token, ...)` (transaction.ts:511-541, 587-623). A document the transaction already opened
// returns its settled draft promise (1). Otherwise #acquire awaits the load: a cached tracker 1, a stored document
// findDocument + document() + the async load's return (ReadRow + ReadDocument + 1), an absent one findDocument + return
// (ReadRow + 1); an absent document then awaits RequireConversation(createdHere) for a conversation scope (taskScope:
// CurrentTask for a task scope, owner's staged record `staged`) and mintId. #acquire settles after those awaits and the
// caller resumes one tick later. scope is the document's scope kind: 'c' conversation, 't' task, anything else session.
func TxDoc(p Profile, repeat bool, load DocLoad, scope byte, createdHere, taskStaged bool) int {
	if repeat {
		return 1
	}
	var n int
	switch load {
	case DocCached:
		n = 1
	case DocStored:
		n = ReadHops(p, ReadRow) + ReadHops(p, ReadDocument) + 1
	default:
		n = ReadHops(p, ReadRow) + 1
		switch scope {
		case 'c':
			n += RequireConversation(p, createdHere)
		case 't':
			n += CurrentTask(p, taskStaged, true)
		}
		n += ReadHops(p, MintID)
	}
	return n + 1
}

// TxCreateTask is `await tx.createTask(...)` (transaction.ts:383-428): the async wrapper awaits the owner's task when
// the new task is owned (CurrentTask), RequireConversation, mintId, and returns.
func TxCreateTask(p Profile, ownerTask bool, ownerStaged, ownerCached, conversationCreatedHere bool) int {
	n := 0
	if ownerTask {
		n += CurrentTask(p, ownerStaged, ownerCached)
	}
	n += RequireConversation(p, conversationCreatedHere)
	n += ReadHops(p, MintID)
	return n + 1
}

// TxCreateSubmission is `await tx.createSubmission(...)` (transaction.ts:429-444): RequireConversation, mintId, return.
func TxCreateSubmission(p Profile, conversationCreatedHere bool) int {
	return RequireConversation(p, conversationCreatedHere) + ReadHops(p, MintID) + 1
}

// TxCreateConversation is `await tx.createConversation(...)` for a root or owned conversation (transaction.ts:301-343):
// mintId, the owner's CurrentTask for a task-owned one (ownerTask), then `await host.conversationCreated`, whose
// settlement is hookTicks (the Harness override's awaits, harness.ts:357-364; the Session's is Promise.resolve(): 1).
// The method returns the staging promise itself, so the caller resumes one tick after it settles. A fork's document
// copies (prepareForkDocumentCopies) are not included.
func TxCreateConversation(p Profile, ownerTask bool, ownerStaged, ownerCached bool, hookTicks int) int {
	n := ReadHops(p, MintID)
	if ownerTask {
		n += CurrentTask(p, ownerStaged, ownerCached)
	}
	return n + hookTicks + 1
}

// ReadOnLine is the ticks of a `Session.readOnLine(job)` line job (session.ts:105-115 under #enqueue :531) whose job reads
// for `body` ticks: the sum of its awaited Storage reads (a job that returns the Storage promise itself, `direct`, settles
// one tick before the read's caller would resume). `readOnLine` wraps the job in an async arrow that returns its promise,
// #enqueue's run adopts that promise, and the tail reacts to the run: three ticks after the job settles. E23 measures
// 7 for `readOnLine(() => storage.conversation(id))` (4 + 3) and 25 for readContextFrom's bounds (22 + 3).
func ReadOnLine(body int, direct bool) int {
	// A job that awaits nothing is still an async arrow whose promise #readOnLine's wrapper adopts: one tick of its own.
	body = max(body, 1)
	if direct {
		body--
	}
	return body + 3
}

// The ticks between the Storage reads of ConversationViews #build (view.ts :156-175, context.ts :23-40, :217-225), where an
// async function returns to an awaiting caller.
const (
	// ViewEmptyBounds: captureContextBounds returns undefined to #build (1) and `await activeEntries` of a conversation without
	// entries resolves an async function that awaits nothing (1).
	ViewEmptyBounds = 2
	// ViewBoundsReturn: captureContextBounds returns the bounds of a conversation with entries to #build.
	ViewBoundsReturn = 1
	// ViewActiveReturn: scanRange returns to activeEntries (1), and activeEntries returns to #build (1).
	ViewActiveReturn = 2
	// ViewDocReturn: `await #loadDocument` resumes conversationDocumentOnLine (1), which returns to #build (1); a Session-cached
	// document costs only these.
	ViewDocReturn = 2
	// ViewBuildReturn: #build returns the mount to attach's job.
	ViewBuildReturn = 1
)
