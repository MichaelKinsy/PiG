package ai

import (
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
