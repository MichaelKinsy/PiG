package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Ports the contract of packages/chord/src/sync/state.ts: each file operation of set is awaited in turn, so a failing step (here the
// rename that replaces the state) comes back from the same call and leaves the earlier state in place.

// renameFailing is osFiles whose RenameFile writes nothing and fails, after noting that the temporary file was already written.
type renameFailing struct {
	osFiles
	temporaryWritten *bool
	err              error
}

func (files renameFailing) RenameFile(_ context.Context, source, _ string) error {
	_, statErr := os.Stat(source)
	*files.temporaryWritten = statErr == nil
	return files.err
}

func TestSetOfASyncStateReturnsTheErrorOfAFailedRenameAndKeepsTheEarlierState(t *testing.T) {
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "state")
	old := Seq(3)
	if err := NewFileSyncStateStore(osFiles{}, directory).Set(ctx, "s1", SyncState{SyncedHead: &old}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("rename failed")
	written := false
	next := Seq(9)
	err := NewFileSyncStateStore(renameFailing{temporaryWritten: &written, err: boom}, directory).Set(ctx, "s1", SyncState{SyncedHead: &next})
	if !errors.Is(err, boom) {
		t.Fatalf("Set = %v, want the rename's error", err)
	}
	if !written {
		t.Fatal("the rename ran before the temporary file was written")
	}
	state, err := NewFileSyncStateStore(osFiles{}, directory).Get(ctx, "s1")
	if err != nil || state == nil || state.SyncedHead == nil || *state.SyncedHead != 3 {
		t.Fatalf("Get after the failed Set = %+v, %v; want the earlier head 3", state, err)
	}
}
