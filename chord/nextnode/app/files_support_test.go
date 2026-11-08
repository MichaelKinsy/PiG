package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

var background = context.Background()

// errorLog collects the host's background errors (Pi's `errors` array).
type errorLog struct {
	mu   sync.Mutex
	list []host.HostError
}

func (e *errorLog) push(err host.HostError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, err)
}

func (e *errorLog) all() []host.HostError {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]host.HostError(nil), e.list...)
}

// fixture is a host on the store in `dir/store.sqlite` with the files facet over `dir/files`: the Go form of `testApp`'s
// server host (the app kit is not ported, the facet is what the kit's server loads).
type fixture struct {
	t      *testing.T
	dir    string
	store  *store.ChordStore
	host   host.Host
	files  *host.Reference
	errors *errorLog

	// lastPut is the millisecond of the latest put: the next one waits for the clock to pass it, so no two versions share a
	// `created` (Pi's awaits take more than a millisecond between puts; Go's do not, and the summary orders by `created`).
	lastPut int64
}

// fixtureOptions are what a test varies: the host directory (to reopen one), the caps and the uploads under way.
type fixtureOptions struct {
	Dir     string
	Caps    FilesCaps
	Uploads int
	// Extra loads more facets on the same host.
	Extra []host.HostFacet
}

func openStoreAt(t testing.TB, path string) *store.ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), background, store.StoreOptions{})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return opened.(*store.ChordStore)
}

func filesOf(t *testing.T, options ...fixtureOptions) *fixture {
	t.Helper()
	var opts fixtureOptions
	if len(options) > 0 {
		opts = options[0]
	}
	dir := opts.Dir
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	chordStore := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	errs := &errorLog{}
	h, err := host.CreateHost(host.HostOptions{Store: chordStore, OnError: errs.push})
	if err != nil {
		t.Fatal(err)
	}
	facets := append([]host.HostFacet{{Name: "chord.app.files", Facet: FilesFacet(filepath.Join(dir, "files"), opts.Caps, opts.Uploads)}}, opts.Extra...)
	if _, err := h.Load(facets, background); err != nil {
		t.Fatal(err)
	}
	files, err := h.Get(background, Files, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dir: dir, store: chordStore, host: h, files: files, errors: errs}
}

// close releases the files reference, closes the host and the store.
func (f *fixture) close() {
	f.t.Helper()
	f.files.Release()
	if err := f.host.Close(background); err != nil {
		f.t.Fatalf("host close: %v", err)
	}
	ctx, cancel := context.WithTimeout(background, 10*time.Second)
	defer cancel()
	if err := f.store.Close(ctx); err != nil {
		f.t.Fatalf("store close: %v", err)
	}
}

// blobDir is the blobs of the host's files.
func (f *fixture) blobDir() string { return filepath.Join(f.dir, "files", "blobs") }

// call is a call of the host's `Files`.
func (f *fixture) call(method string, args ...any) (delta.JsonValue, error) {
	f.t.Helper()
	return f.files.Call(method, delta.JsonObjectOf(args...), background)
}

