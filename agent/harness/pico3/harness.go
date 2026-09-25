package pico3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// RootSpec seeds the root conversation's documents.
type RootSpec struct {
	Rewindable JsonObject
	Sticky     JsonObject
}

// HarnessOptions configure a Harness.
type HarnessOptions struct {
	Models Models
	Tools  []*ToolDeclaration
	// TaskKinds are ordinary kinds; their config keys must be disjoint.
	TaskKinds   []*Kind
	Sections    []*SystemSection
	Plugins     map[string]PluginHandler
	ProcessHost ProcessHost
	// Now is the clock for durable kernel timestamps and retry scheduling.
	Now  func() float64
	Root *RootSpec
	// OnReport receives errors from listeners, hooks, watches, and the
	// scheduler. They are never delivered as commit failures.
	OnReport func(error)

	clock clock
}

// BuiltinKinds are the fixed core kinds and the two built-in ordinary kinds.
type BuiltinKinds struct {
	Generation *Kind
	Tool       *Kind
	PostTools  *Kind
	Collapse   *Kind
	Job        *Kind
	Plugin     *Kind
}

func (builtins BuiltinKinds) list() []*Kind {
	return []*Kind{builtins.Generation, builtins.Tool, builtins.PostTools, builtins.Collapse, builtins.Job, builtins.Plugin}
}

// Harness is the Pico3 kernel over one Storage.
type Harness struct {
	session     *Session
	scheduler   *scheduler
	views       *viewManager
	kinds       *kindRegistry
	namespaces  *namespaceRegistry
	hooks       hookRegistry
	onReport    func(error)
	now         func() float64
	clock       clock
	ctx         context.Context
	options     HarnessOptions
	models      Models
	processHost ProcessHost
	plugins     map[string]PluginHandler

	registryMu       sync.Mutex
	tools            map[string]*ToolDeclaration
	sections         map[string]*SystemSection
	entryKinds       map[string]*EntryKind
	toolsRevision    int
	sectionsRevision int

	lifecycleMu           sync.Mutex
	resumed               bool
	suspended             bool
	conversationListeners map[*conversationListener]bool
	resumeDone            chan struct{}
}

type conversationListener struct {
	listener func(*ConversationHandle)
}

// OpenHarness opens a harness over storage and creates the root
// conversation when the storage is empty.
func OpenHarness(ctx context.Context, storage Storage, options HarnessOptions) (*Harness, error) {
	h, err := newHarness(ctx, storage, options)
	if err != nil {
		return nil, err
	}
	if err := h.init(ctx); err != nil {
		return nil, err
	}
	return h, nil
}

func newHarness(ctx context.Context, storage Storage, options HarnessOptions) (*Harness, error) {
	h := &Harness{
		ctx: ctx, options: options, models: options.Models, processHost: options.ProcessHost,
		kinds: newKindRegistry(), namespaces: newNamespaceRegistry(),
		tools: map[string]*ToolDeclaration{}, sections: map[string]*SystemSection{}, entryKinds: map[string]*EntryKind{},
		conversationListeners: map[*conversationListener]bool{}, plugins: maps.Clone(options.Plugins),
	}
	h.clock = options.clock
	if h.clock == nil {
		h.clock = wallClock{}
	}
	h.now = options.Now
	if h.now == nil {
		h.now = h.clock.now
	}
	h.onReport = func(err error) {
		defer func() { _ = recover() }() // upstream: agent/src/harness/pico3/harness.ts:onReport
		if options.OnReport != nil {
			options.OnReport(err)
		}
	}
	if err := h.registerInitial(options); err != nil {
		return nil, err
	}
	session, err := newSession(storage, h.kinds, h.namespaces, h.now)
	if err != nil {
		return nil, err
	}
	h.session = session
	session.onReport = h.onReport
	h.views = newViewManager(session, h.onReport)
	session.addLineListener(h.views.update)
	session.addListener(func(result CommitResult) {
		h.views.deliver()
		for _, conversation := range result.Changes.Conversations {
			h.notifyConversation(conversation)
		}
	})
	h.scheduler = newScheduler(session, h.kinds, h.runtimeFor, h.onReport, ctx)
	return h, nil
}

