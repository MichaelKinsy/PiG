// Ports packages/durable/src/harness/task-graph.ts.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// TaskGraphState is a live task's durable status without its checkpoint and outcome payloads (spec §9.5): pending or running with Phase, waiting with Phase, On, and Policy, sleeping with Phase and the Until, Signals it waits for, or completing with the held Outcome status.
type TaskGraphState struct {
	Status durable.TaskStatus `json:"status"`
	Phase  string             `json:"phase,omitempty"`
	On     []durable.TaskId   `json:"on,omitempty"`
	Policy durable.JoinPolicy `json:"policy,omitempty"`
	// Until is the time a sleeping task wakes at, when it has one: a sleep may wait for signals only. Nil when absent.
	Until *float64 `json:"until,omitempty"`
	// Signals are the signal keys a sleeping task wakes on, when it lists any; nil when absent.
	Signals []durable.SignalKey `json:"signals,omitempty"`
	// Outcome is the status of the outcome held until its ordinary owned work drains.
	Outcome durable.TaskOutcomeStatus `json:"outcome,omitempty"`
}

// MarshalJSON encodes the upstream variant of the state's Status.
func (state TaskGraphState) MarshalJSON() ([]byte, error) {
	var encoded *delta.JsonObject
	switch state.Status {
	case durable.TaskPending, durable.TaskRunning:
		encoded = delta.JsonObjectOf("status", state.Status, "phase", state.Phase)
	case durable.TaskSleeping:
		pairs := []any{"status", state.Status, "phase", state.Phase}
		if state.Until != nil {
			pairs = append(pairs, "until", *state.Until)
		}
		if state.Signals != nil {
			pairs = append(pairs, "signals", state.Signals)
		}
		encoded = delta.JsonObjectOf(pairs...)
	case durable.TaskWaiting:
		on := state.On
		if on == nil {
			on = []durable.TaskId{}
		}
		encoded = delta.JsonObjectOf("status", state.Status, "phase", state.Phase, "on", on, "policy", state.Policy)
	default:
		encoded = delta.JsonObjectOf("status", state.Status, "outcome", state.Outcome)
	}
	return json.Marshal(encoded)
}

// TaskGraphNode is one live task of the graph.
type TaskGraphNode struct {
	Id             durable.TaskId         `json:"id"`
	Kind           string                 `json:"kind"`
	ConversationId durable.ConversationId `json:"conversationId"`
	// Owner is the owner task; nil for a conversation-owned task.
	Owner          *durable.TaskId `json:"owner,omitempty"`
	Background     bool            `json:"background"`
	AbortRequested bool            `json:"abortRequested"`
	State          TaskGraphState  `json:"state"`
	// Conversations are the conversations this task owns, in ID order.
	Conversations []durable.ConversationId `json:"conversations"`
	// Blocked is why it cannot run here, when it cannot: no registered definition takes it (missing_task, task_too_old,
	// migration_failed). Follows registry changes. Empty when it can.
	Blocked durable.BlockedReason `json:"blocked,omitempty"`
}

// TaskGraph is every live task of the Session (spec §9.5).
type TaskGraph struct {
	// Tasks holds every live task, keyed by its decimal ID.
	Tasks map[string]TaskGraphNode `json:"tasks"`
}

// TaskGraphWatch is a serialized watch of the task graph.
type TaskGraphWatch = durable.WatchHandle[TaskGraph]

// taskGraphObserver is a chord.Watch of the graph or a guarded host observer (ObserveTaskGraph).
type taskGraphObserver interface {
	Advance(ctx context.Context, call chord.WatchCall[TaskGraph])
	End(end chord.WatchEnd)
}

// guardedGraphObserver adapts a session.GuardedObserver to the mount's observer interface.
type guardedGraphObserver struct {
	*session.GuardedObserver[TaskGraph]
}

// graphWatch adapts a chord.Watch to the mount's observer interface: a Watch ends with EndWith.
type graphWatch struct {
	*chord.Watch[TaskGraph]
}

func (observer graphWatch) End(end chord.WatchEnd) { observer.EndWith(end) }

type taskGraphDelivery struct {
	ctx       context.Context
	change    chord.WatchCall[TaskGraph]
	observers []taskGraphObserver
}

