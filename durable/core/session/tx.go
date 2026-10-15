package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

const internalScanPage = 256

// TaskDef is the part of a task definition the Session layer needs to create a
// task: its kind, its version and its first checkpoint. Phases, abort and
// migration belong to the scheduler.
type TaskDef struct {
	Name    string
	Version int
	// Initial returns the first checkpoint for a new task's input.
	Initial func(input any) any
}

// TaskOptions are the creation options of a task (types.ts TaskOptions).
type TaskOptions struct {
	// OwnerTask is the owning task of a child task; zero for a conversation-owned task.
	OwnerTask ID
	// ConversationID defaults to the owner task's conversation or the transaction's bound conversation.
	ConversationID ID
	Background     bool
	// AbandonOnRestart marks a task its creator awaits only in memory: a later Harness abort-marks it with reason restart.
	AbandonOnRestart bool
}

// Ownership is how a created conversation is attributed.
type Ownership struct {
	// Task is the owning task; zero means ownerless.
	Task ID
}

// TxScope binds a transaction to a conversation and a task: the default
// conversation of Tx.CreateTask and the task attributed to appended entries.
type TxScope struct {
	ConversationID ID
	TaskID         ID
}

type txTask struct {
	committed    *jv.Object
	hasCommitted bool
	write        *jv.Object
	writeIsNew   bool
	pubConv      ID
}

type submissionChange struct {
	id     ID
	place  bool
	entry  ID
	settle *jv.Object
}

type docTargetKind uint8

const (
	targetLoaded docTargetKind = iota + 1
	targetCreated
	targetForkCopy
	targetRetireOnly
)

type docTarget struct {
	kind    docTargetKind
	loaded  *loadedDoc
	record  *jv.Object // created, fork copy, retire-only
	version int
	tracker *delta.Tracker
	source  DocCopySource
}

type docEntry struct {
	addressID      string
	address        Address
	def            *DocDef
	draft          *delta.Obj
	target         *docTarget
	change         *delta.Change
	prepared       *delta.Prepared
	retireOnCommit bool
	// external holds operations a host's tracker produced for this draft (SetDocOps); hasExternal marks them set.
	external    []delta.Op
	hasExternal bool
}

type planChange struct {
	tracker  *delta.Tracker
	prepared *delta.Prepared
	version  int
	loaded   *loadedDoc
	def      *DocDef
}

type docPlan struct {
	addressID string
	record    *jv.Object
	committed bool
	retire    bool
	content   *Write
	change    *planChange
	convID    ID
}

// Tx stages one atomic commit. Every method must run inside the callback that
// received it; after the callback ends the transaction is sealed.
type Tx struct {
	s     *Session
	scope TxScope

	sealed        bool
	hasTableWrite bool

	writes  []Write
	settled []Write // the writes of a commit a *NeedGate held
	// The maps below are made by their first write: most transactions of a step write none of them, and a nil map reads as empty.
	createdConvs      map[ID]bool
	forkSourceConvs   map[ID]bool
	forkSourceDocs    map[ID]bool
	tasks             map[ID]*txTask
	taskOrder         []ID
	submissions       map[ID]*jv.Object
	submissionOrder   []ID
	submissionChanges []submissionChange
	// settleReads counts the Storage reads of the settle that the change callback did not already make: the committed record of
	// each staged submission change (transaction.ts #assemble :818-823).
	settleReads int
	// settleCached counts the replaced tasks whose committed read the callback had already made (assemble awaits the settled promise).
	settleCached int

	plans        []*docPlan
	documents    []*docEntry
	latestByAddr map[string]*docEntry
	mintStart    ID
	// ticks is Pi's await ticks of the change callback so far (hopcost: each Transaction method await, then the
	// Scheduler wrapper's).
	ticks int
}

// charge adds the ticks of one Transaction method await.
func (tx *Tx) charge(n int) { tx.ticks += n }

// Await charges the ticks of a callback's own `await` of Pi code that is not a Transaction method (hopcost: an async function
// without awaits of its own settles at once and its caller resumes one tick later).
func (tx *Tx) Await(n int) { tx.charge(n) }

func (tx *Tx) readTicks(r hopcost.Read) { tx.charge(hopcost.TxRead(tx.s.hop, r)) }

func newTx(s *Session, scope TxScope) *Tx {
	return &Tx{
		s: s, scope: scope,
		mintStart: s.nextID,
	}
}

