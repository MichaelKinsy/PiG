package schema_test

// Ports packages/schema/test/builders.test.ts: the builders: the JSON Schema they produce and how they compile.
// The static-type assertions of the upstream test (expectTypeOf over Static<...>) are compile-time TypeScript with no Go form and are not ported.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// sameJSON compares a schema with expected JSON text, ignoring member order as vitest's toEqual does.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	text, err := chordjson.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(text, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("got %s, want %s", text, want)
	}
}

func TestBuildersProducePlainJSONSchema(t *testing.T) {
	s := schema.Obj(schema.Properties{
		schema.Prop("path", schema.Str(schema.Description("File path"), schema.MinLength(1))),
		schema.Opt("offset", schema.Int(schema.Minimum(1))),
		schema.Prop("mode", schema.OneOf([]any{"read", "write"})),
	})
	text, err := chordjson.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, mustDecode(t, string(text)), `{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "File path", "minLength": 1 },
			"offset": { "type": "integer", "minimum": 1 },
			"mode": { "enum": ["read", "write"] }
		},
		"required": ["path", "mode"],
		"additionalProperties": false
	}`)
	if got := string(text); got != `{"type":"object","properties":{"path":{"type":"string","description":"File path","minLength":1},"offset":{"type":"integer","minimum":1},"mode":{"enum":["read","write"]}},"required":["path","mode"],"additionalProperties":false}` {
		t.Fatalf("member order = %s", got)
	}
}

func TestBuildersProduceEveryLeaf(t *testing.T) {
	sameJSON(t, schema.Str(), `{"type":"string"}`)
	sameJSON(t, schema.Num(), `{"type":"number"}`)
	sameJSON(t, schema.Int(), `{"type":"integer"}`)
	sameJSON(t, schema.Bool(), `{"type":"boolean"}`)
	sameJSON(t, schema.Nul(), `{"type":"null"}`)
	sameJSON(t, schema.Unknown(), `{}`)
	sameJSON(t, schema.Lit(3), `{"const":3}`)
	sameJSON(t, schema.Bytes(schema.MaxLength(8)), `{"type":"bytes","maxLength":8}`)
	sameJSON(t, schema.Arr(schema.Str(), schema.MaxItems(2)), `{"type":"array","items":{"type":"string"},"maxItems":2}`)
	sameJSON(t, schema.Map(schema.Num()), `{"type":"object","additionalProperties":{"type":"number"}}`)
	sameJSON(t, schema.Tuple([]schema.Schema{schema.Str(), schema.Num()}), `{
		"type": "array",
		"prefixItems": [{ "type": "string" }, { "type": "number" }],
		"items": false,
		"minItems": 2
	}`)
	sameJSON(t, schema.Nullable(schema.Str()), `{"anyOf":[{"type":"string"},{"type":"null"}]}`)
	sameJSON(t, schema.Obj(schema.Properties{schema.Prop("a", schema.Str())}, schema.Open()), `{
		"type": "object",
		"properties": { "a": { "type": "string" } },
		"required": ["a"]
	}`)
}

func TestBuildersAcceptEveryAnnotationAndBound(t *testing.T) {
	sameJSON(t, schema.Str(schema.Title("t"), schema.Description("d"), schema.Examples("a", "b"), schema.Deprecated(true), schema.Default("a"),
		schema.MinLength(1), schema.MaxLength(3), schema.Pattern("^a"), schema.Format("email")),
		`{"type":"string","title":"t","description":"d","examples":["a","b"],"deprecated":true,"default":"a","minLength":1,"maxLength":3,"pattern":"^a","format":"email"}`)
	sameJSON(t, schema.Num(schema.Minimum(0), schema.Maximum(9), schema.ExclusiveMinimum(-1), schema.ExclusiveMaximum(10), schema.MultipleOf(0.5)),
		`{"type":"number","minimum":0,"maximum":9,"exclusiveMinimum":-1,"exclusiveMaximum":10,"multipleOf":0.5}`)
	sameJSON(t, schema.Arr(schema.Int(), schema.MinItems(1), schema.UniqueItems(true)), `{"type":"array","items":{"type":"integer"},"minItems":1,"uniqueItems":true}`)
	sameJSON(t, schema.Nul(schema.Default(nil)), `{"type":"null","default":null}`)
	sameJSON(t, schema.Lit(true, schema.Description("yes")), `{"const":true,"description":"yes"}`)
	sameJSON(t, schema.Union([]schema.Schema{schema.Lit("a"), schema.Lit("b")}, schema.Title("u")), `{"anyOf":[{"const":"a"},{"const":"b"}],"title":"u"}`)
}

