package pico3

import (
	"fmt"
	"slices"
	"sync"
)

// WatchCapacity bounds the envelopes a watch buffers before Start.
const WatchCapacity = 256

// ApplyEnvelope folds one envelope into a view without mutating it.
func ApplyEnvelope(view JsonObject, envelope *Envelope) (JsonObject, error) {
	next, err := ApplyImmutable(view, envelope.Ops)
	if err != nil {
		return nil, err
	}
	object, _ := next.(map[string]any)
	return object, nil
}

// Watch is one subscription to a conversation's view.
type Watch struct {
	// View is the captured snapshot at Revision.
	View     JsonObject
	revision int
	onReport func(error)
	onStop   func()

	mu         sync.Mutex
	listener   func(*Envelope)
	buffer     []*Envelope
	stopped    bool
	delivering bool
}

// Revision is the snapshot's revision; envelopes continue from it.
func (watch *Watch) Revision() int { return watch.revision }

// Closed reports whether the watch stopped.
func (watch *Watch) Closed() bool {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.stopped
}

// Start delivers buffered envelopes, then every later envelope; it is
// idempotent.
func (watch *Watch) Start(listener func(*Envelope)) {
	watch.mu.Lock()
	if watch.listener != nil || watch.stopped {
		watch.mu.Unlock()
		return
	}
	watch.listener = listener
	watch.delivering = true
	watch.mu.Unlock()
	watch.drain()
}

// Stop ends the watch; no callback runs afterwards.
func (watch *Watch) Stop() {
	watch.mu.Lock()
	if watch.stopped {
		watch.mu.Unlock()
		return
	}
	watch.stopped = true
	watch.buffer = nil
	watch.mu.Unlock()
	watch.onStop()
}

func (watch *Watch) accept(envelope *Envelope) {
	watch.mu.Lock()
	if watch.stopped {
		watch.mu.Unlock()
		return
	}
	if watch.listener == nil && len(watch.buffer) >= WatchCapacity {
		watch.mu.Unlock()
		watch.Stop()
		watch.report(fmt.Errorf("watch capacity %d exceeded before start()", WatchCapacity))
		return
	}
	watch.buffer = append(watch.buffer, envelope)
	if watch.listener == nil || watch.delivering {
		watch.mu.Unlock()
		return
	}
	watch.delivering = true
	watch.mu.Unlock()
	watch.drain()
}

func (watch *Watch) drain() {
	for {
		watch.mu.Lock()
		if watch.stopped || len(watch.buffer) == 0 {
			watch.delivering = false
			watch.mu.Unlock()
			return
		}
		envelope := watch.buffer[0]
		watch.buffer = watch.buffer[1:]
		listener := watch.listener
		watch.mu.Unlock()
		if err := callListener(listener, envelope); err != nil {
			watch.fail(err)
		}
	}
}

func callListener(listener func(*Envelope), envelope *Envelope) (err error) {
	defer recoverInto(&err)
	listener(envelope)
	return nil
}

func (watch *Watch) fail(err error) {
	watch.Stop()
	watch.report(err)
}

func (watch *Watch) report(err error) {
	defer func() { _ = recover() }() // upstream: agent/src/harness/pico3/view.ts:onReport
	watch.onReport(err)
}

type viewRecord struct {
	conversationId Id
	tracker        *Tracker
	watchers       []*Watch
	revision       int
}

type delivery struct {
	envelope *Envelope
	watchers []*Watch
}

// viewManager maintains one tracked view per watched conversation.
type viewManager struct {
	session  *Session
	onReport func(error)

	mu         sync.Mutex
	records    map[Id]*viewRecord
	deliveries []delivery
	delivering bool
}

func newViewManager(session *Session, onReport func(error)) *viewManager {
	return &viewManager{session: session, onReport: onReport, records: map[Id]*viewRecord{}}
}

