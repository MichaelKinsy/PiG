package schema_test

// Ports the packages/schema tests Pi main adds after v1.5.0: 2bd2c2d0a (relative root $id, #11726), 20c637b1a (leaf checks inlined by object and array
// loops), af79b0c3d (validate, recursive, equal; bigint and undefined have no Go value and are designed out, see schema/README.md) and
// 55a5c6914 (num rejects NaN and Infinity).

import (
	"math"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// validator.test.ts "schemas": "applies a relative root $id once when resolving local references" (#11697).
func TestPiMainRelativeRootIDAppliedOnce(t *testing.T) {
	root := mustDecode(t, `{
		"$id": "schemas/tool.json",
		"type": "object",
		"properties": { "count": { "$ref": "#/$defs/count" } },
		"required": ["count"],
		"$defs": { "count": { "type": "integer" } }
	}`)
	if !mustCompile(t, root).Check(mustDecode(t, `{"count":2}`)) {
		t.Fatal("a valid value is rejected")
	}
	reject := mustCompile(t, root, schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject})
	if !reject.Check(mustDecode(t, `{"count":2}`)) {
		t.Fatal("a valid value is rejected with unresolvedRefs reject")
	}
	wantErrors(t, reject.Errors(mustDecode(t, `{"count":"a"}`)), []schema.ValidationError{
		{Path: "/count", Keyword: "type", Message: "expected integer, got string"},
	})
	parsed := parseOf(t, coercing(t, root), mustDecode(t, `{"count":"2"}`))
	if !parsed.OK || !reflect.DeepEqual(parsed.Value, mustDecode(t, `{"count":2}`)) {
		t.Fatalf("parse = %#v", parsed)
	}
}

// validator.test.ts "applies a relative root $id once in schemas with unevaluated keywords".
func TestPiMainRelativeRootIDWithUnevaluatedKeywords(t *testing.T) {
	props := mustDecode(t, `{
		"$id": "schemas/tool.json",
		"type": "object",
		"properties": { "count": { "$ref": "#/$defs/count" } },
		"$defs": { "count": { "type": "integer" } },
		"unevaluatedProperties": false
	}`)
	v := mustCompile(t, props, schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject})
	if !v.Check(mustDecode(t, `{"count":2}`)) {
		t.Fatal("a valid object is rejected")
	}
	wantErrors(t, v.Errors(mustDecode(t, `{"count":2,"extra":1}`)), []schema.ValidationError{
		{Path: "/extra", Keyword: "unevaluatedProperties", Message: "unknown field"},
	})
	items := mustDecode(t, `{
		"$id": "schemas/list.json",
		"type": "array",
		"prefixItems": [{ "$ref": "#/$defs/count" }],
		"$defs": { "count": { "type": "integer" } },
		"unevaluatedItems": false
	}`)
	if !mustCompile(t, items, schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject}).Check(mustDecode(t, `[2]`)) {
		t.Fatal("a valid array is rejected")
	}
	if mustCompile(t, items).Check(mustDecode(t, `[2,3]`)) {
		t.Fatal("an unevaluated item is accepted")
	}
}

// validator.test.ts "explains errors under nested resources with a relative $id".
func TestPiMainExplainsNestedResourcesWithARelativeID(t *testing.T) {
	cloned := mustDecode(t, `{
		"$id": "https://example.com/root.json",
		"allOf": [{ "$ref": "#/$defs/child" }],
		"$defs": {
			"child": {
				"$id": "schemas/tool.json",
				"$ref": "#/$defs/props",
				"unevaluatedProperties": false,
				"$defs": { "props": { "properties": { "count": { "type": "integer" } } } }
			}
		}
	}`)
	wantErrors(t, mustCompile(t, cloned).Errors(mustDecode(t, `{"count":2,"extra":1}`)), []schema.ValidationError{
		{Path: "/extra", Keyword: "unevaluatedProperties", Message: "unknown field"},
	})
	branch := mustDecode(t, `{
		"$id": "https://example.com/root.json",
		"anyOf": [{ "$id": "a/b.json", "$ref": "#/$defs/n", "$defs": { "n": { "type": "integer" } } }, { "type": "boolean" }]
	}`)
	wantErrors(t, mustCompile(t, branch).Errors(mustDecode(t, `{}`)), []schema.ValidationError{
		{Path: "", Keyword: "type", Message: "expected integer, got object"},
	})
}

