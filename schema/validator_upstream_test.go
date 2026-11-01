package schema_test

// Ports packages/schema/test/validator.test.ts: Compile and Validator: checks, errors, values, options and schemas from other sources.
// Not ported as Go cases: "treats a property set to undefined as absent" (a Go value has no undefined; an absent member is the case, covered below) and the TypeBox library test (see TestCompilesTypeBoxEmittedSchemas).

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

const readSchema = `{
	"type": "object",
	"properties": { "path": { "type": "string" }, "offset": { "type": "integer", "minimum": 1 } },
	"required": ["path"],
	"additionalProperties": false
}`

const blockSchema = `{
	"anyOf": [
		{
			"type": "object",
			"properties": { "kind": { "const": "text" }, "text": { "type": "string" } },
			"required": ["kind", "text"],
			"additionalProperties": false
		},
		{
			"type": "object",
			"properties": { "kind": { "const": "image" }, "width": { "type": "integer", "minimum": 0 } },
			"required": ["kind", "width"],
			"additionalProperties": false
		}
	]
}`

func mustCompile(t *testing.T, s any, options ...schema.CompileOptions) checked {
	t.Helper()
	var opts schema.CompileOptions
	if len(options) > 0 {
		opts = options[0]
	}
	v, err := schema.Compile(s, opts)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return checked{v, t}
}

func wantErrors(t *testing.T, got, want []schema.ValidationError) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %#v, want %#v", got, want)
	}
}

func TestErrors(t *testing.T) {
	t.Run("reports a missing field and an unknown field at the field", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, readSchema))
		wantErrors(t, v.Errors(mustDecode(t, `{"offset":0,"extra":true}`)), []schema.ValidationError{
			{Path: "/path", Keyword: "required", Message: "missing"},
			{Path: "/offset", Keyword: "minimum", Message: "expected >= 1"},
			{Path: "/extra", Keyword: "additionalProperties", Message: "unknown field"},
		})
	})

	t.Run("is empty for a valid value", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, readSchema))
		wantErrors(t, v.Errors(mustDecode(t, `{"path":"a.ts"}`)), nil)
	})

	t.Run("reports the branch a tagged union's tag selects", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"array","items":`+blockSchema+`}`))
		wantErrors(t, v.Errors(mustDecode(t, `[{"kind":"text","text":"a"},{"kind":"image","width":-1}]`)), []schema.ValidationError{
			{Path: "/1/width", Keyword: "minimum", Message: "expected >= 0"},
		})
		wantErrors(t, v.Errors(mustDecode(t, `[{"kind":"video"}]`)), []schema.ValidationError{
			{Path: "/0/kind", Keyword: "anyOf", Message: `expected one of ["text","image"]`},
		})
	})

	t.Run("escapes ~ and / in paths as JSON Pointer does", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"a/b~c":{"type":"string"}}}`))
		wantErrors(t, v.Errors(mustDecode(t, `{"a/b~c":1}`)), []schema.ValidationError{
			{Path: "/a~1b~0c", Keyword: "type", Message: "expected string, got integer"},
		})
	})

	t.Run("follows $ref into the referenced schema", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"$defs": { "point": { "type": "object", "properties": { "x": { "type": "number" } }, "required": ["x"] } },
			"type": "array",
			"items": { "$ref": "#/$defs/point" }
		}`))
		wantErrors(t, v.Errors(mustDecode(t, `[{"x":1},{}]`)), []schema.ValidationError{
			{Path: "/1/x", Keyword: "required", Message: "missing"},
		})
	})

	t.Run("orders the errors of a Go map by own-key order", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"object","additionalProperties":false}`))
		wantErrors(t, v.Errors(map[string]any{"b": 1.0, "a": 1.0, "10": 1.0, "2": 1.0}), []schema.ValidationError{
			{Path: "/2", Keyword: "additionalProperties", Message: "unknown field"},
			{Path: "/10", Keyword: "additionalProperties", Message: "unknown field"},
			{Path: "/a", Keyword: "additionalProperties", Message: "unknown field"},
			{Path: "/b", Keyword: "additionalProperties", Message: "unknown field"},
		})
	})
}

