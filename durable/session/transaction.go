package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports packages/durable/src/session/transaction.ts

// AnyTaskRecord is a task record with JSON input, checkpoint, and result.
type AnyTaskRecord = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

const internalScanPageSize = 256

// errTransactionSettled is upstream's "Transaction has settled".
var errTransactionSettled = errors.New("Transaction has settled")

// errUndefinedSeed is upstream copyJson's rejection of an undefined family seed (chord json.ts copy).
var errUndefinedSeed = errors.New("Value contains a non-JSON undefined; expected strict JSON")

// submissionChange is a staged settlement, or the placement of a queued
// submission at its entry (Status placed with Entry).
type submissionChange struct {
	settlement durable.SubmissionSettlement
	placed     bool
	entry      durable.EntryId
}

// applySubmissionChange returns the complete record after one change.
// Placement turns a queued input placed and a queued write done; only a
// placed input can be answered. A settled record stays.
func applySubmissionChange(current durable.SubmissionRecord, change submissionChange) (durable.SubmissionRecord, bool, error) {
	if current.Status == durable.SubmissionDone || current.Status == durable.SubmissionUnanswered {
		return current, false, nil
	}
	if change.placed {
		if current.Status != durable.SubmissionQueued {
			return current, false, fmt.Errorf("Submission %d is not queued", current.Id)
		}
		next := current
		next.Status = durable.SubmissionDone
		if current.Type == durable.SubmissionTypeInput {
			next.Status = durable.SubmissionPlaced
		}
		entry := change.entry
		next.Entry = &entry
		return next, true, nil
	}
	settlement := change.settlement
	if settlement.Status == durable.SubmissionDone && current.Status != durable.SubmissionPlaced {
		return current, false, fmt.Errorf("Submission %d is not a placed input", current.Id)
	}
	// Queued and placed records carry no answer, reason, or detail; an unanswered input keeps its entry.
	next := current
	next.Status = settlement.Status
	if settlement.Status == durable.SubmissionDone {
		answer := settlement.Answer
		next.Answer = &answer
	} else {
		reason := settlement.Reason
		next.Reason = &reason
		next.Detail = settlement.Detail
	}
	return next, true, nil
}

// LoadedDocument is one committed document incarnation owned by the Session
// tracker cache.
type LoadedDocument struct {
	AddressId string
	Record    durable.DocumentRecord
	// ValueVersion is the definition version whose shape the tracked value has; access with another version reloads
	// from Storage.
	ValueVersion int
	Tracker      *delta.Tracker

	mu sync.Mutex
	// storedVersion is the persisted definition version; older while the tracked value is migrated only in memory.
	storedVersion int
	// deltasSinceBase counts stored deltas after the newest base; advanced by adoption so the next predicate call
	// needs no read.
	deltasSinceBase int
}

// StoredVersion returns the persisted definition version.
func (loaded *LoadedDocument) StoredVersion() int {
	loaded.mu.Lock()
	defer loaded.mu.Unlock()
	return loaded.storedVersion
}

func (loaded *LoadedDocument) deltaCount() int {
	loaded.mu.Lock()
	defer loaded.mu.Unlock()
	return loaded.deltasSinceBase
}

// transactionHost holds the Session services a transaction uses while it
// holds the mutation line.
type transactionHost struct {
	storage durable.Storage
	// now is the wall clock in milliseconds for task lifecycle times.
	now func() float64
	// cached returns the cached current incarnation without loading.
	cached func(addressId string) *LoadedDocument
	// load returns the cached current incarnation, cold-loading and migrating it when necessary.
	load func(ctx context.Context, definition *durable.AnyDocDefinition, addressId string, address durable.DocumentAddress) (*LoadedDocument, error)
	// install installs a newly committed incarnation.
	install func(document *LoadedDocument)
	// evict removes a retired incarnation if it is still the cached occupant of its address.
	evict func(addressId string, recordId durable.DocumentId)
	// conversationCreated stages writes that belong to every newly created or forked conversation.
	conversationCreated func(tx *Transaction, record durable.ConversationRecord) error
}

