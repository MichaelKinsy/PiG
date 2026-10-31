package documents

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/documents/transaction.ts
//
// Transactions (§9, R12, R13): drafts of an object's documents, prepared and adopted in one batch, with Chord's own
// staged changes (job runs, §15), fenced by the token they run under; they run on the object's line (line.go).
//
// Async contract: Draft (of a cold persisted document), Retire (of one), Get, Page, Rows and Commit block, outside the
// host's sections, in the order Pi awaits; every other method is a section. Each `tx.*` read counts as pending until
// it returns (rule 3): a callback that settles while another goroutine of its own still has one underway is refused.
// A Transaction is used by its callback's goroutine and, through Cut and Discard, by the host.
//
// Go forced differences: the table type parameters and the `draft`/`retire` overloads are designed out (see the
// package documentation); the table handle is the store's *TableHandle; `rows` is a TxRows iterator.

// Tx is what `self.tx` gives its callback. Inside it: only `tx.*`, no other `self.tx`, no calls to other objects.
type Tx interface {
	// Draft is the document's draft: the same one for the same document within one transaction. handle is a
	// SingletonHandle (no key) or a KeyedHandle with its key.
	Draft(handle any, key ...string) (*delta.Object, error)
	// Retire retires the document when the transaction commits (its address reads `initial()` again); a no-op when it
	// is absent.
	Retire(handle any, key ...string) error
	// Start starts a run of job, one of this object's jobs, with this transaction's other writes (§15): both commit or
	// neither. It returns its Run at once; a discarded transaction's Run fails `released` from Outcome. A key of an
	// unsettled run (one started earlier in this transaction too) returns that run.
	Start(job JobHandle, args any, options ...StartOptions) (Run, error)
	// Get is the row with key as this transaction leaves it (its own put, update, delete overlaid; W10).
	Get(table *store.TableHandle, key any) (store.Row, error)
	// Page is a page of committed rows, stable for the transaction (without its own writes).
	Page(table *store.TableHandle, q *store.Query, limit int64, options ...store.PageOptions) (store.Page, error)
	// Rows is every committed row of q, as Page.
	Rows(table *store.TableHandle, q *store.Query) *TxRows
	// Put writes row when the transaction commits (an upsert: the whole row replaced).
	Put(table *store.TableHandle, row any) error
	// Update changes the row with key when the transaction commits; an absent key changes nothing.
	Update(table *store.TableHandle, key, set any) error
	// Delete deletes the row with key when the transaction commits.
	Delete(table *store.TableHandle, key any) error
	// UpdateWhere changes every row matching where when the transaction commits (one statement, its count recorded).
	UpdateWhere(table *store.TableHandle, where, set any) error
	// DeleteWhere deletes every row matching where when the transaction commits.
	DeleteWhere(table *store.TableHandle, where any) error
}

// Staged is Chord's own changes staged in a transaction (a job's run records): adopted in its commit step with the
// documents, then told how the transaction ended, once the owner's line is free.
type Staged interface {
	// Adopt adopts the changes, inside the commit's batch.
	Adopt() error
	// Committed is: the transaction committed: the actions after the commit (a run starts, waiters settle).
	Committed()
	// Discarded is: the transaction was discarded: nothing was adopted.
	Discarded()
}

type draftState struct {
	tracker *delta.Tracker
	change  *delta.Change
	// state is the draft handle `change.state` is in Pi: one per change, the same for every use.
	state *delta.Object
}

// Entry is what one transaction does with one document: a draft, a retirement, or both (retired, then drafted anew);
// found is what is stored of a cold persisted document it retires without loading (R13).
type Entry struct {
	slot   DocumentSlot
	key    string
	draft  *draftState
	retire bool
	found  *Stored
}

// adoption is one document change a commit adopts: a removal, a draft's prepared change, or both; and for a persisted
// document what the commit stores of it.
type adoption struct {
	entry    *Entry
	prepared *delta.Prepared
	retires  bool
	stored   *StoredChange
}

type txState int

const (
	txOpen txState = iota
	txCommitting
	txStepping
	txCommitted
	txDiscarded
	txCut
)

type txWrites struct {
	store  *store.ChordStore
	staged []store.StagedWrite
}

