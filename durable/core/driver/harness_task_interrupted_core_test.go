// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi b3a29f11 scheduler.ts runtime.commit reports task_interrupted for any task whose terminal commit fails with an error whose
// detail.reason is restart_limit or interrupted, an extension's included, with the task's restarts and the extension that
// installed it. No Pi test runs an extension task through it (harness-restart-failure.test.ts reports the built-in tasks), so
// these cases follow the wrapper: the report is made after the commit, once per ending, and only for those two reasons.
func TestCoreAnExtensionTaskThatEndsForCrashesIsReportedInterrupted(t *testing.T) {
	cases := []struct {
		name, reason string
		reported     bool
	}{
		{"interrupted", "interrupted", true},
		{"restart_limit", "restart_limit", true},
		{"another reason", "model_error", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eachEngine(t, func(t *testing.T, core bool) {
				state := &crashLoopState{crashing: true}
				task := endsForCrashesTask(state, c.reason)
				registry := harness.CreateRegistry()
				installTasks(t, registry, "loop", task.Erased())
				rig := newCrashRig(t, core, registry, harness.HarnessSettings{})
				current := rig.open()
				root := rootOf(t, current.h)
				id := startOwned(t, root, task.Erased())
				current.h.Resume()
				pollUntil(t, "the phase started", func() bool { _, started := state.get(); return started == 1 })
				rig.kill() // a crash: a close would not count
				state.set(false)
				current = rig.open()
				h := current.h
				h.Resume()
				outcome := waitFinished2(t, h, id).State.Outcome
				if outcome == nil || outcome.Status != durable.OutcomeFailed || outcome.Error == nil || outcome.Error.Message != "ended for crashes" {
					t.Fatalf("outcome %+v, want failed with the message", outcome)
				}
				// A report is made as the commit returns to the task, before the receipt can be told apart from it: wait for a
				// later commit's turn on the line so a missing report is not a late one.
				if _, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (struct{}, error) { return struct{}{}, nil }); err != nil {
					t.Fatal(err)
				}
				var want []durable.ReportInfo
				if c.reported {
					want = []durable.ReportInfo{{Type: durable.ReportTaskInterrupted, Task: &durable.ReportTask{Id: id, Kind: "test.ends-for-crashes", ConversationId: root.Id()}, Extension: "loop", Reason: c.reason, Restarts: 1}}
				}
				got := current.infos.infos()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("report infos %+v, want %+v", got, want)
				}
				if c.reported {
					if err := current.infos.all()[0].err; err == nil || err.Error() != "ended for crashes" {
						t.Fatalf("report %v, want the outcome's message", err)
					}
				}
			})
		})
	}
}

// endsForCrashesTask hangs in its phase while state is crashing, and then ends failed with detail.reason as given.
func endsForCrashesTask(state *crashLoopState, reason string) durable.Task[durable.JsonValue, loopState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, loopState, durable.JsonValue, any]{
		Name: "test.ends-for-crashes", Version: 1,
		Initial: func(durable.JsonValue) loopState { return loopState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, loopState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ loopRecord, runtime loopRuntime) error {
				if state.begin() {
					<-runtime.Signal().Done()
					return nil
				}
				return runtime.Commit(ctx, func(durable.Tx, loopRecord) (*loopNext, error) {
					return &loopNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{
						Status: durable.OutcomeFailed,
						Error:  &durable.TaskOutcomeError{Message: "ended for crashes", Detail: delta.JsonObjectOf("reason", reason)},
					}}, nil
				})
			},
		},
	})
}
