// Ports packages/durable/src/harness/task-graph.ts.

package harness

import (
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

// TaskGraphState is a live task's durable status without its checkpoint and outcome payloads (spec §9.5): pending or running with Phase, waiting with Phase, On, and Policy, or completing with the held Outcome status.
type TaskGraphState struct {
	Status durable.TaskStatus `json:"status"`
	Phase  string             `json:"phase,omitempty"`
	On     []durable.TaskId   `json:"on,omitempty"`
	Policy durable.JoinPolicy `json:"policy,omitempty"`
	// Outcome is the status of the outcome held until its ordinary owned work drains.
	Outcome durable.TaskOutcomeStatus `json:"outcome,omitempty"`
}

// MarshalJSON encodes the upstream variant of the state's Status.
func (state TaskGraphState) MarshalJSON() ([]byte, error) {
	switch state.Status {
	case durable.TaskPending, durable.TaskRunning:
		return json.Marshal(delta.JsonObjectOf("status", state.Status, "phase", state.Phase))
	case durable.TaskWaiting:
		on := state.On
		if on == nil {
			on = []durable.TaskId{}
		}
		return json.Marshal(delta.JsonObjectOf("status", state.Status, "phase", state.Phase, "on", on, "policy", state.Policy))
	}
	return json.Marshal(delta.JsonObjectOf("status", state.Status, "outcome", state.Outcome))
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
}

// TaskGraph is every live task of the Session (spec §9.5).
type TaskGraph struct {
	// Tasks holds every live task, keyed by its decimal ID.
	Tasks map[string]TaskGraphNode `json:"tasks"`
}

// TaskGraphWatch is a serialized exact-frame watch of the task graph.
type TaskGraphWatch = durable.WatchHandle[TaskGraph]

// taskGraphObserver is a CommittedStateSource or a CommittedWatch of the graph.
type taskGraphObserver interface {
	Advance(ctx context.Context, value TaskGraph, ops []durable.Op)
	CloseSession()
}

type taskGraphMount struct {
	value     TaskGraph
	observers []taskGraphObserver
}

var taskGraphLiveStatuses = []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskWaiting, durable.TaskCompleting}

const taskGraphScanPageSize = 256

// TaskGraphView is the Harness's task graph mount: built on the Session line by its first observer and dropped with its last. It advances from the Session's commit publications, which are durable.
type TaskGraphView struct {
	session *session.SessionImpl
	storage durable.Storage
	// mu guards mount and closed: a watch may detach off the line, from its delivery goroutine.
	mu     sync.Mutex
	mount  *taskGraphMount
	closed bool
}

// NewTaskGraphView subscribes the mount to the Session's commits and close.
func NewTaskGraphView(sessionImpl *session.SessionImpl, storage durable.Storage) *TaskGraphView {
	view := &TaskGraphView{session: sessionImpl, storage: storage}
	sessionImpl.SubscribeCommits(func(ctx context.Context, publication durable.CommitPublication) {
		view.mu.Lock()
		mount := view.mount
		var observers []taskGraphObserver
		if mount != nil {
			observers = slices.Clone(mount.observers)
		}
		view.mu.Unlock()
		if mount != nil {
			advanceTaskGraph(ctx, mount, observers, publication)
		}
	})
	sessionImpl.SubscribeClose(func() {
		view.mu.Lock()
		view.closed = true
		var observers []taskGraphObserver
		if view.mount != nil {
			observers = slices.Clone(view.mount.observers)
		}
		view.mount = nil
		view.mu.Unlock()
		for _, observer := range observers {
			observer.CloseSession()
		}
	})
	return view
}

// State returns a disposable read-only Chord state of the graph.
func (view *TaskGraphView) State(ctx context.Context) (durable.AttachedReplicatedState[TaskGraph], error) {
	var source *session.CommittedStateSource[TaskGraph]
	detach, err := view.attach(ctx, func(value TaskGraph, release func()) taskGraphObserver {
		source = session.NewCommittedStateSource(view.session, value, release)
		return source
	})
	if err != nil {
		return nil, err
	}
	state, err := chord.AttachReplicatedStateSource[TaskGraph](source, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		detach()
		return nil, err
	}
	return state, nil
}

// Watch returns a serialized exact-frame watch of the graph; cancelling ctx stops it.
func (view *TaskGraphView) Watch(ctx context.Context) (TaskGraphWatch, error) {
	var watch *session.CommittedWatch[TaskGraph]
	if _, err := view.attach(ctx, func(value TaskGraph, release func()) taskGraphObserver {
		watch = session.NewCommittedWatch(view.session, value, release, nil)
		return watch
	}); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		watch.Cancel()
		return nil, context.Cause(ctx)
	}
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return watch, nil
}

