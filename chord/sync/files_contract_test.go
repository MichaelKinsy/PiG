package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Proof of the SyncFileSystem and SyncFileReader contract (files.ts) as the directory-backed remote and state store drive it: the
// operations they call, in the order and with the arguments Pi's filesystem.ts and state.ts call them. A recording environment
// wraps the real one, so the production code (FileSystemSyncRemote, FileSyncStateStore) runs against actual files.

// recordingFiles is osFiles that notes each operation, its path relative to root and its salient argument.
type recordingFiles struct {
	osFiles
	root string
	log  *[]string
	seen map[string]bool
}

type recordingReader struct {
	SyncFileReader
	files recordingFiles
}

func (files recordingFiles) note(operation, path, detail string) {
	files.seen[operation] = true
	*files.log = append(*files.log, strings.Join(strings.Fields(operation+" "+strings.TrimPrefix(path, files.root)+" "+detail), " "))
}

func (reader recordingReader) Read(ctx context.Context, offset, length int64) ([]byte, error) {
	chunk, err := reader.SyncFileReader.Read(ctx, offset, length)
	reader.files.note("Read", "", fmt.Sprintf("offset=%d length=%d got=%d", offset, length, len(chunk)))
	return chunk, err
}

func (reader recordingReader) Close(ctx context.Context) error {
	reader.files.note("Close", "", "")
	return reader.SyncFileReader.Close(ctx)
}

func (files recordingFiles) JoinPath(ctx context.Context, parts []string) (string, error) {
	joined, err := files.osFiles.JoinPath(ctx, parts)
	files.note("JoinPath", "", "")
	return joined, err
}

func (files recordingFiles) ReadTextFile(ctx context.Context, path string) (string, error) {
	files.note("ReadTextFile", path, "")
	return files.osFiles.ReadTextFile(ctx, path)
}

func (files recordingFiles) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	files.note("ReadBinaryFile", path, "")
	return files.osFiles.ReadBinaryFile(ctx, path)
}

func (files recordingFiles) OpenBinaryReader(ctx context.Context, path string, options *OpenBinaryReaderOptions) (SyncFileReader, error) {
	files.note("OpenBinaryReader", path, "")
	reader, err := files.osFiles.OpenBinaryReader(ctx, path, options)
	if err != nil {
		return nil, err
	}
	return recordingReader{SyncFileReader: reader, files: files}, nil
}

func (files recordingFiles) WriteFile(ctx context.Context, path string, content []byte) error {
	files.note("WriteFile", path, fmt.Sprintf("bytes=%d", len(content)))
	return files.osFiles.WriteFile(ctx, path, content)
}

func (files recordingFiles) AppendFile(ctx context.Context, path string, content []byte) error {
	files.note("AppendFile", path, fmt.Sprintf("bytes=%d", len(content)))
	return files.osFiles.AppendFile(ctx, path, content)
}

func (files recordingFiles) RenameFile(ctx context.Context, source, destination string) error {
	files.note("RenameFile", source, "-> "+strings.TrimPrefix(destination, files.root))
	return files.osFiles.RenameFile(ctx, source, destination)
}

func (files recordingFiles) ListDir(ctx context.Context, path string) ([]SyncFileInfo, error) {
	files.note("ListDir", path, "")
	return files.osFiles.ListDir(ctx, path)
}

func (files recordingFiles) CreateDir(ctx context.Context, path string, recursive bool) error {
	files.note("CreateDir", path, fmt.Sprintf("recursive=%t", recursive))
	return files.osFiles.CreateDir(ctx, path, recursive)
}

func (files recordingFiles) Remove(ctx context.Context, path string, recursive, force bool) error {
	files.note("Remove", path, fmt.Sprintf("recursive=%t force=%t", recursive, force))
	return files.osFiles.Remove(ctx, path, recursive, force)
}

// operations are the log's entries, and clears the log.
func operations(log *[]string) []string {
	out := slices.DeleteFunc(slices.Clone(*log), func(entry string) bool { return entry == "JoinPath" })
	*log = nil
	return out
}

