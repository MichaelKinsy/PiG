package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

var errMemoryStorageClosed = errors.New("MemoryStorage is closed")

// MemoryStorageOptions configure MemoryStorage; Now defaults to the wall clock
// in Unix milliseconds.
type MemoryStorageOptions struct {
	Now func() int64
}

// MemoryStorage is the in-memory Storage backend. Commits and forks serialize
// on one FIFO queue admitted at call time; reads observe the latest fully
// applied commit.
type MemoryStorage struct {
	now   func() int64
	queue MutationLine

	mu           sync.RWMutex
	storageState *InMemoryStorageState
	state        int
	closeOnce    sync.Once
}

// NewMemoryStorage returns an empty open MemoryStorage.
func NewMemoryStorage(options *MemoryStorageOptions) *MemoryStorage {
	now := func() int64 { return time.Now().UnixMilli() }
	if options != nil && options.Now != nil {
		now = options.Now
	}
	return &MemoryStorage{now: now, storageState: NewInMemoryStorageState()}
}

// admit enqueues op on the commit queue if the storage is open.
func (storage *MemoryStorage) admit(op func() (any, error)) (*LineJob, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if storage.state != sessionOpen {
		return nil, errMemoryStorageClosed
	}
	return storage.queue.Enqueue(op), nil
}

// Commit applies writes atomically after every earlier admitted commit.
func (storage *MemoryStorage) Commit(_ context.Context, writes []Write) (CommitResult, error) {
	job, err := storage.admit(func() (any, error) {
		storage.mu.Lock()
		defer storage.mu.Unlock()
		prepared, err := storage.storageState.PrepareCommit(writes, storage.now())
		if err != nil {
			return nil, err
		}
		result := prepared.Result
		result.Stats = storage.storageState.ApplyValidated(prepared.Writes)
		return result, nil
	})
	if err != nil {
		return CommitResult{}, err
	}
	value, err := job.Wait()
	if err != nil {
		return CommitResult{}, err
	}
	return value.(CommitResult), nil
}

// read runs a read against the materialized state while open.
func memoryRead[T any](storage *MemoryStorage, read func(state *InMemoryStorageState) (T, error)) (T, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if storage.state != sessionOpen {
		var zero T
		return zero, errMemoryStorageClosed
	}
	return read(storage.storageState)
}

// GetEntries returns the requested existing entries.
func (storage *MemoryStorage) GetEntries(_ context.Context, ids []string) (map[string]Entry, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) (map[string]Entry, error) { return state.GetEntries(ids), nil })
}

// GetValue returns one current value, or nil.
func (storage *MemoryStorage) GetValue(_ context.Context, address StoredAddressBase) (*StoredValue[any], error) {
	return memoryRead(storage, func(state *InMemoryStorageState) (*StoredValue[any], error) { return state.GetValue(address), nil })
}

// ScanValues returns values under a namespace-scoped key prefix.
func (storage *MemoryStorage) ScanValues(_ context.Context, prefix StoredAddressBase) ([]StoredValue[any], error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]StoredValue[any], error) { return state.ScanValues(prefix), nil })
}

// ReadList returns one list page.
func (storage *MemoryStorage) ReadList(_ context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]ListElement[any], error) { return state.ReadList(address, options) })
}

// ScanBranch scans one branch path.
func (storage *MemoryStorage) ScanBranch(_ context.Context, query StorageBranchScan) ([]Entry, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]Entry, error) { return state.ScanBranch(query) })
}

// ScanBranchStructure scans one branch path without payloads.
func (storage *MemoryStorage) ScanBranchStructure(_ context.Context, query StorageBranchScan) ([]EntryStructure, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]EntryStructure, error) { return state.ScanBranchStructure(query) })
}

// ScanEntries scans the entry inventory.
func (storage *MemoryStorage) ScanEntries(_ context.Context, query EntryScan) ([]Entry, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]Entry, error) { return state.ScanEntries(query), nil })
}

// ScanUsage scans the usage ledger.
func (storage *MemoryStorage) ScanUsage(_ context.Context, query UsageScan) ([]UsageRow, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) ([]UsageRow, error) { return state.ScanUsage(query), nil })
}

