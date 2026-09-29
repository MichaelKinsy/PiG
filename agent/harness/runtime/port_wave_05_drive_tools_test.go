package runtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type toolFixture struct {
	*procedureFixture
	assistantEntryID string
	operationID      string
	resultEntryIDs   []string
}
type toolFixtureOptions struct {
	calls       []string
	tools       []harness.AgentHarnessTool
	mode        string
	stopReason  ai.StopReason
	callStates  func([]string) []session.ToolCall
	extraWrites func(*toolFixture, session.OperationState) []session.Write
	toolContext ToolContextFactory
	cancelled   bool
	onEmit      func([]agentharness.HarnessEvent) error
}

type observedToolStorage struct {
	*session.MemoryStorage
	observe func(string)
}

func (storage *observedToolStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	result, err := storage.MemoryStorage.Commit(ctx, writes)
	if err != nil {
		return result, err
	}
	intent, outcome, replay := false, false, false
	for _, write := range writes {
		switch value := write.(type) {
		case session.ValueSetWrite:
			intent = intent || value.Namespace == "pi.op.tool_args"
			outcome = outcome || value.Namespace == "pi.pending.entry"
		case session.ValueDeleteWrite:
			replay = replay || value.Namespace == "pi.pending.tool_output"
		}
	}
	if storage.observe != nil {
		if intent {
			storage.observe("intent_commit")
		}
		if outcome {
			storage.observe("outcome_commit")
		}
		if !outcome && replay {
			storage.observe("replay_commit")
		}
	}
	return result, nil
}

func newToolFixture(t *testing.T, options toolFixtureOptions) *toolFixture {
	t.Helper()
	backend := &observedToolStorage{MemoryStorage: session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 100 }})}
	fixture := &toolFixture{procedureFixture: newProcedureFixture(t, backend)}
	backend.observe = func(value string) {
		fixture.mu.Lock()
		fixture.observations = append(fixture.observations, value)
		fixture.mu.Unlock()
	}
	operationID := fixture.session.IdGenerator().Next(new(int64(10)))
	fixture.operationID = operationID
	fixture.assistantEntryID = fixture.session.IdGenerator().Next(new(int64(20)))
	configuration := session.LaneConfiguration{Model: session.ModelRef{Provider: "faux", ModelID: "faux-1"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: options.calls}
	blocks := []ai.AssistantContentBlock{}
	calls := []session.ToolCall{}
	for index, name := range options.calls {
		id := fixture.session.IdGenerator().Next(new(int64(20)))
		fixture.resultEntryIDs = append(fixture.resultEntryIDs, id)
		blocks = append(blocks, ai.ToolCall{ID: "call-" + string(rune('0'+index)), Name: name, Arguments: ai.JsonObject{"value": name}})
		calls = append(calls, session.ToolCall{Status: session.ToolCallPlanned, SourceIndex: index, ResultEntryID: id})
	}
	if options.callStates != nil {
		calls = options.callStates(fixture.resultEntryIDs)
	}
	if options.mode == "" {
		options.mode = "parallel"
	}
	if options.stopReason == "" {
		options.stopReason = ai.StopReasonToolUse
	}
	fixture.config.Tools = options.tools
	fixture.config.ToolContext = options.toolContext
	fixture.config.ToolExecution = agent.ToolExecutionMode(options.mode)
	fixture.config.RetryPolicy = ai.RetryPolicy{}
	fixture.onEmit = options.onEmit
	control := session.Control{Status: session.ControlRunning}
	if options.cancelled {
		control = session.Control{Status: session.ControlCancelRequested, RequestedAt: 30}
	}
	run := session.OperationState{At: session.AtTools, OperationScope: session.OperationScope{Control: control, Settings: session.RunSettings{Compaction: fixture.config.Compaction, SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: options.mode}, LatestAssistantEntryID: &fixture.assistantEntryID}, Batch: session.ToolBatch{AssistantEntryID: fixture.assistantEntryID, Configuration: configuration, TurnID: "turn-1", Calls: calls}}
	assistant := ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", Content: blocks, StopReason: options.stopReason, Timestamp: 20}
	writes := []session.Write{session.InsertEntry(session.Entry{ID: fixture.assistantEntryID, Type: session.EntryTypeMessage, Message: agent.AgentMessage{Assistant: new(runtimeAssistantMessage(&assistant))}}), session.SetValue(session.BranchTip("main"), &fixture.assistantEntryID), session.SetValue(session.LaneConfig("main"), configuration), session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &operationID, Inbox: []session.InboxItem{}}), session.SetValue(session.OperationMetaValue(operationID), session.OperationMeta{OperationID: operationID, Lane: "main", StartedAt: 10, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}), session.SetValue(session.OperationStateValue(operationID), run)}
	if options.extraWrites != nil {
		writes = append(writes, options.extraWrites(fixture, run)...)
	}
	fixture.installLane(t, operationID, writes)
	return fixture
}