func (h *Harness) registerInitial(options HarnessOptions) error {
	for _, kind := range Kinds.list() {
		h.kinds.set(kind)
	}
	for _, kind := range options.TaskKinds {
		if strings.HasPrefix(kind.Name, "pi.") {
			return fmt.Errorf(`task kind "%s": names beginning with "pi." are reserved`, kind.Name)
		}
		if h.kinds.get(kind.Name) != nil {
			return fmt.Errorf(`task kind "%s" registered twice`, kind.Name)
		}
		h.kinds.set(kind)
	}
	for _, tool := range options.Tools {
		if _, err := h.RegisterTool(tool); err != nil {
			return err
		}
	}
	for _, section := range []*SystemSection{SystemSections.Identity, SystemSections.Environment, SystemSections.Skills} {
		h.sections[section.Key] = section
	}
	for _, section := range options.Sections {
		if _, err := h.RegisterSection(section); err != nil {
			return err
		}
	}
	for _, kind := range Entries.list() {
		h.entryKinds[kind.Kind] = kind
	}
	return nil
}

func (h *Harness) runtimeFor(task Task, authority invoker, _ context.Context) *Runtime {
	return &Runtime{
		TaskId: task.Id, ConversationId: task.ConversationId, Kind: authority.taskKind,
		Hooks:       h.hooks.runnerFor(authority.taskKind, HookApi{TaskId: task.Id, ConversationId: task.ConversationId}, h.session.ancestors, h.onReport),
		Models:      h.models,
		Registries:  Registries{Sections: SectionRegistry{Map: h.sectionMap, Revision: h.sectionRevision}, Tools: ToolRegistry{Map: h.toolMap, Revision: h.toolRevision}},
		ProcessHost: h.processHost, Plugins: h.plugins, harness: h, authority: authority,
	}
}

func (h *Harness) toolMap() map[string]*ToolDeclaration {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	return maps.Clone(h.tools)
}

func (h *Harness) sectionMap() map[string]*SystemSection {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	return maps.Clone(h.sections)
}

func (h *Harness) toolRevision() int {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	return h.toolsRevision
}

func (h *Harness) sectionRevision() int {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	return h.sectionsRevision
}

func (h *Harness) notifyConversation(conversation Conversation) {
	h.lifecycleMu.Lock()
	listeners := make([]*conversationListener, 0, len(h.conversationListeners))
	for listener := range h.conversationListeners {
		listeners = append(listeners, listener)
	}
	h.lifecycleMu.Unlock()
	if len(listeners) == 0 {
		return
	}
	handle := h.handle(conversation)
	for _, listener := range listeners {
		h.callConversationListener(listener, handle)
	}
}

func (h *Harness) callConversationListener(listener *conversationListener, handle *ConversationHandle) {
	defer func() {
		if recovered := recover(); recovered != nil {
			h.onReport(fmt.Errorf("%v", recovered))
		}
	}()
	listener.listener(handle)
}

func (h *Harness) assertInvocation(authority invoker) error {
	if !authority.token.Alive() {
		return forbidden("operation from a finished invocation")
	}
	live, ok := h.session.liveTask(authority.id)
	if !ok {
		return forbidden("operation from a task that is not live")
	}
	if authority.mode == "run" && live.Abort {
		return forbidden("operation from a marked run invocation")
	}
	return nil
}

func (h *Harness) assertTaskConversationScope(taskId, conversationId Id) error {
	task, ok := h.session.liveTask(taskId)
	if ok && task.ConversationId == conversationId {
		return nil
	}
	for _, root := range task.Owns {
		if h.session.subtree(root)[conversationId] {
			return nil
		}
	}
	return forbidden("conversation %d is outside task %d's subtree", conversationId, taskId)
}

func (h *Harness) assertOwnedConversation(taskId, conversationId Id) error {
	if task, ok := h.session.liveTask(taskId); ok {
		for _, root := range task.Owns {
			if h.session.subtree(root)[conversationId] {
				return nil
			}
		}
	}
	return forbidden("conversation %d is not owned by task %d", conversationId, taskId)
}

