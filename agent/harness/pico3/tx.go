package pico3

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ScopedEvent is a view event bound to its conversation.
type ScopedEvent struct {
	ConversationId Id
	Event          ViewEvent
}

// DocChange is one document's committed ops.
type DocChange struct {
	Ref DocRef
	Ops []Op
}

// CommitChanges is what one commit changed.
type CommitChanges struct {
	Entries       []Entry
	Tasks         []Task
	Inputs        []Input
	Conversations []Conversation
	Docs          []DocChange
	Events        []ScopedEvent
}

type touchedDoc struct {
	ref     DocRef
	tracker *Tracker
}

// Tx is one transaction. A single implementation serves the host, ordinary
// tasks, and core machinery; every method checks the invoker's authority at
// run time and rejects with *Forbidden. Direct reads see this transaction's
// writes; scan-shaped reads after a same-batch write to their domain reject
// with *ReadAfterWrite and poison the transaction. Methods called after the
// callback returns reject with a *TypeError.
type Tx struct {
	session              *Session
	invoker              invoker
	ctx                  context.Context
	writes               []Write
	touched              map[string]touchedDoc
	touchedOrder         []string
	membrane             *Membrane
	createdTasks         map[Id]Task
	createdEntries       map[Id]Entry
	createdConversations map[Id]Conversation
	inputsById           map[Id]Input
	inputsByRequest      map[string]Input
	namespaceViews       map[*Namespace]*Node
	changedConfig        map[Id][]string
	changedConfigOrder   []Id
	wroteEntries         map[Id]bool
	wroteTasks           bool
	poisoned             error
	surfaceActive        bool
	closing              bool
	changes              CommitChanges
}

func newTx(session *Session, authority invoker, ctx context.Context) *Tx {
	return &Tx{
		session: session, invoker: authority, ctx: ctx,
		touched: map[string]touchedDoc{}, membrane: NewMembrane("tx"),
		createdTasks: map[Id]Task{}, createdEntries: map[Id]Entry{}, createdConversations: map[Id]Conversation{},
		inputsById: map[Id]Input{}, inputsByRequest: map[string]Input{}, namespaceViews: map[*Namespace]*Node{},
		changedConfig: map[Id][]string{}, wroteEntries: map[Id]bool{}, surfaceActive: true,
	}
}

func (tx *Tx) closeSurface() { tx.surfaceActive = false }

func (tx *Tx) surface() error {
	if !tx.surfaceActive {
		return &TypeError{Message: "transaction used outside its callback"}
	}
	return nil
}

func (tx *Tx) isCore() bool {
	return tx.invoker.kind == invokerKernel || (tx.invoker.kind == invokerTask && tx.invoker.core)
}

func (tx *Tx) assertCore(what string) error {
	if err := tx.surface(); err != nil {
		return err
	}
	if !tx.isCore() {
		return forbidden("%s: core turn machinery only", what)
	}
	return nil
}

func (tx *Tx) assertTask(what string) error {
	if err := tx.surface(); err != nil {
		return err
	}
	if tx.invoker.kind != invokerTask {
		return forbidden("%s outside a task", what)
	}
	return nil
}

func (tx *Tx) invokerTask() (Task, bool) {
	if task, ok := tx.createdTasks[tx.invoker.id]; ok {
		return task, true
	}
	return tx.session.liveTask(tx.invoker.id)
}

// inScope reports whether an ordinary task may touch conversationId: its own
// conversation and the subtree it owns. The host and core may touch anything.
func (tx *Tx) inScope(conversationId Id) bool {
	if tx.invoker.kind != invokerTask || tx.invoker.core {
		return true
	}
	if conversationId == *tx.invoker.conversationId {
		return true
	}
	if _, created := tx.createdConversations[conversationId]; created {
		return true
	}
	task, _ := tx.invokerTask()
	for _, root := range task.Owns {
		if tx.session.subtree(root)[conversationId] {
			return true
		}
	}
	return false
}

func (tx *Tx) assertScope(conversationId Id, what string) error {
	if !tx.inScope(conversationId) {
		return forbidden("%s: conversation %d is outside this task's subtree", what, conversationId)
	}
	return nil
}