// GetStats returns the maintained totals.
func (storage *MemoryStorage) GetStats(context.Context) (SessionStats, error) {
	return memoryRead(storage, func(state *InMemoryStorageState) (SessionStats, error) { return state.GetStats(), nil })
}

// Fork constructs a destination storage at one serialized boundary between
// source commits.
func (storage *MemoryStorage) Fork(options ForkOptions) (*MemoryStorage, error) {
	job, err := storage.admit(func() (any, error) {
		storage.mu.RLock()
		defer storage.mu.RUnlock()
		forked, err := storage.storageState.CreateFork(options)
		if err != nil {
			return nil, err
		}
		destination := NewMemoryStorage(&MemoryStorageOptions{Now: storage.now})
		destination.storageState = forked
		return destination, nil
	})
	if err != nil {
		return nil, err
	}
	value, err := job.Wait()
	if err != nil {
		return nil, err
	}
	return value.(*MemoryStorage), nil
}

// Close rejects later calls, drains admitted commits, and is idempotent.
func (storage *MemoryStorage) Close(context.Context) error {
	storage.closeOnce.Do(func() {
		storage.mu.Lock()
		storage.state = sessionClosing
		storage.mu.Unlock()
		drained := storage.queue.Enqueue(func() (any, error) { return nil, nil })
		_, _ = drained.Wait()
		storage.mu.Lock()
		storage.state = sessionClosed
		storage.mu.Unlock()
	})
	return nil
}

// memoryStorageVersion is the storage version of every Memory session.
const memoryStorageVersion = 1

type memorySessionRecord struct {
	metadata SessionMetadata
	storage  *MemoryStorage
	session  *StorageBackedSession
	open     bool
}

// memorySessionFacade is one open handle of a retained Memory session. Close
// drains the handle's admitted calls, including explicit mutations until End,
// without closing the retained session or storage.
type memorySessionFacade struct {
	session  *StorageBackedSession
	onClose  func()
	mu       sync.Mutex
	state    int
	admitted sync.WaitGroup
	once     sync.Once
}

func (facade *memorySessionFacade) enter() error {
	facade.mu.Lock()
	defer facade.mu.Unlock()
	if facade.state != sessionOpen {
		return ErrSessionClosed
	}
	facade.admitted.Add(1)
	return nil
}

func (facade *memorySessionFacade) isOpen() bool {
	facade.mu.Lock()
	defer facade.mu.Unlock()
	return facade.state == sessionOpen
}

func facadeCall[T any](facade *memorySessionFacade, operation func() (T, error)) (T, error) {
	if err := facade.enter(); err != nil {
		var zero T
		return zero, err
	}
	defer facade.admitted.Done()
	return operation()
}

func (facade *memorySessionFacade) Metadata() SessionMetadata { return facade.session.Metadata() }
func (facade *memorySessionFacade) IdGenerator() IdGenerator  { return facade.session.IdGenerator() }

func (facade *memorySessionFacade) BeginMutation(ctx context.Context) (SessionMutation, error) {
	if err := facade.enter(); err != nil {
		return nil, err
	}
	source, err := facade.session.BeginMutation(ctx)
	if err != nil {
		facade.admitted.Done()
		return nil, err
	}
	if !facade.isOpen() {
		_ = source.End(ctx)
		facade.admitted.Done()
		return nil, ErrSessionClosed
	}
	return &facadeMutation{SessionMutation: source, finish: sync.OnceFunc(facade.admitted.Done)}, nil
}

type facadeMutation struct {
	SessionMutation
	finish func()
}

func (mutation *facadeMutation) End(ctx context.Context) error {
	defer mutation.finish()
	return mutation.SessionMutation.End(ctx)
}

func (facade *memorySessionFacade) Mutate(ctx context.Context, mutation SessionMutationCallback) (any, error) {
	return facadeCall(facade, func() (any, error) {
		return facade.session.Mutate(ctx, func(ctx context.Context, mutator SessionMutator) (any, error) {
			if !facade.isOpen() {
				return nil, ErrSessionClosed
			}
			return mutation(ctx, mutator)
		})
	})
}