func testHarnessTool(name string, execute func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error), replay string) harness.AgentHarnessTool {
	return harness.AgentHarnessTool{ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"}}}, Label: name, Replay: replay,
		Execute: func(ctx context.Context, _ string, args map[string]any, update harness.AgentHarnessToolUpdateCallback, _ any, invocation harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			return execute(ctx, args["value"].(string), update, invocation)
		},
	}
}
func toolTextResult(value string) harness.AgentToolResult {
	return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: value}}, Details: map[string]any{"value": value}}
}
func (fixture *toolFixture) run(t *testing.T) ProcedureResult {
	t.Helper()
	result, err := RunTools(t.Context(), fixture.lane, fixture.drive, fixture.currentRun(t))
	require.NoError(t, err)
	return result
}
func (fixture *toolFixture) transcriptIDs(t *testing.T) []string {
	t.Helper()
	entries, err := fixture.lane.FindEntries(t.Context(), &session.BranchScan{Order: session.OrderOldestFirst})
	require.NoError(t, err)
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}
func (fixture *toolFixture) requireResult(t *testing.T, index int) agent.ToolResultMessage {
	t.Helper()
	entry := requireEntry(t, fixture.procedureFixture, fixture.resultEntryIDs[index])
	require.NotNil(t, entry.Message.ToolResult)
	return *entry.Message.ToolResult
}
func requireBefore(t *testing.T, values []string, first, second string) {
	t.Helper()
	require.NotEqual(t, -1, slices.Index(values, first))
	require.NotEqual(t, -1, slices.Index(values, second))
	require.Less(t, slices.Index(values, first), slices.Index(values, second))
}

