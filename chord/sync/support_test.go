package sync

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// background is Pi's BACKGROUND_CONTEXT.
var background = chordctx.Background()

func noError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// backendAt is a backend over the SQLite file at path, `:memory:` for "", closed with the test.
func backendAt(t testing.TB, path string) *store.Backend {
	t.Helper()
	if path == "" {
		path = ":memory:"
	}
	db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	// A backend no store ever opened (a pull refused before it opens its target) is closed with the test too; a second
	// Close of one a store closed is refused and ignored.
	t.Cleanup(func() { _ = db.Close() })
	return store.SqliteBackend(db)
}

// opened opens a store over backend with the outbox on or off, closed with the test.
func opened(t testing.TB, backend *store.Backend, outbox bool) store.Store {
	t.Helper()
	s, err := store.OpenStore(backend, background, store.StoreOptions{Outbox: &outbox})
	noError(t, err)
	t.Cleanup(func() { _ = s.Close(background) })
	return s
}

// sessionStore is a store with its outbox on.
func sessionStore(t testing.TB) store.Store { return opened(t, backendAt(t, ""), true) }

// chordOf is the concrete store.
func chordOf(s store.Store) *store.ChordStore { return s.(*store.ChordStore) }

var openFence = store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}

// counts is the table every copy writes: a counter per id.
var counts = func() *store.TableDefinition {
	definition, err := store.DefineTable("counts", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("n", store.Int()), store.Col("pad", store.Opt(store.Str()))},
		Key:     []string{"id"},
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

const countsTable = "app.sync:counts"

// rows is one batch of the test's history: rows to put.
type rows []store.Row

func row(id string, n int64) store.Row { return store.Row{"id": id, "n": n} }

// testOwner is the owner of the test's table handle.
type testOwner struct{ scope *struct{} }

func (*testOwner) Label() string               { return "app.sync" }
func (owner *testOwner) Scope() any            { return owner.scope }
func (*testOwner) Queries() *store.LiveQueries { return store.NewLiveQueries() }
func (*testOwner) AssertLive() error           { return nil }
func (*testOwner) Fail(err *data.ChordError)   {}

// commitAll commits each batch of rows to s, preparing the table first (its creation is a commit of its own).
func commitAll(t testing.TB, s store.Store, batches ...rows) {
	t.Helper()
	chord := chordOf(s)
	noError(t, store.PrepareTables([]store.DeclaredTable{{Definition: counts, Table: countsTable}}, chord, openFence, "app.sync", background))
	handle := store.NewTableHandle(counts, countsTable, chord, &testOwner{scope: new(struct{})})
	for _, batch := range batches {
		var staged []store.StagedWrite
		changes := store.NewTableChanges(handle, &staged)
		for _, each := range batch {
			noError(t, changes.Put(each))
		}
		_, err := chord.Commit(staged, openFence, background)
		noError(t, err)
	}
}

// reads is what a copy holds, as one comparable value: every part of its export as JSON text.
func reads(t testing.TB, s store.Store) []string {
	t.Helper()
	export, err := s.Export(background)
	noError(t, err)
	defer func() { _ = export.Close() }()
	var parts []string
	for {
		part, ok, err := export.Next(background)
		noError(t, err)
		if !ok {
			return parts
		}
		text, err := jsstring.MarshalJSON(part)
		noError(t, err)
		parts = append(parts, string(text))
	}
}

// bytesOf is a body's bytes, joined.
func bytesOf(t testing.TB, body SyncBody) []byte {
	t.Helper()
	var out []byte
	defer func() { _ = body.Close() }()
	for {
		chunk, ok, err := body.Next(background)
		noError(t, err)
		if !ok {
			return out
		}
		out = append(out, chunk...)
	}
}

// sliceBody is bytes as a body of chunks of size: decoding must not depend on where chunks split.
type sliceBody struct {
	bytes []byte
	size  int
}

func chunked(bytes []byte, size int) SyncBody { return &sliceBody{bytes: bytes, size: size} }

func (body *sliceBody) Next(context.Context) ([]byte, bool, error) {
	if len(body.bytes) == 0 {
		return nil, false, nil
	}
	n := min(body.size, len(body.bytes))
	chunk := body.bytes[:n]
	body.bytes = body.bytes[n:]
	return chunk, true, nil
}

func (*sliceBody) Close() error { return nil }

// osFiles is SyncFileSystem over the operating system's files, for the sync tests (a host passes its own environment).
type osFiles struct{}

type osReader struct{ file *os.File }

func (reader osReader) Read(_ context.Context, offset, length int64) ([]byte, error) {
	buffer := make([]byte, length)
	n, err := reader.file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buffer[:n], nil
}

func (reader osReader) Close(context.Context) error { return reader.file.Close() }

func (osFiles) JoinPath(_ context.Context, parts []string) (string, error) {
	return filepath.Join(parts...), nil
}
func (osFiles) ReadTextFile(_ context.Context, path string) (string, error) {
	bytes, err := os.ReadFile(path)
	return string(bytes), err
}
func (osFiles) ReadBinaryFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (osFiles) OpenBinaryReader(_ context.Context, path string) (SyncFileReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return osReader{file: file}, nil
}
func (osFiles) WriteFile(_ context.Context, path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}
func (osFiles) AppendFile(_ context.Context, path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	return errors.Join(err, file.Close())
}
func (osFiles) RenameFile(_ context.Context, source, destination string) error {
	return os.Rename(source, destination)
}
func (osFiles) ListDir(_ context.Context, path string) ([]SyncFileInfo, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	infos := make([]SyncFileInfo, len(entries))
	for index, entry := range entries {
		kind := SyncFile
		switch {
		case entry.IsDir():
			kind = SyncDirectory
		case entry.Type()&fs.ModeSymlink != 0:
			kind = SyncSymlink
		}
		infos[index] = SyncFileInfo{Name: entry.Name(), Kind: kind}
	}
	return infos, nil
}
func (osFiles) CreateDir(_ context.Context, path string, recursive bool) error {
	if recursive {
		return os.MkdirAll(path, 0o755)
	}
	return os.Mkdir(path, 0o755)
}
func (osFiles) Remove(_ context.Context, path string, recursive, force bool) error {
	var err error
	if recursive {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if force && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// newRemote is a FileSystemSyncRemote in a directory of the test.
func newRemote(t testing.TB) *FileSystemSyncRemote {
	t.Helper()
	return NewFileSystemSyncRemote(osFiles{}, t.TempDir())
}

// memoryState is a SyncStateStore in memory.
type memoryState struct{ states map[string]SyncState }

func newMemoryState() *memoryState { return &memoryState{states: map[string]SyncState{}} }

func (state *memoryState) Get(_ context.Context, sessionID string) (*SyncState, error) {
	found, ok := state.states[sessionID]
	if !ok {
		return nil, nil
	}
	return &found, nil
}

func (state *memoryState) Set(_ context.Context, sessionID string, next SyncState) error {
	state.states[sessionID] = next
	return nil
}

// rejects fails the test unless err is of target's type.
func isError[T error](t testing.TB, err error) T {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("error = %v (%T), want a %T", err, err, target)
	}
	return target
}

// deltaObject is a JSON object of pairs.
func deltaObject(pairs ...any) *delta.JsonObject { return delta.JsonObjectOf(pairs...) }
