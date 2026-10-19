package extension

import (
	"slices"
	"testing"
)

// resource-loader.ts omitReplacedExtensions reads `extension.tools.keys()`: the names must include tools a live registry holds (registered
// after load, in order) and tools a plain Extension value lists without an order.
func TestRegisteredToolNamesReadTheLiveRegistryAndThePlainMap(t *testing.T) {
	plain := Extension{Tools: map[string]RegisteredTool{"b": {}, "a": {}}, ToolOrder: []string{"b"}}
	if got := plain.RegisteredToolNames(); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("plain names = %q, want the order first, then the unlisted sorted: [b a]", got)
	}
	live := Extension{}
	live.InitializeToolRegistry()
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "z"}})
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "y"}})
	if got := live.RegisteredToolNames(); !slices.Equal(got, []string{"z", "y"}) {
		t.Fatalf("live names = %q, want registration order [z y]", got)
	}
	if len(live.ToolOrder) != 0 {
		t.Fatalf("the live registry should not fill ToolOrder (the reason ToolOrder cannot be read), got %q", live.ToolOrder)
	}
}

// Every registration a live registry takes, including one that replaces a name with an identical definition, carries a revision no other registration has, and reading the registry leaves them as they were. A registration no live registry stamped has none.
func TestRegisteredToolRevisionIdentifiesEachRegistration(t *testing.T) {
	var plain Extension
	plain.Tools = map[string]RegisteredTool{"p": {Definition: ToolDefinition{Name: "p"}}}
	if tool, _ := plain.RegisteredTool("p"); tool.Revision != 0 {
		t.Fatalf("an unregistered tool has revision %d, want 0", tool.Revision)
	}
	live := Extension{Tools: map[string]RegisteredTool{"initial": {Definition: ToolDefinition{Name: "initial"}}}, ToolOrder: []string{"initial"}}
	live.InitializeToolRegistry()
	seen := map[uint64]string{}
	take := func(what, name string) uint64 {
		t.Helper()
		tool, ok := live.RegisteredTool(name)
		if !ok || tool.Revision == 0 {
			t.Fatalf("%s: revision %d (found %v), want a stamped registration", what, tool.Revision, ok)
		}
		if prior, dup := seen[tool.Revision]; dup {
			t.Fatalf("%s: revision %d repeats %s", what, tool.Revision, prior)
		}
		seen[tool.Revision] = what
		return tool.Revision
	}
	take("a tool the registry started with", "initial")
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "a"}})
	first := take("first registration of a", "a")
	_ = live.RegisteredTools()
	if again, _ := live.RegisteredTool("a"); again.Revision != first {
		t.Fatalf("reading moved the revision %d -> %d", first, again.Revision)
	}
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "a"}})
	take("an identical replacement of a", "a")
	source := Extension{Tools: map[string]RegisteredTool{"b": {Definition: ToolDefinition{Name: "b"}}}, ToolOrder: []string{"b"}}
	live.ReplaceRegisteredTools(&source)
	take("a tool taken from a replacing registry", "b")
}
