package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/store/store.ts
//
// The store (slice 6a §2, §3, slice 6b §1): opened over a backend, one commit queue that serializes only the commit
// step (the seal fence's admission check, `BEGIN`, the writes in their order, the outbox commit, `lastSeq` and the
// allocator's value, `COMMIT`, then the owner's adoption), the id allocator, the claims of its tables and documents,
// reads guarded as Durable's `GuardedStorage` guards them, Durable's Storage semantics for a refused step (D4), the
// stop after an unknown outcome, group commit (S18) and the commit series (several commits stored in one backend
// transaction).
//
// Async contract (Go form of Pi's promises). Pi is single-threaded and its commit queue is the `#tail` promise chain:
// every `commit`, `replay` and `take` call takes its place in the queue synchronously, at the call, and runs once the
// call before it settled; a settled queue entry never blocks the next one, whatever it rejected with. A commit
// series is one entry, and so is a group (the commits queued behind a running step, written in one backend
// transaction: S18; Durable's Session writer groups above the store too). Go keeps that
// contract with a ticket: a call takes its place under `mu` (so the queue order is the order of the calls that reach
// it) and waits for the ticket before it (a channel the previous entry closes when it settled). `Commit`, `Replay`
// and `Read` block until they settle, as the Pi promises resolve, and so does `Take`'s channel: an export selects on it
// with its context, as Pi's does while its caller awaits the promise. A caller that needs two entries queued in order
// takes the second place after the first one's call has returned, as Pi's `const a = store.commit(...); const b =
// store.commit(...)` does (`enlist` takes a commit's place at once and `Wait` settles it: tests enlist on their own
// goroutine, then wait on others). Nothing is
// cancelled once queued (Pi's `context` counts the step; it does not abort it). A read or a maintenance write runs on
// the caller's goroutine, outside the queue, as Pi's does outside it. `mu` guards the store's own state (the
// position, the catalog, the stopped state, the counters, the queue's tail); no database call and no callback runs
// under it. A `ChordStore` is safe for use from several goroutines.

// Store is a store as apps hold it: opened by OpenStore, owned and closed by the app.
type Store interface {
	// LastSeq is the seq of the store's last commit (0 for a new store).
	LastSeq() int64
	// Outbox is the outbox the sync layer reads; nil without one.
	Outbox() *Outbox
	// Export is the store's state after its LastSeq as archive parts (slice 6a §13), holding the commit queue from the
	// first Next until the iteration ends, fails, is closed or its context aborts.
	Export(ctx context.Context) (*StoreExport, error)
	// Close closes the store: waits for a running commit step, rejects queued commits and reads `released`, closes the
	// backend. Idempotent, also after `storage_failed`; ctx ends only this caller's wait (the close goes on).
	Close(ctx context.Context) error
}

// StoreOptions is what OpenStore takes besides the backend.
type StoreOptions struct {
	// Outbox turns the outbox on (true) or off (false); nil, the persisted setting stays (a new store: off).
	Outbox *bool
}

// OpenStore opens a store over backend: the store's schema migrations, the UTF-8 check, the metadata read once, the
// outbox switched when asked. A failure closes the backend and returns its error (rule 12).
func OpenStore(backend *Backend, ctx context.Context, options StoreOptions) (Store, error) {
	db := backend.DB
	meta, catalog, err := openState(db, ctx, options)
	if err != nil {
		// The open's own failure is the one to report: the close's is dropped.
		_ = db.Close()
		return nil, err
	}
	return NewChordStore(db, meta, catalog), nil
}

// openState is OpenStore's work up to the store: the context check, the schema, the outbox switch, the catalog.
func openState(db sqlite.SqliteDatabase, ctx context.Context, options StoreOptions) (MetaRow, map[string]*TableRecord, error) {
	if err := ctx.Err(); err != nil {
		return MetaRow{}, nil, context.Cause(ctx)
	}
	meta, err := ApplyStoreSchema(db)
	if err != nil {
		return MetaRow{}, nil, err
	}
	meta, err = switchOutbox(db, meta, options.Outbox)
	if err != nil {
		return MetaRow{}, nil, err
	}
	catalog, err := ReadCatalog(db)
	if err != nil {
		return MetaRow{}, nil, err
	}
	return meta, catalog, nil
}

// switchOutbox turns the outbox on (from the current `lastSeq`) or off (its commits dropped) when outbox asks for the
// other.
func switchOutbox(db sqlite.SqliteDatabase, meta MetaRow, outbox *bool) (MetaRow, error) {
	if outbox == nil || *outbox == (meta.Outbox == 1) {
		return meta, nil
	}
	on := *outbox
	floor := int64(0)
	flag := int64(0)
	if on {
		floor = meta.LastSeq
		flag = 1
	}
	err := db.Transaction(func(executor sqlite.SqliteExecutor) error {
		if err := executor.Run("UPDATE "+TableMeta+" SET outbox = ?, outbox_floor = ? WHERE singleton = 1", flag, floor); err != nil {
			return err
		}
		if !on {
			return executor.Run("DELETE FROM " + TableOutbox)
		}
		return nil
	})
	if err != nil {
		return MetaRow{}, err
	}
	meta.Outbox = flag
	meta.OutboxFloor = floor
	return meta, nil
}

// CommitFence is the seal fence's two checks of a commit (§1.3), as the commit step calls them.
type CommitFence struct {
	// Admit is called at the step's entry: false rejects the commit `released` and writes nothing.
	Admit func() bool
	// Adopt is called after `COMMIT`: it adopts the commit, ids being its created documents' ids in the order of
	// their writes. An error stops the store (an unknown outcome).
	Adopt func(seq int64, ids []int64) error
}

// Attachment is what the open host gives the store: its `storage_failed` listener and its batch, in which adapters
// publish (§1.3).
type Attachment struct {
	Failed func(err error)
	Batch  *state.Batch
}

// commitResult is what a commit settled with.
type commitResult struct {
	seq int64
	err error
}

