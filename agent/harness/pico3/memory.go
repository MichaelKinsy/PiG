package pico3

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
)

// Storage persists one Session's four tables and three document families.
// A batch commits atomically: all writes or none.
type Storage interface {
	// Commit persists one batch and returns its commit sequence.
	Commit(ctx context.Context, writes []Write) (Seq, error)
	MintId() Id
	Conversation(ctx context.Context, id Id) (*Conversation, error)
	Conversations(ctx context.Context) ([]Conversation, error)
	Entries(ctx context.Context, ids []Id) (map[Id]Entry, error)
	// ScanEntries is newest-first and fork-aware: this conversation's entries,
	// then the parent's up to the fork point, and so on.
	ScanEntries(ctx context.Context, scan EntryScan) ([]Entry, error)
	Task(ctx context.Context, id Id) (*Task, error)
	ScanTasks(ctx context.Context, scan TaskScan) ([]Task, error)
	Input(ctx context.Context, id Id) (*Input, error)
	InputByRequest(ctx context.Context, conversationId Id, requestId string) (*Input, error)
	// Doc returns a document, or nil when it does not exist.
	Doc(ctx context.Context, ref DocRef) (JsonObject, error)
	// DocAsOf returns the rewindable document as of the atomic commit
	// containing entry at, walking the fork chain.
	DocAsOf(ctx context.Context, conversationId, at Id) (JsonObject, error)
	// Truncate rewrites a document log from its last base.
	Truncate(ctx context.Context, ref DocRef) error
	Close(ctx context.Context) error
}

type rewindableRecord struct {
	seq Seq
	ops []Op
}

// MemoryStorage is the in-memory reference Storage and the read path of
// JsonlStorage. A batch is validated and staged in full before any table
// changes: a failed batch changes no table, document, id high-water, or
// sequence. Committed ids are never reused; ids minted but never committed may
// be reused after reopen.
type MemoryStorage struct {
	mu                    sync.Mutex
	conversationsById     map[Id]Conversation
	conversationOrder     []Id
	entriesById           map[Id]Entry
	entriesByConversation map[Id][]Id
	tasksById             map[Id]Task
	taskOrder             []Id
	inputsById            map[Id]Input
	inputsByRequest       map[string]Id
	rewindableLog         map[Id][]rewindableRecord
	stickyLog             map[Id][][]Op
	sessionDoc            JsonObject
	entrySeq              map[Id]Seq
	nextIdValue           Id
	seq                   Seq
	closed                bool
}

// NewMemoryStorage creates an empty storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		conversationsById:     map[Id]Conversation{},
		entriesById:           map[Id]Entry{},
		entriesByConversation: map[Id][]Id{},
		tasksById:             map[Id]Task{},
		inputsById:            map[Id]Input{},
		inputsByRequest:       map[string]Id{},
		rewindableLog:         map[Id][]rewindableRecord{},
		stickyLog:             map[Id][][]Op{},
		entrySeq:              map[Id]Seq{},
		nextIdValue:           1,
	}
}

// MintId returns a fresh id.
func (storage *MemoryStorage) MintId() Id {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	id := storage.nextIdValue
	storage.nextIdValue++
	return id
}

func (storage *MemoryStorage) setNextIdLocked(next Id) {
	storage.nextIdValue = max(storage.nextIdValue, next)
}

// Commit validates and applies one batch.
func (storage *MemoryStorage) Commit(_ context.Context, writes []Write) (Seq, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return 0, errors.New("storage closed")
	}
	seq := storage.seq + 1
	apply, err := storage.stageLocked(writes, seq)
	if err != nil {
		return 0, err
	}
	if err := apply(); err != nil {
		return 0, err
	}
	storage.seq = seq
	return seq, nil
}

type stager struct {
	storage    *MemoryStorage
	writes     []Write
	seq        Seq
	ops        []func()
	created    map[Id]bool
	maxId      Id
	applyError error
}

func (stage *stager) claim(id Id, what string) error {
	if id <= 0 || float64(id) > 1<<53-1 {
		return fmt.Errorf("%s: invalid id %d", what, id)
	}
	if stage.created[id] {
		return fmt.Errorf("%s: id %d created twice in one batch", what, id)
	}
	stage.created[id] = true
	stage.maxId = max(stage.maxId, id)
	return nil
}

