package pico3

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
)

// AppendEntry appends an entry immediately (core only).
func (tx *Tx) AppendEntry(conversationId Id, entry NewEntry) (Id, error) {
	if err := tx.assertCore("appendEntry"); err != nil {
		return 0, err
	}
	return tx.appendEntry(conversationId, entry)
}

// AppendEntryOf appends a typed entry (core only).
func (tx *Tx) AppendEntryOf(conversationId Id, kind *EntryKind, entry NewEntry) (EntryRef, error) {
	entry.Kind = kind.Kind
	id, err := tx.AppendEntry(conversationId, entry)
	if err != nil {
		return EntryRef{}, err
	}
	return EntryRef{Id: id, Kind: kind}, nil
}

func (tx *Tx) appendEntry(conversationId Id, entry NewEntry) (Id, error) {
	if tx.invoker.kind == invokerTask && !tx.closing {
		if _, live := tx.session.liveTask(tx.invoker.id); !live {
			return 0, forbidden("appendEntry from a task that is not live")
		}
	}
	id := tx.session.storage.MintId()
	record := Entry{Id: id, ConversationId: conversationId, Kind: entry.Kind, Model: entry.Model, Data: entry.Data, Edits: entry.Edits}
	if entry.Head != nil {
		head := entry.Head.Id
		if entry.Head.Self {
			head = id
		}
		record.Head = &head
	}
	if tx.invoker.kind == invokerTask {
		by := tx.invoker.id
		record.ByTaskId = &by
	}
	record, err := plain(record)
	if err != nil {
		return 0, err
	}
	if err := validateEntry(record); err != nil {
		return 0, err
	}
	tx.writes = append(tx.writes, Write{Type: WriteEntry, Entry: &record})
	tx.changes.Entries = append(tx.changes.Entries, record)
	stored := storedObject(record)
	if record.Head != nil {
		tx.addEvent(conversationId, ViewEvent{"type": "head.moved", "entry": stored})
	}
	tx.addEvent(conversationId, ViewEvent{"type": "entry.added", "entry": cloneObject(stored)})
	tx.createdEntries[id] = record
	tx.wroteEntries[conversationId] = true
	return id, nil
}

func validateEntry(entry Entry) error {
	if entry.Head != nil && *entry.Head > entry.Id {
		return fmt.Errorf("entry %d: head %d is in the future", entry.Id, *entry.Head)
	}
	for _, message := range entry.Model {
		if _, ok := message["role"].(string); !ok {
			return fmt.Errorf("entry %d: malformed model message", entry.Id)
		}
	}
	return nil
}

// Write writes a passive entry: queued when the conversation is busy,
// appended when idle. It returns the input id.
func (tx *Tx) Write(conversationId Id, entry NewEntry) (Id, error) {
	if err := tx.surface(); err != nil {
		return 0, err
	}
	if err := tx.assertScope(conversationId, "write"); err != nil {
		return 0, err
	}
	if !tx.isCore() {
		if err := checkOrdinaryWrite(entry); err != nil {
			return 0, err
		}
	}
	return tx.write(conversationId, entry)
}

// WriteOf writes a typed passive entry.
func (tx *Tx) WriteOf(conversationId Id, kind *EntryKind, entry NewEntry) (Id, error) {
	entry.Kind = kind.Kind
	return tx.Write(conversationId, entry)
}

func checkOrdinaryWrite(entry NewEntry) error {
	if entry.Head != nil {
		return forbidden("write: head entries are core only")
	}
	if entry.Edits != nil {
		return forbidden("write: edits are core only")
	}
	if entryKindReserved(entry.Kind) {
		return forbidden("write: kind %s is reserved", entry.Kind)
	}
	if entry.Kind == "pi.notice" && (len(entry.Model) != 1 || str(entry.Model[0], "role") != "user") {
		return forbidden("write: pi.notice requires exactly one user model message")
	}
	return nil
}