// taskGraphMount holds one mounted task graph shared between the Session's commit follower (writer goroutine)
// and Session-line jobs (attach, refreshBlocked).
//
// Lock order:
//   - Session line -> mount.deliverMu -> mount.mu
//   - mount.deliverMu -> view.mu (via observer Detach or panic inside Advance)
//   - s.mu (inside blocking.Blocked) is never taken while holding mount.mu, view.mu, or mount.deliverMu
//   - view.mu is never held across mount.mu or mount.deliverMu
//   - mount.mu is never held across view.mu, blocking.Blocked, or observer.Advance
type taskGraphMount struct {
	// mu guards value, records, pending, and refreshes.
	mu    sync.Mutex
	value TaskGraph
	// observers is guarded by TaskGraphView.mu.
	observers []taskGraphObserver
	// records is what the blocked reason reads of each live task, so a registry change can derive it again without
	// payloads.
	records   map[string]BlockedSubject
	pending   []taskGraphDelivery
	refreshes uint64
	// deliverMu serializes observer.Advance calls as a leaf lock so deliveries stay synchronous and ordered.
	// Contract (durable/types.go): "Advance must only record the call, synchronously and quickly, never wait, call the Session, or panic."
	deliverMu sync.Mutex
}

// TaskGraphBlocking is what the graph asks the scheduler: a task's blocked reason, and when reasons may change without a
// commit.
type TaskGraphBlocking struct {
	Blocked          func(record BlockedSubject) durable.BlockedReason
	SubscribeBlocked func(listener func()) func()
}

var taskGraphLiveStatuses = []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskWaiting, durable.TaskSleeping, durable.TaskCompleting}

const taskGraphScanPageSize = 256

// TaskGraphView is the Harness's task graph mount: built on the Session line by its first observer and dropped with its last. It advances from the Session's commit publications as memory adopts them.
type TaskGraphView struct {
	session  *session.SessionImpl
	blocking TaskGraphBlocking
	// mu guards mount, closed, and mount.observers: a watch may detach off the line, from its delivery goroutine.
	mu     sync.Mutex
	mount  *taskGraphMount
	closed bool
}

// NewTaskGraphView subscribes the mount to the Session's commits and close.
func NewTaskGraphView(sessionImpl *session.SessionImpl, blocking TaskGraphBlocking) *TaskGraphView {
	view := &TaskGraphView{session: sessionImpl, blocking: blocking}
	sessionImpl.FollowCommits(func(ctx context.Context, publication session.ObservedCommit) {
		// A commit held in memory changes documents only, which the graph does not show.
		if publication.Memory {
			return
		}
		view.mu.Lock()
		mount := view.mount
		var observers []taskGraphObserver
		if mount != nil {
			observers = slices.Clone(mount.observers)
		}
		view.mu.Unlock()
		if mount != nil {
			advanceTaskGraph(ctx, mount, observers, publication, blocking.Blocked, view.session.Report)
		}
	})
	blocking.SubscribeBlocked(view.refreshBlocked)
	sessionImpl.SubscribeClose(func() {
		watchEnd, observerEnd := session.CloseEnds(view.session.Failure())
		view.mu.Lock()
		view.closed = true
		var observers []taskGraphObserver
		if view.mount != nil {
			observers = slices.Clone(view.mount.observers)
		}
		view.mount = nil
		view.mu.Unlock()
		for _, observer := range observers {
			end := observerEnd
			if _, follows := observer.(graphWatch); follows {
				end = watchEnd
			}
			session.Contained(func() { observer.End(end) }, view.session.Report)
		}
	})
	return view
}

// Observe attaches a synchronous observer of the graph (Harness.ObserveTaskGraph), behind session.GuardObserver; ctx governs
// only the attachment.
func (view *TaskGraphView) Observe(ctx context.Context, observer durable.Observer[TaskGraph]) (durable.Observation[TaskGraph], error) {
	var value TaskGraph
	var guarded *session.GuardedObserver[TaskGraph]
	detach, err := view.attach(ctx, func(current TaskGraph, release func()) taskGraphObserver {
		value = current
		guarded = session.GuardObserver(observer, release, view.session.Report)
		return guardedGraphObserver{guarded}
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		detach()
		return nil, context.Cause(ctx)
	}
	return session.NewObservation(value, guarded), nil
}

// Watch returns a serialized watch of the graph, an observer of its mount; cancelling ctx stops it.
func (view *TaskGraphView) Watch(ctx context.Context) (TaskGraphWatch, error) {
	var watch *chord.Watch[TaskGraph]
	if _, err := view.attach(ctx, func(value TaskGraph, release func()) taskGraphObserver {
		watch = session.NewSessionWatch(view.session, value, release)
		return graphWatch{watch}
	}); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		watch.Abort()
		return nil, context.Cause(ctx)
	}
	if ctx.Done() != nil {
		watch.ObserveAbort(ctx)
	}
	return watch, nil
}