// queuedCommit is a commit waiting in the commit queue for its group's turn: what Commit was given, and where its
// outcome goes (settled once, by the goroutine running its group's turn).
type queuedCommit struct {
	writes  []StagedWrite
	fence   CommitFence
	ctx     context.Context
	result  chan commitResult
	settled bool
}

// settle gives the commit its outcome, once.
func (queued *queuedCommit) settle(seq int64, err error) {
	if queued.settled {
		return
	}
	queued.settled = true
	queued.result <- commitResult{seq: seq, err: err}
}

// commitGroup is the commits queued behind a running step that share the next step's backend transaction (group commit,
// decision S18). Its first commit's caller runs its turn once previous settled and closes done after it.
type commitGroup struct {
	commits  []*queuedCommit
	previous <-chan struct{}
	done     chan struct{}
}

// pendingCommit is a commit that has its place in the commit queue (enlist): Wait runs it when its turn is its caller's
// and returns its outcome.
type pendingCommit struct {
	store *ChordStore
	// group is the group this commit leads; nil for a commit that joined one.
	group  *commitGroup
	queued *queuedCommit
}

// SeriesCommit is one commit of a series (CommitSeries): its own check, which writes nothing, and its writes. The check
// reads through the commit's step, which sees what the series' earlier commits wrote.
type SeriesCommit struct {
	// Check returns an error to refuse the commit: the series stops before it, the commits before it stand.
	Check  func(step *Step) error
	Writes []StagedWrite
}

// SeriesResult is what a series stored: how many of its commits, and why the next one was refused, if one was (nil
// otherwise).
type SeriesResult struct {
	Stored  int
	Refused error
}

// storedCommit is one stored commit of a series or a group: its seq and its created documents.
type storedCommit struct {
	seq     int64
	created []int64
}

// writtenLast is a transaction as its last commit that wrote leaves it: its seq, the allocator's value after it and the
// table records its commits changed.
type writtenLast struct {
	seq    int64
	nextID int64
	tables map[string]*TableRecord
}

// written is the commits of one backend transaction (a series or a group) as it leaves them: each stored commit's seq and
// created documents; once a commit wrote, its last (last is nil before); the commits that wrote and the statements run.
type written struct {
	stored     []storedCommit
	last       *writtenLast
	written    int64
	statements int
}

// writeProgress is the commits of one backend transaction (a series or a group) while it runs: its first seq's
// predecessor, each stored commit, the last step that wrote, the allocator's value after it, the table records they
// changed (tables) and the catalog the next step reads (a copy of the store's, made once a record changed) and the
// statements run.
type writeProgress struct {
	lastSeq    int64
	stored     []storedCommit
	last       *Step
	nextID     int64
	tables     map[string]*TableRecord
	catalog    map[string]*TableRecord
	statements int
}

// nextSeq is the seq the transaction's next commit takes.
func (progress *writeProgress) nextSeq() int64 {
	if progress.last != nil {
		return progress.last.Seq + 1
	}
	return progress.lastSeq + 1
}

// Replayed is where a replayed batch leaves the store: its last commit's seq and allocator value, the tables it
// changed.
type Replayed struct {
	LastSeq int64
	NextID  int64
	Tables  map[string]*TableRecord
}

// storeStateKind is where a store stands: open, stopped by a backend error, or closed.
type storeStateKind int

const (
	storeOpen storeStateKind = iota
	storeFailed
	storeClosed
)

// writeOrders is the phases of a commit's writes, in the order they apply.
var writeOrders = []WriteOrder{OrderRetire, OrderCreate, OrderWrite}

// heldEnd is the end of an export holding the queue or waiting for it; a pointer, as funcs have no identity.
type heldEnd struct{ end func() }

// closing is a store's close, started once: done is closed when it ended, err is its result.
type closing struct {
	done chan struct{}
	err  error
}

// ChordStore is the store behind Store, with what Chord's layers use: commits, guarded reads, the host's failure
// listener.
type ChordStore struct {
	// Claims are the claims of its tables and documents (slice 6b).
	Claims *Claims

	db     sqlite.SqliteDatabase
	ids    *IdAllocator
	outbox *Outbox

	mu sync.Mutex
	// lastSeq is the seq of the last commit.
	lastSeq int64
	// committedNextID is the allocator's value as the last commit persisted it (an export's manifest).
	committedNextID int64
	state           storeStateKind
	// failure is the backend error that stopped the store, latched also when it happened while the store was closing (the
	// state stays closed then): a commit admitted before it is refused.
	failure error
	// diverged is set once a batch's recorded count did not reproduce (S17): no batch applies any more.
	diverged bool
	// tail is the commit queue's tail: each entry starts after the one before it settled (its channel is closed).
	tail <-chan struct{}
	// open is the group the next commit joins: queued last, its turn not yet come; nil once anything queued after it.
	open *commitGroup
	// underway counts reads and maintenance writes underway, which Close waits for.
	underway int
	// drained is closed, once, when no read is underway: Close's wait.
	drained chan struct{}
	// attachment is the open host's listener for `storage_failed` (told once) and batch; one host per store (rule 7).
	attachment *Attachment
	closing    *closing
	// catalog is the recorded tables, by SQL name; replaced (never changed in place) only after a step's `COMMIT`, so
	// a map once read stays valid and is never written.
	catalog map[string]*TableRecord
	// held is the ends of the exports holding the queue or waiting for it, which closing the store calls.
	held map[*heldEnd]struct{}
	// announced is the versions migrations of tables announced (AnnounceTable), by SQL name, until their last commit.
	announced map[string]int
}

var _ Store = (*ChordStore)(nil)

// NewChordStore is the store over db, its metadata row and its catalog, as OpenStore builds it.
func NewChordStore(db sqlite.SqliteDatabase, meta MetaRow, catalog map[string]*TableRecord) *ChordStore {
	closed := make(chan struct{})
	close(closed)
	store := &ChordStore{
		Claims:          NewClaims(),
		db:              db,
		ids:             NewIdAllocator(meta.NextID),
		lastSeq:         meta.LastSeq,
		committedNextID: meta.NextID,
		tail:            closed,
		catalog:         catalog,
		held:            map[*heldEnd]struct{}{},
		announced:       map[string]int{},
	}
	if meta.Outbox == 1 {
		store.outbox = NewOutbox(store, meta.OutboxFloor)
	}
	return store
}

