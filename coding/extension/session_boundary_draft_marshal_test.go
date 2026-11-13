package extension

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

func sessionBoundaryDraftMarshalCases() []struct {
	name  string
	draft SessionBoundaryDraft
	want  string
} {
	kept := "e7"
	return []struct {
		name  string
		draft SessionBoundaryDraft
		want  string
	}{
		{"custom, no data", SessionBoundaryDraft{Type: SessionBoundaryDraftCustom, CustomType: "note"}, `{"type":"custom","customType":"note"}`},
		{"custom, empty members", SessionBoundaryDraft{Type: SessionBoundaryDraftCustom}, `{"type":"custom","customType":""}`},
		{"custom, empty object data", SessionBoundaryDraft{Type: SessionBoundaryDraftCustom, CustomType: "note", Data: map[string]any{}}, `{"type":"custom","customType":"note","data":{}}`},
		{"custom_message, absent content", SessionBoundaryDraft{Type: SessionBoundaryDraftCustomMessage}, `{"type":"custom_message","customType":"","display":false}`},                                                    // session-manager.ts:1359: the entry holds the content it was given, and JSON.stringify drops an undefined member
		{"custom_message, explicit null content", SessionBoundaryDraft{Type: SessionBoundaryDraftCustomMessage, Content: orderedjson.Null()}, `{"type":"custom_message","customType":"","content":null,"display":false}`}, // session-manager.ts:1359: a null content is held and written
		{"custom_message, details", SessionBoundaryDraft{Type: SessionBoundaryDraftCustomMessage, CustomType: "note", Content: "hi", Display: true, Details: []any{}}, `{"type":"custom_message","customType":"note","content":"hi","display":true,"details":[]}`},
		// context_edit: the member is absent only for a draft decoded without it; a draft with no value has null.
		{"context_edit, undefined replacement", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", ReplacementUndefined: true}, `{"type":"context_edit","targetId":"e1"}`},
		{"context_edit, undefined replacement, empty target", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, ReplacementUndefined: true}, `{"type":"context_edit","targetId":""}`},
		{"context_edit, undefined replacement wins over a value", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", ReplacementUndefined: true, Replacement: json.RawMessage(`{"a":1}`)}, `{"type":"context_edit","targetId":"e1"}`},
		{"context_edit, nil replacement is null", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1"}, `{"type":"context_edit","targetId":"e1","replacement":null}`},
		{"context_edit, empty replacement is null", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", Replacement: json.RawMessage{}}, `{"type":"context_edit","targetId":"e1","replacement":null}`},
		{"context_edit, null replacement", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", Replacement: json.RawMessage(`null`)}, `{"type":"context_edit","targetId":"e1","replacement":null}`},
		{"context_edit, value", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", Replacement: json.RawMessage(` { "role" : "user", "content" : [ ] } `)}, `{"type":"context_edit","targetId":"e1","replacement":{"role":"user","content":[]}}`},
		{"context_edit, escaped target", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "<e&1>\"\u2028", Replacement: json.RawMessage(`"<&>"`)}, `{"type":"context_edit","targetId":"\u003ce\u00261\u003e\"\u2028","replacement":"\u003c\u0026\u003e"}`},
		{"compaction, null first kept", SessionBoundaryDraft{Type: SessionBoundaryDraftCompaction, Summary: "s"}, `{"type":"compaction","summary":"s","firstKeptEntryId":null}`},
		{"compaction, first kept and details", SessionBoundaryDraft{Type: SessionBoundaryDraftCompaction, FirstKeptEntryID: &kept, Details: map[string]any{"n": 1}}, `{"type":"compaction","summary":"","firstKeptEntryId":"e7","details":{"n":1}}`},
		{"no type", SessionBoundaryDraft{}, `{"type":""}`},
		{"unknown type keeps every set member", SessionBoundaryDraft{Type: "other", TargetID: "e1", Replacement: json.RawMessage(`[1]`), ReplacementUndefined: true, Summary: "s"}, `{"type":"other","targetId":"e1","replacement":[1],"summary":"s"}`},
	}
}

// The bytes of every SessionBoundaryDraft member, written by the method itself and through an outer encoder (as a value, a pointer and a slice element).
// A context_edit draft has three forms: no `replacement` member (decoded without one), `"replacement":null`, and a value.
func TestSessionBoundaryDraftMarshalBytes(t *testing.T) {
	for _, tc := range sessionBoundaryDraftMarshalCases() {
		direct, err := tc.draft.MarshalJSON()
		if err != nil {
			t.Errorf("%s: MarshalJSON: %v", tc.name, err)
			continue
		}
		if string(direct) != tc.want {
			t.Errorf("%s: MarshalJSON wrote\n%s\nwant\n%s", tc.name, direct, tc.want)
		}
		for form, value := range map[string]any{"value": tc.draft, "pointer": &tc.draft} {
			raw, err := json.Marshal(value)
			if err != nil || string(raw) != tc.want {
				t.Errorf("%s (%s): got %s, %v; want %s", tc.name, form, raw, err, tc.want)
			}
		}
		raw, err := json.Marshal([]SessionBoundaryDraft{tc.draft, tc.draft})
		if want := "[" + tc.want + "," + tc.want + "]"; err != nil || string(raw) != want {
			t.Errorf("%s (slice): got %s, %v; want %s", tc.name, raw, err, want)
		}
	}
}

// A replacement that is not JSON fails with the encoder's text, and an undefined replacement is never read.
func TestSessionBoundaryDraftMarshalErrorText(t *testing.T) {
	draft := SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "e1", Replacement: json.RawMessage(`{bad`)}
	_, err := draft.MarshalJSON()
	if want := "json: error calling MarshalJSON for type *jsontext.Value: invalid character 'b' looking for beginning of object key string"; err == nil || err.Error() != want {
		t.Errorf("MarshalJSON error = %v\nwant %s", err, want)
	}
	_, err = json.Marshal(draft)
	if want := "json: error calling MarshalJSON for type *extension.SessionBoundaryDraft: json: error calling MarshalJSON for type *jsontext.Value: invalid character 'b' looking for beginning of object key string"; err == nil || err.Error() != want {
		t.Errorf("json.Marshal error = %v\nwant %s", err, want)
	}
	draft.ReplacementUndefined = true
	if raw, err := json.Marshal(draft); err != nil || string(raw) != `{"type":"context_edit","targetId":"e1"}` {
		t.Errorf("undefined replacement: got %s, %v", raw, err)
	}
}

// A draft decoded from each context_edit form is written back in that form.
func TestSessionBoundaryDraftContextEditRoundTrip(t *testing.T) {
	for _, text := range []string{
		`{"type":"context_edit","targetId":"e1"}`,
		`{"type":"context_edit","targetId":"e1","replacement":null}`,
		`{"type":"context_edit","targetId":"e1","replacement":{"role":"user","content":"x"}}`,
	} {
		var draft SessionBoundaryDraft
		if err := json.Unmarshal([]byte(text), &draft); err != nil {
			t.Errorf("%s: %v", text, err)
			continue
		}
		if raw, err := json.Marshal(draft); err != nil || string(raw) != text {
			t.Errorf("%s: written back as %s, %v", text, raw, err)
		}
	}
}

func BenchmarkSessionBoundaryDraftMarshal(b *testing.B) {
	for _, tc := range []struct {
		name  string
		draft SessionBoundaryDraft
	}{
		{"context_edit_undefined", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "entry-1", ReplacementUndefined: true}},
		{"context_edit_null", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "entry-1"}},
		{"context_edit_value", SessionBoundaryDraft{Type: SessionBoundaryDraftContextEdit, TargetID: "entry-1", Replacement: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"hello"}]}`)}},
		{"custom", SessionBoundaryDraft{Type: SessionBoundaryDraftCustom, CustomType: "note", Data: map[string]any{"n": 1}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := json.Marshal(tc.draft); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
