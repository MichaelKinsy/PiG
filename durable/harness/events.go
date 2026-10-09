// Ports packages/durable/src/harness/events.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// QueuedItem is one queued inbox item as events report it.
type QueuedItem struct {
	Id   durable.SubmissionId `json:"id"`
	Mode InboxMode            `json:"mode"`
}

// MessageChange is one change to the in-flight assistant message, relative to that message. Type is text_start, thinking_start, toolcall_start (Block), text_delta, thinking_delta (Delta), toolcall_delta (Path, Delta), block (Block), or message (Message).
type MessageChange struct {
	Type         string
	ContentIndex int
	Block        ai.AssistantContentBlock
	Delta        string
	Path         []any
	Message      *ai.AssistantMessage
}

// MarshalJSON encodes the upstream variant of the change's Type (events.ts MessageChange).
func (change MessageChange) MarshalJSON() ([]byte, error) {
	switch change.Type {
	case "message":
		return json.Marshal(delta.JsonObjectOf("type", change.Type, "message", change.Message))
	case "text_delta", "thinking_delta":
		return json.Marshal(delta.JsonObjectOf("type", change.Type, "contentIndex", change.ContentIndex, "delta", change.Delta))
	case "toolcall_delta":
		return json.Marshal(delta.JsonObjectOf("type", change.Type, "contentIndex", change.ContentIndex, "path", change.Path, "delta", change.Delta))
	}
	return json.Marshal(delta.JsonObjectOf("type", change.Type, "contentIndex", change.ContentIndex, "block", change.Block))
}

// AgentEvent is an experimental agent event, shaped like the coding agent's session events (spec §9.4).
type AgentEvent interface {
	EventType() string
}

// SnapshotGeneration is the current generation attempt: its in-flight partial, retry backoff, or deferred poll.
type SnapshotGeneration struct {
	Attempt  int                  `json:"attempt"`
	Message  *ai.AssistantMessage `json:"message,omitempty"`
	Retry    *RetryStatus         `json:"retry,omitempty"`
	Deferred *DeferredStatus      `json:"deferred,omitempty"`
}

// SnapshotEvent is the state of a conversation at attachment.
type SnapshotEvent struct {
	Entries []durable.EntryRecord `json:"entries"`
	// Run is nil when idle.
	Run        *RunEvent           `json:"run,omitempty"`
	Generation *SnapshotGeneration `json:"generation,omitempty"`
	Tools      []ToolSlot          `json:"tools"`
	// Compactions are pi.live.compactions: live compactions with their attempt and retry backoff.
	Compactions []CompactionStatus `json:"compactions"`
	Inbox       []QueuedItem       `json:"inbox"`
	// Agent is pi.agent; empty when absent.
	Agent AgentState `json:"agent"`
	Usage UsageState `json:"usage"`
}

// RunEvent carries the inputs of run_start and run_end.
type RunEvent struct {
	Inputs []durable.SubmissionId `json:"inputs"`
}

type (
	RunStartEvent struct {
		Inputs []durable.SubmissionId `json:"inputs"`
	}
	RunEndEvent struct {
		Inputs []durable.SubmissionId `json:"inputs"`
	}
	TurnStartEvent    struct{}
	TurnEndEvent      struct{}
	MessageStartEvent struct {
		Message ai.Message `json:"message"`
	}
	// MessageUpdateEvent carries the partial's current usage, as in the coding agent's JSON mode.
	MessageUpdateEvent struct {
		Usage   ai.Usage        `json:"usage"`
		Changes []MessageChange `json:"changes"`
	}
	MessageEndEvent struct {
		Entry durable.EntryRecord `json:"entry"`
	}
	ToolExecutionStartEvent struct {
		ToolCallId string             `json:"toolCallId"`
		ToolName   string             `json:"toolName"`
		Args       durable.JsonObject `json:"args"`
	}
	// ToolOutputChange is a front trim and then an append of the retained window (TrimStart, Append), or its replacement (Set).
	ToolOutputChange struct {
		TrimStart *int    `json:"trimStart,omitempty"`
		Append    *string `json:"append,omitempty"`
		Set       *string `json:"set,omitempty"`
	}
	ToolExecutionUpdateEvent struct {
		ToolCallId string
		ToolName   string
		Output     *ToolOutputChange
		// HasDetails reports a details change; Details is nil when they were removed.
		HasDetails  bool
		Details     durable.JsonValue
		Diagnostics []durable.ToolDiagnostic
	}
	// ToolExecutionEndEvent has a nil Entry when the tool task faulted or was orphaned.
	ToolExecutionEndEvent struct {
		ToolCallId string               `json:"toolCallId"`
		ToolName   string               `json:"toolName"`
		Entry      *durable.EntryRecord `json:"entry,omitempty"`
	}
	InboxUpdateEvent struct {
		Items []QueuedItem `json:"items"`
	}
	SubmissionEvent struct {
		Record durable.SubmissionRecord `json:"record"`
	}
	AutoRetryStartEvent struct {
		Attempt      int     `json:"attempt"`
		At           float64 `json:"at"`
		ErrorMessage string  `json:"errorMessage"`
	}
	AutoRetryEndEvent struct {
		Attempt int `json:"attempt"`
	}
	DeferredPollEvent struct {
		PollAt float64 `json:"pollAt"`
	}
	EntryAppendedEvent struct {
		Entry durable.EntryRecord `json:"entry"`
	}
	AgentChangedEvent struct {
		Agent AgentState `json:"agent"`
	}
	UsageChangedEvent struct {
		Usage UsageState `json:"usage"`
	}
	TaskFailedEvent struct {
		TaskId  durable.TaskId `json:"taskId"`
		Kind    string         `json:"kind"`
		Message string         `json:"message"`
	}
	CompactionStartEvent struct {
		TaskId   durable.TaskId           `json:"taskId"`
		Reason   durable.CompactionReason `json:"reason"`
		Blocking bool                     `json:"blocking"`
	}
	// CompactionEndEvent: the task's receipt tells whether it produced a summary; the summary entry has its own events.
	CompactionEndEvent struct {
		TaskId durable.TaskId           `json:"taskId"`
		Reason durable.CompactionReason `json:"reason"`
	}
)