func (tx *Tx) write(conversationId Id, entry NewEntry) (Id, error) {
	if tx.busy(conversationId) {
		id := tx.session.storage.MintId()
		sticky, err := tx.raw(StickyDoc(conversationId))
		if err != nil {
			return 0, err
		}
		sticky["inbox"] = append(arr(sticky, "inbox"), JsonObject{"id": float64(id), "mode": "write", "entry": storedObject(entry)})
		tx.putInput(Input{Id: id, ConversationId: conversationId, Status: InputQueued})
		tx.addEvent(conversationId, ViewEvent{"type": "input.queued", "input": float64(id), "mode": "write"})
		return id, nil
	}
	id := tx.session.storage.MintId()
	entryId, err := tx.appendEntry(conversationId, entry)
	if err != nil {
		return 0, err
	}
	tx.putInput(Input{Id: id, ConversationId: conversationId, Status: InputDone, Entry: &entryId})
	return id, nil
}

// Checkpoint replaces the invoking task's checkpoint.
func (tx *Tx) Checkpoint(value Checkpoint) error {
	if err := tx.assertTask("checkpoint"); err != nil {
		return err
	}
	if tx.invoker.mode == "abort" {
		return forbidden("checkpoint from an abort invocation")
	}
	current, ok := tx.invokerTask()
	if !ok {
		return errors.New("task not live")
	}
	checkpoint, err := plain(value)
	if err != nil {
		return err
	}
	current.Checkpoint = checkpoint
	return tx.setTask(current)
}

// SetTask replaces a task's mutable fields (core only).
func (tx *Tx) SetTask(task Task) error {
	if err := tx.assertCore("setTask"); err != nil {
		return err
	}
	return tx.setTask(task)
}

// setTask persists only what changed; a terminal task drops its checkpoint
// and live slot and emits its end event.
func (tx *Tx) setTask(task Task) error {
	task = task.clone()
	previous, hadPrevious := tx.createdTasks[task.Id]
	if !hadPrevious {
		previous, hadPrevious = tx.session.liveTask(task.Id)
	}
	patch, changed := diffTask(previous, hadPrevious, task)
	if task.Status == TaskTerminal {
		patch = terminalPatch(task)
		changed = true
		task.Checkpoint = nil
	}
	if !changed {
		return nil
	}
	if task.Status == TaskTerminal {
		sticky, err := tx.raw(StickyDoc(task.ConversationId))
		if err != nil {
			return err
		}
		if tasks, ok := sticky["tasks"].(map[string]any); ok {
			delete(tasks, fmt.Sprint(task.Id))
		}
	}
	tx.writes = append(tx.writes, Write{Type: WriteTaskPatch, Patch: &patch})
	tx.changes.Tasks = append(tx.changes.Tasks, task)
	if task.Status == TaskTerminal && (!hadPrevious || previous.Status != TaskTerminal) {
		tx.emitTaskEnd(task)
	}
	tx.createdTasks[task.Id] = task
	tx.wroteTasks = true
	return nil
}

func diffTask(previous Task, hadPrevious bool, task Task) (TaskPatch, bool) {
	patch := TaskPatch{Id: task.Id}
	changed := false
	if previous.Status != task.Status {
		status := task.Status
		patch.Status, changed = &status, true
	}
	if !jsonEqual(mustStored(previous.Checkpoint), mustStored(task.Checkpoint)) {
		changed = true
		if task.Checkpoint == nil {
			patch.ClearCheckpoint = true
		} else {
			patch.Checkpoint = task.Checkpoint
		}
	}
	if previous.Abort != task.Abort && task.Abort {
		abort := true
		patch.Abort, changed = &abort, true
	}
	if !jsonEqual(mustStored(previous.Outcome), mustStored(task.Outcome)) {
		if task.Outcome != nil {
			outcome := *task.Outcome
			patch.Outcome, changed = &outcome, true
		}
	}
	if !hadPrevious || !slices.Equal(previous.Owns, task.Owns) {
		patch.Owns, patch.HasOwns, changed = append([]Id{}, task.Owns...), true, true
	}
	return patch, changed
}

func terminalPatch(task Task) TaskPatch {
	status := TaskTerminal
	patch := TaskPatch{Id: task.Id, Status: &status, ClearCheckpoint: true, Owns: append([]Id{}, task.Owns...), HasOwns: true}
	if task.Outcome != nil {
		outcome := *task.Outcome
		patch.Outcome = &outcome
	}
	if task.Abort {
		abort := true
		patch.Abort = &abort
	}
	return patch
}

