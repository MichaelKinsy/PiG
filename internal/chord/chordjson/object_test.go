package chordjson

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"
)

// JavaScript property order (ECMA-262 OrdinaryOwnPropertyKeys): array-index keys ascending, then the other keys in creation order.
// Assigning an existing key keeps its place; deleting it and assigning it again moves it to the end. Node 24 gives, for
// o = {b: 1, 10: 2, a: 3, 2: 4}; o.b = 5; delete o.a; o.a = 6: Object.keys(o) = ["2", "10", "b", "a"].
func TestObjectKeepsJavaScriptPropertyOrder(t *testing.T) {
	object := ObjectOf("b", 1.0, "10", 2.0, "a", 3.0, "2", 4.0)
	object.Set("b", 5.0)
	object.Delete("a")
	object.Set("a", 6.0)
	if got, want := object.Keys(), []string{"2", "10", "b", "a"}; !slices.Equal(got, want) {
		t.Fatalf("Keys = %v, want %v", got, want)
	}
	if got := mustJSONText(t, object); got != `{"2":4,"10":2,"b":5,"a":6}` {
		t.Fatalf("Marshal = %s", got)
	}
}

// The order holds past the linear-scan size and across compaction of deleted entries.
func TestObjectOrderSurvivesIndexingAndCompaction(t *testing.T) {
	object := NewObject(0)
	var want []string
	for at := range 40 {
		key := "k" + strconv.Itoa(39-at)
		object.Set(key, float64(at))
		want = append(want, key)
	}
	for at := 0; at < 30; at++ {
		if !object.Delete(want[at]) {
			t.Fatalf("Delete(%s) missed", want[at])
		}
	}
	want = want[30:]
	object.Set(want[0], "again")
	object.Set("k0b", true)
	want = append(want, "k0b")
	if got := object.Keys(); !slices.Equal(got, want) {
		t.Fatalf("Keys = %v, want %v", got, want)
	}
	if object.Len() != len(want) || object.Value(want[0]) != "again" || object.Has(want[0]+"x") {
		t.Fatalf("Len %d, Value %v", object.Len(), object.Value(want[0]))
	}
}

// Decode reports encoding/json's error for invalid text, and round-trips through json.Unmarshal into an Object.
func TestDecodeErrorsAndUnmarshal(t *testing.T) {
	for _, text := range []string{``, `{`, `{"a" 1}`, `[1,]`, `01`, `1.`, `"\x01"`, `{"a":1}x`, `nul`, `-`, `1e`} {
		_, got := Decode([]byte(text))
		var reference any
		want := json.Unmarshal([]byte(text), &reference)
		if got == nil || want == nil || got.Error() != want.Error() {
			t.Fatalf("Decode(%q) = %v, encoding/json says %v", text, got, want)
		}
	}
	var object Object
	if err := json.Unmarshal([]byte(`{"z":{"y":1,"x":[{"w":2,"v":3}]},"a":"\u00e9\n"}`), &object); err != nil {
		t.Fatal(err)
	}
	if got := mustJSONText(t, &object); got != `{"z":{"y":1,"x":[{"w":2,"v":3}]},"a":"é\n"}` {
		t.Fatalf("round trip = %s", got)
	}
	if err := json.Unmarshal([]byte(`[1]`), &object); err == nil {
		t.Fatal("array unmarshaled into an Object")
	}
}

// upstream: JSON.stringify({k: "\ud800x", "\udc01": ["a\udfff"]}) is {"k":"\ud800x","\udc01":["a\udfff"]} (Node). A Go
// string from a JavaScript-semantics decoder holds a lone surrogate as WTF-8, and the object writes it as its escape.
func TestObjectWritesLoneSurrogatesAsJSONStringifyDoes(t *testing.T) {
	object := ObjectOf("k", "\xed\xa0\x80x", "\xed\xb0\x81", []any{"a\xed\xbf\xbf"})
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"k":"\ud800x","\udc01":["a\udfff"]}`; string(encoded) != want {
		t.Fatalf("got %s, want %s", encoded, want)
	}
}