func (SnapshotEvent) EventType() string            { return "snapshot" }
func (RunStartEvent) EventType() string            { return "run_start" }
func (RunEndEvent) EventType() string              { return "run_end" }
func (TurnStartEvent) EventType() string           { return "turn_start" }
func (TurnEndEvent) EventType() string             { return "turn_end" }
func (MessageStartEvent) EventType() string        { return "message_start" }
func (MessageUpdateEvent) EventType() string       { return "message_update" }
func (MessageEndEvent) EventType() string          { return "message_end" }
func (ToolExecutionStartEvent) EventType() string  { return "tool_execution_start" }
func (ToolExecutionUpdateEvent) EventType() string { return "tool_execution_update" }
func (ToolExecutionEndEvent) EventType() string    { return "tool_execution_end" }
func (InboxUpdateEvent) EventType() string         { return "inbox_update" }
func (SubmissionEvent) EventType() string          { return "submission" }
func (AutoRetryStartEvent) EventType() string      { return "auto_retry_start" }
func (AutoRetryEndEvent) EventType() string        { return "auto_retry_end" }
func (DeferredPollEvent) EventType() string        { return "deferred_poll" }
func (EntryAppendedEvent) EventType() string       { return "entry_appended" }
func (AgentChangedEvent) EventType() string        { return "agent_changed" }
func (UsageChangedEvent) EventType() string        { return "usage_changed" }
func (TaskFailedEvent) EventType() string          { return "task_failed" }
func (CompactionStartEvent) EventType() string     { return "compaction_start" }
func (CompactionEndEvent) EventType() string       { return "compaction_end" }

// eventJSON encodes value, a JSON object, with the event's type first, as the upstream discriminated union.
func eventJSON(kind string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	prefix := `{"type":` + strconv.Quote(kind)
	if len(encoded) == 2 {
		return []byte(prefix + "}"), nil
	}
	return append([]byte(prefix+","), encoded[1:]...), nil
}

func (event SnapshotEvent) MarshalJSON() ([]byte, error) {
	type plain SnapshotEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event RunStartEvent) MarshalJSON() ([]byte, error) {
	type plain RunStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event RunEndEvent) MarshalJSON() ([]byte, error) {
	type plain RunEndEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event TurnStartEvent) MarshalJSON() ([]byte, error) {
	type plain TurnStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event TurnEndEvent) MarshalJSON() ([]byte, error) {
	type plain TurnEndEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event MessageStartEvent) MarshalJSON() ([]byte, error) {
	type plain MessageStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event MessageUpdateEvent) MarshalJSON() ([]byte, error) {
	type plain MessageUpdateEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event MessageEndEvent) MarshalJSON() ([]byte, error) {
	type plain MessageEndEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event ToolExecutionStartEvent) MarshalJSON() ([]byte, error) {
	type plain ToolExecutionStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event ToolExecutionEndEvent) MarshalJSON() ([]byte, error) {
	type plain ToolExecutionEndEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event InboxUpdateEvent) MarshalJSON() ([]byte, error) {
	type plain InboxUpdateEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event SubmissionEvent) MarshalJSON() ([]byte, error) {
	type plain SubmissionEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event AutoRetryStartEvent) MarshalJSON() ([]byte, error) {
	type plain AutoRetryStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event AutoRetryEndEvent) MarshalJSON() ([]byte, error) {
	type plain AutoRetryEndEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event DeferredPollEvent) MarshalJSON() ([]byte, error) {
	type plain DeferredPollEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event EntryAppendedEvent) MarshalJSON() ([]byte, error) {
	type plain EntryAppendedEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event AgentChangedEvent) MarshalJSON() ([]byte, error) {
	type plain AgentChangedEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event UsageChangedEvent) MarshalJSON() ([]byte, error) {
	type plain UsageChangedEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event TaskFailedEvent) MarshalJSON() ([]byte, error) {
	type plain TaskFailedEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event CompactionStartEvent) MarshalJSON() ([]byte, error) {
	type plain CompactionStartEvent
	return eventJSON(event.EventType(), plain(event))
}

