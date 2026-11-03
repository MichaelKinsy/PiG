package session

import (
	"context"
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/durable"
)

// Deferred commits (durable.CommitPersistence, spec section 4.1): a commit whose writes are cheap to lose is adopted in
// memory and shown to the Session's own observers at once, and stored later, folded per document, with the next commit that
// reads or writes the document, at the document's deadline, before its tracker leaves memory, or at close.
//
// Ports the deferred-commit parts of packages/durable/src/session/session.ts (#deferral, #hold, #storeHolds, #release,
// #armHoldTimer) and transaction.ts (holdable, adoptHeld, takeHeld, storeHeld).
//
// Go mapping decisions:
//   - Upstream's optional `options` argument is a trailing variadic of one durable.CommitOptions.
//   - The hold timer's job is a detached line job: it takes its place on the line when the timer fires, and runs on its own
//     goroutine once the earlier jobs settled, as the document sweep's does.
//   - A deferred commit is a part of no storage call (Writer, a part without writes): it is adopted in memory when the parts
//     before it are published, in line order, and its keys stay in the unpublished write set until then.
//   - A store of held changes is a barrier through the writer: it is selected once every commit admitted before is published, and
//     stored as a part of its own, which the job that stores them waits for.

// defaultPersistWithinMs is the default window of a deferred commit, settings.progress.persistWithinMs (session.ts
// DEFAULT_PERSIST_WITHIN_MS).
const defaultPersistWithinMs = 1_000

// PersistWithin returns ms when it is a number of milliseconds, 0 or more (+Inf waits for a reading or writing commit or
// close); otherwise the default (session.ts persistWithin).
func PersistWithin(ms *float64) float64 {
	if ms != nil && *ms >= 0 {
		return *ms
	}
	return defaultPersistWithinMs
}

// ObservedCommit is what the Session's own components observe of one commit (FollowCommits): a stored commit with its
// own changes, or a deferred one adopted in memory only (Memory), which changes documents only. Memory is never behind: a
// later store of held changes is not observed again.
type ObservedCommit struct {
	// Memory is set for a commit held in memory (durable.CommitPersistence); Seq is then zero.
	Memory  bool
	Seq     durable.Seq
	Changes []durable.CommitChange
}

// internalListener is one of the Session's own commit observers.
type internalListener struct {
	listener func(ctx context.Context, commit ObservedCommit)
}

// holdTimer is the armed store of held documents: a foreground timer for the earliest deadline, fired once its job is
// queued. Guarded by the Session mutex.
type holdTimer struct {
	at     float64
	disarm func()
	fired  bool
}

// persistWithin is the default window of a deferred commit, read at each use; a source that panics counts as the default.
func (session *SessionImpl) persistWithin() (ms float64) {
	defer func() {
		// upstream: packages/durable/src/session/session.ts:#persistWithinMs
		if recover() != nil {
			ms = PersistWithin(nil)
		}
	}()
	if session.hooks.PersistWithinMs == nil {
		return PersistWithin(nil)
	}
	return PersistWithin(session.hooks.PersistWithinMs())
}

// deferral is how long a commit may stay in memory before it is stored; nil when it is immediate. persistence was checked by
// durable.SingleCommitOptions: its WithinMs, when set, is a number of milliseconds, 0 or more.
func (session *SessionImpl) deferral(persistence *durable.CommitPersistence) *float64 {
	if persistence == nil || persistence.Kind != durable.CommitDeferred {
		return nil
	}
	if persistence.WithinMs == nil {
		ms := session.persistWithin()
		return &ms
	}
	return persistence.WithinMs
}

// heldOf returns the hold of an incarnation, nil when it holds nothing.
func (session *SessionImpl) heldOf(id durable.DocumentId) *heldDocument {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.holds[id]
}

// holdCount is the number of documents with held changes.
func (session *SessionImpl) holdCount() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.holds)
}

// allHolds returns every hold, earliest first (upstream's Map keeps insertion order).
func (session *SessionImpl) allHolds() []*heldDocument {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]*heldDocument(nil), session.holdOrder...)
}

// hold holds a deferred commit (Transaction.holdable) at its publication: adopt it in memory, queue its changes per document,
// pinned, by the earliest deadline of the document's held commits, and show it to the Session's own observers. A failure of the
// adoption is the part's; the writer fails the Session with it.
func (session *SessionImpl) hold(ctx context.Context, tx *Transaction, held []heldChange, withinMs float64) *PublishFailure {
	documents, err := tx.adoptHeld()
	if err != nil {
		// As a stored commit's: memory could not adopt it.
		return &PublishFailure{Kind: PublishAdoption, Err: err}
	}
	deadline := session.clockNow() + withinMs
	for _, change := range held {
		loaded := change.loaded
		id := loaded.Record.Id
		hold := session.heldOf(id)
		if hold == nil {
			hold = &heldDocument{loaded: loaded, deadline: deadline, unpin: session.pinDocument(loaded.AddressId, id)}
			session.mu.Lock()
			session.holds[id] = hold
			session.holdOrder = append(session.holdOrder, hold)
			session.mu.Unlock()
		}
		hold.pending.Push(change.ops)
		if change.base {
			hold.base = true
		}
		if deadline < hold.deadline {
			hold.deadline = deadline
		}
	}
	session.armHoldTimer()
	session.mu.Lock()
	internal := append([]*internalListener(nil), session.internalOrder...)
	session.mu.Unlock()
	if len(internal) == 0 {
		return nil
	}
	changes := make([]durable.CommitChange, 0, len(documents))
	for _, document := range documents {
		changes = append(changes, document)
	}
	commit := ObservedCommit{Memory: true, Changes: changes}
	// The watches the observers advance deliver once, for all the frames of this commit.
	defer session.scheduler.Hold()()
	var failure *PublishFailure
	for _, listener := range internal {
		// Our own observers are synchronous.
		contained(func() { listener.listener(ctx, commit) }, func(err error) {
			if failure == nil {
				failure = &PublishFailure{Kind: PublishListener, Err: err}
			}
		})
	}
	return failure
}

