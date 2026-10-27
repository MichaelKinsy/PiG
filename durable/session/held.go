package session

import (
	"context"
	"errors"
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

// ObservedCommit is what the Session's own components observe of one commit (ObserveCommits): a stored commit with its
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

// allHolds returns every hold, earliest first (upstream's Map keeps insertion order).
func (session *SessionImpl) allHolds() []*heldDocument {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]*heldDocument(nil), session.holdOrder...)
}

// hold holds a deferred commit (Transaction.holdable): adopt it in memory, queue its changes per document, pinned, by the
// earliest deadline of the document's held commits, and show it to the Session's own observers.
func (session *SessionImpl) hold(ctx context.Context, tx *Transaction, held []heldChange, withinMs float64) error {
	documents, err := tx.adoptHeld()
	if err != nil {
		// Memory is behind what the commit meant: nothing can repair it.
		session.failIn(ctx, err)
		return err
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
	session.beginPublication()
	release := session.scheduler.Hold()
	for _, listener := range internal {
		// Our own observers are synchronous.
		contained(func() { listener.listener(ctx, commit) }, func(err error) { session.failIn(ctx, err) })
	}
	release()
	session.endPublication()
	return nil
}

// storeHolds stores the held changes of holds in one storage transaction, on the line. Memory already has them, so only
// SubscribeCommits listeners see it, published as Storage adopts it. A failure fails the Session, as every Storage failure does.
func (session *SessionImpl) storeHolds(ctx context.Context, holds []*heldDocument) error {
	if len(holds) == 0 {
		return nil
	}
	writes := make([]durable.StorageWrite, 0, len(holds))
	changes := make([]durable.CommitChange, 0, len(holds))
	for _, hold := range holds {
		loaded := hold.loaded
		record := loaded.Record
		value := loaded.Tracker.Value()
		version := loaded.ValueVersion
		content, ops := storeHeld(hold, nil, value, version)
		writes = append(writes, durable.DocumentChangeWrite{Id: record.Id, Content: content})
		// Held documents are conversation documents.
		var conversationId *durable.ConversationId
		if record.Scope.Kind == durable.ScopeConversation {
			id := record.Scope.ConversationId
			conversationId = &id
		}
		changes = append(changes, durable.DocumentChange{Record: record, ConversationId: conversationId, Version: &version, Value: value, Ops: ops})
	}
	adopted := false
	adopt := func(seq durable.Seq) {
		adopted = true
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
			session.beginPublication()
			session.notifyHosts(ctx, durable.CommitPublication{Seq: seq, Changes: changes})
			session.armPublication()
		}
	}
	_, err := session.storage.Commit(context.WithoutCancel(ctx), writes, adopt)
	if err != nil {
		return err
	}
	if !adopted {
		err := errors.New("Storage committed without adopting the batch")
		session.failIn(ctx, err)
		return err
	}
	session.release(holds)
	return nil
}

// release runs after the store of holds: forget them, unpin their documents, and move the timer to the next deadline.
func (session *SessionImpl) release(holds []*heldDocument) {
	session.mu.Lock()
	for _, hold := range holds {
		delete(session.holds, hold.loaded.Record.Id)
		if index := slices.Index(session.holdOrder, hold); index >= 0 {
			session.holdOrder = slices.Delete(session.holdOrder, index, index+1)
		}
	}
	session.mu.Unlock()
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
		session.enqueueDetached(func() {
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
			_ = session.storeHolds(unscoped, session.allHolds())
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
