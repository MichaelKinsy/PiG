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
	Seq Seq
	// Changes is what the Session's own observers see: this commit's own changes. A deferred commit adopted in memory only
	// (Memory) has the document changes and no Seq.
	Changes []Change
	// Memory is set for a commit held in memory (held.go): it is not stored, and the commit listeners that follow the Storage
	// (the host's subscribeCommits) get nothing.
	// Span is the host's handle of the span of the commit's Context: where the commit's entry and settlement events go (TxScope.Span).
	Span   uint32
	Memory bool
	// Stored is every change one Storage transaction stored, tables included, when that differs from Changes: a commit that
	// also stored held changes of the documents it reads or writes (its document change carries the held operations first), or
	// a deadline's store of held documents (no Changes: nothing is shown again). Nil: it is Changes.
	Stored []Change
	// Created is the tasks the commit created, whose documents the Session then knows (taskdocs.go).
	Created []ID
}

// StoredChanges is what the Storage transaction of the publication stored.
func (p *Publication) StoredChanges() []Change {
	if p.Stored != nil {
		return p.Stored
	}
	return p.Changes
}

// Session is the Session kernel: the mutation line's committed-state cache and
// the commit procedure of session/session.ts. It is single-threaded; its owner
// serializes calls.
type Session struct {
	// store is what the line reads through (gatedStore over raw); raw is the Store commits go to.
	store Store
	raw   Store
	// touchAt is the ticks the running job had spent when it last touched a key (Tx.charge): where a wait for a publication began.
	touchAt int
	// hop is the Storage profile whose await ticks a transaction's callback is charged (hopcost); lastTicks is the
	// Change of the last transaction whose callback succeeded (hopcost.Job.Change).
	hop       hopcost.Profile
	lastTicks int
	// settleReads is the ticks of the settle's reads of the transaction being committed beyond its callback's (Tx.settleReads).
	settleReads int
	// settleCached is the replaced tasks whose committed read the callback had already made (Tx.settleCached).
	settleCached int
	now          func() float64
	nextID       ID
	// latent is how many of the IDs minted so far Pi's Storage has not minted yet: the body of a job runs at the job's start, and Pi mints
	// each ID as its Transaction method runs, later in the job's duration. kept counts the IDs a callback that runs again minted before it
	// stopped: Pi's callback goes on from there, so its Storage minted them, and the callback mints them again.
	latent, kept int
	keptJob      *job
	mints        mintClock
	docs         map[string]*loadedDoc
	docEpochs    uint64 // the epochs given to cached document incarnations so far (loadedDoc.epoch)
	// idle is the cached documents in use order, oldest first (retention.go); sweepAt is the time of the pending eviction.
	idle               map[string]*idleDoc
	idleHead, idleTail *idleDoc
	sweepPending       bool
	sweepAt            float64
	sweepToken         int
	attached           map[ID]int
	sweepCancel        func()
	// SweepJobs counts the sweep jobs queued on the line so far (documentCacheSizes.sweeps). EnqueueSweep queues a detached job on
	// the Session line (a *NeedPublication its job returns runs it again once the key's writer is published), Microtask runs fn as a
	// microtask, ArmTimer arms a host timer that calls fire at the time at, ArmForeground one that paces or resumes work (the
	// deadline of deferred commits); a Session without them keeps no sweep job or timer.
	SweepJobs     int
	EnqueueSweep  func(job func() error)
	Microtask     func(fn func())
	ArmTimer      func(at float64, fire func()) (cancel func())
	ArmForeground func(at float64, fire func()) (cancel func())
	// PersistWithin is the default window of a deferred commit in milliseconds, read at each use (settings.progress.persistWithinMs);
	// false means the default.
	PersistWithin func() (float64, bool)
	// holdOrder is the documents with held changes, in the order they were first held; each is pinned in
	// the cache until its changes are stored (held.go).
	// TrackDropped makes the Session list the incarnations that leave the cache (TakeDropped); dropped is the list.
	TrackDropped bool
	dropped      []DroppedDoc
	holdOrder    []*loadedDoc
	holdTimer    holdTimer
	holdArmed    bool
	// holdFiredFn and holdJobFn are the timer's callbacks, made once; scratchHolds is reused by each store of held changes.
	holdFiredFn  func()
	holdJobFn    func() error
	scratchHolds []*loadedDoc
	// holdsPart is the part of the store of held changes the running job admitted: its outcome is the job's when it runs again.
	holdsPart *Part
	// DocumentRetention is the document retention in milliseconds read at each use (HarnessSettings.documentRetentionMs); nil or
	// a nil result means the default.
	DocumentRetention func() *float64
	failure           error
	// OnFail reports the storage error that failed the Session, once (session.ts fail).
	OnFail func(error)
	// failedListeners run when the Session fails, before it seals (session.ts `failed`, rejected before close runs).
	failedListeners []*func(error)
	closed          bool

	// tails is the newest entry and head marker of the conversations used lately, kept from the stored commits.
	tails conversationTails
	// records is which table records the Session's cache holds (cached-storage.ts).
	records recordCache
	// taskDocuments is the current documents of each live task the Session created, kept from the stored commits.
	taskDocuments map[ID]map[ID]*jv.Object

	commitListeners []func(*Publication)
	closeListeners  []func()

	// inJob is true while a Loop line job body runs.
	inJob bool
	// loop is the Loop whose line jobs commit through this Session: the writer's waits are its ticks. Nil: nothing waits, and a commit
	// is stored and published as it is admitted.
	loop   *Loop
	writer Writer
	// gated is the Store the line jobs read through (store).
	gated gatedStore
	// part is the part the running line job admitted, which the Loop takes when the job's body ends.
	part *Part
	// CommitBatches, when set, is the Storage call of the writer: the host's Storage stores the batches in order and answers through
	// receive (a durability receipt per batch, in order) and done (the call's result, or the error of a call that rejected), in the tick
	// the writer resumes from `await storage.commit(...)`. Without it the writer commits batch by batch through the Store, and the Loop's
	// StoreTicks say when.
	CommitBatches func(batches [][]Write, nextID ID, receive func(index int, seq Seq), done func(CommitResult, error))
	// Publishing, when set, is the Storage's own scope for a publication (Pi's optional Storage.publishing): the writer calls it with each part's
	// publication before it runs it. Returning true says the host runs publish itself, once, later; the writer then goes on from there. Returning
	// false makes the writer publish directly (publish may also have been called already).
	Publishing func(publish func()) bool
	// HeldHost, when set, is the host's side of the adoption of a commit held in memory (Pi's Transaction.adoptHeld): the writer calls it
	// before it publishes the part, and publishes it, or fails the Session with the error, when done is told. done is told once, in the
	// step that carries the host's answer.
	HeldHost interface{ AdoptHeld(done HeldAdopter) }
	// failIndex is the line index of the job whose outcome the failure is: every later job's outcome is SessionFailed, except
	// ownOutcomes (session.ts #failAt, #ownOutcomes).
	failIndex   int
	ownOutcomes map[int]bool
	// job is what the running line job charged in Pi's await ticks beside its transaction's (JobTicks).
	job JobTicks
	// closeWritten is set once the writes at close were admitted.
	closeWritten bool
	// MembershipKeys, when set, is the keys a commit writes besides its records' (Transaction.writeKeys): the Scheduler adds the ownership
	// memberships its task changes write (scheduler.ts membershipKeys). An error restarts the job (*NeedRead, *NeedPublication) or fails it.
	MembershipKeys func(tx *Tx) ([]string, error)
	// unpublished counts the documents loaded by the running or finishing line job that Loaded does not yet report.
	unpublished int
	// HostAdmits: the host admits calls itself, so the seal does not reject a call it admitted before the seal (assertUsable).
	HostAdmits bool
	// ReadOnly refuses every commit with writes and every scheduling call (HarnessOptions.readOnly): ReadOnlyError, before the store,
	// so the Session stays healthy.
	ReadOnly bool

	// ConversationCreated runs inside every transaction that creates or forks a
	// conversation, after the conversation record is staged. A plain Session
	// stages nothing; a Harness stages its built-in documents.
	ConversationCreated func(tx *Tx, record *jv.Object) error

	// lastCheckpoint and lastOutcome are the latest initial checkpoint a created task took and outcome a task committed (shared).
	lastCheckpoint, lastOutcome *jv.Object
}

