// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The Durable core is pinned to Pi 1.4.0+e7a0df27 (docs/plan/durable-core/CONTRACT.md section 1); Pi 473515f9c, the suspendable
// work (signals, tool parks, gates, hook tasks), is a pin bump (CONTRACT.md section 8). These tests pin what a program that
// uses it observes on the core backend: an error that names the pin, never a dropped park, signal, or task.

const pinText = "is not in the Durable core's contract pin (Pi 1.4.0+e7a0df27, docs/plan/durable-core/CONTRACT.md); a pin bump adds it"

var pinSignal = durable.DefineSignal(durable.SignalDefinition[string]{Name: "pin.go", Version: 1, Schema: map[string]any{"type": "string"}})

func expectPinError(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, driver.ErrSuspendableWorkNotInCorePin) {
		t.Fatalf("%s: err = %v, want driver.ErrSuspendableWorkNotInCorePin", what, err)
	}
	if !strings.Contains(err.Error(), pinText) {
		t.Fatalf("%s: err = %q, want it to name the contract pin (%q)", what, err, pinText)
	}
}

func TestSuspendableWorkSentinelNamesTheContractPin(t *testing.T) {
	want := "suspendable work (signals, parks, wake, checkpoint, ExecuteToolNoWait, gates, hook tasks) " + pinText
	if got := driver.ErrSuspendableWorkNotInCorePin.Error(); got != want {
		t.Fatalf("sentinel = %q, want %q", got, want)
	}
}

// A call on the core never parks: Wake is nil and Checkpoint is absent, as on a fresh call, and the members that only parking
// work uses return the pin error.
func TestCoreToolApiHasNoWakeCheckpointSignalsOrNoWaitCalls(t *testing.T) {
	core := &funcCore{respond: openWith("t", `{}`, "")}
	var sawWake atomic.Bool
	host := toolHost(t, core, tool(func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if wake := api.Wake(); wake != nil {
			t.Errorf("Wake() = %+v, want nil", wake)
		}
		if value, ok := api.Checkpoint(); ok || value != nil {
			t.Errorf("Checkpoint() = %v, %v, want nil, false", value, ok)
		}
		state, err := api.ReadSignalErased(ctx, pinSignal, "k")
		if state != nil {
			t.Errorf("ReadSignalErased state = %+v, want nil", state)
		}
		expectPinError(t, "ReadSignalErased", err)
		nested, err := api.ExecuteToolNoWait(ctx, "other", map[string]any{})
		if nested.TaskId != 0 || nested.Result != nil {
			t.Errorf("ExecuteToolNoWait = %+v, want the zero call", nested)
		}
		expectPinError(t, "ExecuteToolNoWait", err)
		sawWake.Store(true)
		return durable.ToolExecutionResult{}, nil
	}))
	runTool(t, core, host)
	waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
	if !sawWake.Load() {
		t.Fatal("the tool did not run to its end")
	}
}

// A park is not dropped: the call ends with an invalid_park error result (tool.ts, a park that breaks a rule), whose text is the
// pin's, and the registration's Cancel is never called.
func TestCoreToolThatParksEndsWithAnInvalidParkResultAndNeverCancels(t *testing.T) {
	until := 9000.0
	parks := map[string]*durable.ToolPark{
		"sleep": {Sleep: &durable.ToolParkSleep{Until: &until, Checkpoint: "c", HasCheckpoint: true}},
		"wait":  {Wait: &durable.ToolParkWait{On: []durable.TaskId{1}, Policy: durable.JoinAllSettled}},
	}
	for name, park := range parks {
		t.Run(name, func(t *testing.T) {
			var cancels atomic.Int32
			core := &funcCore{respond: openWith("t", `{}`, "")}
			registration := durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: "t"},
				Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					return durable.ToolExecutionResult{Park: park, Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "dropped"}}}, nil
				},
				Cancel: func(context.Context, any, durable.ToolExecutionApi) error { cancels.Add(1); return nil },
			}
			host := toolHost(t, core, map[string]durable.ToolRegistration{"t": registration})
			runTool(t, core, host)
			waitFor(t, "tool_done", func() bool { _, ok := done(core); return ok })
			ev, _ := done(core)
			want := `{"isError":true,"diagnostics":[{"severity":"error","message":"Tool t returned an invalid park: ` +
				driver.ErrSuspendableWorkNotInCorePin.Error() + `","code":"invalid_park"}]}`
			if ev.Phase != abi.OutcomeResult || ev.ID != 1 || string(ev.Payload) != want {
				t.Fatalf("tool_done = outcome %d %s\nwant %s", ev.Phase, ev.Payload, want)
			}
			if cancels.Load() != 0 {
				t.Fatalf("Cancel ran %d times, want never", cancels.Load())
			}
		})
	}
}

