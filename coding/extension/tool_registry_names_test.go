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