func (tx *Tx) assertOpen() error {
	if tx.sealed {
		return ErrSealed
	}
	return nil
}

func (tx *Tx) mint() ID {
	id := tx.s.nextID
	tx.s.nextID++
	return id
}

// read gates a table read: it fails once the transaction has written a table.
func (tx *Tx) read(method string) error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	if tx.hasTableWrite {
		return readAfterWrite(method)
	}
	return nil
}

func (tx *Tx) write() error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	return nil
}

// Conversation returns a committed conversation record, or nil.
func (tx *Tx) Conversation(id ID) (*jv.Object, error) {
	if err := tx.read("conversation"); err != nil {
		return nil, err
	}
	tx.readTicks(hopcost.ReadRow)
	return tx.s.store.Conversation(id)
}

// Entry returns a committed entry, or nil.
func (tx *Tx) Entry(id ID) (*jv.Object, error) {
	if err := tx.read("entry"); err != nil {
		return nil, err
	}
	tx.charge(hopcost.TxEntry(tx.s.hop))
	e, _, err := tx.s.store.Entry(id)
	return e, err
}

// Task returns the committed task record, or nil.
func (tx *Tx) Task(id ID) (*jv.Object, error) {
	if err := tx.read("task"); err != nil {
		return nil, err
	}
	t := tx.tasks[id]
	tx.charge(hopcost.TxTask(tx.s.hop, t != nil && t.hasCommitted))
	return tx.committedTask(id)
}

// ScanConversations scans committed conversations.
func (tx *Tx) ScanConversations(q ConversationQuery, limit int, after ID) (Page, error) {
	if err := tx.read("scanConversations"); err != nil {
		return Page{}, err
	}
	tx.readTicks(hopcost.ReadRow)
	return tx.s.store.ScanConversations(q, limit, after)
}

// ScanEntries scans committed entries visible from a conversation.
func (tx *Tx) ScanEntries(q EntryQuery, limit int, after ID) (Page, error) {
	if err := tx.read("scanEntries"); err != nil {
		return Page{}, err
	}
	tx.readTicks(hopcost.ReadScanEntries)
	return tx.s.store.ScanEntries(q, limit, after)
}

// LatestHeadMarker returns the newest head-marker entry of a conversation, or nil.
func (tx *Tx) LatestHeadMarker(conversationID ID) (*jv.Object, error) {
	if err := tx.read("latestHeadMarker"); err != nil {
		return nil, err
	}
	tx.readTicks(hopcost.ReadHeadMarker)
	return tx.s.store.LatestHeadMarker(conversationID, 0)
}

// ScanTasks scans committed tasks.
func (tx *Tx) ScanTasks(q TaskQuery, limit int, after ID) (Page, error) {
	if err := tx.read("scanTasks"); err != nil {
		return Page{}, err
	}
	tx.readTicks(hopcost.ReadRow)
	return tx.s.store.ScanTasks(q, limit, after)
}

// Submission returns a committed submission, or nil.
func (tx *Tx) Submission(id ID) (*jv.Object, error) {
	if err := tx.read("submission"); err != nil {
		return nil, err
	}
	tx.readTicks(hopcost.ReadRow)
	return tx.s.store.Submission(id)
}

// SubmissionByRequest returns the submission with the request ID, or nil.
func (tx *Tx) SubmissionByRequest(conversationID ID, requestID string) (*jv.Object, error) {
	if err := tx.read("submissionByRequest"); err != nil {
		return nil, err
	}
	tx.readTicks(hopcost.ReadRow)
	return tx.s.store.SubmissionByRequest(conversationID, requestID)
}

// taskTicksState is whether the task is staged in this transaction, and whether its committed read is cached.
func (tx *Tx) taskTicksState(id ID) (staged, cached bool) {
	if t := tx.tasks[id]; t != nil {
		return t.write != nil, t.hasCommitted
	}
	return false, false
}

func (tx *Tx) taskEntry(id ID) *txTask {
	t := tx.tasks[id]
	if t == nil {
		t = &txTask{}
		if tx.tasks == nil {
			tx.tasks = map[ID]*txTask{}
		}
		tx.tasks[id] = t
		tx.taskOrder = append(tx.taskOrder, id)
	}
	return t
}