// Transaction is one transaction of an object, under the seal token of whoever runs it (an attempt, a job run's
// invocation, the object). Commit prepares every draft (Delta records ops and checks only placed values) and adopts
// the changed documents and the staged changes in one batch; drafts are revoked once it settles. Cut at its token's
// seal, it discards everything, releases the line and refuses every later use.
type Transaction struct {
	documents *Documents
	token     *SealToken
	release   func()
	ctx       context.Context
	entries   map[DocumentSlot]map[string]*Entry
	entryList []*Entry
	// staged is Chord's own staged changes, by their owner (a job); made by the first.
	staged     map[any]Staged
	stagedList []Staged
	// writes are the store writes, in staging order, and the store they commit to; made by the first (the flag that
	// sends the commit through the store's step, W12).
	writes *txWrites
	// tables are the tables written, each with its writes and overlay.
	tables map[*store.TableHandle]*store.TableChanges
	// pending counts the `tx.*` reads underway: a callback settling with one is refused (rule 3).
	pending int
	// persistence is when its commit reaches the store (a run state's deferred change, D8).
	persistence CommitPersistence
	state       txState
	// queued rejects the commit while it waits in the store's queue (cut): its caller is told at once (rule 3).
	queued chan error
}

// NewTransaction is a transaction of documents under token, whose line release is release.
func NewTransaction(documents *Documents, token *SealToken, release func(), ctx context.Context, persistence CommitPersistence) *Transaction {
	if persistence.Kind == "" {
		persistence = Immediate
	}
	return &Transaction{
		documents: documents, token: token, release: release, ctx: ctx, persistence: persistence,
		entries: map[DocumentSlot]map[string]*Entry{},
	}
}

// Token is the seal token the transaction runs under.
func (tx *Transaction) Token() *SealToken { return tx.token }

// Documents is the object whose transaction this is.
func (tx *Transaction) Documents() *Documents { return tx.documents }

// StageStore is the store writes of this transaction, which writes join (Chord's own documents: a job's run records):
// made by the first, which sends the commit through chordStore's step.
func (tx *Transaction) StageStore(chordStore *store.ChordStore) *[]store.StagedWrite {
	if tx.writes == nil {
		tx.writes = &txWrites{store: chordStore}
	}
	return &tx.writes.staged
}

// keyed is what a slot of either kind says of a key.
type keyer interface {
	Key(key string) (string, error)
}

func (tx *Transaction) slotOf(handle any) (slot DocumentSlot, err error) {
	// Handles are Chord's own: `self.state` made them.
	switch typed := handle.(type) {
	case *Singleton:
		if err := typed.Slot.definition.AssertVersion(typed.Version); err != nil {
			return nil, err
		}
		return typed.Slot, nil
	case *Keyed:
		if err := typed.Slot.definition.AssertVersion(typed.Version); err != nil {
			return nil, err
		}
		return typed.Slot, nil
	}
	return nil, fmt.Errorf("%s: a transaction drafts only documents of handles self.state made", tx.documents.Label())
}

