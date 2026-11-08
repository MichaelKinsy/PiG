package app

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Test support for StartServer and StartWorker (Pi's test/node/app/support.ts): an app started in a temporary data directory with a
// temporary plugins root, the tests' API (testdata/api/v1.mjs), a worker kind whose entry is this test binary run again
// (TestMain, fixtureEnv "worker"), and the plugins the tests write.

const appFixtureEnv = "CHORD_APP_FIXTURE"

var (
	project = mustHostKind("project", contracts.Prop("id", contracts.Str()))
	team    = mustHostKind("team", contracts.Prop("id", contracts.Str()))

	testServer = delta.JsonObjectOf("kind", "server")
	testOwner  = delta.JsonObjectOf("kind", "owner")
)

func TestMain(m *testing.M) {
	if os.Getenv(appFixtureEnv) == "worker" {
		StartWorker(workerOptions())
		return
	}
	os.Exit(m.Run())
}

// apiModules are the tests' API as the server and the fixture worker have it.
func apiModules() AppAPI {
	path, err := filepath.Abs(filepath.Join("testdata", "api", "v1.mjs"))
	if err != nil {
		panic(err)
	}
	return AppAPI{Name: "@test/app", Versions: []string{"v1"}, Modules: plugins.HostPackages{"@test/app/v1": path}}
}

func testKinds() []AppKind {
	return []AppKind{{Name: "server", Platform: plugins.PlatformNode}, {Name: "worker", Platform: plugins.PlatformNode}, {Name: "teamworker", Platform: plugins.PlatformNode}}
}

// workerOptions are the fixture worker's: StartWorker with the tests' API.
func workerOptions() WorkerOptions {
	return WorkerOptions{
		API: apiModules(), Kinds: testKinds(), WorkerKinds: []*contracts.HostKind{project, team},
		OnError: func(err any) { fmt.Fprintln(os.Stderr, err) },
	}
}

// testWorker is the tests' worker kind: its entry is this test binary.
func testWorker(t testing.TB, kind *contracts.HostKind, facet string) AppWorkerKind {
	t.Helper()
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return AppWorkerKind{
		Kind: kind, Entry: entry, Facet: facet,
		Env: map[string]string{appFixtureEnv: "worker", "GORACE": "atexit_sleep_ms=0"},
		Start: func(address contracts.Address, _ context.Context) (delta.JsonValue, error) {
			return delta.JsonObjectOf(kind.Kind, address.Key), nil
		},
		Authorize: func(data.Principal, contracts.Address) bool { return true },
		IdleMs:    func() time.Duration { return time.Minute },
	}
}

// appErrors collects what an app reports.
type appErrors struct {
	mu   sync.Mutex
	list []AppError
}

func (e *appErrors) add(err AppError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, err)
}

// started is a started test app and what it reported.
type started struct {
	app     *App
	errors  *appErrors
	dataDir string
	root    string
	port    int
}

// testOptions are the options of a test app in base: a temporary data directory and plugins root unless options name them.
func testOptions(t testing.TB, edit func(*AppOptions)) (AppOptions, *appErrors) {
	t.Helper()
	base := t.TempDir()
	errs := &appErrors{}
	options := AppOptions{
		Name: "chord-test", DataDir: filepath.Join(base, "data"), Root: filepath.Join(base, "plugins"),
		Listen: &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0},
		API:    apiModules(), Kinds: testKinds(),
		Principal: AppPrincipals{
			Server: testServer, Owner: testOwner,
			Worker: func(address contracts.Address) data.Principal {
				return delta.JsonObjectOf("kind", "worker", "key", address.Key)
			},
		},
		OnError: errs.add,
	}
	if edit != nil {
		edit(&options)
	}
	if err := os.MkdirAll(options.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	return options, errs
}

// startTestApp starts an app with testOptions; it is closed when the test ends unless a test closed it.
func startTestApp(t testing.TB, plugins map[string]map[string]string, edit func(*AppOptions)) started {
	t.Helper()
	options, errs := testOptions(t, edit)
	for dir, files := range plugins {
		writePlugin(t, options.Root, dir, "@test/"+dir, files)
	}
	app, err := StartServer(options, background)
	if err != nil {
		t.Fatal(err)
	}
	// Registered first, so it runs after the close: no goroutine of the app, its HTTP component, its workers or its plugins outlives it.
	t.Cleanup(func() {
		noGoroutinesOf(t, "chord/nextnode/app.", "chord/nextnode/http.", "chord/nextnode/workers.", "chord/nextnode/plugins.")
	})
	t.Cleanup(func() { _ = app.Close(background) })
	var port int
	_, _ = fmt.Sscanf(app.OwnerLink(), "http://127.0.0.1:%d/", &port)
	return started{app: app, errors: errs, dataDir: options.DataDir, root: options.Root, port: port}
}