// storeHolds stores the held changes select returns in one storage transaction, through the writer, from a job on the line. A
// barrier: they are selected once every commit admitted before is published. Memory already has them, so only ObserveCommits
// listeners see it, published as the writer publishes it. A failure fails the Session, as every Storage failure does.
func (session *SessionImpl) storeHolds(ctx context.Context, selectHolds func() []*heldDocument) error {
	session.awaitDrained()
	holds := selectHolds()
	if len(holds) == 0 {
		return nil
	}
	writes := make([]durable.StorageWrite, 0, len(holds))
	changes := make([]durable.CommitChange, 0, len(holds))
	keys := make([]string, 0, 2*len(holds))
	for _, hold := range holds {
		loaded := hold.loaded
		record := loaded.Record
		value := loaded.Tracker.Value()
		version := loaded.ValueVersion
		content, ops := storeHeld(hold, nil, value, version)
		writes = append(writes, durable.DocumentChangeWrite{Id: record.Id, Content: content})
		keys = append(keys, DocumentKey(loaded.AddressId), IncarnationKey(record.Id))
		// Held documents are conversation documents.
		var conversationId *durable.ConversationId
		if record.Scope.Kind == durable.ScopeConversation {
			id := record.Scope.ConversationId
			conversationId = &id
		}
		changes = append(changes, durable.DocumentChange{Record: record, ConversationId: conversationId, Version: version, Value: value, Ops: ops})
	}
	publish := func(seq durable.Seq) *PublishFailure {
		for index, hold := range holds {
			loaded := hold.loaded
			loaded.mu.Lock()
			if writes[index].(durable.DocumentChangeWrite).Content.Kind == durable.ContentBase {
				loaded.deltasSinceBase = 0
			} else {
				loaded.deltasSinceBase++
			}
			loaded.mu.Unlock()
		}
		session.mu.Lock()
		hosts := len(session.commitOrder) > 0
		session.mu.Unlock()
		if hosts {
			session.notifyHosts(ctx, durable.CommitPublication{Seq: seq, Changes: changes})
		}
		session.forget(holds)
		return nil
	}
	// Run from a job on the line, which a failure it causes is the outcome of.
	session.jobMu.Lock()
	index := 0
	if session.current != nil {
		index = session.current.index
	}
	session.jobMu.Unlock()
	part := session.writer.Admit(PartSpec{Index: index, Writes: writes, Keys: keys, Ctx: context.WithoutCancel(ctx), Publish: publish, Published: func() { session.finishRelease(holds) }, Discard: func() {}})
	return part.Wait()
}

// forget runs in the publication of a store of holds, or of a commit that took them: the Session no longer holds them.
func (session *SessionImpl) forget(holds []*heldDocument) {
	session.mu.Lock()
	for _, hold := range holds {
		delete(session.holds, hold.loaded.Record.Id)
		if index := slices.Index(session.holdOrder, hold); index >= 0 {
			session.holdOrder = slices.Delete(session.holdOrder, index, index+1)
		}
	}
	session.mu.Unlock()
}

// finishRelease runs once the keys of the publication that stored holds left the unpublished set: unpin their documents, which
// may schedule an eviction that has to see the keys gone, and move the timer to the next deadline.
func (session *SessionImpl) finishRelease(holds []*heldDocument) {
	for _, hold := range holds {
		hold.unpin()
	}
	session.armHoldTimer()
}

// armHoldTimer keeps a foreground timer (Workers refuse background ones) for the earliest deadline of the held documents.
// When it fires, every held document is stored, in one storage transaction: a deadline is the latest time, and the others
// cost little more in the same transaction. Once fired, it stays until its job ran.
func (session *SessionImpl) armHoldTimer() {
	session.mu.Lock()
	if session.closing != nil {
		session.mu.Unlock()
		return
	}
	at := math.Inf(1)
	for _, hold := range session.holdOrder {
		at = math.Min(at, hold.deadline)
	}
	armed := session.holdTimer
	if armed != nil && (armed.fired || armed.at == at) {
		session.mu.Unlock()
		return
	}
	var disarmArmed func()
	if armed != nil {
		disarmArmed = armed.disarm
	}
	session.holdTimer = nil
	if math.IsInf(at, 1) {
		session.mu.Unlock()
		if disarmArmed != nil {
			disarmArmed()
		}
		return
	}
	timer := &holdTimer{at: at}
	session.holdTimer = timer
	session.mu.Unlock()
	if disarmArmed != nil {
		disarmArmed()
	}
	run := func() {
		session.mu.Lock()
		timer.fired = true
		session.mu.Unlock()
		session.enqueueDetached(func(int) {
			session.mu.Lock()
			if session.holdTimer == timer {
				session.holdTimer = nil
			}
			closing := session.closing != nil
			session.mu.Unlock()
			// Close stores what is held itself.
			if closing || session.failure() != nil {
				return
			}
			// A failing store has failed the Session; nobody waits on this job.
			_ = session.storeHolds(unscoped, session.allHolds)
		})
	}
	disarm := session.timers.Foreground(run, math.Max(0, at-session.clockNow()))
	session.mu.Lock()
	timer.disarm = disarm
	closing := session.closing != nil
	session.mu.Unlock()
	// Close began while the timer was being armed: its disarm could not see it yet.
	if closing {
		disarm()
	}
}