// watch captures a snapshot and subscribes; it runs on the line.
func (views *viewManager) watch(conversation Conversation, entries []Entry) (*Watch, error) {
	views.mu.Lock()
	record, ok := views.records[conversation.Id]
	views.mu.Unlock()
	if !ok {
		built, err := views.build(conversation, entries)
		if err != nil {
			return nil, err
		}
		tracker := Track(built)
		tracker.Flush()
		record = &viewRecord{conversationId: conversation.Id, tracker: tracker}
		views.mu.Lock()
		views.records[conversation.Id] = record
		views.mu.Unlock()
	}
	watch := &Watch{View: cloneObject(record.tracker.State()), revision: record.revision, onReport: views.onReport}
	watch.onStop = func() { views.removeWatcher(record, watch) }
	views.mu.Lock()
	record.watchers = append(record.watchers, watch)
	views.mu.Unlock()
	return watch, nil
}

func (views *viewManager) removeWatcher(record *viewRecord, watch *Watch) {
	views.mu.Lock()
	defer views.mu.Unlock()
	record.watchers = slices.DeleteFunc(record.watchers, func(candidate *Watch) bool { return candidate == watch })
	if len(record.watchers) == 0 && views.records[record.conversationId] == record {
		delete(views.records, record.conversationId)
	}
}

// update runs on the Session line after persistence and index updates.
func (views *viewManager) update(result CommitResult) {
	views.mu.Lock()
	records := make([]*viewRecord, 0, len(views.records))
	for _, record := range views.records {
		records = append(records, record)
	}
	views.mu.Unlock()
	slices.SortFunc(records, func(left, right *viewRecord) int { return int(left.conversationId - right.conversationId) })
	for _, record := range records {
		if _, ok := views.session.conversationRecord(record.conversationId); !ok {
			continue
		}
		envelope, err := views.envelopeFor(record, result.Changes)
		if err != nil {
			views.failRecord(record, err)
			continue
		}
		if envelope == nil {
			continue
		}
		views.mu.Lock()
		views.deliveries = append(views.deliveries, delivery{envelope: envelope, watchers: slices.Clone(record.watchers)})
		views.mu.Unlock()
	}
}

func (views *viewManager) failRecord(record *viewRecord, err error) {
	views.mu.Lock()
	if views.records[record.conversationId] == record {
		delete(views.records, record.conversationId)
	}
	watchers := slices.Clone(record.watchers)
	views.mu.Unlock()
	for _, watch := range watchers {
		watch.fail(err)
	}
}

type recordChanges struct {
	appended          []Entry
	events            []ViewEvent
	rewindableChanged bool
	stickyChanged     bool
	taskChanged       bool
	configChanged     bool
	pluginChanged     bool
}

func scopeChanges(conversationId Id, changes *CommitChanges) (recordChanges, bool) {
	var scoped recordChanges
	relevantDocs := false
	for _, entry := range changes.Entries {
		if entry.ConversationId == conversationId {
			scoped.appended = append(scoped.appended, entry)
		}
	}
	for _, change := range changes.Docs {
		if change.Ref.Doc != DocSession && change.Ref.ConversationId != conversationId {
			continue
		}
		relevantDocs = true
		scoped.rewindableChanged = scoped.rewindableChanged || (change.Ref.Doc == DocRewindable)
		scoped.stickyChanged = scoped.stickyChanged || (change.Ref.Doc == DocSticky)
		scoped.pluginChanged = scoped.pluginChanged || touchesKey(change.Ops, "plugins")
	}
	for _, task := range changes.Tasks {
		scoped.taskChanged = scoped.taskChanged || task.ConversationId == conversationId
	}
	for _, event := range changes.Events {
		if event.ConversationId != conversationId {
			continue
		}
		scoped.events = append(scoped.events, event.Event)
		scoped.configChanged = scoped.configChanged || event.Event["type"] == "config.changed"
	}
	relevant := len(scoped.appended) > 0 || relevantDocs || scoped.taskChanged || len(scoped.events) > 0
	return scoped, relevant
}

func touchesKey(ops []Op, key string) bool {
	for _, op := range ops {
		path := op.Path()
		if op.Verb() == "r" || (len(path) > 0 && path[0] == key) {
			return true
		}
	}
	return false
}

