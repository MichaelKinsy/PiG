package contracts_test

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// Ports packages/chord/test/next/contracts/snapshot.test.ts.
//
// Designed out (no Go input): TypeBox refinements, codecs and unsafe types (executable features a Go schema, which is
// JSON data, cannot carry: "refinement", "codec", "Unsafe"); symbol keys and accessor properties; a `maximum:
// undefined` datum (Go has no undefined; the NaN case covers a non-JSON datum); the non-enumerable `~refine` of a
// bytes schema (the bytes check is contracts.CheckBytes, read from the marker).

func TestSnapshotVocabulary(t *testing.T) {
	t.Run("accepts primitives, arrays, tuples, objects, records, unions, intersections, $defs/$ref, constraints", func(t *testing.T) {
		schemas := map[string]contracts.TSchema{
			"tuple":     raw(t, `{"type":"array","additionalItems":false,"items":[{"type":"string"},{"type":"integer"}],"minItems":2}`),
			"intersect": raw(t, `{"allOf":[{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}},{"type":"object","required":["b"],"properties":{"b":{"type":"string"}}}]}`),
			"enum":      raw(t, `{"enum":["a","b"]}`),
			"recursive": raw(t, `{"$defs":{"Node":{"type":"object","required":["value","next"],"properties":{"value":{"type":"number"},"next":{"anyOf":[{"type":"null"},{"$ref":"Node"}]}},"$id":"Node"}},"$ref":"Node"}`),
			"str":       contracts.Str(contracts.Min(1), contracts.Pattern("^x")),
			"annotated": raw(t, `{"type":"string","format":"email","default":"a@b.c","readOnly":true,"title":"t","description":"d"}`),
			"number":    raw(t, `{"type":"number","multipleOf":2,"exclusiveMinimum":0}`),
			"array":     raw(t, `{"type":"array","items":{"type":"string"},"uniqueItems":true}`),
			"object":    raw(t, `{"type":"object","properties":{},"minProperties":1,"maxProperties":2,"$comment":"c","deprecated":true,"examples":[{}]}`),
			"record":    contracts.Map(contracts.Int()),
			"bytes":     contracts.Bytes(contracts.Max(4)),
		}
		for name, schema := range schemas {
			if _, err := contracts.SnapshotSchema(schema, "s"); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	refusals := []struct {
		name    string
		schema  func(t *testing.T) any
		message string
	}{
		{"conditional", text(`{"if":{"type":"string"},"else":{"minLength":1}}`), `unsupported keyword "else"`},
		{"dependent", text(`{"type":"object","dependentSchemas":{"a":{}}}`), `unsupported keyword "dependentSchemas"`},
		{"dependentRequired", text(`{"type":"object","dependentRequired":{"a":["b"]}}`), `unsupported keyword "dependentRequired"`},
		{"not", text(`{"not":{"type":"string"}}`), `unsupported keyword "not"`},
		{"contains", text(`{"type":"array","contains":{"type":"string"}}`), `unsupported keyword "contains"`},
		{"propertyNames", text(`{"type":"object","propertyNames":{"pattern":"^a"}}`), `unsupported keyword "propertyNames"`},
		{"unevaluatedProperties", text(`{"unevaluatedProperties":false}`), `unsupported keyword "unevaluatedProperties"`},
		{"unevaluatedItems", text(`{"unevaluatedItems":false}`), `unsupported keyword "unevaluatedItems"`},
		{"contentSchema", text(`{"type":"string","contentSchema":{}}`), `unsupported keyword "contentSchema"`},
		{"definitions", text(`{"definitions":{}}`), `unsupported keyword "definitions"`},
		{"oneOf", text(`{"oneOf":[{"type":"string"}]}`), `unsupported keyword "oneOf"`},
		{"unknown keyword", text(`{"type":"string","flavour":"x"}`), `unsupported keyword "flavour"`},
		{"non-JSON type", text(`{"type":"bigint"}`), `unsupported type "bigint"`},
		{"undefined", text(`{"type":"undefined"}`), `unsupported type "undefined"`},
		{"function", text(`{"type":"function","parameters":[],"returnType":{"type":"null"}}`), "s: unsupported"},
		{"non-JSON data", func(*testing.T) any { return delta.JsonObjectOf("type", "number", "default", math.NaN()) }, "s.default: expected JSON data"},
		{"foreign prototype", func(*testing.T) any { return struct{}{} }, "s: expected a schema"},
		{"array as schema", func(*testing.T) any { return []any{} }, "s: expected a schema"},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			_, err := contracts.SnapshotSchema(tc.schema(t), "s")
			wantError(t, err, tc.message)
		})
	}

	t.Run("refuses cycles, naming the path", func(t *testing.T) {
		cyclic := delta.JsonObjectOf("type", "array")
		cyclic.Set("items", cyclic)
		_, err := contracts.SnapshotSchema(cyclic, "s")
		wantError(t, err, "s.items: cyclic schema")
		_, err = contracts.SnapshotSchema(raw(t, `{"type":"object","properties":{"a":{"type":1}}}`), "s")
		wantError(t, err, "s.properties.a.type: unsupported type 1")
	})

	t.Run("allows a schema object shared by two places (not a cycle)", func(t *testing.T) {
		shared := contracts.Str()
		_, err := contracts.SnapshotSchema(contracts.Obj(contracts.Prop("a", shared), contracts.Prop("b", shared)), "s")
		wantNoError(t, err)
	})
}

// text is a refusal case's schema from JSON text.
func text(source string) func(t *testing.T) any {
	return func(t *testing.T) any { return js(t, source) }
}

func TestSnapshotClosedArguments(t *testing.T) {
	object := func(properties ...contracts.Property) contracts.TSchema { return tbObject(properties...) }
	str := func() contracts.TSchema { return contracts.Str() }

	t.Run("requires every object, nested ones included, to be closed", func(t *testing.T) {
		_, err := contracts.SnapshotArguments(contracts.Obj(contracts.Prop("a", contracts.Obj(contracts.Prop("b", str())))), "m.args")
		wantNoError(t, err)
		_, err = contracts.SnapshotArguments(contracts.Obj(contracts.Prop("a", object(contracts.Prop("b", str())))), "m.args")
		wantError(t, err, "m.args.properties.a: argument objects must be closed (additionalProperties: false)")
		_, err = contracts.SnapshotArguments(object(), "m.args")
		wantError(t, err, "m.args: argument objects must be closed")
		_, err = contracts.SnapshotArguments(contracts.Obj(contracts.Prop("list", contracts.Arr(object()))), "m.args")
		wantError(t, err, "m.args.properties.list.items: argument objects must be closed")
		_, err = contracts.SnapshotArguments(raw(t, `{"type":"object"}`), "m.args")
		wantError(t, err, "argument objects must be closed")
	})

	t.Run("accepts a raw closed object and records", func(t *testing.T) {
		_, err := contracts.SnapshotArguments(tbClosedObject(contracts.Prop("a", str())), "m")
		wantNoError(t, err)
		_, err = contracts.SnapshotArguments(contracts.Obj(contracts.Prop("tags", contracts.Map(str()))), "m")
		wantNoError(t, err)
	})

	t.Run("checks objects inside $defs, unions and tuples", func(t *testing.T) {
		open := object(contracts.Prop("a", str()))
		_, err := contracts.SnapshotArguments(contracts.Union([]contracts.TSchema{open, str()}), "m")
		wantError(t, err, "m.anyOf[0]: argument objects")
		tuple := contracts.TSchema{Node: delta.JsonObjectOf("type", "array", "additionalItems", false, "items", []any{open.Node}, "minItems", 1.0)}
		_, err = contracts.SnapshotArguments(tuple, "m")
		wantError(t, err, "m.items[0]: argument objects")
		_, err = contracts.SnapshotArguments(delta.JsonObjectOf("$defs", delta.JsonObjectOf("A", open.Node), "$ref", "A"), "m")
		wantError(t, err, "m.$defs.A: argument objects")
	})

	t.Run("does not apply to state or results", func(t *testing.T) {
		_, err := contracts.SnapshotSchema(object(contracts.Prop("a", str())), "state")
		wantNoError(t, err)
	})
}

func TestSnapshots(t *testing.T) {
	t.Run("are copies, independent of later changes to the app's schema objects", func(t *testing.T) {
		app := contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tags", contracts.Arr(contracts.Str())))
		snapshot := must(contracts.SnapshotSchema(app, "s"))
		properties, _ := app.Node.(*delta.JsonObject).Get("properties")
		text, _ := properties.(*delta.JsonObject).Get("text")
		text.(*delta.JsonObject).Set("minLength", 5.0)
		app.Node.(*delta.JsonObject).Set("required", []any{"text", "tags", "x"})
		if snapshot.Schema.Node == app.Node {
			t.Fatal("the snapshot shares the app's schema object")
		}
		if !typebox.Check(snapshot.Schema.Node, delta.JsonObjectOf("text", "", "tags", []any{})) {
			t.Fatal("the snapshot followed the app's later changes")
		}
	})

	t.Run("copy data values (defaults, consts)", func(t *testing.T) {
		list := []any{1.0}
		fallback := delta.JsonObjectOf("a", list)
		node := delta.JsonObjectOf("type", "object", "properties", delta.NewJsonObject(0), "default", fallback, "additionalProperties", false)
		snapshot := must(contracts.SnapshotSchema(node, "s"))
		list[0] = 2.0
		fallback.Set("b", 1.0)
		copied, _ := snapshot.Schema.Node.(*delta.JsonObject).Get("default")
		sameAs(t, copied, js(t, `{"a":[1]}`))
	})

	t.Run("keep the bytes check in the marker; no app callback exists", func(t *testing.T) {
		snapshot := must(contracts.SnapshotSchema(contracts.Bytes(contracts.Max(2)), "s"))
		if !contracts.CheckBytes(snapshot.Schema, make([]byte, 2)) || contracts.CheckBytes(snapshot.Schema, make([]byte, 3)) {
			t.Fatal("the snapshot's bytes bounds are wrong")
		}
		// A hand-written marker works too: the check comes from the marker.
		marker := must(contracts.SnapshotSchema(raw(t, `{"type":"bytes","minLength":1}`), "s")).Schema
		if contracts.CheckBytes(marker, make([]byte, 0)) || !contracts.CheckBytes(marker, make([]byte, 1)) {
			t.Fatal("the hand-written marker's bounds are wrong")
		}
	})

	t.Run("refuse a bytes marker with other keywords or bad bounds", func(t *testing.T) {
		_, err := contracts.SnapshotSchema(raw(t, `{"type":"bytes","items":{}}`), "s")
		wantError(t, err, `s: unsupported keyword "items"`)
		_, err = contracts.SnapshotSchema(raw(t, `{"type":"bytes","maxLength":-1}`), "s")
		wantError(t, err, "s: bytes bounds must be non-negative integers")
		_, err = contracts.SnapshotSchema(raw(t, `{"type":"bytes","maxLength":1.5}`), "s")
		wantError(t, err, "s: bytes bounds must be non-negative integers")
	})

	t.Run("normal form: sorted keys, sorted required and enum, annotations dropped on schema nodes only", func(t *testing.T) {
		snapshot := must(contracts.SnapshotSchema(js(t, `{
			"title":"T","type":"object","required":["b","a"],
			"properties":{"b":{"enum":["y","x"]},"a":{"type":"string","default":{"title":"kept","z":1,"a":2}}},
			"additionalProperties":false}`), "s"))
		want := `{"additionalProperties":false,"properties":{"a":{"default":{"a":2,"title":"kept","z":1},"type":"string"},"b":{"enum":["x","y"]}},"required":["a","b"],"type":"object"}`
		if got := stringify(t, snapshot.Normal); got != want {
			t.Fatalf("normal = %s\nwant     %s", got, want)
		}
	})

	t.Run("normal form keeps union and tuple order", func(t *testing.T) {
		union := func(schemas ...contracts.TSchema) string {
			return stringify(t, must(contracts.SnapshotSchema(contracts.Union(schemas), "s")).Normal)
		}
		if union(contracts.Str(), contracts.Int()) == union(contracts.Int(), contracts.Str()) {
			t.Fatal("union order is part of the normal form")
		}
		sameAs(t, must(contracts.SnapshotSchema(contracts.Opt(contracts.Str()), "s")).Normal, js(t, `{"type":"string"}`))
	})

	t.Run("a snapshot's schema is no longer optional", func(t *testing.T) {
		if must(contracts.SnapshotSchema(contracts.Opt(contracts.Str()), "s")).Schema.Optional {
			t.Fatal("the snapshot kept the optional marker")
		}
	})
}

func TestSnapshotBytes(t *testing.T) {
	t.Run("mark a snapshot with a bytes leaf anywhere inside", func(t *testing.T) {
		bytesOf := func(schema any) bool { return must(contracts.SnapshotSchema(schema, "s")).Bytes }
		if bytesOf(contracts.Str()) {
			t.Error("str() has no bytes leaf")
		}
		if !bytesOf(contracts.Obj(contracts.Prop("a", contracts.Arr(contracts.Obj(contracts.Prop("b", contracts.Bytes())))))) {
			t.Error("a nested bytes leaf is missed")
		}
		if !bytesOf(contracts.Union([]contracts.TSchema{contracts.Str(), contracts.Bytes()})) {
			t.Error("a bytes leaf in a union is missed")
		}
		if bytesOf(raw(t, `{"type":"object","properties":{"a":{"type":"string"}},"default":{"type":"bytes"}}`)) {
			t.Error("a datum that looks like a bytes marker is no leaf")
		}
	})
}
