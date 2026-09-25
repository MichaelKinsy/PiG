package pico3

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// ConversationParent names the fork point of a forked conversation.
type ConversationParent struct {
	ConversationId Id `json:"conversationId"`
	At             Id `json:"at"`
}

// Conversation is one conversation record.
type Conversation struct {
	Id     Id                  `json:"id"`
	Parent *ConversationParent `json:"parent,omitempty"`
	// Owner is the task that created the conversation.
	Owner *Id `json:"owner,omitempty"`
	// Sections is the private section seed applied while the conversation has
	// no local managed entry.
	Sections []SectionSeed `json:"sections,omitempty"`
}

// ContextEdit omits or replaces one target entry's model contribution.
type ContextEdit struct {
	Target   Id           `json:"target"`
	Action   string       `json:"action"`
	Messages []JsonObject `json:"messages,omitempty"`
}

// Entry is one transcript record. Model is what the model sees; a nil Model
// marks a display or bookkeeping entry, while an empty non-nil Model is a
// present empty array.
type Entry struct {
	Id             Id
	ConversationId Id
	Kind           string
	Model          []JsonObject
	Data           JsonObject
	Head           *Id
	Edits          []ContextEdit
	ByTaskId       *Id
}

type entryWire struct {
	Id             Id             `json:"id"`
	ConversationId Id             `json:"conversationId"`
	Kind           string         `json:"kind"`
	Model          *[]JsonObject  `json:"model,omitempty"`
	Data           *JsonObject    `json:"data,omitempty"`
	Head           *Id            `json:"head,omitempty"`
	Edits          *[]ContextEdit `json:"edits,omitempty"`
	ByTaskId       *Id            `json:"byTaskId,omitempty"`
}