// coerce.test.ts "follows a relative $ref from the root $id on the first call of a schema with $dynamicRef".
func TestPiMainCoerceFollowsARelativeRefOnTheFirstCallWithDynamicRef(t *testing.T) {
	root := mustDecode(t, `{
		"$id": "https://example.com/root.json",
		"$dynamicAnchor": "node",
		"type": "object",
		"properties": { "count": { "$ref": "defs/n.json" }, "children": { "type": "array", "items": { "$dynamicRef": "#node" } } }
	}`)
	options := schema.CompileOptions{Schemas: map[string]schema.JsonSchema{"https://example.com/defs/n.json": mustDecode(t, `{"type":"integer"}`)}}
	want := mustDecode(t, `{"count":2}`)
	if got := coerceOf(t, coercing(t, root, options), mustDecode(t, `{"count":"2"}`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("coerce = %#v", got)
	}
	validator := coercing(t, root, options)
	for range 2 {
		if got := parseOf(t, validator, mustDecode(t, `{"count":"2"}`)); !got.OK || !reflect.DeepEqual(got.Value, want) {
			t.Fatalf("parse = %#v", got)
		}
	}
}

// validator.test.ts "leaf checks inlined by object and array loops": "agree with the standalone check as a property, a pattern property and an item".
func TestPiMainLeafChecksAgreeWithTheStandaloneCheck(t *testing.T) {
	cases := []struct{ schema, values string }{
		{`{"const":"a"}`, `["a","b",1,null]`},
		{`{"const":null}`, `[null,0,false,"null"]`},
		{`{"enum":["a",1,null]}`, `["a",1,null,"b",2,true]`},
		{`{"type":["string","null"]}`, `["a",null,1,{},[]]`},
		{`{"type":["integer","boolean"]}`, `[1,1.5,true,"1"]`},
		{`{"type":["number","array"]}`, `[1.5,2,[],{}]`},
		{`{"type":["object","bytes"]}`, `[{},[],null]`},
		{`{"anyOf":[{"type":"string"},{"type":"null"}]}`, `["a",null,1]`},
		{`{"anyOf":[{"type":"integer"},{"type":"string","minLength":2}]}`, `[1,1.5,"ab","a"]`},
		{`{"type":"integer","minimum":1,"exclusiveMaximum":5}`, `[1,4,5,0,2.5,"2"]`},
		{`{"type":"number","exclusiveMinimum":0,"maximum":1}`, `[0.5,1,0,1.5,"0.5"]`},
		{`{"type":"integer","multipleOf":2}`, `[2,3,2.5]`},
		{`{"type":"string","minLength":2,"maxLength":3}`, `["ab","abc","a","abcd","😀😀",12]`},
		{`{"type":"string","pattern":"^a+$"}`, `["aa","ab",1]`},
		{`{"minimum":1}`, `[1,0,"x",null]`},
	}
	for _, c := range cases {
		alone := mustCompile(t, mustDecode(t, c.schema))
		closed := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"p":`+c.schema+`},"additionalProperties":false}`))
		open := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"p":`+c.schema+`},"required":["q"]}`))
		items := mustCompile(t, mustDecode(t, `{"type":"array","items":`+c.schema+`}`))
		values := mustDecode(t, c.values).([]any)
		values = append(values, []byte{0})
		for _, v := range values {
			expected := alone.Check(v)
			got := [3]bool{
				closed.Check(chordjson.ObjectOf("p", v)),
				open.Check(chordjson.ObjectOf("p", v, "q", 1.0)),
				items.Check([]any{v}),
			}
			if got != [3]bool{expected, expected, expected} {
				t.Errorf("schema %s, value %#v: standalone %v, as property, open property and item %v", c.schema, v, expected, got)
			}
		}
	}
}

// validator.test.ts "leaf checks inlined by object and array loops": "find properties in any order".
func TestPiMainClosedObjectFindsPropertiesInAnyOrder(t *testing.T) {
	v := mustCompile(t, mustDecode(t, `{
		"type": "object",
		"properties": { "a": { "const": 1 }, "b": { "type": "string" }, "c": { "type": "integer", "minimum": 0 } },
		"required": ["a", "c"],
		"additionalProperties": false
	}`))
	for _, c := range []struct {
		value string
		want  bool
	}{
		{`{"a":1,"b":"x","c":0}`, true},
		{`{"c":0,"b":"x","a":1}`, true},
		{`{"b":"x","c":0,"a":1}`, true},
		{`{"a":1,"c":0,"a2":1}`, false},
		{`{"c":-1,"a":1}`, false},
		{`{"c":0,"b":1,"a":1}`, false},
		{`{"c":0,"b":"x"}`, false},
	} {
		if got := v.Check(mustDecode(t, c.value)); got != c.want {
			t.Errorf("Check(%s) = %v, want %v", c.value, got, c.want)
		}
	}
}

