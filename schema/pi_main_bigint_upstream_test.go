package schema_test

// pi: packages/schema/src/coerce.ts
// pi: packages/schema/src/checks.ts
// pi: packages/schema/src/explain.ts
// pi: packages/schema/src/builders.ts
//
// Ports the packages/schema tests of Pi main af79b0c3d (TypeBox parity for extra types) that need a bigint or an undefined value, which Go holds as a *big.Int and schema.Undefined: coerce.test.ts "bigint and undefined, as TypeBox's Value.Convert", validator.test.ts "TypeBox's JavaScript types" and "values: treats a property set to undefined as absent", builders.test.ts "build a bigint with bounds", equal.test.ts' two undefined cases, and the cases of coerce-typebox.test.ts that hold a bigint or undefined (the 18 that testdata/coerce-typebox.json cannot carry, and the 15 Pi un-skipped at af79b0c3d: Array "Should Convert 10" and the 14 LiteralBigInt cases).

import (
	"math"
	"math/big"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

func bigint(n int64) *big.Int { return big.NewInt(n) }

// bigintOfFloat is BigInt(x) of an integer-valued number.
func bigintOfFloat(x float64) *big.Int {
	i, _ := new(big.Float).SetFloat64(x).Int(nil)
	return i
}

func typed(name string) schema.Schema { return chordjson.ObjectOf("type", name) }

func isUndefinedValue(v any) bool {
	_, ok := v.(schema.UndefinedType)
	return ok
}

// coerceAny is coerce.test.ts coerceValue for a value built in code: the coerced `value` property of `{ value }` under an object schema with one required property `value`.
func coerceAny(t *testing.T, s any, value any) any {
	t.Helper()
	root := chordjson.ObjectOf("type", "object", "properties", chordjson.ObjectOf("value", s), "required", []any{"value"})
	return coerce(t, root, chordjson.ObjectOf("value", value)).(*schema.Object).Value("value")
}

// coerce.test.ts "bigint and undefined, as TypeBox's Value.Convert": "converts to a bigint like TypeBox's TryBigInt".
func TestPiMainConvertsToABigIntLikeTypeBox(t *testing.T) {
	root := chordjson.ObjectOf("type", "object", "properties", chordjson.ObjectOf("n", schema.BigInt(schema.BigMinimum(bigint(0)))), "required", []any{"n"})
	parsed := parseOf(t, coercing(t, root), chordjson.ObjectOf("n", "42"))
	if !parsed.OK || !equalJSON(parsed.Value, chordjson.ObjectOf("n", bigint(42))) {
		t.Fatalf("parse = %#v", parsed)
	}
	for _, c := range []struct{ input, want any }{
		{"12n", bigint(12)},
		{"12.9", bigint(12)},
		{3.7, bigint(3)},
		{true, bigint(1)},
		{"x", "x"},
	} {
		if got := coerceAny(t, typed("bigint"), c.input); !equalJSON(got, c.want) {
			t.Errorf("coerceValue(BigInt, %#v) = %#v, want %#v", c.input, got, c.want)
		}
	}
}

// coerce.test.ts "bigint and undefined, as TypeBox's Value.Convert": "converts to undefined like TypeBox's TryUndefined".
func TestPiMainConvertsToUndefinedLikeTypeBox(t *testing.T) {
	for _, value := range []any{nil, 0.0, bigint(0), false, "", "0", "null", "undefined"} {
		if got := coerceAny(t, typed("undefined"), value); !isUndefinedValue(got) {
			t.Errorf("coerceValue(Undefined, %#v) = %#v, want undefined", value, got)
		}
	}
	if got := coerceAny(t, typed("undefined"), "x"); got != "x" {
		t.Errorf("coerceValue(Undefined, \"x\") = %#v, want \"x\"", got)
	}
}

// coerce-typebox.test.ts: the cases that hold a bigint or undefined (pass 2 alone, as TypeBox's Value.Convert). The two LiteralBigInt describe blocks hold the same seven cases.
func TestPiMainValueConvertWithBigIntAndUndefined(t *testing.T) {
	anything := chordjson.ObjectOf()
	literal := func(n int64) any { return chordjson.ObjectOf("const", bigint(n)) }
	tenToThe52 := bigintOfFloat(1e52)
	cases := []struct {
		name   string
		schema any
		input  any
		want   any
	}{
		{"Any 2", anything, schema.Undefined, schema.Undefined},
		{"Array 9", schema.Arr(anything), schema.Undefined, []any{schema.Undefined}},
		{"Array 10", schema.Arr(typed("bigint")), bigint(1), []any{bigint(1)}},
		{"Boolean 17", schema.Bool(), bigint(0), false},
		{"Boolean 18", schema.Bool(), bigint(1), true},
		{"Boolean 19", schema.Bool(), bigint(2), bigint(2)},
		{"Boolean 20", schema.Bool(), bigint(1), true},
		{"Boolean 23", schema.Bool(), schema.Undefined, false},
		{"Null 10", schema.Nul(), bigint(0), nil},
		{"Null 11", schema.Nul(), bigint(1), bigint(1)},
		{"Null 17", schema.Nul(), schema.Undefined, nil},
		{"Number 10", schema.Num(), bigint(0), 0.0},
		{"Number 11", schema.Num(), tenToThe52, tenToThe52},
		{"Number 19", schema.Num(), schema.Undefined, 0.0},
		{"Object 7", chordjson.ObjectOf("type", "object", "properties", chordjson.ObjectOf("x", schema.Num())), chordjson.ObjectOf("x", schema.Undefined), chordjson.ObjectOf("x", schema.Undefined)},
		{"String 5", schema.Str(), bigint(12345), "12345"},
		{"String 8", schema.Str(), bigint(42), "42"},
		{"String 14", schema.Str(), schema.Undefined, ""},
		{"Unknown 2", anything, schema.Undefined, schema.Undefined},
	}
	for range 2 {
		for _, c := range []struct {
			name        string
			constant    int64
			input, want any
		}{
			{"LiteralBigInt 1", 100, bigint(100), bigint(100)},
			{"LiteralBigInt 2", 100, 100.0, bigint(100)},
			{"LiteralBigInt 3", 1, "true", bigint(1)},
			{"LiteralBigInt 4", 0, "false", bigint(0)},
			{"LiteralBigInt 5", 1, "1", bigint(1)},
			{"LiteralBigInt 6", 0, "0", bigint(0)},
			{"LiteralBigInt 7", 100, "2", "2"},
		} {
			cases = append(cases, struct {
				name   string
				schema any
				input  any
				want   any
			}{c.name, literal(c.constant), c.input, c.want})
		}
	}
	for _, c := range cases {
		got, err := schema.ConvertOnly(c.schema, c.input)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !equalJSON(got, c.want) {
			t.Errorf("%s: Convert(%s, %#v) = %#v, want %#v", c.name, marshalled(c.schema), c.input, got, c.want)
		}
	}
}

// builders.test.ts "build a bigint with bounds, typed bigint" (the static type assertion has no Go form).
func TestPiMainBuildsABigIntWithBounds(t *testing.T) {
	metric := schema.Nullable(schema.BigInt(schema.BigMinimum(bigint(0))))
	want := chordjson.ObjectOf("anyOf", []any{chordjson.ObjectOf("type", "bigint", "minimum", bigint(0)), chordjson.ObjectOf("type", "null")})
	if !exactJSON(metric, want) {
		t.Fatalf("Nullable(BigInt) = %s", marshalled(metric))
	}
	v := mustCompile(t, metric)
	for value, ok := range map[any]bool{bigint(3): true, bigint(-1): false, 3.0: false} {
		if got := v.Check(value); got != ok {
			t.Errorf("Check(%v) = %v, want %v", value, got, ok)
		}
	}
	// Every BigInt option writes its keyword, in call order, after the type.
	all := schema.BigInt(schema.Title("t"), schema.BigMinimum(bigint(1)), schema.BigMaximum(bigint(9)), schema.BigExclusiveMinimum(bigint(0)), schema.BigExclusiveMaximum(bigint(10)), schema.BigMultipleOf(bigint(2)))
	wantAll := chordjson.ObjectOf("type", "bigint", "title", "t", "minimum", bigint(1), "maximum", bigint(9), "exclusiveMinimum", bigint(0), "exclusiveMaximum", bigint(10), "multipleOf", bigint(2))
	if !exactJSON(all, wantAll) {
		t.Fatalf("BigInt(options) = %s", marshalled(all))
	}
}

// validator.test.ts "TypeBox's JavaScript types": "check Type.BigInt with its bounds, as TypeBox does".
func TestPiMainChecksTypeBoxBigIntWithItsBounds(t *testing.T) {
	union := schema.Union([]schema.Schema{schema.BigInt(schema.BigMinimum(bigint(0))), schema.Nul()})
	v := mustCompile(t, union)
	for _, value := range []any{bigint(5), bigint(0), nil} {
		if !v.Check(value) {
			t.Errorf("rejects %#v", value)
		}
	}
	for _, value := range []any{bigint(-1), 5.0, "5", schema.Undefined} {
		if v.Check(value) {
			t.Errorf("accepts %#v", value)
		}
	}
	if !mustCompile(t, schema.BigInt(schema.BigMaximum(bigint(10)), schema.BigMultipleOf(bigint(5)))).Check(bigint(10)) {
		t.Error("10n is within maximum 10n and a multiple of 5n")
	}
	if mustCompile(t, schema.BigInt(schema.BigMultipleOf(bigint(5)))).Check(bigint(7)) {
		t.Error("7n is no multiple of 5n")
	}
	if mustCompile(t, schema.BigInt(schema.BigExclusiveMaximum(bigint(3)))).Check(bigint(3)) {
		t.Error("3n is not below exclusiveMaximum 3n")
	}
	wantErrors(t, mustCompile(t, schema.BigInt(schema.BigMinimum(bigint(0)))).Errors(bigint(-2)), []schema.ValidationError{
		{Path: "", Keyword: "minimum", Message: "expected >= 0"},
	})
	wantErrors(t, mustCompile(t, schema.BigInt()).Errors(2.0), []schema.ValidationError{
		{Path: "", Keyword: "type", Message: "expected bigint, got integer"},
	})
}

// validator.test.ts "TypeBox's JavaScript types": "check Type.Undefined: only undefined, and a required property with it must be present".
func TestPiMainChecksTypeBoxUndefined(t *testing.T) {
	if !mustCompile(t, typed("undefined")).Check(schema.Undefined) || mustCompile(t, typed("undefined")).Check(nil) {
		t.Fatal("undefined is the only value of {type: undefined}")
	}
	v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"selection":{"const":"average"},"principalId":{"type":"undefined"}},"required":["selection","principalId"]}`))
	for _, c := range []struct {
		value *schema.Object
		want  bool
	}{
		{chordjson.ObjectOf("selection", "average", "principalId", schema.Undefined), true},
		{chordjson.ObjectOf("selection", "average"), false},
		{chordjson.ObjectOf("selection", "average", "principalId", "x"), false},
		{chordjson.ObjectOf("selection", "average", "principalId", nil), false},
	} {
		if got := v.Check(c.value); got != c.want {
			t.Errorf("Check(%s) = %v, want %v", marshalled(c.value), got, c.want)
		}
	}
	wantErrors(t, v.Errors(chordjson.ObjectOf("selection", "average")), []schema.ValidationError{
		{Path: "/principalId", Keyword: "required", Message: "missing"},
	})
	wantErrors(t, v.Errors(chordjson.ObjectOf("selection", "average", "principalId", "x")), []schema.ValidationError{
		{Path: "/principalId", Keyword: "type", Message: "expected undefined, got string"},
	})
}

// validator.test.ts "TypeBox's JavaScript types": "check a required property set to undefined by its schema, as TypeBox does".
func TestPiMainChecksARequiredPropertySetToUndefinedBySchema(t *testing.T) {
	// Present and undefined: valid when the schema takes undefined (Undefined in a union, Unknown, Any).
	for _, property := range []string{`{"anyOf":[{"type":"string"},{"type":"undefined"}]}`, `{}`} {
		for _, closed := range []string{``, `,"additionalProperties":false`} {
			v := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"a":`+property+`},"required":["a"]`+closed+`}`))
			if !v.Check(chordjson.ObjectOf("a", schema.Undefined)) {
				t.Errorf("%s%s rejects a set to undefined", property, closed)
			}
			if v.Check(chordjson.ObjectOf()) {
				t.Errorf("%s%s accepts a missing a", property, closed)
			}
		}
	}
	// A required property whose schema does not take undefined stays missing when set to undefined.
	name := mustCompile(t, mustDecode(t, `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`))
	if name.Check(chordjson.ObjectOf("name", schema.Undefined)) {
		t.Error("a required string accepts undefined")
	}
	wantErrors(t, name.Errors(chordjson.ObjectOf("name", schema.Undefined)), []schema.ValidationError{{Path: "/name", Keyword: "required", Message: "missing"}})
	// An optional property set to undefined still counts as absent.
	if !mustCompile(t, mustDecode(t, `{"type":"object","properties":{"name":{"type":"string"}}}`)).Check(chordjson.ObjectOf("name", schema.Undefined)) {
		t.Error("an optional string set to undefined is not absent")
	}
}