// New returns a Session over a store. nextID is the next unminted record ID
// and now the Harness clock.
func New(store Store, nextID ID, now func() float64) *Session {
	s := &Session{raw: store, now: now, nextID: nextID, docs: map[string]*loadedDoc{}, lastTicks: -1, failIndex: -1}
	s.mints.s = s
	s.gated = gatedStore{Store: store, s: s}
	s.store = &s.gated
	s.writer.init(s)
	s.tails.init()
	s.records.init()
	return s
}

// commitBatches is the Storage call over the Store: the batches of parts in order, each as one atomic commit checked against the state the
// earlier ones left. It stops at the first batch a check refuses (the batches before it are durable) and at the first error of the Store
// (an unknown outcome); receive is called for every durable batch, in order.
func (s *Session) commitBatches(parts []*Part, nextID ID, receive func(index int, seq Seq)) (CommitResult, error) {
	if c, ok := s.raw.(callStarter); ok {
		c.BeginCall()
	}
	for i, p := range parts {
		seq, err := s.raw.Commit(p.Writes, nextID)
		if err != nil {
			if refused, ok := errors.AsType[*RefusedError](err); ok {
				return CommitResult{Stored: i, Refused: refused.Err}, nil
			}
			return CommitResult{Stored: i}, err
		}
		receive(i, seq)
	}
	return CommitResult{Stored: len(parts)}, nil
}