// LastSeq is the seq of the store's last commit (0 for a new store).
func (store *ChordStore) LastSeq() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lastSeq
}

// Outbox is the outbox the sync layer reads; nil without one.
func (store *ChordStore) Outbox() *Outbox { return store.outbox }

// NextID is the allocator's value as the last commit persisted it.
func (store *ChordStore) NextID() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.committedNextID
}

// Ids is the id allocator: Mint takes the next id, Raise makes room above an explicit one (D3).
func (store *ChordStore) Ids() *IdAllocator { return store.ids }

// MintId takes the next id from the allocator (Durable's `mintId`); refused once the store stopped or closed.
func (store *ChordStore) MintId() (int64, error) {
	if err := store.AssertUsable(); err != nil {
		return 0, err
	}
	return store.ids.Mint()
}

// Batch is the open host's batch, in which an adapter publishes its commits; nil without a host.
func (store *ChordStore) Batch() *state.Batch {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.attachment == nil {
		return nil
	}
	return store.attachment.Batch
}

// Tables is every recorded table, by SQL name, as of the last commit. The map is a snapshot: the store replaces it
// after a commit that changed a table and never writes to one it handed out; callers must not either.
func (store *ChordStore) Tables() map[string]*TableRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.catalog
}

// Table is the recorded table name (its SQL name), as of the last commit; nil when none.
func (store *ChordStore) Table(name string) *TableRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.catalog[name]
}

// TableVersion is the version table name (its SQL name) is at or is being migrated to: the recorded one, or a higher
// one a migration announced before its first commit (AnnounceTable), so an old handle is refused before its SQL could
// queue behind the migration's DDL (fix round 1, Sol 8). ok is false for a table the store does not have and none
// announced.
func (store *ChordStore) TableVersion(name string) (version int, ok bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record := store.catalog[name]
	announced, isAnnounced := store.announced[name]
	if !isAnnounced || (record != nil && record.Version >= announced) {
		if record == nil {
			return 0, false
		}
		return record.Version, true
	}
	return announced, true
}

// AnnounceTable announces a migration of table name to version until the returned withdrawal (after its last commit).
func (store *ChordStore) AnnounceTable(name string, version int) (withdraw func()) {
	store.mu.Lock()
	store.announced[name] = version
	store.mu.Unlock()
	return func() {
		store.mu.Lock()
		defer store.mu.Unlock()
		if announced, ok := store.announced[name]; ok && announced == version {
			delete(store.announced, name)
		}
	}
}

// RecordsOutbox reports whether the store has an outbox, which a replayed batch writes too.
func (store *ChordStore) RecordsOutbox() bool { return store.outbox != nil }

// Export is the store's state after its LastSeq as archive parts (slice 6a §13).
func (store *ChordStore) Export(ctx context.Context) (*StoreExport, error) {
	if err := store.AssertUsable(); err != nil {
		return nil, err
	}
	return NewStoreExport(store, ctx), nil
}

// Take takes the commit queue (an export): its place is taken at the call, and the channel yields the release that
// lets the later entries run (idempotent) once every earlier commit settled. Closing the store calls end (from the
// call on, also while waiting, and at once for a store closed before the call took the queue), which must release.
func (store *ChordStore) Take(end func()) <-chan func() {
	entry := &heldEnd{end: end}
	held := make(chan struct{})
	var once sync.Once
	release := func() {
		once.Do(func() {
			store.mu.Lock()
			delete(store.held, entry)
			store.mu.Unlock()
			close(held)
		})
	}
	store.mu.Lock()
	store.held[entry] = struct{}{}
	previous := store.tail
	store.tail = held
	store.open = nil
	store.mu.Unlock()
	taken := make(chan func(), 1)
	//portlint:allow asyncorder this is Pi's `this.#tail.then(...)` continuation (store.ts:245): the queue place (store.tail) is taken under mu above, before the goroutine starts, and its result is joined by the returned channel, as take's promise
	go func() {
		<-previous
		// A store closed meanwhile (also before the export's first Next) holds nothing: the export ends.
		store.mu.Lock()
		closed := store.state == storeClosed
		store.mu.Unlock()
		if closed {
			release()
			end()
		}
		taken <- release
	}()
	return taken
}

// Attach attaches the open host; a store attached to an open host is refused (rule 7).
func (store *ChordStore) Attach(attachment Attachment) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.attachment != nil {
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: "store: already attached to an open host"}, nil)
	}
	store.attachment = &attachment
	return nil
}

// Detach: the host closed, the store may serve another one.
func (store *ChordStore) Detach() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.attachment = nil
}

// ticket takes the next place of the commit queue: the entry waits for previous, and closes done once it settled.
func (store *ChordStore) ticket() (previous <-chan struct{}, done chan struct{}) {
	done = make(chan struct{})
	store.mu.Lock()
	previous = store.tail
	store.tail = done
	store.open = nil
	store.mu.Unlock()
	return previous, done
}

// Commit commits writes as one seq when the commit queue reaches it: `fence.Admit()` at the entry of its step (its
// group's), the writes in their order (retirements, creations, the rest), the outbox commit, `lastSeq` and the
// allocator's value, `COMMIT`, then `fence.Adopt` before the next step starts. It returns the seq. A refused admission
// fails `released`. A step the backend rolled back after the step's own code failed (a refusal, D4) fails with that
// error unchanged and the store goes on; any other failure (`BEGIN`, `COMMIT`, `ROLLBACK`, a failing Adopt) is an
// unknown outcome that stops the store (`storage_failed`). ctx counts the step (no abort once queued).
//
// Group commit (decision S18): the commits queued while a step runs form one group, which the next turn admits and writes
// in one backend transaction (each commit its own seq, outbox commit and adoption, in queue order; the metadata once), so
// a busy store costs a transaction per group, not per commit. If one of them is refused, the group rolls back and its
// commits run again one by one as above: the refusal stays its own commit's.
//
// Async contract: the call takes its place in the queue under `mu` and blocks until its commit settled; the caller of a
// group's first commit runs the group's turn, the others wait for it.
func (store *ChordStore) Commit(writes []StagedWrite, fence CommitFence, ctx context.Context) (int64, error) {
	return store.enlist(writes, fence, ctx).Wait()
}