// attach registers an observer created from the current revision, atomically on the Session line.
func (view *TaskGraphView) attach(ctx context.Context, create func(value TaskGraph, release func()) taskGraphObserver) (func(), error) {
	result, err := view.session.ReadOnLineCtx(ctx, func(storage durable.Storage) (any, error) {
		view.mu.Lock()
		mount := view.mount
		view.mu.Unlock()
		var initial TaskGraph
		if mount == nil {
			built, err := view.build(ctx, storage)
			if err != nil {
				return nil, err
			}
			mount = built
			initial = mount.value
		} else {
			mount.mu.Lock()
			initial = mount.value
			mount.mu.Unlock()
		}
		var observer taskGraphObserver
		detach := func() {
			view.mu.Lock()
			defer view.mu.Unlock()
			mount.observers = slices.DeleteFunc(mount.observers, func(candidate taskGraphObserver) bool { return candidate == observer })
			if len(mount.observers) == 0 && view.mount == mount {
				view.mount = nil
			}
		}
		observer = create(initial, detach)
		view.mu.Lock()
		defer view.mu.Unlock()
		// Close or cancellation may begin while the mount builds; register nothing then.
		if view.closed {
			return nil, closedError(view.session)
		}
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		view.mount = mount
		mount.observers = append(mount.observers, observer)
		return detach, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(func()), nil
}

// refreshBlocked derives every node's blocked reason again on the Session line, and publishes the ones that changed.
func (view *TaskGraphView) refreshBlocked() {
	view.mu.Lock()
	skip := view.mount == nil || view.closed
	view.mu.Unlock()
	if skip {
		return
	}
	// Rejected only once the Session is closing or failed.
	_, _ = view.session.ReadOnLine(func(durable.Storage) (any, error) {
		view.mu.Lock()
		mount := view.mount
		var observers []taskGraphObserver
		if mount != nil {
			observers = slices.Clone(mount.observers)
		}
		view.mu.Unlock()
		if mount == nil {
			return nil, nil
		}
		mount.mu.Lock()
		mount.refreshes++
		snapshot := maps.Clone(mount.records)
		mount.mu.Unlock()

		keys := slices.Sorted(maps.Keys(snapshot))
		reasons := make([]durable.BlockedReason, len(keys))
		for i, key := range keys {
			reasons[i] = view.blocking.Blocked(snapshot[key])
		}

		mount.mu.Lock()
		var ops []durable.Op
		changed := map[string]*TaskGraphNode{}
		for i, key := range keys {
			subject := snapshot[key]
			current, ok := mount.records[key]
			if !ok || current != subject {
				continue
			}
			node, ok := mount.value.Tasks[key]
			if !ok || reasons[i] == node.Blocked {
				continue
			}
			node.Blocked = reasons[i]
			changed[key] = &node
			if reasons[i] == "" {
				ops = append(ops, durable.Op{"d", []any{"tasks", key, "blocked"}})
			} else {
				ops = append(ops, durable.Op{"s", []any{"tasks", key, "blocked"}, reasons[i]})
			}
		}
		//portlint:allow ctxbackground task-graph.ts publishes a registry-change refresh under BACKGROUND_CONTEXT: no caller started it
		mount.applyLocked(context.Background(), observers, changed, ops)
		mount.mu.Unlock()
		mount.drainDeliveries(view.session.Report)
		return nil, nil
	})
}

// build reads the graph on the line, through the line's storage.
func (view *TaskGraphView) build(ctx context.Context, storage durable.Storage) (*taskGraphMount, error) {
	tasks := map[string]TaskGraphNode{}
	live := map[string]BlockedSubject{}
	var records []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, status := range taskGraphLiveStatuses {
		scanned, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return storage.ScanTasks(ctx, durable.TaskQuery{Status: &status}, taskGraphScanPageSize, cursor)
		})
		if err != nil {
			return nil, err
		}
		records = append(records, scanned...)
	}
	slices.SortFunc(records, func(a, b durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) int {
		return int(a.Id - b.Id)
	})
	for _, record := range records {
		owner := record.Id
		owned, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
			return storage.ScanConversations(ctx, durable.ConversationQuery{OwnerTaskId: &owner}, taskGraphScanPageSize, cursor)
		})
		if err != nil {
			return nil, err
		}
		conversations := make([]durable.ConversationId, len(owned))
		for i, conversation := range owned {
			conversations[i] = conversation.Id
		}
		slices.Sort(conversations)
		subject := BlockedSubjectOf(record)
		tasks[taskKey(record.Id)] = taskGraphNodeOf(record, conversations, view.blocking.Blocked(subject))
		live[taskKey(record.Id)] = subject
	}
	return &taskGraphMount{value: TaskGraph{Tasks: tasks}, records: live}, nil
}