func (views *viewManager) envelopeFor(record *viewRecord, changes *CommitChanges) (*Envelope, error) {
	scoped, relevant := scopeChanges(record.conversationId, changes)
	if !relevant {
		return nil, nil
	}
	state := record.tracker.State()
	ops := appendViewEntries(record.tracker, scoped.appended)
	if err := views.syncState(record.conversationId, state, scoped); err != nil {
		return nil, err
	}
	ops = append(ops, record.tracker.Flush()...)
	if len(ops) == 0 && len(scoped.events) == 0 {
		return nil, nil
	}
	record.revision++
	events := scoped.events
	if events == nil {
		events = []ViewEvent{}
	}
	return &Envelope{Revision: record.revision, Ops: ops, Events: events}, nil
}

// appendViewEntries preserves the explicit head splice from upstream view.ts
// applyEntries. A structural diff alone can replace equal-length entries in
// place, losing the transcript truncation operation observed by consumers.
func appendViewEntries(tracker *Tracker, appended []Entry) []Op {
	var ops []Op
	state := tracker.State()
	for _, entry := range appended {
		entries := arr(state, "entries")
		if entry.Head != nil {
			remove := len(entries)
			for index, candidate := range entries {
				if id, _ := asID(asObject(candidate)["id"]); id >= *entry.Head {
					remove = index
					break
				}
			}
			if remove > 0 {
				ops = append(ops, tracker.Flush()...)
				entries = slices.Clone(entries[remove:])
				state["entries"] = entries
				tracker.Flush()
				ops = append(ops, Op{"p", []any{"entries"}, 0, remove, []any{}})
			}
		}
		state["entries"] = append(entries, storedObject(entry))
	}
	return ops
}

func (views *viewManager) syncState(conversationId Id, state JsonObject, scoped recordChanges) error {
	needRewindable := scoped.rewindableChanged || scoped.configChanged || scoped.pluginChanged
	needSticky := scoped.stickyChanged || scoped.taskChanged || scoped.configChanged || scoped.pluginChanged
	var rewindable, sticky JsonObject
	var err error
	if needRewindable {
		if rewindable, err = views.document(RewindableDoc(conversationId)); err != nil {
			return err
		}
	}
	if needSticky {
		if sticky, err = views.document(StickyDoc(conversationId)); err != nil {
			return err
		}
	}
	if scoped.configChanged {
		state["config"] = views.config(rewindable, sticky)
	}
	if scoped.stickyChanged {
		state["inbox"] = cloneJSON(stickyInbox(sticky))
	}
	if scoped.stickyChanged || scoped.taskChanged {
		if err := views.syncTurn(conversationId, state, sticky); err != nil {
			return err
		}
	}
	if scoped.pluginChanged {
		session, err := views.document(SessionDoc())
		if err != nil {
			return err
		}
		plugins, err := views.plugins(rewindable, sticky, session)
		if err != nil {
			return err
		}
		state["plugins"] = plugins
	}
	return nil
}

func stickyInbox(sticky JsonObject) []any {
	inbox := arr(sticky, "inbox")
	if inbox == nil {
		return []any{}
	}
	return inbox
}

func (views *viewManager) syncTurn(conversationId Id, state JsonObject, sticky JsonObject) error {
	setOptional(state, "turn", views.turn(conversationId, sticky))
	setOptional(state, "compaction", views.compaction(conversationId))
	tasks, err := views.tasks(conversationId, sticky)
	state["tasks"] = tasks
	return err
}

func setOptional(state JsonObject, key string, value JsonObject) {
	if value == nil {
		delete(state, key)
		return
	}
	state[key] = value
}

func (views *viewManager) build(conversation Conversation, entries []Entry) (JsonObject, error) {
	rewindable, err := views.document(RewindableDoc(conversation.Id))
	if err != nil {
		return nil, err
	}
	sticky, err := views.document(StickyDoc(conversation.Id))
	if err != nil {
		return nil, err
	}
	session, err := views.document(SessionDoc())
	if err != nil {
		return nil, err
	}
	public := cloneConversation(conversation)
	public.Sections = nil
	list := make([]any, len(entries))
	for index, entry := range entries {
		list[index] = storedObject(entry)
	}
	tasks, err := views.tasks(conversation.Id, sticky)
	if err != nil {
		return nil, err
	}
	view := JsonObject{
		"conversation": storedObject(public),
		"entries":      list,
		"config":       views.config(rewindable, sticky),
		"inbox":        cloneJSON(stickyInbox(sticky)),
		"tasks":        tasks,
	}
	setOptional(view, "turn", views.turn(conversation.Id, sticky))
	setOptional(view, "compaction", views.compaction(conversation.Id))
	plugins, err := views.plugins(rewindable, sticky, session)
	if err != nil {
		return nil, err
	}
	view["plugins"] = plugins
	return view, nil
}