func (tx *Tx) emitTaskEnd(task Task) {
	kind := tx.session.kinds.get(task.Kind)
	if task.Kind == "pi.collapse" {
		tx.emitCollapseEnd(task)
		return
	}
	if (kind == nil || !kind.Turn) && task.Outcome != nil {
		tx.addEvent(task.ConversationId, ViewEvent{"type": "task.ended", "taskId": float64(task.Id), "kind": task.Kind, "outcome": task.Outcome.Status})
	}
}

func (tx *Tx) emitCollapseEnd(task Task) {
	if task.Outcome == nil {
		return
	}
	switch task.Outcome.Status {
	case OutcomeCompleted:
		result := asObject(task.Outcome.Result)
		tx.addEvent(task.ConversationId, ViewEvent{"type": "compaction.finished", "taskId": float64(task.Id), "summary": result["summary"]})
	case OutcomeFailed:
		failure := asObject(task.Outcome.Failure)
		tx.addEvent(task.ConversationId, ViewEvent{"type": "compaction.failed", "taskId": float64(task.Id), "reason": failure["reason"], "detail": failure["detail"]})
	}
}

// CreateTask creates a task from a registered kind token.
func (tx *Tx) CreateTask(kind *Kind, input JsonValue, options TaskOptions) (TaskRef, error) {
	if err := tx.surface(); err != nil {
		return TaskRef{}, err
	}
	if registered := tx.session.kinds.get(kind.Name); registered != kind {
		return TaskRef{}, forbidden(`createTask: kind "%s" is not the registered token`, kind.Name)
	}
	id, err := tx.createTask(TaskSpec{Kind: kind.Name, Input: input, ConversationId: options.ConversationId, Background: options.Background, After: options.After})
	if err != nil {
		return TaskRef{}, err
	}
	return TaskRef{Id: id, Kind: kind}, nil
}

// CreateTaskSpec creates a task by kind name (core only).
func (tx *Tx) CreateTaskSpec(spec TaskSpec) (Id, error) {
	if err := tx.assertCore("createTask by name"); err != nil {
		return 0, err
	}
	return tx.createTask(spec)
}

func (tx *Tx) createTask(spec TaskSpec) (Id, error) {
	kind := tx.session.kinds.get(spec.Kind)
	if kind == nil {
		return 0, fmt.Errorf("unknown task kind %s", spec.Kind)
	}
	if IsCoreKind(spec.Kind) && !tx.isCore() {
		return 0, forbidden("create core task %s", spec.Kind)
	}
	conversationId, err := tx.taskConversation(spec)
	if err != nil {
		return 0, err
	}
	if spec.Kind == "pi.generation" && tx.hasLiveKind(conversationId, spec.Kind) {
		return 0, &GenerationInProgress{Id: conversationId}
	}
	if spec.Kind == "pi.collapse" && tx.hasLiveKind(conversationId, spec.Kind) {
		return 0, &CollapseInProgress{Id: conversationId}
	}
	id := tx.session.storage.MintId()
	input, err := ToStored(spec.Input)
	if err != nil {
		return 0, err
	}
	task := Task{Id: id, ConversationId: conversationId, Kind: spec.Kind, Input: input, Status: TaskPending, After: append([]Id{}, spec.After...), Owns: []Id{}, Background: spec.Background}
	tx.writes = append(tx.writes, Write{Type: WriteTask, Task: &task})
	tx.changes.Tasks = append(tx.changes.Tasks, task)
	tx.emitTaskStart(task, kind)
	tx.createdTasks[id] = task
	tx.wroteTasks = true
	return id, nil
}

func (tx *Tx) taskConversation(spec TaskSpec) (Id, error) {
	var conversationId *Id
	switch {
	case spec.ConversationId != nil:
		conversationId = spec.ConversationId
	case tx.invoker.kind == invokerTask:
		conversationId = tx.invoker.conversationId
	}
	if conversationId == nil {
		return 0, errors.New("createTask: conversationId required")
	}
	return *conversationId, tx.assertScope(*conversationId, "createTask")
}