// future is a memoized operation result shared by concurrent callers, like a
// shared Promise.
type future[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func newFuture[T any]() *future[T] { return &future[T]{done: make(chan struct{})} }

func (result *future[T]) resolve(value T, err error) {
	result.value, result.err = value, err
	close(result.done)
}

func (result *future[T]) wait() (T, error) {
	<-result.done
	return result.value, result.err
}

type taskWriteKind int

const (
	taskCreate taskWriteKind = iota + 1
	taskReplace
)

// transactionTask holds committed and candidate state for one task touched by
// this transaction.
type transactionTask struct {
	committedRead             *future[*AnyTaskRecord]
	writeKind                 taskWriteKind
	write                     *AnyTaskRecord
	publicationConversationId *durable.ConversationId
}

// TransactionScope holds the defaults a commit binds to.
type TransactionScope struct {
	// ConversationId is the default Tx.CreateTaskErased conversation.
	ConversationId *durable.ConversationId
	// TaskId is the task whose runtime commit this is; stamped as ByTaskId on appended entries.
	TaskId *durable.TaskId
}

type targetKind int

const (
	targetLoaded targetKind = iota + 1
	targetCreated
	targetForkCopy
	targetRetireOnly
)

// documentTarget is the storage/cache provenance of one staged incarnation.
type documentTarget struct {
	kind     targetKind
	document *LoadedDocument
	create   durable.DocumentCreate
	version  int
	tracker  *delta.Tracker
	source   durable.DocumentCopySource
	record   durable.DocumentRecord
}

// documentEntry is one document incarnation acquired, created, or retired by
// this transaction.
type documentEntry struct {
	addressId string
	address   durable.DocumentAddress
	// definition is nil for definition-free fork copies and retirement entries discovered by a terminal-task scan.
	definition *durable.AnyDocDefinition
	// draft memoizes the public acquisition; nil for metadata-only retirement.
	draft          *future[*delta.Object]
	target         *documentTarget
	change         *delta.Change
	prepared       *delta.Prepared
	retireOnCommit bool
}

// planRecord is the record of a staged incarnation: committed for an
// incarnation that already exists, a create otherwise.
type planRecord struct {
	committed *durable.DocumentRecord
	create    durable.DocumentCreate
}

func (record planRecord) id() durable.DocumentId {
	if record.committed != nil {
		return record.committed.Id
	}
	return record.create.Id
}

func (record planRecord) scope() durable.DocumentRecordScope {
	if record.committed != nil {
		return record.committed.Scope
	}
	return record.create.Scope
}

func (record planRecord) fork() durable.DocumentFork {
	if record.committed != nil {
		return record.committed.Fork
	}
	return record.create.Fork
}

type planChange struct {
	tracker    *delta.Tracker
	prepared   *delta.Prepared
	version    int
	loaded     *LoadedDocument
	definition *durable.AnyDocDefinition
}

// documentPlan is what one staged incarnation writes and publishes, decided
// before Storage admission so adoption only applies it.
type documentPlan struct {
	addressId string
	record    planRecord
	retire    bool
	// content is the creation, copy, or change write; nil when only retirement is written.
	content durable.StorageWrite
	// change is the prepared change of a tracked incarnation; nil for fork copies and retirement-only entries.
	change         *planChange
	conversationId *durable.ConversationId
}

// Transaction is the transaction of one Session commit callback.
//
// Every operation is tracked so callback settlement can reject and drain
// unfinished work. Session calls one settlement method, then either discards
// prepared changes or adopts them once after Storage succeeds.
type Transaction struct {
	host  *transactionHost
	ctx   context.Context
	scope TransactionScope

	mu            sync.Mutex
	pendingOps    int
	pendingIdle   *sync.Cond
	sealed        bool
	hasTableWrite bool

	writes                    []durable.StorageWrite
	createdConversationIds    map[durable.ConversationId]bool
	forkSourceConversationIds map[durable.ConversationId]bool
	forkSourceDocumentIds     map[durable.DocumentId]bool
	tasksById                 map[durable.TaskId]*transactionTask
	taskOrder                 []durable.TaskId
	submissions               map[durable.SubmissionId]durable.SubmissionRecord
	submissionOrder           []durable.SubmissionId
	submissionChanges         []struct {
		id     durable.SubmissionId
		change submissionChange
	}
	plans                   []*documentPlan
	documents               []*documentEntry
	latestDocumentByAddress map[string]*documentEntry
}

var _ durable.Tx = (*Transaction)(nil)

func newTransaction(host *transactionHost, ctx context.Context, scope TransactionScope) *Transaction {
	tx := &Transaction{
		host:                      host,
		ctx:                       ctx,
		scope:                     scope,
		createdConversationIds:    map[durable.ConversationId]bool{},
		forkSourceConversationIds: map[durable.ConversationId]bool{},
		forkSourceDocumentIds:     map[durable.DocumentId]bool{},
		tasksById:                 map[durable.TaskId]*transactionTask{},
		submissions:               map[durable.SubmissionId]durable.SubmissionRecord{},
		latestDocumentByAddress:   map[string]*documentEntry{},
	}
	tx.pendingIdle = sync.NewCond(&tx.mu)
	return tx
}

// ─── Operation tracking ────────────────────────────────────────────────────

func (tx *Transaction) assertOpenLocked() error {
	if tx.sealed {
		return errTransactionSettled
	}
	return nil
}

func (tx *Transaction) assertOpen() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.assertOpenLocked()
}

func (tx *Transaction) beginLocked() { tx.pendingOps++ }

func (tx *Transaction) finish() {
	tx.mu.Lock()
	tx.pendingOps--
	if tx.pendingOps == 0 {
		tx.pendingIdle.Broadcast()
	}
	tx.mu.Unlock()
}

func (tx *Transaction) drainLocked() {
	for tx.pendingOps > 0 {
		tx.pendingIdle.Wait()
	}
}

// read runs a table read: rejected once the transaction settled or wrote a table.
func read[T any](tx *Transaction, method string, run func() (T, error)) (T, error) {
	var zero T
	tx.mu.Lock()
	if err := tx.assertOpenLocked(); err != nil {
		tx.mu.Unlock()
		return zero, err
	}
	if tx.hasTableWrite {
		tx.mu.Unlock()
		return zero, durable.NewReadAfterWrite(method)
	}
	tx.beginLocked()
	tx.mu.Unlock()
	defer tx.finish()
	return run()
}

// write runs a table write: rejected once the transaction settled.
func write[T any](tx *Transaction, run func() (T, error)) (T, error) {
	var zero T
	tx.mu.Lock()
	if err := tx.assertOpenLocked(); err != nil {
		tx.mu.Unlock()
		return zero, err
	}
	tx.hasTableWrite = true
	tx.beginLocked()
	tx.mu.Unlock()
	defer tx.finish()
	return run()
}

// ─── Table reads ───────────────────────────────────────────────────────────

// Conversation returns the committed conversation, or nil.
func (tx *Transaction) Conversation(id durable.ConversationId) (*durable.ConversationRecord, error) {
	return read(tx, "conversation", func() (*durable.ConversationRecord, error) {
		return tx.host.storage.Conversation(tx.ctx, id)
	})
}

// Entry returns the committed entry, or nil.
func (tx *Transaction) Entry(id durable.EntryId) (*durable.EntryRecord, error) {
	return read(tx, "entry", func() (*durable.EntryRecord, error) {
		stored, err := tx.host.storage.Entry(tx.ctx, id)
		if err != nil || stored == nil {
			return nil, err
		}
		return &stored.Entry, nil
	})
}

// Task returns the committed task, or nil.
func (tx *Transaction) Task(id durable.TaskId) (*AnyTaskRecord, error) {
	return read(tx, "task", func() (*AnyTaskRecord, error) { return tx.committedTask(id) })
}

// ScanConversations scans committed conversations.
func (tx *Transaction) ScanConversations(query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return read(tx, "scanConversations", func() (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
		return tx.host.storage.ScanConversations(tx.ctx, query, limit, cursor)
	})
}

// ScanEntries scans committed visible entries newest-first.
func (tx *Transaction) ScanEntries(query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return read(tx, "scanEntries", func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return tx.host.storage.ScanEntries(tx.ctx, query, limit, cursor)
	})
}

// LatestHeadMarker returns the newest visible entry of the conversation that carries a head.
func (tx *Transaction) LatestHeadMarker(conversationId durable.ConversationId) (*durable.EntryRecord, error) {
	return read(tx, "latestHeadMarker", func() (*durable.EntryRecord, error) {
		return tx.host.storage.FindLatestHeadMarker(tx.ctx, conversationId, nil)
	})
}

// ScanTasks scans committed task records.
func (tx *Transaction) ScanTasks(query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[AnyTaskRecord, durable.Cursor], error) {
	return read(tx, "scanTasks", func() (durable.Page[AnyTaskRecord, durable.Cursor], error) {
		return tx.host.storage.ScanTasks(tx.ctx, query, limit, cursor)
	})
}

// Submission returns the committed submission record, or nil.
func (tx *Transaction) Submission(id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return read(tx, "submission", func() (*durable.SubmissionRecord, error) {
		return tx.host.storage.Submission(tx.ctx, id)
	})
}