func (h *Harness) init(ctx context.Context) error {
	session := h.session
	value, err := session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.Conversations(lineCtx)
	})
	if err != nil {
		return err
	}
	conversations, _ := value.([]Conversation)
	live, err := session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.ScanTasks(lineCtx, TaskScan{Status: []string{TaskPending, TaskRunning}})
	})
	if err != nil {
		return err
	}
	session.stateMu.Lock()
	for _, conversation := range conversations {
		session.setConversationLocked(conversation)
	}
	for _, task := range live.([]Task) {
		session.liveTasks[task.Id] = task
	}
	session.stateMu.Unlock()
	if err := h.loadOwnerTasks(ctx, conversations); err != nil {
		return err
	}
	if len(conversations) > 0 {
		return nil
	}
	root := h.options.Root
	if root == nil {
		root = &RootSpec{}
	}
	_, err = session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		return tx.CreateConversation(ConversationSpec{Rewindable: root.Rewindable, Sticky: root.Sticky})
	}, commitOptions{})
	return err
}

// loadOwnerTasks caches terminal owner tasks that still own conversations,
// for ancestry after reopen.
func (h *Harness) loadOwnerTasks(ctx context.Context, conversations []Conversation) error {
	for _, conversation := range conversations {
		if conversation.Owner == nil {
			continue
		}
		id := *conversation.Owner
		if _, live := h.session.liveTask(id); live {
			continue
		}
		value, err := h.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
			return storage.Task(lineCtx, id)
		})
		if err != nil {
			return err
		}
		if task, ok := value.(*Task); ok && task != nil {
			h.session.stateMu.Lock()
			h.session.ownerTaskCache[id] = *task
			h.session.stateMu.Unlock()
		}
	}
	return nil
}

// Resume orphans live tasks of unknown kinds, then starts the scheduler.
func (h *Harness) Resume() error {
	h.lifecycleMu.Lock()
	if h.suspended {
		h.lifecycleMu.Unlock()
		return errors.New("cannot resume a suspended harness; reopen storage with a new harness")
	}
	if h.resumed {
		h.lifecycleMu.Unlock()
		return nil
	}
	h.resumed = true
	h.resumeDone = make(chan struct{})
	h.lifecycleMu.Unlock()
	go func() {
		defer close(h.resumeDone)
		if err := h.reconcileOrphans(); err != nil {
			h.onReport(err)
			return
		}
		h.lifecycleMu.Lock()
		suspended := h.suspended
		h.lifecycleMu.Unlock()
		if !suspended {
			h.scheduler.resume()
		}
	}()
	return nil
}

func (h *Harness) reconcileOrphans() error {
	var orphaned []Task
	var docs []DocRef
	seen := map[Id]bool{}
	for _, task := range h.session.liveTaskList() {
		if h.kinds.get(task.Kind) != nil {
			continue
		}
		orphaned = append(orphaned, task)
		if !seen[task.ConversationId] {
			seen[task.ConversationId] = true
			docs = append(docs, StickyDoc(task.ConversationId))
		}
	}
	if len(orphaned) == 0 {
		return nil
	}
	_, err := h.session.commit(h.ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		for _, task := range orphaned {
			task.Status = TaskTerminal
			task.Outcome = &Outcome{Status: OutcomeOrphaned}
			if err := tx.setTask(task); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, commitOptions{docs: docs})
	return err
}

// Quiescent reports whether no invocation is running.
func (h *Harness) Quiescent() bool { return h.scheduler.quiescent() }

// Hold pauses dispatch on a quiescent harness; commits continue.
func (h *Harness) Hold() (func(), error) {
	if !h.scheduler.quiescent() {
		return nil, errors.New("cannot hold a non-quiescent harness; suspend it instead")
	}
	return h.scheduler.hold(), nil
}

// Suspend cancels and joins invocations, clears transient waits, and closes
// storage without terminalizing tasks.
func (h *Harness) Suspend(ctx context.Context) error {
	h.lifecycleMu.Lock()
	if h.suspended {
		h.lifecycleMu.Unlock()
		return nil
	}
	h.suspended = true
	resumeDone := h.resumeDone
	h.lifecycleMu.Unlock()
	if resumeDone != nil {
		<-resumeDone
	}
	h.scheduler.joinAll()
	if err := h.clearWaitingTools(ctx); err != nil {
		return err
	}
	h.views.close()
	return h.session.close(ctx)
}

func (h *Harness) clearWaitingTools(ctx context.Context) error {
	var tools []Task
	var docs []DocRef
	seen := map[Id]bool{}
	for _, task := range h.session.liveTaskList() {
		if task.Kind != "pi.tool" {
			continue
		}
		tools = append(tools, task)
		if !seen[task.ConversationId] {
			seen[task.ConversationId] = true
			docs = append(docs, StickyDoc(task.ConversationId))
		}
	}
	if len(tools) == 0 {
		return nil
	}
	_, err := h.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		for _, task := range tools {
			index, _ := asID(asObject(task.Input)["index"])
			sticky, err := tx.raw(StickyDoc(task.ConversationId))
			if err != nil {
				return nil, err
			}
			tools := arr(obj(sticky, "turn"), "tools")
			if int(index) < len(tools) {
				delete(asObject(tools[index]), "waitingOn")
			}
		}
		return nil, nil
	}, commitOptions{docs: docs})
	return err
}

