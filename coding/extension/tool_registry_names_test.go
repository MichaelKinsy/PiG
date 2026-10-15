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

// ToolRegistryVersion is the mutation counter the subprocess state push uses to decide that a serialized tool catalog is still current: every registration, including one that replaces a definition in place, and every registry replacement moves it; a read does not.
func TestToolRegistryVersionMovesOnEveryMutation(t *testing.T) {
	var plain Extension
	if got := plain.ToolRegistryVersion(); got != 0 {
		t.Fatalf("an extension without a live registry reports version %d, want 0", got)
	}
	live := Extension{}
	live.InitializeToolRegistry()
	start := live.ToolRegistryVersion()
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "a", Description: "first"}})
	afterAdd := live.ToolRegistryVersion()
	if afterAdd == start {
		t.Fatal("registering a tool did not move the version")
	}
	_ = live.RegisteredTools()
	_, _ = live.RegisteredTool("a")
	_ = live.RegisteredToolNames()
	if got := live.ToolRegistryVersion(); got != afterAdd {
		t.Fatalf("reads moved the version %d -> %d", afterAdd, got)
	}
	live.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "a", Description: "second"}})
	afterReplace := live.ToolRegistryVersion()
	if afterReplace == afterAdd {
		t.Fatal("replacing a definition in place did not move the version")
	}
	source := Extension{Tools: map[string]RegisteredTool{"b": {Definition: ToolDefinition{Name: "b"}}}, ToolOrder: []string{"b"}}
	live.ReplaceRegisteredTools(&source)
	if live.ToolRegistryVersion() == afterReplace {
		t.Fatal("replacing the registry did not move the version")
	}
}
