package orderedjson

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestObjectKeepsKeyOrderAndRawNumbers(t *testing.T) {
	o, err := Parse([]byte(`{"z":1,"a":{"k":[1,2]},"m":12345678901234567890}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(o.Keys(), ","); got != "z,a,m" {
		t.Fatalf("keys = %s", got)
	}
	o.Set("z", json.RawMessage(`2`))
	o.Set("n", json.RawMessage(`null`))
	o.Delete("a")
	out, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"z":2,"m":12345678901234567890,"n":null}` {
		t.Fatalf("out = %s", out)
	}
}

func TestObjectRejectsNonObjectsAndKeepsLastValueAtFirstPosition(t *testing.T) {
	for _, in := range []string{`null`, `[]`, `1`, `"s"`, ``, `{"a":`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) succeeded", in)
		}
	}
	o, err := Parse([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := o.MarshalJSON()
	if string(out) != `{"a":3,"b":2}` {
		t.Fatalf("out = %s", out)
	}
	if IsObject([]byte(` [1]`)) || !IsObject([]byte(` {}`)) {
		t.Fatal("IsObject")
	}
}

// pi: a parsed JSON object enumerates (Object.entries, JSON.stringify) its array-index keys first in ascending order, then its other keys in insertion order.
func TestObjectEnumeratesArrayIndexKeysFirst(t *testing.T) {
	o, err := Parse([]byte(`{"b":1,"10":2,"2":3,"a":4,"01":5,"4294967295":6,"4294967294":7}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2", "10", "4294967294", "b", "a", "01", "4294967295"}
	if got := o.Keys(); !slices.Equal(got, want) {
		t.Fatalf("Keys = %q, want %q", got, want)
	}
	o.Set("5", json.RawMessage("8"))
	o.Set("c", json.RawMessage("9"))
	o.Delete("10")
	o.Set("1", json.RawMessage("0"))
	want = []string{"1", "2", "5", "4294967294", "b", "a", "01", "4294967295", "c"}
	if got := o.Keys(); !slices.Equal(got, want) {
		t.Fatalf("Keys after changes = %q, want %q", got, want)
	}
	encoded, err := json.Marshal(o)
	if err != nil || string(encoded) != `{"1":0,"2":3,"5":8,"4294967294":7,"b":1,"a":4,"01":5,"4294967295":6,"c":9}` {
		t.Fatalf("Marshal = %s, %v", encoded, err)
	}
	if got := o.Clone().Keys(); !slices.Equal(got, want) {
		t.Fatalf("Clone keys = %q, want %q", got, want)
	}
}