func (event CompactionEndEvent) MarshalJSON() ([]byte, error) {
	type plain CompactionEndEvent
	return eventJSON(event.EventType(), plain(event))
}

// MarshalJSON includes details when HasDetails (null when removed) and diagnostics when non-nil.
func (event ToolExecutionUpdateEvent) MarshalJSON() ([]byte, error) {
	value := map[string]any{"toolCallId": event.ToolCallId, "toolName": event.ToolName}
	if event.Output != nil {
		value["output"] = event.Output
	}
	if event.HasDetails {
		value["details"] = event.Details
	}
	if event.Diagnostics != nil {
		value["diagnostics"] = event.Diagnostics
	}
	return eventJSON(event.EventType(), value)
}

// AgentEventStream is the serialized stream of one conversation's event batches, one per commit.
type AgentEventStream struct {
	// Snapshot is the snapshot event at attachment.
	Snapshot SnapshotEvent
	watch    *session.CommittedWatch[[]AgentEvent]
}

// Start installs the sole listener; it never runs inline.
func (stream *AgentEventStream) Start(listener func(ctx context.Context, events []AgentEvent) error) {
	stream.watch.Start(func(ctx context.Context, events []AgentEvent, _ []durable.Op) error { return listener(ctx, events) })
}

// Stop idempotently stops the stream and returns its terminal result.
func (stream *AgentEventStream) Stop() (durable.WatchEnd, error) { return stream.watch.Stop() }

// Closed is closed when the stream ends; End returns why.
func (stream *AgentEventStream) Closed() <-chan struct{} { return stream.watch.Closed() }

// End returns the terminal result once Closed is closed.
func (stream *AgentEventStream) End() durable.WatchEnd { return stream.watch.End() }

// viewParts are the raw parts of a view the events read; identity of a raw value tells whether it changed.
type viewParts struct {
	live  *delta.JsonObject
	inbox *delta.JsonObject
	agent *delta.JsonObject
	usage *delta.JsonObject
}

func partsOf(view ConversationView) viewParts {
	live := viewDoc(view, "pi.live")
	if live == nil {
		live = delta.NewJsonObject(0)
	}
	return viewParts{live: live, inbox: viewDoc(view, "pi.inbox"), agent: viewDoc(view, "pi.agent"), usage: viewDoc(view, "pi.usage")}
}

func decodeEventJSON[T any](value any) T {
	decoded, err := durable.FromJsonValue[T](value)
	if err != nil {
		panic(err)
	}
	return decoded
}

func snapshotOf(view ConversationView) SnapshotEvent {
	parts := partsOf(view)
	snapshot := SnapshotEvent{
		Entries:     view.Entries,
		Tools:       []ToolSlot{},
		Compactions: []CompactionStatus{},
		Inbox:       queuedItems(parts.inbox),
		Agent:       AgentState{},
		Usage:       NewUsageState(),
	}
	if run, ok := parts.live.Value("run").(*delta.JsonObject); ok {
		snapshot.Run = &RunEvent{Inputs: decodeEventJSON[[]durable.SubmissionId](run.Value("inputs"))}
	}
	if generation, ok := parts.live.Value("generation").(*delta.JsonObject); ok {
		live := decodeEventJSON[LiveGeneration](generation)
		snapshot.Generation = &SnapshotGeneration{Attempt: live.Attempt, Retry: live.Retry, Deferred: live.Deferred}
		if message, present := generation.Get("message"); present {
			snapshot.Generation.Message = decodeAssistant(message)
		}
	}
	if tools, ok := parts.live.Get("tools"); ok {
		snapshot.Tools = decodeEventJSON[[]ToolSlot](tools)
	}
	if compactions, ok := parts.live.Get("compactions"); ok {
		snapshot.Compactions = decodeEventJSON[[]CompactionStatus](compactions)
	}
	if parts.agent != nil {
		snapshot.Agent = decodeEventJSON[AgentState](parts.agent)
	}
	if parts.usage != nil {
		snapshot.Usage = decodeEventJSON[UsageState](parts.usage)
	}
	return snapshot
}