// enlist takes the commit's place in the commit queue, as Pi's `commit` call does at the call: behind the group still
// open (a commit with writes), or as the first of a new one. An empty commit takes no transaction: it neither joins nor
// opens a group. A store that is not usable settles it at once.
func (store *ChordStore) enlist(writes []StagedWrite, fence CommitFence, ctx context.Context) *pendingCommit {
	queued := &queuedCommit{writes: writes, fence: fence, ctx: ctx, result: make(chan commitResult, 1)}
	pending := &pendingCommit{store: store, queued: queued}
	if err := store.AssertUsable(); err != nil {
		queued.settle(0, err)
		return pending
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(writes) > 0 && store.open != nil {
		store.open.commits = append(store.open.commits, queued)
		return pending
	}
	group := &commitGroup{commits: []*queuedCommit{queued}, previous: store.tail, done: make(chan struct{})}
	store.tail = group.done
	store.open = nil
	if len(writes) > 0 {
		store.open = group
	}
	pending.group = group
	return pending
}

// Wait is the commit's outcome: the seq it stored, or why it was not. The caller of a group's first commit runs the
// group's turn once the entry before it settled.
func (pending *pendingCommit) Wait() (int64, error) {
	if pending.group != nil {
		<-pending.group.previous
		pending.store.runGroup(pending.group)
	}
	outcome := <-pending.queued.result
	return outcome.seq, outcome.err
}

// CommitSeries commits commits in one backend transaction when the commit queue reaches it, one seq each, in order: each
// commit's Check, which writes nothing, then its writes as Commit writes them, with its own outbox commit and the
// metadata (a commit without writes takes no seq: it stands at the one before). A Check that fails refuses its commit:
// the series stops there and the transaction commits the ones before it. A Check one of whose backend statements
// failed, and a failure of a commit's writes, roll the whole series back and return that error, the store going on
// (D4); any other failure is an unknown outcome that stops the store, as for Commit. After `COMMIT`, receive(index,
// seq, ids) is called per stored commit, in order, before the next step starts (ids: its created documents); an error
// of receive stops the store. It returns what it stored.
//
// Async contract: Pi's `commitSeries` takes its place in the queue at the call and settles once its receipts ran; here
// the call takes the place under the ticket (as Commit does) and blocks until then.
//
// receive runs while the series holds the queue ticket: a blocking Commit or CommitSeries called from inside it
// deadlocks (Pi would queue it behind the series), as CommitFence.Adopt always has; it must not commit.
func (store *ChordStore) CommitSeries(commits []SeriesCommit, receive func(index int, seq int64, ids []int64) error, ctx context.Context) (SeriesResult, error) {
	if err := store.AssertUsable(); err != nil {
		return SeriesResult{}, err
	}
	previous, done := store.ticket()
	<-previous
	defer close(done)
	return store.series(commits, receive, ctx)
}

// Read runs read on the backend under the guard (rule 11, Durable's `GuardedStorage`): refused once the store stopped
// or closed; a backend error fails the store unless ctx was aborted (then `aborted`); a read settling after the
// store stopped fails `unavailable/storage_failed`. read returns data and never fails for a request: an adapter
// checks a request from what it returned, after the guard (slice 6b §1.4).
func Read[T any](store *ChordStore, read func(executor sqlite.SqliteExecutor) (T, error), ctx context.Context) (T, error) {
	return guarded(store, func() (T, error) { return read(store.db) }, ctx)
}

// ReadTransaction is Read for several queries that must see one state (S4, a document from its record and
// revisions): one backend transaction, guarded as Read.
func ReadTransaction[T any](store *ChordStore, read func(executor sqlite.SqliteExecutor) (T, error), ctx context.Context) (T, error) {
	return guarded(store, func() (T, error) {
		var result T
		err := store.db.Transaction(func(executor sqlite.SqliteExecutor) error {
			var err error
			result, err = read(executor)
			return err
		})
		return result, err
	}, ctx)
}

func guarded[T any](store *ChordStore, run func() (T, error), ctx context.Context) (T, error) {
	var zero T
	if err := store.AssertUsable(); err != nil {
		return zero, err
	}
	store.begin()
	defer store.settle()
	result, err := run()
	if err != nil {
		// Latched before the closed check: a backend failure while closing still refuses later admitted commits.
		if ctx.Err() == nil {
			store.latch(err)
		}
		if usable := store.AssertUsable(); usable != nil {
			return zero, usable
		}
		if ctx.Err() != nil {
			return zero, data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "store: read aborted"}, context.Cause(ctx))
		}
		return zero, store.fail(err)
	}
	if usable := store.AssertUsable(); usable != nil {
		return zero, usable
	}
	return result, nil
}

// begin counts a read or maintenance write underway.
func (store *ChordStore) begin() {
	store.mu.Lock()
	store.underway++
	store.mu.Unlock()
}

// settle: one read or maintenance write settled; Close goes on once none is underway.
func (store *ChordStore) settle() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.underway--
	if store.underway == 0 && store.drained != nil {
		close(store.drained)
		store.drained = nil
	}
}

