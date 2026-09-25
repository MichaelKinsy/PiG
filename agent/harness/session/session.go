package session

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Storage is the durable backend of one session.
type Storage interface {
	Commit(ctx context.Context, writes []Write) (CommitResult, error)
	GetEntries(ctx context.Context, ids []string) (map[string]Entry, error)
	GetValue(ctx context.Context, address StoredAddressBase) (*StoredValue[any], error)
	ScanValues(ctx context.Context, prefix StoredAddressBase) ([]StoredValue[any], error)
	ReadList(ctx context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error)
	ScanBranch(ctx context.Context, query StorageBranchScan) ([]Entry, error)
	ScanBranchStructure(ctx context.Context, query StorageBranchScan) ([]EntryStructure, error)
	ScanEntries(ctx context.Context, query EntryScan) ([]Entry, error)
	ScanUsage(ctx context.Context, query UsageScan) ([]UsageRow, error)
	GetStats(ctx context.Context) (SessionStats, error)
	Close(ctx context.Context) error
}

// SessionReader is the bounded read surface of a Session and of mutation
// capabilities. Use GetValue, ScanValues, and ReadList for typed reads.
type SessionReader interface {
	GetEntries(ctx context.Context, ids []string) (map[string]Entry, error)
	GetStats(ctx context.Context) (SessionStats, error)
	GetValue(ctx context.Context, address StoredAddressBase) (*StoredValue[any], error)
	ScanValues(ctx context.Context, prefix StoredAddressBase) ([]StoredValue[any], error)
	ReadList(ctx context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error)
	// ScanBranch scans from an explicit entry while the capability is valid.
	ScanBranch(ctx context.Context, query StorageBranchScan) ([]Entry, error)
}

// SessionMutator is the callback-scoped mutation capability: bounded reads and
// exactly zero or one commit attempt.
type SessionMutator interface {
	SessionReader
	Commit(ctx context.Context, writes []Write) (CommitResult, error)
}

// SessionMutation is the exclusive keyless mutation barrier of one Session.
// Every BeginMutation caller must call End.
type SessionMutation interface {
	SessionMutator
	// End waits for any commit attempt, invalidates the capability, and
	// releases the barrier.
	End(ctx context.Context) error
}

// SessionMutationCallback runs under the Session mutation line.
type SessionMutationCallback func(ctx context.Context, mutator SessionMutator) (any, error)

// Branch is one named data path with a movable tip.
type Branch interface {
	Name() string
	// GetTipID returns nil for an empty Branch.
	GetTipID(ctx context.Context) (*string, error)
	FindEntries(ctx context.Context, query *BranchScan) ([]Entry, error)
	// FindEntry returns nil when nothing matches.
	FindEntry(ctx context.Context, query *BranchScan) (*Entry, error)
	AppendMessage(ctx context.Context, message agent.AgentMessage) (string, error)
	// AppendCustomEntry appends a custom entry; nil data means no data.
	AppendCustomEntry(ctx context.Context, customType string, data *JsonValue) (string, error)
}

// Session owns global metadata, values and lists, entry and usage queries,
// Branch discovery and creation, one mutation line, and one backend lifecycle.
type Session interface {
	SessionReader
	Metadata() SessionMetadata
	IdGenerator() IdGenerator
	GetEntry(ctx context.Context, id string) (*Entry, error)
	GetName(ctx context.Context) (*string, error)
	GetLabel(ctx context.Context, targetID string) (*string, error)
	FindEntries(ctx context.Context, query *EntryQuery) ([]Entry, error)
	FindEntry(ctx context.Context, query *EntryQuery) (*Entry, error)
	// Branch returns nil when the Branch does not exist.
	Branch(ctx context.Context, name string) (Branch, error)
	CreateBranch(ctx context.Context, name string, at *string) (Branch, error)
	BeginMutation(ctx context.Context) (SessionMutation, error)
	// Mutate runs a trusted exclusive callback over the mutation line. A public
	// Session writer called from the callback queues behind it, so waiting for
	// it deadlocks; use the supplied mutator for the callback's sole commit.
	Mutate(ctx context.Context, mutation SessionMutationCallback) (any, error)
	SetValue(ctx context.Context, address StoredAddressBase, next any) error
	DeleteValue(ctx context.Context, address StoredAddressBase) error
	AppendList(ctx context.Context, address StoredAddressBase, element any) error
	DeleteList(ctx context.Context, address StoredAddressBase) error
	// SetName deletes the name when name is nil.
	SetName(ctx context.Context, name *string) error
	// SetLabel deletes the label when label is nil.
	SetLabel(ctx context.Context, targetID string, label *string) error
	Close(ctx context.Context) error
}