func keyOf(keys []string) string {
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// Draft is the draft of the document of handle (at key, for a keyed one).
func (tx *Transaction) Draft(handle any, keys ...string) (*delta.Object, error) {
	var slot DocumentSlot
	var at string
	var err error
	// Keyed keys follow the text rules (KeyedSlot.Key); a singleton's key is always "".
	tx.documents.Do(func() {
		if slot, err = tx.slotOf(handle); err != nil {
			return
		}
		at = keyOf(keys)
		if at != "" {
			at, err = slot.(keyer).Key(at)
		}
	})
	if err != nil {
		return nil, err
	}
	return tx.DraftSlot(slot, at)
}

// DraftSlot is the draft of the document at key of slot (Chord's own slots, a job's run states). A cold persisted
// document is loaded first, inside the transaction (it holds the line); the draft counts as pending until it returns
// (rule 3). A warm draft returns at once.
func (tx *Transaction) DraftSlot(slot DocumentSlot, key string) (*delta.Object, error) {
	var draft *delta.Object
	var err error
	var cold bool
	tx.documents.Do(func() {
		var e *Entry
		if e, err = tx.entry(slot, key); err != nil {
			return
		}
		persisted := slot.Persisted()
		if e.draft != nil || e.retire || persisted == nil || !persisted.Cold(key) {
			draft, err = tx.begin(e)
			return
		}
		tx.pending++
		cold = true
	})
	if !cold {
		return draft, err
	}
	defer tx.documents.Do(func() { tx.pending-- })
	if err := slot.Persisted().Load(key, tx.ctx, tx.token); err != nil {
		return nil, err
	}
	tx.documents.Do(func() {
		if err = tx.assertOpen(); err != nil {
			return
		}
		var e *Entry
		e, _ = tx.entry(slot, key)
		draft, err = tx.begin(e)
	})
	return draft, err
}

// Retire retires the document of handle (at key, for a keyed one) when the transaction commits.
func (tx *Transaction) Retire(handle any, keys ...string) error {
	var slot DocumentSlot
	var at string
	var e *Entry
	var err error
	tx.documents.Do(func() {
		if slot, err = tx.slotOf(handle); err != nil {
			return
		}
		at = keyOf(keys)
		if at != "" {
			if at, err = slot.(keyer).Key(at); err != nil {
				return
			}
		}
		e, err = tx.RetireSlot(slot, at)
	})
	if err != nil {
		return err
	}
	persisted := slot.Persisted()
	// A cold persisted document is found, not loaded (one read; absent: a no-op, R13); a version or fingerprint the
	// code cannot read fails the owner as a load would (R15): a retirement of it is no exception.
	var cold bool
	tx.documents.Do(func() { cold = e.found == nil && persisted != nil && persisted.Cold(at) })
	if !cold {
		return nil
	}
	tx.documents.Do(func() { tx.pending++ })
	found, err := persisted.Persistence().Find(at, tx.ctx)
	tx.documents.Do(func() { tx.pending-- })
	if err != nil {
		return err
	}
	tx.documents.Do(func() {
		// Cut meanwhile: its attempt was sealed, and failing the owner now would fail its successor.
		if err = tx.assertOpen(); err != nil {
			return
		}
		if found != nil {
			if err = tx.checkFound(found, persisted, slot); err != nil {
				return
			}
			stored := found.Stored
			e.found = &stored
		}
	})
	return err
}

// RetireSlot retires the document at key of slot when the transaction commits.
func (tx *Transaction) RetireSlot(slot DocumentSlot, key string) (*Entry, error) {
	e, err := tx.entry(slot, key)
	if err != nil {
		return nil, err
	}
	if e.draft != nil {
		e.draft.change.Abort()
	}
	e.draft = nil
	e.retire = true
	return e, nil
}

// Start stages a run of job in this transaction.
func (tx *Transaction) Start(job JobHandle, args any, options ...StartOptions) (Run, error) {
	var option StartOptions
	if len(options) > 0 {
		option = options[0]
	}
	var run Run
	var err error
	// Job handles are Chord's own: `self.job` made them.
	tx.documents.Do(func() { run, err = job.Stage(tx, args, tx.ctx, option) })
	return run, err
}

// Get is the row with key as this transaction leaves it.
func (tx *Transaction) Get(table *store.TableHandle, key any) (store.Row, error) {
	var handle *store.TableHandle
	var changes *store.TableChanges
	var err error
	tx.documents.Do(func() {
		if handle, err = tx.handle(table); err == nil {
			changes = tx.tables[handle]
		}
	})
	if err != nil {
		return nil, err
	}
	tx.documents.Do(func() { tx.pending++ })
	defer tx.documents.Do(func() { tx.pending-- })
	if changes == nil {
		return handle.Get(key, tx.ctx)
	}
	return changes.Get(key, tx.ctx)
}

// Page is a page of committed rows, stable for the transaction (without its own writes).
func (tx *Transaction) Page(table *store.TableHandle, q *store.Query, limit int64, options ...store.PageOptions) (store.Page, error) {
	var option store.PageOptions
	if len(options) > 0 {
		option = options[0]
	}
	var handle *store.TableHandle
	var err error
	tx.documents.Do(func() { handle, err = tx.handle(table) })
	if err != nil {
		return store.Page{}, err
	}
	tx.documents.Do(func() { tx.pending++ })
	defer tx.documents.Do(func() { tx.pending-- })
	return handle.Page(q, limit, tx.ctx, option)
}

// TxRows is every committed row of a query, read a page of store.RowsPage at a time (Pi's `rows` AsyncIterable).
type TxRows struct {
	tx       *Transaction
	table    *store.TableHandle
	q        *store.Query
	buffer   []store.Row
	after    *string
	finished bool
}

// Rows is every committed row of q, as Page.
func (tx *Transaction) Rows(table *store.TableHandle, q *store.Query) *TxRows {
	return &TxRows{tx: tx, table: table, q: q}
}

// Next is the next row; ok is false once every row was read.
func (rows *TxRows) Next(ctx context.Context) (store.Row, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, context.Cause(ctx)
	}
	for len(rows.buffer) == 0 {
		if rows.finished {
			return nil, false, nil
		}
		options := store.PageOptions{After: rows.after}
		page, err := rows.tx.Page(rows.table, rows.q, store.RowsPage, options)
		if err != nil {
			rows.finished = true
			return nil, false, err
		}
		rows.buffer, rows.after = page.Items, page.Next
		rows.finished = page.Next == nil
	}
	row := rows.buffer[0]
	rows.buffer = rows.buffer[1:]
	return row, true, nil
}