func TestBuildersCompileIntoChecksWithTheDeclaredSemantics(t *testing.T) {
	v := mustCompile(t, schema.Obj(schema.Properties{
		schema.Prop("name", schema.Str(schema.MinLength(1))),
		schema.Opt("tags", schema.Arr(schema.Str(), schema.UniqueItems(true))),
	}))
	if !v.Check(mustDecode(t, `{"name":"a"}`)) {
		t.Fatal("name only is valid")
	}
	if !v.Check(mustDecode(t, `{"name":"a","tags":["x","y"]}`)) {
		t.Fatal("unique tags are valid")
	}
	wantErrors(t, v.Errors(mustDecode(t, `{"name":"","tags":["x","x"],"other":1}`)), []schema.ValidationError{
		{Path: "/name", Keyword: "minLength", Message: "expected at least 1 characters"},
		{Path: "/tags", Keyword: "uniqueItems", Message: "expected unique items"},
		{Path: "/other", Keyword: "additionalProperties", Message: "unknown field"},
	})
}

func TestBuildersNarrowValuesThroughACompiledCheck(t *testing.T) {
	point := schema.Obj(schema.Properties{schema.Prop("x", schema.Num()), schema.Prop("y", schema.Num()), schema.Opt("label", schema.Str())})
	check := mustCompile(t, point).Check
	value := mustDecode(t, `{"x":1,"y":2}`).(*schema.Object)
	if !check(value) {
		t.Fatal("expected a point")
	}
	if got := value.Value("x").(float64) + value.Value("y").(float64); got != 3 {
		t.Fatalf("x+y = %v", got)
	}
}

func TestBuildersAreDeterministicForTagUnions(t *testing.T) {
	block := schema.Union([]schema.Schema{
		schema.Obj(schema.Properties{schema.Prop("kind", schema.Lit("text")), schema.Prop("text", schema.Str())}),
		schema.Obj(schema.Properties{schema.Prop("kind", schema.Lit("image")), schema.Prop("width", schema.Int(schema.Minimum(0)))}),
	})
	wantErrors(t, mustCompile(t, block).Errors(mustDecode(t, `{"kind":"video"}`)), []schema.ValidationError{
		{Path: "/kind", Keyword: "anyOf", Message: `expected one of ["text","image"]`},
	})
}

// Obj follows JavaScript's own-key order, as Pi's obj() does when it reads Object.entries(properties): array-index names first in ascending order, then the others as declared; a name declared twice keeps its first position and its last declaration.
func TestObjUsesOwnKeyOrder(t *testing.T) {
	s := schema.Obj(schema.Properties{
		schema.Prop("b", schema.Str()),
		schema.Prop("10", schema.Int()),
		schema.Opt("a", schema.Str()),
		schema.Prop("2", schema.Int()),
	})
	text, err := chordjson.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"object","properties":{"2":{"type":"integer"},"10":{"type":"integer"},"b":{"type":"string"},"a":{"type":"string"}},"required":["2","10","b"],"additionalProperties":false}`
	if string(text) != want {
		t.Fatalf("got %s\nwant %s", text, want)
	}
	text, err = chordjson.Marshal(schema.Obj(schema.Properties{schema.Prop("a", schema.Str()), schema.Prop("b", schema.Str()), schema.Opt("a", schema.Int())}, schema.Open(), schema.Description("d")))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != `{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b"],"description":"d"}` {
		t.Fatalf("duplicate name: %s", text)
	}
}
