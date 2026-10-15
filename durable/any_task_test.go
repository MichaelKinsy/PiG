package durable

import "testing"

// upstream: packages/durable/src/harness/types.ts:91-101 AnyTask = { readonly definition: { name, version, initial, phases, abort, migrate?, hooks? } }; the registry and conversations read exactly those members of a task.
func TestAnyTaskExposesTheErasedDefinitionPiReads(t *testing.T) {
	task := DefineTask(stepperDefinition())
	erased := task.Erased()
	definition := erased.Definition
	if definition.Name != "test.stepper" || definition.Version != 1 {
		t.Fatalf("name, version = %q, %d", definition.Name, definition.Version)
	}
	if definition.Initial == nil || definition.Abort == nil {
		t.Fatal("the erased definition lacks initial or abort")
	}
	if len(definition.Phases) != 2 || definition.Phases["plan"] == nil || definition.Phases["run"] == nil {
		t.Fatalf("phases = %v, want plan and run", definition.Phases)
	}
	if definition.Migrate != nil {
		t.Fatal("a definition without a migration carries one")
	}
	// hooks?: the erased definition carries the task's hook declaration.
	if _, ok := definition.Hooks.(stepperHooks); !ok {
		t.Fatalf("hooks = %T, want the task's stepperHooks declaration", definition.Hooks)
	}
	checkpoint, err := definition.Initial(map[string]any{"steps": float64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonOf(t, checkpoint); got != `{"phase":"plan","steps":3}` {
		t.Fatalf("initial checkpoint = %s", got)
	}
	// An extension lists the task as an AnyTask, and the erased definition is the task's own.
	extension := Extension{Tasks: []AnyTask{task.Erased()}}
	if extension.Tasks[0].Definition != definition {
		t.Fatal("the extension's AnyTask reports another erased definition")
	}
}

// upstream: packages/durable/src/harness/types.ts:91 AnyTask is an object holding the definition, so a caller builds one from a definition alone, and a task registered through it is the task's definition.
func TestAnyTaskIsBuiltFromADefinitionAlone(t *testing.T) {
	definition := DefineTask(stepperDefinition()).AnyDefinition()
	task := AnyTask{Definition: definition}
	if task.AnyDefinition() != definition || task.Definition.Name != "test.stepper" {
		t.Fatalf("AnyTask{Definition} = %+v", task)
	}
	if (Task[JsonValue, JsonValue, JsonValue, any]{}).Erased().Definition != nil {
		t.Fatal("the zero AnyTask holds a definition")
	}
}
