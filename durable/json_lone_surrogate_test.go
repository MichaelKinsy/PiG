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

// A DocumentRecord's text is what JSON.stringify writes for it: a lone surrogate of its kind or key is the escape \udXXX, not U+FFFD
// (SqliteStorage stores this text as the `record` column and StoreStorage as the document's meta).
func TestDocumentRecordMarshalJSONKeepsALoneSurrogate(t *testing.T) {
	key := "k\xed\xb0\x80"
	record := DocumentRecord{Id: 3, Kind: "\xed\xa0\x80", Key: &key, Scope: DocumentRecordScope{Kind: ScopeSession}, CreatedAt: 2}
	text, err := record.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":3,"kind":"\ud800","key":"k\udc00","scope":{"kind":"session"},"createdAt":2}`
	if string(text) != want {
		t.Fatalf("MarshalJSON = %s, want %s", text, want)
	}
}
