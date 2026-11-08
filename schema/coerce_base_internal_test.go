package schema

// pi: packages/schema/src/coerce.ts
//
// The base URI the coercer reads for a node (Pi's `baseOf`, which reads `Resolver.baseFor`, Pi main 2bd2c2d0a). The coercer indexes the schema before it first walks it, where Pi indexes on the first reference, so a node the index reached always has its indexed base; the one the index did not reach is a draft 7 object under a `$ref` (its siblings are inert), which pass 1 and pass 3 still walk. Its `$id` must give its base, not the base of the node it was reached from.

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func TestCoercerBaseOfAnUnreachedNodeFollowsItsID(t *testing.T) {
	root := chordjson.ObjectOf("type", "object")
	co := newCoercer(newCompiler(root, CompileOptions{}, false), root)
	const parent = "https://example.test/dir/parent.json"

	unreached := chordjson.ObjectOf("$id", "child.json")
	info := co.info(jsonObject{ordered: unreached})
	if info.indexed {
		t.Fatal("a node outside the schema was indexed")
	}
	if got, want := info.baseOr(parent), "https://example.test/dir/child.json"; got != want {
		t.Errorf("base of a node with $id %q under %s = %s, want %s", "child.json", parent, got, want)
	}

	noID := co.info(jsonObject{ordered: chordjson.ObjectOf("type", "string")})
	if got := noID.baseOr(parent); got != parent {
		t.Errorf("base of a node without $id = %s, want its parent's %s", got, parent)
	}

	indexed := co.info(jsonObject{ordered: root})
	if !indexed.indexed {
		t.Fatal("the root was not indexed")
	}
	if got := indexed.baseOr(parent); got != defaultBase {
		t.Errorf("base of the root = %s, want the index's %s", got, defaultBase)
	}
}
