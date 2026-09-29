package runtime

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave06RuntimeLaneRestore(t *testing.T) {
	ctx := context.Background()
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:90
	t.Run("restores an idle lane's latest operation id without reading its result", func(t *testing.T) {
		opened := restoreFixture(t)
		result := session.OperationResultRecord{OperationID: opened.IdGenerator().Next(nil), Kind: "navigation", Status: "completed", StartedAt: 1, EndedAt: 2}
		harnessCommit(t, opened, session.SetValue(session.OperationResult(result.OperationID), result), session.SetValue(session.LaneStateValue("main"), session.LaneState{LastOperationID: &result.OperationID, Inbox: []session.InboxItem{}}))
		state, err := RestoreLane(ctx, opened, "main")
		requireHarnessOK(t, err)
		requireHarnessEqual(t, state, LaneState{Configuration: laneTestConfiguration(), Inbox: []session.InboxItem{}, LastOperationID: &result.OperationID})
	})
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:128
	t.Run("restores an open operation without interpreting its referenced payloads", func(t *testing.T) {
		opened := restoreFixture(t)
		meta := session.OperationMeta{OperationID: opened.IdGenerator().Next(nil), Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
		state := restoreCheckpoint(opened.IdGenerator().Next(nil))
		storeCurrentOperation(t, opened, meta, state)
		restored, err := RestoreLane(ctx, opened, "main")
		requireHarnessOK(t, err)
		requireHarnessEqual(t, restored.Operation, &session.Operation{Meta: meta, State: state})
	})
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:162
	t.Run("validates current-operation identity, lane ownership, and intent/state compatibility", func(t *testing.T) {
		for _, corruption := range []string{"identity", "lane", "kind"} {
			t.Run(corruption, func(t *testing.T) {
				opened := restoreFixture(t)
				id := opened.IdGenerator().Next(nil)
				meta := session.OperationMeta{OperationID: id, Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
				switch corruption {
				case "identity":
					meta.OperationID = "different-operation"
				case "lane":
					meta.Lane = "worker"
				case "kind":
					meta.Intent = session.OperationIntent{Kind: "navigation"}
				}
				harnessCommit(t, opened, session.SetValue(session.OperationMetaValue(id), meta), session.SetValue(session.OperationStateValue(id), restoreCheckpoint("trigger")), session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &id, Inbox: []session.InboxItem{}}))
				if _, err := RestoreLane(ctx, opened, "main"); err == nil {
					t.Fatal("corrupt operation restored without error")
				}
			})
		}
	})
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:200
	t.Run("accepts exactly the family-neutral state reachability matrix", func(t *testing.T) {
		checkpoint := restoreCheckpoint("trigger")
		resume := session.ResultBoundary{Kind: "resume_checkpoint", ResumeAfter: &session.CheckpointData{Continuation: checkpoint.Continuation, TriggerEntryID: checkpoint.TriggerEntryID}}
		finish := session.ResultBoundary{Kind: "finish"}
		navigation := session.ResultBoundary{Kind: "commit_navigation", TargetID: "target"}
		summary := func(boundary session.ResultBoundary) session.OperationState {
			state := session.OperationState{OperationScope: restoreScope(), At: session.AtSummaryDeciding, Task: session.SummaryTask{TaskID: "task", Boundary: boundary}}
			switch boundary.Kind {
			case "finish":
				state.Task.Reason = "manual"
			case "resume_checkpoint":
				state.Task.Reason = "threshold"
			}
			return state
		}
		run := session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}
		compact := session.OperationIntent{Kind: "compaction"}
		navigate := func(target *string, summarize bool) session.OperationIntent {
			return session.OperationIntent{Kind: "navigation", TargetID: target, Summarize: summarize}
		}
		ready := func(target *string) session.OperationState {
			return session.OperationState{OperationScope: restoreScope(), At: session.AtNavigationReadyToCommit, TargetID: target}
		}
		cases := []struct {
			intent   session.OperationIntent
			state    session.OperationState
			accepted bool
		}{
			{run, checkpoint, true},
			{run, summary(resume), true},
			{run, summary(finish), false},
			{run, summary(navigation), false},
			{compact, summary(finish), true},
			{compact, summary(resume), false},
			{compact, summary(navigation), false},
			{compact, checkpoint, false},
			{navigate(nil, false), ready(nil), true},
			{navigate(new("target"), false), ready(new("different")), false},
			{navigate(new("target"), false), summary(navigation), false},
			{navigate(new("target"), true), summary(navigation), true},
			{navigate(new("different"), true), summary(navigation), false},
			{navigate(new("target"), true), ready(new("target")), false},
			{navigate(new("target"), true), summary(finish), false},
			{navigate(new("target"), true), summary(resume), false},
			{navigate(new("target"), false), checkpoint, false},
		}
		for index, tc := range cases {
			t.Run(fmt.Sprint(index), func(t *testing.T) {
				opened := restoreFixture(t)
				meta := session.OperationMeta{OperationID: opened.IdGenerator().Next(nil), Lane: "main", StartedAt: 1, Intent: tc.intent}
				storeCurrentOperation(t, opened, meta, tc.state)
				restored, err := RestoreLane(ctx, opened, "main")
				if tc.accepted {
					requireHarnessOK(t, err)
					requireHarnessEqual(t, restored.Operation, &session.Operation{Meta: meta, State: tc.state})
				} else if err == nil || !strings.Contains(err.Error(), "does not match state") {
					t.Fatalf("error = %v, want does not match state", err)
				}
			})
		}
	})
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:293
	for _, tc := range []struct {
		namespace string
		write     session.Write
	}{
		{session.BranchTip("main").Namespace, session.DeleteValue(session.BranchTip("main"))},
		{session.LaneConfig("main").Namespace, session.DeleteValue(session.LaneConfig("main"))},
		{session.LaneStateValue("main").Namespace, session.DeleteValue(session.LaneStateValue("main"))},
	} {
		t.Run("requires "+tc.namespace, func(t *testing.T) {
			opened := restoreFixture(t)
			harnessCommit(t, opened, tc.write)
			_, err := RestoreLane(ctx, opened, "main")
			if err == nil || !strings.Contains(err.Error(), "missing "+tc.namespace[3:]) {
				t.Fatalf("error = %v, want missing %s", err, tc.namespace[3:])
			}
		})
	}
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:304
	for _, namespace := range []string{session.OperationMetaValue("").Namespace, session.OperationStateValue("").Namespace} {
		t.Run("requires "+namespace+" for the current operation", func(t *testing.T) {
			opened := restoreFixture(t)
			id := opened.IdGenerator().Next(nil)
			meta := session.OperationMeta{OperationID: id, Lane: "main", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
			state := restoreCheckpoint(opened.IdGenerator().Next(nil))
			writes := []session.Write{}
			if namespace != session.OperationMetaValue(id).Namespace {
				writes = append(writes, session.SetValue(session.OperationMetaValue(id), meta))
			}
			if namespace != session.OperationStateValue(id).Namespace {
				writes = append(writes, session.SetValue(session.OperationStateValue(id), state))
			}
			writes = append(writes, session.SetValue(session.LaneStateValue("main"), session.LaneState{CurrentOperationID: &id, Inbox: []session.InboxItem{}}))
			harnessCommit(t, opened, writes...)
			_, err := RestoreLane(ctx, opened, "main")
			if err == nil || !strings.Contains(err.Error(), "missing "+namespace[3:]) {
				t.Fatalf("error = %v, want missing %s", err, namespace[3:])
			}
		})
	}
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:344
	t.Run("restores every configured lane exactly once without writing", func(t *testing.T) {
		opened := restoreFixture(t)
		worker := session.LaneConfiguration{Model: session.ModelRef{Provider: "test", ModelID: "worker"}, ThinkingLevel: ai.ThinkingHigh, ActiveToolNames: []string{"read"}}
		harnessCommit(t, opened, restoreLaneWrites("worker", worker)...)
		meta := session.OperationMeta{OperationID: opened.IdGenerator().Next(nil), Lane: "worker", StartedAt: 1, Intent: session.OperationIntent{Kind: "run", PromptEntryIDs: []string{}}}
		state := restoreCheckpoint(opened.IdGenerator().Next(nil))
		storeCurrentOperation(t, opened, meta, state)
		before := captureRestoreValues(t, opened)
		probe := &restoreReadProbe{Session: opened}
		lanes, err := RestoreSession(ctx, probe)
		requireHarnessOK(t, err)
		names := []string{}
		for _, lane := range lanes {
			names = append(names, lane.Name)
			if lane.Name == "main" {
				requireHarnessEqual(t, lane.State.Configuration, laneTestConfiguration())
			}
			if lane.Name == "worker" {
				requireHarnessEqual(t, lane.State.Configuration, worker)
				requireHarnessEqual(t, lane.State.Operation, &session.Operation{Meta: meta, State: state})
			}
		}
		slices.Sort(names)
		requireHarnessEqual(t, names, []string{"main", "worker"})
		requireHarnessEqual(t, probe.mutations, 1)
		requireHarnessEqual(t, probe.reads, []session.StoredAddressBase{session.OperationMetaValue(meta.OperationID).Address(), session.OperationStateValue(meta.OperationID).Address()})
		requireHarnessEqual(t, probe.lists, 0)
		requireHarnessEqual(t, captureRestoreValues(t, opened), before)
	})
	// upstream: packages/agent/test/harness/runtime/restore.test.ts:428
	t.Run("allows an empty inventory but rejects lane values without a Branch", func(t *testing.T) {
		repo := session.NewMemorySessionRepo(nil)
		t.Cleanup(func() { requireHarnessOK(t, repo.Close(ctx)) })
		empty, err := repo.Create(ctx, session.SessionCreateOptions{})
		requireHarnessOK(t, err)
		lanes, err := RestoreSession(ctx, empty)
		requireHarnessOK(t, err)
		requireHarnessEqual(t, lanes, []RestoredLane{})
		opened := restoreFixture(t)
		harnessCommit(t, opened, session.DeleteValue(session.BranchTip("main")))
		_, err = RestoreSession(ctx, opened)
		if err == nil || !strings.Contains(err.Error(), `Lane "main" is missing branch.tip`) {
			t.Fatalf("error = %v, want missing branch.tip", err)
		}
	})
}

