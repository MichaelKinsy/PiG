package session

import (
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Publication is what one applied commit changed.
type Publication struct {
	Seq     Seq
	Changes []Change
}

// Session is the Session kernel: the mutation line's committed-state cache and
// the commit procedure of session/session.ts. It is single-threaded; its owner
// serializes calls.
type Session struct {
	store Store
	// hop is the Storage profile whose await ticks a transaction's callback is charged (hopcost); lastTicks is the
	// Change of the last transaction whose callback succeeded (hopcost.Job.Change).
	hop       hopcost.Profile
	lastTicks int
	// settleReads is the Storage reads the settle of the transaction being committed made beyond its callback's (Tx.settleReads).
	settleReads int
	// settleCached is the replaced tasks whose committed read the callback had already made (Tx.settleCached).
	settleCached int
	now          func() float64
	nextID       ID
	docs         map[string]*loadedDoc
	// idle is the cached documents in use order, oldest first (retention.go); sweepAt is the time of the pending eviction.
	idle               map[string]*idleDoc
	idleHead, idleTail *idleDoc
	sweepPending       bool
	sweepAt            float64
	sweepToken         int
	attached           map[ID]int
	sweepCancel        func()
	// SweepJobs counts the sweep jobs queued on the line so far (documentCacheSizes.sweeps). EnqueueSweep queues a job on the
	// Session line, Microtask runs fn as a microtask, ArmTimer arms a host timer that calls fire at the time at; a Session without
	// them keeps no sweep job or timer.
	SweepJobs    int
	EnqueueSweep func(job func())
	Microtask    func(fn func())
	ArmTimer     func(at float64, fire func()) (cancel func())
	// DocumentRetention is the document retention in milliseconds read at each use (HarnessSettings.documentRetentionMs); nil or
	// a nil result means the default.
	DocumentRetention func() *float64
	failure           error
	// OnFail reports the storage error that failed the Session, once (session.ts fail).
	OnFail func(error)
	// failedListeners run when the Session fails, before it seals (session.ts `failed`, rejected before close runs).
	failedListeners []*func(error)
	closed          bool

	commitListeners []func(*Publication)
	closeListeners  []func()

	// holdPubs makes Finish keep its publication in heldPubs instead of running the commit listeners: a Loop line job's
	// publication happens at its completion, which may wait for Pi's duration of the job (Loop.Hold).
	holdPubs bool
	// inJob is true while a Loop line job body runs.
	inJob bool
	// unpublished counts the documents loaded by the running or finishing line job that Loaded does not yet report.
	unpublished int
	// HostAdmits: the host admits calls itself, so the seal does not reject a call it admitted before the seal (assertUsable).
	HostAdmits bool
	heldPubs   []*Publication

	// ConversationCreated runs inside every transaction that creates or forks a
	// conversation, after the conversation record is staged. A plain Session
	// stages nothing; a Harness stages its built-in documents.
	ConversationCreated func(tx *Tx, record *jv.Object) error
}

// New returns a Session over a store. nextID is the next unminted record ID
// and now the Harness clock.
func New(store Store, nextID ID, now func() float64) *Session {
	return &Session{store: store, now: now, nextID: nextID, docs: map[string]*loadedDoc{}, lastTicks: -1}
}

// NextID returns the next unminted record ID.
func (s *Session) NextID() ID { return s.nextID }

// Failure returns the first storage error that failed the Session, or nil.
func (s *Session) Failure() error { return s.failure }

// fail fails the Session with err unless it already failed: the Session seals, so its close listeners end what they hold, and
// the first error is reported once after them (session.ts fail: close, then report).
func (s *Session) fail(err error) {
	if s.failure != nil {
		return
	}
	s.failure = err
	listeners := s.failedListeners
	s.failedListeners = nil
	for _, l := range listeners {
		(*l)(err)
	}
	s.Seal()
	if s.OnFail != nil {
		s.OnFail(err)
	}
}

// OnFailed registers f to run once with the error that fails the Session, before the seal (a race against session.ts `failed`),
// and returns its removal. It never runs for a Session that has already failed: the caller reads Failure.
func (s *Session) OnFailed(f func(error)) (remove func()) {
	p := &f
	s.failedListeners = append(s.failedListeners, p)
	return func() {
		s.failedListeners = slices.DeleteFunc(s.failedListeners, func(q *func(error)) bool { return q == p })
	}
}

// WriteTasks writes complete task records straight to the Storage, with nothing published and the records not stamped
// (session.ts writeAtClose). It writes nothing once the Session failed; a failing Storage fails the Session.
func (s *Session) WriteTasks(records []*jv.Object) error {
	if s.failure != nil || len(records) == 0 {
		return nil
	}
	writes := make([]Write, len(records))
	for i, rec := range records {
		writes[i] = Write{Kind: WriteTask, Record: rec}
	}
	_, err := s.store.Commit(writes, s.nextID)
	if err != nil {
		if !IsNeedGate(err) {
			s.fail(err)
		}
		return err
	}
	return nil
}

// Seal is the synchronous start of Session.close (session.ts close): admission closes, so later operations fail with
// ErrClosed when they are enqueued (Admit), and the close listeners run once. Jobs admitted before keep running.
func (s *Session) Seal() {
	if s.closed {
		return
	}
	s.closed = true
	listeners := s.closeListeners
	s.closeListeners = nil
	for _, l := range listeners {
		l()
	}
}

// Close seals the Session and drops its commit listeners and loaded documents, as the close job does after the
// Harness joined its invocations.
func (s *Session) Close() {
	s.Seal()
	s.commitListeners = nil
	s.docs = map[string]*loadedDoc{}
	s.clearIdle()
	s.attached = nil
}

// Admit is the admission check of an operation as it is enqueued on the Session line (session.ts #assertUsable in
// commitWith and readOnLine): it fails once the Session is closed or poisoned.
func (s *Session) Admit() error { return s.assertUsable() }

func (s *Session) assertUsable() error {
	// A job admitted before the seal runs after it (session.ts: #assertUsable guards enqueue, not the job body).
	if s.closed && !s.inJob && !s.HostAdmits {
		if err := s.assertHealthy(); err != nil {
			return err
		}
		return ErrClosed
	}
	return s.assertHealthy()
}

func (s *Session) assertHealthy() error {
	if s.failure != nil {
		return &FailedError{Cause: s.failure}
	}
	return nil
}

// Commit runs change on the Session line as one commit and returns the commit's
// publication. A callback that errors stages nothing: the error is returned
// and IDs it minted stay consumed, as a rolled-back Pi callback leaves a gap.
// When any read returns a *NeedRead the callback restarts from scratch after
// the host answers: Commit restores the ID counter and returns the NeedRead.
// An empty write set commits nothing and returns a nil publication.
func (s *Session) Commit(change func(tx *Tx) error, scope TxScope) (*Publication, error) {
	if err := s.Admit(); err != nil {
		return nil, err
	}
	return s.RunCommit(change, scope)
}

// RunCommit is the body of a commit job already admitted to the Session line (session.ts #runCommit): it checks only
// that the Session is healthy, so a job enqueued before Seal still commits.
func (s *Session) RunCommit(change func(tx *Tx) error, scope TxScope) (*Publication, error) {
	tx, err := s.Begin(scope)
	if err != nil {
		return nil, err
	}
	if err := change(tx); err != nil {
		s.Abandon(tx, err)
		return nil, err
	}
	s.lastTicks = tx.ticks
	pub, err := s.Finish(tx)
	if IsNeedGate(err) {
		// The line job runs again from the start after the host answered the gate, as it does after a NeedRead.
		tx.abortChanges()
		s.nextID = tx.mintStart
	}
	return pub, err
}

// Begin opens a transaction whose callback runs outside this call: a host drives it one operation at a time (ABI
// section 8) and ends it with Finish or Abandon. Commit is Begin, the callback, and Finish.
func (s *Session) Begin(scope TxScope) (*Tx, error) {
	if err := s.assertHealthy(); err != nil {
		return nil, err
	}
	return newTx(s, scope), nil
}

// Abandon seals a transaction whose callback failed with err; it stages nothing. A *NeedRead failure restores the ID
// counter, since the callback runs again from scratch.
func (s *Session) Abandon(tx *Tx, err error) {
	tx.settleFailure()
	if _, need := IsNeedRead(err); need {
		s.nextID = tx.mintStart
		return
	}
	// A restarted callback has not ended the commit; any other failure has (session.ts #runCommit).
	s.sweepDocuments()
}

// Finish settles a transaction whose callback succeeded and commits its writes, as the tail of Commit.
func (s *Session) Finish(tx *Tx) (*Publication, error) {
	// A transaction the host drove (not RunCommit's callback) has charged its Transaction awaits as its operations ran: the job's duration is theirs.
	s.lastTicks = tx.ticks
	defer func() { s.settleReads, s.settleCached = 0, 0 }()
	writes, err := tx.settleSuccess()
	if err != nil {
		if _, need := IsNeedRead(err); need {
			s.nextID = tx.mintStart
		} else if !IsNeedGate(err) {
			s.sweepDocuments()
		}
		return nil, err
	}
	s.settleReads, s.settleCached = tx.settleReads, tx.settleCached
	return s.commitWrites(tx, writes)
}

// FinishSettled commits again a transaction whose commit returned a *NeedGate: its writes are settled and the host's
// Storage answered the gate.
func (s *Session) FinishSettled(tx *Tx) (*Publication, error) {
	s.settleReads, s.settleCached = tx.settleReads, tx.settleCached
	defer func() { s.settleReads, s.settleCached = 0, 0 }()
	return s.commitWrites(tx, tx.settled)
}

//go:noinline
func (s *Session) commitWrites(tx *Tx, writes []Write) (*Publication, error) {
	if len(writes) == 0 {
		tx.abortChanges()
		s.sweepDocuments()
		return nil, nil
	}
	seq, err := s.store.Commit(writes, s.nextID)
	if err != nil {
		if IsNeedGate(err) {
			// Nothing was applied: the caller commits the same transaction again (FinishSettled) or discards it.
			tx.settled = writes
			return nil, err
		}
		tx.abortChanges()
		// Whether a failed commit took effect is unknown, and a rejected one failed the Storage call all the same: every
		// failure ends the Session (session.ts GuardedStorage #call; a commit is never exempt).
		s.fail(err)
		return nil, err
	}
	docs, err := tx.adopt(seq)
	if err != nil {
		s.fail(err)
		return nil, err
	}
	changes := make([]Change, 0, len(writes)+len(docs))
	for _, w := range writes {
		switch w.Kind {
		case WriteConversation, WriteEntry, WriteTask, WriteSubmission:
			changes = append(changes, Change{Kind: ChangeTable, Table: w.Kind, Record: w.Record})
		}
	}
	changes = append(changes, docs...)
	pub := &Publication{Seq: seq, Changes: changes}
	for _, change := range docs {
		s.touchDocument(addressID(recordAddress(change.Record)))
	}
	if s.holdPubs {
		s.heldPubs = append(s.heldPubs, pub)
	} else {
		s.publish(pub)
	}
	s.sweepDocuments()
	return pub, nil
}

// InJob reports whether a Loop line job body is running.
func (s *Session) InJob() bool { return s.inJob }

// HoldPublications makes the next commits keep their publications until HeldPublications takes them: a host
// transaction's commit publishes when its job's duration has passed, as a Loop line job's does.
func (s *Session) HoldPublications() { s.holdPubs = true }

// HeldPublications ends HoldPublications and returns the publications it kept, in commit order.
func (s *Session) HeldPublications() []*Publication {
	pubs := s.heldPubs
	s.holdPubs, s.heldPubs = false, nil
	return pubs
}

// Publish runs the commit listeners on a held publication.
func (s *Session) Publish(pub *Publication) { s.publish(pub) }

// publish runs the commit listeners on a publication (session.ts #publish).
//
//go:noinline
func (s *Session) publish(pub *Publication) {
	for _, l := range slices.Clone(s.commitListeners) {
		l(pub)
	}
}

// SubscribeCommits registers a synchronous post-adoption listener. It must not
// throw, block or call Session operations.
func (s *Session) SubscribeCommits(fn func(*Publication)) {
	s.commitListeners = append(s.commitListeners, fn)
}

// SubscribeClose registers a listener that runs when Close begins.
func (s *Session) SubscribeClose(fn func()) { s.closeListeners = append(s.closeListeners, fn) }

// loadDocument returns the cached incarnation for the address, or loads the
// current one from the store. A tracker serves only the definition version its
// value was materialized for; another version reloads.
func (s *Session) loadDocument(def *DocDef, id string, addr Address) (*loadedDoc, error) {
	if cached := s.docs[id]; cached != nil {
		if cached.valueVersion == def.Version {
			s.touchDocument(id)
			return cached, nil
		}
		s.dropDocument(id)
	}
	rec, err := s.store.FindDocument(addr, Current)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	stored, err := s.store.Document(mustInt(rec, "id"), Current)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		kind, _ := getStr(rec, "kind")
		return nil, errors.New("Current document " + kind + " cannot be read")
	}
	value, err := materialize(def, stored.Record, stored.Version, stored.Value)
	if err != nil {
		return nil, err
	}
	l := &loadedDoc{
		addressID: id, record: stored.Record, storedVersion: stored.Version, valueVersion: def.Version,
		deltasSinceBase: stored.DeltasSinceBase, tracker: delta.Track(value),
	}
	if s.inJob {
		l.unpublished = true
		s.unpublished++
	}
	s.docs[id] = l
	s.touchDocument(id)
	return l, nil
}