// Writer is the Session's writer, whose parts the line admits (Loop).
func (s *Session) Writer() *Writer { return &s.writer }

// NextID returns the next unminted record ID.
func (s *Session) NextID() ID { return s.nextID }

// mintClock counts down the IDs minted ahead of Pi's clock (Session.latent); one chain of ticks per ID resumes it.
type mintClock struct{ s *Session }

func (m *mintClock) Resume() {
	if m.s.latent > 0 {
		m.s.latent--
	}
}

// mintsAt notes that the ID just minted is minted by Pi's Storage `after` ticks from now, or at that offset of the running job's duration
// when inJob is set (a body that runs the whole callback at the job's start). An ID the callback minted before it ran again is minted by Pi
// once.
func (s *Session) mintsAt(after int, inJob bool) {
	if s.keptNow() > 0 {
		s.kept--
		return
	}
	if s.loop == nil || !s.inJob {
		return
	}
	if inJob {
		after = s.loop.Until(after)
	}
	if after > 0 {
		s.latent++
		s.loop.Wait(after, &s.mints)
	}
}

// storageNextID is the next ID Pi's Storage would mint now: the IDs a job's callback has not reached yet are not minted, and the ones
// an earlier run of the callback minted already are.
func (s *Session) storageNextID() ID { return s.nextID + ID(s.keptNow()) - ID(s.latent) }

// keptNow is Session.kept of the job on the line: a job that ended without minting them all again leaves none behind.
func (s *Session) keptNow() int {
	if s.kept > 0 && (s.loop == nil || s.keptJob != s.loop.cur) {
		s.kept = 0
	}
	return s.kept
}

// restoreMints takes back the IDs tx minted, as its callback runs again from its start: Pi's callback does not run again, it goes on from
// the touch it waited at, so the IDs the run minted before it stay minted.
func (s *Session) restoreMints(tx *Tx) {
	s.kept = s.keptNow() + int(s.nextID-tx.mintStart)
	if s.loop != nil {
		s.keptJob = s.loop.cur
	}
	s.nextID = tx.mintStart
}

// Failure returns the first storage error that failed the Session, or nil.
func (s *Session) Failure() error { return s.failure }

// fail fails the Session with err unless it already failed, at the job on the line: the Session seals, so its close listeners end what
// they hold, and the first error is reported once after them (session.ts fail: close, then report).
func (s *Session) fail(err error) { s.failAt(err, s.failBoundary(), nil) }

// failBoundary is the line index of the job whose outcome a failure is by default: the job on the line, else the last one whose outcome
// was delivered.
func (s *Session) failBoundary() int {
	if s.loop != nil {
		return s.loop.boundary()
	}
	return -1
}

