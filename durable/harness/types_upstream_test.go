// Ports packages/durable/test/types.test.ts :248 for the checks that name durable/harness declarations: defineExtension,
// hook, HooksOf, Harness.waitForTask, Conversation.compact and the SubmissionDraft drafts. The rest of the case runs in
// durable/types_upstream_test.go.
//
// Upstream asserts type-level contracts with expectTypeOf and @ts-expect-error. The Go port type-checks each snippet
// against the compiled packages, as durable/types_upstream_test.go does, and runs the typed task and its hook through a
// real Harness.

package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var harnessImports = map[string]string{
	"context": "context",
	"d":       "github.com/MichaelKinsy/PiG/durable",
	"h":       "github.com/MichaelKinsy/PiG/durable/harness",
}

// stepper is the case's task: narrowed input, several phases and custom hooks.
type stepperInput struct {
	Steps int `json:"steps"`
}

type stepperCheckpoint struct {
	Phase string `json:"phase"`
	Steps int    `json:"steps,omitempty"`
	Step  int    `json:"step,omitempty"`
}

type stepperResult struct {
	Ran int `json:"ran"`
}

type stepperSkip struct{ Skip bool }

type stepperHooks struct {
	BeforeStep func(step int, api HookApi) *stepperSkip
}

type (
	stepperTask    = durable.Task[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]
	stepperRunning = durable.RunningTask[stepperInput, stepperCheckpoint, stepperResult]
	stepperRuntime = durable.TaskRuntime[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]
)

func newStepper() stepperTask {
	return durable.DefineTask(durable.TaskDefinition[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
		Name:    "test.stepper",
		Version: 1,
		Initial: func(input stepperInput) stepperCheckpoint {
			return stepperCheckpoint{Phase: "plan", Steps: input.Steps}
		},
		Phases: map[string]durable.PhaseHandler[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
			"plan": func(ctx context.Context, _ stepperRunning, runtime stepperRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepperRunning) (*durable.NextTaskState[stepperCheckpoint, stepperResult], error) {
					return &durable.NextTaskState[stepperCheckpoint, stepperResult]{Status: durable.TaskRunning, Checkpoint: &stepperCheckpoint{Phase: "run", Step: 0}}, nil
				})
			},
			"run": func(ctx context.Context, task stepperRunning, runtime stepperRuntime) error {
				ran := 0
				if err := runtime.Hooks().Each("beforeStep", func(handlers stepperHooks) error {
					if handlers.BeforeStep != nil && handlers.BeforeStep(task.State.Checkpoint.Step, nil) == nil {
						ran++
					}
					return nil
				}); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, stepperRunning) (*durable.NextTaskState[stepperCheckpoint, stepperResult], error) {
					return completed[stepperCheckpoint](stepperResult{Ran: ran}), nil
				})
			},
		},
		Abort: func(context.Context, stepperRunning, stepperRuntime) error { return nil },
	})
}

// stepperSnippet declares the stepper and its hook set in a snippet.
const stepperSnippet = `type stepperInput struct{ Steps int }
type stepperCheckpoint struct{ Phase string; Steps, Step int }
type stepperSkip struct{ Skip bool }
type stepperHooks struct{ BeforeStep func(step int, api h.HookApi) *stepperSkip }
stepper := d.DefineTask(d.TaskDefinition[stepperInput, stepperCheckpoint, struct{}, stepperHooks]{Name: "test.stepper", Version: 1, Initial: func(input stepperInput) stepperCheckpoint { return stepperCheckpoint{Phase: "plan", Steps: input.Steps} }})
var harness h.Harness
var conversation h.Conversation
var ctx context.Context
`