// Maintain runs write on the backend outside the commit queue (the outbox's `trim`), in a backend transaction,
// guarded as a read without a context: any error stops the store.
func (store *ChordStore) Maintain(write func(executor sqlite.SqliteExecutor) error) error {
	if err := store.AssertUsable(); err != nil {
		return err
	}
	store.begin()
	defer store.settle()
	if err := store.db.Transaction(write); err != nil {
		// Latched before the closed check, as for a read.
		store.latch(err)
		if usable := store.AssertUsable(); usable != nil {
			return usable
		}
		return store.fail(err)
	}
	return nil
}

// Close closes the store: waits for a running commit step, rejects queued commits and reads `released`, closes the
// backend. Idempotent, also after `storage_failed`; ctx ends only this caller's wait (the close goes on).
func (store *ChordStore) Close(ctx context.Context) error {
	store.mu.Lock()
	started := store.closing
	if started == nil {
		started = &closing{done: make(chan struct{})}
		store.closing = started
		store.state = storeClosed
		ends := make([]func(), 0, len(store.held))
		for entry := range store.held {
			ends = append(ends, entry.end)
		}
		tail := store.tail
		store.mu.Unlock()
		for _, end := range ends {
			end()
		}
		//portlint:allow golifetime finishClose is Pi's memoized `this.#closing ??= this.#close()` (store.ts:399): it closes started.done, which every Close call selects on, and it ends once the queue's last entry and the reads underway settled
		go store.finishClose(started, tail)
	} else {
		store.mu.Unlock()
	}
	// awaitWithContext: an aborted caller ends its own wait with the abort's cause, the close goes on.
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	select {
	case <-started.done:
		return started.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// finishClose is the close after its synchronous part: the queue's last entry, the reads underway, the backend.
func (store *ChordStore) finishClose(started *closing, tail <-chan struct{}) {
	defer close(started.done)
	<-tail
	store.mu.Lock()
	var drained chan struct{}
	if store.underway > 0 {
		drained = make(chan struct{})
		store.drained = drained
	}
	store.mu.Unlock()
	if drained != nil {
		<-drained
	}
	started.err = store.db.Close()
}

// stepDone is what a step's backend transaction leaves: the step and the allocator's value it persisted.
type stepDone struct {
	step   *Step
	nextID int64
}

// runGroup is a group's turn, run by its first commit's caller (the group takes no more commits from here): its commits
// admitted together at its entry, one as its own step, several written in one backend transaction (grouped). It settles
// every commit of the group and then lets the queue go on, whatever happened. A panic out of a callback of the turn
// (Admit, Adopt, the host's failed listener) is an unknown outcome: the store stops, the commits not settled yet fail
// `storage_failed`, and the panic goes on in the caller that ran the turn.
func (store *ChordStore) runGroup(group *commitGroup) {
	defer close(group.done)
	store.mu.Lock()
	if store.open == group {
		store.open = nil
	}
	store.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			cause := fmt.Errorf("store: a commit step panicked: %v", recovered)
			failed := storageFailed(cause)
			for _, queued := range group.commits {
				queued.settle(0, failed)
			}
			store.fail(cause)
			panic(recovered)
		}
	}()
	if err := store.AssertUsable(); err != nil {
		for _, queued := range group.commits {
			queued.settle(0, err)
		}
		return
	}
	// Admitted together at the group's entry, as one step admits its commit: each runs to its end from here.
	admitted := make([]*queuedCommit, 0, len(group.commits))
	for _, queued := range group.commits {
		if queued.fence.Admit() {
			admitted = append(admitted, queued)
		} else {
			queued.settle(0, discarded())
		}
	}
	switch len(admitted) {
	case 0:
	case 1:
		store.alone(admitted[0])
	default:
		store.grouped(admitted)
	}
}

// discarded is the `released` error of a commit its fence refused at the step's entry: its attempt ended.
func discarded() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonDiscarded, Message: "store: the commit's attempt ended"}, nil)
}

// alone runs an admitted commit as its own step and settles it.
func (store *ChordStore) alone(queued *queuedCommit) {
	seq, err := store.admittedStep(queued.writes, queued.fence, queued.ctx)
	queued.settle(seq, err)
}

// eachAlone runs admitted commits one by one, each its own step, in order.
func (store *ChordStore) eachAlone(admitted []*queuedCommit) {
	for _, queued := range admitted {
		store.alone(queued)
	}
}

// groupWritten is a group as its transaction leaves it: each commit's step and the allocator's value after it, in order;
// the whole.
type groupWritten struct {
	steps   []groupStep
	written written
}

// groupStep is one commit's step of a group and the allocator's value after it.
type groupStep struct {
	step   *Step
	nextID int64
}

// grouped writes admitted commits in one backend transaction, each on its own step and seq, the metadata once; after
// `COMMIT`, memory, the counts and each commit's adoption in order. When a commit's own writes fail (a refusal, D4), the
// transaction rolls back whole and every commit runs again alone, admitted already: only the ones whose own writes fail
// are refused, as without grouping. Any other failure is an unknown outcome that stops the store.
func (store *ChordStore) grouped(admitted []*queuedCommit) {
	lastSeq := store.LastSeq()
	// Near the end of the seq range, alone: the commits that still fit are stored, the first past it stops the store.
	if lastSeq+int64(len(admitted)) > MaxSafeInteger {
		store.eachAlone(admitted)
		return
	}
	var thrown error
	var done groupWritten
	err := store.db.Transaction(func(executor sqlite.SqliteExecutor) error {
		var err error
		done, err = store.writeGroup(admitted, executor, lastSeq)
		if err != nil {
			thrown = err
		}
		return err
	})
	if err != nil {
		if thrown != nil && sameError(err, thrown) {
			store.eachAlone(admitted)
			return
		}
		failed := store.fail(err)
		for _, queued := range admitted {
			queued.settle(0, failed)
		}
		return
	}
	store.adoptGroup(admitted, done)
}

