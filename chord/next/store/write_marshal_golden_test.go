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
	}
	for _, test := range cases {
		got, err := jsstring.MarshalJSON(test.value)
		if err != nil || string(got) != strings.ReplaceAll(test.want, `\u2028`, "\u2028") {
			t.Errorf("%s: got %s, %v; want %s", test.name, got, err, test.want)
		}
	}
}