func (tx *Tx) emitTaskStart(task Task, kind *Kind) {
	if task.Kind == "pi.collapse" {
		input := asObject(task.Input)
		tx.addEvent(task.ConversationId, ViewEvent{"type": "compaction.started", "taskId": float64(task.Id), "reason": input["reason"], "through": input["through"]})
		return
	}
	if kind.Turn {
		return
	}
	event := ViewEvent{"type": "task.started", "taskId": float64(task.Id), "kind": task.Kind}
	if task.Background {
		event["background"] = true
	}
	tx.addEvent(task.ConversationId, event)
}

func (tx *Tx) hasLiveKind(conversationId Id, kind string) bool {
	for _, task := range tx.session.liveTaskList() {
		if task.ConversationId != conversationId || task.Kind != kind {
			continue
		}
		current := task
		if overlay, ok := tx.createdTasks[task.Id]; ok {
			current = overlay
		}
		if current.Status == TaskTerminal {
			continue
		}
		if tx.closing && tx.invoker.kind == invokerTask && tx.invoker.id == task.Id {
			continue
		}
		return true
	}
	for _, task := range tx.createdTasks {
		if _, live := tx.session.liveTask(task.Id); !live && task.ConversationId == conversationId && task.Kind == kind && task.Status != TaskTerminal {
			return true
		}
	}
	return false
}

// CreateConversation creates a conversation; a task becomes its owner.
func (tx *Tx) CreateConversation(spec ConversationSpec) (Id, error) {
	if err := tx.surface(); err != nil {
		return 0, err
	}
	var owner *Id
	if tx.invoker.kind == invokerTask {
		id := tx.invoker.id
		owner = &id
	}
	if spec.Parent != nil {
		if err := tx.assertScope(spec.Parent.ConversationId, "createConversation: parent"); err != nil {
			return 0, err
		}
	}
	return tx.insertConversation(spec, owner, false)
}

// CreateForkConversation creates a host fork from a validated snapshot (core
// only).
func (tx *Tx) CreateForkConversation(spec ConversationSpec) (Id, error) {
	if err := tx.assertCore("createForkConversation"); err != nil {
		return 0, err
	}
	return tx.insertConversation(spec, nil, true)
}

// CreateOwnedConversation creates a conversation owned by a live task,
// optionally inheriting the source's transcript tip and rewindable state
// (core only).
func (tx *Tx) CreateOwnedConversation(ownerTaskId, sourceConversationId Id, spec OwnedConversationSpec) (Id, error) {
	if err := tx.assertCore("createOwnedConversation"); err != nil {
		return 0, err
	}
	owner, ok := tx.session.liveTask(ownerTaskId)
	if !ok {
		owner, ok = tx.createdTasks[ownerTaskId]
	}
	if !ok || owner.Status == TaskTerminal || owner.ConversationId != sourceConversationId {
		return 0, forbidden("task %d cannot create an owned conversation", ownerTaskId)
	}
	var tip *Entry
	var inherited JsonObject
	if spec.Inherit {
		var err error
		if tip, err = tx.NewestEntry(sourceConversationId, NewestOptions{}); err != nil {
			return 0, err
		}
		if tip != nil {
			if inherited, err = tx.RewindableAsOf(sourceConversationId, tip.Id); err != nil {
				return 0, err
			}
		}
	}
	overrides, err := tx.session.defaults.validateSeed(DocRewindable, withoutPlugins(spec.Rewindable))
	if err != nil {
		return 0, err
	}
	rewindable := mergeInherited(inherited, overrides)
	conversation := ConversationSpec{Rewindable: rewindable, Sticky: spec.Sticky}
	if tip != nil {
		conversation.Parent = &ConversationParentSpec{ConversationId: sourceConversationId, At: tip.Id}
	}
	return tx.insertConversation(conversation, &ownerTaskId, inherited != nil)
}

func withoutPlugins(object JsonObject) JsonObject {
	out := cloneObject(object)
	if out == nil {
		return JsonObject{}
	}
	delete(out, "plugins")
	return out
}

func mergeInherited(inherited, overrides JsonObject) JsonObject {
	if inherited == nil {
		return overrides
	}
	merged := cloneObject(inherited)
	maps.Copy(merged, overrides)
	merged["plugins"] = cloneJSON(inherited["plugins"])
	return merged
}

