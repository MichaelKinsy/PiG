package typebox

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const pinnedTypeboxVersion = "1.3.27"

type valueCase struct {
	Schema string `json:"schema"`
	Value  any    `json:"value"`
}

type valueAnswer struct {
	Schema  any          `json:"schema"`
	Check   bool         `json:"check"`
	Errors  []ValueError `json:"errors"`
	Clean   any          `json:"clean"`
	Default any          `json:"default"`
}

func (e *ValueError) UnmarshalJSON(data []byte) error {
	var wire struct {
		InstancePath string `json:"instancePath"`
		Message      string `json:"message"`
	}
	err := json.Unmarshal(data, &wire)
	e.InstancePath, e.Message = wire.InstancePath, wire.Message
	return err
}

// absent stands for a JavaScript undefined value in a case.
const absent = "<absent>"

var valueCases = []valueCase{
	{`Type.Object({ session: Type.String(), conversation: Type.Number({ default: 0 }) })`, map[string]any{"session": "s", "extra": 1}},
	{`Type.Object({ session: Type.String(), conversation: Type.Number({ default: 0 }) })`, map[string]any{"session": 1}},
	{`Type.Object({ session: Type.String(), conversation: Type.Number({ default: 0 }) })`, map[string]any{}},
	{`Type.Object({ a: Type.Optional(Type.String()), b: Type.Object({ c: Type.Boolean({ default: true }) }, { default: {} }) })`, map[string]any{"x": 1}},
	{`Type.Object({ a: Type.String() }, { additionalProperties: true })`, map[string]any{"a": "x", "b": 2}},
	{`Type.Object({ a: Type.String() }, { additionalProperties: Type.Number() })`, map[string]any{"a": "x", "b": 2, "c": "no"}},
	{`Type.Array(Type.Object({ id: Type.Number(), tag: Type.String({ default: "t" }) }))`, []any{map[string]any{"id": 1, "x": 2}, map[string]any{"id": 2, "tag": "u"}}},
	{`Type.Tuple([Type.String(), Type.Number()])`, []any{"a", 1, true}},
	{`Type.Union([Type.Object({ kind: Type.Literal("a"), a: Type.Number() }), Type.Object({ kind: Type.Literal("b"), b: Type.String() })])`, map[string]any{"kind": "b", "b": "x", "a": 1}},
	{`Type.Record(Type.String(), Type.Number())`, map[string]any{"a": 1, "b": 2}},
	{`Type.Intersect([Type.Object({ a: Type.Number() }), Type.Object({ b: Type.String({ default: "d" }) })])`, map[string]any{"a": 1, "z": 3}},
	{`Type.String({ minLength: 2, maxLength: 3 })`, "a"},
	{`Type.Number({ minimum: 1, maximum: 5 })`, float64(9)},
	{`Type.Integer()`, 1.5},
	{`Type.Array(Type.String(), { minItems: 2 })`, []any{"a"}},
	{`Type.Literal("x")`, "y"},
	{`Type.Null()`, nil},
	{`Type.Object({ when: Type.String({ format: "date" }) })`, map[string]any{"when": "2024-13-01"}},
	{`Type.Object({ a: Type.String({ default: "x" }) }, { default: { a: "y" } })`, absent},
	// Review of b64e713570 (rev-complexity-audit-3): error lists of unions, required, additional properties, nested
	// arrays, patterns, formats, integer bounds, records, intersections, short tuples, unique items and bounds.
	{`Type.Union([Type.Object({ kind: Type.Literal("a"), a: Type.Number() }), Type.Object({ kind: Type.Literal("b"), b: Type.String() })])`, map[string]any{"kind": "c"}},
	{`Type.Object({ a: Type.String(), b: Type.Number(), c: Type.Boolean() })`, map[string]any{}},
	{`Type.Object({ a: Type.String() }, { additionalProperties: false })`, map[string]any{"a": "x", "b": 1, "c": 2}},
	{`Type.Object({ list: Type.Array(Type.Number()) })`, map[string]any{"list": []any{1, "x", "y"}}},
	{`Type.Union([Type.String(), Type.Number()])`, true},
	{`Type.Object({ a: Type.String({ pattern: "^x" }) })`, map[string]any{"a": "y"}},
	{`Type.Object({ a: Type.String({ format: "email" }) })`, map[string]any{"a": "y"}},
	{`Type.Object({ a: Type.Integer({ minimum: 0 }) })`, map[string]any{"a": -1.5}},
	{`Type.Record(Type.String(), Type.Number())`, map[string]any{"a": "x"}},
	{`Type.Intersect([Type.Object({ a: Type.Number() }), Type.Object({ b: Type.String() })])`, map[string]any{"a": "x"}},
	{`Type.Object({ a: Type.Optional(Type.Union([Type.Null(), Type.Literal(1)])) })`, map[string]any{"a": 2}},
	{`Type.Tuple([Type.String(), Type.Number()])`, []any{1}},
	{`Type.Array(Type.String(), { uniqueItems: true })`, []any{"a", "a"}},
	{`Type.Object({ a: Type.Number({ exclusiveMaximum: 3, multipleOf: 2 }) })`, map[string]any{"a": 3}},
	// A closed tuple stops at its first failing extra element (additionalItems.mjs uses Every).
	{`Type.Tuple([Type.String()])`, []any{"a", 1.0, true}},
	{`Type.Object({ t: Type.Tuple([Type.String(), Type.Number()]) })`, map[string]any{"t": []any{"a", "b", "c", "d"}}},
	// Review of f89c8aecc6 (rev-complexity-audit-3): maxErrors, nested arrays, union of object and array, literals, integer
	// record keys, several string constraints, null and boolean, contradictory bounds, nested closed objects, unevaluated
	// properties and short tuples with defaults.
	{`Type.Object({ a: Type.Number(), b: Type.Number(), c: Type.Number(), d: Type.Number(), e: Type.Number(), f: Type.Number(), g: Type.Number(), h: Type.Number(), i: Type.Number(), j: Type.Number() })`, map[string]any{"a": "x", "b": "x", "c": "x", "d": "x", "e": "x", "f": "x", "g": "x", "h": "x", "i": "x", "j": "x"}},
	{`Type.Array(Type.Object({ id: Type.Integer(), tags: Type.Array(Type.String({ maxLength: 2 })) }))`, []any{map[string]any{"id": 1.5, "tags": []any{"abc", 1}}, map[string]any{}}},
	{`Type.Union([Type.Object({ a: Type.String() }), Type.Array(Type.Number())])`, []any{"x"}},
	{`Type.Object({ a: Type.Literal(3) })`, map[string]any{"a": 4}},
	{`Type.Object({ a: Type.Union([Type.Literal("x"), Type.Literal("y")]) })`, map[string]any{"a": "z"}},
	{`Type.Record(Type.Integer(), Type.String())`, map[string]any{"1": 2, "x": "y"}},
	{`Type.Object({ a: Type.String({ minLength: 3, pattern: "^z", format: "uri" }) })`, map[string]any{"a": "y"}},
	{`Type.Object({ a: Type.Boolean(), b: Type.Null() })`, map[string]any{"a": nil, "b": false}},
	{`Type.Array(Type.Number(), { minItems: 3, maxItems: 1 })`, []any{"a", "b"}},
	{`Type.Object({ o: Type.Object({ p: Type.Number() }, { additionalProperties: false }) }, { additionalProperties: false })`, map[string]any{"o": map[string]any{"p": "x", "q": 1}, "z": 1}},
	{`Type.Intersect([Type.Object({ a: Type.Number() }), Type.Object({ b: Type.String() })], { unevaluatedProperties: false })`, map[string]any{"a": 1, "b": "x", "c": 2}},
	{`Type.Tuple([Type.Number(), Type.Object({ k: Type.String({ default: "d" }) })])`, []any{1}},
	{`Type.Intersect([Type.Object({ a: Type.Number() }), Type.Object({ b: Type.String() })], { unevaluatedProperties: Type.Boolean() })`, map[string]any{"a": 1, "b": "x", "c": true}},
}