// Snapshot returns the current value of a document, or nil when absent.
func (s *Session) Snapshot(def *DocDef, args DocArgs) (*jv.Object, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	addr := def.address(args)
	l, err := s.loadDocument(def, addressID(addr), addr)
	if err != nil || l == nil {
		return nil, err
	}
	if err := checkRecordScope(def, l.record); err != nil {
		return nil, err
	}
	if err := checkRecordVersion(def, l.record, l.storedVersion); err != nil {
		return nil, err
	}
	return l.tracker.Value(), nil
}

// Loaded reports whether Snapshot would answer from the loaded-document cache (session.ts snapshot: a cached tracker of
// the definition's version), which Pi reads without a job on the Session line.
func (s *Session) Loaded(def *DocDef, args DocArgs) bool {
	cached := s.docs[addressID(def.address(args))]
	return cached != nil && cached.valueVersion == def.Version && !cached.unpublished
}

// revealLoads makes the documents the finished job loaded visible to Loaded.
// HasUnrevealedLoads reports whether a line job's loads wait for its publication to show (revealLoads has something to do).
func (s *Session) HasUnrevealedLoads() bool { return s.unpublished != 0 }

func (s *Session) revealLoads() {
	if s.unpublished == 0 {
		return
	}
	for _, d := range s.docs {
		d.unpublished = false
	}
	s.unpublished = 0
}

// UnloadDocuments drops every cached document; later access loads from the store.
func (s *Session) UnloadDocuments() {
	s.docs = map[string]*loadedDoc{}
	s.clearIdle()
}

// SetHopProfile names the Storage profile whose await ticks transactions are charged (open.storageProfile).
func (s *Session) SetHopProfile(p hopcost.Profile) { s.hop = p }

// SettleReads is the Storage reads the settle of the transaction being committed made for its staged submission changes; it
// is 0 outside a commit.
func (s *Session) SettleReads() int { return s.settleReads }

// SettleCached is the replaced tasks the settle of the transaction being committed found already read by its callback: their
// committed read costs one tick, not a Storage read; 0 outside a commit.
func (s *Session) SettleCached() int { return s.settleCached }

// PeekTicks is the Change of the transaction callback that just succeeded, without clearing it: -1 when none ran.
func (s *Session) PeekTicks() int { return s.lastTicks }

// TakeTicks returns and clears the Change of the last transaction callback that succeeded on the line: the ticks of
// Pi's change callback for its Transaction method awaits and Scheduler wrapper (hopcost), -1 when none ran.
func (s *Session) TakeTicks() int {
	n := s.lastTicks
	s.lastTicks = -1
	return n
}
