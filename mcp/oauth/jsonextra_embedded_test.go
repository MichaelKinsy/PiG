package oauth

import (
	"encoding/json"
	"testing"
)

type extraInner struct {
	A string `json:"a"`
}

type extraOuter struct {
	extraInner
	B string `json:"b"`
}

// The extra members of an OAuth metadata object are those that no known field claims, and the known fields include the ones of an embedded struct
// (the metadata types embed the client metadata). An extra named like an embedded field must therefore not be written a second time, and a
// genuinely unknown member is kept, in sorted order, after the known ones.
// Invariant: the doc comments of knownKeys and marshalWithExtra (upstream `[key: string]: unknown` index signatures keep only unknown keys).
func TestMarshalWithExtraTreatsEmbeddedStructFieldsAsKnown(t *testing.T) {
	known := extraOuter{extraInner{"x"}, "y"}
	extra := map[string]json.RawMessage{"a": json.RawMessage(`"dup"`), "z": json.RawMessage(`1`), "m": json.RawMessage(`true`)}
	got, err := marshalWithExtra(known, extra)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"x","b":"y","m":true,"z":1}`; string(got) != want {
		t.Errorf("marshalWithExtra = %s, want %s", got, want)
	}
}

// The reverse direction: members that an embedded struct's fields claim fill those fields and are not returned as extras.
func TestUnmarshalWithExtraClaimsEmbeddedStructMembers(t *testing.T) {
	var known extraOuter
	extra, err := unmarshalWithExtra([]byte(`{"a":"1","b":"2","q":3}`), &known)
	if err != nil {
		t.Fatal(err)
	}
	if known.A != "1" || known.B != "2" {
		t.Errorf("known = %+v, want a=1 b=2", known)
	}
	if len(extra) != 1 || string(extra["q"]) != "3" {
		t.Errorf("extra = %v, want only q", extra)
	}
}