func TestValueMatchesTypeboxOnCases(t *testing.T) {
	payload, err := json.Marshal(valueCases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/value_oracle.mjs", testenv.ModuleRoot(t), pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("typebox oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var answers []valueAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	for index, answer := range answers {
		input := valueCases[index].Value
		var value any
		if input != absent {
			value = Plain(input)
		}
		if got := Check(answer.Schema, value); got != answer.Check {
			t.Errorf("case %d %s: Check %v, typebox %v", index, valueCases[index].Schema, got, answer.Check)
		}
		if got := Errors(answer.Schema, value); !reflect.DeepEqual(got, answer.Errors) && (len(got) > 0 || len(answer.Errors) > 0) {
			t.Errorf("case %d %s: errors\n  go %+v\n  tb %+v", index, valueCases[index].Schema, got, answer.Errors)
		}
		clean := Clean(answer.Schema, Clone(value))
		if answer.Clean == absent {
			answer.Clean = nil
		}
		if !reflect.DeepEqual(Plain(clean), answer.Clean) {
			t.Errorf("case %d %s: Clean %#v, typebox %#v", index, valueCases[index].Schema, Plain(clean), answer.Clean)
		}
		defaulted := Default(answer.Schema, Clone(value))
		if input == absent {
			if defaulted = defaultValue(answer.Schema, nil, true); defaulted == (undefined{}) {
				defaulted = nil
			}
		}
		if answer.Default == absent {
			answer.Default = nil
		}
		if !reflect.DeepEqual(Plain(defaulted), answer.Default) {
			t.Errorf("case %d %s: Default %#v, typebox %#v", index, valueCases[index].Schema, Plain(defaulted), answer.Default)
		}
	}
}
