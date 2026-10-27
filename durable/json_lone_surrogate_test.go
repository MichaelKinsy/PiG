package durable

import (
	"testing"
)

// ToJsonValue is Chord's copyJson and FromJsonValue its inverse for typed values: a string that holds a lone UTF-16 surrogate
// (a Go string holds it as WTF-8) comes back as the same string, as JSON.parse(JSON.stringify(value)) gives Pi the same
// unit, not U+FFFD.
func TestToJsonValueAndFromJsonValueKeepALoneSurrogate(t *testing.T) {
	type record struct {
		Text   string            `json:"text"`
		Nested map[string]string `json:"nested"`
		List   []string          `json:"list"`
	}
	const wtf = "a\xed\xa0\xbd<&>"
	value, err := ToJsonValue(record{Text: wtf, Nested: map[string]string{"k": wtf}, List: []string{wtf}})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(JsonObject)
	if !ok {
		t.Fatalf("value is %T", value)
	}
	if got, _ := object.Get("text"); got != wtf {
		t.Errorf("text = %q, want %q", got, wtf)
	}
	back, err := FromJsonValue[record](value)
	if err != nil {
		t.Fatal(err)
	}
	if back.Text != wtf || back.Nested["k"] != wtf || back.List[0] != wtf {
		t.Errorf("round trip = %+v, want the unit kept in every string", back)
	}
}
