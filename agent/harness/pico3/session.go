package pico3

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ViewEvent is one view event: a JSON object whose "type" names the event.
type ViewEvent = JsonObject

// Envelope is one commit's view change for one conversation.
type Envelope struct {
	Revision int
	Ops      []Op
	Events   []ViewEvent
}

// StickyBaseBudget is the op-byte budget after which a busy conversation's
// sticky log is rebased and truncated.
const StickyBaseBudget = 256 * 1024

type lineKeyType struct{}

var lineKey lineKeyType

// owners maps a Storage to its owning Session.
var owners sync.Map

// kindRegistry is the harness-wide kind table.
type kindRegistry struct {
	mu    sync.RWMutex
	kinds map[string]*Kind
	order []string
}

func newKindRegistry() *kindRegistry { return &kindRegistry{kinds: map[string]*Kind{}} }

func (registry *kindRegistry) get(name string) *Kind {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.kinds[name]
}

func (registry *kindRegistry) set(kind *Kind) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.kinds[kind.Name]; !exists {
		registry.order = append(registry.order, kind.Name)
	}
	registry.kinds[kind.Name] = kind
}

func (registry *kindRegistry) remove(kind *Kind) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.kinds[kind.Name] != kind {
		return false
	}
	delete(registry.kinds, kind.Name)
	registry.order = slices.DeleteFunc(registry.order, func(name string) bool { return name == kind.Name })
	return true
}

func (registry *kindRegistry) list() []*Kind {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]*Kind, 0, len(registry.order))
	for _, name := range registry.order {
		out = append(out, registry.kinds[name])
	}
	return out
}

// namespaceRegistry is the harness-wide namespace table.
type namespaceRegistry struct {
	mu            sync.RWMutex
	registrations map[string]*namespaceRegistration
	order         []string
}

func newNamespaceRegistry() *namespaceRegistry {
	return &namespaceRegistry{registrations: map[string]*namespaceRegistration{}}
}

func (registry *namespaceRegistry) get(id string) *namespaceRegistration {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.registrations[id]
}

func (registry *namespaceRegistry) list() []*namespaceRegistration {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]*namespaceRegistration, 0, len(registry.order))
	for _, id := range registry.order {
		out = append(out, registry.registrations[id])
	}
	return out
}

// CommitResult is one line commit's value, sequence, and changes. Seq is nil
// when nothing was persisted.
type CommitResult struct {
	Value   any
	Seq     *Seq
	Changes *CommitChanges
}

// TransactionControl is the kernel's handle for terminal task writes.
type TransactionControl struct {
	tx *Tx
}

// SetTask writes a task's terminal or mutable fields.
func (control TransactionControl) SetTask(task Task) error { return control.tx.setTask(task) }

type commitOptions struct {
	docs    []DocRef
	closing bool
}

// Session is the single-writer line over one Storage.
type Session struct {
	storage    Storage
	kinds      *kindRegistry
	namespaces *namespaceRegistry
	defaults   *Defaults
	docs       *docs
	now        func() float64
	line       fifo

	stateMu             sync.Mutex
	liveTasks           map[Id]Task
	conversationRecords map[Id]Conversation
	conversationOrder   []Id
	ownerTaskCache      map[Id]Task
	closed              bool
	fault               *Faulted

	listenersMu   sync.Mutex
	lineListeners []func(CommitResult)
	listeners     []func(CommitResult)
	onReport      func(error)
}

func newSession(storage Storage, kinds *kindRegistry, namespaces *namespaceRegistry, now func() float64) (*Session, error) {
	session := &Session{
		storage: storage, kinds: kinds, namespaces: namespaces, now: now,
		liveTasks: map[Id]Task{}, conversationRecords: map[Id]Conversation{}, ownerTaskCache: map[Id]Task{},
		onReport: func(error) {},
	}
	session.defaults = newDefaults()
	for _, kind := range kinds.list() {
		if err := session.defaults.register(kind); err != nil {
			return nil, err
		}
	}
	if _, loaded := owners.LoadOrStore(storage, session); loaded {
		return nil, errors.New("this Storage already has an owning Session")
	}
	session.docs = newDocs(storage, session.defaults)
	return session, nil
}

func (session *Session) liveTask(id Id) (Task, bool) {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	task, ok := session.liveTasks[id]
	return task, ok
}