func taskKey(id durable.TaskId) string { return strconv.FormatInt(int64(id), 10) }

// advanceTaskGraph derives the mount's operations from one publication, applies them, and hands the revision to every observer.
func advanceTaskGraph(ctx context.Context, mount *taskGraphMount, observers []taskGraphObserver, publication session.ObservedCommit, blocked func(BlockedSubject) durable.BlockedReason, report func(error)) {
	type taskEntry struct {
		record  durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		key     string
		subject BlockedSubject
	}
	var entries []taskEntry
	var hasConversationWrites bool
	for _, change := range publication.Changes {
		switch write := change.(type) {
		case durable.TaskWrite:
			record := write.Value
			entries = append(entries, taskEntry{
				record:  record,
				key:     taskKey(record.Id),
				subject: BlockedSubjectOf(record),
			})
		case durable.ConversationWrite:
			if write.Value.Owner != nil {
				hasConversationWrites = true
			}
		}
	}
	if len(entries) == 0 && !hasConversationWrites {
		return
	}

	// Update mount.records under mount.mu before deriving blocked reasons off every lock so any concurrent
	// refreshBlocked observes the updated records and bumps refreshes if it starts after this point.
	mount.mu.Lock()
	var liveEntries []int
	for i, entry := range entries {
		if entry.record.State.Status == durable.TaskTerminal {
			delete(mount.records, entry.key)
			continue
		}
		mount.records[entry.key] = entry.subject
		liveEntries = append(liveEntries, i)
	}
	seenRefreshes := mount.refreshes
	mount.mu.Unlock()

	var reasons []durable.BlockedReason
	if len(liveEntries) > 0 {
		reasons = make([]durable.BlockedReason, len(entries))
	}
	for {
		for _, i := range liveEntries {
			reasons[i] = blocked(entries[i].subject)
		}
		mount.mu.Lock()
		if len(liveEntries) > 0 && mount.refreshes != seenRefreshes {
			seenRefreshes = mount.refreshes
			mount.mu.Unlock()
			continue
		}

		var ops []durable.Op
		// Nodes this publication set (non-nil) or deleted (nil), over the mount's value.
		changed := map[string]*TaskGraphNode{}
		node := func(key string) *TaskGraphNode {
			if next, ok := changed[key]; ok {
				return next
			}
			if current, ok := mount.value.Tasks[key]; ok {
				return &current
			}
			return nil
		}
		for i, entry := range entries {
			previous := node(entry.key)
			if entry.record.State.Status == durable.TaskTerminal {
				if previous == nil {
					continue
				}
				ops = append(ops, durable.Op{"d", []any{"tasks", entry.key}})
				changed[entry.key] = nil
				continue
			}
			conversations := []durable.ConversationId{}
			if previous != nil {
				conversations = previous.Conversations
			}
			next := taskGraphNodeOf(entry.record, conversations, reasons[i])
			if previous != nil && sameNode(next, *previous) {
				continue
			}
			value, err := durable.ToJsonValue(next)
			if err != nil {
				mount.mu.Unlock()
				panic(err)
			}
			ops = append(ops, durable.Op{"s", []any{"tasks", entry.key}, value})
			changed[entry.key] = &next
		}
		// After the tasks, so a conversation created with its owner task in one commit finds the owner's node. Change order within a publication is unspecified, so each owner's list is sorted again.
		if hasConversationWrites {
			created := map[string][]durable.ConversationId{}
			var createdOrder []string
			for _, change := range publication.Changes {
				write, ok := change.(durable.ConversationWrite)
				if !ok || write.Value.Owner == nil {
					continue
				}
				key := taskKey(write.Value.Owner.TaskId)
				if node(key) == nil {
					continue
				}
				if _, seen := created[key]; !seen {
					createdOrder = append(createdOrder, key)
				}
				created[key] = append(created[key], write.Value.Id)
			}
			for _, key := range createdOrder {
				current := node(key)
				conversations := slices.Concat(current.Conversations, created[key])
				slices.Sort(conversations)
				next := *current
				next.Conversations = conversations
				changed[key] = &next
				values := make([]any, len(conversations))
				for i, id := range conversations {
					values[i] = float64(id)
				}
				ops = append(ops, durable.Op{"s", []any{"tasks", key, "conversations"}, values})
			}
		}
		mount.applyLocked(ctx, observers, changed, ops)
		mount.mu.Unlock()
		break
	}
	mount.drainDeliveries(report)
}

