package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/agent/harness/session/testing/conformance"
	"github.com/MichaelKinsy/PiG/ai"
)

func jsonlHeader(id string) session.JsonlStorageHeader {
	return session.JsonlStorageHeader{V: session.JSONLFormatVersion, Kind: "header", ID: id, StorageVersion: session.JSONLStorageVersion, CreatedAt: now, Cwd: "/workspace"}
}
func jsonlOptions(t *testing.T) session.JsonlStorageOptions {
	t.Helper()
	return session.JsonlStorageOptions{FileSystem: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: t.TempDir()}), Path: "session.jsonl", Now: fixedNow}
}
func readFile(t *testing.T, fs harness.FileSystem, path string) string {
	t.Helper()
	text, err := fs.ReadTextFile(background, path)
	mustNoErr(t, err)
	return text
}

func TestJsonlStorageConformance(t *testing.T) {
	sessiontesting.RunConformance(t, conformance.CreateStorageConformance(func() (sessiontesting.StorageFixture, error) {
		options := jsonlOptions(t)
		storage, err := session.CreateJsonlStorage(background, options, jsonlHeader("test"), nil)
		if err != nil {
			return sessiontesting.StorageFixture{}, err
		}
		return sessiontesting.StorageFixture{Storage: storage, Close: func() error { return storage.Close(background) }}, nil
	}))
}

func jsonlRepoAdapter(repo *session.JsonlSessionRepo) conformance.RepoAdapter {
	return conformance.RepoAdapter{Create: func(ctx context.Context, options session.SessionCreateOptions) (session.Session, error) {
		options.Cwd = "/workspace"
		return repo.Create(ctx, options)
	}, Open: repo.Open, List: func(ctx context.Context) ([]session.SessionMetadata, error) { return repo.List(ctx, nil) }, Delete: repo.Delete, Fork: repo.Fork}
}

func TestJsonlSessionRepoConformance(t *testing.T) {
	var repo *session.JsonlSessionRepo
	factory := func() (conformance.RepoAdapter, error) {
		repo = session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: jsonlOptions(t).FileSystem, SessionsRoot: "sessions", Now: fixedNow})
		return jsonlRepoAdapter(repo), nil
	}
	closeRepo := func() error { return repo.Close(background) }
	sessiontesting.RunConformance(t, conformance.CreateSessionRepoConformance(factory, closeRepo))
	sessiontesting.RunConformance(t, conformance.CreateSessionRepoStreamingForkConformance(factory, closeRepo))
}

func TestJsonlStorageRoundTripAndWholeListDeletion(t *testing.T) {
	options := jsonlOptions(t)
	storage, err := session.CreateJsonlStorage(background, options, jsonlHeader("round-trip"), nil)
	mustNoErr(t, err)
	usage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
	events := session.MustList[string]("app.events", "")
	result, err := storage.Commit(background, []session.Write{session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeMessage, Message: userText("hello", 1)}), session.SetValue(session.BranchTip("main"), new("root")), session.InsertUsage(session.UsageRow{ID: "usage", Usage: usage, EntryID: new("root")})})
	mustNoErr(t, err)
	_, err = storage.Commit(background, []session.Write{session.AppendList(events, "before"), session.DeleteList(events), session.AppendList(events, "after")})
	mustNoErr(t, err)
	_, err = storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "name")})
	mustNoErr(t, err)
	lines := strings.Split(strings.TrimSuffix(readFile(t, options.FileSystem, options.Path), "\n"), "\n")
	if len(lines) != 4 || lines[1][0] != '[' || lines[3][0] == '[' {
		t.Fatalf("transaction framing: %v", lines)
	}
	mustNoErr(t, storage.Close(background))
	reopened, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	defer func() { _ = reopened.Close(background) }()
	entries, err := reopened.GetEntries(background, []string{"root"})
	mustNoErr(t, err)
	if entries["root"].Seq != result.FirstSeq || entries["root"].Timestamp != now {
		t.Fatalf("entry=%+v", entries["root"])
	}
	list, err := session.ReadList(background, reopened, events, nil)
	mustNoErr(t, err)
	assertJSON(t, list, []session.ListElement[string]{{Seq: 6, Value: "after"}})
	next, err := reopened.Commit(background, nil)
	mustNoErr(t, err)
	if next.FirstSeq != 8 {
		t.Fatalf("next=%d", next.FirstSeq)
	}
	assertJSON(t, next.Stats, session.SessionStats{MessageCount: 1, Usage: usage})
}

