package schema_test

// pi: packages/schema/src/coerce.ts
// Ports packages/schema/test/coerce.test.ts (Pi b556cfe5): CompileCoercing, Coerce and Parse, the three coercion passes (optional nulls, TypeBox's conversion rules, primitive coercion by type), their order where they conflict, and the guarantees around them. Ports pi-ai's tool argument coercion tests.
//
// Go forms of what the TypeScript tests express differently:
//   - `expectTypeOf` assertions are compile-time types (`Static<S>`) and have no Go form. "adds coerce and parse only with coerce: true" is the method set: a Validator has no Coerce or Parse, a CoercingValidator has both.
//   - "works on frozen schemas" asserts the schema is only read; Go has no Object.freeze, so the Go form checks that the schema is unchanged after coercing.
//   - "still coerces when the Function constructor is unavailable" has no Go counterpart (nothing here generates code); it keeps the coercion assertion.
//   - Cases that build TypeBox schemas (`Type.Record`, `Type.Cyclic`) use the JSON TypeBox 1.3.27 emits for them.

import (
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// equalJSON is vitest's toEqual for JSON values: member order is ignored, +0 and -0 differ, NaN equals NaN.
func equalJSON(a, b any) bool { return sameJSONValue(a, b, false) }

// exactJSON is equalJSON that also compares member order.
func exactJSON(a, b any) bool { return sameJSONValue(a, b, true) }

func sameJSONValue(a, b any, ordered bool) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && (x == y && math.Signbit(x) == math.Signbit(y) || math.IsNaN(x) && math.IsNaN(y))
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSONValue(x[i], y[i], ordered) {
				return false
			}
		}
		return true
	case *schema.Object, map[string]any:
		return sameObjects(a, b, ordered)
	case *big.Int:
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	case schema.UndefinedType:
		_, ok := b.(schema.UndefinedType)
		return ok
	}
	return false
}

func objectKeysAndValues(v any) (keys []string, get func(string) (any, bool), ok bool) {
	switch x := v.(type) {
	case *schema.Object:
		return x.Keys(), x.Get, true
	case map[string]any:
		keys = chordjson.MapOwnKeys(x)
		return keys, func(k string) (any, bool) { v, ok := x[k]; return v, ok }, true
	}
	return nil, nil, false
}

func sameObjects(a, b any, ordered bool) bool {
	ak, aget, aok := objectKeysAndValues(a)
	bk, bget, bok := objectKeysAndValues(b)
	if !aok || !bok || len(ak) != len(bk) {
		return false
	}
	if ordered {
		for i := range ak {
			if ak[i] != bk[i] {
				return false
			}
		}
	}
	for _, k := range ak {
		av, _ := aget(k)
		bv, present := bget(k)
		if !present || !sameJSONValue(av, bv, ordered) {
			return false
		}
	}
	return true
}

func wantJSON(t testing.TB, got any, want string) {
	t.Helper()
	if !equalJSON(got, mustDecode(t, want)) {
		t.Fatalf("got %s, want %s", marshalled(got), want)
	}
}

func marshalled(v any) string {
	text, err := chordjson.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(text)
}

// coercing compiles schema with the formats, as Pi's `compile(schema, { formats, coerce: true })`, or with the given options.
func coercing(t testing.TB, s any, options ...schema.CompileOptions) *schema.CoercingValidator {
	t.Helper()
	opts := schema.CompileOptions{Formats: formats.Formats()}
	if len(options) > 0 {
		opts = options[0]
	}
	v, err := schema.CompileCoercing(s, opts)
	if err != nil {
		t.Fatalf("CompileCoercing: %v", err)
	}
	return v
}

func coerceOf(t testing.TB, v *schema.CoercingValidator, value any) any {
	t.Helper()
	got, err := v.Coerce(value)
	if err != nil {
		t.Fatalf("Coerce: %v", err)
	}
	return got
}