// stageLocked validates the whole batch against the current tables and
// returns the function that applies it.
func (storage *MemoryStorage) stageLocked(writes []Write, seq Seq) (func() error, error) {
	stage := &stager{storage: storage, writes: writes, seq: seq, created: map[Id]bool{}}
	for _, write := range writes {
		if err := stage.stage(write); err != nil {
			return nil, err
		}
	}
	return func() error {
		for _, op := range stage.ops {
			op()
			if stage.applyError != nil {
				return stage.applyError
			}
		}
		storage.setNextIdLocked(stage.maxId + 1)
		return nil
	}, nil
}

func (stage *stager) stage(write Write) error {
	switch write.Type {
	case WriteConversation:
		return stage.conversation(write)
	case WriteEntry:
		return stage.entry(write)
	case WriteTask:
		return stage.task(write)
	case WriteTaskPatch:
		return stage.patch(write)
	case WriteInput:
		return stage.input(write)
	case WriteDoc:
		return stage.doc(write)
	default:
		return fmt.Errorf("unknown write type %q", write.Type)
	}
}

func (stage *stager) conversation(write Write) error {
	storage := stage.storage
	conversation := cloneConversation(*write.Conversation)
	if _, exists := storage.conversationsById[conversation.Id]; exists {
		return fmt.Errorf("conversation %d exists", conversation.Id)
	}
	if err := stage.claim(conversation.Id, "conversation"); err != nil {
		return err
	}
	stage.ops = append(stage.ops, func() {
		storage.conversationsById[conversation.Id] = conversation
		storage.conversationOrder = append(storage.conversationOrder, conversation.Id)
		if _, ok := storage.entriesByConversation[conversation.Id]; !ok {
			storage.entriesByConversation[conversation.Id] = nil
		}
	})
	return nil
}

func (stage *stager) entry(write Write) error {
	storage := stage.storage
	entry := cloneEntry(*write.Entry)
	if _, exists := storage.entriesById[entry.Id]; exists {
		return fmt.Errorf("entry %d exists", entry.Id)
	}
	if err := stage.claim(entry.Id, "entry"); err != nil {
		return err
	}
	seq := stage.seq
	stage.ops = append(stage.ops, func() {
		storage.entriesById[entry.Id] = entry
		storage.entriesByConversation[entry.ConversationId] = append(storage.entriesByConversation[entry.ConversationId], entry.Id)
		storage.entrySeq[entry.Id] = seq
	})
	return nil
}

func (stage *stager) task(write Write) error {
	storage := stage.storage
	task := write.Task.clone()
	if _, exists := storage.tasksById[task.Id]; exists {
		return fmt.Errorf("task %d exists", task.Id)
	}
	if err := stage.claim(task.Id, "task"); err != nil {
		return err
	}
	stage.ops = append(stage.ops, func() {
		storage.tasksById[task.Id] = task
		storage.taskOrder = append(storage.taskOrder, task.Id)
	})
	return nil
}

func (stage *stager) patch(write Write) error {
	storage := stage.storage
	patch := *write.Patch
	_, exists := storage.tasksById[patch.Id]
	if !exists && !stage.batchCreatesTask(patch.Id) {
		return fmt.Errorf("patch for unknown task %d", patch.Id)
	}
	stage.ops = append(stage.ops, func() {
		storage.tasksById[patch.Id] = patch.apply(storage.tasksById[patch.Id])
	})
	return nil
}

func (stage *stager) batchCreatesTask(id Id) bool {
	for _, write := range stage.writes {
		if write.Type == WriteTask && write.Task.Id == id {
			return true
		}
	}
	return false
}

func (stage *stager) input(write Write) error {
	storage := stage.storage
	input := cloneInput(*write.Input)
	if _, exists := storage.inputsById[input.Id]; !exists {
		if err := stage.claim(input.Id, "input"); err != nil {
			return err
		}
	}
	stage.ops = append(stage.ops, func() {
		storage.inputsById[input.Id] = input
		if input.RequestId != "" {
			storage.inputsByRequest[requestKey(input.ConversationId, input.RequestId)] = input.Id
		}
	})
	return nil
}

func (stage *stager) doc(write Write) error {
	storage := stage.storage
	ref := write.Ref
	ops := cloneOps(write.Ops)
	seq := stage.seq
	switch ref.Doc {
	case DocSession:
		stage.ops = append(stage.ops, func() {
			current := storage.sessionDoc
			if current == nil {
				current = JsonObject{"plugins": JsonObject{}}
			}
			next, err := ApplyImmutable(current, ops)
			if err != nil {
				stage.applyError = err
				return
			}
			storage.sessionDoc, _ = next.(map[string]any)
		})
	case DocRewindable:
		stage.ops = append(stage.ops, func() {
			storage.rewindableLog[ref.ConversationId] = append(storage.rewindableLog[ref.ConversationId], rewindableRecord{seq: seq, ops: ops})
		})
	default:
		stage.ops = append(stage.ops, func() {
			storage.stickyLog[ref.ConversationId] = append(storage.stickyLog[ref.ConversationId], ops)
		})
	}
	return nil
}

