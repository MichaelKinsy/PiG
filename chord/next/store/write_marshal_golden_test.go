package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// TestWriteMarshalGolden pins the bytes of the engine and table writes' MarshalJSON, captured before they became
// StringifyAppenders. U+2028 is written raw (JSON.stringify does not escape it); the cases spell it \u2028.
func TestWriteMarshalGolden(t *testing.T) {
	value := delta.JsonObjectOf("a<b>&", "é\u2028😀", "n", []any{1.5, "x"})
	base := DocumentContent{Kind: ContentBase, Version: 3, Fingerprint: "f<>&é", Value: value}
	revision := RevisionContent{Kind: ContentBase, Version: 3, Value: value}
	documentKey := "k<é"
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"DocumentContent base", base, `{"kind":"base","version":3,"fingerprint":"f<>&é","value":{"a<b>&":"é\u2028😀","n":[1.5,"x"]}}`},
		{"DocumentContent delta", DocumentContent{Kind: ContentDelta, Version: 4}, `{"kind":"delta","version":4,"ops":[]}`},
		{"RevisionContent base", revision, `{"kind":"base","version":3,"value":{"a<b>&":"é\u2028😀","n":[1.5,"x"]}}`},
		{"RevisionContent delta", RevisionContent{Kind: ContentDelta, Version: 4}, `{"kind":"delta","version":4,"ops":[]}`},
		{"DocumentChange", &DocumentChange{ID: 1, Revision: 2, Content: base}, `{"kind":"document.change","id":1,"revision":2,"content":{"kind":"base","version":3,"fingerprint":"f<>&é","value":{"a<b>&":"é\u2028😀","n":[1.5,"x"]}}}`},
		{"DocumentRetire", &DocumentRetire{ID: 1, Revision: 2}, `{"kind":"document.retire","id":1,"revision":2}`},
		{"DocumentRevisionWrite", &DocumentRevisionWrite{ID: 1, Seq: 2, Content: revision}, `{"kind":"document.revision","id":1,"seq":2,"content":{"kind":"base","version":3,"value":{"a<b>&":"é\u2028😀","n":[1.5,"x"]}}}`},
		{"IndexWrite", &IndexWrite{Table: "t<é>", Name: "i&"}, `{"kind":"index","table":"t<é>","name":"i&","columns":[]}`},
		{"DropIndexWrite", &DropIndexWrite{Table: "t<é>", Name: "i&"}, `{"kind":"dropIndex","table":"t<é>","name":"i&"}`},
		{"AlterWrite", &AlterWrite{Table: "t😀", Version: 2, Edit: TableEdit{Kind: EditRename, From: "a<", To: "b&"}}, `{"kind":"alter","table":"t😀","version":2,"edit":{"kind":"rename","from":"a<","to":"b&"}}`},
		{"DeleteWrite", &DeleteWrite{Table: "t", Count: 2}, `{"kind":"delete","table":"t","key":null,"count":2}`},
		{"DocumentCreate", &DocumentCreate{Record: DocumentRecord{ID: 1, Owner: "o<é", Namespace: "n&", Kind: "k", Key: &documentKey, History: HistoryLatest, CreatedAt: 5, Revision: 2, Version: 3, Fingerprint: "f>"}, Base: &DocumentBase{Version: 3, Fingerprint: "f>", Value: value}}, `{"kind":"document.create","record":{"id":1,"owner":"o<é","namespace":"n&","kind":"k","key":"k<é","history":"latest","createdAt":5,"revision":2,"version":3,"fingerprint":"f>"},"base":{"version":3,"fingerprint":"f>","value":{"a<b>&":"é\u2028😀","n":[1.5,"x"]}}}`},
		{"DocumentCreate without base", &DocumentCreate{Record: DocumentRecord{ID: 1, Owner: "o", Namespace: "n", Kind: "k", History: HistoryNone}}, `{"kind":"document.create","record":{"id":1,"owner":"o","namespace":"n","kind":"k","history":"none","createdAt":0,"revision":0,"version":0,"fingerprint":""}}`},
		{"TableDefinitionWrite", &TableDefinitionWrite{Table: "t<é", Version: 2, State: StateReady, Columns: ColumnRecords{{Name: "a&", Record: ColumnRecord{Type: "str", Optional: true, Default: "d\u2028"}}, {Name: "b", Record: ColumnRecord{Type: "int"}}}, Key: []string{"a&"}, Fingerprint: "f<"}, `{"kind":"table","table":"t<é","version":2,"state":"ready","columns":{"a&":{"type":"str","optional":true,"default":"d\u2028"},"b":{"type":"int"}},"key":["a&"],"fingerprint":"f<"}`},
		{"PutWrite", &PutWrite{Table: "t<", Row: delta.JsonObjectOf("a&", "é\u2028", "b", 1.5), Version: 2, Columns: delta.JsonObjectOf("c", 1)}, `{"kind":"put","table":"t<","row":{"a&":"é\u2028","b":1.5},"version":2,"columns":{"c":1}}`},
		{"UpdateWrite", &UpdateWrite{Table: "t<", Key: delta.JsonObjectOf("id", "k&"), Set: SetNode{{Column: "a>", Node: ExprNode{Op: ExprLiteral, Value: "é\u2028"}}}, Columns: delta.JsonObjectOf("c", 1), Count: 2}, `{"kind":"update","table":"t<","key":{"id":"k&"},"set":{"a>":"é\u2028"},"columns":{"c":1},"count":2}`},
		{"UpdateWhereWrite", &UpdateWhereWrite{Table: "t<", Where: WhereNode{Op: WhereEq, Column: "a&", Value: "é"}, Set: SetNode{{Column: "a>", Node: ExprNode{Op: ExprLiteral, Value: 1.5}}}, Columns: delta.JsonObjectOf("c", 1), Count: 2}, `{"kind":"updateWhere","table":"t<","where":{"eq":["a&","é"]},"set":{"a>":1.5},"columns":{"c":1},"count":2}`},
		{"DeleteWhereWrite", &DeleteWhereWrite{Table: "t<", Where: WhereNode{Op: WhereEq, Column: "a&", Value: "é\u2028"}, Count: 3}, `{"kind":"deleteWhere","table":"t<","where":{"eq":["a&","é\u2028"]},"count":3}`},
	}
	for _, test := range cases {
		got, err := jsstring.MarshalJSON(test.value)
		if err != nil || string(got) != strings.ReplaceAll(test.want, `\u2028`, "\u2028") {
			t.Errorf("%s: got %s, %v; want %s", test.name, got, err, test.want)
		}
	}
}