func (tx *Tx) assertEntryScope(entry Entry, what string) error {
	if tx.inScope(entry.ConversationId) {
		return nil
	}
	candidates := map[Id]bool{*tx.invoker.conversationId: true}
	for id := range tx.createdConversations {
		candidates[id] = true
	}
	task, _ := tx.invokerTask()
	for _, root := range task.Owns {
		for id := range tx.session.subtree(root) {
			candidates[id] = true
		}
	}
	for candidate := range candidates {
		if tx.forkSees(candidate, entry) {
			return nil
		}
	}
	return forbidden("%s: entry %d is not visible from this task's subtree", what, entry.Id)
}

// forkSees reports whether conversation candidate inherits entry through its
// fork chain.
func (tx *Tx) forkSees(candidate Id, entry Entry) bool {
	conversation, ok := tx.createdConversations[candidate]
	if !ok {
		conversation, ok = tx.session.conversationRecord(candidate)
	}
	for ok && conversation.Parent != nil {
		if conversation.Parent.ConversationId == entry.ConversationId && entry.Id <= conversation.Parent.At {
			return true
		}
		conversation, ok = tx.session.conversationRecord(conversation.Parent.ConversationId)
	}
	return false
}

func (tx *Tx) poison(err error) error {
	if tx.poisoned == nil {
		tx.poisoned = err
	}
	return err
}

func (tx *Tx) assertNoEntryWrites(conversationId Id, read string) error {
	if tx.wroteEntries[conversationId] {
		return tx.poison(&ReadAfterWrite{Read: read, Write: "entry append"})
	}
	return nil
}

func (tx *Tx) assertNoTaskWrites(read string) error {
	if tx.wroteTasks {
		return tx.poison(&ReadAfterWrite{Read: read, Write: "task write"})
	}
	return nil
}

// Conversation reads a conversation, including one created in this batch.
func (tx *Tx) Conversation(id Id) (*Conversation, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertScope(id, "conversation"); err != nil {
		return nil, err
	}
	if conversation, ok := tx.createdConversations[id]; ok {
		return &conversation, nil
	}
	return tx.session.storage.Conversation(tx.ctx, id)
}

// Entry reads an entry, including one appended in this batch.
func (tx *Tx) Entry(id Id) (*Entry, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	entry, ok := tx.createdEntries[id]
	if !ok {
		found, err := tx.session.storage.Entries(tx.ctx, []Id{id})
		if err != nil {
			return nil, err
		}
		if entry, ok = found[id]; !ok {
			return nil, nil
		}
	}
	if err := tx.assertEntryScope(entry, "entry"); err != nil {
		return nil, err
	}
	return &entry, nil
}

// EntryOf reads an entry and narrows it to kind; nil when the kind differs.
func (tx *Tx) EntryOf(kind *EntryKind, id Id) (*Entry, error) {
	entry, err := tx.Entry(id)
	if err != nil || !kind.Is(entry) {
		return nil, err
	}
	return entry, nil
}