func TestValues(t *testing.T) {
	t.Run("treats a missing property as absent", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, readSchema))
		if !v.Check(mustDecode(t, `{"path":"a.ts"}`)) {
			t.Fatal("a value without the optional offset is valid")
		}
		if v.Check(mustDecode(t, `{"offset":1}`)) {
			t.Fatal("a value without the required path is invalid")
		}
	})

	t.Run("checks bytes as []byte with length bounds", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"data":{"type":"bytes","maxLength":4}}}`))
		object := func(data any) *schema.Object { return chordjson.ObjectOf("data", data) }
		if !v.Check(object(make([]byte, 4))) {
			t.Fatal("4 bytes are valid")
		}
		if v.Check(object(make([]byte, 5))) {
			t.Fatal("5 bytes are invalid")
		}
		if v.Check(object([]any{1.0, 2.0})) {
			t.Fatal("an array is not bytes")
		}
		wantErrors(t, v.Errors(object(make([]byte, 5))), []schema.ValidationError{
			{Path: "/data", Keyword: "maxLength", Message: "expected at most 4 bytes"},
		})
	})

	t.Run("does not treat bytes as an object", func(t *testing.T) {
		if mustCompile(t, mustDecode(t, `{"type":"object"}`)).Check([]byte{0}) {
			t.Fatal("bytes are not an object")
		}
	})

	t.Run("checks properties named like Object.prototype members as own properties", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"type": "object",
			"properties": { "constructor": { "type": "string" }, "toString": { "type": "number" } },
			"required": ["constructor"],
			"additionalProperties": false
		}`))
		if v.Check(mustDecode(t, `{}`)) {
			t.Fatal("constructor is required")
		}
		if !v.Check(mustDecode(t, `{"constructor":"a"}`)) {
			t.Fatal("constructor present is valid")
		}
		if v.Check(mustDecode(t, `{"constructor":"a","toString":"b"}`)) {
			t.Fatal("toString must be a number")
		}
	})

	t.Run("does not accept every value for a type named like an Object.prototype member", func(t *testing.T) {
		if mustCompile(t, mustDecode(t, `{"type":"constructor"}`)).Check(1.0) {
			t.Fatal("an unknown type name accepts nothing")
		}
	})

	t.Run("checks a detached check function", func(t *testing.T) {
		check := mustCompile(t, mustDecode(t, readSchema)).Check
		var kept []any
		for _, value := range []any{mustDecode(t, `{"path":"a"}`), mustDecode(t, `{"path":1}`)} {
			if check(value) {
				kept = append(kept, value)
			}
		}
		if len(kept) != 1 || !reflect.DeepEqual(kept[0], mustDecode(t, `{"path":"a"}`)) {
			t.Fatalf("kept = %v", kept)
		}
	})

	t.Run("reads Go number kinds as numbers", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"integer","minimum":1}`))
		for _, value := range []any{1, int64(2), uint8(3), float32(4), 5.0} {
			if !v.Check(value) {
				t.Fatalf("%T(%v) is a valid integer", value, value)
			}
		}
		if v.Check(float32(1.5)) {
			t.Fatal("1.5 is not an integer")
		}
	})
}

func TestOptions(t *testing.T) {
	t.Run("treats format as an annotation unless formats are given", func(t *testing.T) {
		s := mustDecode(t, `{"type":"string","format":"email"}`)
		if !mustCompile(t, s).Check("not an email") {
			t.Fatal("format is an annotation without formats")
		}
		with := schema.CompileOptions{Formats: formats.Formats()}
		if mustCompile(t, s, with).Check("not an email") {
			t.Fatal("not an email passed with formats")
		}
		if !mustCompile(t, s, with).Check("a@example.com") {
			t.Fatal("a@example.com failed with formats")
		}
	})

	t.Run("resolves $ref to registered documents", func(t *testing.T) {
		point := mustDecode(t, `{"type":"object","properties":{"x":{"type":"number"}},"required":["x"]}`)
		v := mustCompile(t, mustDecode(t, `{"$ref":"https://example.com/point.json"}`),
			schema.CompileOptions{Schemas: map[string]schema.JsonSchema{"https://example.com/point.json": point}})
		if !v.Check(mustDecode(t, `{"x":1}`)) {
			t.Fatal("{x:1} is a point")
		}
		if v.Check(mustDecode(t, `{}`)) {
			t.Fatal("{} is not a point")
		}
	})

	t.Run("throws for a $ref that names nothing", func(t *testing.T) {
		unresolved := regexp.MustCompile(`Unresolved \$ref`)
		for _, text := range []string{`{"$ref":"https://example.com/missing.json"}`, `{"$ref":"#/$defs/missing"}`} {
			_, err := schema.Compile(mustDecode(t, text), schema.CompileOptions{})
			if err == nil || !unresolved.MatchString(err.Error()) {
				t.Fatalf("Compile(%s) error = %v, want Unresolved $ref", text, err)
			}
		}
	})

	t.Run("applies draft 7 semantics: $ref overrides its siblings", func(t *testing.T) {
		s := mustDecode(t, `{"definitions":{"s":{"type":"string"}},"$ref":"#/definitions/s","maxLength":1}`)
		if !mustCompile(t, s, schema.CompileOptions{Draft: schema.Draft7}).Check("ab") {
			t.Fatal("draft 7 ignores maxLength next to $ref")
		}
		if mustCompile(t, s).Check("ab") {
			t.Fatal("draft 2020-12 applies maxLength next to $ref")
		}
	})
}

func TestSchemas(t *testing.T) {
	// TypeBox's runtime is a TypeScript library with no Go form; this is the JSON that Type.Object({ name: Type.String(), tags: Type.Array(Type.String()) }) emits.
	t.Run("TestCompilesTypeBoxEmittedSchemas", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"name":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["name","tags"]}`))
		if !v.Check(mustDecode(t, `{"name":"a","tags":[]}`)) {
			t.Fatal("valid value rejected")
		}
		wantErrors(t, v.Errors(mustDecode(t, `{"name":"a","tags":[1]}`)), []schema.ValidationError{
			{Path: "/tags/0", Keyword: "type", Message: "expected string, got integer"},
		})
	})

	t.Run("never modifies a schema", func(t *testing.T) {
		block := mustDecode(t, blockSchema).(*schema.Object)
		block.Set("$defs", mustDecode(t, `{"s":{"type":"string","pattern":"^a"}}`))
		var s any = block
		before, err := chordjson.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		v := mustCompile(t, s)
		if !v.Check(mustDecode(t, `{"kind":"text","text":"x"}`)) {
			t.Fatal("valid value rejected")
		}
		if got := v.Errors(mustDecode(t, `{"kind":"image","width":-1}`)); len(got) != 1 {
			t.Fatalf("errors = %v, want 1", got)
		}
		after, err := chordjson.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("schema changed:\n%s\n%s", before, after)
		}
	})

	t.Run("compiles a recursive schema", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"type": "object",
			"properties": { "value": { "type": "number" }, "children": { "type": "array", "items": { "$ref": "#" } } },
			"required": ["value"]
		}`))
		if !v.Check(mustDecode(t, `{"value":1,"children":[{"value":2,"children":[{"value":3}]}]}`)) {
			t.Fatal("valid tree rejected")
		}
		wantErrors(t, v.Errors(mustDecode(t, `{"value":1,"children":[{"value":2,"children":[{}]}]}`)), []schema.ValidationError{
			{Path: "/children/0/children/0/value", Keyword: "required", Message: "missing"},
		})
	})

	t.Run("throws for a malformed schema", func(t *testing.T) {
		_, err := schema.Compile(mustDecode(t, `{"properties":{"a":1}}`), schema.CompileOptions{})
		if err == nil || !regexp.MustCompile(`must be an object or a boolean`).MatchString(err.Error()) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("accepts a Go map schema", func(t *testing.T) {
		v := mustCompile(t, map[string]any{"type": "object", "required": []any{"a"}})
		if v.Check(map[string]any{}) || !v.Check(map[string]any{"a": nil}) {
			t.Fatal("required on a Go map schema and value")
		}
	})

	t.Run("serves concurrent checks of a schema with a dynamic reference", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"$id": "https://example.com/root",
			"$ref": "list",
			"$defs": {
				"foo": { "$dynamicAnchor": "items", "type": "string" },
				"list": {
					"$id": "list",
					"type": "array",
					"items": { "$dynamicRef": "#items" },
					"$defs": { "items": { "$dynamicAnchor": "items" } }
				}
			}
		}`))
		done := make(chan bool, 8)
		for range 8 {
			go func() {
				ok := true
				for range 200 {
					ok = ok && v.Check(mustDecode(t, `["a","b"]`)) && !v.Check(mustDecode(t, `["a",1]`))
				}
				done <- ok
			}()
		}
		for range 8 {
			if !<-done {
				t.Fatal("concurrent checks disagree")
			}
		}
	})

	t.Run("serves concurrent explanations", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"type": "object",
			"properties": { "a": { "type": "integer" } },
			"patternProperties": { "^x-": { "type": "string" } },
			"unevaluatedProperties": false
		}`))
		want := v.Errors(mustDecode(t, `{"a":1,"x-b":"s","c":true}`))
		if len(want) != 1 || want[0].Path != "/c" || want[0].Keyword != "unevaluatedProperties" {
			t.Fatalf("errors = %v", want)
		}
		done := make(chan bool, 8)
		for range 8 {
			go func() {
				ok := true
				for range 200 {
					ok = ok && reflect.DeepEqual(v.Errors(mustDecode(t, `{"a":1,"x-b":"s","c":true}`)), want)
				}
				done <- ok
			}()
		}
		for range 8 {
			if !<-done {
				t.Fatal("concurrent explanations disagree")
			}
		}
	})
}