// validator.test.ts "TypeBox's JavaScript types": "check multipleOf of bigints exactly, however large".
func TestPiMainChecksMultipleOfOfBigIntsExactly(t *testing.T) {
	five := mustCompile(t, schema.BigInt(schema.BigMultipleOf(bigint(5))))
	huge := new(big.Int).Exp(bigint(10), bigint(400), nil)
	if !five.Check(huge) {
		t.Error("10n ** 400n is a multiple of 5n")
	}
	if five.Check(new(big.Int).Add(huge, bigint(1))) {
		t.Error("10n ** 400n + 1n is no multiple of 5n")
	}
	// A bigint is no multiple of 0 or of a fraction.
	if mustCompile(t, chordjson.ObjectOf("multipleOf", bigint(0))).Check(bigint(4)) {
		t.Error("4n is a multiple of 0n")
	}
	if mustCompile(t, chordjson.ObjectOf("multipleOf", 0.5)).Check(bigint(3)) {
		t.Error("3n is a multiple of 0.5")
	}
}

// validator.test.ts "values": "treats a property set to undefined as absent", with the value Go holds for undefined.
func TestPiMainTreatsAPropertySetToUndefinedAsAbsent(t *testing.T) {
	v := mustCompile(t, mustDecode(t, readSchema))
	if !v.Check(chordjson.ObjectOf("path", "a.ts", "offset", schema.Undefined)) {
		t.Error("an optional offset set to undefined is not absent")
	}
	if v.Check(chordjson.ObjectOf("path", schema.Undefined)) {
		t.Error("a required path set to undefined is present")
	}
}

