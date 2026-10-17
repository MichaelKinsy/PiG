package durable

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeErasedRuntime records the erased commits a typed phase makes through the adapter.
type fakeErasedRuntime struct {
	ErasedTaskRuntime
	current ErasedRunningTask
	hooks   []any
	commits []NextTaskState[JsonValue, JsonValue]
}

func (runtime *fakeErasedRuntime) Commit(_ context.Context, change func(Tx, ErasedRunningTask) (*NextTaskState[JsonValue, JsonValue], error)) error {
	next, err := change(nil, runtime.current)
	if err != nil {
		return err
	}
	if next != nil {
		runtime.commits = append(runtime.commits, *next)
		runtime.current.State = *next
	}
	return nil
}

func (runtime *fakeErasedRuntime) Hooks() HookRunner[any] { return fakeHookRunner(runtime.hooks) }

type fakeHookRunner []any

func (runner fakeHookRunner) Each(_ string, invoke func(any) error) error {
	for _, handlers := range runner {
		if err := invoke(handlers); err != nil {
			return err
		}
	}
	return nil
}

// The erased phase handlers decode the JSON record into the typed task, hand typed hook sets, and encode the typed
// next state, as the Harness scheduler drives them.
func TestErasedPhasesRunTheTypedTask(t *testing.T) {
	definition := DefineTask(stepperDefinition()).AnyDefinition()
	checkpoint, err := definition.Initial(map[string]any{"steps": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeErasedRuntime{
		current: ErasedRunningTask{
			Id: 4, ConversationId: 1, Kind: definition.Name, Version: 1, Input: map[string]any{"steps": float64(2)},
			State: TaskState[JsonValue, JsonValue]{Status: TaskRunning, Checkpoint: &checkpoint},
		},
		hooks: []any{
			stepperHooks{BeforeStep: func(int) *struct{ Skip bool } { return nil }},
			stepperHooks{},
			stepperHooks{BeforeStep: func(step int) *struct{ Skip bool } { return &struct{ Skip bool }{Skip: step > 1} }},
		},
	}
	for _, phase := range []string{"plan", "run"} {
		var checkpoint struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(jsonOf(t, *runtime.current.State.Checkpoint)), &checkpoint); err != nil {
			t.Fatal(err)
		}
		if got := `"` + checkpoint.Phase + `"`; got != `"`+phase+`"` {
			t.Fatalf("phase = %s, want %s", got, phase)
		}
		if err := definition.Phases[phase](context.Background(), runtime.current, runtime); err != nil {
			t.Fatal(err)
		}
	}
	if got := jsonOf(t, runtime.commits); got != `[{"status":"running","checkpoint":{"phase":"run","step":2}},{"status":"terminal","outcome":{"status":"completed","result":{"ran":1}}}]` {
		t.Fatalf("commits = %s", got)
	}
}
