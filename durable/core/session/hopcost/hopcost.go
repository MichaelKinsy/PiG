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

// Writes is one batch of a Storage.commit call: the StorageWrite batch one job's commit made.
type Writes struct {
	// Tables counts the conversation, entry, task and submission writes.
	Tables int
	// DocCreates counts the document.create and document.copy writes: each claims its ID in checkGlobalIds.
	DocCreates int
	// DocEdits counts the document.change and document.retire writes.
	DocEdits int
	// Actions are the merged document actions in write order.
	Actions []Action
	// Refused is set for a batch Durable's own checks refuse in checkGlobalIds (an ID that is taken, storage.ts:754-772): the
	// number of claiming writes checked up to and including the refused one. The batch writes nothing and ends the call; only the
	// last batch of a call can be refused.
	Refused int
}

// Commit is the ticks from the call of Storage.commit(batches) to its caller's resumption through the Session's guard: the
// backend's commitBackend, adopted by the guard.
func Commit(p Profile, batches []Writes) int { return guarded(commitBackend(p, batches)) }

// CommitCall is what a host that runs the Storage call itself needs of a call's batches: receive, the ticks from the writer's call to the
// COMMIT after which the statements of every stored batch are visible (CommitTail ticks before the writer resumes; MemoryStorage stores in the
// tick of the call), and resume, the ticks to the writer's resumption from `await storage.commit(...)`. The batches are one transaction, so
// the statements of all of them land at the one COMMIT.
func CommitCall(p Profile, batches []Writes) (receive, resume int) {
	resume = Commit(p, batches)
	if p == Memory {
		return 0, resume
	}
	return resume - CommitTail(p), resume
}

// IDReadLead is the ticks from the Storage's read of its ID counter to the COMMIT of the call (SqliteStorage.commit, storage.ts:249-253):
// candidateNextId(writes) reads the counter once the last stored batch is applied, so an ID a job mints until then is in the next_id the
// call writes. After the read come the checks of the refused batch the call ends in, if it does, then `await writeMetadata` (the run and
// the async method's return: 2) and `await cb(direct)` (1), after which the transaction commits: 3 ticks, measured on Pi (the tick of
// candidateNextId against capsql's COMMIT tick, gate and node profiles). A call that stored nothing writes no metadata and reads nothing.
func IDReadLead(batches []Writes) int {
	n := 3
	if len(batches) > 0 && batches[len(batches)-1].Refused > 0 {
		n += refusedBatch(batches[len(batches)-1].Refused)
	}
	return n
}

// commitBackend is Commit of the adapter itself, before the Session's guard: SqliteStorage.commit (storage.ts:218-259) over the
// capsql facade (capsql.mjs:142-160), counted by the awaits of its one transaction. One tick for `queue.then(work)` to start the work,
// readMetadata, each batch's checks and writes, the metadata write when a batch was stored, and the four ticks that leave the transaction:
// `await cb(direct)`, the facade's adoption of the work's promise, `await this.db.transaction` and the caller's `await commit`.
func commitBackend(p Profile, batches []Writes) int {
	n := 1 // serial: queue.then(work)
	n += 2 // await readMetadata: the get, and the async method's return
	stored := false
	for _, w := range batches {
		if w.Refused > 0 {
			n += refusedBatch(w.Refused)
			break
		}
		n += checkBatch(w) + applyBatch(w)
		stored = true
	}
	if stored {
		n += 2 // await writeMetadata: the run, and the async method's return
	}
	n += 4
	if p == Node {
		n += nodeTransactionEnd
	}
	return backend(p, n)
}

// watched is the ticks of `await checked.get(...)` of one statement: `watchCalls` wraps each call of the checks in an async arrow that
// awaits it (1) and returns, so the caller resumes one tick later (storage.ts:160-171).
const watched = 2

// checkBatch is `await this.checkBatch(checked, writes, actions)` (storage.ts:278-286): the three async checks, each awaited, and the
// method's own return.
func checkBatch(w Writes) int {
	n := checkGlobalIDs(w) + 1
	n += checkDocumentActions(w.Actions) + 1
	n += resolveCopies(w.Actions) + 1
	return n + 1
}

