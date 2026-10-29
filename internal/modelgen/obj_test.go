package modelgen

// Tests of Obj, the JavaScript plain object the catalog is built from. No upstream test: these pin the JavaScript semantics
// (insertion order, spread, integer-like keys first, JSON.stringify and JSON.parse) the generated JSON depends on.

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func keysOf(t *testing.T, object *Obj, want ...string) {
	t.Helper()
	if got := object.Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
}

func TestObjKeepsInsertionOrder(t *testing.T) {
	object := O("b", 1, "a", 2, "c", 3)
	keysOf(t, object, "b", "a", "c")
	if object.Len() != 3 {
		t.Fatalf("Len = %d", object.Len())
	}
	// Set keeps the position of an existing key and takes the new value.
	object.Set("b", "x")
	keysOf(t, object, "b", "a", "c")
	if object.Str("b") != "x" {
		t.Fatalf("b = %v", object.Str("b"))
	}
	object.Set("d", true)
	keysOf(t, object, "b", "a", "c", "d")
}

func TestSpreadKeepsFirstPositionAndLastValue(t *testing.T) {
	merged := Spread(O("a", 1, "b", 2), nil, O("c", 3, "a", 9), O("b", nil))
	keysOf(t, merged, "a", "b", "c")
	requireJSON(t, merged, `{"a":9,"b":null,"c":3}`)
	// Spreading undefined contributes nothing, and the result is a new object whose members are shared.
	if empty := Spread(nil, nil); empty == nil || empty.Len() != 0 {
		t.Fatalf("Spread(nil, nil) = %v", empty)
	}
	nested := O("k", 1)
	source := O("n", nested)
	copied := Spread(source)
	copied.Set("m", 2)
	if source.Has("m") {
		t.Fatal("Spread aliased the object")
	}
	if copied.Object("n") != nested {
		t.Fatal("Spread cloned a member; JavaScript shares it")
	}
}

func TestObjIntegerLikeKeysComeFirstAscending(t *testing.T) {
	// Canonical array indices (0 to 2^32-2) enumerate first in ascending order; "-1", "01", "1.5" and 2^32-1 are ordinary keys.
	object := O("b", 1, "10", 2, "2", 3, "a", 4, "-1", 5, "01", 6, "4294967295", 7, "4294967294", 8, "1.5", 9, "0", 10)
	keysOf(t, object, "0", "2", "10", "4294967294", "b", "a", "-1", "01", "4294967295", "1.5")
	if got := string(Stringify(object, "")); got != `{"0":10,"2":3,"10":2,"4294967294":8,"b":1,"a":4,"-1":5,"01":6,"4294967295":7,"1.5":9}` {
		t.Fatalf("Stringify = %s", got)
	}
	// A spread orders them the same way.
	keysOf(t, Spread(O("x", 1), O("3", 1, "1", 1)), "1", "3", "x")
}

func TestObjDelete(t *testing.T) {
	object := O("a", 1, "b", 2, "c", 3)
	object.Delete("b")
	object.Delete("missing")
	keysOf(t, object, "a", "c")
	if object.Has("b") || object.Len() != 2 {
		t.Fatalf("object = %s", Stringify(object, ""))
	}
	// A deleted key is appended again, as in JavaScript.
	object.Set("b", 5)
	keysOf(t, object, "a", "c", "b")
	var absent *Obj
	if absent.Len() != 0 || absent.Has("a") || absent.Str("a") != "" || absent.Object("a") != nil {
		t.Fatal("a nil object is empty")
	}
}

func TestObjSetNormalizesAndRejectsUnsupportedValues(t *testing.T) {
	object := O("n", 3, "s", []string{"a", "b"})
	if object.Num("n") != 3 {
		t.Fatalf("int stored as %v", object.member("n"))
	}
	if got := object.StringList("s"); !slices.Equal(got, []string{"a", "b"}) || !object.Includes("s", "b") || object.Includes("s", "c") {
		t.Fatalf("strings = %v", got)
	}
	for name, value := range map[string]any{"int64": int64(1), "map": map[string]any{}, "nil object": (*Obj)(nil)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Set accepted the value", name)
				}
			}()
			NewObj().Set("k", value)
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("O accepted an odd argument count")
			}
		}()
		odd := []any{"a"}
		O(odd...) //nolint:staticcheck // SA5012: the panic on an odd argument count is what the test asserts
	}()
}

func TestObjCloneIsDeep(t *testing.T) {
	original := parseObj(t, `{"a":{"b":[1,{"c":"d"}]},"e":[ "x" ]}`)
	clone := original.Clone()
	requireJSON(t, clone, string(Stringify(original, "")))
	clone.Object("a").Set("z", 1)
	clone.Object("a").List("b")[1].(*Obj).Set("c", "changed")
	clone.List("e")[0] = "y"
	clone.Delete("e")
	requireJSON(t, original, `{"a":{"b":[1,{"c":"d"}]},"e":["x"]}`)

	models := []*Obj{O("id", "a", "n", O("k", 1))}
	cloned := CloneAll(models)
	cloned[0].Object("n").Set("k", 2)
	if models[0].Object("n").Num("k") != 1 {
		t.Fatal("CloneAll aliased a member")
	}
}