// Mutate runs a typed callback over a Session's mutation line.
func Mutate[T any](ctx context.Context, session Session, mutation func(ctx context.Context, mutator SessionMutator) (T, error)) (T, error) {
	result, err := session.Mutate(ctx, func(ctx context.Context, mutator SessionMutator) (any, error) {
		return mutation(ctx, mutator)
	})
	typed, _ := result.(T)
	return typed, err
}

// SessionInvariantError reports inconsistent durable session state that
// cannot be safely advanced.
type SessionInvariantError struct{ Message string }

func (err *SessionInvariantError) Error() string { return err.Message }

// SessionInvalidBranchError reports an invalid Branch name.
type SessionInvalidBranchError struct {
	Branch string
	Reason string
}

func (err *SessionInvalidBranchError) Error() string {
	return "Invalid branch " + jsonQuote(err.Branch) + ": " + err.Reason
}

// SessionBranchExistsError reports a Branch that already exists.
type SessionBranchExistsError struct{ Branch string }

func (err *SessionBranchExistsError) Error() string { return "Branch already exists: " + err.Branch }

// SessionPendingAssistantMessageError rejects persisting a pending assistant
// message.
type SessionPendingAssistantMessageError struct{}

func (*SessionPendingAssistantMessageError) Error() string {
	return "Cannot persist a pending assistant message"
}

// SessionUnknownTargetError reports an absent target entry.
type SessionUnknownTargetError struct{ TargetID string }

func (err *SessionUnknownTargetError) Error() string { return "Unknown target: " + err.TargetID }

// ErrSessionClosed rejects Session calls after close.
var ErrSessionClosed = errors.New("Session is closed")

var (
	errMutatorOutside      = errors.New("SessionMutator cannot be used outside its mutation callback")
	errCommitAttempted     = errors.New("SessionMutator commit already attempted")
	errPendingAssistantMsg = &SessionPendingAssistantMessageError{}
)

// IsPendingAssistant reports whether message is an assistant message whose
// stopReason is "pending".
func IsPendingAssistant(message agent.AgentMessage) bool {
	return message.Assistant != nil && message.Assistant.StopReason == ai.StopReasonPending
}

type storageBackedSessionMutation struct {
	storage    Storage
	release    func()
	mu         sync.Mutex
	active     bool
	attempted  bool
	commitDone chan struct{}
	endOnce    sync.Once
}

func (mutation *storageBackedSessionMutation) Commit(ctx context.Context, writes []Write) (CommitResult, error) {
	mutation.mu.Lock()
	if !mutation.active {
		mutation.mu.Unlock()
		return CommitResult{}, errMutatorOutside
	}
	if mutation.attempted {
		mutation.mu.Unlock()
		return CommitResult{}, errCommitAttempted
	}
	mutation.attempted = true
	done := make(chan struct{})
	mutation.commitDone = done
	mutation.mu.Unlock()
	defer close(done)
	for _, write := range writes {
		if entry, ok := write.(EntryWrite); ok && entry.Entry.Type == EntryTypeMessage && IsPendingAssistant(entry.Entry.Message) {
			return CommitResult{}, errPendingAssistantMsg
		}
	}
	return mutation.storage.Commit(ctx, writes)
}

func (mutation *storageBackedSessionMutation) End(context.Context) error {
	mutation.endOnce.Do(func() {
		mutation.mu.Lock()
		mutation.active = false
		done := mutation.commitDone
		mutation.mu.Unlock()
		if done != nil {
			<-done
		}
		mutation.release()
	})
	return nil
}

func (mutation *storageBackedSessionMutation) assertActive() error {
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if !mutation.active {
		return errMutatorOutside
	}
	return nil
}

