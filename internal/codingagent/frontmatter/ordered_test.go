package frontmatter

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/yaml12"
)

// ParseOrdered keeps the key order the yaml package inserts: document order, at every depth (the core schema reads `<<` as an ordinary key). Parse returns maps, which have none.
func TestParseOrderedListsKeysInInsertionOrder(t *testing.T) {
	doc := ParseOrdered("---\nz: 1\n10: a\nb:\n  y: 1\n  x: 2\nbase: &base\n  m: 1\n  n: 2\nmerged:\n  <<: *base\n  own: 3\n---\nbody\n")
	object, ok := doc.Value.(*yaml12.Object)
	if !ok {
		t.Fatalf("value %T, want *yaml12.Object", doc.Value)
	}
	if want := []string{"z", "10", "b", "base", "merged"}; !reflect.DeepEqual(object.Keys, want) {
		t.Fatalf("keys %v, want %v", object.Keys, want)
	}
	if object.Values["z"] != float64(1) || doc.Err != nil {
		t.Fatalf("z = %v, error %v", object.Values["z"], doc.Err)
	}
	nested := object.Values["b"].(*yaml12.Object)
	if want := []string{"y", "x"}; !reflect.DeepEqual(nested.Keys, want) {
		t.Fatalf("nested keys %v, want %v", nested.Keys, want)
	}
	merged := object.Values["merged"].(*yaml12.Object)
	if want := []string{"<<", "own"}; !reflect.DeepEqual(merged.Keys, want) {
		t.Fatalf("merged keys %v, want %v", merged.Keys, want)
	}
	if doc.Body != "body" || len(doc.Frontmatter) != 5 {
		t.Fatalf("body %q, frontmatter %v", doc.Body, doc.Frontmatter)
	}
	if plain := Parse("---\nz: 1\n---\n"); !reflect.DeepEqual(plain.Value, map[string]any{"z": float64(1)}) {
		t.Fatalf("Parse changed: %#v", plain.Value)
	}
}
