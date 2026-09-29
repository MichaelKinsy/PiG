package runtime

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func watchConfiguration() session.LaneConfiguration {
	return session.LaneConfiguration{Model: session.ModelRef{Provider: "configured", ModelID: "model"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{}}
}

func watchSession(t *testing.T, storage session.Storage) session.Session {
	t.Helper()
	isolateHarnessTest(t)
	if storage == nil {
		storage = session.NewMemoryStorage(nil)
	}
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: t.Name(), CreatedAt: 1, StorageVersion: 1}, storage, nil)
	t.Cleanup(func() { requireHarnessOK(t, opened.Close(context.Background())) })
	harnessCommit(t, opened, restoreLaneWrites("main", watchConfiguration())...)
	return opened
}

func attachWatch(t *testing.T, opened session.Session) (*Lane, *Harness) {
	t.Helper()
	// Upstream's faux provider is only registered here; watch must not make provider requests.
	faux := newLaneFaux()
	created, err := CreateAgentHarness(context.Background(), AgentHarnessOptions{Session: opened, Models: faux.models, Model: faux.model})
	requireHarnessOK(t, err)
	t.Cleanup(func() { requireHarnessOK(t, created.Harness.Close(context.Background())) })
	lane, err := created.Harness.Lane(context.Background(), "main", nil)
	requireHarnessOK(t, err)
	return lane, created.Harness
}

func watchAssistant(content []ai.AssistantContentBlock, reason ai.StopReason) agent.AgentMessage {
	// packages/ai/src/providers/faux.ts:78-101
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: content, API: "faux", Provider: "faux", ModelID: "faux-1", Usage: &ai.Usage{}, StopReason: reason, Timestamp: time.Now().UnixMilli()}}
}

