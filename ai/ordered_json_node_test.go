package ai

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// TestOrderedObjectReadersMatchNode: orderedJSONObject and readJSONObjectMembers keep the members JSON.parse has, in its order, with the last value of
// a repeated key: "\ud800" and "\ud801", or a raw ED A0 80, are three keys although encoding/json gives each the same U+FFFD.
func TestOrderedObjectReadersMatchNode(t *testing.T) {
	docs := jsonseed.NodeDocuments()
	want := jsonseed.NodeMembers(t, docs)
	for i, doc := range docs {
		entries, err := orderedJSONObject(doc)
		if err != nil {
			t.Fatalf("orderedJSONObject(%q): %v", doc, err)
		}
		members, err := readJSONObjectMembers(doc)
		if err != nil {
			t.Fatalf("readJSONObjectMembers(%q): %v", doc, err)
		}
		if len(entries) != len(members) || len(entries) != len(want[i]) {
			t.Errorf("%q: %d entries, %d members, Node has %d keys", doc, len(entries), len(members), len(want[i]))
			continue
		}
		for j, w := range want[i] {
			if entries[j].key != jsonseed.ValidKey(w.Key) || jsonseed.Describe(entries[j].value) != w.Value {
				t.Errorf("%q entry %d: %q %s, Node %v", doc, j, entries[j].key, entries[j].value, w)
			}
			if members[j].key != jsonseed.ValidKey(w.Key) || jsonseed.Describe(members[j].value) != w.Value {
				t.Errorf("%q member %d: %q %s, Node %v", doc, j, members[j].key, members[j].value, w)
			}
		}
	}
}

// TestSchemaObjectOrderMatchesNode: readSchemaObjectOrder names the keys JSON.parse enumerates, in its order, for the same documents: "\ud800" and "\ud801",
// or a raw ED A0 80, stay three keys. A leading U+10FFFF key sorts after every other key, so each object is recorded as unsorted.
func TestSchemaObjectOrderMatchesNode(t *testing.T) {
	var docs [][]byte
	for _, doc := range jsonseed.NodeDocuments() {
		docs = append(docs, append([]byte(`{"\udbff\udfff":null,`), doc[1:]...))
	}
	want := jsonseed.NodeMembers(t, docs)
	for i, doc := range docs {
		order, err := readSchemaObjectOrder(doc)
		if err != nil {
			t.Fatalf("readSchemaObjectOrder(%q): %v", doc, err)
		}
		keys := order[""]
		if len(keys) != len(want[i]) {
			t.Errorf("%q: %d keys, Node has %d", doc, len(keys), len(want[i]))
			continue
		}
		for j, w := range want[i] {
			if !slices.Equal(jsonseed.Units(keys[j]), w.Key) {
				t.Errorf("%q key %d: %v, Node %v", doc, j, jsonseed.Units(keys[j]), w.Key)
			}
		}
	}
}