// equal.test.ts: the two cases that hold undefined, which Value.Equal answers false for.
func TestPiMainEqualOfUndefined(t *testing.T) {
	if schema.Equal(nil, schema.Undefined) {
		t.Error("Equal(null, undefined)")
	}
	if schema.Equal(chordjson.ObjectOf("a", schema.Undefined), chordjson.ObjectOf()) {
		t.Error("Equal({ a: undefined }, {})")
	}
	if !schema.Equal(schema.Undefined, schema.Undefined) || !schema.Equal(bigint(7), bigint(7)) || schema.Equal(bigint(7), 7.0) {
		t.Error("undefined equals itself, a bigint is equal by value and never to a number")
	}
}

// Go forms the Pi tests do not pin: bounds of either kind compare with values of either kind, exactly; an enum or const holds a bigint; the number keywords of a bigint; and a bigint or undefined holds where Pi's checks read `typeof v`.
func TestBigIntAndUndefinedAreValuesOfTheChecks(t *testing.T) {
	huge := new(big.Int).Exp(bigint(10), bigint(30), nil)
	for _, c := range []struct {
		name  string
		s     any
		value any
		want  bool
	}{
		{"number bound, bigint value above", chordjson.ObjectOf("minimum", 5.0), bigint(6), true},
		{"number bound, bigint value below", chordjson.ObjectOf("minimum", 5.0), bigint(4), false},
		{"fractional bound, bigint value", chordjson.ObjectOf("maximum", 5.5), bigint(5), true},
		{"fractional bound, bigint value above", chordjson.ObjectOf("maximum", 5.5), bigint(6), false},
		{"infinite bound", chordjson.ObjectOf("maximum", math.Inf(1)), huge, true},
		{"-Infinity minimum", chordjson.ObjectOf("minimum", math.Inf(-1)), bigint(-3), true},
		{"+Infinity minimum", chordjson.ObjectOf("minimum", math.Inf(1)), huge, false},
		{"NaN bound holds for nothing", chordjson.ObjectOf("minimum", math.NaN()), bigint(1), false},
		{"bigint bound, number value", chordjson.ObjectOf("minimum", bigint(5)), 6.0, true},
		{"bigint bound, number value below", chordjson.ObjectOf("exclusiveMinimum", bigint(5)), 5.0, false},
		{"bigint bound, fractional value", chordjson.ObjectOf("maximum", bigint(5)), 5.5, false},
		{"bigint bound, NaN value", chordjson.ObjectOf("maximum", bigint(5)), math.NaN(), false},
		{"bigint bounds, bigint value", chordjson.ObjectOf("minimum", bigint(5), "exclusiveMaximum", huge), huge, false},
		{"bigint of a number schema", chordjson.ObjectOf("type", "number"), bigint(1), false},
		{"bigint of an integer schema", chordjson.ObjectOf("type", "integer"), bigint(1), false},
		{"bigint of a typeless number keyword passes the type", chordjson.ObjectOf("multipleOf", 2.0), bigint(4), true},
		{"number of a bigint schema", chordjson.ObjectOf("type", "bigint"), 1.0, false},
		{"bigint of a bigint schema", chordjson.ObjectOf("type", "bigint"), huge, true},
		{"bigint in a type array", chordjson.ObjectOf("type", []any{"string", "bigint"}), bigint(1), true},
		{"undefined in a type array", chordjson.ObjectOf("type", []any{"string", "undefined"}), schema.Undefined, true},
		{"multiple of a number divisor", chordjson.ObjectOf("type", "bigint", "multipleOf", 3.0), bigint(9), true},
		{"not a multiple of an infinite divisor", chordjson.ObjectOf("type", "bigint", "multipleOf", math.Inf(1)), bigint(9), false},
		{"number multiple of a bigint divisor", chordjson.ObjectOf("multipleOf", bigint(3)), 9.0, true},
		{"fractional number is no multiple of a bigint", chordjson.ObjectOf("multipleOf", bigint(3)), 4.5, false},
		{"const bigint", chordjson.ObjectOf("const", bigint(100)), bigint(100), true},
		{"const bigint, another value", chordjson.ObjectOf("const", bigint(100)), bigint(101), false},
		{"const bigint, a number", chordjson.ObjectOf("const", bigint(100)), 100.0, false},
		{"enum of bigints", chordjson.ObjectOf("enum", []any{bigint(1), bigint(2)}), bigint(2), true},
		{"enum of bigints, other", chordjson.ObjectOf("enum", []any{bigint(1), bigint(2)}), bigint(3), false},
		{"enum of more than eight", chordjson.ObjectOf("enum", []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, bigint(9), schema.Undefined}), bigint(9), true},
		{"enum of more than eight, undefined", chordjson.ObjectOf("enum", []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, bigint(9), schema.Undefined}), schema.Undefined, true},
		{"enum of more than eight, other bigint", chordjson.ObjectOf("enum", []any{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, bigint(9), schema.Undefined}), bigint(10), false},
		{"unique bigints", chordjson.ObjectOf("uniqueItems", true), []any{bigint(1), bigint(2)}, true},
		{"repeated bigints", chordjson.ObjectOf("uniqueItems", true), []any{bigint(1), bigint(1)}, false},
		{"undefined is not an object", chordjson.ObjectOf("type", "object"), schema.Undefined, false},
		{"undefined is not null", chordjson.ObjectOf("type", "null"), schema.Undefined, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mustCompile(t, c.s).Check(c.value); got != c.want {
				t.Errorf("Check(%#v) of %s = %v, want %v", c.value, marshalled(c.s), got, c.want)
			}
		})
	}
}