//go:noinline
func (tx *Tx) committedTask(id ID) (*jv.Object, error) {
	t := tx.taskEntry(id)
	if !t.hasCommitted {
		rec, err := tx.s.store.Task(id)
		if err != nil {
			return nil, err
		}
		t.committed, t.hasCommitted = rec, true
	}
	return t.committed, nil
}

func (tx *Tx) currentTask(id ID) (*jv.Object, error) {
	if t := tx.tasks[id]; t != nil && t.write != nil {
		return t.write, nil
	}
	return tx.committedTask(id)
}

// ConversationCreated reports whether this transaction created the conversation (transaction.ts #createdConversationIds).
func (tx *Tx) ConversationCreated(id ID) bool { return tx.createdConvs[id] }

func (tx *Tx) requireConversation(id ID) error {
	if tx.createdConvs[id] {
		return nil
	}
	rec, err := tx.s.store.Conversation(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return errorf("Conversation %d does not exist", id)
	}
	return nil
}

// CreateConversation stages a new conversation.
func (tx *Tx) CreateConversation(own Ownership) (*jv.Object, error) {
	if err := tx.write(); err != nil {
		return nil, err
	}
	return tx.stageConversation(nil, own, 0)
}

// CreateRootConversation stages the root conversation under its reserved ID.
func (tx *Tx) CreateRootConversation() (*jv.Object, error) {
	if err := tx.write(); err != nil {
		return nil, err
	}
	return tx.stageConversation(nil, Ownership{}, RootConversationID)
}

// ForkConversation stages a conversation that inherits the parent's history
// through the entry at.
func (tx *Tx) ForkConversation(parent, at ID, own Ownership) (*jv.Object, error) {
	if err := tx.write(); err != nil {
		return nil, err
	}
	return tx.stageConversation(&forkParent{parent, at}, own, 0)
}

type forkParent struct{ conversationID, at ID }

func (tx *Tx) stageConversation(parent *forkParent, own Ownership, reserved ID) (*jv.Object, error) {
	ownerStaged, ownerCached := tx.taskTicksState(own.Task)
	before := tx.ticks
	defer func() {
		// The conversationCreated hook's own awaits were charged as they ran; Pi's Session default is Promise.resolve() (1).
		hook := tx.ticks - before
		if tx.s.ConversationCreated == nil {
			hook = 1
		} else {
			hook++ // the async hook's return
		}
		tx.ticks = before + hopcost.TxCreateConversation(tx.s.hop, own.Task != 0, ownerStaged, ownerCached, hook)
	}()
	id := reserved
	if id == 0 {
		id = tx.mint()
	}
	var owner *jv.Object
	if own.Task != 0 {
		task, err := tx.currentTask(own.Task)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, errorf("Conversation owner task %d does not exist", own.Task)
		}
		owner = obj("conversationId", idv(mustInt(task, "conversationId")), "taskId", idv(own.Task))
	}
	record := obj("id", idv(id))
	if parent != nil {
		record.Set("parent", obj("conversationId", idv(parent.conversationID), "at", idv(parent.at)))
	}
	if owner != nil {
		record.Set("owner", owner)
	}
	var copies []forkCopy
	if parent != nil {
		var err error
		copies, err = tx.prepareForkDocumentCopies(parent.conversationID, parent.at, id)
		if err != nil {
			return nil, err
		}
	}
	for _, c := range copies {
		if tx.forkSourceDocs == nil {
			tx.forkSourceDocs = map[ID]bool{}
		}
		tx.forkSourceDocs[c.source.ID] = true
		e := &docEntry{
			addressID: addressID(recordAddress(c.record)), address: recordAddress(c.record),
			target: &docTarget{kind: targetForkCopy, record: c.record, source: c.source},
		}
		tx.documents = append(tx.documents, e)
		if tx.latestByAddr == nil {
			tx.latestByAddr = map[string]*docEntry{}
		}
		tx.latestByAddr[e.addressID] = e
	}
	if parent != nil {
		if tx.forkSourceConvs == nil {
			tx.forkSourceConvs = map[ID]bool{}
		}
		tx.forkSourceConvs[parent.conversationID] = true
	}
	if tx.createdConvs == nil {
		tx.createdConvs = map[ID]bool{}
	}
	tx.createdConvs[id] = true
	tx.writes = append(tx.writes, Write{Kind: WriteConversation, Record: record})
	if tx.s.ConversationCreated != nil {
		if err := tx.s.ConversationCreated(tx, record); err != nil {
			return nil, err
		}
	}
	return record, nil
}