func TestStringifyMatchesJSONStringify(t *testing.T) {
	text := "q\" b\\ n\n r\r t\t b\b f\f c1\x01 c1f\x1f del\x7f é 😀 \u2028 </script> &"
	value := O(
		"s", text,
		"n", 1.5,
		"i", 100,
		"negzero", math.Copysign(0, -1),
		"big", 1e21,
		"small", 1e-7,
		"whole", 123456789012.0,
		"inf", math.Inf(1),
		"t", true,
		"f", false,
		"z", nil,
		"list", []any{},
		"empty", NewObj(),
		"arr", []any{1.0, "x", O("k", nil), []any{}, []any{true}},
		"é\n", "key escapes",
	)
	compact := `{"s":"q\" b\\ n\n r\r t\t b\b f\f c1\u0001 c1f\u001f del` + "\x7f" + ` é 😀 ` + "\u2028" + ` </script> &","n":1.5,"i":100,"negzero":0,"big":1e+21,"small":1e-7,"whole":123456789012,"inf":null,"t":true,"f":false,"z":null,"list":[],"empty":{},"arr":[1,"x",{"k":null},[],[true]],"é\n":"key escapes"}`
	if got := string(Stringify(value, "")); got != compact {
		t.Fatalf("compact:\n%s\nwant\n%s", got, compact)
	}
	if got := string(Stringify(value, "")); got != string(mustMarshal(t, value)) {
		t.Fatalf("MarshalJSON differs from compact Stringify: %s", mustMarshal(t, value))
	}
	indented := "{\n  \"a\": [\n    1,\n    {\n      \"k\": null,\n      \"e\": {}\n    },\n    []\n  ],\n  \"b\": \"x\",\n  \"c\": {\n    \"d\": [\n      \"y\"\n    ]\n  },\n  \"l\": []\n}"
	nested := O("a", []any{1.0, O("k", nil, "e", NewObj()), []any{}}, "b", "x", "c", O("d", []any{"y"}), "l", []any{})
	if got := string(Stringify(nested, "  ")); got != indented {
		t.Fatalf("indented:\n%s\nwant\n%s", got, indented)
	}
	if got := string(Stringify(NewObj(), "  ")); got != "{}" {
		t.Fatalf("empty object = %s", got)
	}
	if got := string(Stringify([]any{}, "  ")); got != "[]" {
		t.Fatalf("empty array = %s", got)
	}
	if got := string(Stringify("a\u0000", "  ")); got != `"a\u0000"` {
		t.Fatalf("string = %s", got)
	}
	if got := string(Stringify(nested, "\t")); !strings.HasPrefix(got, "{\n\t\"a\": [\n\t\t1,") {
		t.Fatalf("tab indent = %q", got)
	}
}

func mustMarshal(t *testing.T, object *Obj) []byte {
	t.Helper()
	data, err := object.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseJSON(t *testing.T) {
	t.Run("keeps document order", func(t *testing.T) {
		object := parseObj(t, `{"b":1,"a":{"z":1,"y":[{"q":1,"p":2}]},"c":[]}`)
		keysOf(t, object, "b", "a", "c")
		if got := string(Stringify(object, "")); got != `{"b":1,"a":{"z":1,"y":[{"q":1,"p":2}]},"c":[]}` {
			t.Fatalf("round trip = %s", got)
		}
		if object.List("c") == nil {
			t.Fatal("an empty array parses as a non-nil list")
		}
	})
	t.Run("integer-like keys enumerate first", func(t *testing.T) {
		keysOf(t, parseObj(t, `{"b":1,"2":2,"1":3}`), "1", "2", "b")
	})
	t.Run("a repeated key keeps its first position and takes its last value", func(t *testing.T) {
		object := parseObj(t, `{"a":1,"b":2,"a":3,"c":{"x":1,"x":{"y":1,"y":2}}}`)
		keysOf(t, object, "a", "b", "c")
		requireJSON(t, object, `{"a":3,"b":2,"c":{"x":{"y":2}}}`)
	})
	t.Run("numbers are JavaScript numbers", func(t *testing.T) {
		list := parse(t, `[0.1, 1E2, -0, 12345678901234567890, 1e400, 5e-324, 1.0, 0.000001, 1e21, 9007199254740993]`).([]any)
		want := []float64{0.1, 100, math.Copysign(0, -1), 12345678901234567890, math.Inf(1), 5e-324, 1, 0.000001, 1e21, 9007199254740992}
		for i, number := range list {
			if got, ok := number.(float64); !ok || math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Errorf("number %d = %v, want %v", i, number, want[i])
			}
		}
		if got := string(Stringify(list, "")); got != `[0.1,100,0,12345678901234567000,null,5e-324,1,0.000001,1e+21,9007199254740992]` {
			t.Fatalf("Stringify = %s", got)
		}
	})
	t.Run("scalars", func(t *testing.T) {
		if got := parse(t, `null`); got != nil {
			t.Fatalf("null = %v", got)
		}
		if got := parse(t, ` "a\u00e9\n" `); got != "a\u00e9\n" {
			t.Fatalf("string = %q", got)
		}
		if got := parse(t, `true`); got != true {
			t.Fatalf("true = %v", got)
		}
	})
	t.Run("rejects invalid input", func(t *testing.T) {
		for _, text := range []string{``, `{`, `{"a":}`, `[1,]`, `{"a":1} 2`, `[1] []`, `{a:1}`, `nul`} {
			if value, err := ParseJSON([]byte(text)); err == nil {
				t.Errorf("ParseJSON(%q) = %v, want an error", text, value)
			}
		}
	})
}