// applyLocked applies changed nodes (non-nil set, nil deleted) to mount.value and queues the delivery for drainDeliveries.
// Caller holds mount.mu.
func (mount *taskGraphMount) applyLocked(ctx context.Context, observers []taskGraphObserver, changed map[string]*TaskGraphNode, ops []durable.Op) {
	if len(ops) == 0 {
		return
	}
	tasks := maps.Clone(mount.value.Tasks)
	for key, next := range changed {
		if next == nil {
			delete(tasks, key)
		} else {
			tasks[key] = *next
		}
	}
	mount.value = TaskGraph{Tasks: tasks}
	frameContext := context.WithoutCancel(ctx)
	change := chord.WatchCall[TaskGraph]{Kind: chord.WatchCallValue, Value: mount.value, Ops: ops}
	mount.pending = append(mount.pending, taskGraphDelivery{ctx: frameContext, change: change, observers: observers})
}

// drainDeliveries delivers queued revisions synchronously in order under deliverMu, with no state lock held across observer.Advance.
func (mount *taskGraphMount) drainDeliveries(report func(error)) {
	mount.deliverMu.Lock()
	defer mount.deliverMu.Unlock()
	for {
		mount.mu.Lock()
		if len(mount.pending) == 0 {
			mount.mu.Unlock()
			return
		}
		delivery := mount.pending[0]
		mount.pending[0] = taskGraphDelivery{}
		mount.pending = mount.pending[1:]
		if len(mount.pending) == 0 {
			mount.pending = nil
		}
		mount.mu.Unlock()
		for _, observer := range delivery.observers {
			session.Contained(func() { observer.Advance(delivery.ctx, delivery.change) }, report)
		}
	}
}

// sameNode compares nodes by their JSON form, as upstream compares JSON.stringify output.
func sameNode(a, b TaskGraphNode) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		panic("durable/harness: task graph node is not JSON")
	}
	return bytes.Equal(left, right)
}

func taskGraphNodeOf(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], conversations []durable.ConversationId, blocked durable.BlockedReason) TaskGraphNode {
	return TaskGraphNode{
		Id:             record.Id,
		Kind:           record.Kind,
		ConversationId: record.ConversationId,
		Owner:          record.Owner,
		Background:     record.Background,
		AbortRequested: record.AbortRequested,
		State:          taskGraphStateOf(record),
		Conversations:  conversations,
		Blocked:        blocked,
	}
}

func taskGraphStateOf(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) TaskGraphState {
	state := record.State
	switch state.Status {
	case durable.TaskPending, durable.TaskRunning:
		return TaskGraphState{Status: state.Status, Phase: phaseOf(state.Checkpoint)}
	case durable.TaskWaiting:
		return TaskGraphState{Status: durable.TaskWaiting, Phase: phaseOf(state.Checkpoint), On: slices.Clone(state.On), Policy: state.Policy}
	case durable.TaskSleeping:
		sleeping := TaskGraphState{Status: durable.TaskSleeping, Phase: phaseOf(state.Checkpoint), Signals: slices.Clone(state.Signals)}
		if state.Until != nil {
			until := *state.Until
			sleeping.Until = &until
		}
		return sleeping
	}
	// Terminal records never reach here: they leave the graph.
	var outcome durable.TaskOutcomeStatus
	if state.Outcome != nil {
		outcome = state.Outcome.Status
	}
	return TaskGraphState{Status: durable.TaskCompleting, Outcome: outcome}
}

func phaseOf(checkpoint *durable.JsonValue) string {
	if checkpoint == nil {
		return ""
	}
	phase, _ := jsonMember(*checkpoint, "phase").(string)
	return phase
}