// AppendEntry stages a new entry. The draft's head may be the string "self".
func (tx *Tx) AppendEntry(conversationID ID, draft *jv.Object) (*jv.Object, error) {
	if err := tx.write(); err != nil {
		return nil, err
	}
	tx.charge(hopcost.TxAppendEntry(tx.s.hop, tx.createdConvs[conversationID]))
	if err := tx.requireConversation(conversationID); err != nil {
		return nil, err
	}
	id := tx.mint()
	rest := without(draft, "head")
	record := rest.Shallow()
	record.Set("id", idv(id))
	record.Set("conversationId", idv(conversationID))
	if h, ok := draft.Get("head"); ok {
		if s, isStr := h.(string); isStr && s == "self" {
			record.Set("head", idv(id))
		} else if h != nil {
			record.Set("head", h)
		}
	}
	if tx.scope.TaskID != 0 {
		record.Set("byTaskId", idv(tx.scope.TaskID))
	}
	tx.writes = append(tx.writes, Write{Kind: WriteEntry, Record: record})
	return record, nil
}

// CreateTask stages a pending task and returns its ID.
func (tx *Tx) CreateTask(def *TaskDef, input any, opts TaskOptions) (ID, error) {
	if err := tx.write(); err != nil {
		return 0, err
	}
	ownerStaged, ownerCached := tx.taskTicksState(opts.OwnerTask)
	defer func() {
		conv := opts.ConversationID
		if conv == 0 {
			conv = tx.scope.ConversationID
		}
		if t := tx.tasks[opts.OwnerTask]; opts.OwnerTask != 0 && t != nil {
			if rec := t.write; rec != nil {
				conv = mustInt(rec, "conversationId")
			} else if t.committed != nil {
				conv = mustInt(t.committed, "conversationId")
			}
		}
		tx.charge(hopcost.TxCreateTask(tx.s.hop, opts.OwnerTask != 0, ownerStaged, ownerCached, tx.createdConvs[conv]))
	}()
	var owner *jv.Object
	if opts.OwnerTask != 0 {
		var err error
		if owner, err = tx.currentTask(opts.OwnerTask); err != nil {
			return 0, err
		}
		if owner == nil {
			return 0, errorf("Task owner %d does not exist", opts.OwnerTask)
		}
		if opts.Background {
			return 0, errors.New("A child task cannot be background")
		}
		if opts.ConversationID != 0 && opts.ConversationID != mustInt(owner, "conversationId") {
			return 0, errorf("A child task lives in its owner's conversation %d", mustInt(owner, "conversationId"))
		}
	}
	conv := opts.ConversationID
	if conv == 0 {
		conv = tx.scope.ConversationID
	}
	if owner != nil {
		conv = mustInt(owner, "conversationId")
	}
	if conv == 0 {
		return 0, errors.New("Tx.createTask() requires options.conversationId")
	}
	if err := tx.requireConversation(conv); err != nil {
		return 0, err
	}
	checkpoint := def.Initial(input)
	id := tx.mint()
	rec := obj("id", idv(id), "conversationId", idv(conv), "kind", def.Name, "version", float64(def.Version), "input", jv.Clone(input))
	if owner != nil {
		rec.Set("owner", idv(mustInt(owner, "id")))
	}
	rec.Set("background", opts.Background)
	rec.Set("abortRequested", false)
	if opts.AbandonOnRestart {
		rec.Set("abandonOnRestart", true)
	}
	rec.Set("state", obj("status", StatusPending, "checkpoint", jv.Clone(checkpoint)))
	t := tx.taskEntry(id)
	t.write, t.writeIsNew = rec, true
	return id, nil
}

// CreateSubmission stages a submission; its record is {...create, id}.
func (tx *Tx) CreateSubmission(create *jv.Object) (*jv.Object, error) {
	if err := tx.write(); err != nil {
		return nil, err
	}
	tx.charge(hopcost.TxCreateSubmission(tx.s.hop, tx.createdConvs[mustInt(create, "conversationId")]))
	if err := tx.requireConversation(mustInt(create, "conversationId")); err != nil {
		return nil, err
	}
	id := tx.mint()
	rec := jv.Clone(create).(*jv.Object)
	rec.Set("id", idv(id))
	tx.setSubmission(id, rec)
	return rec, nil
}