// applyBatch is `await this.applyBatch(transaction, writes, actions, seq)` (storage.ts:288-296): every write awaited through
// applyTableWrite (only the table kinds do work), applyDocumentActions awaited, and the method's own return.
func applyBatch(w Writes) int {
	return w.Tables*tableWrite + w.DocCreates + w.DocEdits + applyDocumentActions(w.Actions) + 1 + 1
}

// refusedBatch is a batch refused after `claims` checked claims: checkGlobalIds rejects as its last check resumes, the await of
// it in checkBatch resumes one tick later and checkBatch rejects, and the await in commit resumes one tick after that into the catch.
func refusedBatch(claims int) int { return claims*watched + 2 }

// CommitTail is the ticks from the backend's COMMIT statement to the writer's resumption from Storage.commit (writer.ts #send
// `await this.#host.commit(...)`): commitTailBackend, then the Session's guard, whose async arrow adopts the backend's promise and whose
// commit returns (+2, as guarded adds to the whole call). The statements of a call are applied CommitTail ticks before the writer resumes.
func CommitTail(p Profile) int { return commitTailBackend(p) + 2 }

// ReceiveTail is the ticks from the last `receive(index, seq)` of a call to the writer's resumption: `receive` runs where `await
// this.db.transaction` resumed, one tick before the caller of commit does (the lazy proxy's adoption adds one), and the guard adds 2.
// Nothing observes the tick of a receipt; the writer reads the seqs after it resumes.
func ReceiveTail(p Profile) int { return receiveTailBackend(p) + 2 }

// receiveTailBackend is ReceiveTail of the adapter itself, before the Session's guard (scenarios/storage-ticks.mjs `receiveTail`).
func receiveTailBackend(p Profile) int {
	if p == Gate {
		return 2
	}
	return 1
}

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

// tableWrite is applyTableWrite of a conversation, entry, task or submission (storage.ts:881-940): claimId awaits one
// statement and is awaited (2), the row statement (1), and the function's own await by the caller loop (1).
const tableWrite = 4

// checkGlobalIDs is the body of checkGlobalIds (storage.ts:754-772): one watched get per write that claims an ID (every table write
// and document create or copy; document.change and retire skip).
func checkGlobalIDs(w Writes) int { return (w.Tables + w.DocCreates) * watched }

// checkDocumentActions is the body of checkDocumentActions (storage.ts:824-859): per action the watched record get, the watched
// version get of a delta, and `await this.currentDocumentId(...)` (a watched get and the async method's return) unless an earlier
// action already looked the address up.
func checkDocumentActions(actions []Action) int {
	n := 0
	for _, a := range actions {
		n += watched // SELECT record FROM documents
		if a.Content == Delta {
			n += watched // SELECT version FROM document_revisions
		}
		if !a.KnownAddress {
			n += watched + 1
		}
	}
	return n
}

// resolveCopies is the body of resolveCopies (storage.ts:951-973): per copy `await this.materializeDocument(checked, ...)`, whose
// three statements are watched (storage.ts:708-750), and the method's return.
func resolveCopies(actions []Action) int {
	n := 0
	for _, a := range actions {
		if a.Copy {
			n += materializeDocument*watched + 1
		}
	}
	return n
}