// SubmissionByRequest returns the committed submission with a conversation-scoped request ID, or nil.
func (tx *Transaction) SubmissionByRequest(conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return read(tx, "submissionByRequest", func() (*durable.SubmissionRecord, error) {
		return tx.host.storage.SubmissionByRequest(tx.ctx, conversationId, requestId)
	})
}

// ─── Table writes ──────────────────────────────────────────────────────────

// CreateConversation creates a conversation with explicitly selected ownership.
func (tx *Transaction) CreateConversation(options durable.CreateConversationOptions) (durable.ConversationRecord, error) {
	return write(tx, func() (durable.ConversationRecord, error) {
		return tx.stageConversation(nil, options.Ownership, nil)
	})
}

// CreateRootConversation is the internal bootstrap path for the reserved root identity.
func (tx *Transaction) CreateRootConversation() (durable.ConversationRecord, error) {
	return write(tx, func() (durable.ConversationRecord, error) {
		root := durable.ROOT_CONVERSATION_ID
		return tx.stageConversation(nil, durable.ConversationOwnership{Kind: durable.ConversationOwnerless}, &root)
	})
}

// ForkConversation creates a history fork at one concrete visible entry.
func (tx *Transaction) ForkConversation(parentConversationId durable.ConversationId, at durable.EntryId, options durable.CreateConversationOptions) (durable.ConversationRecord, error) {
	return write(tx, func() (durable.ConversationRecord, error) {
		return tx.stageConversation(&durable.ConversationParent{ConversationId: parentConversationId, At: at}, options.Ownership, nil)
	})
}

func (tx *Transaction) stageConversation(parent *durable.ConversationParent, ownership durable.ConversationOwnership, reservedId *durable.ConversationId) (durable.ConversationRecord, error) {
	var id durable.ConversationId
	if reservedId != nil {
		id = *reservedId
	} else {
		minted, err := tx.host.storage.MintId()
		if err != nil {
			return durable.ConversationRecord{}, err
		}
		id = durable.ConversationId(minted)
	}
	if err := tx.assertOpen(); err != nil {
		return durable.ConversationRecord{}, err
	}
	record := durable.ConversationRecord{Id: id, Parent: parent}
	if ownership.Kind == durable.ConversationOwnedByTask {
		task, err := tx.currentTask(ownership.TaskId)
		if err != nil {
			return durable.ConversationRecord{}, err
		}
		if err := tx.assertOpen(); err != nil {
			return durable.ConversationRecord{}, err
		}
		if task == nil {
			return durable.ConversationRecord{}, fmt.Errorf("Conversation owner task %d does not exist", ownership.TaskId)
		}
		record.Owner = &durable.ConversationOwner{ConversationId: task.ConversationId, TaskId: ownership.TaskId}
	}
	var copies []forkDocumentCopy
	if parent != nil {
		var err error
		copies, err = prepareForkDocumentCopies(tx.ctx, tx.host.storage, parent.ConversationId, parent.At, id)
		if err != nil {
			return durable.ConversationRecord{}, err
		}
	}
	tx.mu.Lock()
	if err := tx.assertOpenLocked(); err != nil {
		tx.mu.Unlock()
		return durable.ConversationRecord{}, err
	}
	for _, copied := range copies {
		tx.forkSourceDocumentIds[copied.source.Id] = true
		entry := &documentEntry{
			addressId: durable.AddressId(durable.DocumentAddress{Kind: copied.record.Kind, Scope: copied.record.Scope, Key: copied.record.Key}),
			address:   durable.DocumentAddress{Kind: copied.record.Kind, Scope: copied.record.Scope, Key: copied.record.Key},
			target:    &documentTarget{kind: targetForkCopy, create: copied.record, source: copied.source},
		}
		tx.documents = append(tx.documents, entry)
		tx.latestDocumentByAddress[entry.addressId] = entry
	}
	if parent != nil {
		tx.forkSourceConversationIds[parent.ConversationId] = true
	}
	tx.createdConversationIds[id] = true
	tx.writes = append(tx.writes, durable.ConversationWrite{Value: record})
	tx.mu.Unlock()
	if tx.host.conversationCreated != nil {
		if err := tx.host.conversationCreated(tx, record); err != nil {
			return durable.ConversationRecord{}, err
		}
	}
	if err := tx.assertOpen(); err != nil {
		return durable.ConversationRecord{}, err
	}
	return record, nil
}

// AppendEntry appends an entry; the returned record is Session-owned and immutable.
func (tx *Transaction) AppendEntry(conversationId durable.ConversationId, value durable.EntryDraft) (durable.EntryRecord, error) {
	return write(tx, func() (durable.EntryRecord, error) {
		if err := tx.requireConversation(conversationId); err != nil {
			return durable.EntryRecord{}, err
		}
		if err := tx.assertOpen(); err != nil {
			return durable.EntryRecord{}, err
		}
		minted, err := tx.host.storage.MintId()
		if err != nil {
			return durable.EntryRecord{}, err
		}
		if err := tx.assertOpen(); err != nil {
			return durable.EntryRecord{}, err
		}
		id := durable.EntryId(minted)
		record := durable.EntryRecord{
			Id:             id,
			ConversationId: conversationId,
			Kind:           value.Kind,
			Model:          value.Model,
			Data:           value.Data,
			Edits:          value.Edits,
			Head:           value.Head,
			ByTaskId:       tx.scope.TaskId,
		}
		if value.HeadSelf {
			record.Head = &id
		}
		owned, err := copyEntryRecord(record)
		if err != nil {
			return durable.EntryRecord{}, err
		}
		tx.mu.Lock()
		tx.writes = append(tx.writes, durable.EntryWrite{Value: owned})
		tx.mu.Unlock()
		return owned, nil
	})
}

// copyEntryRecord takes ownership of an entry's JSON, as upstream's copyJson
// of the assembled record: Data is strict-copied and the typed model and
// edits are detached through their JSON codec.
func copyEntryRecord(record durable.EntryRecord) (durable.EntryRecord, error) {
	data, err := chord.CopyJSON(record.Data)
	if err != nil {
		return durable.EntryRecord{}, err
	}
	record.Data = data
	if record.Head != nil {
		head := *record.Head
		record.Head = &head
	}
	if record.ByTaskId != nil {
		task := *record.ByTaskId
		record.ByTaskId = &task
	}
	if record.Model == nil && record.Edits == nil {
		return record, nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return durable.EntryRecord{}, err
	}
	var copied durable.EntryRecord
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return durable.EntryRecord{}, err
	}
	copied.Data = data
	return copied, nil
}