// failAt fails the Session with err; at is the line index of the first job whose outcome the failure is, and own the jobs after it
// whose outcome is still their own error (the other parts of a call that rejected), not SessionFailed.
func (s *Session) failAt(err error, at int, own []int) {
	if s.failure != nil {
		return
	}
	s.failure = err
	s.failIndex = at
	for _, index := range own {
		if s.ownOutcomes == nil {
			s.ownOutcomes = map[int]bool{}
		}
		s.ownOutcomes[index] = true
	}
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

// poisoned reports whether job index comes after the failure's boundary without an outcome of its own: it gets SessionFailed.
func (s *Session) poisoned(index int) bool {
	return s.failure != nil && index > s.failIndex && !s.ownOutcomes[index]
}

// Fail fails the Session with err, as a failing Storage call does (session.ts fail): the host's Storage rejected a commit it had
// adopted, which stays applied and published.
func (s *Session) Fail(err error) { s.fail(err) }

// OnFailed registers f to run once with the error that fails the Session, before the seal (a race against session.ts `failed`),
// and returns its removal. It never runs for a Session that has already failed: the caller reads Failure.
func (s *Session) OnFailed(f func(error)) (remove func()) {
	p := &f
	s.failedListeners = append(s.failedListeners, p)
	return func() {
		s.failedListeners = slices.DeleteFunc(s.failedListeners, func(q *func(error)) bool { return q == p })
	}
}

// WriteTasks writes complete task records straight to the Storage, through the writer, with nothing published and the records not
// stamped (session.ts writeAtClose). It runs on the line, after every commit admitted before is published; it writes nothing once the
// Session failed, and returns a *NeedPublication while its writes are stored, which runs the job again; a failing Storage fails the
// Session.
func (s *Session) WriteTasks(records []*jv.Object) error {
	if s.failure != nil || s.closeWritten {
		return nil
	}
	if g := s.writer.Drained(); g != nil {
		return s.needPublication(g)
	}
	if len(records) == 0 {
		return nil
	}
	writes := make([]Write, len(records))
	for i, rec := range records {
		writes[i] = Write{Kind: WriteTask, Record: rec}
	}
	part := s.newPart(partPlain, writes, nil, nil)
	s.closeWritten = true
	s.writer.Admit(part)
	return s.waitFor(part)
}

// Seal is the synchronous start of Session.close (session.ts close): admission closes, so later operations fail with
// ErrClosed when they are enqueued (Admit), and the close listeners run once. Jobs admitted before keep running.
func (s *Session) Seal() {
	if s.closed {
		return
	}
	s.closed = true
	s.cancelHoldTimer()
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
	s.taskDocuments = nil
	s.clearIdle()
	s.attached = nil
	s.closeHolds()
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
		return s.writer.sessionFailed(s.failure)
	}
	return nil
}

// Commit runs change on the Session line as one commit and returns the commit's
// publication when the commit was published as it was admitted (a Session without a Loop). A callback that errors stages nothing: the
// error is returned and IDs it minted stay consumed, as a rolled-back Pi callback leaves a gap.
// When any read returns a *NeedRead or a *NeedPublication the callback restarts from scratch after
// the host answered or the key's writer was published: Commit restores the ID counter and returns it.
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
	return s.Finish(tx)
}

// Begin opens a transaction whose callback runs outside this call: a host drives it one operation at a time (ABI
// section 8) and ends it with Finish or Abandon. Commit is Begin, the callback, and Finish.
func (s *Session) Begin(scope TxScope) (*Tx, error) {
	if err := s.assertHealthy(); err != nil {
		return nil, err
	}
	return newTx(s, scope), nil
}

// restarts reports whether err makes the callback that returned it run again from its start: a read the host must answer, or a key an
// unpublished commit writes.
func restarts(err error) bool {
	if _, need := IsNeedRead(err); need {
		return true
	}
	return IsNeedPublication(err)
}

// Abandon seals a transaction whose callback failed with err; it stages nothing. A restarting failure restores the ID
// counter, since the callback runs again from scratch.
func (s *Session) Abandon(tx *Tx, err error) {
	s.lastTicks = tx.ticks
	tx.settleFailure()
	if restarts(err) {
		s.restoreMints(tx)
		return
	}
	// A restarted callback has not ended the commit; any other failure has (session.ts #runCommit).
	s.sweepDocuments()
}

// Finish settles a transaction whose callback succeeded and admits its writes to the writer, as the tail of Commit: the commit is
// stored and published by the writer, later. It returns the publication only when the writer published it before it returned.
func (s *Session) Finish(tx *Tx) (*Publication, error) {
	// A transaction the host drove (not RunCommit's callback) has charged its Transaction awaits as its operations ran: the job's duration is theirs.
	s.lastTicks = tx.ticks
	defer func() { s.settleReads, s.settleCached = 0, 0 }()
	writes := tx.settledWrites
	if !tx.settledOK {
		var err error
		if err = tx.touchSettle(); err == nil {
			writes, err = tx.settleSuccess()
		}
		if err != nil {
			switch {
			case !restarts(err):
				s.sweepDocuments()
			case !tx.scope.Host:
				s.restoreMints(tx)
			}
			return nil, err
		}
		tx.settledWrites, tx.settledOK = writes, true
		s.job.SettleReads += tx.settleReads
		s.job.SettleCached += tx.settleCached
		s.job.SettleOwners += tx.settleOwners
	}
	s.settleReads, s.settleCached = tx.settleReads, tx.settleCached
	return s.commitWrites(tx, writes)
}