// liveTaskList returns live tasks in id order.
func (session *Session) liveTaskList() []Task {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	out := make([]Task, 0, len(session.liveTasks))
	for _, task := range session.liveTasks {
		out = append(out, task)
	}
	slices.SortFunc(out, func(left, right Task) int { return int(left.Id - right.Id) })
	return out
}

func (session *Session) conversationRecord(id Id) (Conversation, bool) {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	conversation, ok := session.conversationRecords[id]
	return conversation, ok
}

func (session *Session) conversationList() []Conversation {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	out := make([]Conversation, 0, len(session.conversationOrder))
	for _, id := range session.conversationOrder {
		out = append(out, session.conversationRecords[id])
	}
	return out
}

func (session *Session) setConversationLocked(conversation Conversation) {
	if _, exists := session.conversationRecords[conversation.Id]; !exists {
		session.conversationOrder = append(session.conversationOrder, conversation.Id)
	}
	session.conversationRecords[conversation.Id] = conversation
}

func (session *Session) ownerTask(id Id) (Task, bool) {
	if task, ok := session.liveTasks[id]; ok {
		return task, true
	}
	task, ok := session.ownerTaskCache[id]
	return task, ok
}

// subtree returns the conversations in the ownership subtree rooted at root.
func (session *Session) subtree(root Id) map[Id]bool {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	out := map[Id]bool{root: true}
	for grew := true; grew; {
		grew = false
		for _, conversation := range session.conversationRecords {
			if out[conversation.Id] || conversation.Owner == nil {
				continue
			}
			if owner, ok := session.ownerTask(*conversation.Owner); ok && out[owner.ConversationId] {
				out[conversation.Id] = true
				grew = true
			}
		}
	}
	return out
}

// ancestors returns the conversations from the root down to id, exclusive,
// via ownership.
func (session *Session) ancestors(id Id) []Id {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	var chain []Id
	conversation, ok := session.conversationRecords[id]
	for ok && conversation.Owner != nil {
		owner, found := session.ownerTask(*conversation.Owner)
		if !found {
			break
		}
		chain = append([]Id{owner.ConversationId}, chain...)
		conversation, ok = session.conversationRecords[owner.ConversationId]
	}
	return chain
}

func (session *Session) report(err error) {
	defer func() { _ = recover() }() // upstream: agent/src/harness/pico3/session.ts:onReport
	session.onReport(err)
}

func (session *Session) addLineListener(listener func(CommitResult)) {
	session.listenersMu.Lock()
	defer session.listenersMu.Unlock()
	session.lineListeners = append(session.lineListeners, listener)
}

func (session *Session) addListener(listener func(CommitResult)) {
	session.listenersMu.Lock()
	defer session.listenersMu.Unlock()
	session.listeners = append(session.listeners, listener)
}

func (session *Session) callListeners(listeners []func(CommitResult), result CommitResult) {
	for _, listener := range listeners {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					session.report(fmt.Errorf("%v", recovered))
				}
			}()
			listener(result)
		}()
	}
}

func (session *Session) listenerSnapshot(line bool) []func(CommitResult) {
	session.listenersMu.Lock()
	defer session.listenersMu.Unlock()
	if line {
		return slices.Clone(session.lineListeners)
	}
	return slices.Clone(session.listeners)
}

// commit runs fn in one transaction on the line.
func (session *Session) commit(ctx context.Context, authority invoker, fn func(ctx context.Context, tx *Tx, control TransactionControl) (any, error), options commitOptions) (CommitResult, error) {
	if ctx.Err() != nil {
		return CommitResult{}, context.Cause(ctx)
	}
	value, err := session.enter(ctx, func(lineCtx context.Context) (any, error) {
		return session.commitOnLine(lineCtx, authority, fn, options)
	})
	if err != nil {
		return CommitResult{}, err
	}
	result, _ := value.(CommitResult)
	if result.Seq != nil {
		session.callListeners(session.listenerSnapshot(false), result)
	}
	return result, nil
}

func (session *Session) checkAuthority(authority invoker) error {
	if authority.kind != invokerTask {
		return nil
	}
	if !authority.token.Alive() {
		return forbidden("commit from a finished invocation")
	}
	live, ok := session.liveTask(authority.id)
	if !ok {
		return forbidden("commit from a task that is not live")
	}
	if authority.mode == "run" && live.Abort {
		return forbidden("commit from a marked run invocation")
	}
	return nil
}

