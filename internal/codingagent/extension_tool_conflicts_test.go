package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// DetectToolConflicts reports each later tool registration with its first owner, in the order DetectExtensionConflicts reports the same conflicts, and no flag conflict.
func TestDetectToolConflictsNamesTheOwner(t *testing.T) {
	tools := func(names ...string) map[string]extension.RegisteredTool {
		out := map[string]extension.RegisteredTool{}
		for _, name := range names {
			out[name] = extension.RegisteredTool{}
		}
		return out
	}
	exts := []extension.Extension{
		{Name: "a", Path: "/a.ts", Tools: tools("shared", "zeta"), ToolOrder: []string{"shared", "zeta"}, Flags: map[string]extension.ExtensionFlag{"mode": {}}},
		{Name: "b", Path: "/b.ts", Tools: tools("zeta", "shared", "own"), ToolOrder: []string{"zeta", "shared", "own"}, Flags: map[string]extension.ExtensionFlag{"mode": {}}},
		{Name: "c", Path: "/a.ts", Tools: tools("shared")},
	}
	want := []ToolConflict{{Path: "/b.ts", Owner: "/a.ts", Tool: "zeta"}, {Path: "/b.ts", Owner: "/a.ts", Tool: "shared"}}
	if got := DetectToolConflicts(exts); !slices.Equal(got, want) {
		t.Fatalf("tool conflicts = %#v, want %#v", got, want)
	}
	if all := DetectExtensionConflicts(exts); len(all) != 3 {
		t.Fatalf("Pi's conflicts = %#v, want the two tools and the flag", all)
	}
}
