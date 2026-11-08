package sessions_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagents "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// recordedWorkers is the session workers component of a test server: no worker runs (a stopped worker only gets the
// catalog update of a relocation), and the calls the sessions plugin makes are recorded in order.
type recordedWorkers struct {
	p2runtime.NoWorkers
	mu    sync.Mutex
	calls []string
}

func (w *recordedWorkers) record(call string) {
	w.mu.Lock()
	w.calls = append(w.calls, call)
	w.mu.Unlock()
}

func (w *recordedWorkers) log() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.calls)
}

func (w *recordedWorkers) Relocate(ctx context.Context, session string, steps p2runtime.RelocationSteps) error {
	w.record("relocate " + session)
	return steps.Update(ctx)
}

func (w *recordedWorkers) Pause(ctx context.Context, session string, _ func(context.Context) (string, error), work func(context.Context) error, doing string) error {
	w.record(doing + " " + session)
	return work(ctx)
}

func (w *recordedWorkers) Suspend(_ context.Context, session string) error {
	w.record("suspend " + session)
	return nil
}

func (w *recordedWorkers) Resume(_ context.Context, session string) error {
	w.record("resume " + session)
	return nil
}

// fakeResources is the model configuration of a test server: only its models are read.
type fakeResources struct {
	modelscontract.Resources
	models *ai.Models
}

func (r fakeResources) Models() *ai.Models { return r.models }

// sshManager resolves working directories on environments by prefixing them.
type sshManager struct{}

func (sshManager) WorkingDirectory(_ context.Context, env, cwd string, create bool) (string, error) {
	if create {
		return "/" + env + "/created" + cwd, nil
	}
	return "/" + env + cwd, nil
}

// ListDirectory is never used by these tests.
func (sshManager) ListDirectory(context.Context, string, string) (p2runtime.DirectoryListing, error) {
	return p2runtime.DirectoryListing{}, nil
}

// Connection is never used by these tests.
func (sshManager) Connection(string, *chordjson.Object) *env.Connection { return nil }

// inert are methods of a stubbed local object that nothing under test calls.
func inert(names ...string) map[string]p2test.StubMethod {
	methods := map[string]p2test.StubMethod{}
	for _, name := range names {
		methods[name] = func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	}
	return methods
}

type sessionsFixture struct {
	*p2test.TestHost
	dataDir string
	workers *recordedWorkers
	model   *ai.Model
	// defaultModel, configs and children drive the stubs of the neighbouring plugins.
	defaultModel *p2test.Cell
	configs      *p2test.Cell
	children     *p2test.Cell
}

func newSessionsFixture(t *testing.T, extra ...host.Facet) *sessionsFixture {
	t.Helper()
	return newSessionsFixtureAt(t, t.TempDir(), extra...)
}

// newSessionsFixtureAt is the fixture over the data directory dir (a catalog it may already hold).
func newSessionsFixtureAt(t *testing.T, dir string, extra ...host.Facet) *sessionsFixture {
	t.Helper()
	p2test.CheckLeaks(t, 0)
	fixture := &sessionsFixture{dataDir: dir, workers: &recordedWorkers{}}
	fakeModels := models.CreateFakeModels(ai.CreateModelsOptions{})
	fixture.model = fakeModels.GetModels()[0]
	neighbours := host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		if err := modelscontract.ServeModelResources(facet); err != nil {
			return err
		}
		if err := envcontract.ServeEnvManagers(facet); err != nil {
			return err
		}
		if err := self.Contribute(modelscontract.ModelResources, &modelscontract.ModelResourcesValue{Resources: func() modelscontract.Resources { return fakeResources{models: fakeModels} }}); err != nil {
			return err
		}
		return self.Contribute(envcontract.EnvManagers, &envcontract.EnvManagerParts{Manager: sshManager{}})
	})
	info := p2test.Stub{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{State: map[string]any{"info": delta.JsonObjectOf("app", "/app", "dataDir", fixture.dataDir, "fake", true, "allowedHosts", []any{}, "port", float64(1))}, Methods: inert("revokeOwners", "revokeGuests")}
	}}
	modelConfig := p2test.Stub{Contract: modelscontract.ModelConfig, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		fixture.defaultModel = self.Cell(delta.JsonObjectOf("models", []any{}, "defaultModel", nil, "errors", []any{}))
		return p2test.StubMembers{State: map[string]any{"summary": fixture.defaultModel, "snapshot": delta.NewJsonObject(0)}, Methods: inert("setDefault", "setEnabledModels")}
	}}
	envService := p2test.Stub{Contract: envcontract.EnvService, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		fixture.configs = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{State: map[string]any{"configs": fixture.configs, "tests": delta.NewJsonObject(0), "failure": nil}, Methods: inert("test", "reload")}
	}}
	childStore := p2test.Stub{Contract: subagents.ChildStore, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		fixture.children = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{State: map[string]any{"children": fixture.children}, Methods: inert("report")}
	}}
	fixture.TestHost = p2test.NewTestHost(t, p2test.TestHostOptions{
		Facets: append([]host.Facet{neighbours, sessions.Server}, extra...),
		Stubs:  []p2test.Stub{info, modelConfig, envService, childStore},
		Server: &p2runtime.ServerParts{Workers: fixture.workers},
		Store:  openStore(t),
	})
	return fixture
}