func coreToolResultTexts(t *testing.T, root harness.Conversation) []string {
	t.Helper()
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, entry := range page.Items {
		if entry.Kind != "pi.tool-result" || len(entry.Model) == 0 {
			continue
		}
		if message, ok := entry.Model[0].(ai.ToolResultMessage); ok {
			var text strings.Builder
			for _, block := range message.Content {
				if content, isText := block.(ai.TextContent); isText {
					text.WriteString(content.Text)
				}
			}
			results = append(results, text.String())
		}
	}
	return results
}

type pinReports struct {
	mu   sync.Mutex
	list []string
}

func (r *pinReports) add(report durable.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if report.Error != nil {
		r.list = append(r.list, string(report.Kind)+": "+report.Error.Error())
	}
}

// texts are the reports as "kind: error".
func (r *pinReports) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.list...)
}

func okCoreTool(execute func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) *durable.ToolRegistration {
	return nestedCoreTool("batch", execute)
}

// End to end through a core Harness: a tool that parks gets the invalid_park error result in the transcript, so the model sees
// why, and the run goes on to its answer.
func TestCoreHarnessParkingToolResultIsTheInvalidParkError(t *testing.T) {
	until := 1e12
	_, root, sub := nestedCoreHarnessWith(t, &durable.Extension{Name: "park", Tools: []*durable.ToolRegistration{
		okCoreTool(func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Until: &until}}}, nil
		}),
	}})
	nestedCoreWait(t, sub, durable.SubmissionDone)
	results := coreToolResultTexts(t, root)
	if len(results) != 1 || !strings.Contains(results[0], "[error] Tool batch returned an invalid park: suspendable work (") || !strings.Contains(results[0], pinText) {
		t.Fatalf("tool results = %q, want the invalid_park error naming the pin", results)
	}
}

// A keyed task of a tool call is a row of the call's task memo, which the core has not: the call gets the pin error, and no task
// is created.
func TestCoreHarnessKeyedToolTaskReturnsThePinError(t *testing.T) {
	var keyed error
	key := "k"
	_, _, sub := nestedCoreHarnessWith(t, &durable.Extension{Name: "keyed", Tools: []*durable.ToolRegistration{
		okCoreTool(func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			_, keyed = api.CreateTaskErased(ctx, harness.GenerationTask.Erased(), nil, durable.ToolTaskOptions{Key: &key})
			return nestedCoreText("ok"), nil
		}),
	}})
	nestedCoreWait(t, sub, durable.SubmissionDone)
	expectPinError(t, "CreateTaskErased with a key", keyed)
}

// The hooks of Pi 473515f9c that the core's host protocol cannot carry fail with the pin error, which the core reports as a hook
// failure; the run itself is not changed by them.
func TestCoreHarnessHookResultsOfSuspendableWorkAreReportedWithThePinError(t *testing.T) {
	someTask := durable.AnyTask{}
	cases := []struct {
		name  string
		hooks any
		// blocked is whether the hook's error blocks the call instead of being reported (a beforeTool error blocks).
		blocked bool
	}{
		{name: "a beforeTool sleep", hooks: &harness.ToolHooks{
			BeforeTool: func(context.Context, harness.ToolHookCall, harness.HookApi) (*harness.BeforeToolResult, error) {
				until := 5e12
				return &harness.BeforeToolResult{Sleep: &harness.GateSleep{Until: &until}}, nil
			},
		}, blocked: true},
		{name: "an afterTool result with a park", hooks: &harness.ToolHooks{
			AfterTool: func(_ context.Context, _ harness.ToolHookCall, result durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				until := 5e12
				result.Park = &durable.ToolPark{Sleep: &durable.ToolParkSleep{Until: &until}}
				return &result, nil
			},
		}},
		{name: "an onYield tasks result", hooks: &harness.GenerationHooks{
			OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (harness.YieldResult, error) {
				return &harness.YieldTasks{Tasks: []harness.HookTask{{Task: someTask}}}, nil
			},
		}},
		{name: "an afterTools tasks result", hooks: &harness.GenerationHooks{
			AfterTools: func(context.Context, durable.EntryId, []durable.EntryId, harness.HookApi) (*harness.HookTasks, error) {
				return &harness.HookTasks{Tasks: []harness.HookTask{{Task: someTask}}}, nil
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reports := &pinReports{}
			var ran atomic.Bool
			task := harness.ToolTask.AnyDefinition().Name
			if _, ok := tc.hooks.(*harness.GenerationHooks); ok {
				task = harness.GenerationTask.AnyDefinition().Name
			}
			_, root, sub := nestedCoreHarnessWith(t, &durable.Extension{
				Name: "hooked",
				Tools: []*durable.ToolRegistration{okCoreTool(func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					ran.Store(true)
					return nestedCoreText("ran"), nil
				})},
				Hooks: []durable.HookRegistration{{Task: task, Handlers: tc.hooks}},
			}, reports.add)
			nestedCoreWait(t, sub, durable.SubmissionDone)
			results := coreToolResultTexts(t, root)
			if tc.blocked {
				// A throwing beforeTool blocks the call, with the error as the reason: the model sees the pin's text.
				if ran.Load() || len(results) != 1 || !strings.Contains(results[0], "Tool call blocked: ") || !strings.Contains(results[0], pinText) {
					t.Fatalf("ran = %v, tool results = %q, want the call blocked with the pin's text", ran.Load(), results)
				}
				return
			}
			// Any other hook that fails is reported, and the run goes on as if it had returned nothing.
			found := false
			for _, text := range reports.texts() {
				found = found || strings.HasPrefix(text, string(durable.ReportExtensionError)+": ") && strings.Contains(text, pinText)
			}
			if !found || !ran.Load() || len(results) != 1 || results[0] != "ran" {
				t.Fatalf("reports = %q, ran = %v, tool results = %q, want a report naming the pin and the tool's own result", reports.texts(), ran.Load(), results)
			}
		})
	}
}

