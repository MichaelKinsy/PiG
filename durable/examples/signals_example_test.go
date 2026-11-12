// Ports packages/durable/test/examples/38-signals.ts: a typed JSON value delivered to a live task from outside, and what
// each delivery returns. A value that arrives before the task sleeps on its key is kept, and the sleep wakes at once.
// The script's printing is the transcript this test reads.
//
// Go mapping: the greeter's own print comes from its task, on a goroutine of its own, so it is collected apart from the
// script's lines (upstream's order of the two depends on the event loop). The script's pauses of a few milliseconds
// become settledWake.

package examples_test

import (
	"context"
	"fmt"

	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

type greeting struct {
	Text string `json:"text"`
}

type greeterState struct {
	Phase string `json:"phase"`
}

// Pi: packages/durable/src/signals.ts, packages/durable/src/harness/scheduler.ts (sleeps on signals).
func TestExample38Signals(t *testing.T) {
	// A signal kind: a name, a version, and a schema of a JSON value. Deliveries are checked against it.
	greetingSignal := durable.DefineSignal(durable.SignalDefinition[greeting]{
		Name: "example.greeting", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})

	// A task that first does some work, then sleeps until a greeting arrives under the key "hello".
	type greeterRuntime = durable.TaskRuntime[any, greeterState, string, any]
	type greeterTask = durable.RunningTask[any, greeterState, string]
	type greeterNext = durable.NextTaskState[greeterState, string]
	var greeterLines printed
	greeter := durable.DefineTask(durable.TaskDefinition[any, greeterState, string, any]{
		Name:    "example.greeter",
		Version: 1,
		Initial: func(any) greeterState { return greeterState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[any, greeterState, string, any]{
			"work": func(ctx context.Context, _ greeterTask, runtime greeterRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, greeterTask) (*greeterNext, error) {
					return &greeterNext{Status: durable.TaskSleeping, Checkpoint: &greeterState{Phase: "greeted"}, Signals: []durable.SignalKey{greetingSignal.Key("hello")}}, nil
				})
			},
			"greeted": func(ctx context.Context, task greeterTask, runtime greeterRuntime) error {
				// The wake names the key; ReadSignal is how the value is read.
				wake := task.State.Wake
				state, err := durable.ReadSignal(ctx, runtime, greetingSignal, "hello")
				if err != nil {
					return err
				}
				text := "(none)"
				if state != nil && !state.Expired {
					text = state.Value.Text
				}
				greeterLines.add(fmt.Sprintf("  [greeter] woke by %s, read %q", jsonText(wake), text))
				return runtime.Commit(ctx, func(durable.Tx, greeterTask) (*greeterNext, error) {
					return &greeterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &text}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ greeterTask, runtime greeterRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, greeterTask) (*greeterNext, error) {
				return &greeterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "greeter", Tasks: []durable.AnyTask{greeter.Erased()}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry, OnWake: func(harness.Wake) {}})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, nil))
	createGreeter := func() durable.TaskId {
		return commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, greeter, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		})
	}
	id := createGreeter()

	var script printed
	// 1. Delivered before the task ran at all: kept in the task. Harness.Signal also starts scheduling.
	script.add("early: " + string(must(harness.Signal(background, opened, id, greetingSignal, "hello", greeting{Text: "hi there"}))))
	// 2. Webhook retries of the same key are answered `duplicate`.
	script.add("retry: " + string(must(harness.Signal(background, opened, id, greetingSignal, "hello", greeting{Text: "hi again"}))))
	// 3. A value that does not match the schema is `invalid`; nothing is stored. (`{ text: 42 } as never` in the script.)
	badValue := must(durable.ToJsonValue(map[string]any{"text": float64(42)}))
	script.add("bad value: " + string(must(opened.SignalErased(background, id, greetingSignal, "other", badValue))))
	// 4. No such task.
	script.add("unknown task: " + string(must(harness.Signal(background, opened, 999_999, greetingSignal, "hello", greeting{Text: "?"}))))

	// The sleep finds the early value and wakes at once.
	script.add("result: " + outcomeText(waitTask(t, opened, id)))
	// 5. The task ended: every later delivery is `closed`.
	script.add("after the end: " + string(must(harness.Signal(background, opened, id, greetingSignal, "hello", greeting{Text: "too late"}))))

	// A second greeter sleeps first. While it sleeps it holds no invocation: the Session needs no process (`active`
	// false) and has no deadline (`at` absent), so a host could let it hibernate until a delivery comes.
	second := createGreeter()
	opened.Resume()
	waitSleeping(t, opened, second)
	script.add("asleep, wake: " + wakeText(settledWake(opened)))
	script.add("late: " + string(must(harness.Signal(background, opened, second, greetingSignal, "hello", greeting{Text: "good morning"}))))
	script.add("result: " + outcomeText(waitTask(t, opened, second)))

	expectEqual(t, "script", script.all(), []string{
		"early: delivered",
		"retry: duplicate",
		"bad value: invalid",
		"unknown task: not_found",
		`result: {"status":"completed","result":"hi there"}`,
		"after the end: closed",
		`asleep, wake: {"active":false}`,
		"late: delivered",
		`result: {"status":"completed","result":"good morning"}`,
	})
	expectEqual(t, "greeter", greeterLines.all(), []string{
		`  [greeter] woke by {"reason":"signal","signal":"example.greeting","key":"hello"}, read "hi there"`,
		`  [greeter] woke by {"reason":"signal","signal":"example.greeting","key":"hello"}, read "good morning"`,
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
