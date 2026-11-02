package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// NeedRead is the error a Store returns when the loaded state cannot answer a
// read. The callback that hit it is discarded and runs again after the host
// answers; Reads names what the host must fetch.
type NeedRead struct {
	Reads []ReadRequest
}

func (e *NeedRead) Error() string { return "read required" }

// IsNeedRead reports whether err is, or wraps, a *NeedRead.
func IsNeedRead(err error) (*NeedRead, bool) {
	if err == nil {
		return nil, false // errors.As(nil, ...) is false; the target would be allocated first
	}
	if n, ok := errors.AsType[*NeedRead](err); ok {
		return n, true
	}
	return nil, false
}

// RefusedError wraps the error a Store's Commit returns when one of Durable's own checks refuses the batch (global IDs, document actions,
// copy resolution), run against the state before the batch writes anything: the batch is not durable, and the batches before it in the
// writer's call are. Any other error of Commit is an unknown outcome and fails the Session.
type RefusedError struct{ Err error }

func (e *RefusedError) Error() string { return e.Err.Error() }
func (e *RefusedError) Unwrap() error { return e.Err }

// CommitResult is what a Storage call stored: the number of durable batches, and why the next one was refused, if one was. Adoption is the
// error of the first stored batch the host's side of the call could not adopt (Transaction.adopt threw), AdoptionAt the batch's index in the
// call: that batch is durable, but its publication fails with it and adopts nothing.
type CommitResult struct {
	Stored     int
	Refused    error
	Adoption   error
	AdoptionAt int
}

// ReadRequest identifies one read of committed state the host must serve.
type ReadRequest struct {
	Kind string
	Args []any
}

// Point addresses a document at "current" or as of a commit sequence.
type Point struct {
	Current bool
	Seq     Seq
}

// Current is the point of the newest incarnation. Read-only.
var Current = Point{Current: true}

// AsOf is the point of commit seq.
func AsOf(seq Seq) Point { return Point{Seq: seq} }

// ScopeKind is the owner kind of a document.
type ScopeKind string

// The document scopes.
const (
	ScopeSession      ScopeKind = "session"
	ScopeConversation ScopeKind = "conversation"
	ScopeTask         ScopeKind = "task"
)

// Scope is the owner of a document: the Session, a conversation or a task.
type Scope struct {
	Kind  ScopeKind
	Owner ID // conversation or task ID; zero for the session
}

// Address names a document: its kind, scope and, for a family member, its key.
type Address struct {
	Kind   string
	Scope  Scope
	Key    string
	Family bool
}

// StoredDoc is a document's materialized content at a point.
type StoredDoc struct {
	Record          *jv.Object
	Version         int
	Value           *jv.Object
	DeltasSinceBase int
}

// TaskQuery filters a task scan. Zero values leave a field unconstrained.
type TaskQuery struct {
	ConversationID ID
	Kind           string
	Status         string
	AbortRequested *bool
	Background     *bool
	Descending     bool
}

// SubmissionQuery filters a submission scan.
type SubmissionQuery struct {
	ConversationID ID
	Status         string
	Descending     bool
}

// ConversationQuery filters a conversation scan.
type ConversationQuery struct {
	OwnerConversationID ID
	OwnerTaskID         ID
	Descending          bool
}

// EntryQuery bounds an entry scan of one conversation, through its ancestry.
type EntryQuery struct {
	ConversationID ID
	MinEntryID     ID   // 0: unbounded
	MaxEntryID     ID   // 0: unbounded
	Ascending      bool // the default order is descending
}

// Page is one page of a scan. After is the cursor for the next page, 0 when none.
type Page struct {
	Items []*jv.Object
	After ID
}

// WriteKind is the kind of a StorageWrite.
type WriteKind uint8

// The StorageWrite kinds, as pi-durable's StorageWrite union.
const (
	WriteConversation WriteKind = iota + 1
	WriteEntry
	WriteTask
	WriteSubmission
	WriteDocCreate
	WriteDocCopy
	WriteDocChange
	WriteDocRetire
)

// DocCopySource is the document a fork copies and the point it is read at.
type DocCopySource struct {
	ID ID
	At Point
}

// Write is one storage write of a commit.
type Write struct {
	Kind WriteKind
	// Record is the conversation, entry, task or submission record, or the
	// document record (DocumentCreate: no createdAt or retiredAt) of a create or copy.
	Record *jv.Object
	// ID is the document of a change or retire.
	ID      ID
	Version int
	// Base is true when the content is a complete value (create, or a change that stores a base).
	Base  bool
	Value *jv.Object // Base content
	Ops   []delta.Op // delta content
	// Source is the copied document of a WriteDocCopy.
	Source DocCopySource
}

// callStarter is implemented by a Store whose commits of one Storage call share a transaction (Pi's Storage.commit runs its batches in one):
// the Session tells it where a call begins, so the first commit of a call is never joined to the last of the call before it.
type callStarter interface{ BeginCall() }

// Store is the committed state a Session reads and the place it commits to.
//
// Every read returns a nil record for an absent one. A read the loaded state
// cannot answer returns a *NeedRead. Commit never returns NeedRead. The Session reads through gatedStore, which touches the key of
// each read first (gate.go).
type Store interface {
	Conversation(id ID) (*jv.Object, error)
	ScanConversations(q ConversationQuery, limit int, after ID) (Page, error)
	// Entry returns the entry with its commit sequence.
	Entry(id ID) (*jv.Object, Seq, error)
	// EntryIn returns the entry when it is visible from the conversation.
	EntryIn(conversationID, id ID) (*jv.Object, Seq, error)
	// LatestHeadMarker returns the newest head-marker entry visible from the
	// conversation at or before atOrBefore (0: no bound), through fork ancestry.
	LatestHeadMarker(conversationID, atOrBefore ID) (*jv.Object, error)
	ScanEntries(q EntryQuery, limit int, after ID) (Page, error)
	Task(id ID) (*jv.Object, error)
	ScanTasks(q TaskQuery, limit int, after ID) (Page, error)
	Submission(id ID) (*jv.Object, error)
	ScanSubmissions(q SubmissionQuery, limit int, after ID) (Page, error)
	SubmissionByRequest(conversationID ID, requestID string) (*jv.Object, error)
	FindDocument(a Address, at Point) (*jv.Object, error)
	Document(id ID, at Point) (*StoredDoc, error)
	// ScanDocuments returns the documents of a scope alive at the point, optionally of one kind, ascending by ID.
	ScanDocuments(scope Scope, at Point, kind string, limit int, after ID) (Page, error)
	// Commit applies the writes as one atomic commit and returns its sequence;
	// nextID is the Session's next unminted ID, which the store keeps as
	// max(its stored next ID, nextID, the largest written ID + 1). A *RefusedError is a batch Durable's own checks refused; any other
	// error fails the Session.
	Commit(writes []Write, nextID ID) (Seq, error)
}