// must is a call that must succeed.
func (f *fixture) must(method string, args ...any) delta.JsonValue {
	f.t.Helper()
	value, err := f.call(method, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
	return value
}

// callRaw is a call whose argument is not an object (`remove`, `finish`, `uploaded`, `cancel`, `file`, `removeSpace`).
func (f *fixture) callRaw(method string, arg delta.JsonValue) (delta.JsonValue, error) {
	f.t.Helper()
	return f.files.Call(method, arg, background)
}

func (f *fixture) mustRaw(method string, arg delta.JsonValue) delta.JsonValue {
	f.t.Helper()
	value, err := f.callRaw(method, arg)
	if err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
	return value
}

// put writes text at path of space `shared` (the write's other fields in extra, a key/value list), written by `test`.
func (f *fixture) put(path, text string, extra ...any) *delta.JsonObject {
	f.t.Helper()
	return f.putIn("shared", path, text, extra...)
}

// putIn is put in space.
func (f *fixture) putIn(space, path, text string, extra ...any) *delta.JsonObject {
	f.t.Helper()
	for time.Now().UnixMilli() <= f.lastPut {
		runtime.Gosched()
	}
	value, err := f.putErr(space, path, []byte(text), extra...)
	f.lastPut = time.Now().UnixMilli()
	if err != nil {
		f.t.Fatalf("put %s:%s: %v", space, path, err)
	}
	return value.(*delta.JsonObject)
}

// putErr is a put that may be refused; the write has mime `text/plain` and source `test` unless extra names them.
func (f *fixture) putErr(space, path string, bytes []byte, extra ...any) (delta.JsonValue, error) {
	f.t.Helper()
	write := delta.JsonObjectOf("space", space, "path", path, "mime", "text/plain", "source", "test")
	for index := 0; index+1 < len(extra); index += 2 {
		write.Set(extra[index].(string), extra[index+1])
	}
	write.Set("bytes", bytes)
	return f.files.Call("put", write, background)
}

// text is the text of version id.
func (f *fixture) text(id string) string {
	f.t.Helper()
	return string(f.must("read", "id", id, "offset", float64(0), "length", float64(1<<20)).([]byte))
}

// names are the names of a folder's entries (`folder/` for folders).
func (f *fixture) names(folder string, space ...string) []string {
	f.t.Helper()
	which := "shared"
	if len(space) > 0 {
		which = space[0]
	}
	page := f.must("list", "space", which, "folder", folder).(*delta.JsonObject)
	return entryNames(page.Value("items"), true)
}

// entryNames are the paths of listing entries (`folder/` for folders when slash).
func entryNames(items any, slash bool) []string {
	names := []string{}
	for _, item := range items.([]any) {
		entry := item.(*delta.JsonObject)
		if entry.Value("kind") == "folder" {
			path := entry.Value("folder").(*delta.JsonObject).Value("path").(string)
			if slash {
				path += "/"
			}
			names = append(names, path)
		} else {
			names = append(names, entry.Value("file").(*delta.JsonObject).Value("path").(string))
		}
	}
	return names
}

// blobFiles are the blob files on disk.
func (f *fixture) blobFiles() []string {
	entries, err := os.ReadDir(f.blobDir())
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// summary is the space's summary, nil when it has none.
func (f *fixture) summary(space string) *delta.JsonObject {
	f.t.Helper()
	value, found, err := f.files.Keyed("summary").Get(background, space)
	if err != nil {
		f.t.Fatalf("summary: %v", err)
	}
	if !found {
		return nil
	}
	return value.(*delta.JsonObject)
}

// refused is the ChordError of a call that failed: the test fails when it did not.
func refused(t testing.TB, err error) *data.ChordError {
	t.Helper()
	if err == nil {
		t.Fatal("the call was not refused")
	}
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("error = %T %v, want a ChordError", err, err)
	}
	return chordErr
}

// wantCode fails the test unless err is a ChordError of code (and reason when given).
func wantCode(t testing.TB, err error, code data.ChordErrorCode, reason ...data.ChordErrorReason) *data.ChordError {
	t.Helper()
	chordErr := refused(t, err)
	if chordErr.Code != code || (len(reason) > 0 && chordErr.Reason != reason[0]) {
		t.Fatalf("error = %s/%s %q, want %s %v", chordErr.Code, chordErr.Reason, chordErr.Message, code, reason)
	}
	return chordErr
}

// jsonOf is the JSON text of v.
func jsonOf(t testing.TB, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// num is a JSON number of an object member.
func num(object *delta.JsonObject, key string) float64 { return object.Value(key).(float64) }

// str is a string member.
func str(object *delta.JsonObject, key string) string { return object.Value(key).(string) }

// eventually polls cond every 10 ms until it holds, as the Pi tests' `while (...) await sleep(10)` loops do.
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pending is a call started and not yet awaited (Pi's Promise of `files.append(...)`).
type pending struct {
	t    *testing.T
	call *host.PendingCall
	err  error
}

// start starts a call of the host's `Files` in the order of the starts, without awaiting it.
func (f *fixture) start(method string, args ...any) *pending {
	f.t.Helper()
	call, err := f.files.Start(method, delta.JsonObjectOf(args...), background)
	return &pending{t: f.t, call: call, err: err}
}

// wait is the answer of a started call.
func (p *pending) wait() (delta.JsonValue, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.call.Wait(background)
}