// RegisterTaskKind registers an ordinary kind at run time. The returned
// function unregisters exactly this kind; it is idempotent.
func (h *Harness) RegisterTaskKind(kind *Kind) (func(), error) {
	if strings.HasPrefix(kind.Name, "pi.") {
		return nil, fmt.Errorf(`task kind "%s": names beginning with "pi." are reserved`, kind.Name)
	}
	if h.kinds.get(kind.Name) != nil {
		return nil, fmt.Errorf(`task kind "%s" already registered`, kind.Name)
	}
	if err := h.session.defaults.register(kind); err != nil {
		return nil, err
	}
	h.kinds.set(kind)
	h.scheduler.kick()
	return func() {
		if h.kinds.remove(kind) {
			h.session.defaults.unregister(kind)
		}
	}, nil
}

// NamespaceDefaults are a namespace's defaults by document.
type NamespaceDefaults struct {
	Rewindable JsonObject
	Sticky     JsonObject
	Session    JsonObject
}

var namespaceName = regexp.MustCompile(`(?i)^[a-z][a-z0-9_.-]*$`)

// Namespace registers a durable namespace; View projects its merged slice
// into the public view.
func (h *Harness) Namespace(id string, defaults NamespaceDefaults, view func(slice JsonObject) (JsonValue, error)) (*Namespace, error) {
	if !namespaceName.MatchString(id) || strings.HasPrefix(id, "pi.") {
		return nil, fmt.Errorf(`invalid namespace "%s"`, id)
	}
	registration := &namespaceRegistration{defaults: map[string]JsonObject{}, routes: map[string]string{}, project: view}
	for _, doc := range []struct {
		name   string
		values JsonObject
	}{{DocRewindable, defaults.Rewindable}, {DocSticky, defaults.Sticky}, {DocSession, defaults.Session}} {
		stored := storedObject(doc.values)
		if stored == nil {
			stored = JsonObject{}
		}
		registration.defaults[doc.name] = stored
		for _, key := range sortedKeys(stored) {
			if _, taken := registration.routes[key]; taken {
				return nil, fmt.Errorf(`namespace "%s" key "%s" is declared in more than one document`, id, key)
			}
			registration.routes[key] = doc.name
			registration.order = append(registration.order, key)
		}
	}
	h.namespaces.mu.Lock()
	defer h.namespaces.mu.Unlock()
	if _, exists := h.namespaces.registrations[id]; exists {
		return nil, fmt.Errorf(`namespace "%s" already registered`, id)
	}
	token := &Namespace{Id: id}
	token.unregister = func() {
		h.namespaces.mu.Lock()
		current := h.namespaces.registrations[id]
		if current == nil || current.token != token {
			h.namespaces.mu.Unlock()
			return
		}
		delete(h.namespaces.registrations, id)
		h.namespaces.order = slices.DeleteFunc(h.namespaces.order, func(candidate string) bool { return candidate == id })
		h.namespaces.mu.Unlock()
		h.hooks.removeNamespace(token)
	}
	registration.token = token
	h.namespaces.registrations[id] = registration
	h.namespaces.order = append(h.namespaces.order, id)
	return token, nil
}

