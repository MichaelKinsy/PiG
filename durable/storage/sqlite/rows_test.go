package sqlite

// pi: packages/durable/src/storage/sqlite/rows.ts

// The cells of a document's `documents` row, its address columns and its current-only rule, asserted against the text of
// rows.ts for records chosen so that a swapped, shifted or inverted column differs: every cell of the row is distinct
// (id 11, created 5, retired 8, owner 7) and each field decides exactly one of them.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

func TestDocumentRowCellsAreTheDocumentsColumnsInOrder(t *testing.T) {
	key := "k"
	empty := ""
	retired := durable.Seq(8)
	cases := []struct {
		name   string
		record durable.DocumentRecord
		want   []cell
	}{
		{
			"a live session singleton",
			durable.DocumentRecord{Id: 11, Kind: "kind", CreatedAt: 5, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
			[]cell{int64(11), `"kind"`, int64(0), `""`, "session", int64(0), int64(5), nil,
				`{"id":11,"kind":"kind","scope":{"kind":"session"},"createdAt":5}`},
		},
		{
			"a retired conversation family member",
			durable.DocumentRecord{
				Id: 11, Kind: "kind", Key: &key, CreatedAt: 5, RetiredAt: &retired,
				Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 7},
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			},
			[]cell{int64(11), `"kind"`, int64(1), `"k"`, "conversation", int64(7), int64(5), int64(8),
				`{"id":11,"kind":"kind","key":"k","scope":{"kind":"conversation","conversationId":7},"history":"rewindable","fork":"asOf","createdAt":5,"retiredAt":8}`},
		},
		{
			"a live task document with an empty key is a family member",
			durable.DocumentRecord{Id: 11, Kind: "a\"b", Key: &empty, CreatedAt: 5, Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 7}},
			[]cell{int64(11), `"a\"b"`, int64(1), `""`, "task", int64(7), int64(5), nil,
				`{"id":11,"kind":"a\"b","key":"","scope":{"kind":"task","taskId":7},"createdAt":5}`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := documentRow(c.record)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("documentRow = %#v\nwant           %#v", got, c.want)
			}
		})
	}
}

func TestAddressPartsAreTheAddressColumns(t *testing.T) {
	key := "k"
	empty := ""
	cases := []struct {
		name  string
		scope durable.DocumentRecordScope
		key   *string
		want  addressParts
	}{
		{"session singleton", durable.DocumentRecordScope{Kind: durable.ScopeSession}, nil,
			addressParts{kind: `"kind"`, scopeKind: "session", family: 0, keyValue: `""`}},
		{"session family member with an empty key", durable.DocumentRecordScope{Kind: durable.ScopeSession}, &empty,
			addressParts{kind: `"kind"`, scopeKind: "session", family: 1, keyValue: `""`}},
		{"conversation owner", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 4, TaskId: 6}, &key,
			addressParts{kind: `"kind"`, scopeKind: "conversation", ownerId: 4, family: 1, keyValue: `"k"`}},
		{"task owner", durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 6, ConversationId: 4}, &key,
			addressParts{kind: `"kind"`, scopeKind: "task", ownerId: 6, family: 1, keyValue: `"k"`}},
		{"session ignores stray owners", durable.DocumentRecordScope{Kind: durable.ScopeSession, ConversationId: 4, TaskId: 6}, nil,
			addressParts{kind: `"kind"`, scopeKind: "session", family: 0, keyValue: `""`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := addressPartsOf("kind", c.scope, c.key); got != c.want {
				t.Fatalf("addressPartsOf = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestEncodeIndexedStringHoldsTheLiteralJSONStringifyText(t *testing.T) {
	cases := map[string]string{
		"": `""`, "plain": `"plain"`, `q"b\`: `"q\"b\\"`, "a\x00b": `"a\u0000b"`, "\xed\xa0\x80": `"\ud800"`, "\xed\xb0\x80": `"\udc00"`,
		"</&\u2028>": "\"</&\u2028>\"", "é日本": `"é日本"`, "\n\t": `"\n\t"`,
	}
	for value, want := range cases {
		if got := encodeIndexedString(value); got != want {
			t.Errorf("encodeIndexedString(%q) = %s, want %s", value, got, want)
		}
	}
}

func TestIsCurrentOnlyKeepsHistoryOfRewindableConversationDocumentsOnly(t *testing.T) {
	cases := []struct {
		name    string
		scope   durable.DocumentRecordScope
		history durable.DocumentHistory
		want    bool
	}{
		{"session", durable.DocumentRecordScope{Kind: durable.ScopeSession}, "", true},
		{"task", durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 2}, "", true},
		{"task that names a history", durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: 2}, durable.HistoryRewindable, true},
		{"session that names a history", durable.DocumentRecordScope{Kind: durable.ScopeSession}, durable.HistoryRewindable, true},
		{"latest conversation", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 1}, durable.HistoryLatest, true},
		{"rewindable conversation", durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 1}, durable.HistoryRewindable, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isCurrentOnly(durable.DocumentRecord{Id: 1, Kind: "k", Scope: c.scope, History: c.history}); got != c.want {
				t.Fatalf("isCurrentOnly = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEncodeJSONKeepsLoneSurrogatesAndLiteralMarkup(t *testing.T) {
	got, err := encodeJSON([]any{"\xed\xa0\x80", "</&\u2028\u2029>", "x\x00", float64(1e21), 1.5, nil, true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "[\"\\ud800\",\"</&\u2028\u2029>\",\"x\\u0000\",1e+21,1.5,null,true]"; got != want {
		t.Fatalf("encodeJSON = %s, want %s", got, want)
	}
}

// SqliteStorage reads back the text it wrote: the escape of a lone UTF-16 surrogate is the unit (JSON.parse), not U+FFFD.
func TestParseJSONReadsALoneSurrogateEscapeAsTheUnit(t *testing.T) {
	record, err := parseJSON[durable.DocumentRecord](`{"id":3,"kind":"\ud800","key":"k\udc00","scope":{"kind":"session"},"createdAt":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != "\xed\xa0\x80" || record.Key == nil || *record.Key != "k\xed\xb0\x80" {
		t.Errorf("record = %q %v, want the units kept", record.Kind, record.Key)
	}
}