func TestPortWave05DurableToolBatch(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:279
	t.Run("executes a sequential batch with memos, checkpoints, hooks, usage, and source-order placement", func(t *testing.T) {
		var contextResolutions int
		var lateInvocation harness.AgentHarnessToolInvocation
		usage := &ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
		first := testHarnessTool("first", func(ctx context.Context, value string, update harness.AgentHarnessToolUpdateCallback, invocation harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			lateInvocation = invocation
			memo := any(map[string]any{"value": "memo"})
			if err := invocation.SetMemo(ctx, "step/a", &memo); err != nil {
				return harness.AgentToolResult{}, err
			}
			got, ok, err := invocation.GetMemo(ctx, "step/a")
			assert.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, memo, got)
			update(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"progress": "partial"}}, harness.AgentHarnessToolUpdateOptions{Checkpoint: true})
			result := toolTextResult(value)
			result.Usage = usage
			return result, nil
		}, "never")
		second := testHarnessTool("second", func(_ context.Context, value string, _ harness.AgentHarnessToolUpdateCallback, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			return toolTextResult(value), nil
		}, "never")
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"first", "second"}, tools: []harness.AgentHarnessTool{first, second}, mode: "sequential", toolContext: func(context.Context) (any, error) { contextResolutions++; return nil, nil }})
		_, err := fixture.hooks.OnBeforeTool(func(_ context.Context, event agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
			if event.ToolName == "first" {
				return &agentharness.BeforeToolResult{Args: map[string]any{"value": "prepared"}}, nil
			}
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		checkpointSeen := false
		_, err = fixture.hooks.OnAfterTool(func(ctx context.Context, event agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			if event.ToolName == "first" {
				stored, err := session.GetValue(ctx, fixture.session, session.PendingToolOutput(fixture.drive.OperationID, fixture.resultEntryIDs[0]))
				checkpointSeen = stored != nil
				return nil, err
			}
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		require.Equal(t, ProcedureResult{Kind: ProcedureContinue}, fixture.run(t))
		require.Equal(t, append([]string{fixture.assistantEntryID}, fixture.resultEntryIDs...), fixture.transcriptIDs(t))
		message := fixture.requireResult(t, 0)
		require.Equal(t, "toolResult", message.Role)
		require.Equal(t, []ai.ToolResultMessageContent{ai.TextContent{Text: "prepared"}}, message.Content)
		require.Equal(t, 1, contextResolutions)
		require.True(t, checkpointSeen)
		run := fixture.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		require.Equal(t, session.ContinuationNeedAssistant, run.Continuation.Kind)
		require.Equal(t, []string{"first", "second"}, fixture.lane.SnapshotState().Configuration.ActiveToolNames)
		args, err := session.ScanValues(t.Context(), fixture.session, session.OperationToolArgsPrefix(fixture.drive.OperationID, nil))
		require.NoError(t, err)
		require.Empty(t, args)
		memos, err := session.ScanValues(t.Context(), fixture.session, session.OperationToolMemoPrefix(fixture.drive.OperationID, nil))
		require.NoError(t, err)
		require.Empty(t, memos)
		requireNoValue(t, fixture.procedureFixture, session.PendingToolOutput(fixture.drive.OperationID, fixture.resultEntryIDs[0]))
		late := any(true)
		require.ErrorContains(t, lateInvocation.SetMemo(t.Context(), "late", &late), "no longer owns")
		types := fixture.eventTypes()
		require.Less(t, slices.Index(types, agentharness.EventToolUpdate), slices.Index(types, agentharness.EventToolEnd))
		for _, pair := range [][2]string{{"intent_commit", "tool_start"}, {"tool_start", "tool_update"}, {"tool_update", "outcome_commit"}, {"outcome_commit", "tool_end"}, {"tool_end", "entry_added"}} {
			requireBefore(t, fixture.observations, pair[0], pair[1])
		}
		require.Len(t, fixture.eventsOf(agentharness.EventEntryAdded), 2)
		require.Len(t, fixture.eventsOf(agentharness.EventUsage), 1)
		require.Equal(t, agentharness.EventTurnEnd, types[len(types)-1])
		fixture.requireRestores(t)
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:380
	t.Run("stages parallel completion order but materializes source order", func(t *testing.T) {
		finishA, finishB := make(chan struct{}), make(chan struct{})
		releaseA, releaseB := sync.OnceFunc(func() { close(finishA) }), sync.OnceFunc(func() { close(finishB) })
		started := make(chan string, 2)
		endedB := make(chan struct{})
		makeTool := func(name string, finish <-chan struct{}) harness.AgentHarnessTool {
			return testHarnessTool(name, func(ctx context.Context, _ string, _ harness.AgentHarnessToolUpdateCallback, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
				started <- name
				select {
				case <-finish:
					return toolTextResult(name), nil
				case <-ctx.Done():
					return harness.AgentToolResult{}, context.Cause(ctx)
				}
			}, "never")
		}
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"a", "b"}, tools: []harness.AgentHarnessTool{makeTool("a", finishA), makeTool("b", finishB)}, mode: "parallel", onEmit: func(events []agentharness.HarnessEvent) error {
			for _, event := range events {
				if payload, ok := event.Payload.(agentharness.ToolEndPayload); ok && payload.ToolName == "b" {
					close(endedB)
				}
			}
			return nil
		}})
		run := fixture.currentRun(t)
		done := runProcedureAsync(t, fixture.procedureFixture, func() (ProcedureResult, error) { return RunTools(t.Context(), fixture.lane, fixture.drive, run) }, releaseA, releaseB)
		<-started
		<-started
		releaseB()
		<-endedB
		calls := fixture.currentRun(t).Batch.Calls
		require.Equal(t, []string{"effect_pending", "outcome_ready"}, []string{calls[0].Status, calls[1].Status})
		entry, err := fixture.session.GetEntry(t.Context(), fixture.resultEntryIDs[1])
		require.NoError(t, err)
		require.Nil(t, entry)
		start := requireToolEvent(t, fixture.procedureFixture, agentharness.EventToolStart, "call-1").Payload.(agentharness.ToolStartPayload)
		require.Equal(t, map[string]any{"value": "b"}, start.Args)
		end := requireToolEvent(t, fixture.procedureFixture, agentharness.EventToolEnd, "call-1").Payload.(agentharness.ToolEndPayload)
		require.Equal(t, []ai.ToolResultMessageContent{ai.TextContent{Text: "b"}}, end.Result.Content)
		releaseA()
		require.NoError(t, done())
		require.Equal(t, []string{fixture.assistantEntryID, fixture.resultEntryIDs[0], fixture.resultEntryIDs[1]}, fixture.transcriptIDs(t))
		fixture.requireRestores(t)
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:419
	t.Run("safe-replays persisted arguments and memos while interrupting unsafe effects", func(t *testing.T) {
		var safeCalls, unsafeCalls atomic.Int32
		var safeValue string
		safe := testHarnessTool("safe", func(ctx context.Context, value string, _ harness.AgentHarnessToolUpdateCallback, invocation harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			safeCalls.Add(1)
			safeValue = value
			assert.NotEmpty(t, invocation.InvocationID())
			memo, ok, err := invocation.GetMemo(ctx, "step/a")
			assert.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, map[string]any{"complete": true}, memo)
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "safe replay"}}, Details: map[string]any{"value": "safe"}}, nil
		}, "safe")
		unsafe := testHarnessTool("unsafe", func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			unsafeCalls.Add(1)
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "must not run"}}, Details: map[string]any{}}, nil
		}, "never")
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"safe", "unsafe"}, tools: []harness.AgentHarnessTool{safe, unsafe}, callStates: func(ids []string) []session.ToolCall {
			return []session.ToolCall{{Status: "effect_pending", SourceIndex: 0, ResultEntryID: ids[0], Replay: "safe"}, {Status: "effect_pending", SourceIndex: 1, ResultEntryID: ids[1], Replay: "never"}}
		}, extraWrites: func(f *toolFixture, _ session.OperationState) []session.Write {
			return []session.Write{
				session.SetValue(session.OperationToolArgs(f.operationID, "turn-1", 0), map[string]any{"value": "persisted"}),
				session.SetValue(session.OperationToolArgs(f.operationID, "turn-1", 1), map[string]any{"value": "unsafe"}),
				session.SetValue(session.OperationToolMemo(f.operationID, f.resultEntryIDs[0], "step/a"), any(map[string]any{"complete": true})),
				session.SetValue(session.PendingToolOutput(f.operationID, f.resultEntryIDs[0]), harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "old progress"}}, Details: map[string]any{}}),
				session.SetValue(session.PendingToolOutput(f.operationID, f.resultEntryIDs[1]), harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "durable partial"}}, Details: map[string]any{"progress": "kept"}}),
			}
		}})
		fixture.run(t)
		require.Equal(t, int32(1), safeCalls.Load())
		require.Equal(t, "persisted", safeValue)
		require.Zero(t, unsafeCalls.Load())
		starts := fixture.eventsOf(agentharness.EventToolStart)
		require.Len(t, starts, 1)
		require.True(t, starts[0].Recovery)
		require.Equal(t, map[string]any{"value": "persisted"}, starts[0].Payload.(agentharness.ToolStartPayload).Args)
		require.Equal(t, "call-0", starts[0].Payload.(agentharness.ToolStartPayload).ToolCallID)
		requireBefore(t, fixture.observations, "replay_commit", "tool_start")
		for _, id := range []string{"call-0", "call-1"} {
			event := requireToolEvent(t, fixture.procedureFixture, agentharness.EventToolEnd, id)
			payload := event.Payload.(agentharness.ToolEndPayload)
			require.True(t, event.Recovery)
			require.Equal(t, id == "call-1", payload.IsError)
		}
		message := fixture.requireResult(t, 1)
		require.Equal(t, "toolResult", message.Role)
		require.True(t, message.IsError)
		require.Equal(t, map[string]any{"progress": "kept"}, message.Details)
		require.Contains(t, message.Content[len(message.Content)-1].(ai.TextContent).Text, "external outcome is unknown")
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:483
	t.Run("reconciles a restored cancelled batch without hooks, context, or effects", func(t *testing.T) {
		var executes, contexts, before, after atomic.Int32
		execute := func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			executes.Add(1)
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}, nil
		}
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"planned", "pending"}, tools: []harness.AgentHarnessTool{testHarnessTool("planned", execute, "never"), testHarnessTool("pending", execute, "safe")}, toolContext: func(context.Context) (any, error) { contexts.Add(1); return nil, nil }, cancelled: true, callStates: func(ids []string) []session.ToolCall {
			return []session.ToolCall{{Status: "planned", SourceIndex: 0, ResultEntryID: ids[0]}, {Status: "effect_pending", SourceIndex: 1, ResultEntryID: ids[1], Replay: "safe"}}
		}, extraWrites: func(f *toolFixture, _ session.OperationState) []session.Write {
			return []session.Write{
				session.SetValue(session.OperationToolArgs(f.operationID, "turn-1", 1), map[string]any{"value": "pending"}),
				session.SetValue(session.PendingToolOutput(f.operationID, f.resultEntryIDs[1]), harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "checkpoint"}}, Details: map[string]any{}}),
			}
		}})
		_, err := fixture.hooks.OnBeforeTool(func(context.Context, agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
			before.Add(1)
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		_, err = fixture.hooks.OnAfterTool(func(context.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			after.Add(1)
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		fixture.run(t)
		require.Zero(t, executes.Load())
		require.Zero(t, contexts.Load())
		require.Zero(t, before.Load())
		require.Zero(t, after.Load())
		run := fixture.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		require.Equal(t, session.ControlCancelRequested, run.Control.Status)
		require.Equal(t, session.ContinuationNeedAssistant, run.Continuation.Kind)
		pending := fixture.requireResult(t, 1)
		require.Contains(t, pending.Content[len(pending.Content)-1].(ai.TextContent).Text, "external outcome is unknown")
		starts := fixture.eventsOf(agentharness.EventToolStart)
		require.Len(t, starts, 1)
		start := starts[0].Payload.(agentharness.ToolStartPayload)
		require.Equal(t, "planned", start.ToolName)
		require.Equal(t, ai.JsonObject{"value": "planned"}, start.Args)
		ends := fixture.eventsOf(agentharness.EventToolEnd)
		require.Len(t, ends, 2)
		require.True(t, requireToolEvent(t, fixture.procedureFixture, agentharness.EventToolEnd, start.ToolCallID).Payload.(agentharness.ToolEndPayload).IsError)
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:532
	t.Run("materializes outcome-ready state without resolving tools or tool context", func(t *testing.T) {
		var executes, contexts atomic.Int32
		tool := testHarnessTool("ready", func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			executes.Add(1)
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}, nil
		}, "never")
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"ready"}, tools: []harness.AgentHarnessTool{tool}, toolContext: func(context.Context) (any, error) { contexts.Add(1); return nil, nil }, callStates: func(ids []string) []session.ToolCall {
			return []session.ToolCall{{Status: "outcome_ready", SourceIndex: 0, ResultEntryID: ids[0], Terminate: true}}
		}, extraWrites: func(f *toolFixture, _ session.OperationState) []session.Write {
			return []session.Write{session.SetValue(session.PendingEntryValue(f.resultEntryIDs[0]), session.PendingEntry{Type: "message", Message: agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call-0", ToolName: "ready", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "already done"}}, Timestamp: 30}}})}
		}})
		fixture.run(t)
		require.Zero(t, executes.Load())
		require.Zero(t, contexts.Load())
		run := fixture.currentRun(t)
		require.Equal(t, session.AtCheckpoint, run.At)
		require.Equal(t, session.Continuation{Kind: "may_finish", IncludeFinalAssistant: false}, run.Continuation)
		require.Equal(t, []string{"ready"}, fixture.lane.SnapshotState().Configuration.ActiveToolNames)
		events := fixture.eventSnapshot()
		require.Equal(t, agentharness.HarnessEvent{Lane: "main", Recovery: true, Payload: agentharness.TurnStartPayload{RunID: fixture.drive.OperationID, TurnID: "turn-1"}}, events[0])
		require.Equal(t, agentharness.EventTurnEnd, events[len(events)-1].Type())
		require.True(t, events[len(events)-1].Recovery)
		require.Equal(t, "turn-1", events[len(events)-1].Payload.(agentharness.TurnEndPayload).TurnID)
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:568
	t.Run("never executes genuine-length or missing tool calls", func(t *testing.T) {
		var executes, truncatedAfter, missingAfter atomic.Int32
		tool := testHarnessTool("present", func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			executes.Add(1)
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}, nil
		}, "never")
		truncated := newToolFixture(t, toolFixtureOptions{calls: []string{"present"}, tools: []harness.AgentHarnessTool{tool}, stopReason: ai.StopReasonLength})
		_, err := truncated.hooks.OnAfterTool(func(context.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			truncatedAfter.Add(1)
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		truncated.run(t)
		require.Zero(t, executes.Load())
		require.Contains(t, truncated.requireResult(t, 0).Content[0].(ai.TextContent).Text, "arguments may be truncated")
		require.Zero(t, truncatedAfter.Load())
		require.Len(t, truncated.eventsOf(agentharness.EventToolStart), 1)
		require.Len(t, truncated.eventsOf(agentharness.EventToolEnd), 1)
		for _, pair := range [][2]string{{"outcome_commit", "tool_start"}, {"tool_start", "tool_end"}, {"tool_end", "entry_added"}} {
			requireBefore(t, truncated.observations, pair[0], pair[1])
		}
		missing := newToolFixture(t, toolFixtureOptions{calls: []string{"missing"}, tools: []harness.AgentHarnessTool{}})
		_, err = missing.hooks.OnAfterTool(func(context.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			missingAfter.Add(1)
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		missing.run(t)
		message := missing.requireResult(t, 0)
		require.Equal(t, "toolResult", message.Role)
		require.True(t, message.IsError)
		require.Equal(t, []ai.ToolResultMessageContent{ai.TextContent{Text: `Tool "missing" is unavailable`}}, message.Content)
		require.NotContains(t, jsonObject(t, message), "details")
		require.Zero(t, missingAfter.Load())
		starts := missing.eventsOf(agentharness.EventToolStart)
		require.Len(t, starts, 1)
		require.Equal(t, ai.JsonObject{"value": "missing"}, starts[0].Payload.(agentharness.ToolStartPayload).Args)
		ends := missing.eventsOf(agentharness.EventToolEnd)
		require.Len(t, ends, 1)
		end := ends[0].Payload.(agentharness.ToolEndPayload)
		require.Equal(t, message.Content, end.Result.Content)
		require.Nil(t, end.Result.Details)
		require.True(t, end.IsError)
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:622
	t.Run("awaits update delivery and checkpoint persistence before after_tool", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			releaseUpdate, updateQueued := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(releaseUpdate) })
			var afterStarted atomic.Bool
			tool := testHarnessTool("updating", func(_ context.Context, _ string, update harness.AgentHarnessToolUpdateCallback, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
				update(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"progress": "partial"}}, harness.AgentHarnessToolUpdateOptions{Checkpoint: true})
				return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
			}, "never")
			fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"updating"}, tools: []harness.AgentHarnessTool{tool}, onEmit: func(events []agentharness.HarnessEvent) error {
				for _, event := range events {
					if event.Type() == agentharness.EventToolUpdate {
						close(updateQueued)
						<-releaseUpdate
					}
				}
				return nil
			}})
			_, err := fixture.hooks.OnAfterTool(func(ctx context.Context, _ agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
				afterStarted.Store(true)
				value, err := session.GetValue(ctx, fixture.session, session.PendingToolOutput(fixture.drive.OperationID, fixture.resultEntryIDs[0]))
				assert.NotNil(t, value)
				return nil, err
			}, agentharness.HookOptions{})
			require.NoError(t, err)
			run := fixture.currentRun(t)
			done := runProcedureAsync(t, fixture.procedureFixture, func() (ProcedureResult, error) { return RunTools(t.Context(), fixture.lane, fixture.drive, run) }, release)
			<-updateQueued
			synctest.Wait()
			require.False(t, afterStarted.Load())
			release()
			require.NoError(t, done())
			require.True(t, afterStarted.Load())
		})
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:664
	t.Run("does not stage a cancelled outcome before cancellation is durable", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var executes atomic.Int32
			tool := testHarnessTool("cancel-before-admission", func(context.Context, string, harness.AgentHarnessToolUpdateCallback, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
				executes.Add(1)
				return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}}, nil
			}, "never")
			fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"cancel-before-admission"}, tools: []harness.AgentHarnessTool{tool}, mode: "sequential"})
			cancellation := make(chan struct{})
			releaseCancel := sync.OnceFunc(func() { close(cancellation) })
			fixture.drive.BeginAbort(func() error { <-cancellation; return nil })
			run := fixture.currentRun(t)
			done := runProcedureAsync(t, fixture.procedureFixture, func() (ProcedureResult, error) { return RunTools(t.Context(), fixture.lane, fixture.drive, run) }, releaseCancel)
			synctest.Wait()
			require.Equal(t, "planned", fixture.currentRun(t).Batch.Calls[0].Status)
			requireNoValue(t, fixture.procedureFixture, session.PendingEntryValue(fixture.resultEntryIDs[0]))
			next := fixture.currentRun(t)
			next.Control = session.Control{Status: "cancel_requested", RequestedAt: 40}
			fixture.replaceRun(t, next)
			releaseCancel()
			fixture.drive.SignalAbort()
			require.NoError(t, done())
			require.Zero(t, executes.Load())
			requireEntry(t, fixture.procedureFixture, fixture.resultEntryIDs[0])
		})
	})

	// upstream: packages/agent/test/harness/runtime/drive-tools.test.ts:702
	t.Run("drains live updates before after_tool and stages a non-terminating result after cancellation", func(t *testing.T) {
		updateDelivery, started := make(chan struct{}), make(chan struct{})
		cancellation := make(chan struct{})
		releaseCancel := sync.OnceFunc(func() { close(cancellation) })
		releaseUpdate := sync.OnceFunc(func() { close(updateDelivery) })
		var updateDelivered atomic.Bool
		var afterCalls atomic.Int32
		tool := testHarnessTool("slow", func(ctx context.Context, _ string, update harness.AgentHarnessToolUpdateCallback, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			update(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"progress": "partial"}}, harness.AgentHarnessToolUpdateOptions{})
			close(started)
			<-ctx.Done()
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{}, Terminate: new(true)}, errors.New("cancelled effect")
		}, "never")
		fixture := newToolFixture(t, toolFixtureOptions{calls: []string{"slow"}, tools: []harness.AgentHarnessTool{tool}, mode: "sequential", onEmit: func(events []agentharness.HarnessEvent) error {
			for _, event := range events {
				if event.Type() == agentharness.EventToolUpdate {
					<-updateDelivery
					updateDelivered.Store(true)
				}
			}
			return nil
		}})
		_, err := fixture.hooks.OnAfterTool(func(context.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			afterCalls.Add(1)
			assert.True(t, updateDelivered.Load())
			return nil, nil
		}, agentharness.HookOptions{})
		require.NoError(t, err)
		run := fixture.currentRun(t)
		done := runProcedureAsync(t, fixture.procedureFixture, func() (ProcedureResult, error) { return RunTools(t.Context(), fixture.lane, fixture.drive, run) }, releaseCancel, releaseUpdate)
		<-started
		fixture.drive.BeginAbort(func() error { <-cancellation; return nil })
		next := fixture.currentRun(t)
		next.Control = session.Control{Status: "cancel_requested", RequestedAt: 40}
		fixture.replaceRun(t, next)
		releaseCancel()
		fixture.drive.SignalAbort()
		releaseUpdate()
		require.NoError(t, done())
		require.Zero(t, afterCalls.Load())
		entry := requireEntry(t, fixture.procedureFixture, fixture.resultEntryIDs[0])
		require.NotContains(t, jsonObject(t, entry), "terminate")
		require.NotNil(t, entry.Message.ToolResult)
		require.Equal(t, "toolResult", entry.Message.ToolResult.Role)
		require.True(t, entry.Message.ToolResult.IsError)
	})
}
