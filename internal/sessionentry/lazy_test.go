package sessionentry_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

type memRecords struct{ records []json.RawMessage }

func (m *memRecords) ReadRecords(ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	for k, at := range ats {
		out[k] = m.records[at.Off]
	}
	return -1, nil
}

// MaterializeAll decodes a long run of lazy entries on several goroutines into the members DecodeSessionEntry gives each record, in order,
// and reports the error of the first entry whose record does not have its indexed base members.
func TestMaterializeAllDecodesInOrderAndReportsTheFirstChangedRecord(t *testing.T) {
	const n = 3000
	source := &memRecords{}
	lazy := make([]sessionentry.LazyEntry, n)
	entries := make([]sessionentry.SessionEntry, n)
	for i := range n {
		parent := "null"
		var parentID *string
		if i > 0 {
			p := fmt.Sprintf("e%d", i-1)
			parent, parentID = fmt.Sprintf("%q", p), &p
		}
		role := "user"
		if i%3 == 0 {
			role = "nonsense-role"
		}
		source.records = append(source.records, json.RawMessage(fmt.Sprintf(`{"type":"message","id":"e%d","parentId":%s,"timestamp":"t","message":{"role":%q,"content":"m%d","timestamp":1}}`, i, parent, role, i)))
		lazy[i] = sessionentry.NewLazyEntry(fmt.Sprintf("e%d", i), parentID, "t", source, sessionentry.RecordAt{Off: int64(i)})
		entries[i] = &lazy[i]
	}
	got, err := sessionentry.MaterializeAll(entries)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		want := sessionentry.DecodeSessionEntry(source.records[i])
		if fmt.Sprintf("%T", got[i]) != fmt.Sprintf("%T", want) || string(got[i].Raw()) != string(want.Raw()) {
			t.Fatalf("entry %d is %T %s, want %T %s", i, got[i], got[i].Raw(), want, want.Raw())
		}
	}
	for _, bad := range []int{n - 1, 1700, 5} {
		source.records[bad] = json.RawMessage(fmt.Sprintf(`{"type":"message","id":"other%d","parentId":null,"timestamp":"t","message":{"role":"user","content":"x"}}`, bad))
	}
	var changed *sessionentry.RecordChangedError
	if _, err := sessionentry.MaterializeAll(entries); !errors.As(err, &changed) || changed.ID != "e5" {
		t.Fatalf("MaterializeAll: %v, want the RecordChangedError of e5", err)
	}
}

// A message whose id and parentId are objects (Map keys that only that parse equals) reads back with the keys it was indexed with, though each
// decode of its record keys the objects anew.
func TestLazyEntryKeepsTheKeysOfObjectValuedIDs(t *testing.T) {
	record := json.RawMessage(`{"type":"message","id":{"a":1},"parentId":[2],"timestamp":"t","message":{"role":"user","content":"hi","timestamp":1}}`)
	base := sessionentry.DecodeSessionEntry(record).Base()
	lazy := sessionentry.NewLazyEntry(base.ID, base.ParentID, base.Timestamp, &memRecords{records: []json.RawMessage{record}}, sessionentry.RecordAt{})
	if again := sessionentry.DecodeSessionEntry(record).Base(); again.ID == base.ID || *again.ParentID == *base.ParentID {
		t.Fatalf("a second decode keys the objects %q %q again, want new keys", again.ID, *again.ParentID)
	}
	got, err := sessionentry.Materialize(&lazy)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Base(); b.ID != base.ID || *b.ParentID != *base.ParentID {
		t.Fatalf("materialized keys %q %q, want the indexed %q %q", b.ID, *b.ParentID, base.ID, *base.ParentID)
	}
}