func watchMeta(opened session.Session) session.OperationMeta {
	return session.OperationMeta{OperationID: opened.IdGenerator().Next(nil), Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
}

func TestPortWave06RuntimeLaneWatch(t *testing.T) {
	ctx := context.Background()
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:103
	t.Run("captures a compaction-bounded transcript and isolates the returned snapshot", func(t *testing.T) {
		opened := watchSession(t, nil)
		harnessCommit(t, opened,
			session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeCustom, CustomType: "root"}),
			session.InsertEntry(session.Entry{ID: "compact", ParentID: new("root"), Type: session.EntryTypeCompaction, Summary: "summary", RetainedTail: []agent.AgentMessage{}, TokensBefore: 10}),
			session.InsertEntry(session.Entry{ID: "after", ParentID: new("compact"), Type: session.EntryTypeMessage, Message: laneUser("after", 2)}),
			session.SetValue(session.BranchTip("main"), new("after")),
		)
		lane, _ := attachWatch(t, opened)
		first, err := lane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(first.Unsubscribe)
		snapshot := first.Snapshot()
		requireHarnessEqual(t, harnessEntryIDs(snapshot.Transcript), []string{"compact", "after"})
		requireHarnessEqual(t, snapshot.Lane, "main")
		requireHarnessEqual(t, snapshot.TipID, new("after"))
		requireHarnessEqual(t, snapshot.Configuration, watchConfiguration())
		requireHarnessEqual(t, snapshot.Stats.MessageCount, 1)
		requireHarnessEqual(t, snapshot.Operation, (*agentharness.LaneSnapshotOperation)(nil))
		requireHarnessEqual(t, snapshot.Queues, []agentharness.LaneQueuedItem{})
		requireHarnessEqual(t, snapshot.Faulted, false)
		snapshot.Transcript = snapshot.Transcript[:0]
		snapshot.TipID = nil
		second, err := lane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(second.Unsubscribe)
		requireHarnessEqual(t, harnessEntryIDs(second.Snapshot().Transcript), []string{"compact", "after"})
		requireHarnessEqual(t, second.Snapshot().TipID, new("after"))
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:146
	t.Run("dereferences queues, pending writes, and deferred handles", func(t *testing.T) {
		opened := watchSession(t, nil)
		handle := ai.DeferredHandle{Provider: "provider", ModelID: "model", API: "test", ID: "deferred"}
		source := watchAssistant([]ai.AssistantContentBlock{}, ai.StopReasonDeferred)
		source.Assistant.Deferred = &handle
		meta := watchMeta(opened)
		state := session.OperationState{OperationScope: restoreScope(), At: session.AtDeferredSuspended, StepID: "step", SourceEntryID: "source", Poll: 3, Configuration: watchConfiguration()}
		state.Control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 2}
		writes := []session.Write{session.InsertEntry(session.Entry{ID: "source", Type: session.EntryTypeMessage, Message: source})}
		inbox := []session.InboxItem{{EntryID: "next", Kind: "nextRun"}, {EntryID: "steer", Kind: "steer"}, {EntryID: "follow", Kind: "followUp"}, {EntryID: "write", Kind: "write"}}
		want := []agentharness.LaneQueuedItem{}
		for _, item := range inbox {
			pending := session.PendingEntry{Type: "message", Message: laneUser(item.EntryID, 1)}
			queued := agentharness.LaneQueuedItem{EntryID: item.EntryID, Kind: item.Kind, Type: "message", Message: pending.Message}
			if item.Kind == "write" {
				data := any(map[string]any{"id": item.EntryID})
				pending = session.PendingEntry{Type: "custom", CustomType: "note", CustomPayload: &data}
				queued = agentharness.LaneQueuedItem{EntryID: item.EntryID, Kind: item.Kind, Type: "custom", CustomType: "note", HasData: true, Data: data}
			}
			writes = append(writes, session.SetValue(session.PendingEntryValue(item.EntryID), pending))
			want = append(want, queued)
		}
		writes = append(writes,
			session.SetValue(session.OperationMetaValue(meta.OperationID), meta),
			session.SetValue(session.OperationStateValue(meta.OperationID), state),
			session.SetValue(session.BranchTip("main"), new("source")),
			session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &meta.OperationID, Inbox: inbox}),
		)
		harnessCommit(t, opened, writes...)
		lane, _ := attachWatch(t, opened)
		watch, err := lane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(watch.Unsubscribe)
		snapshot := watch.Snapshot()
		requireHarnessEqual(t, snapshot.Queues, want)
		if snapshot.Operation == nil {
			t.Fatal("missing operation")
		}
		requireHarnessEqual(t, snapshot.Operation.ID, meta.OperationID)
		requireHarnessEqual(t, snapshot.Operation.Status, agentharness.OperationStatusAborting)
		requireHarnessEqual(t, snapshot.Operation.Deferred, &agentharness.LaneSnapshotDeferred{Handle: handle, Poll: 3})
		requireHarnessEqual(t, snapshot.Operation.RunningTools, []agentharness.LaneSnapshotTool{})
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:218
	t.Run("omits streaming presentation when an effect-pending response has no frames", func(t *testing.T) {
		opened := watchSession(t, nil)
		state := progressAssistantState("response-without-frames")
		state.GenerationContext.Configuration = watchConfiguration()
		state.GenerationContext.RetryPolicy.BaseDelayMs = 0
		storeCurrentOperation(t, opened, watchMeta(opened), state)
		lane, _ := attachWatch(t, opened)
		watch, err := lane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(watch.Unsubscribe)
		operation := watch.Snapshot().Operation
		if operation == nil {
			t.Fatal("missing effect-pending operation")
		}
		requireHarnessEqual(t, operation.StreamingMessage, (*agent.AssistantMessage)(nil))
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:260
	t.Run("reduces assistant frames and projects running and settled tools with full-content indexes", func(t *testing.T) {
		frameSession := watchSession(t, nil)
		partial := watchAssistant([]ai.AssistantContentBlock{}, ai.StopReasonPending).Assistant.LLMMessage()
		frames := []ai.AssistantMessageFrame{ai.StartFrame{Partial: partial}, ai.TextStartFrame{ContentIndex: 0, Content: ai.TextContent{Text: ""}}, ai.TextDeltaFrame{ContentIndex: 0, Delta: "partial"}}
		meta := watchMeta(frameSession)
		state := progressAssistantState("response")
		state.GenerationContext.Configuration = watchConfiguration()
		state.GenerationContext.RetryPolicy.BaseDelayMs = 0
		writes := []session.Write{session.SetValue(session.OperationMetaValue(meta.OperationID), meta), session.SetValue(session.OperationStateValue(meta.OperationID), state)}
		for _, frame := range frames {
			writes = append(writes, session.AppendList(session.PendingAssistantFrames(meta.OperationID, "response"), frame))
		}
		writes = append(writes, session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &meta.OperationID, Inbox: []session.InboxItem{}}))
		harnessCommit(t, frameSession, writes...)
		frameLane, _ := attachWatch(t, frameSession)
		frameWatch, err := frameLane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(frameWatch.Unsubscribe)
		if frameWatch.Snapshot().Operation == nil || frameWatch.Snapshot().Operation.StreamingMessage == nil {
			t.Fatal("missing streamingMessage")
		}
		requireHarnessEqual(t, frameWatch.Snapshot().Operation.StreamingMessage.Content, []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}})

		toolSession := watchSession(t, nil)
		toolUsage := &ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10}
		assistant := watchAssistant([]ai.AssistantContentBlock{ai.TextContent{Text: "before"},
			ai.ToolCall{ID: "call-completed", Name: "completed", Arguments: ai.JsonObject{"source": "completed"}},
			ai.ToolCall{ID: "call-running", Name: "read", Arguments: ai.JsonObject{"source": "running"}},
			ai.ToolCall{ID: "call-without-checkpoint", Name: "write", Arguments: ai.JsonObject{"source": "without-checkpoint"}},
			ai.ToolCall{ID: "call-ready", Name: "real", Arguments: ai.JsonObject{"source": "real"}},
			ai.ToolCall{ID: "call-synthetic", Name: "missing", Arguments: ai.JsonObject{"source": "synthetic"}},
			ai.ToolCall{ID: "call-planned", Name: "planned", Arguments: ai.JsonObject{"source": "planned"}},
		}, ai.StopReasonStop)
		meta = watchMeta(toolSession)
		state = session.OperationState{OperationScope: restoreScope(), At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: watchConfiguration(), TurnID: "turn", Calls: []session.ToolCall{
			{Status: "completed", SourceIndex: 1, ResultEntryID: "completed"},
			{Status: "effect_pending", SourceIndex: 2, ResultEntryID: "result", Replay: "safe"},
			{Status: "effect_pending", SourceIndex: 3, ResultEntryID: "without-checkpoint", Replay: "never"},
			{Status: "outcome_ready", SourceIndex: 4, ResultEntryID: "ready", Terminate: true},
			{Status: "outcome_ready", SourceIndex: 5, ResultEntryID: "synthetic"},
			{Status: "planned", SourceIndex: 6, ResultEntryID: "planned"},
		}}}
		result := func(id, name, text string, timestamp int64, isError bool) agent.AgentMessage {
			return agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: id, ToolName: name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Timestamp: timestamp, IsError: isError}}
		}
		ready := result("call-ready", "real", "settled", 3, false)
		ready.ToolResult.Details = map[string]any{"kind": "real"}
		ready.ToolResult.Usage = toolUsage
		harnessCommit(t, toolSession,
			session.InsertEntry(session.Entry{ID: "assistant", Type: session.EntryTypeMessage, Message: assistant}),
			session.SetValue(session.OperationMetaValue(meta.OperationID), meta),
			session.SetValue(session.OperationStateValue(meta.OperationID), state),
			session.InsertEntry(session.Entry{ID: "completed", ParentID: new("assistant"), Type: session.EntryTypeMessage, Message: result("call-completed", "completed", "completed", 2, false)}),
			session.SetValue(session.OperationToolArgs(meta.OperationID, "turn", 2), map[string]any{"path": "file"}),
			session.SetValue(session.OperationToolArgs(meta.OperationID, "turn", 3), map[string]any{"path": "output"}),
			session.SetValue(session.OperationToolArgs(meta.OperationID, "turn", 4), map[string]any{"path": "settled"}),
			session.SetValue(session.PendingToolOutput(meta.OperationID, "result"), harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"bytes": 1}}),
			session.SetValue(session.PendingEntryValue("ready"), session.PendingEntry{Type: "message", Message: ready}),
			session.SetValue(session.PendingEntryValue("synthetic"), session.PendingEntry{Type: "message", Message: result("call-synthetic", "missing", "unavailable", 4, true)}),
			session.SetValue(session.BranchTip("main"), new("completed")),
			session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &meta.OperationID, Inbox: []session.InboxItem{}}),
		)
		toolLane, _ := attachWatch(t, toolSession)
		toolWatch, err := toolLane.Watch(ctx)
		requireHarnessOK(t, err)
		t.Cleanup(toolWatch.Unsubscribe)
		snapshot := toolWatch.Snapshot()
		requireHarnessEqual(t, harnessEntryIDs(snapshot.Transcript), []string{"assistant", "completed"})
		if snapshot.Operation == nil {
			t.Fatal("missing tool operation")
		}
		requireHarnessEqual(t, snapshot.Operation.RunningTools, []agentharness.LaneSnapshotTool{
			{Status: "running", ToolCallID: "call-running", ToolName: "read", Args: map[string]any{"path": "file"}, Result: &harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"bytes": 1}}},
			{Status: "running", ToolCallID: "call-without-checkpoint", ToolName: "write", Args: map[string]any{"path": "output"}},
			{Status: "settled", ToolCallID: "call-ready", ToolName: "real", Args: map[string]any{"path": "settled"}, Result: &harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "settled"}}, Details: map[string]any{"kind": "real"}, Usage: toolUsage, Terminate: new(true)}},
			{Status: "settled", ToolCallID: "call-synthetic", ToolName: "missing", Args: ai.JsonObject{"source": "synthetic"}, Result: &harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "unavailable"}}}, IsError: true},
		})
		requireHarnessEqual(t, snapshot.Operation.RunningTools[1].Result, (*harness.AgentToolResult)(nil))
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:460
	t.Run("faults missing or mismatched staged outcome-ready results", func(t *testing.T) {
		for _, corruption := range []string{"missing", "mismatched"} {
			t.Run(corruption, func(t *testing.T) {
				opened := watchSession(t, nil)
				meta := watchMeta(opened)
				state := session.OperationState{OperationScope: restoreScope(), At: session.AtTools, Batch: session.ToolBatch{AssistantEntryID: "assistant", Configuration: watchConfiguration(), TurnID: "turn", Calls: []session.ToolCall{{Status: "outcome_ready", SourceIndex: 0, ResultEntryID: "result"}}}}
				writes := []session.Write{
					session.InsertEntry(session.Entry{ID: "assistant", Type: session.EntryTypeMessage, Message: watchAssistant([]ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read", Arguments: ai.JsonObject{"path": "file"}}}, ai.StopReasonStop)}),
					session.SetValue(session.OperationMetaValue(meta.OperationID), meta),
					session.SetValue(session.OperationStateValue(meta.OperationID), state),
					session.SetValue(session.BranchTip("main"), new("assistant")),
					session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &meta.OperationID, Inbox: []session.InboxItem{}}),
				}
				if corruption == "mismatched" {
					writes = append(writes, session.SetValue(session.PendingEntryValue("result"), session.PendingEntry{Type: "message", Message: agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "other-call", ToolName: "read", Content: []ai.ToolResultMessageContent{}, Timestamp: 2}}}))
				}
				harnessCommit(t, opened, writes...)
				lane, _ := attachWatch(t, opened)
				_, err := lane.Watch(ctx)
				requireHarnessFault(t, err)
			})
		}
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:519
	t.Run("faults required payload corruption and unsubscribes the incomplete watcher", func(t *testing.T) {
		opened := watchSession(t, nil)
		harnessCommit(t, opened, session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{{EntryID: "missing", Kind: "nextRun"}}}))
		lane, _ := attachWatch(t, opened)
		unsubscribed := false
		original := lane.installWatch
		lane.installWatch = func(ctx harness.Context, snapshot agentharness.LaneSnapshot, filter func(agentharness.HarnessEvent) bool, capture agentharness.ResnapshotCapture[agentharness.LaneSnapshot]) (agentharness.WatchHandle[agentharness.LaneSnapshot], error) {
			handle, err := original(ctx, snapshot, filter, capture)
			if err != nil {
				return nil, err
			}
			return &watchUnsubscribeProbe{WatchHandle: handle, unsubscribed: &unsubscribed}, nil
		}
		_, err := lane.Watch(ctx)
		requireHarnessFault(t, err)
		requireHarnessEqual(t, unsubscribed, true)
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:548
	t.Run("returns snapshot-before plus buffered events when watch wins the lane line", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			storage := &watchBlockingScan{Storage: session.NewMemoryStorage(nil), started: make(chan struct{}), release: make(chan struct{})}
			opened := watchSession(t, storage)
			harnessCommit(t, opened, session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeCustom, CustomType: "root"}), session.SetValue(session.BranchTip("main"), new("root")))
			lane, _ := attachWatch(t, opened)
			storage.block = true
			var watch agentharness.WatchHandle[agentharness.LaneSnapshot]
			var watchErr error
			watchDone := make(chan struct{})
			go func() { defer close(watchDone); watch, watchErr = lane.Watch(ctx) }()
			<-storage.started
			sourceKey := harness.CreateContextKey[string]("watch.event.source")
			sourceContext := harness.WithContextValue(ctx, sourceKey, "append")
			appendDone := make(chan error, 1)
			go func() { _, err := lane.AppendMessage(sourceContext, laneUser("later", 2)); appendDone <- err }()
			close(storage.release)
			<-watchDone
			requireHarnessOK(t, watchErr)
			t.Cleanup(watch.Unsubscribe)
			requireHarnessEqual(t, harnessEntryIDs(watch.Snapshot().Transcript), []string{"root"})
			var mu sync.Mutex
			seen := []agentharness.HarnessEventType{}
			sameContext := []bool{}
			requireHarnessOK(t, watch.Start(func(eventContext harness.Context, event agentharness.HarnessEvent) error {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, event.Type())
				sameContext = append(sameContext, eventContext == sourceContext)
				return nil
			}))
			requireHarnessOK(t, <-appendDone)
			synctest.Wait()
			mu.Lock()
			defer mu.Unlock()
			requireHarnessEqual(t, seen, []agentharness.HarnessEventType{agentharness.EventMessageStart, agentharness.EventMessageEnd, agentharness.EventEntryAdded})
			requireHarnessEqual(t, sameContext, []bool{true, true, true})
		})
	})
	// upstream: packages/agent/test/harness/runtime/watch.test.ts:576
	t.Run("returns snapshot-after without replay when publication wins", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			opened := watchSession(t, nil)
			lane, _ := attachWatch(t, opened)
			entryID, err := lane.AppendMessage(ctx, laneUser("existing", 1))
			requireHarnessOK(t, err)
			watch, err := lane.Watch(ctx)
			requireHarnessOK(t, err)
			t.Cleanup(watch.Unsubscribe)
			seen := 0
			requireHarnessOK(t, watch.Start(func(harness.Context, agentharness.HarnessEvent) error { seen++; return nil }))
			synctest.Wait()
			requireHarnessEqual(t, harnessEntryIDs(watch.Snapshot().Transcript), []string{entryID})
			requireHarnessEqual(t, seen, 0)
		})
	})
}

type watchUnsubscribeProbe struct {
	agentharness.WatchHandle[agentharness.LaneSnapshot]
	unsubscribed *bool
}

func (probe *watchUnsubscribeProbe) Unsubscribe() {
	*probe.unsubscribed = true
	probe.WatchHandle.Unsubscribe()
}

type watchBlockingScan struct {
	session.Storage
	block   bool
	started chan struct{}
	release chan struct{}
}

func (storage *watchBlockingScan) ScanBranch(ctx context.Context, query session.StorageBranchScan) ([]session.Entry, error) {
	if storage.block {
		close(storage.started)
		<-storage.release
	}
	return storage.Storage.ScanBranch(ctx, query)
}