func (tx *Tx) insertConversation(spec ConversationSpec, owner *Id, preservePlugins bool) (Id, error) {
	id := tx.session.storage.MintId()
	conversation := Conversation{Id: id, Owner: cloneIDPointer(owner)}
	if spec.Parent != nil && !spec.Parent.AtStart {
		conversation.Parent = &ConversationParent{ConversationId: spec.Parent.ConversationId, At: spec.Parent.At}
	}
	if len(spec.Sections) > 0 {
		conversation.Sections = spec.Sections
	}
	conversation = cloneConversation(conversation)
	tx.writes = append(tx.writes, Write{Type: WriteConversation, Conversation: &conversation})
	tx.changes.Conversations = append(tx.changes.Conversations, conversation)
	tx.createdConversations[id] = conversation
	rewindable, err := tx.session.defaults.freshRewindable(spec.Rewindable, preservePlugins)
	if err != nil {
		return 0, err
	}
	tx.seedDoc(RewindableDoc(id), rewindable)
	sticky, err := tx.session.defaults.freshSticky(spec.Sticky)
	if err != nil {
		return 0, err
	}
	tx.seedDoc(StickyDoc(id), sticky)
	if owner != nil {
		task, ok := tx.createdTasks[*owner]
		if !ok {
			task, ok = tx.session.liveTask(*owner)
		}
		if ok {
			task.Owns = append(slices.Clone(task.Owns), id)
			if err := tx.setTask(task); err != nil {
				return 0, err
			}
		}
	}
	return id, nil
}

func (tx *Tx) seedDoc(ref DocRef, value JsonObject) {
	tracker := Track(value)
	base := tracker.Flush()
	tx.writes = append(tx.writes, Write{Type: WriteDoc, Ref: ref, Ops: base})
	tx.changes.Docs = append(tx.changes.Docs, DocChange{Ref: ref, Ops: base})
	tx.session.docs.adopt(ref, tracker)
	tx.touch(ref, tracker)
}

// MarkTask durably marks a task for abort (core only).
func (tx *Tx) MarkTask(id Id) error {
	if err := tx.assertCore("markTask"); err != nil {
		return err
	}
	task, ok := tx.createdTasks[id]
	if !ok {
		task, ok = tx.session.liveTask(id)
	}
	if !ok {
		return fmt.Errorf("task %d not live", id)
	}
	if !task.Abort {
		task.Abort = true
		return tx.setTask(task)
	}
	return nil
}

// busy is prospective: live turn tasks, minus this transaction's terminals
// and the closing task, plus this transaction's new turn tasks.
func (tx *Tx) busy(conversationId Id) bool {
	isTurn := func(task Task) bool {
		kind := tx.session.kinds.get(task.Kind)
		return kind != nil && kind.Turn && !task.Background
	}
	for _, task := range tx.session.liveTaskList() {
		if task.ConversationId != conversationId || !isTurn(task) {
			continue
		}
		if overlay, ok := tx.createdTasks[task.Id]; ok && overlay.Status == TaskTerminal {
			continue
		}
		if tx.closing && tx.invoker.kind == invokerTask && task.Id == tx.invoker.id {
			continue
		}
		return true
	}
	for _, task := range tx.createdTasks {
		if _, live := tx.session.liveTask(task.Id); !live && task.ConversationId == conversationId && isTurn(task) && task.Status != TaskTerminal {
			return true
		}
	}
	return false
}

func (tx *Tx) putInput(input Input) {
	input = cloneInput(input)
	tx.writes = append(tx.writes, Write{Type: WriteInput, Input: &input})
	tx.changes.Inputs = append(tx.changes.Inputs, input)
	tx.inputsById[input.Id] = input
	if input.RequestId != "" {
		tx.inputsByRequest[requestKey(input.ConversationId, input.RequestId)] = input
	}
}

// Send admits user input (core only): queued when busy, otherwise placed
// with older queued items and a fresh generation.
func (tx *Tx) Send(conversationId Id, input SendInput) (Id, error) {
	if err := tx.assertCore("send"); err != nil {
		return 0, err
	}
	return tx.send(conversationId, input)
}