type restoreReadProbe struct {
	session.Session
	mu        sync.Mutex
	mutations int
	reads     []session.StoredAddressBase
	lists     int
}

type restoreMutatorProbe struct {
	session.SessionMutator
	probe *restoreReadProbe
}

func (probe *restoreReadProbe) Mutate(ctx context.Context, callback session.SessionMutationCallback) (any, error) {
	probe.mutations++
	return probe.Session.Mutate(ctx, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		return callback(ctx, &restoreMutatorProbe{SessionMutator: mutator, probe: probe})
	})
}
func (reader *restoreMutatorProbe) GetValue(ctx context.Context, address session.StoredAddressBase) (*session.StoredValue[any], error) {
	reader.probe.mu.Lock()
	reader.probe.reads = append(reader.probe.reads, address)
	reader.probe.mu.Unlock()
	return reader.SessionMutator.GetValue(ctx, address)
}
func (reader *restoreMutatorProbe) ReadList(ctx context.Context, address session.StoredAddressBase, options *session.ListReadOptions) ([]session.ListElement[any], error) {
	reader.probe.mu.Lock()
	reader.probe.lists++
	reader.probe.mu.Unlock()
	return reader.SessionMutator.ReadList(ctx, address, options)
}

func captureRestoreValues(t *testing.T, opened session.Session) []any {
	t.Helper()
	ctx := context.Background()
	leaves, err := session.ScanValues(ctx, opened, session.BranchTipInventoryPrefix())
	requireHarnessOK(t, err)
	mainConfig, err := session.GetValue(ctx, opened, session.LaneConfig("main"))
	requireHarnessOK(t, err)
	workerConfig, err := session.GetValue(ctx, opened, session.LaneConfig("worker"))
	requireHarnessOK(t, err)
	mainState, err := session.GetValue(ctx, opened, session.LaneStateValue("main"))
	requireHarnessOK(t, err)
	workerState, err := session.GetValue(ctx, opened, session.LaneStateValue("worker"))
	requireHarnessOK(t, err)
	return []any{leaves, mainConfig, workerConfig, mainState, workerState}
}
