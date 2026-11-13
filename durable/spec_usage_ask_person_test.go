package durable

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
)

// Ports packages/durable/test/spec-usage.test.ts (Pi 473515f9c), the §5.7 example of a task that asks a person: it sleeps
// for an answer signal under a deadline, and reads { value } when answered or { expired } after the deadline. The other
// suspendable-work examples are in durable/examples/spec_usage_suspendable_test.go.

type askPersonInput struct {
	Question string `json:"question"`
}

type askPersonCheckpoint struct {
	Phase string `json:"phase"`
}

type askPersonResult struct {
	Text *string `json:"text,omitempty"`
}

type askPersonAnswer struct {
	Text string `json:"text"`
}

// specAskPerson is `AskPerson` of spec-usage.test.ts.
func specAskPerson() Task[askPersonInput, askPersonCheckpoint, askPersonResult, any] {
	answer := DefineSignal(SignalDefinition[askPersonAnswer]{
		Name: "app.answer", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})
	const hour = 60 * 60 * 1000
	type runtimeOf = TaskRuntime[askPersonInput, askPersonCheckpoint, askPersonResult, any]
	type recordOf = RunningTask[askPersonInput, askPersonCheckpoint, askPersonResult]
	type nextOf = NextTaskState[askPersonCheckpoint, askPersonResult]
	return DefineTask(TaskDefinition[askPersonInput, askPersonCheckpoint, askPersonResult, any]{
		Name:    "app.ask-person",
		Version: 1,
		Initial: func(askPersonInput) askPersonCheckpoint { return askPersonCheckpoint{Phase: "ask"} },
		Phases: map[string]PhaseHandler[askPersonInput, askPersonCheckpoint, askPersonResult, any]{
			"ask": func(ctx context.Context, _ recordOf, runtime runtimeOf) error {
				return runtime.Commit(ctx, func(Tx, recordOf) (*nextOf, error) {
					until := runtime.Now() + 4*hour
					return &nextOf{Status: TaskSleeping, Signals: []SignalKey{answer.Key("answer")}, Until: &until, Checkpoint: &askPersonCheckpoint{Phase: "answered"}}, nil
				})
			},
			"answered": func(ctx context.Context, _ recordOf, runtime runtimeOf) error {
				state, err := ReadSignal(ctx, runtime, answer, "answer") // { value } when answered, { expired } after the deadline
				if err != nil {
					return err
				}
				var result askPersonResult
				if state != nil && !state.Expired {
					result.Text = &state.Value.Text
				}
				return runtime.Commit(ctx, func(Tx, recordOf) (*nextOf, error) {
					return &nextOf{Status: TaskTerminal, Outcome: &TaskOutcome[askPersonResult]{Status: OutcomeCompleted, Result: &result}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ recordOf, runtime runtimeOf) error {
			return runtime.Commit(ctx, func(Tx, recordOf) (*nextOf, error) {
				return &nextOf{Status: TaskTerminal, Outcome: &TaskOutcome[askPersonResult]{Status: OutcomeAborted}}, nil
			})
		},
	})
}

// spec-usage.test.ts it("compiles the spec's usage examples"): the task that asks a person is defined.
func TestSpecUsageCompilesTheAskPersonExample(t *testing.T) {
	if name := specAskPerson().AnyDefinition().Name; name != "app.ask-person" {
		t.Fatalf("task %s", name)
	}
}
