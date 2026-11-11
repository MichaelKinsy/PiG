package node

import (
	"os"
	"path/filepath"
	"testing"
)

// Ports packages/durable/test/env-node-list-race.test.ts (Pi 75ca6e7d0849, #12055): a writer renames a .reclaim file over a sidecar after
// the reader enumerated the directory, so the lstat of the reclaim file finds nothing and the listing leaves it out instead of failing.
func TestListDirLeavesOutAnEntryRemovedBetweenEnumerationAndLstat(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "doc-1.jsonl"), []byte("kept\n"), 0o600))
	reclaim := filepath.Join(root, "doc-2.jsonl.reclaim")
	mustDo(t, os.WriteFile(reclaim, []byte("replacement\n"), 0o600))
	called := false
	testHookBeforeListLstat = func(entryPath string) {
		if entryPath != reclaim {
			return
		}
		called = true
		mustDo(t, os.Rename(reclaim, filepath.Join(root, "doc-2.jsonl")))
	}
	t.Cleanup(func() { testHookBeforeListLstat = nil })

	entries, err := env.ListDir(t.Context(), ".")
	if err != nil {
		t.Fatalf("ListDir failed on an entry removed during the listing: %v", err)
	}
	if !called {
		t.Fatal("the removal was not injected before the lstat of the reclaim file")
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if len(names) != 1 || names[0] != "doc-1.jsonl" {
		t.Fatalf("ListDir names = %v, want [doc-1.jsonl]", names)
	}
}