func (tx *Tx) send(conversationId Id, input SendInput) (Id, error) {
	if input.RequestId != "" {
		existing, err := tx.inputByRequest(conversationId, input.RequestId)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			return existing.Id, nil
		}
	}
	content, err := ToStored(input.Content)
	if err != nil {
		return 0, err
	}
	if tx.busy(conversationId) {
		return tx.queueSend(conversationId, input, content)
	}
	head, err := tx.NewestEntry(conversationId, NewestOptions{WithHead: true})
	if err != nil {
		return 0, err
	}
	boundary, err := tx.boundary(conversationId, "final", entryIdOf(head))
	if err != nil {
		return 0, err
	}
	id := tx.session.storage.MintId()
	entry, err := tx.placeUser(conversationId, content)
	if err != nil {
		return 0, err
	}
	tx.putInput(Input{Id: id, ConversationId: conversationId, Status: InputPlaced, Entry: &entry.id, RequestId: input.RequestId})
	tx.addEvent(conversationId, ViewEvent{"type": "input.placed", "input": float64(id), "entry": float64(entry.id)})
	tx.changes.Events = append(tx.changes.Events, entry.events...)
	inputs := append(slices.Clone(boundary.Triggers), id)
	if _, err := tx.createTask(TaskSpec{Kind: "pi.generation", ConversationId: &conversationId, Input: JsonObject{"inputs": idsJSON(inputs)}}); err != nil {
		return 0, err
	}
	tx.addEvent(conversationId, ViewEvent{"type": "turn.started", "inputs": idsJSON(inputs)})
	return id, nil
}

func (tx *Tx) queueSend(conversationId Id, input SendInput, content JsonValue) (Id, error) {
	mode := input.WhenBusy
	if mode == "" {
		mode = "followUp"
	}
	if mode == "reject" {
		return 0, &ConversationBusy{Id: conversationId}
	}
	id := tx.session.storage.MintId()
	sticky, err := tx.raw(StickyDoc(conversationId))
	if err != nil {
		return 0, err
	}
	sticky["inbox"] = append(arr(sticky, "inbox"), JsonObject{"id": float64(id), "mode": mode, "input": content})
	tx.putInput(Input{Id: id, ConversationId: conversationId, Status: InputQueued, RequestId: input.RequestId})
	tx.addEvent(conversationId, ViewEvent{"type": "input.queued", "input": float64(id), "mode": mode})
	return id, nil
}

func entryIdOf(entry *Entry) *Id {
	if entry == nil {
		return nil
	}
	id := entry.Id
	return &id
}

type placedEntry struct {
	id     Id
	events []ScopedEvent
}

// placeUser appends a pi.user entry and detaches its entry events so the
// caller can emit input.placed first.
func (tx *Tx) placeUser(conversationId Id, content JsonValue) (placedEntry, error) {
	return tx.placeEntry(conversationId, NewEntry{Kind: "pi.user", Model: []JsonObject{{"role": "user", "content": content, "timestamp": tx.session.now()}}})
}

func (tx *Tx) placeEntry(conversationId Id, entry NewEntry) (placedEntry, error) {
	start := len(tx.changes.Events)
	id, err := tx.appendEntry(conversationId, entry)
	if err != nil {
		return placedEntry{}, err
	}
	events := slices.Clone(tx.changes.Events[start:])
	tx.changes.Events = tx.changes.Events[:start]
	return placedEntry{id: id, events: events}, nil
}

func (tx *Tx) setInput(id Id, patch func(*Input)) error {
	current, err := tx.input(id)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("input %d not found", id)
	}
	next := cloneInput(*current)
	patch(&next)
	tx.putInput(next)
	return nil
}

// WithdrawInput withdraws a queued input (core only).
func (tx *Tx) WithdrawInput(id Id) (string, error) {
	if err := tx.assertCore("withdrawInput"); err != nil {
		return "", err
	}
	input, err := tx.input(id)
	if err != nil {
		return "", err
	}
	if input == nil {
		return "not_found", nil
	}
	if input.Status != InputQueued {
		return "already_placed", nil
	}
	if err := tx.preload([]DocRef{StickyDoc(input.ConversationId)}); err != nil {
		return "", err
	}
	sticky, err := tx.raw(StickyDoc(input.ConversationId))
	if err != nil {
		return "", err
	}
	sticky["inbox"] = slices.DeleteFunc(slices.Clone(arr(sticky, "inbox")), func(item any) bool {
		queued, _ := asID(asObject(item)["id"])
		return queued == id
	})
	if err := tx.setInput(id, func(next *Input) { next.Status, next.Reason = InputUnanswered, "aborted" }); err != nil {
		return "", err
	}
	tx.addEvent(input.ConversationId, ViewEvent{"type": "input.aborted", "input": float64(id)})
	return "aborted", nil
}