// CreateTaskErased creates a task of an erased definition.
func (tx *Transaction) CreateTaskErased(task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions) (durable.TaskId, error) {
	return write(tx, func() (durable.TaskId, error) {
		ownership := options.Ownership
		// transaction.ts:387-389 dereferences options.ownership.kind, so a missing ownership throws before any work.
		if ownership.Kind != durable.TaskOwnedByConversation && ownership.Kind != durable.TaskOwnedByTask {
			return 0, errors.New("Tx.createTask() requires options.ownership")
		}
		var owner *AnyTaskRecord
		if ownership.Kind == durable.TaskOwnedByTask {
			// Validated again against the owner's final candidate during assembly.
			current, err := tx.currentTask(ownership.TaskId)
			if err != nil {
				return 0, err
			}
			if err := tx.assertOpen(); err != nil {
				return 0, err
			}
			if current == nil {
				return 0, fmt.Errorf("Task owner %d does not exist", ownership.TaskId)
			}
			if options.Background {
				return 0, errors.New("A child task cannot be background")
			}
			if options.ConversationId != nil && *options.ConversationId != current.ConversationId {
				return 0, fmt.Errorf("A child task lives in its owner's conversation %d", current.ConversationId)
			}
			owner = current
		}
		var conversationId durable.ConversationId
		switch {
		case owner != nil:
			conversationId = owner.ConversationId
		case options.ConversationId != nil:
			conversationId = *options.ConversationId
		case tx.scope.ConversationId != nil:
			conversationId = *tx.scope.ConversationId
		default:
			return 0, errors.New("Tx.createTask() requires options.conversationId")
		}
		if err := tx.requireConversation(conversationId); err != nil {
			return 0, err
		}
		if err := tx.assertOpen(); err != nil {
			return 0, err
		}
		definition := task.AnyDefinition()
		// Upstream order: initial(input), mint, then copyJson of the assembled record; a throwing initial rejects.
		var checkpoint durable.JsonValue
		if err := callDefinition(func() (err error) {
			checkpoint, err = definition.Initial(input)
			return err
		}); err != nil {
			return 0, err
		}
		minted, err := tx.host.storage.MintId()
		if err != nil {
			return 0, err
		}
		if err := tx.assertOpen(); err != nil {
			return 0, err
		}
		ownedInput, err := chord.CopyJSON(input)
		if err != nil {
			return 0, err
		}
		if checkpoint, err = chord.CopyJSON(checkpoint); err != nil {
			return 0, err
		}
		id := durable.TaskId(minted)
		record := &AnyTaskRecord{
			Id:             id,
			ConversationId: conversationId,
			Kind:           definition.Name,
			Version:        definition.Version,
			Input:          ownedInput,
			Background:     options.Background,
			State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
		}
		if owner != nil {
			ownerId := owner.Id
			record.Owner = &ownerId
		}
		tx.mu.Lock()
		entry := tx.taskEntryLocked(id)
		entry.writeKind, entry.write = taskCreate, record
		tx.mu.Unlock()
		return id, nil
	})
}

// CreateSubmission creates a raw submission record with a fresh ID; no admission rules apply.
func (tx *Transaction) CreateSubmission(create durable.SubmissionCreate) (durable.SubmissionRecord, error) {
	return write(tx, func() (durable.SubmissionRecord, error) {
		if err := tx.requireConversation(create.ConversationId); err != nil {
			return durable.SubmissionRecord{}, err
		}
		if err := tx.assertOpen(); err != nil {
			return durable.SubmissionRecord{}, err
		}
		minted, err := tx.host.storage.MintId()
		if err != nil {
			return durable.SubmissionRecord{}, err
		}
		if err := tx.assertOpen(); err != nil {
			return durable.SubmissionRecord{}, err
		}
		record := create.Record(durable.SubmissionId(minted))
		if record.Detail, err = chord.CopyJSON(record.Detail); err != nil {
			return durable.SubmissionRecord{}, err
		}
		tx.mu.Lock()
		tx.setSubmissionLocked(record)
		tx.mu.Unlock()
		return record, nil
	})
}

func (tx *Transaction) setSubmissionLocked(record durable.SubmissionRecord) {
	if _, ok := tx.submissions[record.Id]; !ok {
		tx.submissionOrder = append(tx.submissionOrder, record.Id)
	}
	tx.submissions[record.Id] = record
}

// SettleSubmission stages a settlement, resolved during assembly against the
// transaction's latest candidate record, falling back to committed state.
func (tx *Transaction) SettleSubmission(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
	detail, err := chord.CopyJSON(settlement.Detail)
	if err != nil {
		return err
	}
	settlement.Detail = detail
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	tx.submissionChanges = append(tx.submissionChanges, struct {
		id     durable.SubmissionId
		change submissionChange
	}{id, submissionChange{settlement: settlement}})
	return nil
}

// PlaceSubmission places a queued submission at entry; resolved like SettleSubmission.
func (tx *Transaction) PlaceSubmission(id durable.SubmissionId, entry durable.EntryId) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	tx.submissionChanges = append(tx.submissionChanges, struct {
		id     durable.SubmissionId
		change submissionChange
	}{id, submissionChange{placed: true, entry: entry}})
	return nil
}

// SetTask replaces one task record completely. Tasks change their own state through their runtime.
func (tx *Transaction) SetTask(value AnyTaskRecord) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	task := tx.taskEntryLocked(value.Id)
	candidate := task.write
	if candidate != nil && candidate.State.Status == durable.TaskTerminal {
		return fmt.Errorf("Task %d already has a terminal candidate", value.Id)
	}
	if candidate != nil && candidate.ConversationId != value.ConversationId {
		return fmt.Errorf("Task %d cannot change conversations", value.Id)
	}
	owned, err := copyTaskRecord(tx.stampTimes(value, candidate))
	if err != nil {
		return err
	}
	if task.writeKind != taskCreate {
		task.writeKind = taskReplace
	}
	task.write = &owned
	return nil
}

// stampTimes sets the lifecycle times: StartedAt on the first change to running, EndedAt on the change to terminal. Once
// set, they carry over from the replaced candidate; records written before they existed lack them.
func (tx *Transaction) stampTimes(value AnyTaskRecord, candidate *AnyTaskRecord) AnyTaskRecord {
	stamp := func(current, carried *float64, stamps bool) *float64 {
		switch {
		case carried != nil:
			return carried
		case current != nil:
			return current
		case stamps:
			now := tx.host.now()
			return &now
		}
		return nil
	}
	var candidateStarted, candidateEnded *float64
	if candidate != nil {
		candidateStarted, candidateEnded = candidate.StartedAt, candidate.EndedAt
	}
	value.StartedAt = stamp(value.StartedAt, candidateStarted, value.State.Status == durable.TaskRunning)
	value.EndedAt = stamp(value.EndedAt, candidateEnded, value.State.Status == durable.TaskTerminal)
	return value
}

