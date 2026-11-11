package orderedjson

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

// TestObjectMatchesNode: Parse keeps the keys JSON.parse has, in its order, with the last value of a repeated key, and writes them back as
// JSON.stringify does, a lone surrogate as its escape.
func TestObjectMatchesNode(t *testing.T) {
	docs := jsonseed.NodeDocuments()
	want := jsonseed.NodeMembers(t, docs)
	for i, doc := range docs {
		object, err := Parse(doc)
		if err != nil {
			t.Fatalf("Parse(%q): %v", doc, err)
		}
		keys := object.Keys()
		if len(keys) != len(want[i]) {
			t.Errorf("%q: %d keys, Node has %d", doc, len(keys), len(want[i]))
			continue
		}
		var got []jsonseed.Member
		for _, key := range keys {
			value, _ := object.Get(key)
			got = append(got, jsonseed.Member{Key: jsonseed.Units(key), Value: jsonseed.Describe(value)})
		}
		jsonseed.SameMembers(t, doc, got, want[i])
		written, err := object.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(%q): %v", doc, err)
		}
		again, err := Parse(written)
		if err != nil {
			t.Fatalf("Parse(%q): %v", written, err)
		}
		if len(again.Keys()) != len(keys) {
			t.Errorf("%q written as %q has %d keys, not %d", doc, written, len(again.Keys()), len(keys))
		}
	}
}