func TestFileSystemSyncRemoteCallsTheFileSystemOperationsInPisOrderWithPisArguments(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var log []string
	files := recordingFiles{root: root, log: &log, seen: map[string]bool{}}
	remote := NewFileSystemSyncRemote(files, root)
	const s = "/s1"

	// write: the directory is created recursively, a temporary file is emptied, the body is appended chunk by chunk, and the
	// temporary file is renamed over the name, so a reader never sees half a file (filesystem.ts write).
	if err := remote.PutSnapshot(ctx, "s1", 7, chunked([]byte("abcdefgh"), 3)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CreateDir " + s + "/snapshots recursive=true",
		"WriteFile " + s + "/snapshots/0000000000000007.batch.tmp bytes=0",
		"AppendFile " + s + "/snapshots/0000000000000007.batch.tmp bytes=3",
		"AppendFile " + s + "/snapshots/0000000000000007.batch.tmp bytes=3",
		"AppendFile " + s + "/snapshots/0000000000000007.batch.tmp bytes=2",
		"RenameFile " + s + "/snapshots/0000000000000007.batch.tmp -> " + s + "/snapshots/0000000000000007.batch",
	}
	if got := operations(&log); !slices.Equal(got, want) {
		t.Fatalf("PutSnapshot called\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if content, err := os.ReadFile(filepath.Join(root, "s1", "snapshots", "0000000000000007.batch")); err != nil || string(content) != "abcdefgh" {
		t.Fatalf("the snapshot file = %q, %v; want abcdefgh", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "s1", "snapshots", "0000000000000007.batch.tmp")); !IsNotFound(err) {
		t.Fatalf("the temporary file stays after the rename: %v", err)
	}

	// read: a snapshot streams back through a reader opened once, read at growing offsets until it returns nothing, then closed.
	body, err := remote.GetSnapshot(ctx, "s1", 7)
	if err != nil || body == nil {
		t.Fatalf("GetSnapshot = %v, %v; want a body", body, err)
	}
	if got := string(bytesOf(t, body)); got != "abcdefgh" {
		t.Fatalf("GetSnapshot = %q, want abcdefgh", got)
	}
	want = []string{
		"OpenBinaryReader " + s + "/snapshots/0000000000000007.batch",
		fmt.Sprintf("Read offset=0 length=%d got=8", readChunk),
		fmt.Sprintf("Read offset=8 length=%d got=0", readChunk),
		"Close",
	}
	if got := operations(&log); !slices.Equal(got, want) {
		t.Fatalf("GetSnapshot called\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// join: every path is joined from the root, the session ID and the parts by the environment's own JoinPath; a listing lists
	// the slots and the snapshots directories, and a missing directory is an empty listing, not a failure.
	listing, err := remote.List(ctx, "s1")
	if err != nil || len(listing.Snapshots) != 1 || listing.Snapshots[0] != 7 || len(listing.Batches) != 0 {
		t.Fatalf("List = %+v, %v; want the one snapshot 7", listing, err)
	}
	want = []string{"ListDir " + s + "/slots", "ListDir " + s + "/snapshots"}
	if got := operations(&log); !slices.Equal(got, want) {
		t.Fatalf("List called %v, want %v", got, want)
	}

	// remove: a deleted snapshot is removed as a file (not recursive), and a missing one is no failure (force).
	if err := remote.DeleteSnapshots(ctx, "s1", []Seq{7, 8}); err != nil {
		t.Fatal(err)
	}
	want = []string{
		"Remove " + s + "/snapshots/0000000000000007.batch recursive=false force=true",
		"Remove " + s + "/snapshots/0000000000000008.batch recursive=false force=true",
	}
	if got := operations(&log); !slices.Equal(got, want) {
		t.Fatalf("DeleteSnapshots called %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "s1", "snapshots", "0000000000000007.batch")); !IsNotFound(err) {
		t.Fatalf("the deleted snapshot stays: %v", err)
	}

	// a small file is read whole (ReadBinaryFile), and a session's directories are listed from the root (ListDir).
	if err := remote.PutMeta(ctx, "s1", delta.JsonObjectOf("name", "one")); err != nil {
		t.Fatal(err)
	}
	log = nil
	if meta, err := remote.GetMeta(ctx, "s1"); err != nil || meta == nil || meta.Value("name") != "one" {
		t.Fatalf("GetMeta = %v, %v; want name one", meta, err)
	}
	if got := operations(&log); !slices.Equal(got, []string{"ReadBinaryFile " + s + "/meta.json"}) {
		t.Fatalf("GetMeta called %v, want one ReadBinaryFile of meta.json", got)
	}
	log = nil
	changes, err := remote.Sessions(ctx, nil)
	if err != nil || len(changes.Sessions) != 1 || changes.Sessions[0].ID != "s1" {
		t.Fatalf("Sessions = %+v, %v; want s1", changes, err)
	}
	if got := operations(&log); len(got) == 0 || got[0] != "ListDir" {
		t.Fatalf("Sessions first called %v, want a ListDir of the root", got)
	}
	// Every operation of SyncFileSystem, and the reader's Read, was called by the production code above, and a state store reads its text file.
	if _, err := NewFileSyncStateStore(files, filepath.Join(root, "state")).Get(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"JoinPath", "ReadTextFile", "ReadBinaryFile", "OpenBinaryReader", "Read", "WriteFile", "AppendFile", "RenameFile", "ListDir", "CreateDir", "Remove"} {
		if !files.seen[operation] {
			t.Errorf("no production path called %s", operation)
		}
	}
}

func TestFileSyncStateStoreReadsATextFileAndWritesAThroughATemporaryOne(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var log []string
	store := NewFileSyncStateStore(recordingFiles{root: root, log: &log, seen: map[string]bool{}}, filepath.Join(root, "state"))
	head := Seq(3)
	if err := store.Set(ctx, "s1", SyncState{SyncedHead: &head}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CreateDir /state recursive=true",
		"WriteFile /state/s1.json.tmp bytes=",
		"RenameFile /state/s1.json.tmp -> /state/s1.json",
	}
	got := operations(&log)
	if len(got) != 3 || got[0] != want[0] || !strings.HasPrefix(got[1], want[1]) || got[2] != want[2] {
		t.Fatalf("Set called %v, want %v", got, want)
	}
	state, err := store.Get(ctx, "s1")
	if err != nil || state == nil || state.SyncedHead == nil || *state.SyncedHead != 3 {
		t.Fatalf("Get = %+v, %v; want head 3", state, err)
	}
	if got := operations(&log); !slices.Equal(got, []string{"ReadTextFile /state/s1.json"}) {
		t.Fatalf("Get called %v, want one ReadTextFile", got)
	}
}