// copyTaskRecord takes ownership of a task record's JSON.
func copyTaskRecord(record AnyTaskRecord) (AnyTaskRecord, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return AnyTaskRecord{}, err
	}
	var copied AnyTaskRecord
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return AnyTaskRecord{}, err
	}
	if _, err := chord.CopyJSON(copied.Input); err != nil {
		return AnyTaskRecord{}, err
	}
	return copied, nil
}

// StagedTasks returns the candidate records of the tasks this transaction created or replaced so far.
func (tx *Transaction) StagedTasks() []AnyTaskRecord {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	var records []AnyTaskRecord
	for _, id := range tx.taskOrder {
		if task := tx.tasksById[id]; task.write != nil {
			records = append(records, *task.write)
		}
	}
	return records
}

// StagedConversations returns the conversations this transaction created or forked so far.
func (tx *Transaction) StagedConversations() []durable.ConversationRecord {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	var records []durable.ConversationRecord
	for _, staged := range tx.writes {
		if conversation, ok := staged.(durable.ConversationWrite); ok {
			records = append(records, conversation.Value)
		}
	}
	return records
}

// ─── Documents ─────────────────────────────────────────────────────────────

// Doc returns the document's *delta.Object draft, creating the document when
// absent. args are the owner ID (conversation or task scope), then the
// family key and seed (families). A family access without a seed fails as
// upstream's undefined seed does; an explicit nil seed is JSON null.
func (tx *Transaction) Doc(token durable.AnyDocToken, args ...any) (*delta.Object, error) {
	return tx.doc(token, args)
}

func (tx *Transaction) doc(token durable.AnyDocToken, args []any) (*delta.Object, error) {

	tx.mu.Lock()
	if err := tx.assertOpenLocked(); err != nil {
		tx.mu.Unlock()
		return nil, err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		tx.mu.Unlock()
		return nil, err
	}
	if err := tx.assertTaskDocumentsOpenLocked(resolved); err != nil {
		tx.mu.Unlock()
		return nil, err
	}
	latest := tx.latestDocumentByAddress[resolved.Id]
	if latest != nil && !latest.retireOnCommit {
		if latest.draft != nil {
			tx.mu.Unlock()
			return tx.awaitDraft(latest.draft)
		}
		if latest.target != nil && latest.target.kind == targetForkCopy {
			latest.draft = newFuture[*delta.Object]()
			tx.beginLocked()
			tx.mu.Unlock()
			return tx.settleDraft(latest.draft, func() (*delta.Object, error) {
				return tx.acquireForkCopy(latest, definition)
			})
		}
	}
	var seed durable.JsonValue
	if definition.Family {
		// A missing seed is upstream's undefined argument, which copyJson rejects (transaction.ts doc, json.ts copy);
		// an explicit nil is JSON null. A typed Go seed is stored through its JSON encoding, as Snapshot and CreateTask do.
		if resolved.NextArgument < len(args) {
			seed, err = durable.ToJsonValue(args[resolved.NextArgument])
		} else {
			err = errUndefinedSeed
		}
		if err != nil {
			tx.mu.Unlock()
			return nil, err
		}
	}
	entry := &documentEntry{addressId: resolved.Id, address: resolved.Address, definition: definition}
	tx.documents = append(tx.documents, entry)
	tx.latestDocumentByAddress[entry.addressId] = entry
	// Capture retirement before waiting so a pending old acquisition and its replacement stay distinct.
	skipLoad := latest != nil && latest.retireOnCommit
	entry.draft = newFuture[*delta.Object]()
	tx.beginLocked()
	tx.mu.Unlock()
	return tx.settleDraft(entry.draft, func() (*delta.Object, error) { return tx.acquire(entry, seed, skipLoad) })
}

// settleDraft runs a tracked acquisition and resolves its memoized result.
func (tx *Transaction) settleDraft(draft *future[*delta.Object], acquire func() (*delta.Object, error)) (*delta.Object, error) {
	defer tx.finish()
	value, err := acquire()
	draft.resolve(value, err)
	return value, err
}

// awaitDraft waits for a memoized acquisition as one more tracked operation.
func (tx *Transaction) awaitDraft(draft *future[*delta.Object]) (*delta.Object, error) {
	tx.mu.Lock()
	tx.beginLocked()
	tx.mu.Unlock()
	defer tx.finish()
	return draft.wait()
}

// RetireDoc retires a document; args are the owner ID and family key, without a seed.
func (tx *Transaction) RetireDoc(token durable.AnyDocToken, args ...any) error {
	tx.mu.Lock()
	if err := tx.assertOpenLocked(); err != nil {
		tx.mu.Unlock()
		return err
	}
	definition := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(definition, args)
	if err != nil {
		tx.mu.Unlock()
		return err
	}
	latest := tx.latestDocumentByAddress[resolved.Id]
	if latest != nil && latest.retireOnCommit {
		tx.mu.Unlock()
		return nil
	}
	if latest != nil && latest.target != nil && latest.target.kind == targetForkCopy {
		defer tx.mu.Unlock()
		if err := durable.CheckRecordScope(definition, latest.target.create); err != nil {
			return err
		}
		latest.retireOnCommit = true
		return nil
	}
	if latest != nil && latest.draft != nil {
		// Retirement of an acquired draft persists its final content before retirement.
		latest.retireOnCommit = true
		draft := latest.draft
		tx.beginLocked()
		tx.mu.Unlock()
		defer tx.finish()
		_, err := draft.wait()
		return err
	}
	entry := &documentEntry{addressId: resolved.Id, address: resolved.Address, definition: definition, retireOnCommit: true}
	tx.documents = append(tx.documents, entry)
	tx.latestDocumentByAddress[entry.addressId] = entry
	tx.beginLocked()
	tx.mu.Unlock()
	defer tx.finish()
	return tx.findRetirement(entry)
}