func (facade *memorySessionFacade) GetEntries(ctx context.Context, ids []string) (map[string]Entry, error) {
	return facadeCall(facade, func() (map[string]Entry, error) { return facade.session.GetEntries(ctx, ids) })
}

func (facade *memorySessionFacade) GetEntry(ctx context.Context, id string) (*Entry, error) {
	return facadeCall(facade, func() (*Entry, error) { return facade.session.GetEntry(ctx, id) })
}

func (facade *memorySessionFacade) GetValue(ctx context.Context, address StoredAddressBase) (*StoredValue[any], error) {
	return facadeCall(facade, func() (*StoredValue[any], error) { return facade.session.GetValue(ctx, address) })
}

func (facade *memorySessionFacade) ScanValues(ctx context.Context, prefix StoredAddressBase) ([]StoredValue[any], error) {
	return facadeCall(facade, func() ([]StoredValue[any], error) { return facade.session.ScanValues(ctx, prefix) })
}

func (facade *memorySessionFacade) ReadList(ctx context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	return facadeCall(facade, func() ([]ListElement[any], error) { return facade.session.ReadList(ctx, address, options) })
}

func (facade *memorySessionFacade) ScanBranch(ctx context.Context, query StorageBranchScan) ([]Entry, error) {
	return facadeCall(facade, func() ([]Entry, error) { return facade.session.ScanBranch(ctx, query) })
}

func (facade *memorySessionFacade) GetStats(ctx context.Context) (SessionStats, error) {
	return facadeCall(facade, func() (SessionStats, error) { return facade.session.GetStats(ctx) })
}

func (facade *memorySessionFacade) GetName(ctx context.Context) (*string, error) {
	return facadeCall(facade, func() (*string, error) { return facade.session.GetName(ctx) })
}

func (facade *memorySessionFacade) GetLabel(ctx context.Context, targetID string) (*string, error) {
	return facadeCall(facade, func() (*string, error) { return facade.session.GetLabel(ctx, targetID) })
}

func (facade *memorySessionFacade) FindEntries(ctx context.Context, query *EntryQuery) ([]Entry, error) {
	return facadeCall(facade, func() ([]Entry, error) { return facade.session.FindEntries(ctx, query) })
}

func (facade *memorySessionFacade) FindEntry(ctx context.Context, query *EntryQuery) (*Entry, error) {
	return facadeCall(facade, func() (*Entry, error) { return facade.session.FindEntry(ctx, query) })
}

func (facade *memorySessionFacade) Branch(ctx context.Context, name string) (Branch, error) {
	branch, err := facadeCall(facade, func() (Branch, error) { return facade.session.Branch(ctx, name) })
	if err != nil || branch == nil {
		return nil, err
	}
	return &facadeBranch{facade: facade, branch: branch}, nil
}

func (facade *memorySessionFacade) CreateBranch(ctx context.Context, name string, at *string) (Branch, error) {
	branch, err := facadeCall(facade, func() (Branch, error) { return facade.session.CreateBranch(ctx, name, at) })
	if err != nil {
		return nil, err
	}
	return &facadeBranch{facade: facade, branch: branch}, nil
}

func (facade *memorySessionFacade) SetValue(ctx context.Context, address StoredAddressBase, next any) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.SetValue(ctx, address, next) })
	return err
}

func (facade *memorySessionFacade) DeleteValue(ctx context.Context, address StoredAddressBase) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.DeleteValue(ctx, address) })
	return err
}

func (facade *memorySessionFacade) AppendList(ctx context.Context, address StoredAddressBase, element any) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.AppendList(ctx, address, element) })
	return err
}

func (facade *memorySessionFacade) DeleteList(ctx context.Context, address StoredAddressBase) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.DeleteList(ctx, address) })
	return err
}

func (facade *memorySessionFacade) SetName(ctx context.Context, name *string) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.SetName(ctx, name) })
	return err
}

func (facade *memorySessionFacade) SetLabel(ctx context.Context, targetID string, label *string) error {
	_, err := facadeCall(facade, func() (any, error) { return nil, facade.session.SetLabel(ctx, targetID, label) })
	return err
}

