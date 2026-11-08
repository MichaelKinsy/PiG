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

// countingFiles is osFiles that counts the closes of the readers it hands out: what a body's close must release.
type countingFiles struct {
	osFiles
	closes *int
}

type countingReader struct {
	SyncFileReader
	closes *int
}

func (reader countingReader) Close(ctx context.Context) error {
	*reader.closes++
	return reader.SyncFileReader.Close(ctx)
}

func (files countingFiles) OpenBinaryReader(ctx context.Context, path string, options *OpenBinaryReaderOptions) (SyncFileReader, error) {
	reader, err := files.osFiles.OpenBinaryReader(ctx, path, options)
	if err != nil {
		return nil, err
	}
	return countingReader{reader, files.closes}, nil
}

// The body the remote streams back from a file (FileSystemSyncRemote.stream, production code) reads it at growing offsets, closes
// the reader once at the end or when the body is closed, and answers nil for a missing file.
func TestAStreamedBodyReadsAFileInChunksAndReleasesItsReaderOnce(t *testing.T) {
	ctx := context.Background()
	closes := 0
	remote := NewFileSystemSyncRemote(countingFiles{closes: &closes}, t.TempDir())
	path, err := remote.path(ctx, "s1", "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := make([]byte, 2*readChunk+10)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := remote.stream(ctx, "s1", "body")
	if err != nil || body == nil {
		t.Fatalf("stream = %v, %v; want a body", body, err)
	}
	var got []byte
	chunks := 0
	for {
		chunk, more, err := body.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		chunks++
		got = append(got, chunk...)
	}
	if string(got) != string(content) || chunks != 3 {
		t.Fatalf("read %d bytes in %d chunks, want the file's %d in 3", len(got), chunks, len(content))
	}
	if closes != 1 {
		t.Fatalf("the reader closed %d times at the end of the body, want once", closes)
	}
	if err := body.Close(); err != nil || closes != 1 {
		t.Fatalf("closing a finished body = %v with %d closes, want none more", err, closes)
	}
	again, err := remote.stream(ctx, "s1", "body")
	if err != nil {
		t.Fatal(err)
	}
	if chunk, more, err := again.Next(ctx); err != nil || !more || len(chunk) != readChunk {
		t.Fatalf("first chunk = %d bytes, %v, %v; want %d", len(chunk), more, err, readChunk)
	}
	if err := again.Close(); err != nil || closes != 2 {
		t.Fatalf("closing a body part way = %v with %d closes, want 2", err, closes)
	}
	if _, more, err := again.Next(ctx); more || err != nil {
		t.Fatalf("a closed body still reads: %v, %v", more, err)
	}
	if err := again.Close(); err != nil || closes != 2 {
		t.Fatalf("closing a body twice released the reader again: %d closes", closes)
	}
	if missing, err := remote.stream(ctx, "s1", "absent"); missing != nil || err != nil {
		t.Fatalf("stream of a missing file = %v, %v; want nil", missing, err)
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
