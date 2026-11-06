package turn

import (
	"testing"
)

// TestPlanToolsRefusesFullClear pins the 2026-11-06 incident rule: a planning round that
// sees a non-empty recorded loadout and an empty declaration records NO change instead of
// a removal of every tool. That recorded removal is what left a live session tool-less for
// every later turn and resume (every model read "tools: (none)"); a tool-less transcript is
// established only by a session baseline, never by a diff. A later round with tools
// restored plans no change against the kept loadout, so the refusal self-heals.
func TestPlanToolsRefusesFullClear(t *testing.T) {
	offered := objects(parseArray(t, `[{"name":"read"},{"name":"bash"},{"name":"edit"},{"name":"write"}]`))

	changes := planTools(offered, nil)
	if len(changes.removed) != 0 || len(changes.added) != 0 {
		t.Fatalf("full clear planned a change: removed=%v added=%v, want none", changes.removed, changes.added)
	}

	// Partial changes still plan normally.
	three := objects(parseArray(t, `[{"name":"read"},{"name":"bash"},{"name":"edit"}]`))
	partial := planTools(offered, three)
	if len(partial.removed) != 1 || len(partial.added) != 0 {
		t.Fatalf("partial removal = removed=%v added=%v, want one removed, none added", partial.removed, partial.added)
	}

	// Adding tools still plans normally.
	five := objects(parseArray(t, `[{"name":"read"},{"name":"bash"},{"name":"edit"},{"name":"write"},{"name":"grep"}]`))
	grew := planTools(offered, five)
	if len(grew.removed) != 0 || len(grew.added) != 1 {
		t.Fatalf("addition = removed=%v added=%v, want none removed, one added", grew.removed, grew.added)
	}

	// A fresh (tool-less) transcript still accepts a full declaration.
	fresh := planTools(nil, offered)
	if len(fresh.removed) != 0 || len(fresh.added) != 4 {
		t.Fatalf("fresh declaration = removed=%v added=%v, want none removed, four added", fresh.removed, fresh.added)
	}
}