func (tx *Transaction) acquire(entry *documentEntry, seed durable.JsonValue, skipLoad bool) (*delta.Object, error) {
	definition := entry.definition
	var loaded *LoadedDocument
	if !skipLoad {
		var err error
		loaded, err = tx.host.load(tx.ctx, definition, entry.addressId, entry.address)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	if loaded != nil {
		if err := durable.CheckRecordScope(definition, loaded.Record.AsCreate()); err != nil {
			return nil, err
		}
		if err := durable.CheckRecordVersion(definition, loaded.Record.AsCreate(), loaded.StoredVersion()); err != nil {
			return nil, err
		}
		change := loaded.Tracker.BeginChange()
		tx.mu.Lock()
		defer tx.mu.Unlock()
		if err := tx.assertOpenLocked(); err != nil {
			change.Abort()
			return nil, err
		}
		entry.target = &documentTarget{kind: targetLoaded, document: loaded}
		entry.change = change
		return change.State(), nil
	}
	scope := entry.address.Scope
	switch scope.Kind {
	case durable.ScopeConversation:
		if err := tx.requireConversation(scope.ConversationId); err != nil {
			return nil, err
		}
	case durable.ScopeTask:
		task, err := tx.currentTask(scope.TaskId)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("Task %d does not exist", scope.TaskId)
		}
		if task.State.Status == durable.TaskTerminal {
			return nil, fmt.Errorf("Task %d is terminal", scope.TaskId)
		}
	}
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	var initial durable.JsonObject
	err := callDefinition(func() (err error) {
		initial, err = definition.Initial(seed)
		return err
	})
	if err != nil {
		return nil, err
	}
	value, err := chord.CopyJSONObject(initial)
	if err != nil {
		return nil, err
	}
	minted, err := tx.host.storage.MintId()
	if err != nil {
		return nil, err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return nil, err
	}
	tracker := delta.Track(value)
	entry.target = &documentTarget{
		kind:    targetCreated,
		create:  durable.BuildDocumentCreate(definition, entry.address, durable.DocumentId(minted)),
		version: definition.Version,
		tracker: tracker,
	}
	entry.change = tracker.BeginChange()
	return entry.change.State(), nil
}

func (tx *Transaction) acquireForkCopy(entry *documentEntry, definition *durable.AnyDocDefinition) (*delta.Object, error) {
	target := entry.target
	stored, err := tx.host.storage.Document(tx.ctx, target.source.Id, target.source.At)
	if err != nil {
		return nil, err
	}
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("Fork source document %d cannot be read", target.source.Id)
	}
	record := stored.Record
	if record.Scope.Kind != durable.ScopeConversation || record.Kind != target.create.Kind || !sameKey(record.Key, target.create.Key) || record.History != target.create.History || record.Fork != target.create.Fork {
		return nil, fmt.Errorf("Fork source document %d does not match the copied record", target.source.Id)
	}
	value, err := materializeValue(definition, target.create, stored.Version, stored.Value)
	if err != nil {
		return nil, err
	}
	tracker := delta.Track(value)
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return nil, err
	}
	entry.definition = definition
	entry.target = &documentTarget{kind: targetCreated, create: target.create, version: definition.Version, tracker: tracker}
	entry.change = tracker.BeginChange()
	return entry.change.State(), nil
}