func (tx *Tx) setSubmission(id ID, rec *jv.Object) {
	if _, ok := tx.submissions[id]; !ok {
		tx.submissionOrder = append(tx.submissionOrder, id)
	}
	if tx.submissions == nil {
		tx.submissions = map[ID]*jv.Object{}
	}
	tx.submissions[id] = rec
}

// SettleSubmission stages a settlement: status "done" with answer, or
// "unanswered" with reason and optional detail.
func (tx *Tx) SettleSubmission(id ID, settlement *jv.Object) error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	tx.submissionChanges = append(tx.submissionChanges, submissionChange{id: id, settle: jv.Clone(settlement).(*jv.Object)})
	return nil
}

// PlaceSubmission stages the placement of a queued submission at an entry.
func (tx *Tx) PlaceSubmission(id, entry ID) error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	tx.hasTableWrite = true
	tx.submissionChanges = append(tx.submissionChanges, submissionChange{id: id, place: true, entry: entry})
	return nil
}

// SetTask stages a replacement of a task record, stamping start and end times. The staged record is a deep copy, as Pi's
// copyJson makes it, so the caller's value stays its own.
func (tx *Tx) SetTask(value *jv.Object) error {
	_, err := tx.setTask(value, true)
	return err
}

// stageTask is SetTask for a record the scheduler built for this write (with, withState, withAbortMark): its top level is
// new, and its nested values are those of committed records and host values, which nothing changes in place. It stages the
// record itself, stamped. The copy SetTask makes would be the second record of each task a transaction writes: a finalize
// that ends a 20,000-deep chain in one transaction held both, about 1 KB per task on the TinyGo core.
func (tx *Tx) stageTask(value *jv.Object) error {
	_, err := tx.setTask(value, false)
	return err
}

// setTask stages value, a deep copy of it when clone is set, and returns the staged record.
func (tx *Tx) setTask(value *jv.Object, clone bool) (*jv.Object, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	tx.hasTableWrite = true
	id := mustInt(value, "id")
	t := tx.taskEntry(id)
	candidate := t.write
	if candidate != nil && taskStatus(candidate) == StatusTerminal {
		return nil, errorf("Task %d already has a terminal candidate", id)
	}
	if candidate != nil && mustInt(candidate, "conversationId") != mustInt(value, "conversationId") {
		return nil, errorf("Task %d cannot change conversations", id)
	}
	staged := tx.stampTimes(value, candidate)
	if clone {
		staged = jv.Clone(staged).(*jv.Object)
	}
	// A staged create stays a create.
	t.write = staged
	return staged, nil
}

func (tx *Tx) stampTimes(value, candidate *jv.Object) *jv.Object {
	status := taskStatus(value)
	pick := func(key string, stamps bool) (any, bool) {
		if candidate != nil {
			if v, ok := candidate.Get(key); ok {
				return v, true
			}
		}
		if v, ok := value.Get(key); ok {
			return v, true
		}
		if stamps {
			return tx.s.now(), true
		}
		return nil, false
	}
	started, hasStarted := pick("startedAt", status == StatusRunning)
	ended, hasEnded := pick("endedAt", status == StatusTerminal)
	curStarted, curHas1 := value.Get("startedAt")
	curEnded, curHas2 := value.Get("endedAt")
	if hasStarted == curHas1 && hasEnded == curHas2 && (!hasStarted || curStarted == started) && (!hasEnded || curEnded == ended) {
		return value
	}
	out := spread(value)
	if hasStarted {
		out.Set("startedAt", started)
	}
	if hasEnded {
		out.Set("endedAt", ended)
	}
	return out
}

// StagedTasks returns the staged task records in staging order.
func (tx *Tx) StagedTasks() []*jv.Object {
	var out []*jv.Object
	for _, id := range tx.taskOrder {
		if w := tx.tasks[id].write; w != nil {
			out = append(out, w)
		}
	}
	return out
}

// StagedConversations returns the staged conversation records.
func (tx *Tx) StagedConversations() []*jv.Object {
	var out []*jv.Object
	for _, w := range tx.writes {
		if w.Kind == WriteConversation {
			out = append(out, w.Record)
		}
	}
	return out
}