// writeGroup is the admitted commits' writes in their backend transaction, each on its own step, then the metadata once.
func (store *ChordStore) writeGroup(admitted []*queuedCommit, executor sqlite.SqliteExecutor, lastSeq int64) (groupWritten, error) {
	progress := &writeProgress{lastSeq: lastSeq, nextID: store.ids.Value(), catalog: store.Tables()}
	steps := make([]groupStep, 0, len(admitted))
	for _, queued := range admitted {
		// A grouped commit has writes: enlist never groups an empty one.
		step, err := store.writeCommit(queued.writes, executor, progress)
		if err != nil {
			return groupWritten{}, err
		}
		steps = append(steps, groupStep{step: step, nextID: progress.nextID})
	}
	written, err := store.closeWrites(progress)
	return groupWritten{steps: steps, written: written}, err
}

// adoptGroup is, after a group's `COMMIT`, commit by commit as after a lone step: memory follows it, it counts through its
// own context (the metadata statement ran on the last one's step), it is adopted. Anything that fails here (an adoption)
// is an unknown outcome: memory takes the whole group (it is durable), the store stops, and that commit and the ones
// after it fail `storage_failed`, unadopted; a panic does the same, and goes on.
func (store *ChordStore) adoptGroup(admitted []*queuedCommit, done groupWritten) {
	index := 0
	defer func() {
		if index < len(admitted) {
			store.takeWhole(done.written)
		}
	}()
	for ; index < len(admitted); index++ {
		queued, grouped := admitted[index], done.steps[index]
		step := grouped.step
		store.mu.Lock()
		store.lastSeq = step.Seq
		store.committedNextID = grouped.nextID
		if len(step.Tables) > 0 {
			store.catalog = withTables(store.catalog, step.Tables)
		}
		store.mu.Unlock()
		if metrics := chordctx.Telemetry(queued.ctx).Metrics(); metrics != nil {
			metrics.Counter("chord.store.commits", telemetry.InstrumentOptions{}).Add(1, nil)
			metrics.Counter("chord.store.statements", telemetry.InstrumentOptions{}).Add(float64(step.Statements), nil)
		}
		if err := queued.fence.Adopt(step.Seq, step.Created); err != nil {
			store.takeWhole(done.written)
			failed := store.fail(err)
			for _, rest := range admitted[index:] {
				rest.settle(0, failed)
			}
			index = len(admitted)
			return
		}
		queued.settle(step.Seq, nil)
	}
}

// takeWhole is memory standing at the last commit of a transaction that is durable.
func (store *ChordStore) takeWhole(done written) {
	last := done.last
	store.mu.Lock()
	defer store.mu.Unlock()
	store.lastSeq = last.seq
	store.committedNextID = last.nextID
	if len(last.tables) > 0 {
		store.catalog = withTables(store.catalog, last.tables)
	}
}

// admittedStep is an admitted commit's step (Commit's semantics after admission; the fence only adopts): it runs to its
// end, also when the store closes meanwhile (Close waits for it); only a backend failure before it, closing or not,
// refuses it.
func (store *ChordStore) admittedStep(writes []StagedWrite, fence CommitFence, ctx context.Context) (int64, error) {
	store.mu.Lock()
	failure := store.failure
	store.mu.Unlock()
	if failure != nil {
		return 0, storageFailed(failure)
	}
	lastSeq := store.LastSeq()
	if len(writes) == 0 {
		// An empty commit takes no seq; a failing Adopt stops the store as for any commit.
		if err := fence.Adopt(lastSeq, []int64{}); err != nil {
			return 0, store.fail(err)
		}
		return lastSeq, nil
	}
	seq := lastSeq + 1
	// Past the safe range seqs stop counting exactly (a later one would repeat): the store cannot number further.
	if seq > MaxSafeInteger {
		return 0, store.fail(errors.New("store: no seq past Number.MAX_SAFE_INTEGER"))
	}
	var thrown error
	var done stepDone
	err := store.db.Transaction(func(executor sqlite.SqliteExecutor) error {
		step := NewStep(executor, seq, store.ids, store.Tables())
		nextID, err := store.write(step, writes)
		if err == nil {
			err = writeMeta(step, nextID)
		}
		if err != nil {
			thrown = err
			return err
		}
		done = stepDone{step: step, nextID: nextID}
		return nil
	})
	if err != nil {
		// The facade rolled back and returns the step's own error: a refusal, the store goes on (D4).
		if thrown != nil && sameError(err, thrown) {
			return 0, err
		}
		return 0, store.fail(err)
	}
	// COMMIT returned: the commit is durable, and memory follows.
	store.mu.Lock()
	store.lastSeq = seq
	store.committedNextID = done.nextID
	if len(done.step.Tables) > 0 {
		store.catalog = withTables(store.catalog, done.step.Tables)
	}
	store.mu.Unlock()
	if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
		metrics.Counter("chord.store.commits", telemetry.InstrumentOptions{}).Add(1, nil)
		metrics.Counter("chord.store.statements", telemetry.InstrumentOptions{}).Add(float64(done.step.Statements), nil)
	}
	if err := fence.Adopt(seq, done.step.Created); err != nil {
		return 0, store.fail(err)
	}
	return seq, nil
}

// sameError is Pi's `error === thrown`: the identical error value. Go values of an uncomparable error type are never
// identical to anything.
func sameError(a, b error) bool {
	if a == nil || b == nil {
		return false
	}
	if !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}
	return any(a) == any(b)
}

// withTables is catalog with changed's records, as a new map: a map once handed out is never written.
func withTables(catalog, changed map[string]*TableRecord) map[string]*TableRecord {
	next := make(map[string]*TableRecord, len(catalog)+len(changed))
	maps.Copy(next, catalog)
	maps.Copy(next, changed)
	return next
}

