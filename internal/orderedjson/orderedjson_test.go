package orderedjson

import (
	"encoding/json"
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