func queuedItems(inbox *delta.JsonObject) []QueuedItem {
	items := []QueuedItem{}
	if inbox == nil {
		return items
	}
	for _, item := range decodeEventJSON[[]InboxItem](inbox.Value("items")) {
		items = append(items, QueuedItem{Id: item.Id, Mode: item.Mode})
	}
	return items
}

func decodeAssistant(value any) *ai.AssistantMessage {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	message, err := durable.DecodeMessage(encoded)
	if err != nil {
		panic(err)
	}
	assistant := message.(ai.AssistantMessage)
	return &assistant
}

// decodeBlock decodes one assistant content block.
func decodeBlock(value any) ai.AssistantContentBlock {
	message := decodeAssistant(map[string]any{"role": "assistant", "content": []any{value}, "api": "", "provider": "", "model": "", "usage": map[string]any{}, "stopReason": "stop", "timestamp": 0})
	return message.Content[0]
}

var errNotHarness = errors.New("Not a Harness")

// WatchEvents attaches to one conversation's agent events (spec §9.4). The snapshot and the registration for later commits are captured atomically on the Session line; overflow replaces undelivered batches with one snapshot.
func WatchEvents(ctx context.Context, harness Harness, conversationId durable.ConversationId) (*AgentEventStream, error) {
	impl, ok := harness.(*harnessImpl)
	if !ok {
		// conversationViews throws inside the async watchEvents, which rejects (view.ts:63-67).
		return nil, errNotHarness
	}
	views := impl.host.views
	var watch *session.CommittedWatch[[]AgentEvent]
	var snapshot SnapshotEvent
	_, _, err := views.Attach(ctx, conversationId, func(initial ConversationView, release func(), storage durable.Storage) (*ViewObserver, error) {
		// Generations whose held outcome already ended their turn, read on the line with the snapshot.
		kind, status := "pi.generation", durable.TaskCompleting
		query := durable.TaskQuery{ConversationId: &conversationId, Kind: &kind, Status: &status}
		completing, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return storage.ScanTasks(ctx, query, 100, cursor)
		})
		if err != nil {
			return nil, err
		}
		held := map[durable.TaskId]bool{}
		for _, record := range completing {
			held[record.Id] = true
		}
		current := initial
		snapshot = snapshotOf(initial)
		// Batches are the watch's values; an overflow delivers a snapshot of the newest view instead.
		watch = session.NewCommittedWatch(impl.SessionImpl, []AgentEvent{}, release, func() []AgentEvent { return []AgentEvent{snapshotOf(current)} })
		return &ViewObserver{
			Publication: func(commitCtx context.Context, before, after ConversationView, ops []durable.Op, publication durable.CommitPublication) {
				current = after
				events := translateEvents(conversationId, before, after, ops, publication, held)
				if len(events) > 0 {
					watch.Advance(commitCtx, events, nil)
				}
			},
			CloseSession: watch.CloseSession,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	// Like a watch, the acquisition context governs the stream's lifetime.
	if ctx.Err() != nil {
		watch.Cancel()
		return nil, context.Cause(ctx)
	}
	if ctx.Done() != nil {
		watch.ObserveCancellation(ctx)
	}
	return &AgentEventStream{Snapshot: snapshot, watch: watch}, nil
}

// sameJSON is upstream's === over immutable JSON revisions: containers compare by identity, scalars by value.
func sameJSON(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, right := reflect.ValueOf(a), reflect.ValueOf(b)
	if left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case reflect.Map:
		return left.UnsafePointer() == right.UnsafePointer()
	case reflect.Slice:
		return left.UnsafePointer() == right.UnsafePointer() && left.Len() == right.Len()
	}
	return a == b
}

func rawSlots(live *delta.JsonObject) []*delta.JsonObject {
	items, _ := live.Value("tools").([]any)
	slots := make([]*delta.JsonObject, 0, len(items))
	for _, item := range items {
		slots = append(slots, item.(*delta.JsonObject))
	}
	return slots
}

func slotString(slot *delta.JsonObject, key string) string {
	value, _ := slot.Value(key).(string)
	return value
}

func slotTaskId(slot *delta.JsonObject) (durable.TaskId, bool) {
	value, ok := slot.Value("taskId").(float64)
	return durable.TaskId(value), ok
}

