package main

import (
	"slices"
	"testing"
)

func TestUnassignedEntries(t *testing.T) {
	families := map[string]familyDef{
		"exact":  {Includes: []string{"packages/coding-agent/src/main.ts"}},
		"prefix": {Includes: []string{"packages/ai/src/api/"}},
	}
	entries := []portMapEntry{
		{Path: "packages/ai/src/api/anthropic-messages.ts", Status: "✅"},
		{Path: "packages/coding-agent/src/core/project-trust.ts", Status: "⬜"},
		{Path: "packages/coding-agent/src/main.ts", Status: "✅"},
		{Path: "packages/coding-agent/src/removed.ts", Status: "n/a"},
	}
	want := []string{"packages/coding-agent/src/core/project-trust.ts"}
	if got := unassignedEntries(entries, families); !slices.Equal(got, want) {
		t.Fatalf("unassignedEntries() = %v, want %v", got, want)
	}
}