func requestKey(conversationId Id, requestId string) string {
	return fmt.Sprintf("%d:%s", conversationId, requestId)
}

// Conversation reads one conversation.
func (storage *MemoryStorage) Conversation(_ context.Context, id Id) (*Conversation, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	conversation, ok := storage.conversationsById[id]
	if !ok {
		return nil, nil
	}
	clone := cloneConversation(conversation)
	return &clone, nil
}

// Conversations lists every conversation in creation order.
func (storage *MemoryStorage) Conversations(_ context.Context) ([]Conversation, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	out := make([]Conversation, 0, len(storage.conversationOrder))
	for _, id := range storage.conversationOrder {
		out = append(out, cloneConversation(storage.conversationsById[id]))
	}
	return out, nil
}

// Entries reads entries by id; missing ids are absent from the map.
func (storage *MemoryStorage) Entries(_ context.Context, ids []Id) (map[Id]Entry, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	out := make(map[Id]Entry, len(ids))
	for _, id := range ids {
		if entry, ok := storage.entriesById[id]; ok {
			out[id] = cloneEntry(entry)
		}
	}
	return out, nil
}

// ScanEntries scans newest-first across the fork chain.
func (storage *MemoryStorage) ScanEntries(_ context.Context, scan EntryScan) ([]Entry, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	var out []Entry
	conversationId := &scan.ConversationId
	capId := math.Inf(1)
	if scan.Before != nil {
		capId = float64(*scan.Before)
	}
	for conversationId != nil && len(out) < scan.Limit {
		own := storage.entriesByConversation[*conversationId]
		for index := len(own) - 1; index >= 0 && len(out) < scan.Limit; index-- {
			entry := storage.entriesById[own[index]]
			if float64(entry.Id) >= capId || !scanMatches(scan, entry) {
				continue
			}
			out = append(out, cloneEntry(entry))
		}
		conversation, ok := storage.conversationsById[*conversationId]
		if !ok || conversation.Parent == nil {
			break
		}
		capId = math.Min(capId, float64(conversation.Parent.At+1))
		parent := conversation.Parent.ConversationId
		conversationId = &parent
	}
	return out, nil
}

func scanMatches(scan EntryScan, entry Entry) bool {
	if scan.Kind != "" && entry.Kind != scan.Kind {
		return false
	}
	return !scan.WithHead || entry.Head != nil
}

// Task reads one task.
func (storage *MemoryStorage) Task(_ context.Context, id Id) (*Task, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	task, ok := storage.tasksById[id]
	if !ok {
		return nil, nil
	}
	clone := task.clone()
	return &clone, nil
}

// ScanTasks lists matching tasks in creation order.
func (storage *MemoryStorage) ScanTasks(_ context.Context, scan TaskScan) ([]Task, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	var out []Task
	for _, id := range storage.taskOrder {
		task := storage.tasksById[id]
		if taskScanMatches(scan, task) {
			out = append(out, task.clone())
		}
	}
	return out, nil
}

func taskScanMatches(scan TaskScan, task Task) bool {
	if scan.ConversationId != nil && task.ConversationId != *scan.ConversationId {
		return false
	}
	if scan.Status != nil && !slices.Contains(scan.Status, task.Status) {
		return false
	}
	return scan.Kind == "" || task.Kind == scan.Kind
}

// Input reads one input.
func (storage *MemoryStorage) Input(_ context.Context, id Id) (*Input, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	input, ok := storage.inputsById[id]
	if !ok {
		return nil, nil
	}
	clone := cloneInput(input)
	return &clone, nil
}

// InputByRequest reads the input admitted under a request id.
func (storage *MemoryStorage) InputByRequest(_ context.Context, conversationId Id, requestId string) (*Input, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	id, ok := storage.inputsByRequest[requestKey(conversationId, requestId)]
	if !ok {
		return nil, nil
	}
	clone := cloneInput(storage.inputsById[id])
	return &clone, nil
}

// Doc returns a folded document copy.
func (storage *MemoryStorage) Doc(_ context.Context, ref DocRef) (JsonObject, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return storage.docLocked(ref)
}

