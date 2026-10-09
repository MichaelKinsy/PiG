package extension

import (
	"encoding/json"
	"testing"
)

// types.ts SessionBoundaryDraft: each member is selected by its `type` literal and serializes only its own required fields.
func TestSessionBoundaryDraftTypeSelectsTheUpstreamMember(t *testing.T) {
	for _, tc := range []struct {
		draft SessionBoundaryDraft
		want  string
	}{
		{SessionBoundaryDraft{Type: SessionBoundaryDraftCustom, CustomType: "note"}, `{"type":"custom","customType":"note"}`},
		{SessionBoundaryDraft{Type: SessionBoundaryDraftCustomMessage, CustomType: "note", Content: "hi", Display: true}, `{"type":"custom_message","customType":"note","content":"hi","display":true}`},
		{SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1"}, `{"type":"context_edit","targetId":"e1","replacement":null}`},
		{SessionBoundaryDraft{Type: SessionBoundaryDraftCompaction, Summary: "s"}, `{"type":"compaction","summary":"s","firstKeptEntryId":null}`},
	} {
		raw, err := json.Marshal(tc.draft)
		if err != nil || string(raw) != tc.want {
			t.Errorf("%s: got %s, %v; want %s", tc.draft.Type, raw, err, tc.want)
		}
		var back SessionBoundaryDraft
		if err := json.Unmarshal(raw, &back); err != nil || back.Type != tc.draft.Type {
			t.Errorf("%s: round trip type = %q, %v", tc.draft.Type, back.Type, err)
		}
	}
}
