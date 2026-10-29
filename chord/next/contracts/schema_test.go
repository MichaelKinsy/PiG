package contracts_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// Ports packages/chord/test/next/contracts/schema.test.ts. The Static<> checks and expectTypeOf are type-level only
// (designed out: a Go schema is data, values are JSON). TypeBox's compiler is typebox.Check over the schema node;
// the bytes leaf is checked natively by contracts.CheckBytes.

func TestSchemaHelpers(t *testing.T) {
	t.Run("produce ordinary TypeBox schemas with bounds under their JSON Schema names", func(t *testing.T) {
		sameJSON(t, contracts.Str(contracts.Min(1), contracts.Max(3), contracts.Pattern("^a")),
			`{"type":"string","minLength":1,"maxLength":3,"pattern":"^a"}`)
		sameJSON(t, contracts.Num(contracts.Min(0)), `{"type":"number","minimum":0}`)
		sameJSON(t, contracts.Int(contracts.Max(9)), `{"type":"integer","maximum":9}`)
		sameJSON(t, contracts.Arr(contracts.Bool(), contracts.Min(1), contracts.Max(2)),
			`{"type":"array","items":{"type":"boolean"},"minItems":1,"maxItems":2}`)
		sameJSON(t, contracts.Nul(), `{"type":"null"}`)
		sameJSON(t, contracts.Lit(3), `{"type":"number","const":3}`)
		sameAs(t, contracts.OneOf("a", "b"), delta.JsonObjectOf("anyOf", []any{contracts.Lit("a").Node, contracts.Lit("b").Node}))
		sameJSON(t, contracts.Union([]contracts.TSchema{contracts.Str(), contracts.Nul()}),
			`{"anyOf":[{"type":"string"},{"type":"null"}]}`)
		sameJSON(t, contracts.Map(contracts.Int()), `{"type":"object","patternProperties":{"^.*$":{"type":"integer"}}}`)
		sameJSON(t, contracts.Unknown(), `{}`)
	})

	t.Run("literals keep their type and bounds are numbers", func(t *testing.T) {
		sameJSON(t, contracts.Lit("x"), `{"type":"string","const":"x"}`)
		sameJSON(t, contracts.Lit(true), `{"type":"boolean","const":true}`)
		sameJSON(t, contracts.Str(contracts.Default("d")), `{"type":"string","default":"d"}`)
		sameJSON(t, contracts.Int(contracts.Default(3.0)), `{"type":"integer","default":3}`)
		sameJSON(t, contracts.Bool(contracts.Default(true)), `{"type":"boolean","default":true}`)
	})

	t.Run("leave absent bounds out instead of writing undefined", func(t *testing.T) {
		if got := contracts.Str().Node.(*delta.JsonObject).Keys(); !reflect.DeepEqual(got, []string{"type"}) {
			t.Fatalf("str keys = %v", got)
		}
		if got := contracts.Bytes().Node.(*delta.JsonObject).Keys(); !reflect.DeepEqual(got, []string{"type"}) {
			t.Fatalf("bytes keys = %v", got)
		}
	})

	t.Run("obj() is always closed and opt() makes a field optional", func(t *testing.T) {
		schema := contracts.Obj(contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Opt(contracts.Int())))
		sameJSON(t, schema, `{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"integer"}},"additionalProperties":false}`)
		if !typebox.Check(schema.Node, delta.JsonObjectOf("a", "x")) {
			t.Fatal("{a} should pass")
		}
		if typebox.Check(schema.Node, delta.JsonObjectOf("a", "x", "c", 1.0)) {
			t.Fatal("{a, c} should fail")
		}
	})

	t.Run("opt() marks the schema optional and leaves the data alone", func(t *testing.T) {
		plain := contracts.Int()
		optional := contracts.Opt(plain)
		if !optional.Optional || plain.Optional {
			t.Fatalf("optional = %v, plain = %v", optional.Optional, plain.Optional)
		}
		sameJSON(t, optional, `{"type":"integer"}`)
	})

	t.Run("TypeBox's checker works unchanged", func(t *testing.T) {
		schema := contracts.Obj(
			contracts.Prop("kind", contracts.OneOf("chat", "agent")),
			contracts.Prop("n", contracts.Opt(contracts.Int())),
			contracts.Prop("tags", contracts.Arr(contracts.Str())),
		)
		if !typebox.Check(schema.Node, delta.JsonObjectOf("kind", "chat", "tags", []any{})) {
			t.Fatal("value should pass")
		}
		if typebox.Check(schema.Node, delta.JsonObjectOf("kind", "other", "tags", []any{})) {
			t.Fatal("kind outside the union should fail")
		}
	})
}

func TestSchemaBytes(t *testing.T) {
	t.Run("is the data marker", func(t *testing.T) {
		schema := contracts.Bytes(contracts.Min(1), contracts.Max(2))
		if got := stringify(t, schema.Node); got != `{"type":"bytes","minLength":1,"maxLength":2}` {
			t.Fatalf("marker = %s", got)
		}
		if got := schema.Node.(*delta.JsonObject).Keys(); !reflect.DeepEqual(got, []string{"type", "minLength", "maxLength"}) {
			t.Fatalf("keys = %v", got)
		}
		// Upstream attaches the check as a non-enumerable `~refine`; a Go schema holds data only and
		// contracts.CheckBytes reads the marker (designed out: no executable feature).
	})

	t.Run("checks Uint8Array (a []byte) and the length bounds", func(t *testing.T) {
		schema := contracts.Bytes(contracts.Min(1), contracts.Max(2))
		for _, tc := range []struct {
			name  string
			value any
			want  bool
		}{
			{"one byte", make([]byte, 1), true},
			{"two bytes", make([]byte, 2), true},
			{"none", make([]byte, 0), false},
			{"three", make([]byte, 3), false},
			{"an array of numbers", []any{1.0}, false},
			{"a string", "ab", false},
			{"nil", nil, false},
		} {
			if got := contracts.CheckBytes(schema, tc.value); got != tc.want {
				t.Errorf("%s: CheckBytes = %v, want %v", tc.name, got, tc.want)
			}
		}
		if !contracts.CheckBytes(contracts.Bytes(), []byte{}) {
			t.Error("an unbounded bytes accepts the empty value")
		}
		if contracts.CheckBytes(contracts.Str(), []byte{1}) {
			t.Error("a string schema is no bytes schema")
		}
	})
}

func TestSchemaMethod(t *testing.T) {
	t.Run("an omitted side is null", func(t *testing.T) {
		same := func(got, want *contracts.Method) {
			t.Helper()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		}
		same(contracts.NewMethod(), &contracts.Method{Args: contracts.Nul(), Result: contracts.Nul()})
		same(contracts.NewMethod(contracts.Str()), &contracts.Method{Args: contracts.Str(), Result: contracts.Nul()})
		same(contracts.NewMethod(contracts.TSchema{}, contracts.Int()), &contracts.Method{Args: contracts.Nul(), Result: contracts.Int()})
		same(contracts.NewMethod(contracts.Str(), contracts.Int()), &contracts.Method{Args: contracts.Str(), Result: contracts.Int()})
	})

	t.Run("keyed(schema) holds the schema", func(t *testing.T) {
		member := contracts.Keyed(contracts.Str())
		if !reflect.DeepEqual(member.Schema, contracts.Str()) {
			t.Fatalf("schema = %+v", member.Schema)
		}
	})
}