// An afterTools hook that asks for no task is not a request: the empty answer passes.
func TestCoreHarnessAfterToolsWithoutTasksIsNotAPinError(t *testing.T) {
	reports := &pinReports{}
	_, _, sub := nestedCoreHarnessWith(t, &durable.Extension{
		Name: "empty",
		Tools: []*durable.ToolRegistration{okCoreTool(func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return nestedCoreText("ran"), nil
		})},
		Hooks: []durable.HookRegistration{{Task: harness.GenerationTask.AnyDefinition().Name, Handlers: &harness.GenerationHooks{
			AfterTools: func(context.Context, durable.EntryId, []durable.EntryId, harness.HookApi) (*harness.HookTasks, error) {
				return &harness.HookTasks{}, nil
			},
			OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (harness.YieldResult, error) {
				return nil, nil
			},
		}}},
	}, reports.add)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if texts := reports.texts(); len(texts) != 0 {
		t.Fatalf("reports = %q, want none", texts)
	}
}

// Harness.signal, Tx.signal, and a submission's notify are the writes of signal rows the core has not.
func TestCoreHarnessSignalsAndNotifyReturnThePinError(t *testing.T) {
	h, root, _ := nestedCoreHarnessPaused(t, okCoreTool(func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return nestedCoreText("ran"), nil
	}))
	ctx := t.Context()
	result, err := h.SignalErased(ctx, 1, pinSignal, "k", "v")
	if result != "" {
		t.Fatalf("SignalErased result = %q, want none", result)
	}
	expectPinError(t, "Harness.SignalErased", err)
	result, err = harness.Signal(ctx, h, 1, pinSignal, "k", "v")
	if result != "" {
		t.Fatalf("Signal result = %q, want none", result)
	}
	expectPinError(t, "harness.Signal", err)

	_, err = durable.Commit(ctx, root, func(tx durable.Tx) (durable.SignalResult, error) {
		return tx.SignalErased(1, pinSignal, "k", "v")
	})
	expectPinError(t, "Tx.SignalErased", err)
	_, err = durable.Commit(ctx, root, func(tx durable.Tx) (durable.SignalResult, error) {
		return durable.Signal(tx, 1, pinSignal, "k", "v")
	})
	expectPinError(t, "durable.Signal", err)

	notify := &durable.SubmissionNotify{TaskId: 1, Key: "k"}
	_, err = root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x"), Notify: notify})
	expectPinError(t, "Submit with Notify", err)
	_, err = durable.Commit(ctx, root, func(tx durable.Tx) (durable.SubmissionId, error) {
		return root.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x"), Notify: notify})
	})
	expectPinError(t, "SubmitIn with Notify", err)
	_, err = durable.Commit(ctx, root, func(tx durable.Tx) (durable.SubmissionRecord, error) {
		return tx.CreateSubmission(durable.SubmissionCreate{ConversationId: root.Id(), Notify: notify, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued})
	})
	expectPinError(t, "CreateSubmission with Notify", err)
}