// Entries reads entries by id.
func (tx *Tx) Entries(ids []Id) (map[Id]Entry, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	var stored []Id
	for _, id := range ids {
		if _, created := tx.createdEntries[id]; !created {
			stored = append(stored, id)
		}
	}
	out, err := tx.session.storage.Entries(tx.ctx, stored)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if entry, ok := tx.createdEntries[id]; ok {
			out[id] = entry
		}
	}
	for _, entry := range out {
		if err := tx.assertEntryScope(entry, "entries"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// NewestOptions filter NewestEntry.
type NewestOptions struct {
	Kind     string
	WithHead bool
}

// NewestEntry returns the newest fork-visible matching entry.
func (tx *Tx) NewestEntry(conversationId Id, options NewestOptions) (*Entry, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertScope(conversationId, "newestEntry"); err != nil {
		return nil, err
	}
	if err := tx.assertNoEntryWrites(conversationId, "newestEntry"); err != nil {
		return nil, err
	}
	found, err := tx.session.storage.ScanEntries(tx.ctx, EntryScan{ConversationId: conversationId, Kind: options.Kind, WithHead: options.WithHead, Limit: 1})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return &found[0], nil
}

// ScanEntries scans fork-visible entries newest-first.
func (tx *Tx) ScanEntries(scan EntryScan) ([]Entry, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertScope(scan.ConversationId, "scanEntries"); err != nil {
		return nil, err
	}
	if err := tx.assertNoEntryWrites(scan.ConversationId, "scanEntries"); err != nil {
		return nil, err
	}
	return tx.session.storage.ScanEntries(tx.ctx, scan)
}

// Context derives model context at entry at (nil: the tip).
func (tx *Tx) Context(conversationId Id, at *Id) (ContextView, error) {
	if err := tx.surface(); err != nil {
		return ContextView{}, err
	}
	if err := tx.assertScope(conversationId, "context"); err != nil {
		return ContextView{}, err
	}
	if err := tx.assertNoEntryWrites(conversationId, "context"); err != nil {
		return ContextView{}, err
	}
	return deriveContext(tx.ctx, tx.session.storage, conversationId, at)
}

// Task reads a task, including this batch's creations and patches.
func (tx *Tx) Task(id Id) (*Task, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	task, ok := tx.createdTasks[id]
	if !ok {
		task, ok = tx.session.liveTask(id)
	}
	if !ok {
		stored, err := tx.session.storage.Task(tx.ctx, id)
		if err != nil || stored == nil {
			return nil, err
		}
		task = *stored
	}
	if err := tx.assertScope(task.ConversationId, "task"); err != nil {
		return nil, err
	}
	clone := task.clone()
	return &clone, nil
}

// Tasks scans tasks; it rejects after a task write in this batch.
func (tx *Tx) Tasks(scan TaskScan) ([]Task, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertNoTaskWrites("tasks"); err != nil {
		return nil, err
	}
	if scan.ConversationId != nil {
		if err := tx.assertScope(*scan.ConversationId, "tasks"); err != nil {
			return nil, err
		}
	}
	rows, err := tx.session.storage.ScanTasks(tx.ctx, TaskScan{ConversationId: scan.ConversationId, Kind: scan.Kind})
	if err != nil {
		return nil, err
	}
	seen := map[Id]bool{}
	for _, row := range rows {
		seen[row.Id] = true
	}
	for _, live := range tx.session.liveTaskList() {
		if !seen[live.Id] && taskScanMatches(TaskScan{ConversationId: scan.ConversationId, Kind: scan.Kind}, live) {
			rows = append(rows, live)
		}
	}
	return slices.DeleteFunc(rows, func(task Task) bool {
		return scan.Status != nil && !slices.Contains(scan.Status, task.Status)
	}), nil
}

// Input reads an input, including this batch's writes.
func (tx *Tx) Input(id Id) (*Input, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	return tx.input(id)
}

func (tx *Tx) input(id Id) (*Input, error) {
	input, ok := tx.inputsById[id]
	if !ok {
		stored, err := tx.session.storage.Input(tx.ctx, id)
		if err != nil || stored == nil {
			return nil, err
		}
		input = *stored
	}
	if err := tx.assertScope(input.ConversationId, "input"); err != nil {
		return nil, err
	}
	return &input, nil
}

func (tx *Tx) inputByRequest(conversationId Id, requestId string) (*Input, error) {
	if err := tx.assertScope(conversationId, "inputByRequest"); err != nil {
		return nil, err
	}
	if input, ok := tx.inputsByRequest[requestKey(conversationId, requestId)]; ok {
		return &input, nil
	}
	return tx.session.storage.InputByRequest(tx.ctx, conversationId, requestId)
}

// RewindableAsOf returns the rewindable state as of the commit containing at.
func (tx *Tx) RewindableAsOf(conversationId, at Id) (JsonObject, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertScope(conversationId, "rewindableAsOf"); err != nil {
		return nil, err
	}
	return tx.session.storage.DocAsOf(tx.ctx, conversationId, at)
}

// Snapshot returns a plain copy of a document with declared defaults filled.
func (tx *Tx) Snapshot(ref DocRef) (JsonObject, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if ref.Doc != DocSession {
		if err := tx.assertScope(ref.ConversationId, "snapshot"); err != nil {
			return nil, err
		}
	}
	tracker, err := tx.doc(ref)
	if err != nil {
		return nil, err
	}
	value := cloneObject(tracker.State())
	if ref.Doc == DocSession {
		return value, nil
	}
	tx.session.defaults.fill(ref.Doc, value)
	if tx.invoker.kind == invokerTask {
		for _, declaration := range tx.invoker.taskKind.Config.forDoc(ref.Doc) {
			if _, present := value[declaration.Key]; !present && !declaration.Optional {
				value[declaration.Key] = mustStored(declaration.Default)
			}
		}
	}
	return value, nil
}

func (tx *Tx) doc(ref DocRef) (*Tracker, error) {
	key := ref.key()
	if hit, ok := tx.touched[key]; ok {
		return hit.tracker, nil
	}
	if cached := tx.session.docs.peek(ref); cached != nil {
		tx.touch(ref, cached)
		return cached, nil
	}
	return nil, fmt.Errorf("document %s not loaded; pass it in commit({ docs })", key)
}

func (tx *Tx) touch(ref DocRef, tracker *Tracker) {
	key := ref.key()
	if _, ok := tx.touched[key]; !ok {
		tx.touchedOrder = append(tx.touchedOrder, key)
	}
	tx.touched[key] = touchedDoc{ref: ref, tracker: tracker}
}

func (tx *Tx) preload(refs []DocRef) error {
	for _, ref := range refs {
		if _, ok := tx.touched[ref.key()]; ok {
			continue
		}
		tracker, err := tx.session.docs.get(tx.ctx, ref)
		if err != nil {
			return err
		}
		tx.touch(ref, tracker)
	}
	return nil
}

func (tx *Tx) view(ref DocRef) (*Node, error) {
	tracker, err := tx.doc(ref)
	if err != nil {
		return nil, err
	}
	return tx.membrane.Wrap(tracker.State()), nil
}

// raw returns a document's live state for internal kernel writes.
func (tx *Tx) raw(ref DocRef) (JsonObject, error) {
	tracker, err := tx.doc(ref)
	if err != nil {
		return nil, err
	}
	return tracker.State(), nil
}

// Rewindable returns the rewindable document (core only).
func (tx *Tx) Rewindable(conversationId Id) (*Node, error) {
	if err := tx.assertCore("rewindable document"); err != nil {
		return nil, err
	}
	return tx.view(RewindableDoc(conversationId))
}

// Sticky returns the sticky document (core only).
func (tx *Tx) Sticky(conversationId Id) (*Node, error) {
	if err := tx.assertCore("sticky document"); err != nil {
		return nil, err
	}
	return tx.view(StickyDoc(conversationId))
}

// Session returns the session document (core only).
func (tx *Tx) Session() (*Node, error) {
	if err := tx.assertCore("session document"); err != nil {
		return nil, err
	}
	return tx.view(SessionDoc())
}

// Plugins returns a namespace's merged, non-extensible slice view.
func (tx *Tx) Plugins(namespace *Namespace) (*Node, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	registration := tx.session.namespaces.get(namespace.Id)
	if registration == nil || registration.token != namespace {
		return nil, forbidden(`namespace "%s" is stale`, namespace.Id)
	}
	if cached, ok := tx.namespaceViews[namespace]; ok {
		return cached, nil
	}
	conversationId, err := tx.invocationConversationId(fmt.Sprintf("plugins(%s)", namespace.Id))
	if err != nil {
		return nil, err
	}
	fields := map[string]JsonObject{}
	for _, key := range registration.order {
		doc := registration.routes[key]
		ref := DocRef{Doc: doc, ConversationId: conversationId}
		document, err := tx.raw(ref)
		if err != nil {
			return nil, err
		}
		slice := pluginSlice(document, namespace.Id)
		if defaultValue, ok := registration.defaults[doc][key]; ok {
			if _, present := slice[key]; !present {
				slice[key] = cloneJSON(defaultValue)
			}
		}
		fields[key] = slice
	}
	view := tx.membrane.view(fields)
	tx.namespaceViews[namespace] = view
	return view, nil
}

func pluginSlice(document JsonObject, id string) JsonObject {
	plugins, ok := document["plugins"].(map[string]any)
	if !ok {
		plugins = JsonObject{}
		document["plugins"] = plugins
	}
	slice, ok := plugins[id].(map[string]any)
	if !ok {
		slice = JsonObject{}
		plugins[id] = slice
	}
	return slice
}

var pluginEventName = regexp.MustCompile(`(?i)^[a-z][a-z0-9_.-]*$`)

// Emit emits a namespaced plugin event in this commit's envelope.
func (tx *Tx) Emit(namespace *Namespace, name string, data JsonValue) error {
	if err := tx.surface(); err != nil {
		return err
	}
	conversationId, err := tx.invocationConversationId("emit")
	if err != nil {
		return err
	}
	registration := tx.session.namespaces.get(namespace.Id)
	if registration == nil || registration.token != namespace {
		return forbidden(`namespace "%s" is stale`, namespace.Id)
	}
	if !pluginEventName.MatchString(name) {
		return fmt.Errorf("invalid plugin event")
	}
	stored, err := ToStored(data)
	if err != nil {
		return err
	}
	tx.addEvent(conversationId, ViewEvent{"type": fmt.Sprintf("plugin.%s.%s", namespace.Id, name), "data": stored})
	return nil
}

// EmitEvent emits a core view event (core only).
func (tx *Tx) EmitEvent(event ViewEvent) error {
	conversationId, err := tx.invocationConversationId("emit")
	if err != nil {
		return err
	}
	if err := tx.assertCore("core event"); err != nil {
		return err
	}
	tx.addEvent(conversationId, storedObject(event))
	return nil
}

func (tx *Tx) addEvent(conversationId Id, event ViewEvent) {
	tx.changes.Events = append(tx.changes.Events, ScopedEvent{ConversationId: conversationId, Event: event})
}

func (tx *Tx) invocationConversationId(what string) (Id, error) {
	if tx.invoker.conversationId == nil {
		return 0, forbidden("%s: no conversation is bound to this transaction", what)
	}
	conversationId := *tx.invoker.conversationId
	return conversationId, tx.assertScope(conversationId, what)
}

// ConfigAccess reads and writes one conversation's configuration.
type ConfigAccess struct {
	tx             *Tx
	conversationId Id
}

// Config returns configuration access for a conversation.
func (tx *Tx) Config(conversationId Id) (*ConfigAccess, error) {
	if err := tx.surface(); err != nil {
		return nil, err
	}
	if err := tx.assertScope(conversationId, "config"); err != nil {
		return nil, err
	}
	return &ConfigAccess{tx: tx, conversationId: conversationId}, nil
}

func (access *ConfigAccess) definition(key string) (string, JsonValue, bool, error) {
	tx := access.tx
	if tx.invoker.kind == invokerTask {
		for _, doc := range []string{DocRewindable, DocSticky} {
			if declaration, ok := tx.invoker.taskKind.Config.declared(doc, key); ok {
				return doc, declaration.Default, !declaration.Optional, nil
			}
		}
	}
	doc := tx.session.defaults.routeOf(key)
	if doc == "" {
		return "", nil, false, fmt.Errorf(`unknown config key "%s"`, key)
	}
	fallback, ok := tx.session.defaults.defaultOf(doc, key)
	return doc, fallback, ok, nil
}

// Get returns a key's value, or its declared default when unset.
func (access *ConfigAccess) Get(key string) (JsonValue, error) {
	if err := access.tx.surface(); err != nil {
		return nil, err
	}
	doc, fallback, hasFallback, err := access.definition(key)
	if err != nil {
		return nil, err
	}
	tracker, err := access.tx.doc(DocRef{Doc: doc, ConversationId: access.conversationId})
	if err != nil {
		return nil, err
	}
	if value, present := tracker.State()[key]; present {
		return cloneJSON(value), nil
	}
	if !hasFallback {
		return nil, nil
	}
	return mustStored(fallback), nil
}

func (access *ConfigAccess) assertWritable(key string) error {
	if access.tx.invoker.kind == invokerTask && !access.tx.invoker.core {
		return forbidden("config(%s): ordinary tasks cannot write config", key)
	}
	return nil
}

// Set validates and stores a key (host and core only).
func (access *ConfigAccess) Set(key string, value JsonValue) error {
	if err := access.tx.surface(); err != nil {
		return err
	}
	if err := access.assertWritable(key); err != nil {
		return err
	}
	doc, _, _, err := access.definition(key)
	if err != nil {
		return err
	}
	stored, storedErr := ToStored(value)
	if storedErr != nil || !access.tx.session.defaults.validate(key, stored) {
		return &TypeError{Message: fmt.Sprintf(`invalid config value for "%s"`, key)}
	}
	document, err := access.tx.raw(DocRef{Doc: doc, ConversationId: access.conversationId})
	if err != nil {
		return err
	}
	document[key] = stored
	access.tx.noteConfigChange(access.conversationId, key)
	return nil
}

// Reset deletes a stored override (host and core only).
func (access *ConfigAccess) Reset(key string) error {
	if err := access.tx.surface(); err != nil {
		return err
	}
	if err := access.assertWritable(key); err != nil {
		return err
	}
	doc, _, _, err := access.definition(key)
	if err != nil {
		return err
	}
	document, err := access.tx.raw(DocRef{Doc: doc, ConversationId: access.conversationId})
	if err != nil {
		return err
	}
	delete(document, key)
	access.tx.noteConfigChange(access.conversationId, key)
	return nil
}

func (tx *Tx) noteConfigChange(conversationId Id, key string) {
	keys, seen := tx.changedConfig[conversationId]
	if !seen {
		tx.changedConfigOrder = append(tx.changedConfigOrder, conversationId)
	}
	if !slices.Contains(keys, key) {
		tx.changedConfig[conversationId] = append(keys, key)
	}
}

// Slot returns a task's live slot (sticky.tasks[id]). An ordinary task may
// only access its own slot.
func (tx *Tx) Slot(ref TaskRef) (*Node, error) {
	if err := tx.assertTask("slot"); err != nil {
		return nil, err
	}
	task, ok := tx.session.liveTask(ref.Id)
	if !ok {
		task, ok = tx.createdTasks[ref.Id]
	}
	if !ok {
		return nil, fmt.Errorf("task %d is not live", ref.Id)
	}
	if !tx.invoker.core && ref.Id != tx.invoker.id {
		return nil, forbidden("slot: another task's slot")
	}
	sticky, err := tx.view(StickyDoc(task.ConversationId))
	if err != nil {
		return nil, err
	}
	tasks, _ := sticky.Get("tasks").(*Node)
	key := fmt.Sprint(ref.Id)
	if !tasks.Has(key) {
		initial := JsonObject{}
		if ref.Kind != nil && ref.Kind.Slot != nil {
			initial = ref.Kind.Slot(task.Input)
		}
		tasks.Set(key, initial)
	}
	slot, _ := tasks.Get(key).(*Node)
	return slot, nil
}

// ToolSlot returns the sticky turn tool slot at index (core only).
func (tx *Tx) ToolSlot(conversationId Id, index int) (*Node, error) {
	if err := tx.assertCore("toolSlot"); err != nil {
		return nil, err
	}
	sticky, err := tx.view(StickyDoc(conversationId))
	if err != nil {
		return nil, err
	}
	turn, _ := sticky.Get("turn").(*Node)
	tools, _ := turn.Get("tools").(*Node)
	slot, ok := tools.Index(index).(*Node)
	if !ok {
		return nil, fmt.Errorf("no tool slot at index %d", index)
	}
	return slot, nil
}

// entryKindReserved reports whether an ordinary writer may not use kind.
func entryKindReserved(kind string) bool {
	return strings.HasPrefix(kind, "pi.") && kind != "pi.notice"
}

// plain round-trips a value through JSON.
func plain[T any](value T) (T, error) {
	var out T
	encoded, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(encoded, &out)
	return out, err
}