func (session *Session) commitOnLine(lineCtx context.Context, authority invoker, fn func(ctx context.Context, tx *Tx, control TransactionControl) (any, error), options commitOptions) (any, error) {
	if err := session.assertUsable(); err != nil {
		return nil, err
	}
	if lineCtx.Err() != nil {
		return nil, context.Cause(lineCtx)
	}
	if err := session.checkAuthority(authority); err != nil {
		return nil, err
	}
	tx := newTx(session, authority, lineCtx)
	tx.closing = options.closing
	refs := append([]DocRef{SessionDoc()}, options.docs...)
	if authority.kind == invokerTask {
		refs = append(refs, RewindableDoc(*authority.conversationId), StickyDoc(*authority.conversationId))
	}
	persisted := false
	defer tx.revoke()
	result, err := session.runTransaction(lineCtx, tx, refs, fn, &persisted)
	if err != nil && !persisted {
		tx.evictTouched()
	}
	return result, err
}

func (session *Session) runTransaction(lineCtx context.Context, tx *Tx, refs []DocRef, fn func(ctx context.Context, tx *Tx, control TransactionControl) (any, error), persisted *bool) (any, error) {
	if err := tx.preload(refs); err != nil {
		return nil, err
	}
	value, err := callTransaction(lineCtx, tx, fn)
	tx.closeSurface()
	if err != nil {
		return nil, err
	}
	var touchedTasks []DocRef
	seen := map[Id]bool{}
	for _, task := range tx.changes.Tasks {
		if !seen[task.ConversationId] {
			seen[task.ConversationId] = true
			touchedTasks = append(touchedTasks, StickyDoc(task.ConversationId))
		}
	}
	if err := tx.preload(touchedTasks); err != nil {
		return nil, err
	}
	writes, err := tx.finish()
	if err != nil {
		return nil, err
	}
	result := CommitResult{Value: value, Changes: &tx.changes}
	if len(writes) > 0 || len(tx.changes.Events) > 0 {
		*persisted = true
		seq, err := session.persist(lineCtx, writes)
		if err != nil {
			return nil, err
		}
		session.applyChanges(tx.changes)
		result.Seq = &seq
		session.callListeners(session.listenerSnapshot(true), result)
	}
	return result, nil
}

func callTransaction(lineCtx context.Context, tx *Tx, fn func(ctx context.Context, tx *Tx, control TransactionControl) (any, error)) (value any, err error) {
	defer recoverInto(&err)
	return fn(lineCtx, tx, TransactionControl{tx: tx})
}

// persist commits once with a non-cancellable context; a failure faults the
// session and closes storage.
func (session *Session) persist(lineCtx context.Context, writes []Write) (Seq, error) {
	seq, err := session.storage.Commit(context.WithoutCancel(lineCtx), writes)
	if err == nil {
		return seq, nil
	}
	fault := &Faulted{Cause: err}
	session.stateMu.Lock()
	session.fault = fault
	session.stateMu.Unlock()
	if closeErr := session.storage.Close(context.WithoutCancel(lineCtx)); closeErr == nil {
		owners.Delete(session.storage)
	}
	return 0, fault
}

// read runs a read-only line operation.
func (session *Session) read(ctx context.Context, fn func(ctx context.Context, storage Storage) (any, error)) (any, error) {
	return session.enter(ctx, func(lineCtx context.Context) (any, error) {
		if err := session.assertUsable(); err != nil {
			return nil, err
		}
		return fn(lineCtx, session.storage)
	})
}

// onLine runs a generic line operation.
func (session *Session) onLine(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	return session.enter(ctx, func(lineCtx context.Context) (any, error) {
		if err := session.assertUsable(); err != nil {
			return nil, err
		}
		return fn(lineCtx)
	})
}

