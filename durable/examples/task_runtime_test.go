package examples_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type probeState struct {
	Phase string `json:"phase"`
}

type probeInput struct {
	Peer durable.TaskId `json:"peer"`
}

type probeReport struct {
	ModelsSet      bool
	ToolExecution  durable.ToolExecutionMode
	EnvIsNil       bool
	AbsentEntry    bool
	OwnRecordKind  string
	PeerSettled    durable.TaskStatus
	PeerOutcome    durable.TaskOutcomeStatus
	PeerOutcomeEnd durable.TaskOutcomeStatus
}

// Pi TaskRuntime.entry: packages/durable/src/types.ts:219.
// Pi TaskRuntime.env: packages/durable/src/types.ts:186.
// Pi TaskRuntime.getTask: packages/durable/src/types.ts:208.
// Pi TaskRuntime.models: packages/durable/src/types.ts:184.
// Pi TaskRuntime.settings: packages/durable/src/types.ts:183.
// Pi TaskRuntime.waitfortask: packages/durable/src/types.ts:210.
// packages/durable/src/types.ts TaskRuntime: models, settings, env(), entry(), getTask() and waitForTask() read the Harness's models and settings, the conversation's environment, committed entries and records of other tasks.
// mutation-checked: zeroing the results of TaskRuntime.GetTask, TaskRuntime.Models, TaskRuntime.Settings, TaskRuntime.WaitForTask fails it
// Pi: packages/durable/src/types.ts:208 (getTask)
// Pi: packages/durable/src/types.ts:184 (models)
// Pi: packages/durable/src/types.ts:182 (settings)
// Pi: packages/durable/src/types.ts:210 (waitForTask)
// upstream: packages/durable/src/harness/types.ts:200 getTask reads a task record; harness.ts:242 getTask.
func TestTaskRuntimeReadsHarnessState(t *testing.T) {
	type probeRuntime = durable.TaskRuntime[probeInput, probeState, string, any]
	var report probeReport
	quick := durable.DefineTask(durable.TaskDefinition[struct{}, probeState, string, any]{
		Name: "example.quick", Version: 1,
		Initial: func(struct{}) probeState { return probeState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[struct{}, probeState, string, any]{
			"run": func(ctx context.Context, _ durable.RunningTask[struct{}, probeState, string], runtime durable.TaskRuntime[struct{}, probeState, string, any]) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[struct{}, probeState, string]) (*durable.NextTaskState[probeState, string], error) {
					done := "quick done"
					return &durable.NextTaskState[probeState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &done}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[struct{}, probeState, string], runtime durable.TaskRuntime[struct{}, probeState, string, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[struct{}, probeState, string]) (*durable.NextTaskState[probeState, string], error) {
				return &durable.NextTaskState[probeState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
	probe := durable.DefineTask(durable.TaskDefinition[probeInput, probeState, string, any]{
		Name: "example.probe", Version: 1,
		Initial: func(probeInput) probeState { return probeState{Phase: "inspect"} },
		Phases: map[string]durable.PhaseHandler[probeInput, probeState, string, any]{
			"inspect": func(ctx context.Context, task durable.RunningTask[probeInput, probeState, string], runtime probeRuntime) error {
				report.ModelsSet = runtime.Models() != nil
				report.ToolExecution = runtime.Settings().ToolExecution
				environment, err := runtime.Env(ctx)
				if err != nil {
					return err
				}
				report.EnvIsNil = environment == nil
				entry, err := runtime.Entry(ctx, durable.EntryId(987654))
				if err != nil {
					return err
				}
				report.AbsentEntry = entry == nil
				own, err := runtime.GetTask(ctx, task.Id)
				if err != nil || own == nil {
					return err
				}
				report.OwnRecordKind = own.Kind
				settled, err := runtime.WaitForTask(ctx, task.Input.Peer)
				if err != nil {
					return err
				}
				report.PeerSettled = settled.State.Status
				if settled.State.Outcome != nil {
					report.PeerOutcome = settled.State.Outcome.Status
				}
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[probeInput, probeState, string]) (*durable.NextTaskState[probeState, string], error) {
					done := "inspected"
					return &durable.NextTaskState[probeState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &done}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[probeInput, probeState, string], runtime probeRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[probeInput, probeState, string]) (*durable.NextTaskState[probeState, string], error) {
				return &durable.NextTaskState[probeState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "probes", Tasks: []durable.AnyTask{quick, probe}}))
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry, Settings: func() *harness.HarnessSettings {
		return &harness.HarnessSettings{ToolExecution: durable.ToolExecutionSequential}
	}})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, nil))
	owner := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
	peerId := commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, quick, struct{}{}, owner)
	})
	probeId := commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, probe, probeInput{Peer: peerId}, owner)
	})
	finished, err := opened.WaitForTask(background, probeId)
	if err != nil || finished.State.Outcome == nil || finished.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("probe finished %+v err %v", finished, err)
	}
	if !report.ModelsSet || report.ToolExecution != durable.ToolExecutionSequential || !report.EnvIsNil || !report.AbsentEntry || report.OwnRecordKind != "example.probe" || report.PeerSettled != durable.TaskTerminal || report.PeerOutcome != durable.OutcomeCompleted {
		t.Fatalf("report %+v", report)
	}
	closeSession(t, opened)
}