func (mutation *storageBackedSessionMutation) GetEntries(ctx context.Context, ids []string) (map[string]Entry, error) {
	if err := mutation.assertActive(); err != nil {
		return nil, err
	}
	return mutation.storage.GetEntries(ctx, ids)
}

func (mutation *storageBackedSessionMutation) GetStats(ctx context.Context) (SessionStats, error) {
	if err := mutation.assertActive(); err != nil {
		return SessionStats{}, err
	}
	return mutation.storage.GetStats(ctx)
}

func (mutation *storageBackedSessionMutation) GetValue(ctx context.Context, address StoredAddressBase) (*StoredValue[any], error) {
	if err := mutation.assertActive(); err != nil {
		return nil, err
	}
	return mutation.storage.GetValue(ctx, address)
}

func (mutation *storageBackedSessionMutation) ScanValues(ctx context.Context, prefix StoredAddressBase) ([]StoredValue[any], error) {
	if err := mutation.assertActive(); err != nil {
		return nil, err
	}
	return mutation.storage.ScanValues(ctx, prefix)
}

func (mutation *storageBackedSessionMutation) ReadList(ctx context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	if err := mutation.assertActive(); err != nil {
		return nil, err
	}
	return mutation.storage.ReadList(ctx, address, options)
}

func (mutation *storageBackedSessionMutation) ScanBranch(ctx context.Context, query StorageBranchScan) ([]Entry, error) {
	if err := mutation.assertActive(); err != nil {
		return nil, err
	}
	return mutation.storage.ScanBranch(ctx, query)
}

type storageBackedBranch struct {
	name    string
	session *StorageBackedSession
}

func (branch *storageBackedBranch) Name() string { return branch.name }

func (branch *storageBackedBranch) GetTipID(ctx context.Context) (*string, error) {
	return branch.session.GetBranchTip(ctx, branch.name)
}

func (branch *storageBackedBranch) FindEntries(ctx context.Context, query *BranchScan) ([]Entry, error) {
	if query == nil {
		query = &BranchScan{}
	}
	start := query.Start
	if start == nil {
		tip, err := branch.GetTipID(ctx)
		if err != nil {
			return nil, err
		}
		if tip == nil {
			return []Entry{}, nil
		}
		start = tip
	}
	return branch.session.ScanBranch(ctx, storageScan(*query, *start))
}

func (branch *storageBackedBranch) FindEntry(ctx context.Context, query *BranchScan) (*Entry, error) {
	return firstEntry(branch.FindEntries(ctx, withSingleLimitBranch(query)))
}

func (branch *storageBackedBranch) AppendMessage(ctx context.Context, message agent.AgentMessage) (string, error) {
	return branch.session.AppendToBranch(ctx, branch.name, Entry{Type: EntryTypeMessage, Message: message})
}

func (branch *storageBackedBranch) AppendCustomEntry(ctx context.Context, customType string, data *JsonValue) (string, error) {
	return branch.session.AppendToBranch(ctx, branch.name, Entry{Type: EntryTypeCustom, CustomType: customType, Data: data})
}

func storageScan(query BranchScan, start string) StorageBranchScan {
	order := query.Order
	if order == "" {
		order = OrderNewestFirst
	}
	return StorageBranchScan{
		Start: start, StopAtType: query.StopAtType, StopAtID: query.StopAtID, Type: query.Type,
		CustomType: query.CustomType, Order: order, Limit: query.Limit, Cursor: query.Cursor,
	}
}

func singleLimit(limit *int) *int {
	if limit == nil {
		return new(1)
	}
	return new(min(*limit, 1))
}

func withSingleLimitBranch(query *BranchScan) *BranchScan {
	scan := BranchScan{}
	if query != nil {
		scan = *query
	}
	scan.Limit = singleLimit(scan.Limit)
	return &scan
}

func firstEntry(entries []Entry, err error) (*Entry, error) {
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return &entries[0], nil
}

// StorageBackedSessionOptions configure a StorageBackedSession.
type StorageBackedSessionOptions struct {
	MutationLine *MutationLine
	IdGenerator  IdGenerator
	OnClose      func()
}

const (
	sessionOpen = iota
	sessionClosing
	sessionClosed
)