// Explanations name a bigint and compare it with bounds of either kind, as explain.ts walkNumber does.
func TestExplainsBigIntsAgainstBoundsOfEitherKind(t *testing.T) {
	huge := new(big.Int).Exp(bigint(10), bigint(30), nil)
	wantErrors(t, mustCompile(t, chordjson.ObjectOf("minimum", bigint(5), "maximum", 6.5, "exclusiveMinimum", 0.0, "exclusiveMaximum", bigint(100), "multipleOf", bigint(4))).Errors(bigint(7)), []schema.ValidationError{
		{Path: "", Keyword: "maximum", Message: "expected <= 6.5"},
		{Path: "", Keyword: "multipleOf", Message: "expected a multiple of 4"},
	})
	wantErrors(t, mustCompile(t, chordjson.ObjectOf("minimum", bigint(5), "exclusiveMinimum", bigint(8), "exclusiveMaximum", huge, "maximum", huge)).Errors(bigint(4)), []schema.ValidationError{
		{Path: "", Keyword: "minimum", Message: "expected >= 5"},
		{Path: "", Keyword: "exclusiveMinimum", Message: "expected > 8"},
	})
	wantErrors(t, mustCompile(t, chordjson.ObjectOf("exclusiveMaximum", huge)).Errors(huge), []schema.ValidationError{
		{Path: "", Keyword: "exclusiveMaximum", Message: "expected < " + huge.String()},
	})
	wantErrors(t, mustCompile(t, schema.BigInt()).Errors("1"), []schema.ValidationError{{Path: "", Keyword: "type", Message: "expected bigint, got string"}})
	wantErrors(t, mustCompile(t, schema.Str()).Errors(bigint(1)), []schema.ValidationError{{Path: "", Keyword: "type", Message: "expected string, got bigint"}})
	wantErrors(t, mustCompile(t, schema.Str()).Errors(schema.Undefined), []schema.ValidationError{{Path: "", Keyword: "type", Message: "expected string, got undefined"}})
}