func TestTypesUpstreamHarness(t *testing.T) {
	// types.test.ts:248. TaskId carries no result type in Go, so the typed TaskId<{ran}>, SettledTask<{ran}> and
	// TaskId<CompactionResult> expectations assert the Go types of the same calls (TaskId and SettledTask[JsonValue])
	// and decode the settled result into the stepper's type (compaction: harness_compaction_test.go decodes
	// TaskOutcome[CompactionResult]). SubmissionDraft is one struct with every member field; its three exclusivity
	// negatives are TestSubmissionDraftIgnoresTheFieldsOfTheOtherType (harness_submissions_test.go) and missingPhase is
	// TestMissingPhaseHandlerFaultsTheTaskAsAThrowingHandlerDoes.
	t.Run("types submissions, task waits, compaction, and tasks erased into extensions", func(t *testing.T) {
		stepper := newStepper()
		registration := Hook(stepper, stepperHooks{BeforeStep: func(step int, _ HookApi) *stepperSkip {
			if step > 1 {
				return &stepperSkip{Skip: true}
			}
			return nil
		}})
		// Erased into an extension, whatever its input, phases, and hooks.
		extension := new(durable.Extension{Name: "stepper", Tasks: []durable.AnyTask{stepper}, Hooks: []durable.HookRegistration{registration}})
		if registration.Task != "test.stepper" || reflect.TypeOf(registration.Handlers) != reflect.TypeFor[stepperHooks]() {
			t.Fatalf("registration = %+v", registration)
		}

		harness, registry, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		mustInstall(t, registry, extension)
		root, err := harness.Root(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		created, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return durable.CreateTask(tx, stepper, stepperInput{Steps: 2}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		settled, err := harness.WaitForTask(testContext, created.(durable.TaskId))
		if err != nil {
			t.Fatal(err)
		}
		if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted || settled.State.Outcome.Result == nil {
			t.Fatalf("settled = %+v", settled.State)
		}
		if got := (*settled.State.Outcome.Result).(map[string]any); got["ran"] != float64(1) {
			t.Fatalf("result = %v, want the hook at step 0 not to skip", got)
		}
		// TaskId<{ran}> and SettledTask<{ran}> upstream: the result type is the stepper's, so the settled outcome decodes
		// into it. Go reads the result type from the task definition at the decode site instead of from the id.
		typed, err := durable.FromJsonValue[stepperResult](*settled.State.Outcome.Result)
		if err != nil || typed != (stepperResult{Ran: 1}) {
			t.Fatalf("typed result = %+v, %v", typed, err)
		}
		mustClose(t, harness)

		expectCompiles := func(name, body string) { testenv.ExpectCompiles(t, name, harnessImports, stepperSnippet+body) }
		expectTypeError := func(name, body string) { testenv.ExpectTypeError(t, name, harnessImports, stepperSnippet+body) }
		expectCompiles("typed waits and compaction", `
created, _ := conversation.Commit(ctx, func(tx d.Tx) (any, error) {
	return d.CreateTask(tx, stepper, stepperInput{Steps: 2}, d.TaskOptions{Ownership: d.TaskOwnership{Kind: d.TaskOwnedByConversation}})
})
var id d.TaskId = created.(d.TaskId)
var settled d.SettledTask[d.JsonValue]
settled, _ = harness.WaitForTask(ctx, id)
var compaction d.TaskId
compaction, _ = conversation.Compact(ctx, nil)
settled, _ = harness.WaitForTask(ctx, compaction)
var result *h.CompactionResult
_, _ = settled, result`)
		expectCompiles("hook handlers are typed by the task's hooks", `
_ = h.Hook(stepper, stepperHooks{BeforeStep: func(step int, _ h.HookApi) *stepperSkip { return nil }})`)
		// The handler set is well-typed on its own; only Hook's binding of the set to the task's hooks rejects it, as
		// upstream's hook(Stepper, { beforeStep: (_step: string) => undefined }) is an object literal Partial<HooksOf<K>>
		// rejects.
		expectTypeError("hook handlers are typed by the task's hooks", `
type stringStepHooks struct{ BeforeStep func(step string, api h.HookApi) *stepperSkip }
_ = h.Hook(stepper, stringStepHooks{BeforeStep: func(step string, _ h.HookApi) *stepperSkip { return nil }})`)
		expectTypeError("hook names come from the task's hooks", `
_ = h.Hook(stepper, struct{ AfterStep func() }{})`)
		expectTypeError("another task's hook set is not this task's", `
_ = h.Hook(stepper, &h.ToolHooks{})`)
		expectTypeError("task input is typed by the definition", `
var tx d.Tx
_, _ = d.CreateTask(tx, stepper, map[string]any{"steps": "two"}, d.TaskOptions{Ownership: d.TaskOwnership{Kind: d.TaskOwnedByConversation}})`)
	})
}

// types.test.ts:248 missingPhase. Upstream's phase map is exhaustive only for the compiler (`phases: { plan }` for a
// checkpoint union of plan and run is an @ts-expect-error); Go has no string-literal union to key the map. At run time
// scheduler.ts:867 calls `phases[checkpoint.phase]!(...)`, which throws inside the phase's try block, so a checkpoint
// whose phase has no handler is recorded as the phase's failure, as a throwing handler is. Go's scheduler reports the
// missing handler as the phase failure through the same path; the task ends faulted, not hung and not completed.
func TestMissingPhaseHandlerFaultsTheTaskAsAThrowingHandlerDoes(t *testing.T) {
	definition := func(throwing bool) stepperTask {
		task := durable.DefineTask(durable.TaskDefinition[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
			Name:    "test.missing-phase",
			Version: 1,
			Initial: func(input stepperInput) stepperCheckpoint {
				return stepperCheckpoint{Phase: "plan", Steps: input.Steps}
			},
			Phases: map[string]durable.PhaseHandler[stepperInput, stepperCheckpoint, stepperResult, stepperHooks]{
				"plan": func(ctx context.Context, _ stepperRunning, runtime stepperRuntime) error {
					if throwing {
						panic("phase failed")
					}
					return runtime.Commit(ctx, func(durable.Tx, stepperRunning) (*durable.NextTaskState[stepperCheckpoint, stepperResult], error) {
						return &durable.NextTaskState[stepperCheckpoint, stepperResult]{Status: durable.TaskRunning, Checkpoint: &stepperCheckpoint{Phase: "run"}}, nil
					})
				},
			},
			Abort: func(context.Context, stepperRunning, stepperRuntime) error { return nil },
		})
		return task
	}
	outcome := func(t *testing.T, throwing bool) *durable.TaskOutcome[durable.JsonValue] {
		t.Helper()
		task := definition(throwing)
		harness, registry, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		mustInstall(t, registry, &durable.Extension{Name: "missing-phase", Tasks: []durable.AnyTask{task}})
		root, err := harness.Root(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		created, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			return durable.CreateTask(tx, task, stepperInput{Steps: 1}, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		settled, err := harness.WaitForTask(testContext, created.(durable.TaskId))
		if err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
		return settled.State.Outcome
	}
	missing, throwing := outcome(t, false), outcome(t, true)
	if missing == nil || throwing == nil {
		t.Fatalf("outcomes = %+v, %+v", missing, throwing)
	}
	if missing.Status != throwing.Status || missing.Status != durable.OutcomeFaulted {
		t.Fatalf("missing phase %q, throwing phase %q, both want %q", missing.Status, throwing.Status, durable.OutcomeFaulted)
	}
	// The failure names the phase that has no handler (upstream's message is the engine's "is not a function").
	if missing.Error == nil || !strings.Contains(missing.Error.Message, "no phase run") {
		t.Fatalf("missing phase error = %+v", missing.Error)
	}
}
