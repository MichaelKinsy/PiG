package tools

import (
	"slices"
	"testing"
)

// tools/index.ts createReadOnlyTools returns read, grep, find and ls in that order: it carries no bash tool, and nothing that writes.
func TestCreateReadOnlyToolsReturnsReadGrepFindLs(t *testing.T) {
	var names []string
	for _, tool := range CreateReadOnlyTools(t.TempDir(), nil) {
		names = append(names, tool.Name())
	}
	if want := []string{"read", "grep", "find", "ls"}; !slices.Equal(names, want) {
		t.Fatalf("read-only tools = %v, want %v", names, want)
	}
}