// series is a series inside one backend transaction: per commit a step at the next seq, its check (a failure refuses
// it, unless one of its statements failed), then its writes; then, after `COMMIT`, memory and the receipts as after
// `step`.
func (store *ChordStore) series(commits []SeriesCommit, receive func(index int, seq int64, ids []int64) error, ctx context.Context) (SeriesResult, error) {
	if err := store.AssertUsable(); err != nil {
		return SeriesResult{}, err
	}
	lastSeq := store.LastSeq()
	// Past the safe range seqs stop counting exactly (a later one would repeat): the store cannot number further. Only
	// a commit with writes takes a seq, as for Commit.
	var seqs int64
	for _, commit := range commits {
		if len(commit.Writes) > 0 {
			seqs++
		}
	}
	if lastSeq+seqs > MaxSafeInteger {
		return SeriesResult{}, store.fail(errors.New("store: no seq past Number.MAX_SAFE_INTEGER"))
	}
	var thrown, refused error
	var done written
	err := store.db.Transaction(func(executor sqlite.SqliteExecutor) error {
		var err error
		done, refused, err = store.writeSeries(executor, commits, lastSeq)
		if err != nil {
			thrown = err
		}
		return err
	})
	if err != nil {
		// The facade rolled back and returns the series' own error: the store goes on (D4).
		if thrown != nil && sameError(err, thrown) {
			return SeriesResult{}, err
		}
		return SeriesResult{}, store.fail(err)
	}
	// COMMIT returned: the stored commits are durable, and memory follows.
	if done.last != nil {
		store.mu.Lock()
		store.lastSeq = done.last.seq
		store.committedNextID = done.last.nextID
		if len(done.last.tables) > 0 {
			store.catalog = withTables(store.catalog, done.last.tables)
		}
		store.mu.Unlock()
	}
	if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
		metrics.Counter("chord.store.commits", telemetry.InstrumentOptions{}).Add(float64(done.written), nil)
		metrics.Counter("chord.store.statements", telemetry.InstrumentOptions{}).Add(float64(done.statements), nil)
	}
	for index, stored := range done.stored {
		if err := receive(index, stored.seq, stored.created); err != nil {
			return SeriesResult{}, store.fail(err)
		}
	}
	return SeriesResult{Stored: len(done.stored), Refused: refused}, nil
}

// writeSeries is the commits of a series in its backend transaction, until one is refused (refused is its error): each
// commit's check, then its writes; the metadata once, at the end. lastSeq is the store's.
func (store *ChordStore) writeSeries(executor sqlite.SqliteExecutor, commits []SeriesCommit, lastSeq int64) (done written, refused error, err error) {
	calls := &watchedCalls{}
	watched := &watchedExecutor{SqliteExecutor: executor, calls: calls}
	progress := &writeProgress{lastSeq: lastSeq, nextID: store.ids.Value(), catalog: store.Tables()}
	for _, commit := range commits {
		refusal, err := store.checkCommit(commit, watched, calls, progress)
		if err != nil {
			return written{}, nil, err
		}
		if refusal != nil {
			refused = refusal
			break
		}
		if _, err := store.writeCommit(commit.Writes, executor, progress); err != nil {
			return written{}, nil, err
		}
	}
	done, err = store.closeWrites(progress)
	return done, refused, err
}

// checkCommit runs commit's check on a step at the series' next seq whose backend calls are watched; it returns its
// refusal, if it refused. A check one of whose backend calls failed returns that as err instead: a failure, not a
// refusal.
func (store *ChordStore) checkCommit(commit SeriesCommit, watched sqlite.SqliteExecutor, calls *watchedCalls, progress *writeProgress) (refusal error, err error) {
	step := NewStep(watched, progress.nextSeq(), store.ids, progress.catalog)
	calls.failed = false
	checked := commit.Check(step)
	progress.statements += step.Statements
	if checked == nil {
		return nil, nil
	}
	if calls.failed {
		return nil, checked
	}
	return checked, nil
}

// writeCommit writes a commit's writes on its own step at the transaction's next seq (write), unwatched, and keeps the
// table records it changed in a copy of the catalog, made only once one changed; it returns the step. A commit without
// writes takes no seq and no step (nil).
func (store *ChordStore) writeCommit(writes []StagedWrite, executor sqlite.SqliteExecutor, progress *writeProgress) (*Step, error) {
	seq := progress.nextSeq()
	if len(writes) == 0 {
		progress.stored = append(progress.stored, storedCommit{seq: seq - 1, created: []int64{}})
		return nil, nil
	}
	step := NewStep(executor, seq, store.ids, progress.catalog)
	nextID, err := store.write(step, writes)
	if err != nil {
		return nil, err
	}
	progress.nextID = nextID
	progress.statements += step.Statements
	if len(step.Tables) > 0 {
		if progress.tables == nil {
			progress.tables = map[string]*TableRecord{}
			progress.catalog = maps.Clone(progress.catalog)
		}
		for name, record := range step.Tables {
			progress.tables[name] = record
			progress.catalog[name] = record
		}
	}
	progress.last = step
	progress.stored = append(progress.stored, storedCommit{seq: seq, created: step.Created})
	return step, nil
}

// closeWrites ends a transaction's writes with the metadata, once, after its last commit that wrote; it returns what
// memory takes.
func (store *ChordStore) closeWrites(progress *writeProgress) (written, error) {
	if progress.last == nil {
		return written{stored: progress.stored, statements: progress.statements}, nil
	}
	if err := writeMeta(progress.last, progress.nextID); err != nil {
		return written{}, err
	}
	return written{
		stored:     progress.stored,
		last:       &writtenLast{seq: progress.last.Seq, nextID: progress.nextID, tables: progress.tables},
		written:    progress.last.Seq - progress.lastSeq,
		statements: progress.statements + 1,
	}, nil
}

// write is the step's writes inside its backend transaction: each staged write in its phase, the catalog, the outbox
// commit. It returns the allocator's value, taken once, after the writes applied (mints meanwhile only raise it); the
// metadata (writeMeta) is the caller's.
func (store *ChordStore) write(step *Step, writes []StagedWrite) (int64, error) {
	var recorded []StoreWrite
	if store.outbox != nil {
		recorded = []StoreWrite{}
	}
	for _, order := range writeOrders {
		for _, write := range writes {
			if write.StageOrder() != order {
				continue
			}
			done, err := write.Apply(step)
			if err != nil {
				return 0, err
			}
			if recorded != nil {
				recorded = append(recorded, done...)
			}
		}
	}
	if err := WriteCatalog(step); err != nil {
		return 0, err
	}
	nextID := store.ids.Value()
	if recorded != nil {
		text, err := jsstring.MarshalJSON(recorded)
		if err != nil {
			return 0, err
		}
		err = step.Run("INSERT INTO "+TableOutbox+" (seq, format, next_id, writes) VALUES (?, ?, ?, ?)", step.Seq, OutboxFormat, nextID, string(text))
		if err != nil {
			return 0, err
		}
	}
	return nextID, nil
}