// Close ends the iteration.
func (rows *TxRows) Close() {
	rows.finished = true
	rows.buffer = nil
}

func (tx *Transaction) withChanges(table *store.TableHandle, write func(changes *store.TableChanges) error) error {
	var err error
	tx.documents.Do(func() {
		var changes *store.TableChanges
		if changes, err = tx.changes(table); err == nil {
			err = write(changes)
		}
	})
	return err
}

// Put writes row when the transaction commits (an upsert: the whole row replaced).
func (tx *Transaction) Put(table *store.TableHandle, row any) error {
	return tx.withChanges(table, func(changes *store.TableChanges) error { return changes.Put(row) })
}

// Update changes the row with key when the transaction commits; an absent key changes nothing.
func (tx *Transaction) Update(table *store.TableHandle, key, set any) error {
	return tx.withChanges(table, func(changes *store.TableChanges) error { return changes.Update(key, set) })
}

// Delete deletes the row with key when the transaction commits.
func (tx *Transaction) Delete(table *store.TableHandle, key any) error {
	return tx.withChanges(table, func(changes *store.TableChanges) error { return changes.Delete(key) })
}

// UpdateWhere changes every row matching where when the transaction commits.
func (tx *Transaction) UpdateWhere(table *store.TableHandle, where, set any) error {
	return tx.withChanges(table, func(changes *store.TableChanges) error { return changes.UpdateWhere(where, set) })
}

// DeleteWhere deletes every row matching where when the transaction commits.
func (tx *Transaction) DeleteWhere(table *store.TableHandle, where any) error {
	return tx.withChanges(table, func(changes *store.TableChanges) error { return changes.DeleteWhere(where) })
}

// Stage is the changes owner staged in this transaction, made by make on its first use. A section.
func (tx *Transaction) Stage(owner any, make func() Staged) (Staged, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	if tx.staged == nil {
		tx.staged = map[any]Staged{}
	}
	staged := tx.staged[owner]
	if staged == nil {
		staged = make()
		tx.staged[owner] = staged
		tx.stagedList = append(tx.stagedList, staged)
	}
	return staged, nil
}

// StagedOf is the changes owner staged in this transaction so far, nil if none.
func (tx *Transaction) StagedOf(owner any) Staged { return tx.staged[owner] }

// Commit prepares every draft and adopts what changed, and the staged changes, in one batch; nothing when nothing
// changed. A document retired and drafted anew is removed, then adopted: its frame carries the retirement. Persisted
// documents stage their writes here (rule 8), which sends the commit through the store's step and blocks on it.
func (tx *Transaction) Commit() error {
	var adoptions []*adoption
	var err error
	tx.documents.Do(func() { adoptions, err = tx.prepareCommit() })
	if err != nil {
		return err
	}
	var writes *txWrites
	tx.documents.Do(func() { writes = tx.writes })
	if writes == nil {
		tx.documents.Do(func() {
			if err = tx.adopt(adoptions); err == nil {
				tx.state = txCommitted
			}
		})
		return err
	}
	return tx.step(writes.store, writes.staged, adoptions)
}