// StorageBackedSession is the typed Session boundary shared by concrete
// session repositories.
type StorageBackedSession struct {
	metadata     SessionMetadata
	idGenerator  IdGenerator
	storage      Storage
	mutationLine *MutationLine
	onClose      func()

	mu        sync.Mutex
	branches  map[string]*storageBackedBranch
	state     int
	closeOnce sync.Once
	closeErr  error
}

// NewStorageBackedSession binds a session to its storage.
func NewStorageBackedSession(metadata SessionMetadata, storage Storage, options *StorageBackedSessionOptions) *StorageBackedSession {
	if options == nil {
		options = &StorageBackedSessionOptions{}
	}
	session := &StorageBackedSession{
		metadata: metadata, idGenerator: options.IdGenerator, storage: storage,
		mutationLine: options.MutationLine, onClose: options.OnClose, branches: map[string]*storageBackedBranch{},
	}
	if session.idGenerator == nil {
		session.idGenerator = UUIDv7Generator
	}
	if session.mutationLine == nil {
		session.mutationLine = &MutationLine{}
	}
	return session
}

// Metadata returns the session metadata.
func (session *StorageBackedSession) Metadata() SessionMetadata { return session.metadata }

// IdGenerator returns the session id generator.
func (session *StorageBackedSession) IdGenerator() IdGenerator { return session.idGenerator }

func (session *StorageBackedSession) assertOpen() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != sessionOpen {
		return ErrSessionClosed
	}
	return nil
}

// BeginMutation waits for the Session barrier and returns its capability.
func (session *StorageBackedSession) BeginMutation(context.Context) (SessionMutation, error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	granted := make(chan SessionMutation, 1)
	job := session.mutationLine.Enqueue(func() (any, error) {
		finished := make(chan struct{})
		granted <- &storageBackedSessionMutation{storage: session.storage, active: true, release: func() { close(finished) }}
		<-finished
		return nil, nil
	})
	select {
	case mutation := <-granted:
		return mutation, nil
	case <-job.Done():
		select {
		case mutation := <-granted:
			return mutation, nil
		default:
		}
		_, err := job.Wait()
		return nil, err
	}
}

// Mutate runs mutation under the barrier and always ends it.
func (session *StorageBackedSession) Mutate(ctx context.Context, mutation SessionMutationCallback) (any, error) {
	return mutateWith(ctx, session.BeginMutation, mutation)
}

func mutateWith(ctx context.Context, begin func(context.Context) (SessionMutation, error), mutation SessionMutationCallback) (result any, err error) {
	mutator, err := begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if endErr := mutator.End(ctx); err == nil {
			err = endErr
		}
	}()
	return mutation(ctx, mutator)
}

// GetEntries returns the requested existing entries by id.
func (session *StorageBackedSession) GetEntries(ctx context.Context, ids []string) (map[string]Entry, error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	return session.storage.GetEntries(ctx, ids)
}