func parseOf(t testing.TB, v *schema.CoercingValidator, value any) schema.ParseResult {
	t.Helper()
	got, err := v.Parse(value)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func checkOf(t testing.TB, v *schema.CoercingValidator, value any) bool {
	t.Helper()
	ok, err := v.Check(value)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return ok
}

// coerce is the test file's `coerce(schema, value)`.
func coerce(t testing.TB, s any, value any) any {
	t.Helper()
	return coerceOf(t, coercing(t, s), value)
}

// coerceValue is the coerced `value` property of `{ value }` under an object schema with one required property `value`.
func coerceValue(t testing.TB, schemaText, valueText string) any {
	t.Helper()
	root := mustDecode(t, `{"type":"object","properties":{"value":`+schemaText+`},"required":["value"]}`)
	input := mustDecode(t, `{"value":`+valueText+`}`)
	return coerce(t, root, input).(*schema.Object).Value("value")
}

func wantParsed(t testing.TB, got schema.ParseResult, valueText string) {
	t.Helper()
	if !got.OK || len(got.Errors) != 0 {
		t.Fatalf("Parse = %+v, want ok with %s", got, valueText)
	}
	wantJSON(t, got.Value, valueText)
}

func wantParseErrors(t *testing.T, got schema.ParseResult, want []schema.ValidationError) {
	t.Helper()
	if got.OK || got.Value != nil {
		t.Fatalf("Parse = %+v, want errors %v", got, want)
	}
	wantErrors(t, got.Errors, want)
}

func obj(properties schema.Properties, options ...schema.ObjectOption) schema.Schema {
	return schema.Obj(properties, options...)
}

func TestCoerceUpstream(t *testing.T) {
	t.Run("compile options", func(t *testing.T) {
		t.Run("adds coerce and parse only with coerce: true", func(t *testing.T) {
			plain := reflect.TypeFor[*schema.Validator]()
			for _, name := range []string{"Coerce", "Parse"} {
				if _, ok := plain.MethodByName(name); ok {
					t.Fatalf("a Validator has %s", name)
				}
				if _, ok := reflect.TypeFor[*schema.CoercingValidator]().MethodByName(name); !ok {
					t.Fatalf("a CoercingValidator lacks %s", name)
				}
			}
			coercingValidator := coercing(t, obj(schema.Properties{schema.Prop("n", schema.Num())}))
			if !checkOf(t, coercingValidator, mustDecode(t, `{"n":1}`)) {
				t.Fatal("check of a valid value")
			}
		})
	})

	t.Run("coerce", func(t *testing.T) {
		t.Run("never modifies the value", func(t *testing.T) {
			value := mustDecode(t, `{"count":"42","tags":"a"}`)
			s := obj(schema.Properties{schema.Prop("count", schema.Num()), schema.Prop("tags", schema.Arr(schema.Str()))})
			wantJSON(t, coerce(t, s, value), `{"count":42,"tags":["a"]}`)
			wantJSON(t, value, `{"count":"42","tags":"a"}`)
		})

		t.Run("returns the value itself when nothing changes", func(t *testing.T) {
			value := mustDecode(t, `{"a":{"b":[1,2]},"c":"x"}`)
			s := obj(schema.Properties{
				schema.Prop("a", obj(schema.Properties{schema.Prop("b", schema.Arr(schema.Num()))})),
				schema.Prop("c", schema.Str()),
			})
			if got := coerce(t, s, value); got != value {
				t.Fatalf("coerce returned a different value: %s", marshalled(got))
			}
		})

		t.Run("copies only the changed paths", func(t *testing.T) {
			value := mustDecode(t, `{"a":{"b":[1,2]},"c":{"d":"1"}}`).(*schema.Object)
			unchanged := value.Value("a")
			s := obj(schema.Properties{
				schema.Prop("a", obj(schema.Properties{schema.Prop("b", schema.Arr(schema.Num()))})),
				schema.Prop("c", obj(schema.Properties{schema.Prop("d", schema.Num())})),
			})
			coerced := coerce(t, s, value)
			wantJSON(t, coerced, `{"a":{"b":[1,2]},"c":{"d":1}}`)
			if coerced == any(value) {
				t.Fatal("the changed value is not a copy")
			}
			if coerced.(*schema.Object).Value("a") != unchanged {
				t.Fatal("the unchanged member was copied")
			}
			wantJSON(t, value.Value("c"), `{"d":"1"}`)
		})

		t.Run("works on frozen schemas", func(t *testing.T) {
			s := mustDecode(t, `{"type":"object","properties":{"n":{"type":"integer"}}}`)
			wantJSON(t, coerce(t, s, mustDecode(t, `{"n":"7"}`)), `{"n":7}`)
			wantJSON(t, s, `{"type":"object","properties":{"n":{"type":"integer"}}}`)
		})

		t.Run("still coerces when the Function constructor is unavailable", func(t *testing.T) {
			s := obj(schema.Properties{schema.Prop("count", schema.Num())})
			wantJSON(t, coerce(t, s, mustDecode(t, `{"count":"42"}`)), `{"count":42}`)
		})

		t.Run("leaves values it cannot coerce for the check to report", func(t *testing.T) {
			v := coercing(t, obj(schema.Properties{schema.Prop("count", schema.Num())}))
			coerced := coerceOf(t, v, mustDecode(t, `{"count":"many"}`))
			wantJSON(t, coerced, `{"count":"many"}`)
			if checkOf(t, v, coerced) {
				t.Fatal("the uncoerced value is valid")
			}
		})
	})

	t.Run("parse", func(t *testing.T) {
		t.Run("returns the coerced value when it is valid", func(t *testing.T) {
			v := coercing(t, obj(schema.Properties{schema.Prop("count", schema.Num()), schema.Prop("tags", schema.Arr(schema.Str()))}))
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"count":"42","tags":"a"}`)), `{"count":42,"tags":["a"]}`)
		})

		t.Run("returns the errors of the coerced value when it is invalid", func(t *testing.T) {
			v := coercing(t, obj(schema.Properties{schema.Prop("count", schema.Num()), schema.Prop("name", schema.Str())}))
			wantParseErrors(t, parseOf(t, v, mustDecode(t, `{"count":"many","name":7}`)), []schema.ValidationError{
				{Path: "/count", Keyword: "type", Message: "expected number, got string"},
			})
		})
	})

	t.Run("pass 1: optional nulls", func(t *testing.T) {
		t.Run("drops null in optional non-nullable properties, also nested", func(t *testing.T) {
			s := obj(schema.Properties{
				schema.Prop("path", schema.Str()),
				schema.Opt("offset", schema.Num()),
				schema.Opt("nullable", schema.Nullable(schema.Str())),
				schema.Prop("metadata", obj(schema.Properties{schema.Opt("enabled", schema.Bool())})),
			}, schema.Open())
			got := coerce(t, s, mustDecode(t, `{"path":"file.txt","offset":null,"nullable":null,"metadata":{"enabled":null}}`))
			wantJSON(t, got, `{"path":"file.txt","nullable":null,"metadata":{}}`)
		})

		t.Run("keeps a null whose property is a $ref (the reference may allow it)", func(t *testing.T) {
			s := mustDecode(t, `{
				"type": "object",
				"properties": { "value": { "$ref": "#/$defs/value" } },
				"$defs": { "value": { "anyOf": [{ "type": "number" }, { "type": "null" }] } }
			}`)
			wantJSON(t, coerce(t, s, mustDecode(t, `{"value":null}`)), `{"value":null}`)
		})

		t.Run("walks array items", func(t *testing.T) {
			item := obj(schema.Properties{schema.Prop("a", schema.Str()), schema.Opt("b", schema.Int())})
			s := chordjson.ObjectOf("type", "array", "items", item)
			wantJSON(t, coerce(t, s, mustDecode(t, `[{"a":"x","b":null}]`)), `[{"a":"x"}]`)
		})
	})

	t.Run("pass 2: TypeBox's conversion rules on JSON Schema", func(t *testing.T) {
		t.Run("converts primitives by type", func(t *testing.T) {
			cases := []struct{ schema, input, want string }{
				{`{"type":"number"}`, `"42"`, `42`},
				{`{"type":"number"}`, `""`, `0`},
				{`{"type":"number"}`, `"true"`, `1`},
				{`{"type":"integer"}`, `"3.5"`, `3`},
				{`{"type":"integer"}`, `1.5`, `1`},
				{`{"type":"boolean"}`, `"1"`, `true`},
				{`{"type":"boolean"}`, `"FALSE"`, `false`},
				{`{"type":"null"}`, `"null"`, `null`},
				{`{"type":"string"}`, `42`, `"42"`},
			}
			for _, c := range cases {
				wantJSON(t, coerceValue(t, c.schema, c.input), c.want)
			}
		})

		t.Run("wraps a single value into an array and converts the items", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":"array","items":{"type":"number"}}`, `"1"`), `[1]`)
			wantJSON(t, coerceValue(t, `{"type":"array","items":{"type":"string"}}`, `null`), `["null"]`)
		})

		t.Run("converts tuple items in place and never wraps for a tuple", func(t *testing.T) {
			tuple := `{"type":"array","prefixItems":[{"type":"number"},{"type":"boolean"}],"items":false}`
			wantJSON(t, coerceValue(t, tuple, `["1","true"]`), `[1,true]`)
			wantJSON(t, coerceValue(t, tuple, `"1"`), `"1"`)
		})

		t.Run("converts const and enum values only when the result is a member", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"const":1}`, `"1"`), `1`)
			wantJSON(t, coerceValue(t, `{"const":1}`, `"2"`), `"2"`)
			wantJSON(t, coerceValue(t, `{"enum":[1,2,"hello"]}`, `"2"`), `2`)
			wantJSON(t, coerceValue(t, `{"enum":[1,2,"hello"]}`, `"world"`), `"world"`)
		})

		t.Run("keeps a value one union branch accepts, else takes the first branch whose conversion the union accepts", func(t *testing.T) {
			union := `{"anyOf":[{"type":"number"},{"type":"null"}]}`
			wantJSON(t, coerceValue(t, union, `null`), `null`)
			wantJSON(t, coerceValue(t, union, `"42"`), `42`)
			wantJSON(t, coerceValue(t, union, `"null"`), `null`)
			wantJSON(t, coerceValue(t, `{"oneOf":[{"type":"boolean"},{"type":"number"}]}`, `"1"`), `true`)
		})

		t.Run("converts allOf with each member in turn", func(t *testing.T) {
			s := chordjson.ObjectOf("allOf", []any{
				obj(schema.Properties{schema.Prop("a", schema.Num())}, schema.Open()),
				obj(schema.Properties{schema.Prop("b", schema.Bool())}, schema.Open()),
			})
			wantJSON(t, coerce(t, s, mustDecode(t, `{"a":"1","b":"true"}`)), `{"a":1,"b":true}`)
		})

		t.Run("follows $ref", func(t *testing.T) {
			s := mustDecode(t, `{
				"type": "object",
				"properties": { "item": { "$ref": "#/$defs/item" } },
				"$defs": { "item": { "type": "object", "properties": { "id": { "type": "integer" } } } }
			}`)
			wantJSON(t, coerce(t, s, mustDecode(t, `{"item":{"id":"7"}}`)), `{"item":{"id":7}}`)
		})

		t.Run("converts patternProperties and additionalProperties", func(t *testing.T) {
			s := mustDecode(t, `{
				"type": "object",
				"properties": { "a": { "type": "number" } },
				"patternProperties": { "^n_": { "type": "number" } },
				"additionalProperties": { "type": "boolean" }
			}`)
			wantJSON(t, coerce(t, s, mustDecode(t, `{"a":"1","n_x":"2","other":"true"}`)), `{"a":1,"n_x":2,"other":true}`)
		})
	})

	t.Run("pass 3: primitive coercion by type", func(t *testing.T) {
		t.Run("coerces like Ajv's coerceTypes", func(t *testing.T) {
			cases := []struct{ schema, input, want string }{
				{`{"type":"number"}`, `true`, `1`},
				{`{"type":"number"}`, `null`, `0`},
				{`{"type":"integer"}`, `"42"`, `42`},
				{`{"type":"boolean"}`, `"true"`, `true`},
				{`{"type":"boolean"}`, `0`, `false`},
				{`{"type":"string"}`, `true`, `"true"`},
				{`{"type":"null"}`, `""`, `null`},
				{`{"type":"null"}`, `0`, `null`},
				{`{"type":"null"}`, `false`, `null`},
			}
			for _, c := range cases {
				wantJSON(t, coerceValue(t, c.schema, c.input), c.want)
			}
		})

		t.Run("leaves a value that matches one of a type array's types, else takes the first coercible type", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":["number","string"]}`, `"1"`), `"1"`)
			wantJSON(t, coerceValue(t, `{"type":["boolean","number"]}`, `"1"`), `1`)
			wantJSON(t, coerceValue(t, `{"type":["integer","null"]}`, `"5"`), `5`)
		})

		t.Run("accepts null for nullable array schemas with items", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":["array","null"],"items":{"type":"string"}}`, `null`), `null`)
		})

		t.Run("coerces nullable unions when the value matches no branch", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"anyOf":[{"type":"number"},{"type":"null"}]}`, `"42"`), `42`)
			wantJSON(t, coerceValue(t, `{"oneOf":[{"type":"number"},{"type":"null"}]}`, `null`), `null`)
		})

		t.Run("leaves values no rule coerces", func(t *testing.T) {
			v := coercing(t, mustDecode(t, `{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}`), schema.CompileOptions{})
			for _, input := range []string{`"abc"`, `"4x"`, `{}`, `["1","2"]`} {
				coerced := coerceOf(t, v, mustDecode(t, `{"value":`+input+`}`))
				if checkOf(t, v, coerced) {
					t.Fatalf("%s was coerced to a valid value %s", input, marshalled(coerced))
				}
			}
		})
	})

	t.Run("where the passes conflict, the conversion rules win", func(t *testing.T) {
		t.Run("turns null into 'null' for a required string (pass 3 alone would give '')", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":"string"}`, `null`), `"null"`)
		})

		t.Run("turns '' into 0 for a number (pass 3 leaves an empty string)", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":"number"}`, `""`), `0`)
		})

		t.Run("turns '1' and '0' into booleans (pass 3 takes only 'true', 'false', 1 and 0)", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":"boolean"}`, `"0"`), `false`)
		})

		t.Run("truncates '42.1' for an integer (pass 3 rejects it)", func(t *testing.T) {
			wantJSON(t, coerceValue(t, `{"type":"integer"}`, `"42.1"`), `42`)
		})

		t.Run("drops an optional null before converting it (pass 2 alone would give 'null')", func(t *testing.T) {
			wantJSON(t, coerce(t, obj(schema.Properties{schema.Opt("name", schema.Str())}), mustDecode(t, `{"name":null}`)), `{}`)
		})
	})

	t.Run("recursive schemas", func(t *testing.T) {
		const tree = `{
			"type": "object",
			"properties": { "t": { "$ref": "#/$defs/Tree" } },
			"$defs": { "Tree": { "anyOf": [{ "type": "string" }, { "type": "array", "items": { "$ref": "#/$defs/Tree" } }] } }
		}`
		const jsonSchema = `{
			"type": "object",
			"properties": { "data": { "$ref": "#/$defs/Json" } },
			"required": ["data"],
			"$defs": {
				"Json": {
					"anyOf": [
						{ "type": "string" },
						{ "type": "number" },
						{ "type": "boolean" },
						{ "type": "array", "items": { "$ref": "#/$defs/Json" } },
						{ "type": "object", "additionalProperties": { "$ref": "#/$defs/Json" } }
					]
				}
			}
		}`

		t.Run("convert through a recursive union by its first accepting branch", func(t *testing.T) {
			v := coercing(t, mustDecode(t, tree), schema.CompileOptions{})
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"t":["a",["b"]]}`)), `{"t":["a",["b"]]}`)
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"t":5}`)), `{"t":"5"}`)
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"t":[5]}`)), `{"t":["5"]}`)
			wantParsed(t, parseOf(t, coercing(t, mustDecode(t, jsonSchema), schema.CompileOptions{}), mustDecode(t, `{"data":{"a":[1,null]}}`)), `{"data":{"a":[1,"null"]}}`)
		})

		t.Run("leave a value a recursive schema cannot convert as it is, for the check to report", func(t *testing.T) {
			v := coercing(t, mustDecode(t, tree), schema.CompileOptions{})
			if result := parseOf(t, v, mustDecode(t, `{"t":{}}`)); result.OK {
				t.Fatal("parse of an unconvertible value is ok")
			}
			wantJSON(t, coerceOf(t, v, mustDecode(t, `{"t":{"a":1}}`)), `{"t":{"a":1}}`)
		})

		t.Run("convert a scalar-or-array schema that refers to itself", func(t *testing.T) {
			s := mustDecode(t, `{
				"type": "object",
				"properties": {
					"value": { "anyOf": [{ "type": "number" }, { "type": "array", "items": { "$ref": "#/properties/value" } }] }
				},
				"required": ["value"]
			}`)
			v := coercing(t, s, schema.CompileOptions{})
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"value":"42"}`)), `{"value":42}`)
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"value":["1",["2"]]}`)), `{"value":[1,[2]]}`)
			if parseOf(t, v, mustDecode(t, `{"value":{}}`)).OK {
				t.Fatal("parse of {} is ok")
			}
		})

		t.Run("stop at a schema that cycles through shared JS objects instead of $ref", func(t *testing.T) {
			node := chordjson.NewObject(1)
			node.Set("anyOf", []any{chordjson.ObjectOf("type", "number"), chordjson.ObjectOf("type", "array", "items", node)})
			v := coercing(t, node, schema.CompileOptions{})
			wantParsed(t, parseOf(t, v, mustDecode(t, `"42"`)), `42`)
			wantParsed(t, parseOf(t, v, mustDecode(t, `["1",["2"]]`)), `[1,[2]]`)
			if parseOf(t, v, mustDecode(t, `{}`)).OK {
				t.Fatal("parse of {} is ok")
			}
		})

		t.Run("reset the re-entry state between calls and leave the input unchanged", func(t *testing.T) {
			v := coercing(t, mustDecode(t, `{"anyOf":[{"type":"number"},{"type":"array","items":{"$ref":"#"}}]}`), schema.CompileOptions{})
			for range 20 {
				if parseOf(t, v, mustDecode(t, `{}`)).OK {
					t.Fatal("parse of {} is ok")
				}
				wantParsed(t, parseOf(t, v, mustDecode(t, `"42"`)), `42`)
				input := mustDecode(t, `[1,"2",["3"]]`)
				wantParsed(t, parseOf(t, v, input), `[1,2,[3]]`)
				wantJSON(t, input, `[1,"2",["3"]]`)
			}
		})

		t.Run("convert and explain a deep value that every branch of a recursive union rejects, in linear time", func(t *testing.T) {
			// Each object branch converts and explains every child: without memoizing per call this is 2^depth work.
			v := coercing(t, mustDecode(t, `{
				"type": "object",
				"properties": { "s": { "$ref": "#/$defs/S" } },
				"$defs": {
					"S": {
						"anyOf": [
							{ "type": "object", "additionalProperties": { "$ref": "#/$defs/S" }, "required": ["a"] },
							{ "type": "object", "additionalProperties": { "$ref": "#/$defs/S" }, "required": ["b"] }
						]
					}
				}
			}`), schema.CompileOptions{})
			// The coercion converts a depth-40 value in linear time and the explanation of the failure reuses the errors of a `$ref` target per call (explain.ts walkRef), so both finish; Pi's test asserts both at depth 40.
			deep := func(depth int) any {
				value := mustDecode(t, `{"x":1}`)
				for range depth {
					value = chordjson.ObjectOf("c", value)
				}
				return chordjson.ObjectOf("s", value)
			}
			input := deep(40)
			if got := coerceOf(t, v, input); got != input {
				t.Fatalf("coerce of an unconvertible value returned a copy: %s", marshalled(got))
			}
			const explainedDepth = 40
			result := parseOf(t, v, deep(explainedDepth))
			var want []schema.ValidationError
			for i := range explainedDepth + 1 {
				want = append(want, schema.ValidationError{Path: "/s" + strings.Repeat("/c", i) + "/a", Keyword: "required", Message: "missing"})
			}
			want = append(want, schema.ValidationError{Path: "/s" + strings.Repeat("/c", explainedDepth) + "/x", Keyword: "type", Message: "expected object, got integer"})
			wantParseErrors(t, result, want)
		})

		t.Run("convert a TypeBox Cyclic union", func(t *testing.T) {
			// Type.Object({ t: Type.Cyclic({ Tree: Type.Union([Type.String(), Type.Array(Type.Ref("Tree"))]) }, "Tree") }) under TypeBox 1.3.27.
			v := coercing(t, mustDecode(t, `{"type":"object","required":["t"],"properties":{"t":{"$defs":{"Tree":{"anyOf":[{"type":"string"},{"type":"array","items":{"$ref":"Tree"}}],"$id":"Tree"}},"$ref":"Tree"}}}`), schema.CompileOptions{})
			wantParsed(t, parseOf(t, v, mustDecode(t, `{"t":[5]}`)), `{"t":["5"]}`)
			if parseOf(t, v, mustDecode(t, `{"t":{"a":1}}`)).OK {
				t.Fatal("parse of an object is ok")
			}
		})
	})

	t.Run("member keywords across the passes", func(t *testing.T) {
		t.Run("keep a pattern-matched property from additionalProperties in every pass", func(t *testing.T) {
			// Type.Record(Type.String(), Type.Number(), { additionalProperties: Type.String() }) under TypeBox 1.3.27.
			record := mustDecode(t, `{"type":"object","patternProperties":{"^.*$":{"type":"number"}},"additionalProperties":{"type":"string"}}`)
			wantParsed(t, parseOf(t, coercing(t, record, schema.CompileOptions{}), mustDecode(t, `{"n1":"1"}`)), `{"n1":1}`)
			plain := mustDecode(t, `{
				"type": "object",
				"patternProperties": { "^n_": { "type": ["integer", "null"] } },
				"additionalProperties": { "type": "string" }
			}`)
			wantJSON(t, coerce(t, plain, mustDecode(t, `{"n_a":"5","other":7}`)), `{"n_a":5,"other":"7"}`)
		})

		t.Run("drop optional nulls inside declared, pattern and additional property values alike", func(t *testing.T) {
			item := obj(schema.Properties{schema.Opt("n", schema.Int(schema.Minimum(1)))})
			schemas := []any{
				obj(schema.Properties{schema.Prop("entry", item)}),
				schema.Map(item),
				mustDecode(t, `{"type":"object","patternProperties":{"^entry$":`+marshalled(item)+`},"additionalProperties":false}`),
			}
			for _, s := range schemas {
				input := mustDecode(t, `{"entry":{"n":null}}`)
				wantParsed(t, parseOf(t, coercing(t, s, schema.CompileOptions{}), input), `{"entry":{}}`)
				wantJSON(t, input, `{"entry":{"n":null}}`)
			}
		})

		t.Run("keep a null under pattern and additional properties: their keys are not optional properties", func(t *testing.T) {
			s := mustDecode(t, `{"type":"object","additionalProperties":{"type":["integer","null"]}}`)
			wantParsed(t, parseOf(t, coercing(t, s, schema.CompileOptions{}), mustDecode(t, `{"n":null}`)), `{"n":null}`)
		})

		t.Run("drop optional nulls in tuple items as in array items", func(t *testing.T) {
			item := obj(schema.Properties{schema.Opt("n", schema.Int())})
			wantJSON(t, coerceOf(t, coercing(t, schema.Arr(item), schema.CompileOptions{}), mustDecode(t, `[{"n":null}]`)), `[{}]`)
			wantJSON(t, coerceOf(t, coercing(t, schema.Tuple([]schema.Schema{item}), schema.CompileOptions{}), mustDecode(t, `[{"n":null}]`)), `[{}]`)
		})

		t.Run("coerce tuple items by type in pass 3", func(t *testing.T) {
			s := mustDecode(t, `{"type":"array","prefixItems":[{"type":["integer","null"]},{"type":["boolean","null"]}]}`)
			wantJSON(t, coerce(t, s, mustDecode(t, `["5","true"]`)), `[5,true]`)
		})

		t.Run("convert the properties a union node declares besides its branches", func(t *testing.T) {
			s := mustDecode(t, `{
				"type": "object",
				"properties": { "a": { "type": "integer" }, "b": { "type": "integer" } },
				"anyOf": [{ "required": ["a"] }, { "required": ["b"] }]
			}`)
			wantParsed(t, parseOf(t, coercing(t, s, schema.CompileOptions{}), mustDecode(t, `{"a":"3.5"}`)), `{"a":3}`)
		})

		for _, keyword := range []string{"anyOf", "oneOf"} {
			t.Run("check "+keyword+" candidates after the node's own properties convert", func(t *testing.T) {
				s := mustDecode(t, `{
					"type": "object",
					"`+keyword+`": [
						{ "type": "object", "properties": { "a": { "type": "array", "items": { "type": "integer" } } }, "required": ["a"] },
						{ "type": "object", "properties": { "b": { "type": "boolean" } }, "required": ["b"] }
					],
					"properties": { "c": { "type": "integer" } },
					"required": ["c"]
				}`)
				input := mustDecode(t, `{"a":"3","c":"2"}`)
				wantParsed(t, parseOf(t, coercing(t, s, schema.CompileOptions{}), input), `{"a":[3],"c":2}`)
				wantJSON(t, input, `{"a":"3","c":"2"}`)
			})
		}
	})
}

// TestCoerceUnresolvedReferences ports the case of validator.test.ts's "unresolved references" that coerces (Pi b556cfe5, test/validator.test.ts "leave values at their place uncoerced").
func TestCoerceUnresolvedReferences(t *testing.T) {
	t.Run("unresolved references", func(t *testing.T) {
		t.Run("leave values at their place uncoerced", func(t *testing.T) {
			dangling := mustDecode(t, `{"type":"object","properties":{"t":{"$ref":"#/definitions/Missing"}}}`)
			v := coercing(t, dangling, schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject})
			input := mustDecode(t, `{"t":"1"}`)
			wantJSON(t, coerceOf(t, v, input), `{"t":"1"}`)
			wantParseErrors(t, parseOf(t, v, input), []schema.ValidationError{{Path: "/t", Keyword: "$ref", Message: "unresolved reference #/definitions/Missing"}})
		})
	})
}