func (views *viewManager) document(ref DocRef) (JsonObject, error) {
	value := views.session.loadedDocument(ref)
	if value == nil {
		return nil, fmt.Errorf("view document %s is not loaded", ref.Doc)
	}
	return value, nil
}

func (views *viewManager) config(rewindable, sticky JsonObject) JsonObject {
	out := JsonObject{}
	for _, key := range views.session.defaults.keys() {
		doc := views.session.defaults.routeOf(key)
		source := sticky
		if doc == DocRewindable {
			source = rewindable
		}
		if value, present := source[key]; present {
			out[key] = cloneJSON(value)
			continue
		}
		if fallback, ok := views.session.defaults.defaultOf(doc, key); ok {
			out[key] = fallback
		}
	}
	return out
}

func (views *viewManager) liveTasksOf(conversationId Id) []Task {
	var out []Task
	for _, task := range views.session.liveTaskList() {
		if task.ConversationId == conversationId {
			out = append(out, task)
		}
	}
	return out
}

func (views *viewManager) turn(conversationId Id, sticky JsonObject) JsonObject {
	var generation, postTools *Task
	turnTasks := 0
	for _, task := range views.liveTasksOf(conversationId) {
		kind := views.session.kinds.get(task.Kind)
		if kind == nil || !kind.Turn {
			continue
		}
		turnTasks++
		switch {
		case task.Kind == "pi.generation" && generation == nil:
			generation = &task
		case task.Kind == "pi.post_tools" && postTools == nil:
			postTools = &task
		}
	}
	if turnTasks == 0 {
		return nil
	}
	inputTask := generation
	if inputTask == nil {
		inputTask = postTools
	}
	inputs := []any{}
	if inputTask != nil {
		inputs = idsJSON(idList(asObject(inputTask.Input)["inputs"]))
	}
	turnState := obj(sticky, "turn")
	message, streaming := turnState["message"]
	turn := JsonObject{"inputs": inputs, "tools": stripToolSlots(arr(turnState, "tools"))}
	if generation != nil {
		turn["generation"] = generationStatus(*generation, streaming)
	}
	if streaming {
		turn["message"] = cloneJSON(message)
	}
	return turn
}

func stripToolSlots(tools []any) []any {
	out := make([]any, len(tools))
	for index, slot := range tools {
		copied := cloneObject(asObject(slot))
		delete(copied, "memos")
		out[index] = copied
	}
	return out
}

func generationStatus(task Task, streaming bool) JsonObject {
	if task.Status == TaskPending && len(task.After) > 0 {
		return JsonObject{"stage": "waiting", "on": "compaction"}
	}
	checkpoint := task.Checkpoint
	attempt := numberOr(checkpoint["attempt"], 1)
	switch Phase(checkpoint) {
	case "requesting":
		stage := "requesting"
		if streaming {
			stage = "streaming"
		}
		return JsonObject{"stage": stage, "attempt": attempt}
	case "retrying":
		return JsonObject{"stage": "retrying", "attempt": attempt, "retryAt": numberOr(checkpoint["untilMs"], 0), "lastError": stringOr(checkpoint["lastError"], "")}
	case "deferred":
		return JsonObject{"stage": "deferred", "attempt": attempt, "pollAt": numberOr(checkpoint["pollAt"], 0)}
	default:
		return JsonObject{"stage": "preparing"}
	}
}

func numberOr(value any, fallback float64) float64 {
	if number, ok := asFloat(value); ok {
		return number
	}
	return fallback
}

