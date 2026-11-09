package jsonopts

import (
	"bytes"
	"encoding/json/jsontext"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// TestReadNameMatchesNode: the names a document has after ReadName's dedup (last value wins, first position) are the own keys JSON.parse
// gives, for names Token.String() merges ("\ud800" and "\ud801" are both U+FFFD there). The test fails if ReadName is Token.String().
func TestReadNameMatchesNode(t *testing.T) {
	docs := jsonseed.NodeDocuments()
	want := jsonseed.NodeMembers(t, docs)
	for i, doc := range docs {
		d := jsontext.NewDecoder(bytes.NewReader(doc), Parse)
		if tok, err := d.ReadToken(); err != nil || tok.Kind() != '{' {
			t.Fatalf("%q: %v", doc, err)
		}
		var got []jsonseed.Member
		index := map[string]int{}
		for d.PeekKind() != '}' {
			name, err := ReadName(d)
			if err != nil {
				t.Fatalf("%q: %v", doc, err)
			}
			value, err := d.ReadValue()
			if err != nil {
				t.Fatalf("%q: %v", doc, err)
			}
			member := jsonseed.Member{Key: jsonseed.Units(name), Value: jsonseed.Describe(value)}
			if at, seen := index[name]; seen {
				got[at].Value = member.Value
				continue
			}
			index[name] = len(got)
			got = append(got, member)
		}
		jsonseed.SameMembers(t, doc, got, want[i])
	}
}

func TestValidName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"abc", "abc"},
		{"\xed\xa0\x80", "\ufffd"},
		{"a\xed\xbf\xbfb\xed\xa0\x81", "a\ufffdb\ufffd"},
		{"\ud7ff\ue000", "\ud7ff\ue000"},
	} {
		if got := ValidName(c.in); got != c.want {
			t.Errorf("ValidName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