// GetEntry returns one entry, or nil.
func (session *StorageBackedSession) GetEntry(ctx context.Context, id string) (*Entry, error) {
	entries, err := session.GetEntries(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	entry, ok := entries[id]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

// GetValue reads one erased value.
func (session *StorageBackedSession) GetValue(ctx context.Context, address StoredAddressBase) (*StoredValue[any], error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	return session.storage.GetValue(ctx, address)
}

// ScanValues reads erased values under a namespace-scoped prefix.
func (session *StorageBackedSession) ScanValues(ctx context.Context, prefix StoredAddressBase) ([]StoredValue[any], error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	return session.storage.ScanValues(ctx, prefix)
}

// ReadList reads one erased list page.
func (session *StorageBackedSession) ReadList(ctx context.Context, address StoredAddressBase, options *ListReadOptions) ([]ListElement[any], error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	return session.storage.ReadList(ctx, address, options)
}

// ScanBranch scans a branch path from an explicit entry.
func (session *StorageBackedSession) ScanBranch(ctx context.Context, query StorageBranchScan) ([]Entry, error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	return session.storage.ScanBranch(ctx, query)
}

// GetStats returns the maintained totals.
func (session *StorageBackedSession) GetStats(ctx context.Context) (SessionStats, error) {
	if err := session.assertOpen(); err != nil {
		return SessionStats{}, err
	}
	return session.storage.GetStats(ctx)
}

// GetName returns the session name, or nil.
func (session *StorageBackedSession) GetName(ctx context.Context) (*string, error) {
	return storedString(GetValue(ctx, session, SessionName))
}

// GetLabel returns an entry label, or nil.
func (session *StorageBackedSession) GetLabel(ctx context.Context, targetID string) (*string, error) {
	return storedString(GetValue(ctx, session, EntryLabel(targetID)))
}

func storedString(stored *StoredValue[string], err error) (*string, error) {
	if err != nil || stored == nil {
		return nil, err
	}
	return &stored.Value, nil
}

// FindEntries queries session-wide entries.
func (session *StorageBackedSession) FindEntries(ctx context.Context, query *EntryQuery) ([]Entry, error) {
	if query == nil {
		query = &EntryQuery{}
	}
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	scan, empty := entryScanFor(*query)
	if empty {
		return []Entry{}, nil
	}
	return session.storage.ScanEntries(ctx, scan)
}

func entryScanFor(query EntryQuery) (EntryScan, bool) {
	order := query.Order
	if order == "" {
		order = OrderDesc
	}
	scan := EntryScan{Type: query.Type, CustomType: query.CustomType, Order: order, Limit: query.Limit}
	if query.Cursor == nil {
		return scan, false
	}
	if order == OrderAsc {
		if query.Cursor.Seq == maxSafeInteger {
			return scan, true
		}
		scan.FromSeq = new(query.Cursor.Seq + 1)
		return scan, false
	}
	if query.Cursor.Seq <= 1 {
		return scan, true
	}
	scan.ToSeq = new(query.Cursor.Seq - 1)
	return scan, false
}

// FindEntry returns the first matching entry, or nil.
func (session *StorageBackedSession) FindEntry(ctx context.Context, query *EntryQuery) (*Entry, error) {
	single := EntryQuery{}
	if query != nil {
		single = *query
	}
	single.Limit = singleLimit(single.Limit)
	return firstEntry(session.FindEntries(ctx, &single))
}

// Branch returns an existing Branch, or nil.
func (session *StorageBackedSession) Branch(ctx context.Context, name string) (Branch, error) {
	if err := assertValidBranchName(name); err != nil {
		return nil, err
	}
	tip, err := session.GetValue(ctx, BranchTip(name).StoredAddressBase)
	if err != nil || tip == nil {
		return nil, err
	}
	return session.branchObject(name), nil
}

// CreateBranch atomically validates the name, absence, and a non-null target,
// then writes only the tip.
func (session *StorageBackedSession) CreateBranch(ctx context.Context, name string, at *string) (Branch, error) {
	if err := session.assertOpen(); err != nil {
		return nil, err
	}
	if err := assertValidBranchName(name); err != nil {
		return nil, err
	}
	_, err := session.Mutate(ctx, func(ctx context.Context, mutator SessionMutator) (any, error) {
		existing, err := mutator.GetValue(ctx, BranchTip(name).StoredAddressBase)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, &SessionBranchExistsError{Branch: name}
		}
		if at != nil {
			entries, err := mutator.GetEntries(ctx, []string{*at})
			if err != nil {
				return nil, err
			}
			if _, ok := entries[*at]; !ok {
				return nil, &SessionUnknownTargetError{TargetID: *at}
			}
		}
		return mutator.Commit(ctx, []Write{SetValue(BranchTip(name), at)})
	})
	if err != nil {
		return nil, err
	}
	return session.branchObject(name), nil
}

func (session *StorageBackedSession) commitOne(ctx context.Context, write Write) error {
	_, err := session.Mutate(ctx, func(ctx context.Context, mutator SessionMutator) (any, error) {
		return mutator.Commit(ctx, []Write{write})
	})
	return err
}

// SetValue replaces one value in its own commit.
func (session *StorageBackedSession) SetValue(ctx context.Context, address StoredAddressBase, next any) error {
	return session.commitOne(ctx, ValueSetWrite{Namespace: address.Namespace, Key: address.Key, Value: next})
}

// DeleteValue deletes one value in its own commit.
func (session *StorageBackedSession) DeleteValue(ctx context.Context, address StoredAddressBase) error {
	return session.commitOne(ctx, ValueDeleteWrite{Namespace: address.Namespace, Key: address.Key})
}

// AppendList appends one list element in its own commit.
func (session *StorageBackedSession) AppendList(ctx context.Context, address StoredAddressBase, element any) error {
	return session.commitOne(ctx, ListAppendWrite{Namespace: address.Namespace, Key: address.Key, Value: element})
}

// DeleteList deletes a whole list in its own commit.
func (session *StorageBackedSession) DeleteList(ctx context.Context, address StoredAddressBase) error {
	return session.commitOne(ctx, ListDeleteWrite{Namespace: address.Namespace, Key: address.Key})
}

// SetName sets or, for nil, deletes the session name.
func (session *StorageBackedSession) SetName(ctx context.Context, name *string) error {
	if name == nil {
		return session.DeleteValue(ctx, SessionName.StoredAddressBase)
	}
	return session.SetValue(ctx, SessionName.StoredAddressBase, *name)
}

// SetLabel sets or, for nil, deletes an entry label.
func (session *StorageBackedSession) SetLabel(ctx context.Context, targetID string, label *string) error {
	address := EntryLabel(targetID).StoredAddressBase
	if label == nil {
		return session.DeleteValue(ctx, address)
	}
	return session.SetValue(ctx, address, *label)
}

// Close seals the mutation line, drains admitted mutations, and closes the
// storage; it is idempotent.
func (session *StorageBackedSession) Close(ctx context.Context) error {
	session.closeOnce.Do(func() {
		session.mu.Lock()
		session.state = sessionClosing
		session.mu.Unlock()
		<-session.mutationLine.Seal(ErrSessionClosed)
		session.closeErr = session.storage.Close(ctx)
		session.mu.Lock()
		session.state = sessionClosed
		session.mu.Unlock()
		if session.onClose != nil {
			session.onClose()
		}
	})
	return session.closeErr
}

// GetBranchTip returns a Branch's tip; an absent Branch is an invariant error.
func (session *StorageBackedSession) GetBranchTip(ctx context.Context, name string) (*string, error) {
	stored, err := GetValue(ctx, session, BranchTip(name))
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, &SessionInvariantError{Message: "Unknown branch: " + name}
	}
	return stored.Value, nil
}

