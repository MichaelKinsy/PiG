package sync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Proof of the directory-backed sync pieces Pi's tests reach only through a client: files.ts (the not-found answer and the
// reader's close), filesystem.ts (FileSystemSyncRemote) and state.ts (FileSyncStateStore).

func TestIsNotFoundIsTrueOnlyForAMissingFileOrDirectory(t *testing.T) {
	_, err := os.ReadFile(filepath.Join(t.TempDir(), "missing"))
	if !IsNotFound(err) || !IsNotFound(fmt.Errorf("wrapped: %w", fs.ErrNotExist)) {
		t.Fatal("a missing file is not reported as not found")
	}
	if IsNotFound(errors.New("boom")) || IsNotFound(nil) || IsNotFound(fs.ErrPermission) {
		t.Fatal("another failure, or none, is reported as not found")
	}
}

func TestASyncFileReaderReadsAtOffsetsAndClosesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	var files SyncFileSystem = osFiles{}
	reader, err := files.OpenBinaryReader(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reader.Read(context.Background(), 3, 4); err != nil || string(got) != "3456" {
		t.Fatalf("Read(3, 4) = %q, %v; want 3456", got, err)
	}
	if got, err := reader.Read(context.Background(), 8, 10); err != nil || string(got) != "89" {
		t.Fatalf("Read at the end = %q, %v; want the 2 bytes left", got, err)
	}
	if err := reader.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := reader.Read(context.Background(), 0, 1); err == nil {
		t.Fatal("a closed reader still reads")
	}
	if _, err := files.OpenBinaryReader(context.Background(), path+".missing"); !IsNotFound(err) {
		t.Fatalf("opening a missing file = %v, want not found", err)
	}
}

func TestFileSystemSyncRemoteKeepsASessionsMetaAndRefusesAnEscapingSessionID(t *testing.T) {
	remote := NewFileSystemSyncRemote(osFiles{}, t.TempDir())
	var _ *FileSystemSyncRemote = remote
	ctx := context.Background()
	if meta, err := remote.GetMeta(ctx, "s1"); err != nil || meta != nil {
		t.Fatalf("GetMeta of a session with none = %v, %v; want nil", meta, err)
	}
	if err := remote.PutMeta(ctx, "s1", delta.JsonObjectOf("name", "one")); err != nil {
		t.Fatal(err)
	}
	if err := remote.PutMeta(ctx, "s1", delta.JsonObjectOf("name", "two")); err != nil {
		t.Fatal(err)
	}
	meta, err := remote.GetMeta(ctx, "s1")
	if err != nil || meta == nil || meta.Value("name") != "two" {
		t.Fatalf("GetMeta = %v, %v; want the last write (two)", meta, err)
	}
	if other, _ := remote.GetMeta(ctx, "s2"); other != nil {
		t.Fatal("a session sees another's meta")
	}
	for _, bad := range []string{"../escape", "a/b", ""} {
		if err := remote.PutMeta(ctx, bad, delta.JsonObjectOf()); err == nil {
			t.Fatalf("PutMeta accepted the session ID %q", bad)
		}
	}
}

func TestFileSyncStateStoreKeepsEachSessionsStateUntilDeleted(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	store := NewFileSyncStateStore(osFiles{}, directory)
	var _ *FileSyncStateStore = store
	ctx := context.Background()
	if state, err := store.Get(ctx, "s1"); err != nil || state != nil {
		t.Fatalf("Get of a session without state = %v, %v; want nil", state, err)
	}
	head, pulling := Seq(5), Seq(9)
	if err := store.Set(ctx, "s1", SyncState{SyncedHead: &head, Pulling: &pulling}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "s2", SyncState{}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Get(ctx, "s1")
	if err != nil || state == nil || state.SyncedHead == nil || *state.SyncedHead != 5 || state.Pulling == nil || *state.Pulling != 9 {
		t.Fatalf("Get(s1) = %+v, %v; want head 5, pulling 9", state, err)
	}
	if other, err := store.Get(ctx, "s2"); err != nil || other == nil || other.SyncedHead != nil || other.Pulling != nil {
		t.Fatalf("Get(s2) = %+v, %v; want empty state", other, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "s1.json.tmp")); !IsNotFound(err) {
		t.Fatalf("the temporary file stays after Set: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "s3.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "s3"); err == nil {
		t.Fatal("Get of a corrupt state file did not fail")
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete of a session without state: %v", err)
	}
	if state, _ := store.Get(ctx, "s1"); state != nil {
		t.Fatalf("Get after Delete = %+v", state)
	}
	if err := store.Set(ctx, "../x", SyncState{}); err == nil {
		t.Fatal("Set accepted a session ID leaving the directory")
	}
}