func sameKey(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func (tx *Transaction) findRetirement(entry *documentEntry) error {
	var record *durable.DocumentRecord
	if cached := tx.host.cached(entry.addressId); cached != nil {
		committed := cached.Record
		record = &committed
	} else {
		found, err := tx.host.storage.FindDocument(tx.ctx, entry.address, durable.CurrentPoint)
		if err != nil {
			return err
		}
		record = found
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.assertOpenLocked(); err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	if err := durable.CheckRecordScope(entry.definition, record.AsCreate()); err != nil {
		return err
	}
	entry.target = &documentTarget{kind: targetRetireOnly, record: *record}
	return nil
}

// ─── Settlement ────────────────────────────────────────────────────────────

// settleFailure seals after callback failure: abort every change and wait for every pending operation.
func (tx *Transaction) settleFailure() {

	tx.mu.Lock()
	tx.sealed = true
	tx.abortChangesLocked()
	tx.drainLocked()
	tx.mu.Unlock()
}

// settleSuccess seals after callback success, prepares every open change, and assembles the atomic batch. Any
// failure aborts every change before Storage admission.
func (tx *Transaction) settleSuccess() ([]durable.StorageWrite, error) {
	tx.mu.Lock()
	tx.sealed = true
	if tx.pendingOps > 0 {
		tx.abortChangesLocked()
		tx.drainLocked()
		tx.mu.Unlock()
		return nil, errors.New("Session commit callback settled before its pending Tx operations")
	}
	// Prepare every open change; this revokes every draft.
	for _, document := range tx.documents {
		if document.change == nil {
			continue
		}
		prepared, err := document.change.Prepare()
		if err != nil {
			tx.abortChangesLocked()
			tx.mu.Unlock()
			return nil, err
		}
		document.prepared = prepared
	}
	tx.mu.Unlock()
	writes, err := tx.assemble()
	if err != nil {
		tx.mu.Lock()
		tx.abortChangesLocked()
		tx.mu.Unlock()
		return nil, err
	}
	return writes, nil
}

// discard aborts every prepared change after Storage failure or when no write is required.
func (tx *Transaction) discard() {
	tx.mu.Lock()
	tx.abortChangesLocked()
	tx.mu.Unlock()
}

// adopt adopts every prepared change by pointer swap after Storage success and describes the publication.
func (tx *Transaction) adopt(seq durable.Seq) ([]durable.DocumentCommitChange, error) {
	var publications []durable.DocumentCommitChange
	for _, plan := range tx.plans {
		committed := plan.record.committed != nil
		var record durable.DocumentRecord
		if committed {
			record = *plan.record.committed
		} else {
			create := plan.record.create
			record = durable.DocumentRecord{Id: create.Id, Kind: create.Kind, Key: create.Key, CreatedAt: seq, Scope: create.Scope, History: create.History, Fork: create.Fork}
		}
		if plan.retire {
			retiredAt := seq
			record.RetiredAt = &retiredAt
		}
		change := plan.change
		if change != nil {
			loaded := change.loaded
			// A new incarnation is adopted unless it retires in the same commit; a loaded one only when it changed.
			adoptIt := len(change.prepared.Ops()) > 0
			if loaded == nil {
				adoptIt = !plan.retire
			}
			if adoptIt {
				if err := change.tracker.Adopt(change.prepared); err != nil {
					return nil, err
				}
			} else {
				change.prepared.Abort()
			}
			if loaded != nil {
				loaded.mu.Lock()
				if loaded.storedVersion < change.version {
					loaded.storedVersion = change.version
				}
				if content, ok := plan.content.(durable.DocumentChangeWrite); ok {
					if content.Content.Kind == durable.ContentBase {
						loaded.deltasSinceBase = 0
					} else {
						loaded.deltasSinceBase++
					}
				}
				loaded.mu.Unlock()
			} else if !plan.retire {
				tx.host.install(&LoadedDocument{
					AddressId:     plan.addressId,
					Record:        record,
					ValueVersion:  change.version,
					Tracker:       change.tracker,
					storedVersion: change.version,
				})
			}
		}
		conversationId := plan.conversationId
		_, isCopy := plan.content.(durable.DocumentCopyWrite)
		switch {
		case plan.retire:
			if committed {
				tx.host.evict(plan.addressId, record.Id)
			}
			publications = append(publications, durable.DocumentChange{Record: record, ConversationId: conversationId, Ops: []durable.Op{}})
		case isCopy:
			copyWrite := plan.content.(durable.DocumentCopyWrite)
			publications = append(publications, durable.DocumentCopyChange{Record: record, ConversationId: *conversationId, Source: copyWrite.Source})
		case change != nil && publishes(plan):
			ops := change.prepared.Ops()
			if change.loaded == nil {
				ops = []durable.Op{}
			}
			version := change.version
			publications = append(publications, durable.DocumentChange{
				Record:         record,
				ConversationId: conversationId,
				Version:        &version,
				Value:          change.prepared.Value(),
				Ops:            ops,
			})
		}
	}
	return publications, nil
}

func (tx *Transaction) assemble() ([]durable.StorageWrite, error) {
	storage := tx.host.storage
	for _, document := range tx.documents {
		if plan := planDocument(document); plan != nil {
			tx.plans = append(tx.plans, plan)
		}
	}
	if err := tx.rejectForkSourceWrites(); err != nil {
		return nil, err
	}
	if err := tx.validateOwners(); err != nil {
		return nil, err
	}
	for _, id := range tx.taskOrder {
		task := tx.tasksById[id]
		if task.writeKind != taskReplace {
			continue
		}
		committed, err := tx.committedTask(id)
		if err != nil {
			return nil, err
		}
		if committed == nil {
			return nil, fmt.Errorf("Task %d does not exist", id)
		}
		if committed.State.Status == durable.TaskTerminal {
			return nil, fmt.Errorf("Task %d is already terminal", id)
		}
		if committed.ConversationId != task.write.ConversationId {
			return nil, fmt.Errorf("Task %d cannot change conversations", id)
		}
	}

	// Terminal settlement retires every task document, including ones created by this transaction.
	var terminalTaskIds []durable.TaskId
	terminal := map[durable.TaskId]bool{}
	for _, id := range tx.taskOrder {
		if task := tx.tasksById[id]; task.write != nil && task.write.State.Status == durable.TaskTerminal {
			terminalTaskIds = append(terminalTaskIds, id)
			terminal[id] = true
		}
	}
	if len(terminalTaskIds) > 0 {
		retiring := map[durable.DocumentId]bool{}
		for _, plan := range tx.plans {
			scope := plan.record.scope()
			if scope.Kind != durable.ScopeTask || !terminal[scope.TaskId] {
				continue
			}
			plan.retire = true
			retiring[plan.record.id()] = true
		}
		for _, taskId := range terminalTaskIds {
			if tx.tasksById[taskId].writeKind == taskCreate {
				continue
			}
			var cursor durable.Cursor
			for {
				page, err := storage.ScanDocuments(tx.ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId}, At: durable.CurrentPoint}, internalScanPageSize, cursor)
				if err != nil {
					return nil, err
				}
				for _, record := range page.Items {
					if retiring[record.Id] {
						continue
					}
					committed := record
					tx.plans = append(tx.plans, &documentPlan{
						addressId: durable.AddressId(durable.DocumentAddress{Kind: record.Kind, Scope: record.Scope, Key: record.Key}),
						record:    planRecord{committed: &committed},
						retire:    true,
					})
					retiring[record.Id] = true
				}
				if page.Next == nil {
					break
				}
				cursor = *page.Next
			}
		}
	}

	// Resolve publication ownership before Storage admission so adoption remains synchronous.
	for _, plan := range tx.plans {
		if !publishes(plan) {
			continue
		}
		scope := plan.record.scope()
		if scope.Kind == durable.ScopeConversation {
			conversationId := scope.ConversationId
			plan.conversationId = &conversationId
		}
		if scope.Kind != durable.ScopeTask {
			continue
		}
		tx.mu.Lock()
		task := tx.taskEntryLocked(scope.TaskId)
		tx.mu.Unlock()
		if task.publicationConversationId == nil {
			current, err := tx.currentTask(scope.TaskId)
			if err != nil {
				return nil, err
			}
			if current != nil {
				conversationId := current.ConversationId
				task.publicationConversationId = &conversationId
			}
		}
		plan.conversationId = task.publicationConversationId
	}

	for _, staged := range tx.submissionChanges {
		current, ok := tx.submissions[staged.id]
		if !ok {
			committed, err := storage.Submission(tx.ctx, staged.id)
			if err != nil {
				return nil, err
			}
			if committed == nil {
				return nil, fmt.Errorf("Submission %d does not exist", staged.id)
			}
			current = *committed
		}
		next, changed, err := applySubmissionChange(current, staged.change)
		if err != nil {
			return nil, err
		}
		if changed {
			tx.setSubmissionLocked(next)
		}
	}

	writes := tx.writes
	for _, id := range tx.submissionOrder {
		writes = append(writes, durable.SubmissionWrite{Value: tx.submissions[id]})
	}
	for _, id := range tx.taskOrder {
		if task := tx.tasksById[id]; task.write != nil {
			writes = append(writes, durable.TaskWrite{Value: *task.write})
		}
	}
	for _, plan := range tx.plans {
		change := plan.change
		// Checkpoint predicates run last, after every validation.
		if content, ok := plan.content.(durable.DocumentChangeWrite); ok && content.Content.Kind == durable.ContentDelta && change != nil && change.loaded != nil {
			if change.definition != nil && change.definition.CheckpointWhen != nil {
				info := durable.CheckpointInfo{DeltasSinceBase: change.loaded.deltaCount()}
				var base bool
				err := callDefinition(func() (err error) {
					base, err = change.definition.CheckpointWhen(change.prepared.Value(), change.prepared.Ops(), info)
					return err
				})
				if err != nil {
					return nil, err
				}
				if base {
					plan.content = durable.DocumentChangeWrite{Id: content.Id, Content: durable.DocumentContent{Version: change.version, Kind: durable.ContentBase, Value: change.prepared.Value()}}
				}
			}
		}
		if plan.content != nil {
			writes = append(writes, plan.content)
		}
		if plan.retire {
			writes = append(writes, durable.DocumentRetireWrite{Id: plan.record.id()})
		}
	}
	return writes, nil
}

