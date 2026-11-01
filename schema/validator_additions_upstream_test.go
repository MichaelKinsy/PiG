package schema_test

// Ports the additions of Pi 6536f801 to packages/schema/test/validator.test.ts (b556cfe5:packages/schema/test/validator.test.ts:192-323) except the coercion case "leave values at their place uncoerced", which is in the coercion tests (TestCoerceUnresolvedReferences).
// Not ported as a Go case: "reject a TypeBox Ref without its definition" runs a TypeBox library call; the JSON Type.Ref("A") emits, {"$ref":"A"}, is the case below.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

func TestUnresolvedReferences(t *testing.T) {
	const dangling = `{"type":"object","properties":{"t":{"$ref":"#/definitions/Missing"}}}`
	const external = `{"type":"object","properties":{"t":{"$ref":"https://example.com/schemas/t.json"}}}`

	t.Run("throw when the schema compiles, by default", func(t *testing.T) {
		for text, want := range map[string]string{dangling: "Unresolved $ref #/definitions/Missing", external: "Unresolved $ref https://example.com/schemas/t.json"} {
			_, err := schema.Compile(mustDecode(t, text), schema.CompileOptions{})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Compile(%s) error = %v, want %q", text, err, want)
			}
		}
	})

	t.Run("reject only values at their place with UnresolvedRefsReject", func(t *testing.T) {
		reject := schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject}
		for _, text := range []string{dangling, external} {
			v := mustCompile(t, mustDecode(t, text), reject)
			if !v.Check(mustDecode(t, `{}`)) {
				t.Fatalf("%s: {} must pass", text)
			}
			if v.Check(mustDecode(t, `{"t":1}`)) {
				t.Fatalf("%s: {t:1} must fail", text)
			}
		}
		v := mustCompile(t, mustDecode(t, dangling), reject)
		wantErrors(t, v.Errors(mustDecode(t, `{"t":1}`)), []schema.ValidationError{
			{Path: "/t", Keyword: "$ref", Message: "unresolved reference #/definitions/Missing"},
		})
	})

	t.Run("reject a TypeBox Ref without its definition", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"a":{"$ref":"A"}},"required":["a"]}`), schema.CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject})
		wantErrors(t, v.Errors(mustDecode(t, `{"a":{"x":"1"}}`)), []schema.ValidationError{
			{Path: "/a", Keyword: "$ref", Message: "unresolved reference A"},
		})
	})
}

func TestStringLengths(t *testing.T) {
	t.Run("count an unpaired high surrogate as one code point and a pair as one", func(t *testing.T) {
		// "\ud800A", "\ud800" and "\udc00\ud800" hold lone surrogates, which Go strings keep as WTF-8.
		const high, low = "\xed\xa0\x80", "\xed\xb0\x80"
		cases := []struct {
			schema string
			value  string
			want   bool
		}{
			{`{"type":"string","minLength":2}`, high + "A", true},
			{`{"type":"string","maxLength":1}`, high + "A", false},
			{`{"type":"string","maxLength":1}`, high, true},
			{`{"type":"string","maxLength":1}`, "😀", true},
			{`{"type":"string","minLength":2}`, "😀", false},
			{`{"type":"string","minLength":2}`, low + high, true},
		}
		for _, c := range cases {
			if got := mustCompile(t, mustDecode(t, c.schema)).Check(c.value); got != c.want {
				t.Errorf("%s check %q = %v, want %v", c.schema, c.value, got, c.want)
			}
		}
	})
}

func TestErrorMessages(t *testing.T) {
	t.Run("list at most 20 enum values", func(t *testing.T) {
		values := make([]any, 5000)
		names := make([]string, 5000)
		for i := range values {
			names[i] = "v" + strconv.Itoa(i)
			values[i] = names[i]
		}
		v := mustCompile(t, map[string]any{"type": "object", "properties": map[string]any{"e": map[string]any{"type": "string", "enum": values}}})
		if !v.Check(map[string]any{"e": "v4999"}) {
			t.Fatal("v4999 is a member")
		}
		errs := v.Errors(map[string]any{"e": "v5000"})
		listed, err := json.Marshal(names[:20])
		if err != nil {
			t.Fatal(err)
		}
		if len(errs) != 1 || errs[0].Message != "expected one of "+string(listed[:len(listed)-1])+", ... and 4980 more]" {
			t.Fatalf("errors = %#v", errs)
		}
		wantErrors(t, mustCompile(t, mustDecode(t, `{"enum":["a","b"]}`)).Errors("c"), []schema.ValidationError{
			{Path: "", Keyword: "enum", Message: `expected one of ["a","b"]`},
		})
	})

	t.Run("accept a 10,000-value enum", func(t *testing.T) {
		values := make([]any, 10000)
		for i := range values {
			values[i] = "member" + strconv.Itoa(i)
		}
		if !mustCompile(t, map[string]any{"enum": values}).Check("member9999") {
			t.Fatal("member9999 is a member")
		}
	})

	t.Run("describe contains bounds in words", func(t *testing.T) {
		at := func(text string) string {
			t.Helper()
			errs := mustCompile(t, mustDecode(t, text)).Errors([]any{"a"})
			if len(errs) == 0 {
				t.Fatalf("%s: no error", text)
			}
			return errs[0].Message
		}
		for text, want := range map[string]string{
			`{"type":"array","contains":{"type":"number"}}`:                                 "expected at least 1 matching item",
			`{"type":"array","contains":{"type":"number"},"minContains":2}`:                 "expected at least 2 matching items",
			`{"type":"array","contains":{"type":"string"},"maxContains":0,"minContains":0}`: "expected at most 0 matching items",
			`{"type":"array","contains":{"type":"number"},"minContains":1,"maxContains":3}`: "expected between 1 and 3 matching items",
		} {
			if got := at(text); got != want {
				t.Errorf("%s: message %q, want %q", text, got, want)
			}
		}
	})

	t.Run("blame a oneOf failure on the branch that declares most of the object's properties", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"type": "object",
			"properties": { "where": { "$ref": "#/$defs/F" } },
			"$defs": {
				"F": {
					"oneOf": [
						{
							"type": "object",
							"properties": { "field": { "type": "string" }, "eq": { "type": "string" } },
							"required": ["field", "eq"],
							"additionalProperties": false
						},
						{
							"type": "object",
							"properties": { "and": { "type": "array", "items": { "$ref": "#/$defs/F" } } },
							"required": ["and"],
							"additionalProperties": false
						}
					]
				}
			}
		}`))
		wantErrors(t, v.Errors(mustDecode(t, `{"where":{"and":[{"field":"a","eq":1,"x":2}]}}`)), []schema.ValidationError{
			{Path: "/where/and/0/eq", Keyword: "type", Message: "expected string, got integer"},
			{Path: "/where/and/0/x", Keyword: "additionalProperties", Message: "unknown field"},
		})
	})

	t.Run("explain one object reached through the same reference at two paths at each path", func(t *testing.T) {
		v := mustCompile(t, mustDecode(t, `{
			"type": "object",
			"properties": { "a": { "$ref": "#/$defs/P" }, "b": { "$ref": "#/$defs/P" } },
			"$defs": { "P": { "type": "object", "properties": { "n": { "type": "integer" } } } }
		}`))
		shared := mustDecode(t, `{"n":"x"}`)
		wantErrors(t, v.Errors(map[string]any{"a": shared, "b": shared}), []schema.ValidationError{
			{Path: "/a/n", Keyword: "type", Message: "expected integer, got string"},
			{Path: "/b/n", Keyword: "type", Message: "expected integer, got string"},
		})
	})
}

func TestExtraFormats(t *testing.T) {
	t.Run("check url and json-pointer-uri-fragment", func(t *testing.T) {
		with := schema.CompileOptions{Formats: formats.Formats()}
		url := mustCompile(t, mustDecode(t, `{"type":"string","format":"url"}`), with)
		if !url.Check("https://example.com/a?b=1") || url.Check("not a url") {
			t.Fatal("url format")
		}
		fragment := mustCompile(t, mustDecode(t, `{"type":"string","format":"json-pointer-uri-fragment"}`), with)
		if !fragment.Check("#/a/b~1c") || fragment.Check("#/a b") || fragment.Check("/a") {
			t.Fatal("json-pointer-uri-fragment format")
		}
	})
}
