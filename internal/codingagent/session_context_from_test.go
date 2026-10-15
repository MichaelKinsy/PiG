package codingagent

import (
	"reflect"
	"testing"
)

// session-manager.ts:390-415,576-583 buildSessionContext(entries, leafId, byId): a null leafId is an empty branch, an omitted or falsy leafId and an unknown one fall back to the last entry, and byId is the entry index used to resolve the leaf and walk the parents instead of an index built from entries.
func TestBuildSessionContextUsesTheEntryIndex(t *testing.T) {
	requireTexts := func(t *testing.T, ctx SessionContext, want []string) {
		t.Helper()
		if got := contextTexts(ctx.Messages); !reflect.DeepEqual(got, want) {
			t.Fatalf("texts = %v, want %v", got, want)
		}
	}
	root := contextFixtureMessage(t, "1", "", "user", "root")
	reply := contextFixtureMessage(t, "2", "1", "assistant", "reply")
	branch := contextFixtureMessage(t, "3", "2", "user", "branch")
	listed := []SessionEntry{root, reply}
	index := map[string]SessionEntry{"1": root, "2": reply, "3": branch}

	requireTexts(t, BuildSessionContext(listed, new(""), nil), []string{"root", "reply"})
	requireTexts(t, BuildSessionContext(listed, new("nope"), nil), []string{"root", "reply"})
	requireTexts(t, BuildSessionContext(listed, new("1"), nil), []string{"root"})
	if got := BuildSessionContext(listed, nil, nil).Messages; len(got) != 0 {
		t.Fatalf("a null leaf is an empty branch, got %v", contextTexts(got))
	}
	// The leaf "3" is not in entries; only the supplied index can resolve it, and its parents come from the same index.
	requireTexts(t, BuildSessionContext(listed, new("3"), index), []string{"root", "reply", "branch"})
	requireTexts(t, BuildSessionContext(listed, new("3"), nil), []string{"root", "reply"})
	// The index also resolves a parent that entries does not list.
	requireTexts(t, BuildSessionContext([]SessionEntry{branch}, new(""), index), []string{"root", "reply", "branch"})
	requireTexts(t, BuildSessionContext([]SessionEntry{branch}, new(""), nil), []string{"branch"})
	if got := BuildSessionContext(nil, new(""), nil).Messages; len(got) != 0 {
		t.Fatalf("no entries, got %v", contextTexts(got))
	}
}