// RegisterTool registers a tool; unregister removes only this declaration.
func (h *Harness) RegisterTool(tool *ToolDeclaration) (func(), error) {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	if _, exists := h.tools[tool.Name]; exists {
		return nil, fmt.Errorf(`tool "%s" already registered`, tool.Name)
	}
	h.tools[tool.Name] = tool
	h.toolsRevision++
	return func() {
		h.registryMu.Lock()
		defer h.registryMu.Unlock()
		if h.tools[tool.Name] == tool {
			delete(h.tools, tool.Name)
			h.toolsRevision++
		}
	}, nil
}

// RegisterSection registers a system section.
func (h *Harness) RegisterSection(section *SystemSection) (func(), error) {
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	if _, exists := h.sections[section.Key]; exists {
		return nil, fmt.Errorf(`section "%s" already registered`, section.Key)
	}
	h.sections[section.Key] = section
	h.sectionsRevision++
	return func() {
		h.registryMu.Lock()
		defer h.registryMu.Unlock()
		if h.sections[section.Key] == section {
			delete(h.sections, section.Key)
			h.sectionsRevision++
		}
	}, nil
}

// RegisterEntryKind registers an application entry kind.
func (h *Harness) RegisterEntryKind(kind *EntryKind) (func(), error) {
	if strings.HasPrefix(kind.Kind, "pi.") {
		return nil, fmt.Errorf(`entry kind "%s": names beginning with "pi." are reserved`, kind.Kind)
	}
	h.registryMu.Lock()
	defer h.registryMu.Unlock()
	if _, exists := h.entryKinds[kind.Kind]; exists {
		return nil, fmt.Errorf(`entry kind "%s" already registered`, kind.Kind)
	}
	h.entryKinds[kind.Kind] = kind
	return func() {
		h.registryMu.Lock()
		defer h.registryMu.Unlock()
		if h.entryKinds[kind.Kind] == kind {
			delete(h.entryKinds, kind.Kind)
		}
	}, nil
}

// Hooks registers namespace-bound handlers for one kind, harness-wide.
func (h *Harness) Hooks(namespace *Namespace, kind *Kind, handlers any) (func(), error) {
	if err := h.checkNamespace(namespace); err != nil {
		return nil, err
	}
	if err := h.checkKind(kind); err != nil {
		return nil, err
	}
	return h.hooks.add(&hookRegistration{namespace: namespace, kind: kind, handlers: handlers}), nil
}

func (h *Harness) checkKind(kind *Kind) error {
	if h.kinds.get(kind.Name) != kind {
		return fmt.Errorf(`kind "%s" is not the registered token`, kind.Name)
	}
	return nil
}

func (h *Harness) checkNamespace(namespace *Namespace) error {
	if registration := h.namespaces.get(namespace.Id); registration == nil || registration.token != namespace {
		return forbidden(`namespace "%s" is stale`, namespace.Id)
	}
	return nil
}

// Root returns the root conversation.
func (h *Harness) Root(ctx context.Context) (*ConversationHandle, error) {
	handle, err := h.Conversation(ctx, 1)
	if err != nil {
		return nil, err
	}
	if handle == nil {
		return nil, errors.New("root conversation is missing")
	}
	return handle, nil
}

// OnConversation reports every loaded conversation now and each new one after
// its commit; it returns the unsubscribe function.
func (h *Harness) OnConversation(listener func(*ConversationHandle)) func() {
	registration := &conversationListener{listener: listener}
	h.lifecycleMu.Lock()
	h.conversationListeners[registration] = true
	h.lifecycleMu.Unlock()
	for _, conversation := range h.session.conversationList() {
		h.callConversationListener(registration, h.handle(conversation))
	}
	return func() {
		h.lifecycleMu.Lock()
		defer h.lifecycleMu.Unlock()
		delete(h.conversationListeners, registration)
	}
}

// Conversation returns a handle, or nil when the conversation is unknown.
func (h *Harness) Conversation(ctx context.Context, id Id) (*ConversationHandle, error) {
	value, err := h.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.Conversation(lineCtx, id)
	})
	if err != nil {
		return nil, err
	}
	conversation, _ := value.(*Conversation)
	if conversation == nil {
		return nil, nil
	}
	return h.handle(*conversation), nil
}