// A bigint or undefined that pass 2 converts to is also what a Parse checks, through unions and type arrays (pass 3 reads them by matchesType).
func TestCoerceToABigIntOrUndefinedThroughUnionsAndTypeArrays(t *testing.T) {
	for _, c := range []struct {
		name   string
		s      any
		input  any
		want   any
		wantOK bool
	}{
		{"nullable bigint from a string", schema.Nullable(typed("bigint")), "12", bigint(12), true},
		{"nullable bigint keeps null", schema.Nullable(typed("bigint")), nil, nil, true},
		{"type array bigint or string keeps a string", chordjson.ObjectOf("type", []any{"bigint", "string"}), "abc", "abc", true},
		{"a type array does not convert to a bigint (coercePrimitive has no bigint)", chordjson.ObjectOf("type", []any{"bigint", "null"}), 7.9, 7.9, false},
		{"bigint above the safe integers stays a bigint for a number", schema.Num(), new(big.Int).Exp(bigint(10), bigint(20), nil), new(big.Int).Exp(bigint(10), bigint(20), nil), false},
		{"safe bigint becomes a number", schema.Num(), bigint(9007199254740991), 9007199254740991.0, true},
		{"unsafe negative bigint stays", schema.Int(), bigint(-9007199254740992), bigint(-9007199254740992), false},
		{"bigint 1 becomes true", schema.Bool(), bigint(1), true, true},
		{"large float truncated to a bigint", typed("bigint"), 1e21, bigintOfFloat(1e21), true},
		{"negative fraction truncated toward zero", typed("bigint"), -2.9, bigint(-2), true},
		{"negative zero string", typed("bigint"), "-0", bigint(0), true},
		{"fraction string truncated", typed("bigint"), "-3.9", bigint(-3), true},
		{"signed n string", typed("bigint"), "-12n", bigint(-12), true},
		{"true in any case", typed("bigint"), "TrUe", bigint(1), true},
		{"false in any case", typed("bigint"), "FALSE", bigint(0), true},
		{"leading zeros are no bigint", typed("bigint"), "012", "012", false},
		{"a fraction needs digits", typed("bigint"), "1.", "1.", false},
		{"an array is no bigint", typed("bigint"), []any{}, []any{}, false},
		{"NaN is no bigint", typed("bigint"), math.NaN(), math.NaN(), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := chordjson.ObjectOf("type", "object", "properties", chordjson.ObjectOf("v", c.s), "required", []any{"v"})
			result := parseOf(t, coercing(t, root), chordjson.ObjectOf("v", c.input))
			var got any
			if result.OK {
				got = result.Value.(*schema.Object).Value("v")
			} else {
				got = coerceAny(t, c.s, c.input)
			}
			if result.OK != c.wantOK || !equalJSON(got, c.want) {
				t.Errorf("Parse = ok %v, value %#v; want ok %v, value %#v", result.OK, got, c.wantOK, c.want)
			}
		})
	}
}

