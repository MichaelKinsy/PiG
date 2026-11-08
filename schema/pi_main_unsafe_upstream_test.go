package schema_test

// pi: packages/schema/src/builders.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// builders.test.ts "type a schema as given with unsafe": the runtime assertions. The static types (`Usage`, `"low" | "high"`, the `Static` of the object) are TypeScript compile-time types and have no Go form.
func TestPiMainTypesASchemaAsGivenWithUnsafe(t *testing.T) {
	usage := schema.Unsafe()
	level := schema.Unsafe(chordjson.ObjectOf("type", "string", "enum", []any{"low", "high"}))
	if !exactJSON(usage, chordjson.ObjectOf()) {
		t.Fatalf("Unsafe() = %s", marshalled(usage))
	}
	if !exactJSON(level, chordjson.ObjectOf("type", "string", "enum", []any{"low", "high"})) {
		t.Fatalf("Unsafe(level) = %s", marshalled(level))
	}
	// obj({ usage: nullable(usage), level }) builds from the unsafe schemas.
	_ = schema.Obj(schema.Properties{schema.Prop("usage", schema.Nullable(usage)), schema.Prop("level", level)})
	// Nothing checks the given type: an unconstrained schema accepts any value.
	if !mustCompile(t, usage).Check("not usage") {
		t.Error(`Unsafe() rejects "not usage"`)
	}
	if mustCompile(t, level).Check("medium") {
		t.Error(`Unsafe(level) accepts "medium"`)
	}
}