func (tx *Transaction) prepareCommit() ([]*adoption, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	if tx.pending > 0 {
		return nil, fmt.Errorf("%s: the callback settled before its pending tx operations", tx.documents.Label())
	}
	tx.state = txCommitting
	if err := tx.ctx.Err(); err != nil {
		return nil, context.Cause(tx.ctx)
	}
	var adoptions []*adoption
	for _, e := range tx.entryList {
		var prepared *delta.Prepared
		if e.draft != nil {
			var err error
			if prepared, err = e.draft.change.Prepare(); err != nil {
				return nil, err
			}
			if len(prepared.Ops()) == 0 {
				prepared.Abort()
				prepared = nil
			}
		}
		current, err := e.slot.Current(e.key)
		if err != nil {
			return nil, err
		}
		retires := e.retire && (current != nil || e.found != nil)
		if prepared == nil && !retires {
			continue
		}
		var stored *StoredChange
		if persisted := e.slot.Persisted(); persisted != nil {
			writes := tx.StageStore(persisted.Persistence().Store)
			was := persisted.Stored(e.key)
			if was == nil {
				was = e.found
			}
			change, err := persisted.Stage(e.key, DocumentChange{Prepared: prepared, Retires: retires}, was, writes, tx.persistence)
			if err != nil {
				return nil, err
			}
			stored = &change
		}
		adoptions = append(adoptions, &adoption{entry: e, prepared: prepared, retires: retires, stored: stored})
	}
	return adoptions, nil
}

// step commits through the store's step (§3): admitted while neither cut nor sealed (else `released`, nothing
// written), then the documents and staged changes adopted inside the step. Once queued, the caller's abort no longer
// counts; a cut while queued fails `released` at once (the queue drops the entry at its admission, writing nothing).
func (tx *Transaction) step(chordStore *store.ChordStore, staged []store.StagedWrite, adoptions []*adoption) error {
	fence := store.CommitFence{
		Admit: func() bool {
			var admitted bool
			tx.documents.Do(func() {
				if tx.state != txCommitting || tx.token.Sealed() {
					return
				}
				tx.state = txStepping
				admitted = true
			})
			return admitted
		},
		Adopt: func(int64, []int64) error {
			var err error
			tx.documents.Do(func() { err = tx.adopt(adoptions) })
			return err
		},
	}
	queued := make(chan error, 1)
	tx.documents.Do(func() { tx.queued = queued })
	result := make(chan error, 1)
	go func() {
		_, err := chordStore.Commit(staged, fence, chordctx.WithoutAbortSignal(tx.ctx))
		result <- err
	}()
	var err error
	// The race handles the commit's own later failure too.
	select {
	case err = <-result:
	case err = <-queued:
	}
	var failure error
	tx.documents.Do(func() {
		tx.queued = nil
		if err != nil {
			if tx.state == txCut {
				failure = tx.cutError()
			} else {
				failure = tx.refused(err)
			}
			return
		}
		tx.state = txCommitted
	})
	return failure
}

// refused is the error a commit the store rejected fails with (D4): the store's own (`released`, `storage_failed`) as
// it is; a refused step classified, an `int` `CHECK` failing only this transaction (`invalid/args`), any other refusal
// failing the live object `broken` too (memory and disk disagree).
func (tx *Transaction) refused(err error) error {
	refusal := store.RefusalOf(err, tx.documents.Label())
	if refusal == nil {
		return err
	}
	if refusal.Kind == store.RefusalBroken {
		tx.documents.Fail(refusal.Error)
	}
	return refusal.Error
}

// adopt adopts the changed documents (persisted ones with what the commit stored of them) and the staged changes in
// one batch; nothing when nothing changed.
func (tx *Transaction) adopt(adoptions []*adoption) error {
	if len(adoptions) == 0 && tx.staged == nil {
		return nil
	}
	return tx.documents.Batch().Commit(func() error {
		for _, a := range adoptions {
			if a.retires {
				a.entry.slot.Retire(a.entry.key)
			}
			if a.prepared == nil {
				continue
			}
			if err := a.entry.slot.Adopt(a.entry.key, a.entry.draft.tracker, a.prepared, a.stored); err != nil {
				return err
			}
		}
		for _, staged := range tx.stagedList {
			if err := staged.Adopt(); err != nil {
				return err
			}
		}
		return nil
	}, tx.ctx)
}

// Discard aborts every draft and prepared change. A section.
func (tx *Transaction) Discard() {
	if tx.state == txCut {
		return
	}
	tx.state = txDiscarded
	tx.abortDrafts()
}

