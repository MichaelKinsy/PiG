package typebox

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// typeboxAnswers runs TypeBox's Value functions over cases (testdata/value_oracle.mjs).
func typeboxAnswers(t testing.TB, cases []valueCase) []valueAnswer {
	t.Helper()
	payload, err := json.Marshal(cases)
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
	if len(answers) != len(cases) {
		t.Fatalf("typebox answered %d of %d cases", len(answers), len(cases))
	}
	return answers
}

// D645, the Value.Check row. TypeBox tests an object's properties with `key in value` (Guard.HasPropertyKey; an own-property test only for __proto__, constructor
// and prototype), so for an object that omits an optional property named toString, hasOwnProperty or valueOf it finds the function Object.prototype holds, checks
// it against the property's schema and the object fails. PiG checks own members only: the omitted property is absent, as for any other name. Each row holds
// TypeBox's answer for the object without the property, which the pinned TypeBox must give in this run; with the property present both sides accept.
func TestAnOmittedPropertyNamedLikeAnObjectPrototypeMemberIsAbsent(t *testing.T) {
	rows := []struct {
		name    string
		typebox bool
	}{
		{"toString", false},
		{"hasOwnProperty", false},
		{"valueOf", false},
		{"constructor", true},
		{"__proto__", true},
		{"nosuchname", true},
	}
	var cases []valueCase
	for _, row := range rows {
		schema := `Type.Object({ [` + strconv.Quote(row.name) + `]: Type.Optional(Type.String()) })`
		cases = append(cases, valueCase{schema, map[string]any{}}, valueCase{schema, map[string]any{row.name: "v"}})
	}
	answers := typeboxAnswers(t, cases)
	for i, row := range rows {
		omitted, present := answers[2*i], answers[2*i+1]
		t.Logf("optional property %s omitted: TypeBox Check %v with %d errors", row.name, omitted.Check, len(omitted.Errors))
		if omitted.Check != row.typebox || (len(omitted.Errors) == 0) != row.typebox {
			t.Errorf("optional property %s omitted: TypeBox answers %v with %d errors, the record says %v", row.name, omitted.Check, len(omitted.Errors), row.typebox)
		}
		if !Check(omitted.Schema, Plain(map[string]any{})) {
			t.Errorf("optional property %s omitted: Check = false, want true", row.name)
		}
		if errors := Errors(omitted.Schema, Plain(map[string]any{})); len(errors) != 0 {
			t.Errorf("optional property %s omitted: errors %+v, want none", row.name, errors)
		}
		if !present.Check || !Check(present.Schema, Plain(map[string]any{row.name: "v"})) {
			t.Errorf("property %s present: TypeBox %v, PiG %v, want both true", row.name, present.Check, Check(present.Schema, Plain(map[string]any{row.name: "v"})))
		}
	}
}

// D645, the Value.Check row, uniqueItems. TypeBox hashes an object's members without the name constructor (InstanceKeys), so two objects that differ only in an
// own member of that name are one item to its uniqueItems. PiG hashes every own member. The last two cases have no such member and are equal.
func TestUniqueItemsCountsAMemberNamedConstructor(t *testing.T) {
	const schema = `Type.Array(Type.Any(), { uniqueItems: true })`
	rows := []struct {
		items        []any
		typebox, pig bool
	}{
		{[]any{map[string]any{"a": 1, "constructor": 1}, map[string]any{"a": 1, "constructor": 2}}, false, true},
		{[]any{map[string]any{"constructor": 1}, map[string]any{}}, false, true},
		{[]any{map[string]any{}, map[string]any{"constructor": 2}, "é"}, false, true},
		{[]any{map[string]any{"constructor": 1}, map[string]any{"constructor": 2}}, false, true},
		{[]any{map[string]any{"a": 1, "constructor": 5}, map[string]any{"a": 1}}, false, true},
		{[]any{map[string]any{"constructor": map[string]any{"constructor": 1}}, map[string]any{}}, false, true},
		{[]any{map[string]any{"a": 1, "constructor": 5}, map[string]any{"a": 2}}, true, true},
		{[]any{map[string]any{"a": 1, "constructor": 1}, map[string]any{"a": 1, "constructor": 1}}, false, false},
		{[]any{map[string]any{"a": 1, "b": 1}, map[string]any{"a": 1, "b": 2}}, true, true},
		{[]any{map[string]any{"a": 1, "b": 1}, map[string]any{"a": 1, "b": 1}}, false, false},
	}
	cases := make([]valueCase, len(rows))
	for i, row := range rows {
		cases[i] = valueCase{schema, row.items}
	}
	for i, answer := range typeboxAnswers(t, cases) {
		row := rows[i]
		encoded, err := json.Marshal(row.items)
		if err != nil {
			t.Fatal(err)
		}
		if answer.Check != row.typebox {
			t.Errorf("uniqueItems of %s: TypeBox answers %v, the record says %v", encoded, answer.Check, row.typebox)
		}
		if got := Check(answer.Schema, Plain(row.items)); got != row.pig {
			t.Errorf("uniqueItems of %s: Check = %v, want %v", encoded, got, row.pig)
		}
		if got := len(Errors(answer.Schema, Plain(row.items))) == 0; got != row.pig {
			t.Errorf("uniqueItems of %s: no errors = %v, want %v", encoded, got, row.pig)
		}
	}
}

func BenchmarkCheckPrototypeNames(b *testing.B) {
	var object, list any
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"toString":{"type":"string"},"a":{"type":"number"}},"required":["a"]}`), &object); err != nil {
		b.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"array","items":{"type":"object"},"uniqueItems":true}`), &list); err != nil {
		b.Fatal(err)
	}
	value := Plain(map[string]any{"a": 1.0})
	items := Plain([]any{map[string]any{"a": 1.0, "constructor": 1.0}, map[string]any{"a": 1.0, "constructor": 2.0}, map[string]any{"b": 1.0}})
	b.ReportAllocs()
	for b.Loop() {
		Check(object, value)
		Check(list, items)
	}
}
