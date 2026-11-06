package typebox

import (
	"fmt"
	"math"
	"strings"
)

// The leaf keywords (keywords.go) against typebox 1.3.27: every case runs through TestValueMatchesTypeboxOnCases, which compares Value.Check and
// Value.Errors of typebox with this package's.
func init() {
	var cases []valueCase
	add := func(schema string, values ...any) {
		for _, value := range values {
			cases = append(cases, valueCase{schema, value})
		}
	}
	// minLength and maxLength count grapheme clusters as build/guard/string.mjs does.
	texts := []any{
		"", "a", "ab", "abc", "abcd", "e\u0301", "e\u0301\u0301", "👍", "👍👍", "👍🏽", "👨\u200d👩\u200d👧", "👨\u200d👩\u200d👧\u200d👦x", "🇺🇸", "🇺🇸🇬🇧", "a\ufe0f", "\u0300", "x\u0300\u0301y",
		"\u200d", "a\u200d", "a\u200db", "é", "日本語", "\U0001F600\u0301", "\u1ab0\u1dc0\ufe20",
	}
	for _, limit := range []string{"0", "1", "2", "3", "4"} {
		add(`Type.String({ minLength: `+limit+` })`, texts...)
		add(`Type.String({ maxLength: `+limit+` })`, texts...)
		add(`Type.String({ minLength: `+limit+`, maxLength: `+limit+` })`, texts...)
	}
	// Limits that are negative, fractional or large, against ASCII, non-ASCII and empty texts (textMinLength and textMaxLength decide ASCII texts by their byte length).
	for _, limit := range []string{"-1", "0.5", "1.5", "2.5", "3.0000001", "1000"} {
		add(`Type.String({ minLength: `+limit+` })`, append([]any{"abcd", "ab\u00e9", "\u00e9\u00e9\u00e9", "x" + strings.Repeat("y", 1000)}, texts...)...)
		add(`Type.String({ maxLength: `+limit+` })`, append([]any{"abcd", "ab\u00e9", "\u00e9\u00e9\u00e9", "x" + strings.Repeat("y", 1000)}, texts...)...)
	}
	// multipleOf uses a tolerance of 1e-10 and takes an integer for a multiple of any 1/n.
	numbers := []any{0.0, 1.0, 3.0, 0.3, 0.7, 0.1, 0.15, 7.5, 1e-11, 1e-9, 0.30000000000000004, 1e21, math.Copysign(0, -1), -3.0, -0.3, 2.5}
	for _, divisor := range []string{"0.1", "0.01", "3", "0.5", "2", "1e-9", "7"} {
		add(`Type.Number({ multipleOf: `+divisor+` })`, numbers...)
	}
	for _, rule := range []string{"minimum: 1", "maximum: 1", "exclusiveMinimum: 1", "exclusiveMaximum: 1", "minimum: 0.3, maximum: 0.7", "exclusiveMinimum: -0.3, exclusiveMaximum: 2.5"} {
		add(`Type.Number({ `+rule+` })`, numbers...)
	}
	// const and enum: === for a scalar option, structural equality for another.
	for _, schema := range []string{
		`Type.Unsafe({ const: { a: 1, b: [1, 2, { c: null }] } })`, `Type.Unsafe({ const: [1, "1", true, null] })`, `Type.Unsafe({ const: 1 })`,
		`Type.Unsafe({ const: "1" })`, `Type.Unsafe({ const: null })`, `Type.Unsafe({ const: {} })`, `Type.Unsafe({ const: [] })`, `Type.Unsafe({ const: false })`,
		`Type.Unsafe({ enum: [1, "a", null, { x: [1] }, [2, 3]] })`, `Type.Unsafe({ enum: [] })`, `Type.Unsafe({ enum: [0] })`,
	} {
		add(schema,
			map[string]any{"a": 1.0, "b": []any{1.0, 2.0, map[string]any{"c": nil}}}, map[string]any{"a": 1.0, "b": []any{1.0, 2.0, map[string]any{"c": false}}},
			map[string]any{"a": 1.0}, []any{1.0, "1", true, nil}, []any{1.0, 1.0, true, nil}, []any{}, map[string]any{}, 1.0, "1", "a", nil, false, true, 0.0, math.Copysign(0, -1),
			map[string]any{"x": []any{1.0}}, []any{2.0, 3.0}, []any{3.0, 2.0})
	}
	// Guard.IsDeepEqual: an object on the left equals an array on the right when they have the same own property names, the indices and "length".
	for _, schema := range []string{
		`Type.Unsafe({ const: ["a"] })`, `Type.Unsafe({ enum: [["a"], true] })`, `Type.Unsafe({ const: [] })`, `Type.Unsafe({ const: ["a", "b"] })`, `Type.Unsafe({ const: { k: ["a"] } })`,
		`Type.Unsafe({ const: [[1], {}] })`, `Type.Unsafe({ const: { "0": "a", length: 1 } })`, `Type.Unsafe({ const: [["a"]] })`,
	} {
		add(schema,
			map[string]any{"0": "a", "length": 1.0}, map[string]any{"0": "a", "length": 2.0}, map[string]any{"0": "a", "length": "1"}, map[string]any{"0": "a", "1": "b"}, map[string]any{"0": "a"},
			map[string]any{"length": 0.0}, map[string]any{"length": 0.0, "x": 1.0}, map[string]any{"0": "a", "1": "b", "length": 2.0}, map[string]any{"1": "a", "length": 1.0},
			map[string]any{"00": "a", "length": 1.0}, map[string]any{"0": "b", "length": 1.0}, map[string]any{"k": map[string]any{"0": "a", "length": 1.0}},
			map[string]any{"0": map[string]any{"0": 1.0, "length": 1.0}, "1": map[string]any{}, "length": 2.0}, map[string]any{"0": map[string]any{"0": "a", "length": 1.0}, "length": 1.0},
			[]any{"a"}, []any{}, []any{"a", "b"}, map[string]any{})
	}
	// A keyword whose value has the wrong type is skipped (Schema.IsMinLength and the others need a finite number, a string, an array or a boolean), whatever the value is.
	wrong := []string{`"3"`, `"abc"`, `""`, `null`, `true`, `false`, `[1]`, `{ a: 1 }`, `Infinity`, `NaN`, `-Infinity`, `[]`}
	everyValue := []any{
		"", "ab", "abcdef", "a@b.c", 0.0, 5.0, -1.0, 2.5, []any{}, []any{1.0}, []any{1.0, 2.0, 3.0}, []any{1.0, 1.0}, map[string]any{}, map[string]any{"a": 1.0}, nil, true,
	}
	for _, keyword := range []string{
		"minLength", "maxLength", "pattern", "format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "const", "enum", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
	} {
		for _, bad := range wrong {
			if keyword == "const" && (bad == `Infinity` || bad == `NaN` || bad == `-Infinity`) {
				// const has no guard and the schema reaches this package as JSON, which has no Infinity or NaN.
				continue
			}
			add(`Type.Unsafe({ `+keyword+`: `+bad+` })`, everyValue...)
		}
	}
	add(`Type.Unsafe({ minLength: "3", maxLength: 1, minimum: "1", maximum: 2, minItems: "5", maxItems: 2, pattern: 5, enum: 3, const: "x" })`, everyValue...)
	// uniqueItems hashes the values: an object's members in the order of their names, 0 and -0 apart.
	add(`Type.Array(Type.Any(), { uniqueItems: true })`,
		[]any{1.0, 1.0}, []any{0.0, math.Copysign(0, -1)}, []any{1.0, "1"}, []any{map[string]any{"a": 1.0, "b": 2.0}, map[string]any{"b": 2.0, "a": 1.0}},
		[]any{[]any{1.0}, []any{1.0}}, []any{nil, nil}, []any{true, 1.0}, []any{map[string]any{}, []any{}}, []any{"a", "a", "b"}, []any{}, []any{"é", "e\u0301"},
		[]any{[]any{1.0, 2.0}, []any{1.0}, []any{2.0}}, []any{"𝒜", "\uff5e", "\uff5e"}, []any{map[string]any{"\U0001d49c": 1.0, "\uff5e": 2.0}, map[string]any{"\uff5e": 2.0, "\U0001d49c": 1.0}},
		[]any{1.0, 2.0, 3.0, 1.0, 2.0})
	// hash.mjs InstanceKeys leaves out the key "constructor", so these objects hash alike.
	add(`Type.Array(Type.Any(), { uniqueItems: true })`,
		[]any{map[string]any{}, map[string]any{"constructor": 2.0}, "é"}, []any{map[string]any{"constructor": 1.0}, map[string]any{"constructor": 2.0}},
		[]any{map[string]any{"a": 1.0, "constructor": 5.0}, map[string]any{"a": 1.0}}, []any{map[string]any{"constructor": map[string]any{"constructor": 1.0}}, map[string]any{}},
		[]any{map[string]any{"a": 1.0, "constructor": 5.0}, map[string]any{"a": 2.0}})
	// Value.Clone (system/memory/clone.mjs) leaves out the prototype-pollution keys "__proto__", "constructor" and "prototype", and Clean and Default clone their input.
	add(`Type.Any()`, map[string]any{"__proto__": 1.0, "constructor": 2.0, "prototype": 3.0, "a": 4.0}, []any{map[string]any{"constructor": map[string]any{"prototype": 1.0, "b": 2.0}}})
	add(`Type.Array(Type.Any(), { uniqueItems: false })`, []any{1.0, 1.0})
	// minItems, maxItems, minProperties and maxProperties.
	add(`Type.Array(Type.Any(), { minItems: 2, maxItems: 3 })`, []any{}, []any{1.0}, []any{1.0, 2.0}, []any{1.0, 2.0, 3.0}, []any{1.0, 2.0, 3.0, 4.0})
	add(`Type.Object({}, { additionalProperties: true, minProperties: 1, maxProperties: 2 })`,
		map[string]any{}, map[string]any{"a": 1.0}, map[string]any{"a": 1.0, "b": 2.0}, map[string]any{"a": 1.0, "b": 2.0, "c": 3.0})
	// pattern.
	for _, pattern := range []string{`"^a"`, `"\\\\d+"`, `"^[a-z]+$"`, `"é"`, `"^$"`} {
		add(`Type.String({ pattern: `+pattern+` })`, "a", "b", "", "123", "abc", "ABC", "X", "é", "ab1")
	}
	// The formats of the registry, with the four that schema/formats checks.
	formats := map[string][]any{
		"hostname":     {"example.com", "-bad.com", "a_b.com", "", "xn--bcher-kva.example", "a..b", "a.b.", "-", "a-.b", "example.c0m", "bücher.example", "EXAMPLE.COM", "1.2.3.4"},
		"idn-hostname": {"bücher.example", "例え.jp", "-x.com", "a..b", "", "example.com", "xn--bcher-kva.example", "xn--a", "a\u0301.com", "ab--c.com", "a.b."},
		"idn-email":    {"用户@例子.广告", "a@b", "bad", "a@b.c", "", "\"a b\"@c.de", "a@-b.c", "a@b-.c", "名前@例.jp", "a b@c.de", "a@b c"},
		"email":        {"a@b.c", "a@b", "bad", "", "a@b..c", "用户@例子.广告"},
		"regex":        {"^a+$", "(", "[a-", "\\p{L}", "(?<n>a)\\k<n>", "a{2,1}", "\\", "*", "a**", "(?:a)", "[\\d-z]", "\\u{1F600}", "\\1"},
		"ipv4":         {"1.2.3.4", "256.1.1.1", "1.2.3", ""},
		"uuid":         {"123e4567-e89b-12d3-a456-426614174000", "nope", ""},
		"uri":          {"https://a.b/c", "a", "", "//a.b"},
		"unknown-fmt":  {"anything", ""},
		"semver":       {"1.2.3", "bad", ""},
		"period":       {"bad", ""},
	}
	for name, values := range formats {
		add(fmt.Sprintf(`Type.String({ format: %q })`, name), values...)
	}
	valueCases = append(valueCases, cases...)
}
