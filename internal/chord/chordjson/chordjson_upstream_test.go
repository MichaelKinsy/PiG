package chordjson

import (
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"testing"
)

// Ports packages/chord/test/json.test.ts. JavaScript's undefined, Uint8Array, sparse arrays, array subclasses, accessors, hidden and symbol properties and null prototypes do not exist in Go; the invalid shapes a Go value can take (typed slices and maps, structs, pointers, functions, channels, non-finite numbers, cycles, nil objects) take their place.

type unsupported struct{ A int }

func TestIsValue(t *testing.T) {
	t.Run("checks strict JSON without normalizing it", func(t *testing.T) {
		if !IsValue(map[string]any{"nested": []any{1.0, true, nil}}) {
			t.Fatal("nested value rejected")
		}
		if IsValue([]byte{1}) {
			t.Fatal("byte slice accepted")
		}
		if IsValue(math.Inf(1)) || IsValue(math.NaN()) {
			t.Fatal("non-finite number accepted")
		}
		cyclic := map[string]any{}
		cyclic["self"] = cyclic
		if IsValue(cyclic) {
			t.Fatal("cycle accepted")
		}
		cyclicArray := []any{nil}
		cyclicArray[0] = cyclicArray
		if IsValue(cyclicArray) {
			t.Fatal("array cycle accepted")
		}
	})
}

func TestCopy(t *testing.T) {
	t.Run("copies strict JSON without retaining aliases", func(t *testing.T) {
		shared := map[string]any{"value": 1.0}
		input := map[string]any{"left": shared, "right": shared}
		copied, err := Copy(input)
		if err != nil {
			t.Fatal(err)
		}
		out := copied.(*Object)
		if !reflect.DeepEqual(plain(copied), input) {
			t.Fatalf("copy = %v", copied)
		}
		left, right := out.Value("left").(*Object), out.Value("right").(*Object)
		left.Set("value", 9.0)
		if shared["value"] != 1.0 || right.Value("value") != 1.0 {
			t.Fatal("copy aliases the source or its sibling")
		}
		out.Set("extra", true)
		if _, present := input["extra"]; present {
			t.Fatal("copy aliases the root")
		}
	})

	t.Run("normalizes Go numbers to float64", func(t *testing.T) {
		copied, err := Copy(map[string]any{"i": 3, "u": uint8(4), "f": float32(0.5)})
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"i": 3.0, "u": 4.0, "f": 0.5}; !reflect.DeepEqual(plain(copied), want) {
			t.Fatalf("copy = %#v", copied)
		}
	})

	t.Run("preserves own __proto__ data properties", func(t *testing.T) {
		copied, err := Copy(map[string]any{"__proto__": map[string]any{"safe": true}})
		if err != nil {
			t.Fatal(err)
		}
		inner, present := copied.(*Object).Get("__proto__")
		if !present || !reflect.DeepEqual(plain(inner), map[string]any{"safe": true}) {
			t.Fatalf("copy = %#v", copied)
		}
	})

	t.Run("rejects cycles and non-strict container properties", func(t *testing.T) {
		cyclic := map[string]any{}
		cyclic["self"] = cyclic
		cyclicArray := []any{nil}
		cyclicArray[0] = cyclicArray
		var nilObject map[string]any
		for name, invalid := range map[string]any{
			"cycle": cyclic, "array cycle": cyclicArray, "typed slice": []int{1}, "typed map": map[string]int{"a": 1},
			"struct": unsupported{1}, "pointer": &unsupported{1}, "function": func() {}, "channel": make(chan int),
			"nan": math.NaN(), "infinity": math.Inf(-1), "nil object": nilObject, "nested": []any{map[string]any{"bad": unsupported{}}},
		} {
			if IsValue(invalid) {
				t.Fatalf("%s: IsValue accepted it", name)
			}
			_, err := Copy(invalid)
			if err == nil || !regexp.MustCompile("strict JSON").MatchString(err.Error()) {
				t.Fatalf("%s: Copy error = %v", name, err)
			}
			if Validate(invalid) == nil {
				t.Fatalf("%s: Validate accepted it", name)
			}
		}
	})
}

func TestStored(t *testing.T) {
	type record struct {
		Name    string `json:"name"`
		Omitted string `json:"omitted,omitempty"`
		Count   int    `json:"count"`
	}
	stored, err := Stored(&record{Name: "a", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"name": "a", "count": 2.0}; !reflect.DeepEqual(plain(stored), want) {
		t.Fatalf("stored = %#v", stored)
	}
	if _, err := Stored(make(chan int)); err == nil {
		t.Fatal("channel stored")
	}
}

func TestOmitsUnsetObjectEntriesWithoutNormalizingArrays(t *testing.T) {
	// Equivalence port of json.test.ts:28 "optionally omits undefined object properties without normalizing arrays". Go
	// has no undefined: an unset object entry is a nil pointer or omitempty field, Copy is copyJson without the option
	// (it rejects such a value as not strict JSON), and Stored is copyJson with omitUndefinedProperties (it omits the
	// unset entries). The invariant is that omission drops only unset object entries, at every depth, and never removes
	// or rewrites an array element, a nil (null) array element, or a null object value. Expected text is Pi 1.0.0
	// copyJson output under Node 24 for the same inputs: {"kept":1,"nested":{"kept":true}}, [null],
	// {"a":null,"b":[null,1]} and [{"kept":true}].
	type nested struct {
		Omitted *bool `json:"omitted,omitempty"`
		Kept    bool  `json:"kept"`
	}
	type input struct {
		Kept    int     `json:"kept"`
		Omitted *int    `json:"omitted,omitempty"`
		Nested  *nested `json:"nested"`
	}
	value := input{Kept: 1, Nested: &nested{Kept: true}}
	if _, err := Copy(value); err == nil || !regexp.MustCompile(`strict JSON`).MatchString(err.Error()) {
		t.Fatalf("Copy of a value with unset entries: %v, want a strict JSON error", err)
	}
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"unset entries at every depth", value, `{"kept":1,"nested":{"kept":true}}`},
		{"a nil array element stays", []*int{nil}, `[null]`},
		{"null object values and array elements stay", map[string]any{"a": nil, "b": []any{nil, 1}}, `{"a":null,"b":[null,1]}`},
		{"unset entries inside array elements", []nested{{Kept: true}}, `[{"kept":true}]`},
	} {
		stored, err := Stored(test.value)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got := mustJSONText(t, stored); got != test.want {
			t.Fatalf("%s: %s, want Pi's %s", test.name, got, test.want)
		}
	}
	for _, kept := range []any{[]any{nil}, map[string]any{"a": nil, "b": []any{nil, 1.0}}} {
		copied, err := Copy(kept)
		if err != nil || mustJSONText(t, copied) != mustJSONText(t, kept) {
			t.Fatalf("Copy(%v) = %v, %v; a null entry is data, not an unset entry", kept, copied, err)
		}
	}
}

// plain converts *Object values to map[string]any for comparison with Go literals.
func plain(value any) any {
	switch typed := value.(type) {
	case *Object:
		out := make(map[string]any, typed.Len())
		for key, item := range typed.All() {
			out[key] = plain(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for at, item := range typed {
			out[at] = plain(item)
		}
		return out
	}
	return value
}

func mustJSONText(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
