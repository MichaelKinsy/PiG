package json_test

import (
	"bytes"
	stdjson "encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func TestStandardJSONInteroperability(t *testing.T) {
	type fields struct {
		Text   string `json:"text"`
		Zero   int    `json:"zero,omitzero"`
		Omit   string `json:"omit,omitempty"`
		Quoted int    `json:"quoted,string"`
	}
	for _, value := range []any{
		nil, true, stdjson.Number("1.5"), stdjson.Number("9007199254740993"),
		stdjson.RawMessage(`{"data":"\ud83d"}`),
		map[string]any{"html": "<&>", "unicode": "你好😀\u2028", "nil": nil, "number": stdjson.Number("12")},
		fields{Text: "plain", Quoted: 17}, []byte{0, 1, 255},
	} {
		want, err := stdjson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%#v: %s != %s", value, got, want)
		}
	}
	dec := json.NewDecoder(bytes.NewBufferString(`{"n":1.5}`))
	dec.UseNumber()
	var got map[string]any
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]any{"n": stdjson.Number("1.5")}) {
		t.Fatalf("number type drift: %#v", got)
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	if _, err := json.Marshal(cyclic); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := json.Marshal(stdjson.Number("not-a-number")); err == nil {
		t.Fatal("invalid Number accepted")
	}
}