// The coercions that cannot become undefined or a bigint leave the value to be reported, and an optional null of an undefined-typed property is dropped by pass 1 while a required one is converted.
func TestOptionalAndRequiredNullsOfAnUndefinedProperty(t *testing.T) {
	optional := mustDecode(t, `{"type":"object","properties":{"u":{"type":"undefined"}}}`)
	got := coerce(t, optional, mustDecode(t, `{"u":null}`))
	if !equalJSON(got, chordjson.ObjectOf()) {
		t.Errorf("optional: %s", marshalled(got))
	}
	required := mustDecode(t, `{"type":"object","required":["u"],"properties":{"u":{"type":"undefined"}}}`)
	result := parseOf(t, coercing(t, required), mustDecode(t, `{"u":null}`))
	if !result.OK || !equalJSON(result.Value, chordjson.ObjectOf("u", schema.Undefined)) {
		t.Errorf("required: %#v", result)
	}
	// Anything else is left for the check to report.
	result = parseOf(t, coercing(t, required), mustDecode(t, `{"u":"x"}`))
	wantParseErrors(t, result, []schema.ValidationError{{Path: "/u", Keyword: "type", Message: "expected undefined, got string"}})
}

// matchesType of a type array reads a number as finite (Pi main 55a5c6914): NaN and the infinities, which only a value built in code can be, are no `number`, so the first type that converts them wins.
func TestCoerceReadsNaNAndInfinityAsNoNumberInATypeArray(t *testing.T) {
	v := coercing(t, mustDecode(t, `{"type":["string","number"]}`))
	for value, want := range map[float64]string{math.NaN(): "NaN", math.Inf(1): "Infinity", math.Inf(-1): "-Infinity"} {
		if got := coerceOf(t, v, value); got != want {
			t.Errorf("Coerce(%v) = %#v, want %q", value, got, want)
		}
	}
	if got := coerceOf(t, v, 3.5); got != 3.5 {
		t.Errorf("Coerce(3.5) = %#v, a number stays", got)
	}
}