// writePlugin writes the plugin dir of root: its package.json and files.
func writePlugin(t testing.TB, root, dir, name string, files map[string]string) {
	t.Helper()
	all := map[string]string{"package.json": fmt.Sprintf(`{"name":%q,"private":true,"type":"module"}`, name)}
	maps.Copy(all, files)
	for file, text := range all {
		path := filepath.Join(root, dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// echoServer is a plugin's server facet serving `Echo` (a local type) through the tests' API.
const echoServer = `import { constant, defineFacet, local, str } from "@test/app/v1";
const Echo = local("test.echo", { version: 1, params: {}, members: { text: str() } });
export default defineFacet((host) => host.serve(Echo, () => ({ text: constant("echo") })));`

var echo = mustLocal("test.echo", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())}})

// notesPlugin is the notes plugin of the worker tests: on each project's worker a persisted document, a table, and its WorkerInfo
// start, reached from the server through the remote `Notes`.
var notesPlugin = map[string]string{
	"worker.ts": `import { defineFacet, defineState, defineTable, hostKind, int, local, method, obj, remote, str, unknown, WorkerInfo } from "@test/app/v1";
const Project = hostKind("project", { id: str() });
const Notes = remote("test.notes", { version: 1, params: { id: str() }, hostAddress: ({ id }) => Project({ id }), variants: { main: { state: { text: str(), start: unknown() }, methods: { write: method(str()), visit: method(str(), int()) } } } });
const Text = defineState("test.text", { schema: obj({ text: str() }), version: 1, initial: () => ({ text: "" }), persist: true, checkpointWhen: () => false });
const Visits = defineTable("test.visits", { version: 1, columns: { page: str(), count: int() }, key: ["page"] });
const Store = local("test.store", { version: 1, params: {}, members: { text: str(), write: method(str()), visit: method(str(), int()) } });
export default defineFacet((host) => {
	host.serve(Store, (_params, self) => {
		const text = self.state(Text);
		const visits = self.table(Visits);
		return {
			text: self.derive([text], ([value]) => value.text),
			write: async (next, ctx) => { await text.change((draft) => { draft.text = next; }, ctx); return null; },
			visit: async (page, ctx) => self.tx(async (tx) => { const count = ((await tx.get(visits, { page }))?.count ?? 0) + 1; tx.put(visits, { page, count }); return count; }, ctx),
		};
	});
	host.serve(Notes, (_params, _principal, self) => {
		const store = self.get(Store, {});
		const info = self.get(WorkerInfo, {});
		return { variant: "main", members: {
			text: self.derive([store.text], ([value]) => value),
			start: self.derive([info.start], ([value]) => value),
			write: (next, ctx) => store.write(next, ctx),
			visit: (page, ctx) => store.visit(page, ctx),
		} };
	});
});`,
}

// notes is the Go form of the notes plugin's remote contract (contracts are data).
var notes = func() *contracts.Contract {
	contract, err := contracts.Remote("test.notes", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return project.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("text", contracts.Str()), contracts.Prop("start", contracts.Unknown())},
			Methods: []contracts.MethodEntry{contracts.Meth("write", contracts.NewMethod(contracts.Str())), contracts.Meth("visit", contracts.NewMethod(contracts.Str(), contracts.Int()))},
		})},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// optionsFor is the options of a test app on dataDir and root.
func optionsFor(t testing.TB, dataDir, root string) AppOptions {
	t.Helper()
	options, _ := testOptions(t, func(o *AppOptions) { o.DataDir, o.Root = dataDir, root })
	return options
}

// dirEntries are the entries of dir, sorted ([] when none).
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	list := dirNames(t, dir)
	if list == nil {
		list = []string{}
	}
	return list
}

// socketDial is whether anything accepts on the loopback port.
func socketDial(port int) (net.Conn, error) {
	return net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
}
