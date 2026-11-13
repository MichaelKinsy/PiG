package orderedjson

import (
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// JSON.stringify writes a member whose value is null and leaves out one whose value is undefined, so a decode that feeds a session entry keeps the two apart:
// null is Null and an absent member is nil.
func TestNullMemberStaysApartFromAbsent(t *testing.T) {
	if !IsNull(Null()) || !IsNull(json.RawMessage(" null\n")) || IsNull(nil) || IsNull(json.RawMessage(`{}`)) || IsNull("null") {
		t.Fatal("IsNull does not tell a raw JSON null from other values")
	}
	for _, tc := range []struct {
		raw          string
		null, absent bool
	}{
		{"null", true, false}, {" null ", true, false}, {"", false, true}, {"{", false, true},
		{`"null"`, false, false}, {"0", false, false}, {"false", false, false}, {`{"a":null}`, false, false}, {"[null]", false, false},
	} {
		got := Member([]byte(tc.raw))
		if IsNull(got) != tc.null || (got == nil) != tc.absent {
			t.Errorf("Member(%q) = %#v, want null %t, absent %t", tc.raw, got, tc.null, tc.absent)
		}
	}
	type payload struct {
		Name    string `json:"name"`
		Data    any    `json:"data,omitempty"`
		Details any    `json:"details,omitempty"`
	}
	for _, tc := range []struct{ text, want string }{
		{`{"name":"n","data":null}`, `{"name":"n","data":null}`},
		{`{"name":"n"}`, `{"name":"n"}`},
		{`{"name":"n","data":{"b":1,"a":2},"details":null}`, `{"name":"n","data":{"b":1,"a":2},"details":null}`},
		{`{"name":"n","data":0,"details":""}`, `{"name":"n","data":0,"details":""}`},
		{`{"name":"n","data":false,"details":[]}`, `{"name":"n","data":false,"details":[]}`},
		// Of two members with one name the last counts, as JSON.parse keeps it.
		{`{"name":"n","data":null,"data":1}`, `{"name":"n","data":1}`},
		{`{"name":"n","data":1,"data":null}`, `{"name":"n","data":null}`},
		// A key that matches a member only case-insensitively takes the three decodes: encoding/json reads it into the field, and a null leaves the field nil.
		{`{"name":"n","Data":null,"details":null}`, `{"name":"n","details":null}`},
	} {
		var decoded payload
		if err := UnmarshalFields([]byte(tc.text), &decoded, "data", "details"); err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		encoded, err := Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.want {
			t.Errorf("UnmarshalFields(%s) encodes as %s, want %s", tc.text, encoded, tc.want)
		}
	}
}

func TestIsNullMember(t *testing.T) {
	for _, tc := range []struct {
		data, name string
		want       bool
	}{
		{`{"a":null}`, "a", true},
		{`{"a" : null }`, "a", true},
		{`{"a":1,"b":null}`, "a", false},
		{`{"a":1,"b":null}`, "b", true},
		{`{"a":{"a":null}}`, "a", false},
		{`{"a":"null"}`, "a", false},
		{`{"a":null,"a":1}`, "a", false},
		{`{"a":1,"a":null}`, "a", true},
		{`{"A":null}`, "a", false},
		{`{"\u0061":null}`, "a", true},
		{`[null]`, "a", false},
		{`null`, "a", false},
		{`{}`, "a", false},
	} {
		if got := IsNullMember([]byte(tc.data), tc.name); got != tc.want {
			t.Errorf("IsNullMember(%s, %q) = %t, want %t", tc.data, tc.name, got, tc.want)
		}
	}
	// The session decodes every compaction, branch summary and label entry through it.
	for _, record := range []string{
		`{"type":"compaction","id":"aaaaaaaa","parentId":"bbbbbbbb","timestamp":"2026-01-01T00:00:00.000Z","summary":"s","firstKeptEntryId":"cccccccc","tokensBefore":10,"details":null,"usage":null,"fromHook":true}`,
		`{"type":"compaction","id":"aaaaaaaa","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","summary":"s","firstKeptEntryId":"cccccccc","tokensBefore":10,"fromHook":true}`,
		`{"type":"label","id":"aaaaaaaa","parentId":"bbbbbbbb","timestamp":"2026-01-01T00:00:00.000Z","targetId":"cccccccc"}`,
	} {
		data := []byte(record)
		if allocations := testing.AllocsPerRun(100, func() { IsNullMember(data, "usage") }); allocations != 0 {
			t.Errorf("IsNullMember allocates %v times for %s", allocations, record)
		}
	}
}
