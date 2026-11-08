package sync

import (
	"bytes"
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

// countingFiles is osFiles whose readers record their offsets and closes: it observes the production body stream
// (FileSystemSyncRemote's fileBody), the one caller of a SyncFileReader.
type countingFiles struct {
	osFiles
	readers *[]*countingReader
}

type countingReader struct {
	SyncFileReader
	offsets []int64
	closes  int
}

func (reader *countingReader) Read(ctx context.Context, offset, length int64) ([]byte, error) {
	reader.offsets = append(reader.offsets, offset)
	return reader.SyncFileReader.Read(ctx, offset, length)
}

func (reader *countingReader) Close(ctx context.Context) error {
	reader.closes++
	return reader.SyncFileReader.Close(ctx)
}

func (files countingFiles) OpenBinaryReader(ctx context.Context, path string) (SyncFileReader, error) {
	inner, err := files.osFiles.OpenBinaryReader(ctx, path)
	if err != nil {
		return nil, err
	}
	reader := &countingReader{SyncFileReader: inner}
	*files.readers = append(*files.readers, reader)
	return reader, nil
}

func TestASnapshotBodyStreamsThroughASyncFileReaderAndClosesItOnceAtTheEndOrWhenClosedEarly(t *testing.T) {
	ctx := context.Background()
	var readers []*countingReader
	remote := NewFileSystemSyncRemote(countingFiles{readers: &readers}, t.TempDir())
	payload := make([]byte, 3*readChunk+5)
	for index := range payload {
		payload[index] = byte(index)
	}
	if err := remote.PutSnapshot(ctx, "s1", 4, chunked(payload, 1000)); err != nil {
		t.Fatal(err)
	}
	body, err := remote.GetSnapshot(ctx, "s1", 4)
	if err != nil || body == nil {
		t.Fatalf("GetSnapshot = %v, %v", body, err)
	}
	var got []byte
	for {
		chunk, more, err := body.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		got = append(got, chunk...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("the streamed snapshot has %d bytes, want %d the same", len(got), len(payload))
	}
	reader := readers[len(readers)-1]
	if reader.closes != 1 {
		t.Fatalf("the reader was closed %d times at the end of the body, want once", reader.closes)
	}
	for index := 1; index < len(reader.offsets); index++ {
		if reader.offsets[index] <= reader.offsets[index-1] {
			t.Fatalf("read offsets %v do not grow", reader.offsets)
		}
	}
	if err := body.Close(); err != nil || reader.closes != 1 {
		t.Fatalf("Close after the end = %v, closes = %d; want no second close", err, reader.closes)
	}
	early, err := remote.GetSnapshot(ctx, "s1", 4)
	if err != nil || early == nil {
		t.Fatal(err)
	}
	if _, more, err := early.Next(ctx); err != nil || !more {
		t.Fatalf("first chunk = %v, %v", more, err)
	}
	second := readers[len(readers)-1]
	if err := early.Close(); err != nil || second.closes != 1 {
		t.Fatalf("an early Close = %v, closes = %d; want the reader closed once", err, second.closes)
	}
	if missing, err := remote.GetSnapshot(ctx, "s1", 9); err != nil || missing != nil {
		t.Fatalf("a snapshot nobody put = %v, %v; want none", missing, err)
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
