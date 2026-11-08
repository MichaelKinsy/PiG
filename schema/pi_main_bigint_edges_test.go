package schema_test

// pi: packages/schema/src/checks.ts
// pi: packages/schema/src/coerce.ts
// pi: packages/schema/src/explain.ts

import (
	"math/big"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// checks.ts numberCheck: mode 1 and 2 reject a bigint (`mode !== 0 && !Number.isFinite(v)`), also when the bounds make the number group carry the type.
func TestIntegerAndNumberBoundsRejectABigInt(t *testing.T) {
	for _, s := range []string{`{"type":"integer","minimum":0}`, `{"type":"number","minimum":0}`} {
		if mustCompile(t, mustDecode(t, s)).Check(big.NewInt(5)) {
			t.Errorf("%s accepts 5n", s)
		}
	}
}

// coerce.ts tryNumber: a bigint converts to a number only within the safe integers.
func TestBigIntToNumberStopsAtTheSafeIntegers(t *testing.T) {
	max := big.NewInt(1<<53 - 1)
	above := new(big.Int).Add(max, big.NewInt(1))
	if got, _ := schema.ConvertOnly(schema.Num(), max); got != float64(1<<53-1) {
		t.Errorf("Convert(Number, 2**53-1 n) = %#v", got)
	}
	if got, _ := schema.ConvertOnly(schema.Num(), above); got != any(above) {
		t.Errorf("Convert(Number, 2**53 n) = %#v, want the bigint", got)
	}
}

// explain.ts: a required property set to undefined whose schema takes undefined is not missing.
func TestErrorsOfARequiredUndefinedTheSchemaTakes(t *testing.T) {
	v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"a":{},"b":{"type":"string"}},"required":["a","b"]}`))
	wantErrors(t, v.Errors(chordjson.ObjectOf("a", schema.Undefined, "b", 1.0)), []schema.ValidationError{
		{Path: "/b", Keyword: "type", Message: "expected string, got integer"},
	})
}

// coerce.ts reenter memo: two bigints are two values.
func TestBigIntsUnderARefKeepTheirValues(t *testing.T) {
	s := mustDecode(t, `{"$defs":{"B":{"type":"bigint"}},"type":"array","items":{"$ref":"#/$defs/B"}}`)
	got, err := schema.ConvertOnly(s, []any{big.NewInt(5), big.NewInt(7), "9"})
	if err != nil || !equalJSON(got, []any{big.NewInt(5), big.NewInt(7), big.NewInt(9)}) {
		t.Errorf("Convert = %#v, %v", got, err)
	}
}

// checks.ts openObject: an optional property set to undefined counts as absent.
func TestOpenObjectOptionalUndefinedIsAbsent(t *testing.T) {
	v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"number"}}`))
	if !v.Check(chordjson.ObjectOf("a", schema.Undefined)) {
		t.Error("an optional a set to undefined is not absent")
	}
}