//go:noinline
func (s *Session) commitWrites(tx *Tx, writes []Write) (*Publication, error) {
	if len(writes) == 0 {
		tx.abortChanges()
		s.sweepDocuments()
		return nil, nil
	}
	if s.ReadOnly {
		// Refused before the store: the Session stays healthy, and the caller sees why (session.ts #runCommit).
		tx.abortChanges()
		s.sweepDocuments()
		return nil, errReadOnlyCommit
	}
	if tx.scope.Deferred && tx.holdable(writes) {
		// Adopted in memory when the commits before it are published, as a part of no storage call.
		return s.admit(s.newPart(partHold, nil, tx.writeKeys(writes), tx))
	}
	membership, err := s.membershipKeys(tx)
	if err != nil {
		if restarts(err) && tx.scope.Host {
			// The host's transaction cannot run its callback again: it keeps its settled state and finishes again once the key is published.
			return nil, err
		}
		tx.abortChanges()
		if restarts(err) {
			s.restoreMints(tx)
		} else {
			s.sweepDocuments()
		}
		return nil, err
	}
	// Logical admission. Held changes leave their queue only here, once nothing but Storage can fail; a commit never stores a hold adopted
	// after it in line order. Taken before the keys, so the documents it stores held changes of are its writes too: a later job that reads
	// one waits for it instead of storing them again.
	writes = tx.takeHeld(writes)
	keys := append(tx.writeKeys(writes), membership...)
	return s.admit(s.newPart(partCommit, writes, keys, tx))
}

// newPart makes the part of a commit the line admits, at the index of the job on the line.
func (s *Session) newPart(kind partKind, writes []Write, keys []string, tx *Tx) *Part {
	return &Part{Index: s.jobIndex(), Writes: writes, Keys: keys, kind: kind, tx: tx}
}

// admit gives the writer a part, and the line job that made it the part to wait for: its outcome is the job's.
func (s *Session) admit(part *Part) (*Publication, error) {
	if s.inJob {
		// The Loop admits it when the job's work returns (Loop.workReturned).
		s.part = part
		return nil, nil
	}
	s.writer.Admit(part)
	s.sweepDocuments()
	return part.pub, part.err
}

// jobIndex is the line index of the job on the line.
func (s *Session) jobIndex() int {
	if s.loop != nil {
		return s.loop.boundary()
	}
	return 0
}

// membershipKeys is the keys a commit writes besides its records' (Transaction.writeKeys), at its admission, on the line: a Harness
// adds the ownership memberships its task changes write. A plain Session adds none.
func (s *Session) membershipKeys(tx *Tx) ([]string, error) {
	if s.MembershipKeys == nil {
		return nil, nil
	}
	return s.MembershipKeys(tx)
}

// adoptPart publishes a stored commit (Writer): adopt it in memory and run its publication. An error of the adoption leaves memory behind
// durable state; the writer fails the Session with it.
func (s *Session) adoptPart(tx *Tx, writes []Write, seq Seq, part *Part) error {
	docs, storedDocs, err := tx.adopt(seq)
	if err != nil {
		return err
	}
	if len(tx.taken) > 0 {
		s.release(tx.taken)
	}
	changes := make([]Change, 0, len(writes)+len(docs))
	for _, w := range writes {
		switch w.Kind {
		case WriteConversation, WriteEntry, WriteTask, WriteSubmission:
			changes = append(changes, Change{Kind: ChangeTable, Table: w.Kind, Record: w.Record})
		}
	}
	tables := len(changes)
	changes = append(changes, docs...)
	pub := Publication{Seq: seq, Changes: changes, Span: tx.scope.Span, Created: tx.createdTasks()}
	if storedDocs != nil {
		pub.Stored = append(append([]Change(nil), changes[:tables]...), storedDocs...)
		docs = storedDocs
	}
	for _, change := range docs {
		s.touchDocument(addressID(recordAddress(change.Record)))
	}
	part.pub = s.emitPublication(pub)
	return nil
}

// emitPublication is the one place a Publication is allocated: it runs the commit listeners on it. The one allocation site is what
// budget/allocs.json counts; a per-caller copy would add a site for each.
func (s *Session) emitPublication(p Publication) *Publication {
	pub := &p
	s.publish(pub)
	return pub
}

// InJob reports whether a Loop line job body is running.
func (s *Session) InJob() bool { return s.inJob }