func (storage *MemoryStorage) docLocked(ref DocRef) (JsonObject, error) {
	switch ref.Doc {
	case DocSession:
		if storage.sessionDoc == nil {
			return JsonObject{"plugins": JsonObject{}}, nil
		}
		return cloneObject(storage.sessionDoc), nil
	case DocRewindable:
		records, ok := storage.rewindableLog[ref.ConversationId]
		if !ok {
			return nil, nil
		}
		log := make([][]Op, len(records))
		for index, record := range records {
			log[index] = record.ops
		}
		return fold(log)
	default:
		log, ok := storage.stickyLog[ref.ConversationId]
		if !ok {
			return nil, nil
		}
		return fold(log)
	}
}

// DocAsOf returns the rewindable document after the commit containing at.
func (storage *MemoryStorage) DocAsOf(_ context.Context, conversationId, at Id) (JsonObject, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	entry, ok := storage.entriesById[at]
	if !ok {
		return nil, nil
	}
	owner := entry.ConversationId
	conversation, found := storage.conversationsById[conversationId]
	for found && conversation.Id != owner {
		if conversation.Parent == nil {
			found = false
			break
		}
		conversation, found = storage.conversationsById[conversation.Parent.ConversationId]
	}
	if !found {
		return nil, nil
	}
	seqAt := storage.entrySeq[at]
	var log [][]Op
	for _, record := range storage.rewindableLog[owner] {
		if record.seq <= seqAt {
			log = append(log, record.ops)
		}
	}
	if len(log) == 0 {
		return nil, nil
	}
	return fold(log)
}

// fold replays a document log from its last base.
func fold(log [][]Op) (JsonObject, error) {
	start := 0
	for index, l := range slices.Backward(log) {
		if IsBase(l) {
			start = index
			break
		}
	}
	var state JsonValue = JsonObject{}
	for _, ops := range log[start:] {
		next, err := ApplyImmutable(state, ops)
		if err != nil {
			return nil, err
		}
		state = next
	}
	object, _ := cloneJSON(state).(map[string]any)
	return object, nil
}

// Truncate drops sticky log batches before the last base.
func (storage *MemoryStorage) Truncate(_ context.Context, ref DocRef) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.truncateLocked(ref)
	return nil
}

func (storage *MemoryStorage) truncateLocked(ref DocRef) {
	if ref.Doc != DocSticky {
		return
	}
	log, ok := storage.stickyLog[ref.ConversationId]
	if !ok {
		return
	}
	for index, l := range slices.Backward(log) {
		if IsBase(l) {
			storage.stickyLog[ref.ConversationId] = log[index:]
			return
		}
	}
}

// Close seals the storage.
func (storage *MemoryStorage) Close(_ context.Context) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.closed = true
	return nil
}

func cloneOps(ops []Op) []Op {
	out := make([]Op, len(ops))
	for index, op := range ops {
		out[index] = Op(cloneJSON([]any(op)).([]any))
	}
	return out
}

func cloneIDPointer(id *Id) *Id {
	if id == nil {
		return nil
	}
	value := *id
	return &value
}

func cloneConversation(conversation Conversation) Conversation {
	if conversation.Parent != nil {
		parent := *conversation.Parent
		conversation.Parent = &parent
	}
	conversation.Owner = cloneIDPointer(conversation.Owner)
	if conversation.Sections != nil {
		sections := make([]SectionSeed, len(conversation.Sections))
		for index, seed := range conversation.Sections {
			seed.Value = cloneJSON(seed.Value)
			sections[index] = seed
		}
		conversation.Sections = sections
	}
	return conversation
}

func cloneMessages(messages []JsonObject) []JsonObject {
	if messages == nil {
		return nil
	}
	out := make([]JsonObject, len(messages))
	for index, message := range messages {
		out[index] = cloneObject(message)
	}
	return out
}

func cloneEntry(entry Entry) Entry {
	entry.Model = cloneMessages(entry.Model)
	entry.Data = cloneObject(entry.Data)
	entry.Head = cloneIDPointer(entry.Head)
	entry.ByTaskId = cloneIDPointer(entry.ByTaskId)
	if entry.Edits != nil {
		edits := make([]ContextEdit, len(entry.Edits))
		for index, edit := range entry.Edits {
			edit.Messages = cloneMessages(edit.Messages)
			edits[index] = edit
		}
		entry.Edits = edits
	}
	return entry
}

func cloneInput(input Input) Input {
	input.Entry = cloneIDPointer(input.Entry)
	input.Answer = cloneIDPointer(input.Answer)
	return input
}