func TestJsonlTornTailAndCompleteCorruption(t *testing.T) {
	for _, suffix := range []string{`{"kind":"value","op":"set","seq":2,"namespace":"pi.session.name","key":"","value":"lost"}`, `[{"kind":"list","op":"append","seq":2,"namespace":"events","key":"","value":"lost"}]`, "not-json\n", `{"kind":"register","op":"set","seq":2}` + "\n", `{"kind":"nope","seq":2}` + "\n", "not-json\n{}\n"} {
		t.Run(suffix, func(t *testing.T) {
			options := jsonlOptions(t)
			storage, err := session.CreateJsonlStorage(background, options, jsonlHeader("torn"), []session.Write{session.SetValue(session.SessionName, "kept")})
			mustNoErr(t, err)
			mustNoErr(t, storage.Close(background))
			prefix := readFile(t, options.FileSystem, options.Path)
			mustNoErr(t, options.FileSystem.AppendFile(background, options.Path, []byte(suffix)))
			reopened, err := session.OpenJsonlStorage(background, options)
			if strings.HasSuffix(suffix, "\n") {
				wantErrContains(t, err, "line 3")
				if got := readFile(t, options.FileSystem, options.Path); got != prefix+suffix {
					t.Fatal("corruption rewritten")
				}
				return
			}
			mustNoErr(t, err)
			defer func() { _ = reopened.Close(background) }()
			if got := readFile(t, options.FileSystem, options.Path); got != prefix {
				t.Fatal("torn tail not removed")
			}
			value, err := session.GetValue(background, reopened, session.SessionName)
			mustNoErr(t, err)
			if value.Value != "kept" {
				t.Fatalf("value=%+v", value)
			}
			next, err := reopened.Commit(background, []session.Write{session.SetValue(session.SessionName, "after")})
			mustNoErr(t, err)
			if next.FirstSeq != 2 {
				t.Fatalf("next=%d", next.FirstSeq)
			}
		})
	}
}

func TestJsonlRejectsUnterminatedHeaderAndFutureStorageWithoutRepair(t *testing.T) {
	options := jsonlOptions(t)
	header := jsonlHeader("future")
	header.StorageVersion++
	for _, content := range []string{string(mustJSON(t, header)), string(mustJSON(t, header)) + "\n{torn"} {
		mustNoErr(t, options.FileSystem.WriteFile(background, options.Path, []byte(content)))
		_, err := session.OpenJsonlStorage(background, options)
		if err == nil {
			t.Fatal("invalid storage accepted")
		}
		if got := readFile(t, options.FileSystem, options.Path); got != content {
			t.Fatal("invalid storage rewritten")
		}
	}
}

type failingJsonlFS struct {
	harness.FileSystem
	method  string
	failure error
}

func (fs *failingJsonlFS) WriteFile(ctx context.Context, path string, content []byte) error {
	if fs.method == "write" {
		return fs.failure
	}
	return fs.FileSystem.WriteFile(ctx, path, content)
}
func (fs *failingJsonlFS) AppendFile(ctx context.Context, path string, content []byte) error {
	if fs.method == "append" {
		return fs.failure
	}
	return fs.FileSystem.AppendFile(ctx, path, content)
}
func (fs *failingJsonlFS) RenameFile(ctx context.Context, source, destination string) error {
	if fs.method == "rename" {
		return fs.failure
	}
	return fs.FileSystem.RenameFile(ctx, source, destination)
}

func TestJsonlAtomicPublicationFailuresAndRetry(t *testing.T) {
	for _, method := range []string{"write", "append", "rename", "callback"} {
		t.Run(method, func(t *testing.T) {
			options := jsonlOptions(t)
			mustNoErr(t, options.FileSystem.WriteFile(background, options.Path, []byte("original")))
			failure := errors.New("injected I/O failure")
			fs := &failingJsonlFS{FileSystem: options.FileSystem, method: method, failure: failure}
			err := session.PublishFileAtomically(background, fs, options.Path, func(appendText func(string) error) error {
				if err := appendText("partial"); err != nil {
					return err
				}
				if got := readFile(t, options.FileSystem, options.Path); got != "original" {
					t.Fatal("published before writer complete")
				}
				if method == "callback" {
					return failure
				}
				return nil
			})
			if !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
			if got := readFile(t, options.FileSystem, options.Path); got != "original" {
				t.Fatal("destination changed on failure")
			}
			exists, err := options.FileSystem.Exists(background, options.Path+".tmp")
			mustNoErr(t, err)
			if exists {
				t.Fatal("temporary file leaked")
			}
			fs.method = ""
			mustNoErr(t, session.PublishFileAtomically(background, fs, options.Path, func(appendText func(string) error) error { return appendText("retry") }))
			if got := readFile(t, options.FileSystem, options.Path); got != "retry" {
				t.Fatalf("retry=%q", got)
			}
		})
	}
}