// probeTask runs once, tries what only suspendable work can do, and completes with what each attempt returned.
type probeState struct {
	Phase string `json:"phase"`
}

type probeResult struct {
	ReadSignal  string `json:"readSignal"`
	TxSignal    string `json:"txSignal"`
	SignalSleep string `json:"signalSleep"`
	NoTimeSleep string `json:"noTimeSleep"`
	JoinAny     string `json:"joinAny"`
	WakeWrite   string `json:"wakeWrite"`
}

func probeTask() durable.Task[durable.JsonValue, probeState, probeResult, any] {
	type runtimeOf = durable.TaskRuntime[durable.JsonValue, probeState, probeResult, any]
	type recordOf = durable.RunningTask[durable.JsonValue, probeState, probeResult]
	type nextOf = durable.NextTaskState[probeState, probeResult]
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, probeState, probeResult, any]{
		Name: "test.pin-probe", Version: 1,
		Initial: func(durable.JsonValue) probeState { return probeState{Phase: "start"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, probeState, probeResult, any]{
			"start": func(ctx context.Context, _ recordOf, runtime runtimeOf) error {
				var out probeResult
				note := func(err error) string {
					if errors.Is(err, driver.ErrSuspendableWorkNotInCorePin) && strings.Contains(err.Error(), pinText) {
						return "pin"
					}
					if err == nil {
						return "none"
					}
					return err.Error()
				}
				_, err := runtime.ReadSignalErased(ctx, pinSignal, "k")
				out.ReadSignal = note(err)
				out.TxSignal = note(runtime.Commit(ctx, func(tx durable.Tx, _ recordOf) (*nextOf, error) {
					_, err := durable.Signal(tx, runtime.TaskId(), pinSignal, "k", "v")
					return nil, err
				}))
				checkpoint := probeState{Phase: "start"}
				now := 1.0
				out.SignalSleep = note(runtime.Commit(ctx, func(durable.Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: durable.TaskSleeping, Checkpoint: &checkpoint, Signals: []durable.SignalKey{pinSignal.Key("k")}, Until: &now}, nil
				}))
				out.NoTimeSleep = note(runtime.Commit(ctx, func(durable.Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: durable.TaskSleeping, Checkpoint: &checkpoint}, nil
				}))
				out.JoinAny = note(runtime.Commit(ctx, func(durable.Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: durable.TaskWaiting, Checkpoint: &checkpoint, On: []durable.TaskId{runtime.TaskId()}, Policy: durable.JoinAny}, nil
				}))
				out.WakeWrite = note(runtime.Commit(ctx, func(durable.Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: durable.TaskRunning, Checkpoint: &checkpoint, Wake: &durable.WakeReason{Reason: durable.WakeTime}}, nil
				}))
				return runtime.Commit(ctx, func(durable.Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[probeResult]{Status: durable.OutcomeCompleted, Result: &out}}, nil
				})
			},
		},
	})
}

// A task on the core that reads a signal, delivers one, or commits a state only suspendable work has (a sleep on signals or
// without a time, a wait for any task, a wake) gets the pin error, and the commit is not applied: the task goes on at its
// checkpoint and ends as it commits.
func TestCoreTaskRuntimeRejectsWhatOnlySuspendableWorkHas(t *testing.T) {
	task := probeTask()
	rig := newSleepRig(t, true, task.Erased())
	h := rig.open(nil)
	id := rig.start(h, task.Erased())
	h.Resume()
	receipt := waitSettled(t, h, id)
	outcome := receipt.State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeCompleted || outcome.Result == nil {
		t.Fatalf("outcome = %+v, want completed", outcome)
	}
	got := jsonValueOf(t, *outcome.Result).(map[string]any)
	for _, member := range []string{"readSignal", "txSignal", "signalSleep", "noTimeSleep", "joinAny", "wakeWrite"} {
		if got[member] != "pin" {
			t.Errorf("%s = %v, want the pin error", member, got[member])
		}
	}
}

// A sleeping task with a time is not suspendable work: the core stores it as before.
func TestCoreTaskSleepWithATimeIsNotAPinError(t *testing.T) {
	clock := newClock(1000)
	nap := napTask(1000, nil)
	rig := newSleepRig(t, true, nap.Erased())
	h := rig.open(func(o *harness.HarnessOptions) { o.Now = clock.now })
	id := rig.start(h, nap.Erased())
	h.Resume()
	pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
	if until := stateOf(t, h, id).Until; until == nil || *until != 2000 {
		t.Fatalf("until = %v, want 2000", until)
	}
}