// applyDocumentActions is the body of applyDocumentActions (storage.ts:975-1050): the copies were resolved to their content by the
// checks, so a copy applies like a create.
func applyDocumentActions(actions []Action) int {
	n := 0
	for _, a := range actions {
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

// ColdEntryAwaits is the awaits a read of the newest entry ID adds to its Storage scan (session.ts:377-384): the async `read` that returns the
// first row's ID (+1), and the tails' await of it, which a read-only Session has not (+1).
func ColdEntryAwaits(readOnly bool) int {
	if readOnly {
		return 1
	}
	return 2
}

// ColdMarkerAwaits is the awaits a read of the newest head marker adds to its Storage read (session.ts:399-410): the tails' await of it,
// which a read-only Session has not.
func ColdMarkerAwaits(readOnly bool) int {
	if readOnly {
		return 0
	}
	return 1
}

// LatestEntryID is `await tx.latestEntryId(id)` (transaction.ts:324-326, session.ts:377-384): KnownTail when the Session's commits know it,
// else the scan of the newest entry (ReadScanEntries) and ColdEntryAwaits.
func LatestEntryID(p Profile, known, readOnly bool) int {
	if known {
		return KnownTail
	}
	return ReadHops(p, ReadScanEntries) + ColdEntryAwaits(readOnly)
}

// LatestHeadMarker is `await tx.latestHeadMarker(id)` (transaction.ts:320-322, session.ts:399-410): KnownTail when the Session's commits know it,
// else Storage's findLatestHeadMarker (ReadHeadMarker) and ColdMarkerAwaits.
func LatestHeadMarker(p Profile, known, readOnly bool) int {
	if known {
		return KnownTail
	}
	return ReadHops(p, ReadHeadMarker) + ColdMarkerAwaits(readOnly)
}

// KnownTail is the awaits of a read of the newest entry or head marker of a conversation that the Session knows from its commits: the
// tails' async method returns the kept value, and its caller's await resumes a tick later (session.ts latestEntryId, latestHeadMarker).
const KnownTail = 1

// Job is the ticks of one Session line job from its start to the return of its work (session.ts #runCommit :1078-1160 under #enqueue):
// the change callback and the settle. What follows the return (the writer's call to Storage.commit and the publication, the line's release, the
// delivery of the outcome) is the Session's own promise structure and the Storage's (line.go, Commit).
type Job struct {
	// Change is the ticks of the change callback's own awaits (tx methods, loads and the reads they make, summed by the
	// caller from ReadHops and the tx method awaits); `await change(tx)` itself adds one.
	Change int
	// Settle is the ticks of settleSuccess's body (transaction.ts:674-753): assemble awaits, its Storage reads
	// (committed task, owners, submissions); `await tx.settleSuccess()` adds one.
	Settle int
	// Membership is the ticks of `await membership` in #runCommit (session.ts:1134-1141) for a commit that writes with a Session whose
	// `membershipKeys` returns a promise (Membership); 0 for a commit that changes no task and creates no owned conversation.
	Membership int
}

// Return is the ticks from the start of the job to the return of its work: for a commit with writes the writer's admission, in the tick its call to
// Storage.commit starts when none is in flight.
func (j Job) Return() int { return j.Callback() + 1 + j.Settle + j.Membership }

// Membership is the ticks of `await membership` for the scheduler's `membershipKeys` (scheduler.ts:683-722) of a commit that creates or ends
// `chains` tasks and creates `owned` conversations under a task: the async function awaits `#loadChain` and `#chainKeys` for each (an already
// loaded chain returns at once: 1 each), and the awaiting `#runCommit` resumes one tick after it settles. A commit that changes no task and creates
// no owned conversation gets the keys at once and pays nothing; a commit that only replaces a live task pays the async function's one tick.
func Membership(chains, owned int) int {
	return 2*(chains+owned) + 1
}

// Callback is the ticks from the start of the job to the resumption of `await change(tx)`, where #runCommit checks the Session healthy.
func (j Job) Callback() int { return 1 + j.Change }

// ReturnFailed is Return of a job whose change callback rejected after Change ticks: `await change(tx)` throws, the catch resumes one tick later,
// and `await tx.settleFailure()` (an async function whose only await is of a promise that is already settled: two ticks) precedes the rethrow
// that rejects the work (session.ts #runCommit :1093-1097).
func (j Job) ReturnFailed() int { return 1 + j.Change + 2 }

// Settle is the ticks of settleSuccess's body (transaction.ts:674-753, 755-853), from the awaits the caller sums: `owners`, the awaits of
// `#validateOwners()` over the owners of the tasks and conversations the transaction creates, each `await this.#currentTask(owner)` (1 for an owner
// the transaction staged, else 2: CurrentTask over the settled read the creating call made); `cached`, the replaced tasks whose committed
// read the callback made already (1 each); `reads`, the other awaits in order: the committed task of a replaced task the callback did
// not read, the submissions a settlement names (RecordRead), the document pages of a task going terminal that the Session did not create
// (ReadRow; a task it created has its documents known, transaction.ts:1020-1024), and the `#currentTask` of each task whose document the
// commit publishes (1 for a staged task, once per task, transaction.ts:1032-1040). The awaits of validateOwners' own return (1) and of
// assemble's own return (1) are counted here; `await tx.settleSuccess()` adds one more, counted by Job.Return.
func Settle(owners, cached, reads int) int {
	return owners + 1 + reads + cached + 1
}

// The awaits of the change callback of a line job, in ticks from the callback's start to its promise's settlement.
// The callback is the user's `async (tx) => ...`; the Session awaits it (`await change(tx)`, session.ts:415, one tick
// later, which Job.Hops adds). The functions below give the ticks of one `await` of a Transaction method
// (transaction.ts) and of the Scheduler wrappers around a change; a callback's ticks are the sum of its awaits.

// TxRead is `await tx.X(...)` for a Transaction read that hands back the Storage promise itself (`#read`,
// transaction.ts:1132-1139): scanConversations, scanEntries, scanTasks and submissionByRequest, which the Session's cache passes
// through to Storage (cached-storage.ts), so it costs exactly the Storage read. Conversation, task and submission are TxConversation,
// TxTask and TxSubmission; latestEntryId and latestHeadMarker are the Session's (LatestEntryID).
func TxRead(p Profile, r Read) int { return ReadHops(p, r) }

// Record is a table record the Session's CachedStorage keeps (cached-storage.ts).
type Record uint8

// The cached records.
const (
	RecordConversation Record = iota + 1
	RecordTask
	RecordSubmission
)

// HitTicks is a record read the cache answers: `Promise.resolve(cached)` settles at once, so the Session's guard around it (guarded) takes
// the same ticks over every profile (scenarios/session-ticks.mjs `warm tx.conversation`).
const HitTicks = 4

// Cache is how the Session's CachedStorage answers a record read.
type Cache uint8

// The answers.
const (
	// CacheMiss: the cache does not hold the record: it reads Storage.
	CacheMiss Cache = iota
	// CacheHit: the cache holds the record.
	CacheHit
	// CacheOff: the Session has no cache (a read-only Session shows a Storage another process writes): the read goes to Storage as it is.
	CacheOff
)

// RecordRead is the ticks of `storage.conversation(id)`, `task(id)` or `submission(id)` through the Session's guard and its cache
// (cached-storage.ts conversation, task, submission): a hit is HitTicks. A miss reads Storage; a conversation or task read goes through
// `#keep`, an async function that awaits it and returns (+1 under the guard's adoption), a submission read is passed through, and so is any
// read of a Session without a cache.
func RecordRead(p Profile, r Record, c Cache) int {
	switch {
	case c == CacheHit:
		return HitTicks
	case c == CacheOff || r == RecordSubmission:
		return ReadHops(p, ReadRow)
	}
	return guarded(readBackend(p, ReadRow) + 1)
}

// TxConversation is `await tx.conversation(id)` (transaction.ts:292-294): the cache's answer.
func TxConversation(p Profile, c Cache) int { return RecordRead(p, RecordConversation, c) }

// TxSubmission is `await tx.submission(id)` (transaction.ts:334-336): the cache's answer.
func TxSubmission(p Profile, c Cache) int { return RecordRead(p, RecordSubmission, c) }

// TxTask is `await tx.task(id)` (transaction.ts:307-309, 1190-1194): the first call starts the read and caches its promise
// (`committedRead`); a later call awaits that promise, which is already settled (1). The first is the cache's answer.
func TxTask(p Profile, cached bool, c Cache) int {
	if cached {
		return 1
	}
	return RecordRead(p, RecordTask, c)
}

// TxEntry is `await tx.entry(id)` (transaction.ts:300-306): an async function around the Storage read, so one tick more.
func TxEntry(p Profile) int { return ReadHops(p, ReadEntry) + 1 }

// TxAppendEntry is `await tx.appendEntry(...)` (transaction.ts:392-417): `await #requireConversation(id)` (an async
// function: nothing to await when the transaction created the conversation, else the conversation read and the
// function's return), then `await mintId`, then the wrapper's own return. c is how the cache answers the conversation.
func TxAppendEntry(p Profile, conversationCreatedHere bool, c Cache) int {
	return RequireConversation(p, conversationCreatedHere, c) + ReadHops(p, MintID) + 1
}

// RuntimeCommit is the ticks of the callback `#gated` hands the Session for `runtime.commit(change)`
// (scheduler.ts:2671-2682, 2928-2960) when the user's change settles after u ticks and returns a next state
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

// GatedRegion is the ticks of the decision region the `#gated` callback awaits first (scheduler.ts:2937-2946 `await this.#region(...)`: an
// async function that returns at once when no key it read has an unpublished writer, so its caller resumes one tick later). A region that
// waits for a gate is the Session line's wait (NeedPublication).
const GatedRegion = 1

// GatedChange is the Change of a `#gated` callback (scheduler.ts:2928-2960) whose `change` is an async function that
// settles after s ticks: the callback is `async (tx) => { const current = await region(...); return change(tx, current) }`, and an async
// function that returns a promise settles one tick after that promise (never earlier than tick 1) (scenarios/gated-ticks.mjs).
func GatedChange(s int) int { return GatedRegion + max(s, 1) + 1 }

// GatedSyncChange is the Change of a `#gated` callback whose `change` returns a value without awaiting: the callback returns after its region.
const GatedSyncChange = GatedRegion

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

// RequireConversation is `await this.#requireConversation(id)` (transaction.ts:1160-1165): an async function that returns
// at once for a conversation the transaction created, else awaits the conversation read (hit: the cache answers); its caller resumes one tick
// after its settlement.
func RequireConversation(p Profile, createdHere bool, c Cache) int {
	if createdHere {
		return 1
	}
	return RecordRead(p, RecordConversation, c) + 1
}

// CurrentTask is `await this.#currentTask(id)` (transaction.ts:1186-1188): a record this transaction staged is returned
// at once; else the committed task's cached promise is awaited (an unsettled first read costs the read, a settled one 1),
// and the async function's return adds one tick.
func CurrentTask(p Profile, staged, cached bool, c Cache) int {
	if staged {
		return 1
	}
	return TxTask(p, cached, c) + 1
}

// TxDoc is `await tx.doc(token, ...)` (transaction.ts:511-541, 587-623). A document the transaction already opened
// returns its settled draft promise (1). Otherwise #acquire awaits the load: a cached tracker 1, a stored document
// findDocument + document() + the async load's return (ReadRow + ReadDocument + 1), an absent one findDocument + return
// (ReadRow + 1); an absent document then awaits RequireConversation(createdHere) for a conversation scope (taskScope:
// CurrentTask for a task scope, owner's staged record `staged`) and mintId. #acquire settles after those awaits and the
// caller resumes one tick later. scope is the document's scope kind: 'c' conversation, 't' task, anything else session; taskCached is whether the
// transaction read the task already, c how the cache answers the conversation (scope 'c') or the task (scope 't').
func TxDoc(p Profile, repeat bool, load DocLoad, scope byte, createdHere, taskStaged, taskCached bool, c Cache) int {
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
			n += RequireConversation(p, createdHere, c)
		case 't':
			n += CurrentTask(p, taskStaged, taskCached, c)
		}
		n += ReadHops(p, MintID)
	}
	return n + 1
}