// AppendToBranch inserts a message or custom entry at a Branch tip and moves
// the tip in one mutation. Only Type, Message, CustomType, and Data are read.
func (session *StorageBackedSession) AppendToBranch(ctx context.Context, name string, entry Entry) (string, error) {
	if err := session.assertOpen(); err != nil {
		return "", err
	}
	if entry.Type == EntryTypeMessage && IsPendingAssistant(entry.Message) {
		return "", errPendingAssistantMsg
	}
	id := session.idGenerator.Next(nil)
	_, err := session.Mutate(ctx, func(ctx context.Context, mutator SessionMutator) (any, error) {
		tip, err := GetValue(ctx, mutator, BranchTip(name))
		if err != nil {
			return nil, err
		}
		if tip == nil {
			return nil, &SessionInvariantError{Message: "Unknown branch: " + name}
		}
		placed := Entry{ID: id, ParentID: tip.Value, Type: entry.Type}
		if entry.Type == EntryTypeMessage {
			placed.Message = entry.Message
		} else {
			placed.CustomType = entry.CustomType
			placed.Data = entry.Data
		}
		return mutator.Commit(ctx, []Write{InsertEntry(placed), SetValue(BranchTip(name), &id)})
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (session *StorageBackedSession) branchObject(name string) Branch {
	session.mu.Lock()
	defer session.mu.Unlock()
	branch, ok := session.branches[name]
	if !ok {
		branch = &storageBackedBranch{name: name, session: session}
		session.branches[name] = branch
	}
	return branch
}

func assertValidBranchName(name string) error {
	if name == "" {
		return &SessionInvalidBranchError{Branch: name, Reason: "branch name must not be empty"}
	}
	if strings.ContainsRune(name, 0) {
		return &SessionInvalidBranchError{Branch: name, Reason: "branch name must not contain \\u0000"}
	}
	return nil
}
