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