// TxCreateTask is `await tx.createTask(...)` (transaction.ts:455-520): the async wrapper awaits the owner's task when
// the new task is owned (CurrentTask), RequireConversation, mintId, and returns.
func TxCreateTask(p Profile, ownerTask, ownerStaged, ownerCached bool, ownerCache Cache, conversationCreatedHere bool, conversationCache Cache) int {
	n := 0
	if ownerTask {
		n += CurrentTask(p, ownerStaged, ownerCached, ownerCache)
	}
	n += RequireConversation(p, conversationCreatedHere, conversationCache)
	n += ReadHops(p, MintID)
	return n + 1
}

// TxCreateSubmission is `await tx.createSubmission(...)` (transaction.ts:521-529): RequireConversation, mintId, return.
func TxCreateSubmission(p Profile, conversationCreatedHere bool, c Cache) int {
	return RequireConversation(p, conversationCreatedHere, c) + ReadHops(p, MintID) + 1
}

// TxCreateConversation is `await tx.createConversation(...)` for a root or owned conversation (transaction.ts:347-390):
// mintId, the owner's CurrentTask for a task-owned one (ownerTask), then `await host.conversationCreated`, whose
// settlement is hookTicks (the Harness override's awaits, harness.ts:357-364; the Session's is Promise.resolve(): 1).
// The method returns the staging promise itself, so the caller resumes one tick after it settles. A fork's document
// copies (prepareForkDocumentCopies) are not included.
func TxCreateConversation(p Profile, ownerTask, ownerStaged, ownerCached bool, ownerCache Cache, hookTicks int) int {
	n := ReadHops(p, MintID)
	if ownerTask {
		n += CurrentTask(p, ownerStaged, ownerCached, ownerCache)
	}
	return n + hookTicks + 1
}