// writeMeta records the step's seq as the store's last, and the allocator's value nextID.
func writeMeta(step *Step, nextID int64) error {
	return step.Run("UPDATE "+TableMeta+" SET last_seq = ?, next_id = ? WHERE singleton = 1", step.Seq, nextID)
}

// watchedCalls records that a backend call of a check failed.
type watchedCalls struct{ failed bool }

// watchedExecutor is an executor that sets calls.failed when one of its calls fails: a check that fails then failed in
// the backend, not by its own decision.
type watchedExecutor struct {
	sqlite.SqliteExecutor
	calls *watchedCalls
}

func (executor *watchedExecutor) Exec(sql string) error {
	err := executor.SqliteExecutor.Exec(sql)
	executor.watch(err)
	return err
}

func (executor *watchedExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	err := executor.SqliteExecutor.Run(sql, params...)
	executor.watch(err)
	return err
}

func (executor *watchedExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	changed, err := executor.SqliteExecutor.RunCounted(sql, params...)
	executor.watch(err)
	return changed, err
}

func (executor *watchedExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	row, err := executor.SqliteExecutor.Get(sql, params...)
	executor.watch(err)
	return row, err
}

func (executor *watchedExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	rows, err := executor.SqliteExecutor.All(sql, params...)
	executor.watch(err)
	return rows, err
}

func (executor *watchedExecutor) watch(err error) {
	if err != nil {
		executor.calls.failed = true
	}
}

// ReplayApply is what Replay runs in the commit queue's turn: it applies a batch in one backend transaction over the
// store's catalog and returns the position its last commit leaves.
type ReplayApply func(executor sqlite.SqliteExecutor, catalog map[string]*TableRecord) (Replayed, error)

// Replay is batch apply's turn (slice 6b §1.6, S5): refused (`invalid/args`) while an owner holds a claim (its memory
// would go stale) and once the store diverged; otherwise apply runs in the commit queue, in one backend
// transaction, and returns the position its last commit leaves (`lastSeq`, the allocator's value) and the tables it
// changed, which memory takes after `COMMIT`. apply's own error rolls back and is returned as a refusal (a
// divergence, as isDivergence says, marks the store diverged); any other failure stops the store.
func (store *ChordStore) Replay(apply ReplayApply, isDivergence func(err error) bool) error {
	if err := store.assertReplayable(); err != nil {
		return err
	}
	previous, done := store.ticket()
	<-previous
	defer close(done)
	return store.replay(apply, isDivergence)
}

// assertReplayable is checked at the call of Replay and again at its turn: a claim or a divergence may come between
// the two.
func (store *ChordStore) assertReplayable() error {
	if err := store.AssertUsable(); err != nil {
		return err
	}
	store.mu.Lock()
	diverged := store.diverged
	store.mu.Unlock()
	if diverged {
		return data.Invalid(data.ReasonArgs, "store: diverged; a batch can no longer apply")
	}
	if store.Claims.Held() {
		return data.Invalid(data.ReasonArgs, "store: an owner holds claims; a batch would stale its memory")
	}
	return nil
}

func (store *ChordStore) replay(apply ReplayApply, isDivergence func(err error) bool) error {
	if err := store.assertReplayable(); err != nil {
		return err
	}
	// While it applies, no owner may claim (and read) what it is changing.
	unlock := store.Claims.Lock()
	defer unlock()
	var thrown error
	var replayed Replayed
	err := store.db.Transaction(func(executor sqlite.SqliteExecutor) error {
		var err error
		replayed, err = apply(executor, store.Tables())
		if err != nil {
			thrown = err
		}
		return err
	})
	if err != nil {
		if thrown == nil || !sameError(err, thrown) {
			return store.fail(err)
		}
		if isDivergence(err) {
			store.mu.Lock()
			store.diverged = true
			store.mu.Unlock()
		}
		return err
	}
	store.mu.Lock()
	store.lastSeq = replayed.LastSeq
	store.committedNextID = replayed.NextID
	if len(replayed.Tables) > 0 {
		store.catalog = withTables(store.catalog, replayed.Tables)
	}
	store.mu.Unlock()
	store.ids.Raise(replayed.NextID - 1)
	return nil
}

// AssertUsable fails `unavailable/storage_failed` once the store stopped, `released` once it closed.
func (store *ChordStore) AssertUsable() error {
	store.mu.Lock()
	kind, failure := store.state, store.failure
	store.mu.Unlock()
	switch kind {
	case storeFailed:
		return storageFailed(failure)
	case storeClosed:
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "store: closed"}, nil)
	}
	return nil
}

// latch records err as the backend failure that stopped the store, once, whatever state the store is in: also while it
// closes (its state stays closed), a commit admitted before it is refused.
func (store *ChordStore) latch(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failure == nil {
		store.failure = err
	}
}

// fail stops the store after err (once; its host is told once); it returns the error the caller fails with.
func (store *ChordStore) fail(err error) *data.ChordError {
	store.mu.Lock()
	if store.failure == nil {
		store.failure = err
	}
	var failed func(error)
	if store.state == storeOpen {
		store.state = storeFailed
		if store.attachment != nil {
			failed = store.attachment.Failed
		}
	}
	store.mu.Unlock()
	if failed != nil {
		failed(err)
	}
	return storageFailed(err)
}

// storageFailed is the `unavailable/storage_failed` error of a stopped store, its backend error as the cause.
func storageFailed(cause error) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, cause)
}