func (f *sessionsFixture) ownerSessions(t *testing.T) *host.Reference {
	t.Helper()
	ref, err := f.Client(owner).Hold(p2test.Background, contract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	state, err := p2test.UntilNative(ref.State(), func(state *host.RefState) bool {
		return state.Kind == host.RefStateLive || state.Kind == host.RefStateFailed || state.Kind == host.RefStateEnded
	}, 15*time.Second)
	if err != nil {
		t.Fatalf("Sessions never settled: %v; host errors: %v", err, f.Errors())
	}
	if state.Kind != host.RefStateLive {
		t.Fatalf("Sessions is %v: %v %v; host errors: %v", state.Kind, state.Error, state.Cause, f.Errors())
	}
	return ref
}

// create makes a session in a fresh working directory.
func (f *sessionsFixture) create(t *testing.T, sessionsRef *host.Reference, fields ...any) *delta.JsonObject {
	t.Helper()
	args := delta.JsonObjectOf("cwd", t.TempDir())
	for i := 0; i+1 < len(fields); i += 2 {
		args.Set(fields[i].(string), fields[i+1])
	}
	row, err := sessionsRef.Call("create", args, p2test.Background)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return object(row)
}

func (f *sessionsFixture) modelArg() *delta.JsonObject {
	return delta.JsonObjectOf("provider", f.model.Provider.ID(), "modelId", f.model.ID)
}

// catalogRow is the server's catalog as the plugin's own objects read it, what the owner's call committed before it
// answered.
func (f *sessionsFixture) catalogRow(t *testing.T, id string) *delta.JsonObject {
	t.Helper()
	catalog, err := f.Host.Get(p2test.Background, contract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Release()
	if err := p2runtime.WhenLive(p2test.Background, catalog); err != nil {
		t.Fatal(err)
	}
	rows, err := catalog.Member("sessions").Value()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows.([]any) {
		if object(row).Value("id") == id {
			return object(row)
		}
	}
	return nil
}

func TestCreatesASessionUnderTheClientsIDAndARetryAnswersTheSameRow(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	if err := f.SetCell(f.defaultModel, delta.JsonObjectOf("models", []any{}, "errors", []any{},
		"defaultModel", delta.JsonObjectOf("provider", f.model.Provider.ID(), "modelId", f.model.ID, "thinkingLevel", "low"))); err != nil {
		t.Fatal(err)
	}
	const id = "0b1f2f6e-7d6c-4a5b-9a39-0c4d5e6f7a8b"
	created := f.create(t, owner, "id", id, "title", "  Chosen   title ")
	if created.Value("id") != id || created.Value("title") != "Chosen title" {
		t.Fatalf("created = %s", marshal(t, created))
	}
	// The default model and its level, an explicit title (never replaced automatically), the placeholders of the rest.
	if marshal(t, created.Value("model")) != `{"provider":"`+f.model.Provider.ID()+`","modelId":"`+f.model.ID+`"}` || created.Value("thinkingLevel") != "low" {
		t.Fatalf("model %s level %v", marshal(t, created.Value("model")), created.Value("thinkingLevel"))
	}
	retried := f.create(t, owner, "id", id, "title", "Chosen")
	if marshal(t, retried) != marshal(t, created) {
		t.Fatalf("a retry answered another row: %s", marshal(t, retried))
	}
	if f.catalogRow(t, id) == nil {
		t.Fatal("the catalog lacks the row the call answered")
	}
	// An untitled session is a placeholder an automatic title may replace.
	untitled := f.create(t, owner)
	if untitled.Value("title") != "New session" || untitled.Value("thinkingLevel") != "low" {
		t.Fatalf("untitled = %s", marshal(t, untitled))
	}
}

func TestCreateChecksWhatItTakes(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	call := func(args *delta.JsonObject) error {
		_, err := owner.Call("create", args, p2test.Background)
		return err
	}
	// No model chosen and none configured.
	if err := call(delta.JsonObjectOf("cwd", t.TempDir())); err == nil || !strings.Contains(err.Error(), "Select a model") {
		t.Fatalf("without a model: %v", err)
	}
	// A model the configuration does not know never reaches a worker.
	if err := call(delta.JsonObjectOf("cwd", t.TempDir(), "model", delta.JsonObjectOf("provider", "nobody", "modelId", "nothing"))); err == nil || !strings.Contains(err.Error(), "Unknown model") {
		t.Fatalf("unknown model: %v", err)
	}
	if err := call(delta.JsonObjectOf("cwd", "relative/path", "model", f.modelArg())); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative cwd: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	// As Node's fs reports it: `ENOENT: no such file or directory, stat '<path>'`.
	if err := call(delta.JsonObjectOf("cwd", missing, "model", f.modelArg())); err == nil || err.Error() != "ENOENT: no such file or directory, stat '"+missing+"'" {
		t.Errorf("a missing directory: %v", err)
	}
	// `createDirectory` makes it.
	if err := call(delta.JsonObjectOf("cwd", missing, "createDirectory", true, "model", f.modelArg())); err != nil {
		t.Fatalf("createDirectory: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("directory: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := call(delta.JsonObjectOf("cwd", file, "model", f.modelArg())); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file: %v", err)
	}
	// `createDirectory` under a file: Node's `fs.mkdirSync(.., {recursive: true})` message.
	under := filepath.Join(file, "sub")
	if err := call(delta.JsonObjectOf("cwd", under, "createDirectory", true, "model", f.modelArg())); err == nil || err.Error() != "ENOTDIR: not a directory, mkdir '"+under+"'" {
		t.Fatalf("a directory under a file: %v", err)
	}
	// An environment's directory is the manager's answer.
	row := f.create(t, owner, "cwd", "/proj", "env", "box", "model", f.modelArg())
	if row.Value("cwd") != "/box/proj" || row.Value("env") != "box" {
		t.Fatalf("env session = %s", marshal(t, row))
	}
}

func TestArchivesAtOnceAndRunsTheCleanupAsAnOperation(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	row := f.create(t, owner, "model", f.modelArg())
	id := row.Value("id").(string)
	if _, err := owner.Call("archive", delta.JsonObjectOf("id", id), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if f.catalogRow(t, id).Value("archivedAt") == nil {
		t.Fatal("not archived when the call answered")
	}
	// Restored right away: the running archive's worker stop lands before the restore.
	if _, err := owner.Call("restore", delta.JsonObjectOf("id", id), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if f.catalogRow(t, id).Value("archivedAt") != nil {
		t.Fatal("not restored")
	}
	if _, err := p2test.Until(owner.Member("operations"), func(value delta.JsonValue) bool { return len(value.([]any)) == 0 }, 0); err != nil {
		t.Fatalf("operations: %v", err)
	}
	if calls := f.workers.log(); !slices.Equal(calls, []string{"suspend " + id, "resume " + id}) {
		t.Fatalf("worker calls = %v", calls)
	}
}

func TestRejectsAMoveToAMissingDirectoryAtOnceAndRunsNothing(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	id := f.create(t, owner, "model", f.modelArg()).Value("id")
	_, err := owner.Call("relocate", delta.JsonObjectOf("id", id, "cwd", filepath.Join(t.TempDir(), "missing"), "env", nil), p2test.Background)
	if err == nil || !strings.Contains(err.Error(), "ENOENT: no such file or directory") {
		t.Fatalf("relocate error = %v", err)
	}
	if list, _ := owner.Member("operations").Value(); len(list.([]any)) != 0 {
		t.Fatalf("operations = %s", marshal(t, list))
	}
	if len(f.workers.log()) != 0 {
		t.Fatalf("workers were called: %v", f.workers.log())
	}
}

func TestRelocatesAStoppedSessionThroughTheCatalog(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	id := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	target := t.TempDir()
	if _, err := owner.Call("relocate", delta.JsonObjectOf("id", id, "cwd", target, "env", nil), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(owner.Member("sessions"), func(value delta.JsonValue) bool {
		for _, row := range value.([]any) {
			if object(row).Value("id") == id && object(row).Value("cwd") == target {
				return true
			}
		}
		return false
	}, 0); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if _, err := p2test.Until(owner.Member("operations"), func(value delta.JsonValue) bool { return len(value.([]any)) == 0 }, 0); err != nil {
		t.Fatal(err)
	}
	// The agent's move (SessionMoves) answers why it was refused, nothing when it moved.
	agent, err := f.Host.Get(p2test.Background, contract.SessionMoves, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Release()
	again := t.TempDir()
	answer, err := agent.Call("relocate", delta.JsonObjectOf("id", id, "cwd", again, "env", nil), p2test.Background)
	if err != nil || marshal(t, answer) != `{"refused":null}` {
		t.Fatalf("move = %s, %v", marshal(t, answer), err)
	}
	if f.catalogRow(t, id).Value("cwd") != again {
		t.Fatal("the agent's move was not recorded")
	}
}

func TestClonesTheSessionDirectoryUnderANewRow(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	source := f.create(t, owner, "title", "Source", "model", f.modelArg())
	id := source.Value("id").(string)
	write(t, filepath.Join(f.dataDir, "sessions", id, "store.sqlite"), "store")
	write(t, filepath.Join(f.dataDir, "sessions", id, "files", "blobs", "ab", "blob"), "blob")

	const copyID = "5c0d7a0e-2d3b-4f5a-8b6c-1a2b3c4d5e6f"
	row, err := owner.Call("clone", delta.JsonObjectOf("id", id, "copy", copyID), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if object(row).Value("id") != copyID || object(row).Value("title") != "Source (copy)" {
		t.Fatalf("clone row = %s", marshal(t, row))
	}
	// A retry of the same request answers the row.
	again, err := owner.Call("clone", delta.JsonObjectOf("id", id, "copy", copyID), p2test.Background)
	if err != nil || marshal(t, again) != marshal(t, row) {
		t.Fatalf("retry = %s, %v", marshal(t, again), err)
	}
	if _, err := p2test.Until(owner.Member("operations"), func(value delta.JsonValue) bool { return len(value.([]any)) == 0 }, 0); err != nil {
		t.Fatalf("operations: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(f.dataDir, "sessions", copyID, "files", "blobs", "ab", "blob")); err != nil || string(got) != "blob" {
		t.Fatalf("copied blob = %q, %v", got, err)
	}
	if calls := f.workers.log(); !slices.Equal(calls, []string{"cloning " + id}) {
		t.Fatalf("worker calls = %v", calls)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRenamePinAndDismissAreTheOwners(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	id := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	if _, err := owner.Call("rename", delta.JsonObjectOf("id", id, "title", " A  new\ttitle "), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Call("pin", delta.JsonObjectOf("id", id, "pinned", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	row := f.catalogRow(t, id)
	if row.Value("title") != "A new title" || row.Value("pinned") != true {
		t.Fatalf("row = %s", marshal(t, row))
	}
	for _, title := range []string{"", "   ", strings.Repeat("x", contract.MaxTitle+1)} {
		if _, err := owner.Call("rename", delta.JsonObjectOf("id", id, "title", title), p2test.Background); err == nil {
			t.Errorf("title %q was taken", title)
		}
	}
	// A rename of an unknown session fails; so does a pin.
	if _, err := owner.Call("rename", delta.JsonObjectOf("id", "unknown", "title", "x"), p2test.Background); err == nil || !strings.Contains(err.Error(), "Session not found") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestWorkersRenameConfigureAndPushTheirTree(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	id := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	worker, err := p2test.HoldIn(f.Client(p2runtime.WorkerPrincipal{Session: id, Generation: 1}), contract.Sessions, delta.NewJsonObject(0), "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Release()

	// Its own row.
	if _, err := p2test.Until(worker.Member("session"), func(value delta.JsonValue) bool { return object(value) != nil && object(value).Value("id") == id }, 0); err != nil {
		t.Fatal(err)
	}
	// The agent's rename answers the previous title, collapsing white space.
	answer, err := worker.Call("renameSelf", delta.JsonObjectOf("title", "  Fix   the terminal "), p2test.Background)
	if err != nil || object(answer).Value("previous") != "New session" {
		t.Fatalf("renameSelf = %s, %v", marshal(t, answer), err)
	}
	if f.catalogRow(t, id).Value("title") != "Fix the terminal" {
		t.Fatal("title not renamed")
	}
	// Either field alone updates the entry; the other keeps its value.
	if _, err := worker.Call("configureSelf", delta.JsonObjectOf("thinkingLevel", "low"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	row := f.catalogRow(t, id)
	if row.Value("thinkingLevel") != "low" || marshal(t, row.Value("model")) != marshal(t, f.modelArg()) {
		t.Fatalf("after configureSelf = %s", marshal(t, row))
	}
	// A `/tree` switch is in the catalog before the worker's call answers.
	branches := []any{delta.JsonObjectOf("conversation", float64(1), "label", nil), delta.JsonObjectOf("conversation", float64(3), "label", "side")}
	if _, err := worker.Call("treeSelf", delta.JsonObjectOf("main", float64(3), "branches", branches), p2test.Background); err != nil {
		t.Fatal(err)
	}
	row = f.catalogRow(t, id)
	if row.Value("main") != float64(3) || marshal(t, row.Value("branches")) != `[{"conversation":1,"label":null},{"conversation":3,"label":"side"}]` {
		t.Fatalf("after treeSelf = %s", marshal(t, row))
	}
}

func TestGuestsSeeTheSharedSessionsThatAreNotArchived(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	shared := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	hidden := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	archived := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	if _, err := owner.Call("archive", delta.JsonObjectOf("id", archived), p2test.Background); err != nil {
		t.Fatal(err)
	}
	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: shared, Sessions: map[string]p2runtime.GuestMode{shared: p2runtime.GuestView, archived: p2runtime.GuestView}}
	view, err := p2test.HoldIn(f.Client(guest), contract.Sessions, delta.NewJsonObject(0), "guest")
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	rows, err := p2test.Until(view.Member("sessions"), func(value delta.JsonValue) bool { return len(value.([]any)) == 1 }, 0)
	if err != nil || object(rows.([]any)[0]).Value("id") != shared {
		t.Fatalf("guest sees %v, %v (hidden %s)", rows, err, hidden)
	}
}

func TestWorkersStartDataRefusesArchivedAndUnconfiguredSessions(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	live := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	onEnv := f.create(t, owner, "cwd", "/proj", "env", "box", "model", f.modelArg()).Value("id").(string)
	gone := f.create(t, owner, "model", f.modelArg()).Value("id").(string)
	if _, err := owner.Call("archive", delta.JsonObjectOf("id", gone), p2test.Background); err != nil {
		t.Fatal(err)
	}
	table, err := f.Host.Registry(p2test.Background, p2runtime.WorkerInitValues.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	contribution, ok := table.Value().(*p2runtime.KeyedTable[p2runtime.WorkerInitContribution]).Get("session")
	if !ok {
		t.Fatal("the sessions plugin contributes no `session` start data")
	}

	init, err := contribution(live, p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"` + live + `","cwd":"` + object(init).Value("cwd").(string) + `","env":null,"model":` + marshal(t, f.modelArg()) + `,"thinkingLevel":"high"}`
	if marshal(t, init) != want {
		t.Fatalf("init = %s\nwant %s", marshal(t, init), want)
	}
	if _, err := contribution(gone, p2test.Background); err == nil {
		t.Fatal("an archived session started")
	} else if forbidden := (*data.ChordError)(nil); !asChord(err, &forbidden) || forbidden.Code != data.CodeForbidden {
		t.Fatalf("archived: %v", err)
	}
	if _, err := contribution(onEnv, p2test.Background); err == nil || !strings.Contains(err.Error(), `Environment "box" is not configured`) {
		t.Fatalf("unconfigured env: %v", err)
	}
	if err := f.SetCell(f.configs, delta.JsonObjectOf("box", delta.JsonObjectOf("type", "ssh", "host", "box"))); err != nil {
		t.Fatal(err)
	}
	if _, err := contribution(onEnv, p2test.Background); err != nil {
		t.Fatalf("configured env: %v", err)
	}
	// Archived sessions start only to rebuild their index, then without resuming work.
	allowed, err := f.Host.Get(p2test.Background, contract.ArchivedStarts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Release()
	if _, err := allowed.Call("allow", delta.JsonObjectOf("session", gone), p2test.Background); err != nil {
		t.Fatal(err)
	}
	indexing, err := contribution(gone, p2test.Background)
	if err != nil || object(indexing).Value("resume") != false {
		t.Fatalf("indexing start = %s, %v", marshal(t, indexing), err)
	}
}

func TestAStartForAnUnknownIDWaitsForItsRow(t *testing.T) {
	f := newSessionsFixture(t)
	owner := f.ownerSessions(t)
	table, err := f.Host.Registry(p2test.Background, p2runtime.WorkerInitValues.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	contribution, _ := table.Value().(*p2runtime.KeyedTable[p2runtime.WorkerInitContribution]).Get("session")
	const id = "6d1e8b1f-3e4c-4a6b-9c7d-2b3c4d5e6f70"
	started := make(chan error, 1)
	go func() {
		_, err := contribution(id, p2test.Background)
		started <- err
	}()
	// The page navigated before `create` answered: the start does not fail while the row is not there yet.
	f.create(t, owner, "id", id, "model", f.modelArg())
	if err := <-started; err != nil {
		t.Fatalf("start = %v", err)
	}
}