// MintLead is the ticks from a Transaction method's call of `storage.mintId()` to the adapter's `this.nextId++` (storage.ts:298-302). The
// Session's wrappers call the adapter synchronously; the memory proxy of the gate profile awaits its settled `ready` promise first (1).
func MintLead(p Profile) int {
	if p == Gate {
		return 1
	}
	return 0
}

// TxAppendEntryMint is the ticks from the call of `tx.appendEntry(...)` to the adapter's mint of the entry's ID (TxAppendEntry up to mintId).
func TxAppendEntryMint(p Profile, conversationCreatedHere bool, c Cache) int {
	return RequireConversation(p, conversationCreatedHere, c) + MintLead(p)
}

// TxCreateSubmissionMint is the ticks from the call of `tx.createSubmission(...)` to the mint of the submission's ID.
func TxCreateSubmissionMint(p Profile, conversationCreatedHere bool, c Cache) int {
	return RequireConversation(p, conversationCreatedHere, c) + MintLead(p)
}

// TxCreateTaskMint is the ticks from the call of `tx.createTask(...)` to the mint of the task's ID (TxCreateTask up to mintId).
func TxCreateTaskMint(p Profile, ownerTask, ownerStaged, ownerCached bool, ownerCache Cache, conversationCreatedHere bool, conversationCache Cache) int {
	n := 0
	if ownerTask {
		n += CurrentTask(p, ownerStaged, ownerCached, ownerCache)
	}
	return n + RequireConversation(p, conversationCreatedHere, conversationCache) + MintLead(p)
}

