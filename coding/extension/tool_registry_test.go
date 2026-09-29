package extension

import (
	"slices"
	"testing"
)

func TestToolRegistryReplacementKeepsOrderAndSharedCopies(t *testing.T) {
	original := RegisteredTool{Definition: ToolDefinition{Name: "first", Description: "original"}}
	ext := Extension{Tools: map[string]RegisteredTool{"first": original}, ToolOrder: []string{"first"}}
	ext.InitializeToolRegistry()
	shared := ext
	before := shared.RegisteredTools()
	ext.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "second"}})
	ext.SetRegisteredTool(RegisteredTool{Definition: ToolDefinition{Name: "first", Description: "replacement"}})
	got := shared.RegisteredTools()
	if len(got) != 2 || got[0].Definition.Name != "first" || got[0].Definition.Description != "replacement" || got[1].Definition.Name != "second" {
		t.Fatalf("replacement=%+v", got)
	}
	if !slices.EqualFunc(before, []RegisteredTool{original}, func(a, b RegisteredTool) bool { return a.Definition.Description == b.Definition.Description }) {
		t.Fatal("in-flight snapshot mutated")
	}
	ext.ReplaceRegisteredTools(&Extension{Tools: map[string]RegisteredTool{"fresh": {Definition: ToolDefinition{Name: "fresh"}}}})
	got = shared.RegisteredTools()
	if len(got) != 1 || got[0].Definition.Name != "fresh" {
		t.Fatalf("restart retained obsolete tools: %+v", got)
	}
}