func slotEntry(slot *delta.JsonObject) (durable.EntryId, bool) {
	value, ok := slot.Value("entry").(float64)
	return durable.EntryId(value), ok
}

// resultOf returns the tool result for callId among entries.
func resultOf(entries []durable.EntryRecord, callId string) *durable.EntryRecord {
	for i := range entries {
		if len(entries[i].Model) == 0 {
			continue
		}
		if result, ok := entries[i].Model[0].(ai.ToolResultMessage); ok && result.ToolCallID == callId {
			return &entries[i]
		}
	}
	return nil
}

func rawCompactions(live *delta.JsonObject) []*delta.JsonObject {
	items, _ := live.Value("compactions").([]any)
	out := make([]*delta.JsonObject, 0, len(items))
	for _, item := range items {
		out = append(out, item.(*delta.JsonObject))
	}
	return out
}

func compactionTaskOf(status *delta.JsonObject) durable.TaskId {
	value, _ := status.Value("taskId").(float64)
	return durable.TaskId(value)
}

// translateEvents returns every event one publication causes, in the order of spec §9.4.
func translateEvents(conversationId durable.ConversationId, before, after ConversationView, viewOps []durable.Op, publication durable.CommitPublication, held map[durable.TaskId]bool) []AgentEvent {
	var entries []durable.EntryRecord
	tasks := map[durable.TaskId]durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{}
	var taskOrder []durable.TaskId
	var submissions []durable.SubmissionRecord
	for _, change := range publication.Changes {
		switch typed := change.(type) {
		case durable.EntryWrite:
			if typed.Value.ConversationId == conversationId {
				entries = append(entries, typed.Value)
			}
		case durable.TaskWrite:
			if typed.Value.ConversationId == conversationId {
				if _, seen := tasks[typed.Value.Id]; !seen {
					taskOrder = append(taskOrder, typed.Value.Id)
				}
				tasks[typed.Value.Id] = typed.Value
			}
		case durable.SubmissionWrite:
			if typed.Value.ConversationId == conversationId {
				submissions = append(submissions, typed.Value)
			}
		}
	}
	if len(viewOps) == 0 && len(entries) == 0 && len(tasks) == 0 && len(submissions) == 0 {
		return nil
	}
	// Entries are appended in ID order; submission records are published in the order the commit first touched them.
	slices.SortStableFunc(submissions, func(a, b durable.SubmissionRecord) int { return int(a.Id - b.Id) })
	was, now := partsOf(before), partsOf(after)
	events := []AgentEvent{}

	// Progress: tool starts, the in-flight message, tool updates, retry and deferred state.
	slotsBefore := map[string]*delta.JsonObject{}
	var slotsBeforeOrder []string
	for _, slot := range rawSlots(was.live) {
		callId := slotString(slot, "callId")
		if _, seen := slotsBefore[callId]; !seen {
			slotsBeforeOrder = append(slotsBeforeOrder, callId)
		}
		slotsBefore[callId] = slot
	}
	slots := rawSlots(now.live)
	for _, slot := range slots {
		previous := slotsBefore[slotString(slot, "callId")]
		if slotString(slot, "status") != "running" || (previous != nil && slotString(previous, "status") == "running") {
			continue
		}
		args := delta.NewJsonObject(0)
		if taskId, ok := slotTaskId(slot); ok {
			if task, found := tasks[taskId]; found && task.State.Checkpoint != nil {
				switch arguments := jsonMember(*task.State.Checkpoint, "arguments").(type) {
				case *delta.JsonObject:
					args = arguments
				case map[string]any:
					if copied, err := chord.CopyJSONObject(arguments); err == nil {
						args = copied
					}
				}
			}
		}
		events = append(events, ToolExecutionStartEvent{ToolCallId: slotString(slot, "callId"), ToolName: slotString(slot, "name"), Args: args})
	}
	generationBefore, _ := was.live.Value("generation").(*delta.JsonObject)
	generation, _ := now.live.Value("generation").(*delta.JsonObject)
	var partialBefore, partial any
	if generationBefore != nil {
		partialBefore = generationBefore.Value("message")
	}
	if generation != nil {
		partial = generation.Value("message")
	}
	if partial != nil && partialBefore == nil {
		events = append(events, MessageStartEvent{Message: *decodeAssistant(partial)})
	} else if partial != nil && !sameJSON(partial, partialBefore) {
		message := decodeAssistant(partial)
		events = append(events, MessageUpdateEvent{Usage: message.Usage, Changes: messageChanges(viewOps, partial.(*delta.JsonObject), message)})
	}
	for index, slot := range slots {
		previous := slotsBefore[slotString(slot, "callId")]
		if slotString(slot, "status") != "running" || previous == nil || slotString(previous, "status") != "running" {
			continue
		}
		update, ok := toolUpdate(viewOps, index, slot, previous)
		if !ok {
			continue
		}
		update.ToolCallId, update.ToolName = slotString(slot, "callId"), slotString(slot, "name")
		events = append(events, update)
	}
	retryOf := func(generation *delta.JsonObject) *delta.JsonObject {
		if generation == nil {
			return nil
		}
		retry, _ := generation.Value("retry").(*delta.JsonObject)
		return retry
	}
	attemptOf := func(generation *delta.JsonObject) int {
		attempt, _ := generation.Value("attempt").(float64)
		return int(attempt)
	}
	if retry := retryOf(generation); retry != nil && retryOf(generationBefore) == nil {
		at, _ := retry.Value("at").(float64)
		message, _ := retry.Value("error").(string)
		events = append(events, AutoRetryStartEvent{Attempt: attemptOf(generation), At: at, ErrorMessage: message})
	}
	if retryOf(generationBefore) != nil && retryOf(generation) == nil {
		events = append(events, AutoRetryEndEvent{Attempt: attemptOf(generationBefore)})
	}
	if generation != nil {
		if deferredStatus, ok := generation.Value("deferred").(*delta.JsonObject); ok {
			pollAt, _ := deferredStatus.Value("pollAt").(float64)
			var before any
			if generationBefore != nil {
				if previous, has := generationBefore.Value("deferred").(*delta.JsonObject); has {
					before = previous.Value("pollAt")
				}
			}
			if before != any(pollAt) {
				events = append(events, DeferredPollEvent{PollAt: pollAt})
			}
		}
	}

	// Tools that end in this commit: a slot that becomes done, one created done (a call not offered), or an unfinished one that vanishes because its run ended. A done slot that vanishes ended earlier.
	var toolEnds []ToolExecutionEndEvent
	endTool := func(callId, name string, entryId *durable.EntryId) {
		end := ToolExecutionEndEvent{ToolCallId: callId, ToolName: name}
		if entryId != nil {
			for i := range entries {
				if entries[i].Id == *entryId {
					end.Entry = &entries[i]
					break
				}
			}
		}
		toolEnds = append(toolEnds, end)
	}
	findSlot := func(callId string) *delta.JsonObject {
		for _, slot := range slots {
			if slotString(slot, "callId") == callId {
				return slot
			}
		}
		return nil
	}
	for _, callId := range slotsBeforeOrder {
		previous := slotsBefore[callId]
		if slotString(previous, "status") == "done" {
			continue
		}
		slot := findSlot(callId)
		if slot != nil && slotString(slot, "status") == "done" {
			if entryId, ok := slotEntry(slot); ok {
				endTool(callId, slotString(previous, "name"), &entryId)
			} else {
				endTool(callId, slotString(previous, "name"), nil)
			}
		} else if slot == nil {
			// A slot whose run ended in this commit may have had its result appended with it, as for unstarted calls.
			if result := resultOf(entries, callId); result != nil {
				endTool(callId, slotString(previous, "name"), &result.Id)
			} else {
				endTool(callId, slotString(previous, "name"), nil)
			}
		}
	}
	for _, slot := range slots {
		if _, existed := slotsBefore[slotString(slot, "callId")]; slotString(slot, "status") == "done" && !existed {
			if entryId, ok := slotEntry(slot); ok {
				endTool(slotString(slot, "callId"), slotString(slot, "name"), &entryId)
			} else {
				endTool(slotString(slot, "callId"), slotString(slot, "name"), nil)
			}
		}
	}

	// Entries in append order; a tool's end directly precedes its result's message, as in the coding agent.
	assistantAppended := false
	for i := range entries {
		entry := entries[i]
		for _, end := range toolEnds {
			if end.Entry != nil && end.Entry.Id == entry.Id {
				events = append(events, end)
			}
		}
		if len(entry.Model) == 0 {
			events = append(events, EntryAppendedEvent{Entry: entry})
			continue
		}
		message := entry.Model[0]
		_, isAssistant := message.(ai.AssistantMessage)
		// A streamed answer already started with its first partial.
		streamed := isAssistant && partialBefore != nil && !assistantAppended
		if isAssistant {
			assistantAppended = true
		}
		if !streamed {
			events = append(events, MessageStartEvent{Message: message})
		}
		events = append(events, MessageEndEvent{Entry: entry})
	}
	// Ends without a result entry: a faulted or orphaned tool, or one whose run ended.
	for _, end := range toolEnds {
		if end.Entry == nil {
			events = append(events, end)
		}
	}

	// Compaction ends, task failures, then turn and run ends.
	compactionsBefore, compactions := rawCompactions(was.live), rawCompactions(now.live)
	hasCompaction := func(list []*delta.JsonObject, taskId durable.TaskId) bool {
		return slices.ContainsFunc(list, func(status *delta.JsonObject) bool { return compactionTaskOf(status) == taskId })
	}
	for _, status := range compactionsBefore {
		if !hasCompaction(compactions, compactionTaskOf(status)) {
			reason, _ := status.Value("reason").(string)
			events = append(events, CompactionEndEvent{TaskId: compactionTaskOf(status), Reason: durable.CompactionReason(reason)})
		}
	}
	// A generation's turn ends when its outcome is committed: at a completing hold or at terminal, whichever comes first, so a successor created at the hold starts after it.
	turnEnded := false
	for _, id := range taskOrder {
		task := tasks[id]
		status := task.State.Status
		if task.Kind == "pi.generation" && status == durable.TaskCompleting && !held[task.Id] {
			held[task.Id] = true
			turnEnded = true
		}
		if status != durable.TaskTerminal {
			continue
		}
		if task.Kind == "pi.generation" {
			if held[task.Id] {
				delete(held, task.Id)
			} else {
				turnEnded = true
			}
		}
		if outcome := task.State.Outcome; outcome != nil && (outcome.Status == durable.OutcomeFaulted || outcome.Status == durable.OutcomeOrphaned) {
			message := ""
			if outcome.Status == durable.OutcomeFaulted && outcome.Error != nil {
				message = outcome.Error.Message
			} else if outcome.Reason != nil {
				message = *outcome.Reason
			}
			events = append(events, TaskFailedEvent{TaskId: task.Id, Kind: task.Kind, Message: message})
		}
	}
	if turnEnded {
		events = append(events, TurnEndEvent{})
	}
	runOf := func(live *delta.JsonObject) *LiveRun {
		if run, ok := live.Value("run").(*delta.JsonObject); ok {
			decoded := decodeEventJSON[LiveRun](run)
			return &decoded
		}
		return nil
	}
	run, runBefore := runOf(now.live), runOf(was.live)
	firstInput := func(run *LiveRun) (durable.SubmissionId, bool) {
		if run == nil || len(run.Inputs) == 0 {
			return 0, false
		}
		return run.Inputs[0], true
	}
	nowFirst, nowHas := firstInput(run)
	wasFirst, wasHas := firstInput(runBefore)
	runChanged := nowHas != wasHas || nowFirst != wasFirst
	if runBefore != nil && runChanged {
		events = append(events, RunEndEvent{Inputs: runBefore.Inputs})
	}

	// Submissions, document state, then what began.
	for _, record := range submissions {
		events = append(events, SubmissionEvent{Record: record})
	}
	if !sameJSON(now.inbox, was.inbox) {
		events = append(events, InboxUpdateEvent{Items: queuedItems(now.inbox)})
	}
	// A retired document reads as its initial value, as in a snapshot.
	if !sameJSON(now.agent, was.agent) {
		agent := AgentState{}
		if now.agent != nil {
			agent = decodeEventJSON[AgentState](now.agent)
		}
		events = append(events, AgentChangedEvent{Agent: agent})
	}
	if !sameJSON(now.usage, was.usage) {
		usage := NewUsageState()
		if now.usage != nil {
			usage = decodeEventJSON[UsageState](now.usage)
		}
		events = append(events, UsageChangedEvent{Usage: usage})
	}
	for _, status := range compactions {
		if !hasCompaction(compactionsBefore, compactionTaskOf(status)) {
			reason, _ := status.Value("reason").(string)
			blocking, _ := status.Value("blocking").(bool)
			events = append(events, CompactionStartEvent{TaskId: compactionTaskOf(status), Reason: durable.CompactionReason(reason), Blocking: blocking})
		}
	}
	if run != nil && runChanged {
		events = append(events, RunStartEvent{Inputs: run.Inputs})
	}
	if run != nil && (runBefore == nil || run.TaskId != runBefore.TaskId) {
		if task, found := tasks[run.TaskId]; found && task.Kind == "pi.generation" {
			events = append(events, TurnStartEvent{})
		}
	}
	return events
}

