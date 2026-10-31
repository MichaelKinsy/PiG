package store

import (
	"context"
	"errors"
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
// stop after an unknown outcome, and the commit series (several commits stored in one backend transaction).
//
// Async contract (Go form of Pi's promises). Pi is single-threaded and its commit queue is the `#tail` promise chain:
// every `commit`, `replay` and `take` call takes its place in the queue synchronously, at the call, and runs once the
// call before it settled; a settled queue entry never blocks the next one, whatever it rejected with. A commit
// series is one entry. Pi's grouping of commits is explicit, the array a caller hands `commitSeries`; the store
// batches nothing of its own at the end of a turn (Durable's Session writer groups the commits that arrive while a
// storage call is in flight, above the store), so there is no flush mechanism to translate here. Go keeps that
// contract with a ticket: a call takes its place under `mu` (so the queue order is the order of the calls that reach
// it) and waits for the ticket before it (a channel the previous entry closes when it settled). `Commit`, `Replay`
// and `Read` block until they settle, as the Pi promises resolve, and so does `Take`'s channel: an export selects on it
// with its context, as Pi's does while its caller awaits the promise. A caller that needs two entries queued in order
// takes the second place after the first one's call has returned, as Pi's `const a = store.commit(...); const b =
// store.commit(...)` does (tests start the first on a goroutine and wait for the queue to show its place). Nothing is
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

// seriesStored is one stored commit of a series: its seq and its created documents.
type seriesStored struct {
	seq     int64
	created []int64
}

// seriesLast is a series as its last commit that wrote leaves it: its seq, the allocator's value after it and the
// table records the series changed.
type seriesLast struct {
	seq    int64
	nextID int64
	tables map[string]*TableRecord
}

// series is a series as its transaction leaves it: each stored commit's seq and created documents; once a commit wrote,
// its last (last is nil before); the commits that wrote and the statements run.
type series struct {
	stored     []seriesStored
	last       *seriesLast
	written    int64
	statements int
}

// seriesProgress is a series while its transaction runs: its first seq's predecessor, each stored commit, its last
// step that wrote, the allocator's value after it, the table records it changed (tables) and the catalog the next
// step reads (a copy of the store's, made once a record changed) and the statements run.
type seriesProgress struct {
	lastSeq    int64
	stored     []seriesStored
	last       *Step
	nextID     int64
	tables     map[string]*TableRecord
	catalog    map[string]*TableRecord
	statements int
}

// nextSeq is the seq the series' next commit takes.
func (progress *seriesProgress) nextSeq() int64 {
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
	failure         error
	// diverged is set once a batch's recorded count did not reproduce (S17): no batch applies any more.
	diverged bool
	// tail is the commit queue's tail: each entry starts after the one before it settled (its channel is closed).
	tail <-chan struct{}
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
	store.mu.Unlock()
	taken := make(chan func(), 1)
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
	store.mu.Unlock()
	return previous, done
}

// Commit commits writes as one seq when the commit queue reaches it: `fence.Admit()` at the step's entry, the writes
// in their order (retirements, creations, the rest), the outbox commit, `lastSeq` and the allocator's value,
// `COMMIT`, then `fence.Adopt` before the next step starts. It returns the seq. A refused admission fails
// `released`. A step the backend rolled back after the step's own code failed (a refusal, D4) fails with that error
// unchanged and the store goes on; any other failure (`BEGIN`, `COMMIT`, `ROLLBACK`, a failing Adopt) is an unknown
// outcome that stops the store (`storage_failed`). ctx counts the step (no abort once queued).
func (store *ChordStore) Commit(writes []StagedWrite, fence CommitFence, ctx context.Context) (int64, error) {
	if err := store.AssertUsable(); err != nil {
		return 0, err
	}
	previous, done := store.ticket()
	<-previous
	defer close(done)
	return store.step(writes, fence, ctx)
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

// step is a commit's turn in the queue.
func (store *ChordStore) step(writes []StagedWrite, fence CommitFence, ctx context.Context) (int64, error) {
	if err := store.AssertUsable(); err != nil {
		return 0, err
	}
	if !fence.Admit() {
		return 0, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonDiscarded, Message: "store: the commit's attempt ended"}, nil)
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
	var done series
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
func (store *ChordStore) writeSeries(executor sqlite.SqliteExecutor, commits []SeriesCommit, lastSeq int64) (done series, refused error, err error) {
	calls := &watchedCalls{}
	watched := &watchedExecutor{SqliteExecutor: executor, calls: calls}
	progress := &seriesProgress{lastSeq: lastSeq, nextID: store.ids.Value(), catalog: store.Tables()}
	for _, commit := range commits {
		refusal, err := store.checkCommit(commit, watched, calls, progress)
		if err != nil {
			return series{}, nil, err
		}
		if refusal != nil {
			refused = refusal
			break
		}
		if err := store.writeCommit(commit, executor, progress); err != nil {
			return series{}, nil, err
		}
	}
	done, err = store.closeSeries(progress)
	return done, refused, err
}

// checkCommit runs commit's check on a step at the series' next seq whose backend calls are watched; it returns its
// refusal, if it refused. A check one of whose backend calls failed returns that as err instead: a failure, not a
// refusal.
func (store *ChordStore) checkCommit(commit SeriesCommit, watched sqlite.SqliteExecutor, calls *watchedCalls, progress *seriesProgress) (refusal error, err error) {
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

// writeCommit writes commit on its own step at the series' next seq (write), unwatched, and keeps the table records it
// changed in a copy of the catalog, made only once one changed; a commit without writes takes no seq.
func (store *ChordStore) writeCommit(commit SeriesCommit, executor sqlite.SqliteExecutor, progress *seriesProgress) error {
	seq := progress.nextSeq()
	if len(commit.Writes) == 0 {
		progress.stored = append(progress.stored, seriesStored{seq: seq - 1, created: []int64{}})
		return nil
	}
	step := NewStep(executor, seq, store.ids, progress.catalog)
	nextID, err := store.write(step, commit.Writes)
	if err != nil {
		return err
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
	progress.stored = append(progress.stored, seriesStored{seq: seq, created: step.Created})
	return nil
}

// closeSeries ends the series' writes with the metadata, once, after its last commit that wrote; it returns what memory
// takes.
func (store *ChordStore) closeSeries(progress *seriesProgress) (series, error) {
	if progress.last == nil {
		return series{stored: progress.stored, statements: progress.statements}, nil
	}
	if err := writeMeta(progress.last, progress.nextID); err != nil {
		return series{}, err
	}
	return series{
		stored:     progress.stored,
		last:       &seriesLast{seq: progress.last.Seq, nextID: progress.nextID, tables: progress.tables},
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

// fail stops the store after err (once; its host is told once); it returns the error the caller fails with.
func (store *ChordStore) fail(err error) *data.ChordError {
	store.mu.Lock()
	var failed func(error)
	if store.state == storeOpen {
		store.state = storeFailed
		store.failure = err
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