// CreateConversation creates a conversation and optionally sends input.
func (h *Harness) CreateConversation(ctx context.Context, spec ConversationSpec, input JsonValue) (*ConversationHandle, error) {
	result, err := h.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		id, err := tx.CreateConversation(spec)
		if err != nil {
			return nil, err
		}
		if input != nil {
			if _, err := tx.Send(id, SendInput{Content: input}); err != nil {
				return nil, err
			}
		}
		return id, nil
	}, commitOptions{})
	if err != nil {
		return nil, err
	}
	id, _ := result.Value.(Id)
	return h.Conversation(ctx, id)
}

// Entries scans entries.
func (h *Harness) Entries(ctx context.Context, scan EntryScan) ([]Entry, error) {
	value, err := h.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.ScanEntries(lineCtx, scan)
	})
	entries, _ := value.([]Entry)
	return entries, err
}

// GetTask reads a task.
func (h *Harness) GetTask(ctx context.Context, id Id) (*Task, error) {
	value, err := h.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
		return storage.Task(lineCtx, id)
	})
	task, _ := value.(*Task)
	return task, err
}

// AbortInput withdraws a queued input; with conversationId it must belong to
// that conversation.
func (h *Harness) AbortInput(ctx context.Context, id Id, conversationId *Id) (string, error) {
	if conversationId != nil {
		value, err := h.session.read(ctx, func(lineCtx context.Context, storage Storage) (any, error) {
			return storage.Input(lineCtx, id)
		})
		if err != nil {
			return "", err
		}
		if input, _ := value.(*Input); input != nil && input.ConversationId != *conversationId {
			return "", forbidden("input %d is outside conversation %d", id, *conversationId)
		}
	}
	return h.inputHandle(id).Abort(ctx)
}

// AbortTask marks, signals, and joins a task's run invocation.
func (h *Harness) AbortTask(ctx context.Context, id Id) (string, error) {
	return h.scheduler.abortTask(ctx, id)
}

// MarkTask durably marks a task for abort without signalling its invocation.
func (h *Harness) MarkTask(ctx context.Context, id Id) (string, error) {
	result, err := h.session.commit(ctx, kernelInvoker(nil), func(_ context.Context, tx *Tx, _ TransactionControl) (any, error) {
		task, err := tx.Task(id)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("task %d not found", id)
		}
		if task.Status == TaskTerminal {
			return "terminal", nil
		}
		return "marked", tx.MarkTask(id)
	}, commitOptions{})
	if err != nil {
		return "", err
	}
	status, _ := result.Value.(string)
	return status, nil
}

// WaitForIdle waits until no foreground task is live.
func (h *Harness) WaitForIdle(ctx context.Context) error { return h.scheduler.waitForIdle(ctx, nil) }

// WaitForTask waits until a task is terminal.
func (h *Harness) WaitForTask(ctx context.Context, id Id) (Task, error) {
	return h.scheduler.waitForTask(ctx, id)
}

// Close signals every invocation, waits for them, and closes storage. It
// writes nothing.
func (h *Harness) Close(ctx context.Context) error { return h.Suspend(ctx) }

// CaptureActiveTranscript returns H plus every fork-visible entry with id at
// or after H.head, chronologically; the whole transcript without a head.
func CaptureActiveTranscript(scan func(EntryScan) ([]Entry, error), conversationId Id) ([]Entry, error) {
	heads, err := scan(EntryScan{ConversationId: conversationId, WithHead: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	var from *Id
	if len(heads) > 0 {
		from = heads[0].Head
	}
	var out []Entry
	var before *Id
	for {
		page, err := scan(EntryScan{ConversationId: conversationId, Before: before, Limit: contextPage})
		if err != nil {
			return nil, err
		}
		done := len(page) < contextPage
		for _, entry := range page {
			if from != nil && entry.Id < *from {
				done = true
				break
			}
			out = append(out, entry)
		}
		if done {
			break
		}
		last := page[len(page)-1].Id
		before = &last
	}
	slices.Reverse(out)
	return out, nil
}