var partialPath = []any{"docs", "pi.live", "generation", "message"}

// messageChanges translates the view operations on the in-flight message into message changes (spec §9.4).
func messageChanges(viewOps []durable.Op, raw *delta.JsonObject, message *ai.AssistantMessage) []MessageChange {
	whole := func() []MessageChange { return []MessageChange{{Type: "message", Message: message}} }
	changes := []MessageChange{}
	// A block sent whole already holds every later change to it in this batch.
	sent := map[int]bool{}
	for _, op := range viewOps {
		// View operations never replace the root.
		path, _ := op[1].([]any)
		if !pathHasPrefix(path, partialPath) {
			// The whole message or generation was replaced.
			if pathHasPrefix(partialPath, path) {
				return whole()
			}
			continue
		}
		rest := path[len(partialPath):]
		if len(rest) > 0 && rest[0] == "usage" {
			continue
		}
		if len(rest) == 0 || rest[0] != "content" {
			return whole()
		}
		if len(rest) == 1 {
			if op[0] != "p" || toInt(op[3]) != 0 {
				return whole()
			}
			start := toInt(op[2])
			blocks, _ := op[4].([]any)
			for offset, raw := range blocks {
				block := decodeBlock(raw)
				kind := "toolcall_start"
				switch block.(type) {
				case ai.TextContent:
					kind = "text_start"
				case ai.ThinkingContent:
					kind = "thinking_start"
				}
				changes = append(changes, MessageChange{Type: kind, ContentIndex: start + offset, Block: block})
			}
			continue
		}
		contentIndex := toInt(rest[1])
		var field any
		if len(rest) > 2 {
			field = rest[2]
		}
		if sent[contentIndex] {
			continue
		}
		switch {
		case op[0] == "a" && len(rest) == 3 && (field == "text" || field == "thinking"):
			kind := "text_delta"
			if field == "thinking" {
				kind = "thinking_delta"
			}
			changes = append(changes, MessageChange{Type: kind, ContentIndex: contentIndex, Delta: op[2].(string)})
		case op[0] == "a" && field == "arguments":
			changes = append(changes, MessageChange{Type: "toolcall_delta", ContentIndex: contentIndex, Path: slices.Clone(rest[3:]), Delta: op[2].(string)})
		default:
			sent[contentIndex] = true
			var block ai.AssistantContentBlock
			if contentIndex < len(message.Content) {
				block = message.Content[contentIndex]
			}
			changes = append(changes, MessageChange{Type: "block", ContentIndex: contentIndex, Block: block})
		}
	}
	_ = raw
	return changes
}

func toInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case int64:
		return int(typed)
	}
	return -1
}

// toolUpdate returns the output, details, and diagnostics changes of a running slot, from the view operations on it.
func toolUpdate(viewOps []durable.Op, index int, slot, previous *delta.JsonObject) (ToolExecutionUpdateEvent, bool) {
	outputPath := []any{"docs", "pi.live", "tools", index, "output"}
	trimStart := 0
	var appended strings.Builder
	set := false
	for _, op := range viewOps {
		path, _ := op[1].([]any)
		if !pathHasPrefix(path, outputPath) {
			continue
		}
		switch op[0] {
		case "t":
			trimStart += toInt(op[2])
		case "a":
			appended.WriteString(op[2].(string))
		default:
			set = true
		}
	}
	var update ToolExecutionUpdateEvent
	changed := false
	output, _ := slot.Value("output").(string)
	if set || (!sameJSON(slot.Value("output"), previous.Value("output")) && trimStart == 0 && appended.String() == "") {
		update.Output = &ToolOutputChange{Set: new(output)}
		changed = true
	} else if trimStart > 0 || appended.String() != "" {
		update.Output = &ToolOutputChange{}
		if trimStart > 0 {
			update.Output.TrimStart = new(trimStart)
		}
		if appended.String() != "" {
			update.Output.Append = new(appended.String())
		}
		changed = true
	}
	// A safe replay clears a running slot's progress: removed details send null, removed diagnostics [].
	if !sameJSON(slot.Value("details"), previous.Value("details")) {
		update.HasDetails = true
		update.Details = slot.Value("details")
		changed = true
	}
	if !sameJSON(slot.Value("diagnostics"), previous.Value("diagnostics")) {
		update.Diagnostics = []durable.ToolDiagnostic{}
		if diagnostics, ok := slot.Get("diagnostics"); ok {
			update.Diagnostics = decodeEventJSON[[]durable.ToolDiagnostic](diagnostics)
		}
		changed = true
	}
	return update, changed
}

func pathHasPrefix(path, prefix []any) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, segment := range prefix {
		if toSegment(path[i]) != toSegment(segment) {
			return false
		}
	}
	return true
}

// toSegment normalizes a path segment: indices compare as integers whatever their numeric type.
func toSegment(segment any) any {
	switch typed := segment.(type) {
	case float64:
		return int(typed)
	case int64:
		return int(typed)
	}
	return segment
}