// InputResolution settles inputs as done or unanswered.
type InputResolution struct {
	Status string
	Answer Id
	Reason string
	Detail string
}

// ResolveInputs settles inputs (core only).
func (tx *Tx) ResolveInputs(ids []Id, resolution InputResolution) error {
	if err := tx.assertCore("resolveInputs"); err != nil {
		return err
	}
	return tx.resolveInputs(ids, resolution)
}

func (tx *Tx) resolveInputs(ids []Id, resolution InputResolution) error {
	for _, id := range ids {
		err := tx.setInput(id, func(next *Input) {
			next.Status = resolution.Status
			if resolution.Status == InputDone {
				answer := resolution.Answer
				next.Answer = &answer
				return
			}
			next.Reason = resolution.Reason
			if resolution.Detail != "" {
				next.Detail = resolution.Detail
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// BoundaryResult reports placed triggers and whether a self-head write
// terminated the turn.
type BoundaryResult struct {
	Triggers   []Id
	Terminated bool
}

// Boundary places inbox items at a safe boundary (core only). headBoundary is
// the newest head as the caller knows it.
func (tx *Tx) Boundary(conversationId Id, at string, headBoundary *Id) (BoundaryResult, error) {
	if err := tx.assertCore("boundary"); err != nil {
		return BoundaryResult{}, err
	}
	return tx.boundary(conversationId, at, headBoundary)
}

type queuedItem struct {
	id     Id
	mode   string
	entry  NewEntry
	input  JsonValue
	object JsonObject
}

func parseInbox(inbox []any) []queuedItem {
	items := make([]queuedItem, 0, len(inbox))
	for _, raw := range inbox {
		object := asObject(raw)
		id, _ := asID(object["id"])
		item := queuedItem{id: id, mode: str(object, "mode"), input: object["input"], object: object}
		if item.mode == "write" {
			_ = decodeInto(object["entry"], &item.entry)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].id < items[right].id })
	return items
}

func (tx *Tx) boundary(conversationId Id, at string, headBoundary *Id) (BoundaryResult, error) {
	sticky, err := tx.raw(StickyDoc(conversationId))
	if err != nil {
		return BoundaryResult{}, err
	}
	inbox := parseInbox(arr(sticky, "inbox"))
	var cut *Id
	for _, item := range inbox {
		if item.mode == "write" && item.entry.Head != nil && item.entry.Head.Self {
			id := item.id
			cut = &id
		}
	}
	stale, survivors, err := tx.staleBeforeCut(conversationId, inbox, cut)
	if err != nil {
		return BoundaryResult{}, err
	}
	selected := tx.selectInbox(sticky, survivors, stale, at)
	triggers, err := tx.placeSelected(conversationId, survivors, selected, headBoundary)
	if err != nil {
		return BoundaryResult{}, err
	}
	sticky["inbox"] = slices.DeleteFunc(slices.Clone(arr(sticky, "inbox")), func(item any) bool {
		id, _ := asID(asObject(item)["id"])
		return selected[id]
	})
	return BoundaryResult{Triggers: triggers, Terminated: cut != nil}, nil
}

func (tx *Tx) staleBeforeCut(conversationId Id, inbox []queuedItem, cut *Id) ([]Id, []queuedItem, error) {
	var stale []Id
	var survivors []queuedItem
	for _, item := range inbox {
		if cut != nil && item.mode != "write" && item.id < *cut {
			stale = append(stale, item.id)
			continue
		}
		survivors = append(survivors, item)
	}
	for _, id := range stale {
		if err := tx.setInput(id, func(next *Input) { next.Status, next.Reason = InputUnanswered, "stale" }); err != nil {
			return nil, nil, err
		}
		tx.addEvent(conversationId, ViewEvent{"type": "input.aborted", "input": float64(id)})
	}
	return stale, survivors, nil
}

func (tx *Tx) selectInbox(sticky JsonObject, survivors []queuedItem, stale []Id, at string) map[Id]bool {
	selected := map[Id]bool{}
	for _, id := range stale {
		selected[id] = true
	}
	pick := func(mode, policy string) {
		for _, item := range survivors {
			if item.mode != mode {
				continue
			}
			selected[item.id] = true
			if policy != "all" {
				return
			}
		}
	}
	for _, item := range survivors {
		if item.mode == "write" {
			selected[item.id] = true
		}
	}
	pick("steer", str(sticky, "steeringMode"))
	if at == "final" {
		pick("followUp", str(sticky, "followUpMode"))
	}
	return selected
}

func (tx *Tx) placeSelected(conversationId Id, survivors []queuedItem, selected map[Id]bool, headBoundary *Id) ([]Id, error) {
	head := headBoundary
	triggers := []Id{}
	for _, item := range survivors {
		if !selected[item.id] {
			continue
		}
		if item.mode != "write" {
			if err := tx.placeQueuedInput(conversationId, item); err != nil {
				return nil, err
			}
			triggers = append(triggers, item.id)
			continue
		}
		next, err := tx.placeQueuedWrite(conversationId, item, head)
		if err != nil {
			return nil, err
		}
		head = next
	}
	return triggers, nil
}

func (tx *Tx) placeQueuedInput(conversationId Id, item queuedItem) error {
	placed, err := tx.placeUser(conversationId, item.input)
	if err != nil {
		return err
	}
	if err := tx.setInput(item.id, func(next *Input) { next.Status, next.Entry = InputPlaced, &placed.id }); err != nil {
		return err
	}
	tx.addEvent(conversationId, ViewEvent{"type": "input.placed", "input": float64(item.id), "entry": float64(placed.id)})
	tx.changes.Events = append(tx.changes.Events, placed.events...)
	return nil
}

func (tx *Tx) placeQueuedWrite(conversationId Id, item queuedItem, head *Id) (*Id, error) {
	target := item.entry.Head
	if target != nil && !target.Self && head != nil && target.Id < *head {
		if err := tx.setInput(item.id, func(next *Input) { next.Status, next.Reason = InputUnanswered, "stale" }); err != nil {
			return nil, err
		}
		tx.addEvent(conversationId, ViewEvent{"type": "input.aborted", "input": float64(item.id)})
		return head, nil
	}
	placed, err := tx.placeEntry(conversationId, item.entry)
	if err != nil {
		return nil, err
	}
	if target != nil {
		next := target.Id
		if target.Self {
			next = placed.id
		}
		head = &next
	}
	if err := tx.setInput(item.id, func(next *Input) { next.Status, next.Entry = InputDone, &placed.id }); err != nil {
		return nil, err
	}
	tx.addEvent(conversationId, ViewEvent{"type": "input.placed", "input": float64(item.id), "entry": float64(placed.id)})
	tx.changes.Events = append(tx.changes.Events, placed.events...)
	return head, nil
}

// finish flushes touched documents and returns the batch.
func (tx *Tx) finish() ([]Write, error) {
	if tx.poisoned != nil {
		return nil, tx.poisoned
	}
	for _, conversationId := range tx.changedConfigOrder {
		keys := tx.changedConfig[conversationId]
		list := make([]any, len(keys))
		for index, key := range keys {
			list[index] = key
		}
		tx.addEvent(conversationId, ViewEvent{"type": "config.changed", "keys": list})
	}
	for _, key := range tx.touchedOrder {
		touched := tx.touched[key]
		ops := touched.tracker.Flush()
		if len(ops) == 0 {
			continue
		}
		tx.writes = append(tx.writes, Write{Type: WriteDoc, Ref: touched.ref, Ops: ops})
		tx.changes.Docs = append(tx.changes.Docs, DocChange{Ref: touched.ref, Ops: ops})
		tx.session.docs.noteOps(touched.ref, ops)
	}
	return tx.writes, nil
}

func (tx *Tx) revoke() { tx.membrane.Revoke() }

func (tx *Tx) evictTouched() {
	for _, key := range tx.touchedOrder {
		tx.session.docs.evict(tx.touched[key].ref)
	}
}
