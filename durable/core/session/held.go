package session

import (
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Deferred commits (durable CommitPersistence, spec section 4.1): a commit whose writes are cheap to lose is adopted in memory
// and shown to the Session's own observers at once, and stored later, folded per document, with the next commit that reads or
// writes the document, at the document's deadline, before its tracker leaves memory, or at close.
//
// Ports the deferred-commit parts of packages/durable/src/session/session.ts (#deferral, #hold, #storeHolds, #release,
// #armHoldTimer) and packages/durable/src/session/transaction.ts (holdable, adoptHeld, takeHeld, storeHeld).
//
// Core mapping decisions:
//   - The host validates withinMs (a TypeError) before it begins the transaction; TxScope.WithinMs is the window, or negative
//     for the Session's default (PersistWithin).
//   - The core's Storage commit is synchronous or returns a *NeedGate, after which the job runs again from the start (loop.go).
//     takeHeld therefore only reads the held operations (PendingOps.Peek); release empties them once the Storage took the batch.
//   - The deadline's store is a detached job on the Session line (EnqueueSweep), as the document sweep is.

// DefaultPersistWithinMs is the default window of a deferred commit, settings.progress.persistWithinMs (session.ts
// DEFAULT_PERSIST_WITHIN_MS).
const DefaultPersistWithinMs = 1_000

// infiniteWithinMs is the window the host sends for Infinity, which JSON cannot carry (the largest finite number): such a commit
// waits for a reading or writing commit, a drop of its tracker, or close.
const infiniteWithinMs = 1e300

// heldDocument is the changes of a cached incarnation that deferred commits adopted in memory and the Session has not stored
// yet (transaction.ts HeldDocument, session.ts Hold): folded, and pinned in the cache until they are stored.
type heldDocument struct {
	loaded  *loadedDoc
	pending delta.PendingOps
	// base is set when a held commit selected a base (checkpointWhen): their store writes the whole value.
	base bool
	// deadline is when the earliest of its held commits must be stored, on the Session clock.
	deadline float64
	unpin    func()
}

// holdTimer is the armed store of held documents: a foreground timer for the earliest deadline, fired once its job is queued.
type holdTimer struct {
	at     float64
	cancel func()
	fired  bool
}

// heldChange is what a deferred commit holds of one incarnation: its operations, and whether they selected a base.
type heldChange struct {
	loaded *loadedDoc
	ops    []delta.Op
	base   bool
}

// persistWithin is the default window of a deferred commit, read at each use (session.ts #persistWithinMs): the setting when it
// is a number of milliseconds, 0 or more (+Inf waits for a reading or writing commit or close); otherwise the default.
func (s *Session) persistWithin() float64 {
	if s.PersistWithin != nil {
		if ms := s.PersistWithin(); ms != nil && *ms >= 0 {
			return *ms
		}
	}
	return DefaultPersistWithinMs
}

// HeldDocuments is the number of incarnations with held changes (session.ts documentCacheSizes.held).
func (s *Session) HeldDocuments() int { return len(s.holds) }

// holdable is Transaction.holdable: what a deferred commit holds in memory instead of storing, each changed incarnation with
// its operations; nil when the commit must be stored. It may be held only when every write changes an existing latest,
// initial-fork conversation document at its stored version.
func (tx *Tx) holdable(writes []Write) []heldChange {
	for i := range writes {
		if writes[i].Kind != WriteDocChange {
			return nil
		}
	}
	var held []heldChange
	for _, p := range tx.plans {
		if p.content == nil && !p.retire {
			continue
		}
		if p.retire || p.change == nil || p.change.loaded == nil || p.content.Kind != WriteDocChange {
			return nil
		}
		c := p.change
		record := c.loaded.record
		if sk, _ := getStr(getObj(record, "scope"), "kind"); sk != "conversation" {
			return nil
		}
		if h, _ := getStr(record, "history"); History(h) != HistoryLatest {
			return nil
		}
		if f, _ := getStr(record, "fork"); ForkPolicy(f) != ForkInitial {
			return nil
		}
		// A migration stores a base at once.
		if c.loaded.storedVersion != c.version {
			return nil
		}
		held = append(held, heldChange{loaded: c.loaded, ops: c.prepared.Ops(), base: p.content.Base})
	}
	return held
}

// adoptHeld is Transaction.adoptHeld: adopt a held commit in memory only. Trackers advance by pointer swap; stored versions and
// delta counts stay, as nothing was stored. It returns the changes for the Session's own observers.
func (tx *Tx) adoptHeld() ([]Change, error) {
	var out []Change
	for _, p := range tx.plans {
		// Every plan of a held commit changes or reads a loaded incarnation (holdable).
		c := p.change
		if len(c.prepared.Ops()) == 0 {
			c.prepared.Abort()
			continue
		}
		if err := c.tracker.Adopt(c.prepared); err != nil {
			return nil, err
		}
		out = append(out, Change{Kind: ChangeDocument, Record: c.loaded.record, ConversationID: p.convID, Version: c.version, Value: c.prepared.Value(), Ops: c.prepared.Ops()})
	}
	return out, nil
}

// heldPlan is what a plan that also stores held changes publishes (transaction.ts DocumentPlan.held): the operations its stored
// change carries, held first, and whether the commit has a change of its own to show the Session's observers.
type heldPlan struct {
	ops      []delta.Op
	observed bool
}

// takeHeld is Transaction.takeHeld: store the held changes of every incarnation this commit reads or writes in front of its own,
// so a stored document never misses an operation its later ones build on. Called right before Storage admission, after every
// fallible step. The queues stay until release: a Storage that holds the commit (*NeedGate) runs the job again.
func (tx *Tx) takeHeld(writes []Write) []Write {
	if len(tx.s.holds) == 0 {
		return writes
	}
	for _, p := range tx.plans {
		// A new incarnation holds nothing yet.
		if !p.committed {
			continue
		}
		recordID := mustInt(p.record, "id")
		hold := tx.s.holds[recordID]
		if hold == nil {
			continue
		}
		tx.taken = append(tx.taken, hold)
		own := -1
		var ownOps []delta.Op
		if p.content != nil && p.content.Kind == WriteDocChange {
			for i := range writes {
				if writes[i].Kind == WriteDocChange && writes[i].ID == recordID {
					own = i
				}
			}
			if p.change != nil {
				ownOps = p.change.prepared.Ops()
			}
		}
		value := hold.loaded.tracker.Value()
		if p.change != nil {
			value = p.change.prepared.Value()
		}
		content, ops := storeHeld(hold, ownOps, value, hold.loaded.valueVersion, recordID)
		// A base this commit selected or must write covers what was held.
		if own >= 0 && writes[own].Base {
			content = writes[own]
		}
		switch {
		case own >= 0:
			writes[own] = content
		default:
			writes = insertBeforeRetirement(writes, content)
		}
		if !p.retire {
			p.held = &heldPlan{ops: ops, observed: own >= 0}
		}
		stored := content
		p.content = &stored
		// Held documents are conversation documents; one this commit only read has no owner resolved yet.
		if sk, _ := getStr(getObj(p.record, "scope"), "kind"); sk == "conversation" {
			p.convID = mustInt(getObj(p.record, "scope"), "conversationId")
		}
	}
	return writes
}

// insertBeforeRetirement stages a content write for an incarnation, before its retirement when it retires.
func insertBeforeRetirement(writes []Write, write Write) []Write {
	for i := range writes {
		if writes[i].Kind == WriteDocRetire && writes[i].ID == write.ID {
			return slices.Insert(writes, i, write)
		}
	}
	return append(writes, write)
}

// storeHeld is transaction.ts storeHeld: a hold's changes after ops, a commit's own on top of them, as one stored content for an
// incarnation now at value, and the operations a reader of the stored state applies to reach it: the folded ones, or a root
// replacement after the queue overflowed, which stores a base, as does a hold whose commits selected one.
func storeHeld(hold *heldDocument, ops []delta.Op, value *jv.Object, version int, id ID) (Write, []delta.Op) {
	taken := hold.pending.Peek(ops)
	if taken.Replace {
		return Write{Kind: WriteDocChange, ID: id, Version: version, Base: true, Value: value}, []delta.Op{{Verb: delta.Replace, Value: value}}
	}
	if hold.base {
		return Write{Kind: WriteDocChange, ID: id, Version: version, Base: true, Value: value}, taken.Ops
	}
	return Write{Kind: WriteDocChange, ID: id, Version: version, Ops: taken.Ops}, taken.Ops
}

// hold is Session #hold: hold a deferred commit. It adopts it in memory, queues its changes per document, pinned, by the earliest
// deadline of the document's held commits, and shows it to the Session's own observers.
func (s *Session) hold(tx *Tx, held []heldChange) (*Publication, error) {
	docs, err := tx.adoptHeld()
	if err != nil {
		// Memory is behind what the commit meant: nothing can repair it.
		s.fail(err)
		return nil, err
	}
	within := tx.scope.WithinMs
	if within < 0 {
		within = s.persistWithin()
	}
	deadline := s.now() + within
	if within >= infiniteWithinMs {
		deadline = math.Inf(1)
	}
	for _, change := range held {
		id := mustInt(change.loaded.record, "id")
		hold := s.holds[id]
		if hold == nil {
			hold = &heldDocument{loaded: change.loaded, deadline: deadline, unpin: s.pinDocument(change.loaded.addressID, id)}
			if s.holds == nil {
				s.holds = map[ID]*heldDocument{}
			}
			s.holds[id] = hold
			s.holdOrder = append(s.holdOrder, hold)
		}
		hold.pending.Push(change.ops)
		if change.base {
			hold.base = true
		}
		if deadline < hold.deadline {
			hold.deadline = deadline
		}
	}
	s.armHoldTimer()
	pub := &Publication{Memory: true, Changes: docs}
	if s.holdPubs {
		s.heldPubs = append(s.heldPubs, pub)
	} else {
		s.publish(pub)
	}
	s.sweepDocuments()
	return pub, nil
}

// StoreHolds is Session #storeHolds over every hold: it stores the held changes of every held document in one Storage transaction,
// on the line. Memory already has them, so only the Storage listeners see it, published as the Storage adopts it. A *NeedGate means
// the host's Storage holds the commit and the job runs again; any other failure has failed the Session.
func (s *Session) StoreHolds() error {
	return s.storeHolds(slices.Clone(s.holdOrder))
}

func (s *Session) storeHolds(holds []*heldDocument) error {
	if len(holds) == 0 {
		return nil
	}
	writes := make([]Write, 0, len(holds))
	changes := make([]Change, 0, len(holds))
	for _, hold := range holds {
		loaded := hold.loaded
		record := loaded.record
		value := loaded.tracker.Value()
		content, ops := storeHeld(hold, nil, value, loaded.valueVersion, mustInt(record, "id"))
		writes = append(writes, content)
		// Held documents are conversation documents.
		changes = append(changes, Change{Kind: ChangeDocument, Record: record, ConversationID: mustInt(getObj(record, "scope"), "conversationId"), Version: loaded.valueVersion, Value: value, Ops: ops})
	}
	seq, err := s.store.Commit(writes, s.nextID)
	if err != nil {
		if IsNeedGate(err) {
			return err
		}
		s.fail(err)
		return err
	}
	for i, hold := range holds {
		if writes[i].Base {
			hold.loaded.deltasSinceBase = 0
		} else {
			hold.loaded.deltasSinceBase++
		}
	}
	s.release(holds)
	pub := &Publication{Seq: seq, Stored: changes}
	if s.holdPubs {
		s.heldPubs = append(s.heldPubs, pub)
	} else {
		s.publish(pub)
	}
	return nil
}

// release runs after the store of holds: forget them, unpin their documents, and move the timer to the next deadline.
func (s *Session) release(holds []*heldDocument) {
	for _, hold := range holds {
		delete(s.holds, mustInt(hold.loaded.record, "id"))
		if i := slices.Index(s.holdOrder, hold); i >= 0 {
			s.holdOrder = slices.Delete(s.holdOrder, i, i+1)
		}
		hold.pending = delta.PendingOps{}
	}
	for _, hold := range holds {
		hold.unpin()
	}
	s.armHoldTimer()
}

// armHoldTimer keeps a foreground timer (Workers refuse background ones) for the earliest deadline of the held documents. When it
// fires, every held document is stored, in one Storage transaction: a deadline is the latest time, and the others cost little more
// in the same transaction. Once fired, it stays until its job ran.
func (s *Session) armHoldTimer() {
	if s.closed {
		return
	}
	at := math.Inf(1)
	for _, hold := range s.holdOrder {
		at = math.Min(at, hold.deadline)
	}
	armed := s.holdTimer
	if armed != nil && (armed.fired || armed.at == at) {
		return
	}
	if armed != nil && armed.cancel != nil {
		armed.cancel()
	}
	s.holdTimer = nil
	if math.IsInf(at, 1) || s.ArmForeground == nil || s.EnqueueSweep == nil {
		return
	}
	timer := &holdTimer{at: at}
	s.holdTimer = timer
	run := func() {
		timer.fired = true
		s.EnqueueSweep(func() error {
			if s.holdTimer == timer {
				s.holdTimer = nil
			}
			// Close stores what is held itself.
			if s.closed || s.failure != nil {
				return nil
			}
			return s.StoreHolds()
		})
	}
	timer.cancel = s.ArmForeground(at, run)
}

// dropHeld is the part of a drop of a tracker that holds changes: they are stored first (session.ts loadDocument, unloadDocuments),
// so the reload finds what memory holds.
func (s *Session) dropHeld(recordID ID) error {
	if hold := s.holds[recordID]; hold != nil {
		return s.storeHolds([]*heldDocument{hold})
	}
	return nil
}

// closeHolds is Session close's last step: it forgets the holds once they were stored, the timer with them.
func (s *Session) closeHolds() {
	if s.holdTimer != nil && s.holdTimer.cancel != nil {
		s.holdTimer.cancel()
	}
	s.holdTimer = nil
	s.holds, s.holdOrder = nil, nil
}