// Cut is: its token was sealed while it held the line: every draft is aborted, the line released, and every later use,
// its commit included, fails `released`. A section.
func (tx *Transaction) Cut() {
	// A commit admitted to the step runs to its end; one still queued is refused at admission.
	if tx.state != txOpen && tx.state != txCommitting {
		return
	}
	tx.state = txCut
	tx.abortDrafts()
	tx.release()
	if tx.queued != nil {
		select {
		case tx.queued <- tx.cutError():
		default:
		}
	}
}

// Settle tells the staged changes how the transaction ended; runs once the object's line is free. A section.
func (tx *Transaction) Settle() {
	if tx.staged == nil {
		return
	}
	committed := tx.state == txCommitted
	for _, staged := range tx.stagedList {
		if committed {
			staged.Committed()
		} else {
			staged.Discarded()
		}
	}
}

func (tx *Transaction) abortDrafts() {
	for _, e := range tx.entryList {
		if e.draft != nil {
			e.draft.change.Abort()
		}
	}
}

func (tx *Transaction) entry(slot DocumentSlot, key string) (*Entry, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	if slot.Owner() != tx.documents {
		return nil, fmt.Errorf("%s: a transaction drafts only its own object's documents", tx.documents.Label())
	}
	entries := tx.entries[slot]
	if entries == nil {
		entries = map[string]*Entry{}
		tx.entries[slot] = entries
	}
	e := entries[key]
	if e == nil {
		e = &Entry{slot: slot, key: key}
		entries[key] = e
		tx.entryList = append(tx.entryList, e)
	}
	return e, nil
}

func (tx *Transaction) assertOpen() error {
	switch tx.state {
	case txOpen:
		return nil
	case txCut:
		return tx.cutError()
	}
	return fmt.Errorf("%s: the transaction has settled", tx.documents.Label())
}

func (tx *Transaction) cutError() error {
	return data.NewChordError(data.ChordErrorInit{
		Code: data.CodeReleased, Reason: data.ReasonDiscarded,
		Message: tx.documents.Label() + ": the transaction was discarded at the seal",
	}, nil)
}

// handle is the handle behind table, one of this object's tables (another object's is refused).
func (tx *Transaction) handle(table *store.TableHandle) (*store.TableHandle, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	// Plugin code may pass any table: only this object's handles are taken.
	if table == nil || table.Owner.Scope() != any(tx.documents) {
		return nil, fmt.Errorf("%s: a transaction writes only its own object's tables", tx.documents.Label())
	}
	if err := table.AssertVersion(); err != nil {
		return nil, err
	}
	return table, nil
}

// changes are the writes to table in this transaction, made by the first.
func (tx *Transaction) changes(table *store.TableHandle) (*store.TableChanges, error) {
	handle, err := tx.handle(table)
	if err != nil {
		return nil, err
	}
	if tx.tables == nil {
		tx.tables = map[*store.TableHandle]*store.TableChanges{}
	}
	changes := tx.tables[handle]
	if changes == nil {
		if tx.writes == nil {
			tx.writes = &txWrites{store: handle.Store}
		}
		changes = store.NewTableChanges(handle, &tx.writes.staged)
		tx.tables[handle] = changes
	}
	return changes, nil
}

// begin is the entry's draft, begun on the current document (or a fresh `initial()`) by the first use.
func (tx *Transaction) begin(e *Entry) (*delta.Object, error) {
	if e.draft == nil {
		var current *delta.Tracker
		if !e.retire {
			var err error
			if current, err = e.slot.Current(e.key); err != nil {
				return nil, err
			}
		}
		tracker := current
		if tracker == nil {
			initial, err := e.slot.Initial(tx.ctx)
			if err != nil {
				return nil, err
			}
			tracker = delta.Track(initial)
		}
		change := tracker.BeginChange()
		e.draft = &draftState{tracker: tracker, change: change, state: change.State()}
	}
	return e.draft.state, nil
}

// checkFound fails, failing slot's owner `broken`, when its definition cannot read the cold document found (R15).
func (tx *Transaction) checkFound(found *FoundDocument, persisted PersistedSlot, slot DocumentSlot) error {
	if err := CheckReadable(found.Stored.Version, found.Fingerprint, persisted.Definition().Current()); err != nil {
		failure := data.AsChordError(err, slot.Owner().Label())
		slot.Owner().Fail(failure)
		return failure
	}
	return nil
}

var _ Tx = (*Transaction)(nil)