// retire drops a terminal task's slot and rebases and truncates the sticky
// log when the conversation is idle or over budget.
func (session *Session) retire(ctx context.Context, task Task) error {
	ref := StickyDoc(task.ConversationId)
	idle := true
	for _, live := range session.liveTaskList() {
		if live.ConversationId == task.ConversationId {
			idle = false
			break
		}
	}
	over := session.docs.opsSinceBase(ref) > StickyBaseBudget
	_, err := session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		sticky, err := tx.Sticky(task.ConversationId)
		if err != nil {
			return nil, err
		}
		if tasks, ok := sticky.Get("tasks").(*Node); ok {
			tasks.Delete(fmt.Sprint(task.Id))
		}
		if idle || over {
			session.docs.requestBase(ref)
		}
		return nil, nil
	}, commitOptions{docs: []DocRef{ref}})
	if err != nil || (!idle && !over) {
		return err
	}
	_, err = session.enter(ctx, func(lineCtx context.Context) (any, error) {
		return nil, session.storage.Truncate(context.WithoutCancel(lineCtx), ref)
	})
	return err
}

// fork creates a host fork of parentId at an entry or at the start.
func (session *Session) fork(ctx context.Context, parentId Id, at *Id, spec ConversationSpec) (Id, error) {
	var inherited JsonObject
	if at != nil {
		value, err := session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
			before := *at + 1
			found, err := storage.ScanEntries(lineCtx, EntryScan{ConversationId: parentId, Before: &before, Limit: 1})
			if err != nil {
				return nil, err
			}
			if len(found) == 0 || found[0].Id != *at {
				return nil, fmt.Errorf("entry %d is not visible from conversation %d", *at, parentId)
			}
			return storage.DocAsOf(lineCtx, parentId, *at)
		})
		if err != nil {
			return 0, err
		}
		inherited, _ = value.(JsonObject)
	}
	overrides, err := session.defaults.validateSeed(DocRewindable, withoutPlugins(spec.Rewindable))
	if err != nil {
		return 0, err
	}
	parent := &ConversationParentSpec{ConversationId: parentId, AtStart: at == nil}
	if at != nil {
		parent.At = *at
	}
	result, err := session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return tx.CreateForkConversation(ConversationSpec{Parent: parent, Rewindable: mergeInherited(inherited, overrides), Sticky: spec.Sticky, Sections: spec.Sections})
	}, commitOptions{})
	if err != nil {
		return 0, err
	}
	id, _ := result.Value.(Id)
	return id, nil
}

// close closes storage on the line; it is idempotent.
func (session *Session) close(ctx context.Context) error {
	_, err := session.enter(ctx, func(lineCtx context.Context) (any, error) {
		session.stateMu.Lock()
		closed := session.closed
		session.stateMu.Unlock()
		if closed {
			return nil, nil
		}
		if err := session.storage.Close(context.WithoutCancel(lineCtx)); err != nil {
			return nil, err
		}
		session.stateMu.Lock()
		session.closed = true
		session.stateMu.Unlock()
		owners.Delete(session.storage)
		return nil, nil
	})
	return err
}

func (session *Session) loadedDocument(ref DocRef) JsonObject { return session.docs.loaded(ref) }

func (session *Session) applyChanges(changes CommitChanges) {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	for _, task := range changes.Tasks {
		if task.Status == TaskTerminal {
			delete(session.liveTasks, task.Id)
			if len(task.Owns) > 0 {
				session.ownerTaskCache[task.Id] = task
			}
			continue
		}
		session.liveTasks[task.Id] = task
	}
	for _, conversation := range changes.Conversations {
		session.setConversationLocked(conversation)
	}
}

// enter runs op on the FIFO line; entering from the line rejects.
func (session *Session) enter(ctx context.Context, op func(lineCtx context.Context) (any, error)) (any, error) {
	if ctx.Value(lineKey) == true {
		return nil, &NestedLineOperation{}
	}
	release := session.line.acquire()
	defer release()
	return op(context.WithValue(ctx, lineKey, true))
}

func (session *Session) assertUsable() error {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	if session.fault != nil {
		return session.fault
	}
	if session.closed {
		return &Closed{}
	}
	return nil
}

// fifo is a first-in, first-out lock: holders run in acquisition order.
type fifo struct {
	mu   sync.Mutex
	tail chan struct{}
}

func (lock *fifo) acquire() func() {
	lock.mu.Lock()
	previous := lock.tail
	next := make(chan struct{})
	lock.tail = next
	lock.mu.Unlock()
	if previous != nil {
		<-previous
	}
	return func() { close(next) }
}