// attach registers an observer created from the current revision, atomically on the Session line.
func (view *TaskGraphView) attach(ctx context.Context, create func(value TaskGraph, release func()) taskGraphObserver) (func(), error) {
	result, err := view.session.ReadOnLine(func() (any, error) {
		view.mu.Lock()
		mount := view.mount
		view.mu.Unlock()
		if mount == nil {
			value, err := view.build(ctx)
			if err != nil {
				return nil, err
			}
			mount = &taskGraphMount{value: value}
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
		observer = create(mount.value, detach)
		view.mu.Lock()
		defer view.mu.Unlock()
		// Close or cancellation may begin while the mount builds; register nothing then.
		if view.closed {
			return nil, closedError()
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

func (view *TaskGraphView) build(ctx context.Context) (TaskGraph, error) {
	tasks := map[string]TaskGraphNode{}
	var records []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, status := range taskGraphLiveStatuses {
		scanned, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return view.storage.ScanTasks(ctx, durable.TaskQuery{Status: &status}, taskGraphScanPageSize, cursor)
		})
		if err != nil {
			return TaskGraph{}, err
		}
		records = append(records, scanned...)
	}
	slices.SortFunc(records, func(a, b durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) int {
		return int(a.Id - b.Id)
	})
	for _, record := range records {
		owner := record.Id
		owned, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
			return view.storage.ScanConversations(ctx, durable.ConversationQuery{OwnerTaskId: &owner}, taskGraphScanPageSize, cursor)
		})
		if err != nil {
			return TaskGraph{}, err
		}
		conversations := make([]durable.ConversationId, len(owned))
		for i, conversation := range owned {
			conversations[i] = conversation.Id
		}
		slices.Sort(conversations)
		tasks[taskKey(record.Id)] = taskGraphNodeOf(record, conversations)
	}
	return TaskGraph{Tasks: tasks}, nil
}

func taskKey(id durable.TaskId) string { return strconv.FormatInt(int64(id), 10) }

// advanceTaskGraph derives the mount's operations from one publication, applies them, and hands the revision to every observer.
func advanceTaskGraph(ctx context.Context, mount *taskGraphMount, observers []taskGraphObserver, publication durable.CommitPublication) {
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
	for _, change := range publication.Changes {
		write, ok := change.(durable.TaskWrite)
		if !ok {
			continue
		}
		record := write.Value
		key := taskKey(record.Id)
		previous := node(key)
		if record.State.Status == durable.TaskTerminal {
			if previous == nil {
				continue
			}
			ops = append(ops, durable.Op{"d", []any{"tasks", key}})
			changed[key] = nil
			continue
		}
		conversations := []durable.ConversationId{}
		if previous != nil {
			conversations = previous.Conversations
		}
		next := taskGraphNodeOf(record, conversations)
		if previous != nil && sameNode(next, *previous) {
			continue
		}
		value, err := durable.ToJsonValue(next)
		if err != nil {
			panic(err)
		}
		ops = append(ops, durable.Op{"s", []any{"tasks", key}, value})
		changed[key] = &next
	}
	// After the tasks, so a conversation created with its owner task in one commit finds the owner's node. Change order within a publication is unspecified, so each owner's list is sorted again.
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
	for _, observer := range observers {
		observer.Advance(frameContext, mount.value, ops)
	}
}

// sameNode compares nodes by their JSON form, as upstream compares JSON.stringify output.
func sameNode(a, b TaskGraphNode) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	if errLeft != nil || errRight != nil {
		panic("durable/harness: task graph node is not JSON")
	}
	return string(left) == string(right)
}

func taskGraphNodeOf(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], conversations []durable.ConversationId) TaskGraphNode {
	return TaskGraphNode{
		Id:             record.Id,
		Kind:           record.Kind,
		ConversationId: record.ConversationId,
		Owner:          record.Owner,
		Background:     record.Background,
		AbortRequested: record.AbortRequested,
		State:          taskGraphStateOf(record),
		Conversations:  conversations,
	}
}

func taskGraphStateOf(record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]) TaskGraphState {
	state := record.State
	switch state.Status {
	case durable.TaskPending, durable.TaskRunning:
		return TaskGraphState{Status: state.Status, Phase: phaseOf(state.Checkpoint)}
	case durable.TaskWaiting:
		return TaskGraphState{Status: durable.TaskWaiting, Phase: phaseOf(state.Checkpoint), On: slices.Clone(state.On), Policy: state.Policy}
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
