package durable

import (
	"context"
	"fmt"
)

// Ports packages/durable/src/tasks.ts and the erased task shapes of packages/durable/src/harness/types.ts
// (AnyTask).

// ErasedRunningTask is a running task record with JSON input, checkpoint, and result.
type ErasedRunningTask = RunningTask[JsonValue, JsonValue, JsonValue]

// ErasedTaskRuntime is a task runtime over JSON input, checkpoint, and result, with an untyped hook runner.
type ErasedTaskRuntime = TaskRuntime[JsonValue, JsonValue, JsonValue, any]

// ErasedPhaseHandler runs one phase, or the abort handler, of an erased task definition.
type ErasedPhaseHandler = func(ctx context.Context, task ErasedRunningTask, runtime ErasedTaskRuntime) error

// AnyTaskDefinition is the erased executable task definition stored in the registry.
type AnyTaskDefinition struct {
	Name    string
	Version int
	// Initial decodes a JSON input and returns the JSON first checkpoint.
	Initial func(input JsonValue) (JsonValue, error)
	// Phases maps every phase name to its erased handler.
	Phases map[string]ErasedPhaseHandler
	Abort  ErasedPhaseHandler
	// Migrate is nil when the definition has no migration.
	Migrate func(input JsonValue, checkpoint JsonValue, fromVersion int) (JsonValue, JsonValue, error)
	// Hooks is the definition's hook declaration, or nil.
	Hooks any
}

// AnyTask is an erased executable task definition, whatever its input, phases, and hooks.
type AnyTask interface {
	AnyDefinition() *AnyTaskDefinition
}

// DefineTask defines an executable task. Register it in the registry so a Harness can run tasks of its kind.
func DefineTask[I, S, R, H any](definition TaskDefinition[I, S, R, H]) Task[I, S, R, H] {
	return Task[I, S, R, H]{Definition: &definition, erased: eraseTaskDefinition(&definition)}
}

// AnyDefinition returns the erased definition.
func (task Task[I, S, R, H]) AnyDefinition() *AnyTaskDefinition {
	if task.erased == nil {
		return eraseTaskDefinition(task.Definition)
	}
	return task.erased
}

// CreateTask creates a task in a commit and returns its ID.
func CreateTask[I, S, R, H any](tx Tx, task Task[I, S, R, H], input I, options TaskOptions) (TaskId, error) {
	encoded, err := ToJsonValue(input)
	if err != nil {
		return 0, err
	}
	return tx.CreateTaskErased(task, encoded, options)
}

func eraseTaskDefinition[I, S, R, H any](definition *TaskDefinition[I, S, R, H]) *AnyTaskDefinition {
	erased := &AnyTaskDefinition{
		Name:    definition.Name,
		Version: definition.Version,
		Initial: func(input JsonValue) (JsonValue, error) {
			typed, err := FromJsonValue[I](input)
			if err != nil {
				return nil, err
			}
			return ToJsonValue(definition.Initial(typed))
		},
		Phases: make(map[string]ErasedPhaseHandler, len(definition.Phases)),
		Hooks:  any(definition.Hooks),
	}
	for phase, handler := range definition.Phases {
		erased.Phases[phase] = eraseHandler(handler)
	}
	if definition.Abort != nil {
		erased.Abort = eraseHandler(definition.Abort)
	}
	if migrate := definition.Migrate; migrate != nil {
		erased.Migrate = func(input JsonValue, checkpoint JsonValue, fromVersion int) (JsonValue, JsonValue, error) {
			typedInput, typedCheckpoint, err := migrate(input, checkpoint, fromVersion)
			if err != nil {
				return nil, nil, err
			}
			encodedInput, err := ToJsonValue(typedInput)
			if err != nil {
				return nil, nil, err
			}
			encodedCheckpoint, err := ToJsonValue(typedCheckpoint)
			if err != nil {
				return nil, nil, err
			}
			return encodedInput, encodedCheckpoint, nil
		}
	}
	return erased
}

func eraseHandler[I, S, R, H any](handler func(ctx context.Context, task RunningTask[I, S, R], runtime TaskRuntime[I, S, R, H]) error) ErasedPhaseHandler {
	return func(ctx context.Context, task ErasedRunningTask, runtime ErasedTaskRuntime) error {
		typed, err := DecodeTaskRecord[I, S, R](task)
		if err != nil {
			return err
		}
		return handler(ctx, typed, typedTaskRuntime[I, S, R, H]{ErasedTaskRuntime: runtime})
	}
}

// DecodeTaskRecord decodes an erased task record into typed input, checkpoint, and result.
func DecodeTaskRecord[I, S, R any](record TaskRecord[JsonValue, JsonValue, JsonValue]) (TaskRecord[I, S, R], error) {
	return FromJsonValue[TaskRecord[I, S, R]](record)
}

// EncodeTaskRecord encodes a typed task record into its erased JSON form.
func EncodeTaskRecord[I, S, R any](record TaskRecord[I, S, R]) (TaskRecord[JsonValue, JsonValue, JsonValue], error) {
	return FromJsonValue[TaskRecord[JsonValue, JsonValue, JsonValue]](record)
}

// typedTaskRuntime adapts an erased runtime to a typed task: commits decode the current record and encode the next
// state, and hook dispatch hands the typed handler set.
type typedTaskRuntime[I, S, R, H any] struct {
	ErasedTaskRuntime
}

func (runtime typedTaskRuntime[I, S, R, H]) Commit(ctx context.Context, change func(tx Tx, current RunningTask[I, S, R]) (*NextTaskState[S, R], error)) error {
	return runtime.ErasedTaskRuntime.Commit(ctx, func(tx Tx, current ErasedRunningTask) (*NextTaskState[JsonValue, JsonValue], error) {
		typed, err := DecodeTaskRecord[I, S, R](current)
		if err != nil {
			return nil, err
		}
		next, err := change(tx, typed)
		if err != nil || next == nil {
			return nil, err
		}
		erased, err := FromJsonValue[NextTaskState[JsonValue, JsonValue]](*next)
		if err != nil {
			return nil, err
		}
		return &erased, nil
	})
}

func (runtime typedTaskRuntime[I, S, R, H]) Hooks() HookRunner[H] {
	return typedHookRunner[H]{runner: runtime.ErasedTaskRuntime.Hooks()}
}

type typedHookRunner[H any] struct {
	runner HookRunner[any]
}

func (runner typedHookRunner[H]) Each(name string, invoke func(handlers H) error) error {
	return runner.runner.Each(name, func(handlers any) error {
		typed, ok := handlers.(H)
		if !ok {
			var zero H
			return fmt.Errorf("hook handlers for %s are %T, not %T", name, handlers, zero)
		}
		return invoke(typed)
	})
}