// Close drains admitted calls on this handle and releases the handle.
func (facade *memorySessionFacade) Close(context.Context) error {
	facade.once.Do(func() {
		facade.mu.Lock()
		facade.state = sessionClosing
		facade.mu.Unlock()
		facade.admitted.Wait()
		facade.mu.Lock()
		facade.state = sessionClosed
		facade.mu.Unlock()
		facade.onClose()
	})
	return nil
}

type facadeBranch struct {
	facade *memorySessionFacade
	branch Branch
}

func (branch *facadeBranch) Name() string { return branch.branch.Name() }

func (branch *facadeBranch) GetTipID(ctx context.Context) (*string, error) {
	return facadeCall(branch.facade, func() (*string, error) { return branch.branch.GetTipID(ctx) })
}

func (branch *facadeBranch) FindEntries(ctx context.Context, query *BranchScan) ([]Entry, error) {
	return facadeCall(branch.facade, func() ([]Entry, error) { return branch.branch.FindEntries(ctx, query) })
}

func (branch *facadeBranch) FindEntry(ctx context.Context, query *BranchScan) (*Entry, error) {
	return facadeCall(branch.facade, func() (*Entry, error) { return branch.branch.FindEntry(ctx, query) })
}

func (branch *facadeBranch) AppendMessage(ctx context.Context, message agent.AgentMessage) (string, error) {
	return facadeCall(branch.facade, func() (string, error) { return branch.branch.AppendMessage(ctx, message) })
}

func (branch *facadeBranch) AppendCustomEntry(ctx context.Context, customType string, data *JsonValue) (string, error) {
	return facadeCall(branch.facade, func() (string, error) { return branch.branch.AppendCustomEntry(ctx, customType, data) })
}

// MemorySessionRepoOptions configure MemorySessionRepo; Now defaults to the
// wall clock in Unix milliseconds.
type MemorySessionRepoOptions struct {
	Now func() int64
}

// MemorySessionRepo retains Memory sessions for the life of the process. Each
// session has at most one open handle at a time.
type MemorySessionRepo struct {
	now        func() int64
	mu         sync.Mutex
	sessions   map[string]*memorySessionRecord
	order      []string
	pendingIDs map[string]bool
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

// NewMemorySessionRepo returns an empty repository.
func NewMemorySessionRepo(options *MemorySessionRepoOptions) *MemorySessionRepo {
	now := func() int64 { return time.Now().UnixMilli() }
	if options != nil && options.Now != nil {
		now = options.Now
	}
	return &MemorySessionRepo{now: now, sessions: map[string]*memorySessionRecord{}, pendingIDs: map[string]bool{}}
}

var errMemoryRepoClosed = errors.New("MemorySessionRepo is closed")

func (repo *MemorySessionRepo) reserveID(id string) error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.closed {
		return errMemoryRepoClosed
	}
	if _, exists := repo.sessions[id]; exists || repo.pendingIDs[id] {
		return errors.New("Session already exists: " + id)
	}
	repo.pendingIDs[id] = true
	return nil
}

func (repo *MemorySessionRepo) mintID(requested string, createdAt int64) (string, error) {
	if requested != "" {
		return requested, nil
	}
	return UUIDv7(&createdAt)
}

// publish records a new open session and releases its reservation.
func (repo *MemorySessionRepo) publish(metadata SessionMetadata, storage *MemoryStorage) Session {
	record := &memorySessionRecord{metadata: metadata, storage: storage, session: NewStorageBackedSession(metadata, storage, nil), open: true}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	delete(repo.pendingIDs, metadata.ID)
	repo.sessions[metadata.ID] = record
	repo.order = append(repo.order, metadata.ID)
	return repo.openRecord(record)
}

func (repo *MemorySessionRepo) release(id string) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	delete(repo.pendingIDs, id)
}

// Create creates an empty session with no Branch.
func (repo *MemorySessionRepo) Create(_ context.Context, options SessionCreateOptions) (Session, error) {
	if err := repo.assertOpen(); err != nil {
		return nil, err
	}
	createdAt := repo.now()
	id, err := repo.mintID(options.ID, createdAt)
	if err != nil {
		return nil, err
	}
	if err := repo.reserveID(id); err != nil {
		return nil, err
	}
	metadata := SessionMetadata{ID: id, CreatedAt: createdAt, StorageVersion: memoryStorageVersion, ParentSessionID: options.ParentSessionID}
	return repo.publish(metadata, NewMemoryStorage(&MemoryStorageOptions{Now: repo.now})), nil
}