func TestJsonlRepositoryCwdDiscoveryAndEncodedIDs(t *testing.T) {
	fs := jsonlOptions(t).FileSystem
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: fs, SessionsRoot: "sessions", Now: fixedNow})
	defer func() { _ = repo.Close(background) }()
	first, err := repo.Create(background, session.SessionCreateOptions{ID: "shared /+!", Cwd: "/workspace-a", ParentSessionID: "parent"})
	mustNoErr(t, err)
	second, err := repo.Create(background, session.SessionCreateOptions{ID: "shared /+!", Cwd: "/workspace-b"})
	mustNoErr(t, err)
	defer func() { _ = first.Close(background); _ = second.Close(background) }()
	if !strings.HasSuffix(first.Metadata().Path, "_shared%20%2F%2B!.jsonl") {
		t.Fatalf("path=%s", first.Metadata().Path)
	}
	listed, err := repo.List(background, nil)
	mustNoErr(t, err)
	// Upstream stores the cwd resolved by the FileSystem; on Windows Node's
	// path.resolve puts a drive-less rooted path on the current drive.
	wantA, wantB := "/workspace-a", "/workspace-b"
	if runtime.GOOS == "windows" {
		processDir, err := os.Getwd()
		mustNoErr(t, err)
		drive := filepath.VolumeName(processDir)
		wantA, wantB = drive+`\workspace-a`, drive+`\workspace-b`
	}
	if len(listed) != 2 || listed[0].Cwd != wantA || listed[1].Cwd != wantB {
		t.Fatalf("listed=%+v", listed)
	}
	filtered, err := repo.List(background, &session.JsonlSessionListOptions{Cwd: new("/workspace-a")})
	mustNoErr(t, err)
	assertJSON(t, filtered, []session.SessionMetadata{first.Metadata()})
	var header session.JsonlStorageHeader
	mustNoErr(t, json.Unmarshal([]byte(strings.TrimSuffix(readFile(t, fs, first.Metadata().Path), "\n")), &header))
	if header.ParentSessionID != "parent" {
		t.Fatalf("header=%+v", header)
	}
}

func TestJsonlAppendFailureDoesNotAdvanceLiveState(t *testing.T) {
	options := jsonlOptions(t)
	fs := &failingJsonlFS{FileSystem: options.FileSystem, failure: errors.New("append failed")}
	options.FileSystem = fs
	storage, err := session.CreateJsonlStorage(background, options, jsonlHeader("failure"), nil)
	mustNoErr(t, err)
	defer func() { _ = storage.Close(background) }()
	original := readFile(t, fs, options.Path)
	fs.method = "append"
	_, err = storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "failed")})
	if !errors.Is(err, fs.failure) {
		t.Fatalf("error=%v", err)
	}
	name, err := session.GetValue(background, storage, session.SessionName)
	mustNoErr(t, err)
	if name != nil || readFile(t, fs, options.Path) != original {
		t.Fatal("failed append changed state")
	}
	fs.method = ""
	result, err := storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "retry")})
	mustNoErr(t, err)
	if result.FirstSeq != 1 {
		t.Fatalf("sequence advanced: %+v", result)
	}
}

func TestJsonlHeaderValidation(t *testing.T) {
	valid := map[string]any{"v": 4, "kind": "header", "id": "test", "storageVersion": 1, "createdAt": 0, "cwd": "/workspace"}
	for _, mutation := range []struct {
		key   string
		value any
	}{{"v", 5}, {"kind", "entry"}, {"id", nil}, {"cwd", 7}, {"storageVersion", 0}, {"storageVersion", 1.5}, {"createdAt", -1}, {"nextSeq", 0}, {"nextSeq", 9007199254740992.0}, {"parentSessionId", nil}, {"legacyParentSessionPath", true}} {
		t.Run(mutation.key+string(mustJSON(t, mutation.value)), func(t *testing.T) {
			fields := map[string]any{}
			maps.Copy(fields, valid)
			fields[mutation.key] = mutation.value
			if _, err := session.ParseJsonlSessionHeader(string(mustJSON(t, fields))); err == nil {
				t.Fatalf("accepted %v", fields)
			}
		})
	}
	parsed, err := session.ParseJsonlSessionHeader(string(mustJSON(t, valid)))
	mustNoErr(t, err)
	if parsed.Header == nil || parsed.Header.ID != "test" {
		t.Fatalf("parsed=%+v", parsed)
	}
}