// takePart returns the part the job that just ended admitted, nil when none.
func (s *Session) takePart() *Part {
	p := s.part
	s.part = nil
	return p
}

// publish runs the commit listeners on a publication (session.ts #publish).
//
//go:noinline
func (s *Session) publish(pub *Publication) {
	if !pub.Memory {
		s.tails.apply(pub.StoredChanges())
		s.applyRecords(pub.StoredChanges())
		s.indexTaskDocuments(pub)
	}
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
	// What an unpublished commit wrote at the address is in neither the cache nor what the Store shows yet.
	if err := s.Touch(DocumentKey(id)); err != nil {
		return nil, err
	}
	if cached := s.docs[id]; cached != nil {
		if cached.valueVersion == def.Version {
			s.touchDocument(id)
			return cached, nil
		}
		// The reload must find what memory holds: held changes are stored before their tracker goes.
		if err := s.dropHeld(cached); err != nil {
			return nil, err
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
	s.docEpochs++
	l := &loadedDoc{
		addressID: id, record: stored.Record, storedVersion: stored.Version, valueVersion: def.Version,
		deltasSinceBase: stored.DeltasSinceBase, tracker: delta.Track(value), epoch: s.docEpochs,
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
	id := addressID(addr)
	// A cached tracker of the definition's version is read as it is: no touch of the document's key, so an unpublished commit that
	// wrote the document does not hold the read (session.ts snapshot).
	l := s.docs[id]
	if l != nil && l.valueVersion == def.Version {
		s.touchDocument(id)
	} else {
		var err error
		if l, err = s.loadDocument(def, id, addr); err != nil || l == nil {
			return nil, err
		}
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

// UnloadDocuments drops every cached document, held changes stored first; later access loads from the store. A *NeedPublication error
// means the held changes (or the commits before them) are not published yet: nothing was dropped, and the call is made again once they are.
func (s *Session) UnloadDocuments() error {
	if err, ok := s.HoldsOutcome(); ok && err != nil {
		return err
	}
	if s.failure == nil {
		if err := s.StoreHolds(); err != nil {
			return err
		}
	}
	if s.TrackDropped {
		for _, cached := range s.docs {
			s.dropped = append(s.dropped, DroppedDoc{ID: mustInt(cached.record, "id"), Address: cached.addressID})
		}
	}
	s.docs = map[string]*loadedDoc{}
	s.clearIdle()
	return nil
}

// SetHopProfile names the Storage profile whose await ticks transactions are charged (open.storageProfile).
func (s *Session) SetHopProfile(p hopcost.Profile) { s.hop = p }

// SettleReads is the ticks of the reads the settle of the transaction being committed made (hopcost.Settle); it is 0 outside a commit.
func (s *Session) SettleReads() int { return s.settleReads }

// SettleCached is the replaced tasks the settle of the transaction being committed found already read by its callback: their
// committed read costs one tick, not a Storage read; 0 outside a commit.
func (s *Session) SettleCached() int { return s.settleCached }

// JobTicks is what the line job that ended charged, in Pi's await ticks (hopcost.Job): its change callback's (Change, -1 when no transaction
// ran) and the Storage reads and awaits of its settle.
type JobTicks struct {
	Change int
	// SettleReads is the ticks of the settle's reads, SettleCached the replaced tasks the callback had read already, SettleOwners the ticks of
	// validateOwners (hopcost.Settle).
	SettleReads, SettleCached, SettleOwners int
	// Membership is the awaits of the commit's membershipKeys beyond the settle's: they delay its admission.
	Membership int
}

// takeJobTicks returns what the job that just ended charged and clears it.
func (s *Session) takeJobTicks() JobTicks {
	t := s.job
	t.Change = s.TakeTicks()
	s.job = JobTicks{}
	return t
}

// ChargeRead charges n ticks of a read job's own awaits (a line job without a transaction).
func (s *Session) ChargeRead(n int) {
	if s.lastTicks < 0 {
		s.lastTicks = 0
	}
	s.lastTicks += n
}

// PeekTicks is the Change of the transaction callback that just succeeded, without clearing it: -1 when none ran.
func (s *Session) PeekTicks() int { return s.lastTicks }

// TakeTicks returns and clears the Change of the last transaction callback that succeeded on the line: the ticks of
// Pi's change callback for its Transaction method awaits and Scheduler wrapper (hopcost), -1 when none ran.
func (s *Session) TakeTicks() int {
	n := s.lastTicks
	s.lastTicks = -1
	return n
}