// Open returns a fresh handle to a closed retained session.
func (repo *MemorySessionRepo) Open(_ context.Context, metadata SessionMetadata) (Session, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.closed {
		return nil, errMemoryRepoClosed
	}
	record, ok := repo.sessions[metadata.ID]
	if !ok {
		return nil, errors.New("Unknown session: " + metadata.ID)
	}
	if record.open {
		return nil, errors.New("Session is already open: " + metadata.ID)
	}
	record.open = true
	return repo.openRecord(record), nil
}

// List returns every retained session's metadata in creation order.
func (repo *MemorySessionRepo) List(context.Context) ([]SessionMetadata, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.closed {
		return nil, errMemoryRepoClosed
	}
	listed := make([]SessionMetadata, 0, len(repo.order))
	for _, id := range repo.order {
		listed = append(listed, repo.sessions[id].metadata)
	}
	return listed, nil
}

// Delete removes a closed session.
func (repo *MemorySessionRepo) Delete(ctx context.Context, metadata SessionMetadata) error {
	repo.mu.Lock()
	if repo.closed {
		repo.mu.Unlock()
		return errMemoryRepoClosed
	}
	record, ok := repo.sessions[metadata.ID]
	if !ok {
		repo.mu.Unlock()
		return errors.New("Unknown session: " + metadata.ID)
	}
	if record.open {
		repo.mu.Unlock()
		return errors.New("Session is open: " + metadata.ID)
	}
	repo.mu.Unlock()
	if err := record.session.Close(ctx); err != nil {
		return err
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	delete(repo.sessions, metadata.ID)
	for index, id := range repo.order {
		if id == metadata.ID {
			repo.order = append(repo.order[:index], repo.order[index+1:]...)
			break
		}
	}
	return nil
}

// Fork copies source state into a new open session at one serialized
// boundary between source commits.
func (repo *MemorySessionRepo) Fork(_ context.Context, source SessionMetadata, options ForkOptions) (Session, error) {
	if err := repo.assertOpen(); err != nil {
		return nil, err
	}
	repo.mu.Lock()
	sourceRecord, ok := repo.sessions[source.ID]
	repo.mu.Unlock()
	if !ok {
		return nil, errors.New("Unknown session: " + source.ID)
	}
	createdAt := repo.now()
	id, err := repo.mintID(options.ID, createdAt)
	if err != nil {
		return nil, err
	}
	if err := repo.reserveID(id); err != nil {
		return nil, err
	}
	storage, err := sourceRecord.storage.Fork(options)
	if err != nil {
		repo.release(id)
		return nil, err
	}
	metadata := SessionMetadata{ID: id, CreatedAt: createdAt, StorageVersion: memoryStorageVersion, ParentSessionID: sourceRecord.metadata.ID}
	return repo.publish(metadata, storage), nil
}

// Close closes every retained session; it is idempotent.
func (repo *MemorySessionRepo) Close(ctx context.Context) error {
	repo.closeOnce.Do(func() {
		repo.mu.Lock()
		repo.closed = true
		records := make([]*memorySessionRecord, 0, len(repo.sessions))
		for _, id := range repo.order {
			records = append(records, repo.sessions[id])
		}
		repo.mu.Unlock()
		errs := make([]error, len(records))
		var group sync.WaitGroup
		for index, record := range records {
			group.Go(func() { errs[index] = record.session.Close(ctx) })
		}
		group.Wait()
		repo.closeErr = errors.Join(errs...)
	})
	return repo.closeErr
}

func (repo *MemorySessionRepo) openRecord(record *memorySessionRecord) Session {
	return &memorySessionFacade{session: record.session, onClose: func() {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		record.open = false
	}}
}

func (repo *MemorySessionRepo) assertOpen() error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.closed {
		return errMemoryRepoClosed
	}
	return nil
}