func stringOr(value any, fallback string) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fallback
}

func (views *viewManager) compaction(conversationId Id) JsonObject {
	for _, task := range views.liveTasksOf(conversationId) {
		if task.Kind != "pi.collapse" {
			continue
		}
		checkpoint := task.Checkpoint
		stage := "summarizing"
		if Phase(checkpoint) == "retrying" {
			stage = "retrying"
		}
		out := JsonObject{"taskId": float64(task.Id), "reason": asObject(task.Input)["reason"], "stage": stage, "attempt": numberOr(checkpoint["attempt"], 1)}
		if until, ok := checkpoint["untilMs"]; stage == "retrying" && ok {
			out["retryAt"] = until
		}
		return out
	}
	return nil
}

func (views *viewManager) tasks(conversationId Id, sticky JsonObject) (JsonObject, error) {
	out := JsonObject{}
	slots := obj(sticky, "tasks")
	for _, task := range views.liveTasksOf(conversationId) {
		kind := views.session.kinds.get(task.Kind)
		if kind == nil || kind.Turn || task.Kind == "pi.collapse" {
			continue
		}
		status, err := describeTask(kind, task, slots[fmt.Sprint(task.Id)])
		if err != nil {
			return nil, err
		}
		entry := JsonObject{"kind": task.Kind, "status": status}
		if task.Background {
			entry["background"] = true
		}
		if task.Abort {
			entry["marked"] = true
		}
		out[fmt.Sprint(task.Id)] = entry
	}
	return out, nil
}

func describeTask(kind *Kind, task Task, slot any) (value JsonValue, err error) {
	defer recoverInto(&err)
	if kind.Describe == nil {
		phase := Phase(task.Checkpoint)
		if phase == "" {
			phase = TaskRunning
			if task.Status == TaskPending {
				phase = TaskPending
			}
		}
		return JsonObject{"phase": phase}, nil
	}
	var public JsonObject
	if object, ok := slot.(map[string]any); ok {
		public = cloneObject(object)
		delete(public, "memos")
	}
	return strictJSON(kind.Describe(task.clone(), public), fmt.Sprintf("task kind %s describe", task.Kind))
}

func (views *viewManager) plugins(rewindable, sticky, session JsonObject) (value JsonObject, err error) {
	defer recoverInto(&err)
	out := JsonObject{}
	sources := []JsonObject{obj(rewindable, "plugins"), obj(sticky, "plugins"), obj(session, "plugins")}
	for _, registration := range views.session.namespaces.list() {
		if registration.project == nil {
			continue
		}
		merged := JsonObject{}
		for _, doc := range []string{DocRewindable, DocSticky, DocSession} {
			for key, value := range registration.defaults[doc] {
				merged[key] = cloneJSON(value)
			}
		}
		for _, source := range sources {
			for key, value := range obj(source, registration.token.Id) {
				merged[key] = cloneJSON(value)
			}
		}
		projected, err := registration.project(merged)
		if err != nil {
			return nil, err
		}
		value, err := strictJSON(projected, fmt.Sprintf("namespace %s view", registration.token.Id))
		if err != nil {
			return nil, err
		}
		out[registration.token.Id] = value
	}
	return out, nil
}

// deliver runs after the Session line; listeners are synchronous and ordered.
func (views *viewManager) deliver() {
	views.mu.Lock()
	if views.delivering {
		views.mu.Unlock()
		return
	}
	views.delivering = true
	views.mu.Unlock()
	for {
		views.mu.Lock()
		if len(views.deliveries) == 0 {
			views.delivering = false
			views.mu.Unlock()
			return
		}
		next := views.deliveries[0]
		views.deliveries = views.deliveries[1:]
		views.mu.Unlock()
		for _, watch := range next.watchers {
			watch.accept(next.envelope)
		}
	}
}

func (views *viewManager) close() {
	views.mu.Lock()
	var watches []*Watch
	for _, record := range views.records {
		watches = append(watches, record.watchers...)
	}
	views.records = map[Id]*viewRecord{}
	views.deliveries = nil
	views.mu.Unlock()
	for _, watch := range watches {
		watch.Stop()
	}
}
