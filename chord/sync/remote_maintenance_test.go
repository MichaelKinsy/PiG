package sync

import (
	"context"
	"strings"
	"testing"
)

// A remote's own side (SessionSyncMaintenance, which Pi's FileSystemSyncRemote implements): a snapshot is kept at its end
// (PutSnapshot, GetSnapshot), folding drops a batch's body but keeps its slot taken, and DeleteSnapshots removes a snapshot.
// Also the session ID check every remote call makes (CheckSessionID) and a store's head (StoreHead).
func TestFileSystemSyncRemoteKeepsSnapshotsFoldsBatchesKeepingTheirSlotsAndDeletesSnapshots(t *testing.T) {
	ctx := context.Background()
	var remote SessionSyncMaintenance = newRemote(t)
	put, err := remote.PutBatch(ctx, "s1", 0, 1, "device-a", chunked([]byte("batch one"), 4), PutBatchOptions{})
	noError(t, err)
	if put.Type != PutCreated {
		t.Fatalf("PutBatch = %+v, want the slot created", put)
	}
	noError(t, remote.PutSnapshot(ctx, "s1", 1, chunked([]byte("snapshot at one"), 5)))
	snapshot, err := remote.GetSnapshot(ctx, "s1", 1)
	noError(t, err)
	if got := string(bytesOf(t, snapshot)); got != "snapshot at one" {
		t.Fatalf("GetSnapshot(1) = %q, want the snapshot put", got)
	}
	body, err := remote.GetBatch(ctx, "s1", 0)
	noError(t, err)
	if got := string(bytesOf(t, body)); got != "batch one" {
		t.Fatalf("GetBatch(0) = %q before the fold", got)
	}
	noError(t, remote.FoldBatches(ctx, "s1", []Seq{0}))
	folded, err := remote.GetBatch(ctx, "s1", 0)
	noError(t, err)
	if folded != nil {
		t.Fatal("GetBatch(0) after FoldBatches has a body, want none")
	}
	again, err := remote.PutBatch(ctx, "s1", 0, 1, "device-b", chunked([]byte("another"), 4), PutBatchOptions{})
	noError(t, err)
	if again.Type != PutExists {
		t.Fatalf("PutBatch into a folded slot = %+v, want exists: folding keeps the slot taken", again)
	}
	noError(t, remote.DeleteSnapshots(ctx, "s1", []Seq{1}))
	gone, err := remote.GetSnapshot(ctx, "s1", 1)
	noError(t, err)
	if gone != nil {
		t.Fatal("GetSnapshot(1) after DeleteSnapshots has a body, want none")
	}
}

func TestCheckSessionIDKeepsASessionIDInsideTheRemotesRootAndStoreHeadIsTheStoresLastSeq(t *testing.T) {
	for _, bad := range []string{"", "../escape", "a/b", ".."} {
		if _, err := CheckSessionID(bad); err == nil || !strings.Contains(err.Error(), "Invalid session ID") {
			t.Fatalf("CheckSessionID(%q) = %v, want an Invalid session ID error", bad, err)
		}
	}
	if id, err := CheckSessionID("session-1"); err != nil || id != "session-1" {
		t.Fatalf("CheckSessionID(session-1) = %q, %v", id, err)
	}
	s := sessionStore(t)
	if StoreHead(s) != 0 {
		t.Fatalf("StoreHead of an empty store = %d, want 0", StoreHead(s))
	}
	commitAll(t, s, rows{row("a", 1)})
	if StoreHead(s) != chordOf(s).LastSeq() || StoreHead(s) == 0 {
		t.Fatalf("StoreHead = %d, want the store's last seq %d", StoreHead(s), chordOf(s).LastSeq())
	}
}