// validateOwners requires new owned work to have a live owner, judged on the
// owner's final candidate: not completing, terminal, or abort-marked.
func (tx *Transaction) validateOwners() error {
	type ownerCheck struct {
		what   string
		taskId durable.TaskId
	}
	var owners []ownerCheck
	for _, staged := range tx.writes {
		if conversation, ok := staged.(durable.ConversationWrite); ok && conversation.Value.Owner != nil {
			owners = append(owners, ownerCheck{"Conversation owner task", conversation.Value.Owner.TaskId})
		}
	}
	for _, id := range tx.taskOrder {
		task := tx.tasksById[id]
		if task.writeKind == taskCreate && task.write.Owner != nil {
			owners = append(owners, ownerCheck{"Task owner", *task.write.Owner})
		}
	}
	for _, owner := range owners {
		task, err := tx.currentTask(owner.taskId)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("%s %d does not exist", owner.what, owner.taskId)
		}
		if task.State.Status == durable.TaskTerminal || task.State.Status == durable.TaskCompleting {
			return fmt.Errorf("%s %d is %s", owner.what, owner.taskId, task.State.Status)
		}
		if task.AbortRequested {
			return fmt.Errorf("%s %d is abort-marked", owner.what, owner.taskId)
		}
	}
	return nil
}

func (tx *Transaction) rejectForkSourceWrites() error {
	for _, plan := range tx.plans {
		if plan.content == nil && !plan.retire {
			continue
		}
		if tx.forkSourceDocumentIds[plan.record.id()] {
			return fmt.Errorf("Cannot change fork source document %d in the fork transaction", plan.record.id())
		}
		scope := plan.record.scope()
		if scope.Kind == durable.ScopeConversation && tx.forkSourceConversationIds[scope.ConversationId] && plan.record.fork() == durable.ForkCurrent {
			return fmt.Errorf("Cannot fork conversation %d while changing its current-policy documents", scope.ConversationId)
		}
	}
	return nil
}

func (tx *Transaction) abortChangesLocked() {
	for _, document := range tx.documents {
		if document.change != nil {
			document.change.Abort()
		}
	}
}

func (tx *Transaction) assertTaskDocumentsOpenLocked(resolved durable.ResolvedAddress) error {
	scope := resolved.Address.Scope
	if scope.Kind != durable.ScopeTask {
		return nil
	}
	if task := tx.tasksById[scope.TaskId]; task != nil && task.write != nil && task.write.State.Status == durable.TaskTerminal {
		return fmt.Errorf("Task %d is terminal", scope.TaskId)
	}
	return nil
}

func (tx *Transaction) requireConversation(id durable.ConversationId) error {
	tx.mu.Lock()
	created := tx.createdConversationIds[id]
	tx.mu.Unlock()
	if created {
		return nil
	}
	conversation, err := tx.host.storage.Conversation(tx.ctx, id)
	if err != nil {
		return err
	}
	if conversation == nil {
		return fmt.Errorf("Conversation %d does not exist", id)
	}
	return nil
}

func (tx *Transaction) taskEntryLocked(id durable.TaskId) *transactionTask {
	task := tx.tasksById[id]
	if task == nil {
		task = &transactionTask{}
		tx.tasksById[id] = task
		tx.taskOrder = append(tx.taskOrder, id)
	}
	return task
}

// currentTask returns the latest candidate task record, falling back to committed state; not a caller table read.
func (tx *Transaction) currentTask(id durable.TaskId) (*AnyTaskRecord, error) {
	tx.mu.Lock()
	if task := tx.tasksById[id]; task != nil && task.write != nil {
		candidate := task.write
		tx.mu.Unlock()
		return candidate, nil
	}
	tx.mu.Unlock()
	return tx.committedTask(id)
}

func (tx *Transaction) committedTask(id durable.TaskId) (*AnyTaskRecord, error) {
	tx.mu.Lock()
	task := tx.taskEntryLocked(id)
	if task.committedRead != nil {
		read := task.committedRead
		tx.mu.Unlock()
		return read.wait()
	}
	read := newFuture[*AnyTaskRecord]()
	task.committedRead = read
	tx.mu.Unlock()
	read.resolve(tx.host.storage.Task(tx.ctx, id))
	return read.wait()
}

// planDocument plans one staged document: its record, content write, and prepared change. Retirement is decided
// later.
func planDocument(document *documentEntry) *documentPlan {
	target := document.target
	if target == nil {
		return nil
	}
	plan := &documentPlan{addressId: document.addressId, retire: document.retireOnCommit}
	switch target.kind {
	case targetCreated:
		prepared := document.prepared
		plan.record = planRecord{create: target.create}
		plan.content = durable.DocumentCreateWrite{Record: target.create, Content: durable.DocumentContent{Version: target.version, Kind: durable.ContentBase, Value: prepared.Value()}}
		plan.change = &planChange{tracker: target.tracker, prepared: prepared, version: target.version}
	case targetForkCopy:
		plan.record = planRecord{create: target.create}
		plan.content = durable.DocumentCopyWrite{Record: target.create, Source: target.source}
	case targetRetireOnly:
		record := target.record
		plan.record = planRecord{committed: &record}
	case targetLoaded:
		loaded := target.document
		definition := document.definition
		prepared := document.prepared
		version := definition.Version
		record := loaded.Record
		plan.record = planRecord{committed: &record}
		// A version change stores a base even without operations; otherwise only a change stores a delta.
		switch {
		case loaded.StoredVersion() < version:
			plan.content = durable.DocumentChangeWrite{Id: record.Id, Content: durable.DocumentContent{Version: version, Kind: durable.ContentBase, Value: prepared.Value()}}
		case len(prepared.Ops()) > 0:
			plan.content = durable.DocumentChangeWrite{Id: record.Id, Content: durable.DocumentContent{Version: version, Kind: durable.ContentDelta, Ops: prepared.Ops()}}
		}
		plan.change = &planChange{tracker: loaded.Tracker, prepared: prepared, version: version, loaded: loaded, definition: definition}
	}
	return plan
}

// publishes reports whether adoption publishes the plan: every creation, copy, and retirement, and a loaded
// incarnation that writes content, which includes a migration-only base so observers of the older shape receive the
// new value.
func publishes(plan *documentPlan) bool {
	return plan.retire || plan.change == nil || plan.change.loaded == nil || plan.content != nil
}

// callDefinition runs a document or task definition callback. Upstream callbacks may throw, which rejects the operation; a
// typed Go callback that cannot return an error panics instead, and the panic becomes the operation's error so the
// commit rolls back exactly as on a throw.
func callDefinition(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return run()
}

// materializeValue is durable.MaterializeDocumentValue with migration panics reported as errors.
func materializeValue(definition *durable.AnyDocDefinition, record durable.DocumentCreate, version int, value durable.JsonObject) (result durable.JsonObject, err error) {
	err = callDefinition(func() (err error) {
		result, err = durable.MaterializeDocumentValue(definition, record, version, value)
		return err
	})
	return result, err
}