// MarshalJSON distinguishes absent members from present empty ones.
func (entry Entry) MarshalJSON() ([]byte, error) {
	wire := entryWire{Id: entry.Id, ConversationId: entry.ConversationId, Kind: entry.Kind, Head: entry.Head, ByTaskId: entry.ByTaskId}
	if entry.Model != nil {
		wire.Model = &entry.Model
	}
	if entry.Data != nil {
		wire.Data = &entry.Data
	}
	if entry.Edits != nil {
		wire.Edits = &entry.Edits
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes an entry record.
func (entry *Entry) UnmarshalJSON(data []byte) error {
	var wire entryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*entry = Entry{Id: wire.Id, ConversationId: wire.ConversationId, Kind: wire.Kind, Head: wire.Head, ByTaskId: wire.ByTaskId}
	if wire.Model != nil {
		entry.Model = *wire.Model
		if entry.Model == nil {
			entry.Model = []JsonObject{}
		}
	}
	if wire.Data != nil {
		entry.Data = *wire.Data
	}
	if wire.Edits != nil {
		entry.Edits = *wire.Edits
	}
	return nil
}

// HeadRef is a NewEntry head: an entry id, or the new entry itself.
type HeadRef struct {
	Self bool
	Id   Id
}

// SelfHead is the "self" head reference.
func SelfHead() *HeadRef { return &HeadRef{Self: true} }

// HeadAt references an existing entry.
func HeadAt(id Id) *HeadRef { return &HeadRef{Id: id} }

// MarshalJSON emits "self" or the id.
func (head HeadRef) MarshalJSON() ([]byte, error) {
	if head.Self {
		return []byte(`"self"`), nil
	}
	return json.Marshal(head.Id)
}

// UnmarshalJSON decodes "self" or an id.
func (head *HeadRef) UnmarshalJSON(data []byte) error {
	if string(data) == `"self"` {
		*head = HeadRef{Self: true}
		return nil
	}
	var id Id
	if err := json.Unmarshal(data, &id); err != nil {
		return fmt.Errorf("head: %w", err)
	}
	*head = HeadRef{Id: id}
	return nil
}

// NewEntry is an entry before the kernel assigns its id and conversation.
type NewEntry struct {
	Kind  string
	Model []JsonObject
	Data  JsonObject
	Head  *HeadRef
	Edits []ContextEdit
}

type newEntryWire struct {
	Kind  string         `json:"kind"`
	Model *[]JsonObject  `json:"model,omitempty"`
	Data  *JsonObject    `json:"data,omitempty"`
	Head  *HeadRef       `json:"head,omitempty"`
	Edits *[]ContextEdit `json:"edits,omitempty"`
}

// MarshalJSON distinguishes absent members from present empty ones.
func (entry NewEntry) MarshalJSON() ([]byte, error) {
	wire := newEntryWire{Kind: entry.Kind, Head: entry.Head}
	if entry.Model != nil {
		wire.Model = &entry.Model
	}
	if entry.Data != nil {
		wire.Data = &entry.Data
	}
	if entry.Edits != nil {
		wire.Edits = &entry.Edits
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes a queued new entry.
func (entry *NewEntry) UnmarshalJSON(data []byte) error {
	var wire newEntryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*entry = NewEntry{Kind: wire.Kind, Head: wire.Head}
	if wire.Model != nil {
		entry.Model = *wire.Model
		if entry.Model == nil {
			entry.Model = []JsonObject{}
		}
	}
	if wire.Data != nil {
		entry.Data = *wire.Data
	}
	if wire.Edits != nil {
		entry.Edits = *wire.Edits
	}
	return nil
}

// EntryKind is a typed witness for an entry kind.
type EntryKind struct {
	Kind string
}

// Is reports whether entry has this kind.
func (kind *EntryKind) Is(entry *Entry) bool { return entry != nil && entry.Kind == kind.Kind }

// DefineEntry defines an application entry kind; names beginning with "pi."
// are reserved.
func DefineEntry(kind string) (*EntryKind, error) {
	if strings.HasPrefix(kind, "pi.") {
		return nil, fmt.Errorf(`entry kind names beginning with "pi." are reserved: %s`, kind)
	}
	return &EntryKind{Kind: kind}, nil
}

// Checkpoint is a task checkpoint: a JSON object with a string "phase".
type Checkpoint = JsonObject

// Phase returns a checkpoint's phase; "" for nil.
func Phase(checkpoint Checkpoint) string { return str(checkpoint, "phase") }

// Completion is a closure's declared result: completed with Result or failed
// with Failure.
type Completion struct {
	Status  string
	Result  JsonValue
	Failure JsonValue
}

// Completed builds a completed completion.
func Completed(result JsonValue) Completion {
	return Completion{Status: "completed", Result: result}
}

// Failed builds a failed completion.
func Failed(failure JsonValue) Completion {
	return Completion{Status: "failed", Failure: failure}
}

// Outcome statuses.
const (
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
	OutcomeAborted   = "aborted"
	OutcomeOrphaned  = "orphaned"
	OutcomeFaulted   = "faulted"
)

// Outcome is a terminal task outcome. Result carries completed and aborted
// results, Failure a declared failure, and Error a contract fault.
type Outcome struct {
	Status  string
	Result  JsonValue
	Failure JsonValue
	Error   string
}

// MarshalJSON emits the status-specific upstream shape.
func (outcome Outcome) MarshalJSON() ([]byte, error) {
	switch outcome.Status {
	case OutcomeCompleted, OutcomeAborted:
		return json.Marshal(struct {
			Status string    `json:"status"`
			Result JsonValue `json:"result"`
		}{outcome.Status, outcome.Result})
	case OutcomeFailed:
		return json.Marshal(struct {
			Status  string    `json:"status"`
			Failure JsonValue `json:"failure"`
		}{outcome.Status, outcome.Failure})
	case OutcomeFaulted:
		return json.Marshal(struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}{outcome.Status, outcome.Error})
	default:
		return json.Marshal(struct {
			Status string `json:"status"`
		}{outcome.Status})
	}
}

// UnmarshalJSON decodes any outcome.
func (outcome *Outcome) UnmarshalJSON(data []byte) error {
	var wire struct {
		Status  string    `json:"status"`
		Result  JsonValue `json:"result"`
		Failure JsonValue `json:"failure"`
		Error   string    `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*outcome = Outcome(wire)
	return nil
}

// Task statuses.
const (
	TaskPending  = "pending"
	TaskRunning  = "running"
	TaskTerminal = "terminal"
)

// Task is one task record.
type Task struct {
	Id             Id         `json:"id"`
	ConversationId Id         `json:"conversationId"`
	Kind           string     `json:"kind"`
	Input          JsonValue  `json:"input"`
	Status         string     `json:"status"`
	Checkpoint     Checkpoint `json:"checkpoint,omitempty"`
	// Abort is the durable abort mark.
	Abort   bool     `json:"abort,omitempty"`
	Outcome *Outcome `json:"outcome,omitempty"`
	After   []Id     `json:"after"`
	// Owns lists the conversations this task created.
	Owns []Id `json:"owns"`
	// Background tasks do not hold the conversation busy and survive
	// conversation abort.
	Background bool `json:"background,omitempty"`
}

// clone deep-copies a task.
func (task Task) clone() Task {
	task.Input = cloneJSON(task.Input)
	task.Checkpoint = cloneObject(task.Checkpoint)
	if task.Outcome != nil {
		outcome := *task.Outcome
		outcome.Result = cloneJSON(outcome.Result)
		outcome.Failure = cloneJSON(outcome.Failure)
		task.Outcome = &outcome
	}
	task.After = append([]Id{}, task.After...)
	task.Owns = append([]Id{}, task.Owns...)
	return task
}

// TaskPatch changes a task's mutable fields. ClearCheckpoint deletes the
// checkpoint (JSON null).
type TaskPatch struct {
	Id              Id
	Status          *string
	Checkpoint      Checkpoint
	ClearCheckpoint bool
	Abort           *bool
	Outcome         *Outcome
	Owns            []Id
	HasOwns         bool
}

// MarshalJSON emits only the patched members.
func (patch TaskPatch) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"id": patch.Id}
	if patch.Status != nil {
		fields["status"] = *patch.Status
	}
	if patch.ClearCheckpoint {
		fields["checkpoint"] = nil
	} else if patch.Checkpoint != nil {
		fields["checkpoint"] = patch.Checkpoint
	}
	if patch.Abort != nil {
		fields["abort"] = *patch.Abort
	}
	if patch.Outcome != nil {
		fields["outcome"] = patch.Outcome
	}
	if patch.HasOwns {
		fields["owns"] = append([]Id{}, patch.Owns...)
	}
	return json.Marshal(fields)
}

// UnmarshalJSON decodes a patch, preserving checkpoint null.
func (patch *TaskPatch) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded TaskPatch
	if err := decodeMember(raw, "id", &decoded.Id); err != nil {
		return err
	}
	if value, ok := raw["status"]; ok {
		var status string
		if err := json.Unmarshal(value, &status); err != nil {
			return err
		}
		decoded.Status = &status
	}
	if value, ok := raw["checkpoint"]; ok {
		if string(value) == "null" {
			decoded.ClearCheckpoint = true
		} else if err := json.Unmarshal(value, &decoded.Checkpoint); err != nil {
			return err
		}
	}
	if err := decodePatchTail(raw, &decoded); err != nil {
		return err
	}
	*patch = decoded
	return nil
}

func decodePatchTail(raw map[string]json.RawMessage, decoded *TaskPatch) error {
	if value, ok := raw["abort"]; ok {
		var abort bool
		if err := json.Unmarshal(value, &abort); err != nil {
			return err
		}
		decoded.Abort = &abort
	}
	if value, ok := raw["outcome"]; ok {
		var outcome Outcome
		if err := json.Unmarshal(value, &outcome); err != nil {
			return err
		}
		decoded.Outcome = &outcome
	}
	if value, ok := raw["owns"]; ok {
		decoded.HasOwns = true
		return json.Unmarshal(value, &decoded.Owns)
	}
	return nil
}

func decodeMember(raw map[string]json.RawMessage, key string, target any) error {
	value, ok := raw[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(value, target)
}

// apply returns task with the patch applied.
func (patch TaskPatch) apply(task Task) Task {
	next := task.clone()
	if patch.Status != nil {
		next.Status = *patch.Status
	}
	if patch.ClearCheckpoint {
		next.Checkpoint = nil
	} else if patch.Checkpoint != nil {
		next.Checkpoint = cloneObject(patch.Checkpoint)
	}
	if patch.Abort != nil {
		next.Abort = *patch.Abort
	}
	if patch.Outcome != nil {
		outcome := *patch.Outcome
		outcome.Result = cloneJSON(outcome.Result)
		outcome.Failure = cloneJSON(outcome.Failure)
		next.Outcome = &outcome
	}
	if patch.HasOwns {
		next.Owns = append([]Id{}, patch.Owns...)
	}
	return next
}

// Input statuses.
const (
	InputQueued     = "queued"
	InputPlaced     = "placed"
	InputDone       = "done"
	InputUnanswered = "unanswered"
)

// Input is one admitted input record.
type Input struct {
	Id             Id     `json:"id"`
	ConversationId Id     `json:"conversationId"`
	RequestId      string `json:"requestId,omitempty"`
	Status         string `json:"status"`
	Entry          *Id    `json:"entry,omitempty"`
	Answer         *Id    `json:"answer,omitempty"`
	// Reason is "aborted", "stale", "terminated", or "failed".
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// DocRef addresses one document.
type DocRef struct {
	Doc            string
	ConversationId Id
}

// Document names.
const (
	DocSession    = "session"
	DocRewindable = "rewindable"
	DocSticky     = "sticky"
)

// SessionDoc is the session document reference.
func SessionDoc() DocRef { return DocRef{Doc: DocSession} }

// RewindableDoc is a conversation's rewindable document reference.
func RewindableDoc(conversationId Id) DocRef {
	return DocRef{Doc: DocRewindable, ConversationId: conversationId}
}

// StickyDoc is a conversation's sticky document reference.
func StickyDoc(conversationId Id) DocRef {
	return DocRef{Doc: DocSticky, ConversationId: conversationId}
}

// key is the cache key for a document.
func (ref DocRef) key() string {
	if ref.Doc == DocSession {
		return DocSession
	}
	return fmt.Sprintf("%s:%d", ref.Doc, ref.ConversationId)
}

// MarshalJSON omits the conversation for the session document.
func (ref DocRef) MarshalJSON() ([]byte, error) {
	if ref.Doc == DocSession {
		return json.Marshal(struct {
			Doc string `json:"doc"`
		}{ref.Doc})
	}
	return json.Marshal(struct {
		Doc            string `json:"doc"`
		ConversationId Id     `json:"conversationId"`
	}{ref.Doc, ref.ConversationId})
}

// UnmarshalJSON decodes a document reference.
func (ref *DocRef) UnmarshalJSON(data []byte) error {
	var wire struct {
		Doc            string `json:"doc"`
		ConversationId Id     `json:"conversationId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*ref = DocRef(wire)
	return nil
}

// Write types.
const (
	WriteConversation = "conversation"
	WriteEntry        = "entry"
	WriteTask         = "task"
	WriteTaskPatch    = "task.patch"
	WriteInput        = "input"
	WriteDoc          = "doc"
)

// Write is one storage write; Type selects the populated field.
type Write struct {
	Type         string
	Conversation *Conversation
	Entry        *Entry
	Task         *Task
	Patch        *TaskPatch
	Input        *Input
	Ref          DocRef
	Ops          []Op
}

// MarshalJSON emits the type-specific upstream shape.
func (write Write) MarshalJSON() ([]byte, error) {
	switch write.Type {
	case WriteConversation:
		return json.Marshal(map[string]any{"type": write.Type, "conversation": write.Conversation})
	case WriteEntry:
		return json.Marshal(map[string]any{"type": write.Type, "entry": write.Entry})
	case WriteTask:
		return json.Marshal(map[string]any{"type": write.Type, "task": write.Task})
	case WriteTaskPatch:
		return json.Marshal(map[string]any{"type": write.Type, "patch": write.Patch})
	case WriteInput:
		return json.Marshal(map[string]any{"type": write.Type, "input": write.Input})
	case WriteDoc:
		return json.Marshal(map[string]any{"type": write.Type, "ref": write.Ref, "ops": write.Ops})
	default:
		return nil, fmt.Errorf("unknown write type %q", write.Type)
	}
}

// UnmarshalJSON decodes any write.
func (write *Write) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type         string        `json:"type"`
		Conversation *Conversation `json:"conversation"`
		Entry        *Entry        `json:"entry"`
		Task         *Task         `json:"task"`
		Patch        *TaskPatch    `json:"patch"`
		Input        *Input        `json:"input"`
		Ref          DocRef        `json:"ref"`
		Ops          []Op          `json:"ops"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*write = Write(wire)
	return nil
}

// SectionSeed sets a section value or removes an inherited section key.
type SectionSeed struct {
	Key    string
	Value  JsonValue
	Remove bool
}

// MarshalJSON emits {key, value} or {key, remove: true}.
func (seed SectionSeed) MarshalJSON() ([]byte, error) {
	if seed.Remove {
		return json.Marshal(map[string]any{"key": seed.Key, "remove": true})
	}
	return json.Marshal(map[string]any{"key": seed.Key, "value": seed.Value})
}

// UnmarshalJSON decodes a section seed.
func (seed *SectionSeed) UnmarshalJSON(data []byte) error {
	var wire struct {
		Key    string    `json:"key"`
		Value  JsonValue `json:"value"`
		Remove bool      `json:"remove"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*seed = SectionSeed(wire)
	return nil
}

// ModelRef identifies a model durably.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// RetryPolicy is the durable provider retry policy.
type RetryPolicy struct {
	Enabled         bool     `json:"enabled"`
	MaxRetries      float64  `json:"maxRetries"`
	BaseDelayMs     float64  `json:"baseDelayMs"`
	MaxAgentDelayMs *float64 `json:"maxAgentDelayMs,omitempty"`
}

// MemoOnce is first-writer-wins memoization in slot.memos, including when the
// stored winner is null.
func MemoOnce(slot JsonObject, key string, candidate JsonValue) JsonValue {
	memos, ok := slot["memos"].(map[string]any)
	if !ok {
		if _, exists := slot["memos"]; !exists {
			memos = map[string]any{}
			slot["memos"] = memos
		}
	}
	if winner, exists := memos[key]; exists {
		return winner
	}
	memos[key] = candidate
	return candidate
}

// InvocationToken is the unforgeable capability for one invocation; only the
// scheduler creates tokens.
type InvocationToken struct {
	taskId Id
	mode   string
	alive  atomic.Bool
}

func newInvocationToken(taskId Id, mode string) *InvocationToken {
	token := &InvocationToken{taskId: taskId, mode: mode}
	token.alive.Store(true)
	return token
}

// Alive reports whether the invocation is still running.
func (token *InvocationToken) Alive() bool { return token.alive.Load() }

func (token *InvocationToken) revoke() { token.alive.Store(false) }

// Invoker types.
const (
	invokerHost   = "host"
	invokerKernel = "kernel"
	invokerTask   = "task"
)

// invoker is the authority a transaction runs with.
type invoker struct {
	kind           string
	conversationId *Id
	token          *InvocationToken
	id             Id
	taskKind       *Kind
	core           bool
	mode           string
}

func kernelInvoker(conversationId *Id) invoker {
	return invoker{kind: invokerKernel, conversationId: conversationId}
}

func hostInvoker(conversationId Id) invoker {
	return invoker{kind: invokerHost, conversationId: &conversationId}
}

// Forbidden rejects an operation outside the invoker's authority.
type Forbidden struct{ What string }

func (err *Forbidden) Error() string { return "forbidden: " + err.What }

func forbidden(format string, args ...any) error {
	return &Forbidden{What: fmt.Sprintf(format, args...)}
}

// ConversationBusy rejects a send with whenBusy "reject".
type ConversationBusy struct{ Id Id }

func (err *ConversationBusy) Error() string { return fmt.Sprintf("conversation %d is busy", err.Id) }

// GenerationInProgress rejects a second live generation.
type GenerationInProgress struct{ Id Id }

func (err *GenerationInProgress) Error() string {
	return fmt.Sprintf("conversation %d already has a live generation", err.Id)
}

// CollapseInProgress rejects a second live collapse.
type CollapseInProgress struct{ Id Id }

func (err *CollapseInProgress) Error() string {
	return fmt.Sprintf("conversation %d already has a live collapse", err.Id)
}

// Faulted rejects every operation after a failed storage commit.
type Faulted struct{ Cause error }

func (err *Faulted) Error() string { return fmt.Sprintf("Session faulted: %v", err.Cause) }
func (err *Faulted) Unwrap() error { return err.Cause }

// Closed rejects operations after close.
type Closed struct{}

func (*Closed) Error() string { return "Session is closed" }

// TaskContractFault reports a kind that broke its contract.
type TaskContractFault struct {
	Kind string
	What string
}

func (err *TaskContractFault) Error() string {
	return fmt.Sprintf("kind %s broke its contract: %s", err.Kind, err.What)
}

// ReadAfterWrite rejects a scan-shaped read after a same-batch write to its
// domain; it poisons the transaction.
type ReadAfterWrite struct {
	Read  string
	Write string
}

func (err *ReadAfterWrite) Error() string {
	return fmt.Sprintf("%s after %s in the same transaction: the answer would not include the buffered write", err.Read, err.Write)
}

// NestedLineOperation rejects a Session line operation entered from the line.
type NestedLineOperation struct{}

func (*NestedLineOperation) Error() string { return "nested line operation" }

// TypeError mirrors JavaScript TypeError for document and transaction misuse.
type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }
