package durable

import (
	"encoding/json"
	"testing"
)

// Upstream keeps an entry's present empty `edits` list: Session.appendEntry copies the draft with copyJson (session/transaction.ts:368) and MemoryStorage clones the record, so `edits: []` is read back and persisted as `[]`; an absent list stays absent.
func TestEntryEditsKeepAPresentEmptyList(t *testing.T) {
	for name, test := range map[string]struct {
		value any
		want  string
	}{
		"record empty":  {EntryRecord{Id: 3, ConversationId: 1, Kind: "k", Edits: []ContextEdit{}}, `{"id":3,"conversationId":1,"kind":"k","edits":[]}`},
		"record absent": {EntryRecord{Id: 3, ConversationId: 1, Kind: "k"}, `{"id":3,"conversationId":1,"kind":"k"}`},
		"draft empty":   {EntryDraft{Kind: "k", Edits: []ContextEdit{}}, `{"kind":"k","edits":[]}`},
		"draft absent":  {EntryDraft{Kind: "k"}, `{"kind":"k"}`},
	} {
		encoded, err := json.Marshal(test.value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(encoded) != test.want {
			t.Errorf("%s: %s, want %s", name, encoded, test.want)
		}
	}
	var record EntryRecord
	if err := record.unmarshalGeneral([]byte(`{"id":3,"conversationId":1,"kind":"k","edits":[]}`)); err != nil || record.Edits == nil {
		t.Errorf("general decoder: edits %#v, %v; want a present empty list", record.Edits, err)
	}
	var draft EntryDraft
	if err := json.Unmarshal([]byte(`{"kind":"k","edits":[]}`), &draft); err != nil || draft.Edits == nil {
		t.Errorf("draft decoder: edits %#v, %v; want a present empty list", draft.Edits, err)
	}
}
