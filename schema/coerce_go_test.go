package schema_test

// What the Go forms of the coercion add to Pi's cases: Go maps and Go numeric kinds as instance values, member order after a copy, and the errors of a schema that does not compile.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
)

func TestCoerceGoValues(t *testing.T) {
	numberSchema := mustDecode(t, `{"type":"object","properties":{"n":{"type":"integer"},"s":{"type":"string"}}}`)

	t.Run("a Go map is copied only when a member changes", func(t *testing.T) {
		v := coercing(t, numberSchema, schema.CompileOptions{})
		unchanged := map[string]any{"n": 1.0, "s": "a"}
		if got := coerceOf(t, v, unchanged); !reflect.DeepEqual(got, any(unchanged)) || reflect.ValueOf(got).Pointer() != reflect.ValueOf(unchanged).Pointer() {
			t.Fatalf("an unchanged map came back as %v", got)
		}
		input := map[string]any{"n": "7", "s": "a"}
		got := coerceOf(t, v, input).(map[string]any)
		if !reflect.DeepEqual(got, map[string]any{"n": 7.0, "s": "a"}) {
			t.Fatalf("coerced map = %v", got)
		}
		if !reflect.DeepEqual(input, map[string]any{"n": "7", "s": "a"}) {
			t.Fatalf("the input map was modified: %v", input)
		}
	})

	t.Run("every Go numeric kind is a number", func(t *testing.T) {
		v := coercing(t, numberSchema, schema.CompileOptions{})
		for _, n := range []any{int(5), int64(5), float32(5), uint8(5)} {
			input := map[string]any{"n": n}
			if got := coerceOf(t, v, input); reflect.ValueOf(got).Pointer() != reflect.ValueOf(input).Pointer() {
				t.Fatalf("a %T number was coerced: %v", n, got)
			}
			if got := coerceOf(t, v, map[string]any{"s": n}).(map[string]any)["s"]; got != "5" {
				t.Fatalf("a %T number for a string = %#v, want \"5\"", n, got)
			}
		}
	})

	t.Run("a copied object keeps the member order, array-index keys first", func(t *testing.T) {
		s := mustDecode(t, `{"type":"object","properties":{"b":{"type":"number"},"2":{"type":"number"},"a":{"type":"number"},"1":{"type":"string"}}}`)
		input := mustDecode(t, `{"b":"1","2":"x","a":"2","1":"y","z":"w"}`)
		got := coerceOf(t, coercing(t, s, schema.CompileOptions{}), input).(*schema.Object)
		if want := []string{"1", "2", "b", "a", "z"}; !reflect.DeepEqual(got.Keys(), want) {
			t.Fatalf("member order = %v, want %v", got.Keys(), want)
		}
		if !exactJSON(got, mustDecode(t, `{"1":"y","2":"x","b":1,"a":2,"z":"w"}`)) {
			t.Fatalf("coerced = %s", marshalled(got))
		}
	})

	t.Run("a schema that does not compile fails CompileCoercing as it fails Compile", func(t *testing.T) {
		if _, err := schema.CompileCoercing(mustDecode(t, `{"properties":{"a":5}}`), schema.CompileOptions{}); err == nil {
			t.Fatal("CompileCoercing accepted a malformed schema")
		}
		if _, err := schema.CompileCoercing(mustDecode(t, `{"$ref":"#/$defs/missing"}`), schema.CompileOptions{}); err == nil {
			t.Fatal("CompileCoercing accepted a reference that names nothing")
		}
	})

	t.Run("a boolean schema coerces nothing", func(t *testing.T) {
		for _, s := range []any{true, false} {
			input := mustDecode(t, `{"a":"1"}`)
			if got := coerceOf(t, coercing(t, s, schema.CompileOptions{}), input); got != input {
				t.Fatalf("schema %v coerced %s to %s", s, marshalled(input), marshalled(got))
			}
		}
	})
}