// coerce.test.ts "validate": "checks without coercing, with or without the coerce option".
func TestPiMainValidateChecksWithoutCoercing(t *testing.T) {
	root := schema.Obj(schema.Properties{schema.Prop("count", schema.Num()), schema.Prop("tags", schema.Arr(schema.Str()))})
	plain, err := schema.Compile(root, schema.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, validate := range map[string]func(any) (schema.ParseResult, error){"plain": plain.Validate, "coercing": coercing(t, root).Validate} {
		value := mustDecode(t, `{"count":42,"tags":["a"]}`)
		result, err := validate(value)
		if err != nil || !result.OK || result.Value != value || len(result.Errors) != 0 {
			t.Fatalf("%s: validate of a valid value = %#v, %v", name, result, err)
		}
		// Parse would turn "42" into 42 and "a" into ["a"]; Validate reports both.
		result, err = validate(mustDecode(t, `{"count":"42","tags":"a"}`))
		if err != nil || result.OK {
			t.Fatalf("%s: validate of an invalid value = %#v, %v", name, result, err)
		}
		wantErrors(t, result.Errors, []schema.ValidationError{
			{Path: "/count", Keyword: "type", Message: "expected number, got string"},
			{Path: "/tags", Keyword: "type", Message: "expected array, got string"},
		})
	}
}

// builders.test.ts "build a recursive schema that checks every level".
func TestPiMainRecursiveBuildsASchemaThatChecksEveryLevel(t *testing.T) {
	tree := schema.Recursive("Tree", func(self schema.Schema) schema.Schema {
		return schema.Obj(schema.Properties{schema.Prop("name", schema.Str()), schema.Prop("children", schema.Arr(self))})
	})
	sameJSON(t, tree, `{
		"type": "object",
		"properties": { "name": { "type": "string" }, "children": { "type": "array", "items": { "$ref": "Tree" } } },
		"required": ["name", "children"],
		"additionalProperties": false,
		"$id": "Tree"
	}`)
	v := mustCompile(t, tree)
	if !v.Check(mustDecode(t, `{"name":"a","children":[{"name":"b","children":[]}]}`)) {
		t.Fatal("a valid tree is rejected")
	}
	wantErrors(t, v.Errors(mustDecode(t, `{"name":"a","children":[{"name":"b","children":[{"name":1,"children":[]}]}]}`)), []schema.ValidationError{
		{Path: "/children/0/children/0/name", Keyword: "type", Message: "expected string, got integer"},
	})
}

// builders.test.ts "nest recursive schemas with distinct ids inside other schemas".
func TestPiMainRecursiveSchemasNestWithDistinctIDs(t *testing.T) {
	tree := schema.Recursive("Tree", func(self schema.Schema) schema.Schema {
		return schema.Obj(schema.Properties{schema.Prop("name", schema.Str()), schema.Prop("children", schema.Arr(self))})
	})
	list := schema.Recursive("List", func(self schema.Schema) schema.Schema {
		return schema.Obj(schema.Properties{schema.Prop("value", schema.Num()), schema.Prop("next", schema.Nullable(self))})
	})
	both := schema.Obj(schema.Properties{schema.Prop("tree", tree), schema.Opt("list", list)})
	v := mustCompile(t, both)
	root := `{"name":"a","children":[{"name":"b","children":[]}]}`
	for _, c := range []struct {
		value string
		want  bool
	}{
		{`{"tree":` + root + `,"list":{"value":1,"next":{"value":2,"next":null}}}`, true},
		{`{"tree":` + root + `,"list":{"value":1,"next":{"value":"2","next":null}}}`, false},
		{`{"tree":{"name":"a","children":[{"name":"b","children":[{"name":3}]}]}}`, false},
	} {
		if got := v.Check(mustDecode(t, c.value)); got != c.want {
			t.Errorf("Check(%s) = %v, want %v", c.value, got, c.want)
		}
	}
}

// equal.test.ts "compares JSON values structurally, as TypeBox's Value.Equal does". The cases with undefined have no Go value.
func TestPiMainEqual(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`1`, `1`, true},
		{`"a"`, `"b"`, false},
		{`null`, `null`, true},
		{`{"a":1,"b":[1,{"c":2}]}`, `{"b":[1,{"c":2}],"a":1}`, true},
		{`{"a":1}`, `{"a":1,"b":2}`, false},
		{`[1,2]`, `[2,1]`, false},
		{`[1]`, `{"0":1}`, false},
		{`{}`, `[]`, false},
	} {
		if got := schema.Equal(mustDecode(t, c.a), mustDecode(t, c.b)); got != c.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// validator.test.ts "values": "rejects NaN and Infinity as a number or an integer, with or without bounds, as TypeBox does".
func TestPiMainRejectsNaNAndInfinityAsANumber(t *testing.T) {
	for _, s := range []string{
		`{"type":"number"}`,
		`{"type":"integer"}`,
		`{"type":"number","minimum":0}`,
		`{"type":["number","string"]}`,
		`{"anyOf":[{"type":"number"},{"type":"null"}]}`,
		`{"type":"object","properties":{"a":{"type":"number"}}}`,
	} {
		v := mustCompile(t, mustDecode(t, s))
		for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			var value any = n
			if s == `{"type":"object","properties":{"a":{"type":"number"}}}` {
				value = chordjson.ObjectOf("a", n)
			}
			if v.Check(value) {
				t.Errorf("%s accepts %v", s, n)
			}
		}
	}
	wantErrors(t, mustCompile(t, mustDecode(t, `{"type":"number"}`)).Errors(math.NaN()), []schema.ValidationError{
		{Path: "", Keyword: "type", Message: "expected number, got NaN"},
	})
}
