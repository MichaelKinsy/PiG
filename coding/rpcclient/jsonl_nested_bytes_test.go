package rpcclient

import (
	"encoding/json"
	"testing"
)

func TestSerializeJsonLineNestedMarshalEscapes(t *testing.T) {
	got, err := SerializeJsonLine(struct {
		Raw json.RawMessage `json:"raw"`
	}{json.RawMessage(`{"text":"\u003ctag\u003e\u0026\u2028\u2029","literal":"\\u003c"}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"raw\":{\"text\":\"<tag>&\u2028\u2029\",\"literal\":\"\\\\u003c\"}}\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
