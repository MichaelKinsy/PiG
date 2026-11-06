package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestPlanToolsRefusesFullClear pins the harness twin of the 2026-11-06 incident rule: a
// planning round that sees a non-empty recorded loadout and an empty declaration records no
// change instead of a removal of every tool. That recorded removal is what left a live
// session tool-less for every later turn and resume (every model read "tools: (none)"); a
// tool-less transcript is established only by a session baseline, never by a diff.
func TestPlanToolsRefusesFullClear(t *testing.T) {
	offered := []ai.ToolSchema{
		{Name: "read", Description: "read files", Parameters: map[string]any{"type": "object"}},
		{Name: "bash", Description: "run commands", Parameters: map[string]any{"type": "object"}},
	}

	changes := planTools(offered, nil)
	if len(changes.toolsRemoved) != 0 || len(changes.toolsAdded) != 0 {
		t.Fatalf("full clear planned a change: removed=%v added=%v, want none", namesOfRefs(changes.toolsRemoved), namesOfSchemas(changes.toolsAdded))
	}

	partial := planTools(offered, offered[:1])
	if len(partial.toolsRemoved) != 1 || len(partial.toolsAdded) != 0 {
		t.Fatalf("partial removal = removed=%v added=%v, want one removed, none added", namesOfRefs(partial.toolsRemoved), namesOfSchemas(partial.toolsAdded))
	}
	if partial.toolsRemoved[0].Name != "bash" {
		t.Fatalf("partial removal removed %q, want bash", partial.toolsRemoved[0].Name)
	}

	fresh := planTools(nil, offered)
	if len(fresh.toolsRemoved) != 0 || len(fresh.toolsAdded) != 2 {
		t.Fatalf("fresh declaration = removed=%v added=%v, want none removed, two added", namesOfRefs(fresh.toolsRemoved), namesOfSchemas(fresh.toolsAdded))
	}
}

func namesOfRefs(refs []ai.ToolReference) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

func namesOfSchemas(schemas []ai.ToolSchema) []string {
	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Name)
	}
	return names
}
