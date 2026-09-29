package runtime

import (
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func foldLaneEvents(t *testing.T, snapshot *agentharness.LaneSnapshot, events []agentharness.HarnessEvent) {
	t.Helper()
	for _, event := range events {
		if reduction := ReduceLaneSnapshot(snapshot, event); reduction == "rebase" {
			t.Fatalf("unexpected rebase for %s", event.Type())
		}
	}
}

func reducerWatch(t *testing.T, lane *Lane, events *[]agentharness.HarnessEvent) agentharness.WatchHandle[agentharness.LaneSnapshot] {
	t.Helper()
	watch, err := lane.Watch(t.Context())
	requireHarnessOK(t, err)
	t.Cleanup(watch.Unsubscribe)
	requireHarnessOK(t, watch.Start(func(_ harness.Context, event agentharness.HarnessEvent) error {
		*events = append(*events, event)
		return nil
	}))
	return watch
}

func TestPortWave06LaneSnapshotReducer(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:50
	t.Run("folds an ordinary run to the authoritative resnapshot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			events := []agentharness.HarnessEvent{}
			watch := reducerWatch(t, fixture.lane, &events)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			outcome, err := fixture.lane.Prompt(t.Context(), "question", nil)
			requireLaneRun(t, outcome, err, "completed")
			synctest.Wait()
			replica := watch.Snapshot()
			foldLaneEvents(t, &replica, events)
			authoritative, err := watch.Resnapshot(t.Context())
			requireHarnessOK(t, err)
			requireHarnessEqual(t, replica, authoritative)
		})
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:69
	t.Run("folds suspend and resume without closing the operation early", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, true, agentharness.Resources{})
			events := []agentharness.HarnessEvent{}
			watch := reducerWatch(t, fixture.lane, &events)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			outcome, err := fixture.lane.Prompt(t.Context(), "question", nil)
			requireHarnessOK(t, err)
			if outcome.Suspended == nil {
				t.Fatalf("outcome = %+v, want suspended", outcome)
			}
			requireHarnessEqual(t, outcome.Suspended.Status, "suspended")
			synctest.Wait()
			replica := watch.Snapshot()
			foldLaneEvents(t, &replica, events)
			if replica.Operation == nil || replica.Operation.Deferred == nil {
				t.Fatal("missing open deferred operation")
			}
			requireHarnessEqual(t, replica.Operation.Kind, agentharness.OperationRun)
			requireHarnessEqual(t, replica.Operation.Deferred.Poll, 0)
			authoritative, err := watch.Resnapshot(t.Context())
			requireHarnessOK(t, err)
			requireHarnessEqual(t, replica, authoritative)
			events = events[:0]
			outcome, err = fixture.lane.Resume(t.Context())
			requireLaneRun(t, outcome, err, "completed")
			synctest.Wait()
			// The Go value remains the same mutable replica across the two folds.
			foldLaneEvents(t, &replica, events)
			authoritative, err = watch.Resnapshot(t.Context())
			requireHarnessOK(t, err)
			requireHarnessEqual(t, replica, authoritative)
		})
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:98
	t.Run("folds standalone compaction and preserves segment semantics", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			_, err := fixture.lane.AppendMessage(t.Context(), laneUser("history", 1))
			requireHarnessOK(t, err)
			events := []agentharness.HarnessEvent{}
			watch := reducerWatch(t, fixture.lane, &events)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("summary")})
			outcome, err := fixture.lane.Compact(t.Context(), nil)
			requireHarnessOK(t, err)
			requireHarnessEqual(t, outcome.Compaction.Status, "completed")
			synctest.Wait()
			replica := watch.Snapshot()
			foldLaneEvents(t, &replica, events)
			requireHarnessEqual(t, replica.Operation, (*agentharness.LaneSnapshotOperation)(nil))
			if replica.LastResult == nil {
				t.Fatal("missing last result")
			}
			requireHarnessEqual(t, replica.LastResult.Kind, "compaction")
			requireHarnessEqual(t, replica.LastResult.Status, "completed")
			authoritative, err := watch.Resnapshot(t.Context())
			requireHarnessOK(t, err)
			requireHarnessEqual(t, replica, authoritative)
		})
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:121
	t.Run("replicates globally ordered queue changes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			events := []agentharness.HarnessEvent{}
			watch := reducerWatch(t, fixture.lane, &events)
			_, err := fixture.lane.NextRunText(t.Context(), "next", nil)
			requireHarnessOK(t, err)
			steer, err := fixture.lane.SteerText(t.Context(), "steer", nil)
			requireHarnessOK(t, err)
			_, err = fixture.lane.FollowUpText(t.Context(), "follow", nil)
			requireHarnessOK(t, err)
			_, err = fixture.lane.CancelQueued(t.Context(), steer)
			requireHarnessOK(t, err)
			synctest.Wait()
			replica := watch.Snapshot()
			foldLaneEvents(t, &replica, events)
			kinds := []string{}
			for _, item := range replica.Queues {
				kinds = append(kinds, item.Kind)
			}
			requireHarnessEqual(t, kinds, []string{"nextRun", "followUp"})
			authoritative, err := watch.Resnapshot(t.Context())
			requireHarnessOK(t, err)
			requireHarnessEqual(t, replica, authoritative)
		})
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:142
	t.Run("keeps in-run compaction segments inside the open run", func(t *testing.T) {
		fixture := newPublicLane(t, false, agentharness.Resources{})
		watch, err := fixture.lane.Watch(t.Context())
		requireHarnessOK(t, err)
		t.Cleanup(watch.Unsubscribe)
		running := watch.Snapshot()
		running.Operation = &agentharness.LaneSnapshotOperation{ID: "run", Kind: agentharness.OperationRun, StartedAt: 1, Status: agentharness.OperationStatusOpen, RunningTools: []agentharness.LaneSnapshotTool{}}
		requireHarnessEqual(t, ReduceLaneSnapshot(&running, agentharness.HarnessEvent{Lane: "main", Payload: agentharness.CompactionStartPayload{RunID: "run", Reason: "threshold", StartedAt: 2}}), LaneSnapshotReduction(""))
		requireHarnessEqual(t, ReduceLaneSnapshot(&running, agentharness.HarnessEvent{Lane: "main", Payload: agentharness.CompactionEndPayload{RunID: "run", Reason: "threshold", Status: "declined", EndedAt: 3}}), LaneSnapshotReduction(""))
		if running.Operation == nil {
			t.Fatal("in-run compaction closed the run")
		}
		requireHarnessEqual(t, running.Operation.ID, "run")
		requireHarnessEqual(t, running.Operation.Kind, agentharness.OperationRun)
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:178
	t.Run("retains settled parallel tools until each source-ordered result is placed", func(t *testing.T) {
		fixture := newPublicLane(t, false, agentharness.Resources{})
		watch, err := fixture.lane.Watch(t.Context())
		requireHarnessOK(t, err)
		t.Cleanup(watch.Unsubscribe)
		snapshot := watch.Snapshot()
		snapshot.Operation = &agentharness.LaneSnapshotOperation{ID: "run", Kind: agentharness.OperationRun, StartedAt: 1, Status: agentharness.OperationStatusOpen, RunningTools: []agentharness.LaneSnapshotTool{}}
		result := func(text string) harness.AgentToolResult {
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: map[string]any{"text": text}}
		}
		start := func(index int) agentharness.HarnessEvent {
			return agentharness.HarnessEvent{Lane: "main", Payload: agentharness.ToolStartPayload{RunID: "run", TurnID: "turn", ToolCallID: fmt.Sprintf("call-%d", index), ToolName: fmt.Sprintf("tool-%d", index), Args: map[string]any{"index": index}}}
		}
		end := func(index int) agentharness.HarnessEvent {
			return agentharness.HarnessEvent{Lane: "main", Payload: agentharness.ToolEndPayload{RunID: "run", TurnID: "turn", ToolCallID: fmt.Sprintf("call-%d", index), ToolName: fmt.Sprintf("tool-%d", index), Result: result(fmt.Sprintf("done-%d", index))}}
		}
		entry := func(index int) agentharness.HarnessEvent {
			var parent *string
			if index != 0 {
				parent = new(fmt.Sprintf("result-%d", index-1))
			}
			return agentharness.HarnessEvent{Lane: "main", Payload: agentharness.EntryAddedPayload{Entry: session.Entry{ID: fmt.Sprintf("result-%d", index), ParentID: parent, Seq: int64(index + 1), Timestamp: int64(index + 1), Type: session.EntryTypeMessage, Message: agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call-%d", index), ToolName: fmt.Sprintf("tool-%d", index), Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("done-%d", index)}}, Timestamp: int64(index + 1)}}}}}
		}
		for index := range 3 {
			ReduceLaneSnapshot(&snapshot, start(index))
		}
		requireHarnessEqual(t, len(snapshot.Operation.RunningTools), 3)
		ReduceLaneSnapshot(&snapshot, start(0))
		requireHarnessEqual(t, len(snapshot.Operation.RunningTools), 3)
		ReduceLaneSnapshot(&snapshot, agentharness.HarnessEvent{Lane: "main", Payload: agentharness.ToolUpdatePayload{RunID: "stale", TurnID: "turn", ToolCallID: "call-1", ToolName: "tool-1", PartialResult: result("stale")}})
		requireHarnessEqual(t, snapshot.Operation.RunningTools[1].Result, (*harness.AgentToolResult)(nil))
		ReduceLaneSnapshot(&snapshot, end(2))
		var settled *agentharness.LaneSnapshotTool
		for index := range snapshot.Operation.RunningTools {
			if snapshot.Operation.RunningTools[index].ToolCallID == "call-2" {
				settled = &snapshot.Operation.RunningTools[index]
			}
		}
		if settled == nil {
			t.Fatal("missing call-2")
		}
		requireHarnessEqual(t, settled.Status, "settled")
		requireHarnessEqual(t, settled.Args, any(map[string]any{"index": 2}))
		requireHarnessEqual(t, settled.Result, new(result("done-2")))
		ReduceLaneSnapshot(&snapshot, end(0))
		statuses := []string{}
		for _, tool := range snapshot.Operation.RunningTools {
			statuses = append(statuses, tool.Status)
		}
		requireHarnessEqual(t, statuses, []string{"settled", "running", "settled"})
		ReduceLaneSnapshot(&snapshot, entry(0))
		requireHarnessEqual(t, reducerToolIDs(snapshot), []string{"call-1", "call-2"})
		requireHarnessEqual(t, harnessEntryIDs(snapshot.Transcript), []string{"result-0"})
		ReduceLaneSnapshot(&snapshot, end(1))
		for _, tool := range snapshot.Operation.RunningTools {
			requireHarnessEqual(t, tool.Status, "settled")
		}
		ReduceLaneSnapshot(&snapshot, entry(1))
		requireHarnessEqual(t, reducerToolIDs(snapshot), []string{"call-2"})
		ReduceLaneSnapshot(&snapshot, entry(2))
		requireHarnessEqual(t, snapshot.Operation.RunningTools, []agentharness.LaneSnapshotTool{})
		requireHarnessEqual(t, harnessEntryIDs(snapshot.Transcript), []string{"result-0", "result-1", "result-2"})
	})
	// upstream: packages/agent/test/harness/runtime/reducer.test.ts:268
	t.Run("marks navigation completion for rebase", func(t *testing.T) {
		fixture := newPublicLane(t, false, agentharness.Resources{})
		watch, err := fixture.lane.Watch(t.Context())
		requireHarnessOK(t, err)
		t.Cleanup(watch.Unsubscribe)
		snapshot := watch.Snapshot()
		snapshot.Operation = &agentharness.LaneSnapshotOperation{ID: "navigation", Kind: agentharness.OperationNavigation, StartedAt: 1, Status: agentharness.OperationStatusOpen, RunningTools: []agentharness.LaneSnapshotTool{}}
		reduced := ReduceLaneSnapshot(&snapshot, agentharness.HarnessEvent{Lane: "main", Payload: agentharness.NavigationEndPayload{RunID: "navigation", Status: "completed", TipID: new("target"), EndedAt: 2}})
		requireHarnessEqual(t, reduced, LaneSnapshotReduction("rebase"))
	})
}

func reducerToolIDs(snapshot agentharness.LaneSnapshot) []string {
	ids := make([]string, len(snapshot.Operation.RunningTools))
	for index, tool := range snapshot.Operation.RunningTools {
		ids[index] = tool.ToolCallID
	}
	return ids
}
