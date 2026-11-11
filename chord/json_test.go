package chord

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// upstream: packages/chord/test/json.test.ts "copies strict JSON without retaining aliases".
func TestCopyJSONDetachesSharedAliases(t *testing.T) {
	shared := delta.JsonObjectOf("value", float64(1))
	input := delta.JsonObjectOf("right", shared, "left", shared)
	copied, err := CopyJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	got := copied.(*delta.JsonObject)
	if text, _ := json.Marshal(got); string(text) != `{"right":{"value":1},"left":{"value":1}}` {
		t.Fatalf("copy = %s, want the input in its key order", text)
	}
	got.Value("left").(*delta.JsonObject).Set("value", float64(2))
	if shared.Value("value") != float64(1) {
		t.Fatal("copy retained an alias of the shared input value")
	}
	if got.Value("right").(*delta.JsonObject).Value("value") != float64(1) {
		t.Fatal("left and right of the copy still alias each other")
	}
}

// upstream: json.test.ts "rejects cycles and non-strict container properties" plus the non-finite number case of copy().
func TestCopyJSONRejectsNonStrictValues(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	cases := map[string]any{
		"cycle":        cyclic,
		"nested +Inf":  map[string]any{"n": math.Inf(1)},
		"NaN in array": []any{math.NaN()},
		"non-JSON go":  map[string]any{"c": make(chan int)},
	}
	for name, value := range cases {
		_, err := CopyJSON(value)
		if _, ok := errors.AsType[*delta.StrictJSONError](err); !ok {
			t.Errorf("%s: err = %v, want *delta.StrictJSONError", name, err)
		}
	}
}

// Go integer kinds normalize to float64, the one JSON number type; CopyJSONObject keeps the object root.
func TestCopyJSONNormalizesNumbersAndKeepsObjectRoot(t *testing.T) {
	copied, err := CopyJSON([]any{1, int64(2), float32(0.5), nil, "s", true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{float64(1), float64(2), float64(0.5), nil, "s", true}; !reflect.DeepEqual(copied, want) {
		t.Fatalf("copy = %#v, want %#v", copied, want)
	}
	object, err := CopyJSONObject(map[string]any{"k": []any{1}})
	if text, _ := json.Marshal(object); err != nil || string(text) != `{"k":[1]}` {
		t.Fatalf("CopyJSONObject = %#v, %v", object, err)
	}
}

// upstream: packages/chord/test/json.test.ts "checks strict JSON without normalizing it". Go has no undefined; a byte
// slice stands for Uint8Array.
func TestIsJsonValueChecksStrictJSONWithoutNormalizing(t *testing.T) {
	if !IsJsonValue(delta.JsonObjectOf("nested", []any{float64(1), true, nil})) {
		t.Fatal("strict JSON was rejected")
	}
	cyclic := []any{nil}
	cyclic[0] = cyclic
	for name, value := range map[string]any{"bytes": []byte{1}, "infinity": math.Inf(1), "nan": math.NaN(), "cycle": cyclic} {
		if IsJsonValue(value) {
			t.Errorf("%s was accepted as strict JSON", name)
		}
	}
}

// upstream: json.test.ts "preserves null prototypes and own __proto__ data properties". Go objects have no prototypes; the
// part with a Go meaning is that a "__proto__" key is an ordinary own key of both object forms and survives a copy.
func TestCopyJSONPreservesOwnProtoKeys(t *testing.T) {
	for name, input := range map[string]any{
		"object": delta.JsonObjectOf("__proto__", delta.JsonObjectOf("safe", true)),
		"map":    map[string]any{"__proto__": map[string]any{"safe": true}},
	} {
		copied, err := CopyJSON(input)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if text, _ := json.Marshal(copied); string(text) != `{"__proto__":{"safe":true}}` {
			t.Errorf("%s: copy = %s, want the __proto__ key kept", name, text)
		}
	}
}

// Intention of packages/chord/test/json.test.ts "optionally omits undefined object properties without normalizing arrays": a
// value that is not JSON never becomes JSON silently; an absent property is dropped only where Pi's option allows, never in an
// array. Go has no undefined, so the PiG form is: a nil member is JSON null and stays (nothing is dropped), and a Go kind JSON
// cannot hold is refused as strict JSON in an object and in an array alike.
func TestCopyJSONKeepsNilAsNullAndRefusesNonJSONKindsInObjectsAndArrays(t *testing.T) {
	copied, err := CopyJSON(map[string]any{"kept": 1, "nothing": nil, "nested": map[string]any{"nothing": nil, "kept": true}})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := copied.(*delta.JsonObject)
	if !ok || object.Len() != 3 {
		t.Fatalf("copied = %#v, want an object with all 3 members (a nil member is null, not omitted)", copied)
	}
	if value, present := object.Get("nothing"); !present || value != nil {
		t.Fatalf("nothing = %v, present %v, want null", value, present)
	}
	array, err := CopyJSON([]any{nil})
	if err != nil || !reflect.DeepEqual(array, []any{nil}) {
		t.Fatalf("[nil] copied = %#v, %v, want [null] kept", array, err)
	}
	for name, value := range map[string]any{"object member": map[string]any{"f": func() {}}, "array element": []any{func() {}}} {
		if _, err := CopyJSON(value); err == nil {
			t.Errorf("%s: a func was copied, want a strict JSON error", name)
		} else if _, ok := errors.AsType[*delta.StrictJSONError](err); !ok {
			t.Errorf("%s: err = %v, want *delta.StrictJSONError", name, err)
		}
	}
}

// Intention of Pi's JsonRepresentation<T> (json.ts:5-17): what crosses as JSON is only strict JSON values. PiG form: whatever
// CopyJSON accepts comes back as a value IsJsonValue accepts, for every Go kind it normalizes.
func TestCopyJSONResultIsAlwaysStrictJSON(t *testing.T) {
	for name, value := range map[string]any{
		"ints and floats": map[string]any{"i": 3, "u": uint8(4), "f": float32(1.5)},
		"slices":          []string{"a", "b"},
		"nested maps":     map[string]any{"m": map[string]int{"x": 1}, "s": []any{nil, true, "t"}},
		"scalars":         "text",
	} {
		copied, err := CopyJSON(value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !IsJsonValue(copied) {
			t.Errorf("%s: %#v is not strict JSON", name, copied)
		}
	}
}
