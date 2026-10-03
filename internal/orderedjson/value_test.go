package orderedjson

import (
	"encoding/json"
	"reflect"
	"testing"
)

// JSON.parse keeps an object's members in the order the source wrote them, and JSON.stringify writes them back in that order. A Go map sorts them.
func TestValueKeepsContainersInWrittenOrder(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      any
	}{
		{"object", `{"zeta":1,"alpha":{"yy":2,"bb":3}}`, json.RawMessage(`{"zeta":1,"alpha":{"yy":2,"bb":3}}`)},
		{"array of objects", `[{"qq":1,"aa":2},3]`, json.RawMessage(`[{"qq":1,"aa":2},3]`)},
		{"string", `"text"`, "text"},
		{"number", `1.5`, 1.5},
		{"bool", `false`, false},
		{"null", `null`, nil},
		{"empty", ``, nil},
		{"invalid container", `{"a":`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Value([]byte(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Value(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

// Value owns its bytes: the source buffer is reused by the caller after the call.
func TestValueCopiesItsInput(t *testing.T) {
	source := []byte(`{"b":1,"a":2}`)
	got := Value(source)
	copy(source, `{"x":0,"y":0}`)
	if encoded, err := json.Marshal(got); err != nil || string(encoded) != `{"b":1,"a":2}` {
		t.Fatalf("value after the source changed = %s, %v", encoded, err)
	}
}

type fieldsTarget struct {
	Name    string `json:"name"`
	Details any    `json:"details,omitempty"`
	Data    any    `json:"data,omitempty"`
	Other   any    `json:"other,omitempty"`
}

func TestUnmarshalFieldsKeepsNamedMembersInWrittenOrder(t *testing.T) {
	var target fieldsTarget
	input := `{"name":"n","details":{"zeta":1,"alpha":{"yy":2,"bb":3}},"data":[{"qq":1,"aa":2}],"other":{"z":1,"a":2}}`
	if err := UnmarshalFields([]byte(input), &target, "details", "data"); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	// "other" is not named: encoding/json decodes it into a map, as before.
	want := `{"name":"n","details":{"zeta":1,"alpha":{"yy":2,"bb":3}},"data":[{"qq":1,"aa":2}],"other":{"a":2,"z":1}}`
	if string(encoded) != want {
		t.Fatalf("decoded and encoded = %s, want %s", encoded, want)
	}
	if err := UnmarshalFields([]byte(`{"name":"n","details":null}`), &target, "details"); err != nil || target.Details != nil {
		t.Fatalf("null member = %#v, %v, want nil", target.Details, err)
	}
	if err := UnmarshalFields([]byte(`{"name":1}`), &target, "details"); err == nil {
		t.Fatal("a member of the wrong type decoded without an error")
	}
}

func TestMapReadsAnObjectHeldAsAMapOrAsRawJSON(t *testing.T) {
	for _, value := range []any{map[string]any{"b": 1.0, "a": "x"}, json.RawMessage(`{"b":1,"a":"x"}`)} {
		got, ok := Map(value)
		if !ok || !reflect.DeepEqual(got, map[string]any{"b": 1.0, "a": "x"}) {
			t.Errorf("Map(%#v) = %#v, %v", value, got, ok)
		}
	}
	for _, value := range []any{nil, "x", 1.0, []any{1.0}, json.RawMessage(`[1]`), json.RawMessage(`bad`)} {
		if got, ok := Map(value); ok || got != nil {
			t.Errorf("Map(%#v) = %#v, %v, want no object", value, got, ok)
		}
	}
}

func TestMarshalMapWritesNamedKeysFirstThenTheRestSorted(t *testing.T) {
	encoded, err := MarshalMap(map[string]any{"z": 1, "b": map[string]any{"y": 1, "x": 2}, "a": "s", "role": "custom", "missing": nil}, "role", "a", "absent", "role")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"role":"custom","a":"s","b":{"x":2,"y":1},"missing":null,"z":1}`; string(encoded) != want {
		t.Fatalf("MarshalMap = %s, want %s", encoded, want)
	}
	if encoded, err := MarshalMap(nil); err != nil || string(encoded) != `{}` {
		t.Fatalf("MarshalMap(nil) = %s, %v", encoded, err)
	}
}

func TestMarshalArrayOrdersMapItems(t *testing.T) {
	encoded, err := MarshalArray([]any{map[string]any{"text": "a", "type": "text"}, "s", 1.5, nil}, "type")
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"type":"text","text":"a"},"s",1.5,null]`; string(encoded) != want {
		t.Fatalf("MarshalArray = %s, want %s", encoded, want)
	}
	if encoded, err := MarshalArray(nil); err != nil || string(encoded) != `[]` {
		t.Fatalf("MarshalArray(nil) = %s, %v", encoded, err)
	}
}

func TestMarshalDoesNotEscapeHTML(t *testing.T) {
	encoded, err := Marshal(map[string]any{"html": `<a href="x">y & z</a>`})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"html":"<a href=\"x\">y & z</a>"}`; string(encoded) != want {
		t.Fatalf("Marshal = %s, want %s", encoded, want)
	}
}

// A value an SDK other than Node encoded reaches the host in that language's JSON form: Python writes `1.0`, `\u00e9` and `\u2028` escapes and spaces, the Go SDK `\u003c`, and a dict or map keeps integer-like keys where it put them. Pi holds the JavaScript object and writes JSON.stringify of it, so the host keeps what JSON.parse then JSON.stringify make of the text: the written member order with integer-like keys first and ascending, numbers and strings in JSON.stringify's form.
func TestValueHoldsWhatJSONStringifyWrites(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"python separators and floats", `{"zeta": 1.0, "alpha": {"yy": 2.50, "bb": -0.0}, "big": 1e21, "small": 1E-7}`, `{"zeta":1,"alpha":{"yy":2.5,"bb":0},"big":1e+21,"small":1e-7}`},
		{"escaped strings", `{"name":"caf\u00e9","html":"\u003cb\u003e \u0026","line":"a\u2028b","slash":"a\/b","ctl":"\u0001"}`, "{\"name\":\"café\",\"html\":\"<b> &\",\"line\":\"a\u2028b\",\"slash\":\"a/b\",\"ctl\":\"\\u0001\"}"},
		{"lone surrogate", `["\uD800x"]`, `["\ud800x"]`},
		{"integer-like keys", `{"b":1,"10":2,"2":3,"-1":4,"01":5,"4294967295":6}`, `{"2":3,"10":2,"b":1,"-1":4,"01":5,"4294967295":6}`},
		{"repeated key", `{"a":1,"b":2,"a":3}`, `{"a":3,"b":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Value([]byte(tc.raw)).(json.RawMessage)
			if !ok || string(got) != tc.want {
				t.Fatalf("Value(%s) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}