// TxCreateConversationMint is the ticks from the call of `tx.createConversation(...)` to the mint of the conversation's ID: it mints first.
func TxCreateConversationMint(p Profile) int { return MintLead(p) }

// TxDocMint is the ticks from the call of `tx.doc(...)` for an absent document to the mint of its ID (TxDoc up to mintId): the lookup, then
// the scope's awaits.
func TxDocMint(p Profile, scope byte, createdHere, taskStaged, taskCached bool, c Cache) int {
	n := ReadHops(p, ReadRow) + 1
	switch scope {
	case 'c':
		n += RequireConversation(p, createdHere, c)
	case 't':
		n += CurrentTask(p, taskStaged, taskCached, c)
	}
	return n + MintLead(p)
}

// ReadReturn is the ticks from the start of a `Session.readOnLine(job)` line job (session.ts:536-546 under #enqueue) to the return of its work, for
// a job that awaits for `body` ticks (the sum of the awaited calls, each as its caller resumes from it). `readOnLine` wraps the job in an
// async arrow that returns `{ value: await job(this.#gated) }`: the arrow's own await of the job's promise resumes one tick after the job
// settles, and a job that awaits nothing still settles in the tick it starts. A job that returns the Storage promise itself (`direct`) settles one
// tick before the read's caller would resume.
func ReadReturn(body int, direct bool) int {
	body = max(body, 0)
	if direct {
		body--
	}
	return body + 1
}

// The ticks between the Storage reads of ConversationViews attach (view.ts :140-175, :226-243), where an async function returns
// to an awaiting caller.
const (
	// ViewDocReturn: `await #loadDocument` resumes conversationDocumentOnLine (1), which returns to #build (1); a Session-cached
	// document costs only these.
	ViewDocReturn = 2
	// ViewBuildReturn: #build returns the mount to attach's job.
	ViewBuildReturn = 1
	// ViewScanReturn: scanUpTo returns to loadWindow.
	ViewScanReturn = 1
	// ViewWindowReturn: loadWindow returns to attach's job.
	ViewWindowReturn = 1
)
